package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Emmanuel93/coyote/internal/ci"
	"github.com/Emmanuel93/coyote/internal/github"
	"github.com/Emmanuel93/coyote/internal/infra"
	"github.com/Emmanuel93/coyote/internal/product"
	"github.com/Emmanuel93/coyote/internal/project"
	"github.com/Emmanuel93/coyote/internal/secrets"
)

// coyote gate pr es la aprobación en equipo sin claves compartidas (D6,
// R17): en el pipeline de un PR calcula el riesgo del cambio por sus rutas y
// por su impacto en el producto, y un cambio R2 o R3 pide la aprobación de un
// dueño (CODEOWNERS de la rama base) antes del merge. Con --policy fail el
// chequeo falla hasta que esa aprobación llega; el workflow vuelve a correr
// con cada revisión.

func gatePR(a *app, args []string) error {
	fs := a.flags("gate pr", "--repo nombre=ruta... [--self nombre] [--risk R3=patrón]... [--policy warn|fail] [--comment]")
	var repos, risks multiFlag
	fs.Var(&repos, "repo", "repo del producto: nombre=ruta; se repite, uno por repo")
	fs.Var(&risks, "risk", "regla de riesgo por rutas del proyecto: R2=patrón o R3=patrón; se repite")
	self := fs.String("self", "", "repo del PR (por defecto, el de GITHUB_REPOSITORY)")
	event := fs.String("event", os.Getenv("GITHUB_EVENT_PATH"), "evento del PR de GitHub Actions")
	base := fs.String("base", "", "commit base; manda sobre el evento")
	head := fs.String("head", "", "commit del PR; manda sobre el evento")
	policy := fs.String("policy", "warn", "warn: reporta el riesgo y a quién le toca revisar; fail: el chequeo falla hasta que aprueba un dueño")
	comment := fs.Bool("comment", false, "crea o actualiza el comentario de coyote en el PR (usa GITHUB_TOKEN)")
	summary := fs.String("summary", os.Getenv("GITHUB_STEP_SUMMARY"), "archivo del resumen del job")
	planFile := fs.String("plan", "", "plan de Terraform en JSON (terraform show -json) que generó el pipeline")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	if *policy != "warn" && *policy != "fail" {
		return fail(2, "--policy %q inválida: warn o fail", *policy)
	}
	rules := append([]ci.Rule(nil), ci.DefaultRules...)
	for _, r := range risks {
		rule, err := ci.ParseRiskFlag(r)
		if err != nil {
			return fail(2, "%v", err)
		}
		rules = append(rules, rule)
	}
	// Con pull_request_target los forks se evalúan como cualquier PR: su
	// código se lee como dato y nunca se ejecuta.
	p, err := a.prAnalysis(repos, *self, *event, *base, *head, true)
	if err != nil {
		return err
	}
	dir := p.src[p.self]
	changed, err := product.ChangedFiles(dir, p.pr.BaseSHA+"..."+p.pr.HeadSHA)
	if err != nil {
		return fail(1, "%v", err)
	}
	in := ci.GateInput{Author: p.pr.Author, HeadSHA: p.pr.HeadSHA, Changed: changed, Files: ci.PathRisks(changed, rules)}
	// R18: lo que el PR agrega se revisa como dato, sin ejecutar nada.
	added, right, err := product.AddedText(dir, p.pr.BaseSHA+"..."+p.pr.HeadSHA)
	if err != nil {
		return fail(1, "%v", err)
	}
	// Las reglas de secretos del repo salen de la rama base: un PR no se
	// dispensa a sí mismo agregando secrets.allow.
	baseRules := secrets.Rules{}
	if text, ok := product.FileAt(dir, p.pr.BaseSHA, project.ConfigPath); ok {
		if cfg, err := project.Parse([]byte(text), p.self); err == nil {
			baseRules = cfg.Secrets
		}
	}
	in.Secrets = scanAdded(added, func(f string) (secrets.Rules, string) { return baseRules, f }, func(f string) (string, bool) { return product.FileAt(dir, right, f) })
	var plan *infra.PlanSummary
	if *planFile != "" {
		f, err := os.Open(*planFile)
		if err != nil {
			return fail(1, "no puedo leer el plan: %v", err)
		}
		plan, err = infra.ReadPlan(f)
		f.Close()
		if err != nil {
			return fail(1, "%v", err)
		}
		in.PlanRisk = plan.Risk
		in.PlanWhy = plan.Headline()
		if r := plan.Reasons(); len(r) > 0 {
			in.PlanWhy += "; " + strings.Join(r, ", ")
		}
	}
	in.Impact, in.ImpactWhy = impactRisk(p.im, p.self)
	seen := map[string]bool{}
	for _, h := range p.im.Touched {
		if h.Entry.Repo == p.self && h.Entry.File != "" && !seen[h.Entry.File] {
			seen[h.Entry.File] = true
			in.ImpactFiles = append(in.ImpactFiles, h.Entry.File)
		}
	}
	// Los dueños salen de la rama base: un PR no se nombra dueño a sí mismo.
	for _, f := range ci.CodeownersFiles {
		if text, ok := product.FileAt(dir, p.pr.BaseSHA, f); ok {
			in.Owners = ci.ParseCodeowners(text)
			in.Owners.File = f
			break
		}
	}
	var notes []string
	if probe := ci.DecideGate(in); probe.Required {
		reviews, member, authors, note := a.prReviews(p.pr)
		in.Reviews, in.Member, in.Excluded = reviews, member, authors
		if note != "" {
			notes = append(notes, note)
		}
	}
	res := ci.DecideGate(in)
	res.Notes = append(notes, res.Notes...)
	report := gateReport(res, in, p, *policy, plan)
	a.publishReport(report, *summary, *comment, p.pr)
	if *policy == "fail" && len(in.Secrets) > 0 {
		return fail(1, "coyote: el PR agrega secretos (R18); sácalos y rótalos")
	}
	if *policy == "fail" && !res.OK {
		return fail(1, "coyote: %s pide la aprobación de un dueño antes del merge (R17)", res.Risk)
	}
	return nil
}

// impactRisk da el riesgo por impacto: romper a un consumidor es R3 y llegar
// a otro repo del producto es R2.
func impactRisk(im *product.Impact, self string) (string, string) {
	if n := breakingCount(im); n > 0 {
		return ci.R3, fmt.Sprintf("rompe %d %s que usan otros módulos", n, pluralWord(n, "interfaz", "interfaces"))
	}
	if n := otherRepos(im, self); n > 0 {
		return ci.R2, fmt.Sprintf("llega a %d %s del producto", n, pluralWord(n, "repo más", "repos más"))
	}
	return ci.R1, ""
}

// prReviews lee las revisiones con el token del job. La membresía de los
// equipos usa COYOTE_TEAMS_TOKEN si está: el token del job no ve los equipos.
func (a *app) prReviews(pr ci.PR) ([]ci.Review, func(team, user string) (bool, error), []string, string) {
	token := os.Getenv("GITHUB_TOKEN")
	repo := pr.Repo
	if repo == "" {
		repo = os.Getenv("GITHUB_REPOSITORY")
	}
	if token == "" || repo == "" || pr.Number == 0 {
		return nil, nil, nil, "sin GITHUB_TOKEN, el repo o el número del PR no leo las revisiones: nadie cuenta como aprobación"
	}
	api := os.Getenv("GITHUB_API_URL")
	c := &github.Client{Base: api, Token: token}
	reviews, err := ci.Reviews(context.Background(), c, repo, pr.Number)
	if err != nil {
		return nil, nil, nil, "no pude leer las revisiones del PR: " + err.Error()
	}
	// Quien escribió commits del PR no lo revisa; si no se pueden leer, no cuenta ninguna aprobación.
	authors, err := ci.CommitAuthors(context.Background(), c, repo, pr.Number)
	if err != nil {
		return nil, nil, nil, "no pude leer los commits del PR para saber quién los escribió: nadie cuenta como aprobación"
	}
	teams := c
	if t := os.Getenv("COYOTE_TEAMS_TOKEN"); t != "" {
		teams = &github.Client{Base: api, Token: t}
	}
	memo := map[string]bool{}
	errs := map[string]error{}
	member := func(team, user string) (bool, error) {
		k := team + "\x00" + user
		if err, ok := errs[k]; ok {
			return false, err
		}
		if v, ok := memo[k]; ok {
			return v, nil
		}
		v, err := ci.TeamMember(context.Background(), teams, team, user)
		if err != nil {
			errs[k] = err
			return false, err
		}
		memo[k] = v
		return v, nil
	}
	return reviews, member, authors, ""
}

// gateReport arma el comentario: el veredicto del gate, el riesgo y quién
// revisa, y después el impacto en el producto.
func gateReport(res ci.GateResult, in ci.GateInput, p *prRun, policy string, plan *infra.PlanSummary) string {
	var b strings.Builder
	b.WriteString(ci.Marker + "\n")
	switch {
	case len(in.Secrets) > 0:
		fmt.Fprintf(&b, "### coyote: %s, el cambio agrega secretos\n\n", res.Risk)
	case !res.Required:
		fmt.Fprintf(&b, "### coyote: %s, sin revisión extra\n\n", res.Risk)
	case res.OK:
		fmt.Fprintf(&b, "### coyote: %s, aprobado por %s\n\n", res.Risk, atList(res.Approvers))
	case len(res.Blockers) > 0:
		fmt.Fprintf(&b, "### coyote: %s, %s %s cambios\n\n", res.Risk, atList(res.Blockers), pluralWord(len(res.Blockers), "pidió", "pidieron"))
	default:
		fmt.Fprintf(&b, "### coyote: %s, espera la aprobación de un dueño\n\n", res.Risk)
	}
	if len(res.Why) > 0 {
		fmt.Fprintf(&b, "**Riesgo %s.** %s.\n\n", res.Risk, strings.Join(res.Why, " · "))
	} else {
		b.WriteString("**Riesgo R1.** Ninguna ruta de riesgo y el cambio no llega a otros repos del producto.\n\n")
	}
	if len(in.Files) > 0 {
		b.WriteString("| Archivo | Riesgo | Por qué |\n|---|---|---|\n")
		for i, f := range in.Files {
			if i == ciMaxRows {
				fmt.Fprintf(&b, "| … | | %d archivos más |\n", len(in.Files)-ciMaxRows)
				break
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s |\n", codeCell(f.Path), f.Risk, mdCell(f.Why))
		}
		b.WriteString("\n")
	}
	if len(in.Secrets) > 0 {
		b.WriteString("**Secretos (R18).** El cambio agrega secretos; nunca se muestra el valor. Sácalos del PR y rótalos: ya están en GitHub.\n\n")
		b.WriteString("| Archivo | Línea | Qué |\n|---|---|---|\n")
		for i, f := range in.Secrets {
			if i == ciMaxRows {
				fmt.Fprintf(&b, "| … | | %d más |\n", len(in.Secrets)-ciMaxRows)
				break
			}
			line := "-"
			if f.Line > 0 {
				line = fmt.Sprint(f.Line)
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s |\n", codeCell(f.Path), line, mdCell(f.Kind))
		}
		b.WriteString("\n")
	}
	if res.Required {
		b.WriteString("**Revisión.** ")
		if policy == "fail" {
			b.WriteString("El merge espera la aprobación de un dueño de lo que cambia")
		} else {
			b.WriteString("Conviene la aprobación de un dueño de lo que cambia (política `warn`: el chequeo no falla)")
		}
		if res.Risk == ci.R3 {
			b.WriteString("; en R3, del último commit")
		}
		b.WriteString(".\n\n")
		for _, g := range res.Groups {
			mark := "pendiente"
			if g.OK {
				mark = "aprobado"
			}
			who := "cualquier persona que no abrió el PR"
			if len(g.Owners) > 0 {
				who = strings.Join(g.Owners, " o ")
			}
			fmt.Fprintf(&b, "- %s: %s (%s)\n", mark, who, fileList(g.Files, 3))
		}
		if len(res.Blockers) > 0 {
			fmt.Fprintf(&b, "- %s: %s\n", pluralWord(len(res.Blockers), "pidió cambios", "pidieron cambios"), atList(res.Blockers))
		}
		for _, n := range res.Notes {
			b.WriteString("- " + n + "\n")
		}
		b.WriteString("\n")
	}
	if plan != nil {
		b.WriteString(planMarkdown(plan))
	}
	fmt.Fprintf(&b, "**Impacto.** %s.\n\n", capFirst(impactVerdict(p.im, p.self)))
	b.WriteString(impactBody(p.im, p.m, len(p.sources), policy))
	return finishReport(&b, "coyote gate pr", p.pr, len(p.sources))
}

func atList(users []string) string {
	out := make([]string, len(users))
	for i, u := range users {
		out[i] = "@" + u
	}
	return strings.Join(out, ", ")
}

func fileList(files []string, n int) string {
	shown := files
	if len(shown) > n {
		shown = shown[:n]
	}
	parts := make([]string, len(shown))
	for i, f := range shown {
		parts[i] = "`" + codeCell(f) + "`"
	}
	out := strings.Join(parts, ", ")
	if len(files) > n {
		out += fmt.Sprintf(" y %d más", len(files)-n)
	}
	return out
}

// codeCell deja una ruta apta para un bloque de código en una celda: sin
// acentos graves que lo cierren ni barras que partan la tabla.
func codeCell(s string) string {
	return strings.NewReplacer("`", "'", "|", "¦", "\n", " ", "\r", " ").Replace(s)
}
