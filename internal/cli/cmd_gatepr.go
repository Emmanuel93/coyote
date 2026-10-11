package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/Emmanuel93/coyote/internal/ci"
	"github.com/Emmanuel93/coyote/internal/fsx"
	"github.com/Emmanuel93/coyote/internal/github"
	"github.com/Emmanuel93/coyote/internal/infra"
	"github.com/Emmanuel93/coyote/internal/inject"
	"github.com/Emmanuel93/coyote/internal/product"
	"github.com/Emmanuel93/coyote/internal/project"
	"github.com/Emmanuel93/coyote/internal/safetext"
	"github.com/Emmanuel93/coyote/internal/secrets"
	"github.com/Emmanuel93/coyote/internal/slo"
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
	leaksFile := fs.String("gitleaks", "", "reporte JSON de gitleaks sobre los commits del PR (ADR-0021)")
	leaksNet := fs.String("gitleaks-net", "", "reporte JSON de gitleaks sobre el diff neto del PR")
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
	// Un SLO relajado es R3 aunque su ruta sea R2 (ADR-0020): se compara el
	// archivo de la base con el del PR, como dato.
	in.Files = append(in.Files, sloRisks(dir, p.pr.BaseSHA, p.pr.HeadSHA, changed)...)
	// R18: lo que el PR agrega se revisa como dato, sin ejecutar nada.
	added, right, err := product.AddedText(dir, p.pr.BaseSHA+"..."+p.pr.HeadSHA)
	if err != nil {
		return fail(1, "%v", err)
	}
	// Las reglas de secretos salen de la rama base: un PR no se dispensa a sí
	// mismo agregando secrets.allow. Cada ruta lleva las del proyecto coyote
	// que la contiene, también en una subcarpeta.
	br := &baseRules{dir: dir, base: p.pr.BaseSHA, name: p.self, cache: map[string]*layer{}}
	in.Secrets = scanAdded(added, br.For, func(f string) (string, bool) { return product.FileAt(dir, right, f) })
	if br.err != nil {
		// Sin las reglas de la base, un secreto declarado en secrets.files
		// pasaría: el chequeo falla en vez de seguir con las de siempre.
		return fail(2, "coyote gate pr: %v", br.err)
	}
	// ADR-0023: instrucciones de agentes con cualquier mayúscula, órdenes para
	// un agente en lo que el PR agrega a documentos y un ledger reescrito.
	left, _, err := product.RangeSides(dir, p.pr.BaseSHA+"..."+p.pr.HeadSHA)
	if err != nil {
		return fail(1, "%v", err)
	}
	in.Files = append(in.Files, agentRisks(changed, in.Files)...)
	in.Files = append(in.Files, injectionRisks(added, func(f string) (string, bool) { return product.FileAt(dir, right, f) })...)
	in.Files = append(in.Files, ledgerRisks(dir, left, right, changed)...)
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
	if *leaksFile != "" || *leaksNet != "" {
		// Sin reporte no hay revisión: el paso de gitleaks falló o no corrió.
		read := func(p string) ([]ci.Leak, int, error) {
			if p == "" {
				return nil, 0, nil
			}
			data, err := fsx.ReadCapped(p, 64<<20)
			if err != nil {
				return nil, 0, fmt.Errorf("no puedo leer el reporte de gitleaks: %v", err)
			}
			return ci.ReadGitleaks(bytes.NewReader(data))
		}
		hist, histTotal, err := read(*leaksFile)
		if err != nil {
			return fail(1, "%v", err)
		}
		net, _, err := read(*leaksNet)
		if err != nil {
			return fail(1, "%v", err)
		}
		// El diff neto repite lo que ya vio el escaneo de los commits; solo
		// suma lo que llegó por otro camino (un merge, un rename).
		inHist := map[string]bool{}
		for _, l := range hist {
			inHist[l.File+"\x00"+l.Rule] = true
		}
		in.Leaks, in.LeaksTotal = hist, histTotal
		for _, l := range net {
			if !inHist[l.File+"\x00"+l.Rule] {
				l.Commit = "diff del PR"
				in.Leaks = append(in.Leaks, l)
				in.LeaksTotal++
			}
		}
		seenLeak := map[string]bool{}
		for _, l := range in.Leaks {
			if !seenLeak[l.File] {
				seenLeak[l.File] = true
				in.Files = append(in.Files, ci.FileRisk{Path: l.File, Risk: ci.R3, Why: "gitleaks encontró un posible secreto"})
			}
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
		why := make([]string, len(res.Why))
		for i, w := range res.Why {
			why[i] = mdText(w)
		}
		fmt.Fprintf(&b, "**Riesgo %s.** %s.\n\n", res.Risk, strings.Join(why, " · "))
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
			fmt.Fprintf(&b, "| `%s` | %s | %s |\n", codeCell(f.Path), f.Risk, mdText(f.Why))
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
	if n := max(in.LeaksTotal, len(in.Leaks)); n > 0 {
		fmt.Fprintf(&b, "**gitleaks.** %s en el PR; nunca se muestra el valor. Si es un secreto, sácalo y rótalo: ya está en GitHub. Si es un falso positivo, lo aprueba un dueño y se agrega a `.gitleaksignore` en la rama base, sin commit (`archivo:regla:línea`), en su propio PR.\n\n", capFirst(plural(n, "hallazgo", "hallazgos")))
		b.WriteString("| Archivo | Línea | Regla | Commit |\n|---|---|---|---|\n")
		for i, l := range in.Leaks {
			if i == ciMaxRows {
				fmt.Fprintf(&b, "| … | | | %d más |\n", n-ciMaxRows)
				break
			}
			fmt.Fprintf(&b, "| `%s` | %d | %s | `%s` |\n", codeCell(l.File), l.Line, mdCell(l.Rule), codeCell(l.Commit))
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
	return strings.NewReplacer("`", "'", "|", "¦", "\n", " ", "\r", " ").Replace(safetext.String(s))
}

// baseRules da, para cada ruta del repo, las reglas de secretos del proyecto
// coyote que la contiene en la revisión base, con la ruta relativa a ese
// proyecto; sin proyecto, las de siempre. Un project.yaml de la base que no
// se puede leer (pide un coyote más nuevo, o está roto) queda en err.
type baseRules struct {
	dir, base, name string
	cache           map[string]*layer
	err             error
}

func (b *baseRules) For(f string) (secrets.Rules, string) {
	// Los proyectos que contienen f, del más cercano al de afuera, como en
	// secrets scan: un proyecto anidado no tapa los secretos que declara el
	// de afuera, una dispensa vale solo si la dan todos y uno dentro de
	// dependencias solo suma archivos de secretos (mergeRules).
	var layers []layer
	for d := path.Dir(f); ; d = path.Dir(d) {
		prefix := ""
		if d != "." && d != "/" {
			prefix = d + "/"
		}
		l, seen := b.cache[prefix]
		if !seen {
			if text, ok := product.FileAt(b.dir, b.base, prefix+project.ConfigPath); ok {
				l = &layer{dep: dependency("/", "/"+prefix)}
				if cfg, err := project.Parse([]byte(text), b.name); err == nil {
					l.rules = cfg.Secrets
				} else if b.err == nil && !l.dep {
					b.err = fmt.Errorf("%s%s de la rama base: %v", prefix, project.ConfigPath, err)
				}
			}
			b.cache[prefix] = l
		}
		if l != nil {
			here := *l
			here.rel = strings.TrimPrefix(f, prefix)
			layers = append(layers, here)
		}
		if prefix == "" {
			break
		}
	}
	return mergeRules(f, layers)
}

// newDuplicate dice si un servicio repetido pesa en este PR: x.yaml y x.yml
// en la misma carpeta, siempre; en otra carpeta (dos proyectos de un
// monorepo), cuando el PR agrega uno de los archivos.
func newDuplicate(dir, base, f string, hadOld bool, paths []string) bool {
	for _, p := range paths {
		if p == f {
			continue
		}
		if path.Dir(p) == path.Dir(f) {
			return true
		}
		if _, ok := product.FileAt(dir, base, p); !ok {
			return true
		}
	}
	return !hadOld
}

// sloRisks compara cada archivo de SLOs que toca el PR con el de la base y
// revisa que las reglas generadas del commit del PR salgan de su SLO.
func sloRisks(dir, base, head string, changed []string) []ci.FileRisk {
	var out []ci.FileRisk
	add := func(p, risk, why string) { out = append(out, ci.FileRisk{Path: p, Risk: risk, Why: why}) }
	// Un servicio declarado en dos archivos (x.yaml y x.yml, o en otra
	// carpeta) genera reglas con los mismos nombres: al cargarlas, unas pisan
	// a las otras. Se busca en todo el árbol del PR.
	var dups map[string][]string
	for _, f := range changed {
		if slo.IsSpecPath(f) {
			specs, err := product.ListFiles(dir, head, slo.IsSpecPath)
			if err != nil {
				add(f, ci.R3, "no pude listar los archivos de SLOs del PR para ver si un servicio se declara dos veces")
			}
			dups = slo.Duplicates(specs)
			break
		}
	}
	checked := map[string]bool{}
	for _, f := range changed {
		switch {
		case slo.IsRulesPath(f):
		case slo.InRulesDir(f):
			// Otro archivo en la carpeta de reglas: un cargador que recorre la
			// carpeta lo cargaría sin que salga de ningún SLO.
			add(f, ci.R3, "archivo en la carpeta de reglas generadas que no sale de ningún SLO")
			continue
		case slo.IsSpecPath(f):
			oldText, hadOld := product.FileAt(dir, base, f)
			newText, hasNew := product.FileAt(dir, head, f)
			for _, c := range slo.CompareFile(f, oldText, hadOld, newText, hasNew) {
				add(f, c.Risk, c.Why)
			}
			if ps := dups[slo.ServiceOf(f)]; hasNew && len(ps) > 1 && newDuplicate(dir, base, f, hadOld, ps) {
				add(f, ci.R3, fmt.Sprintf("el servicio %s está en más de un archivo de SLOs (%s): sus reglas se llaman igual y al cargarlas unas pisan a las otras",
					slo.ServiceOf(f), strings.Join(ps, ", ")))
			}
		default:
			continue
		}
		specs, rules := slo.Pair(f)
		if checked[rules] {
			continue
		}
		checked[rules] = true
		raw, hasRules := product.BlobAt(dir, head, rules, 16<<20)
		// Las reglas tienen que salir de cada archivo de SLOs que las genera.
		found := false
		for _, sp := range specs {
			text, ok := product.FileAt(dir, head, sp)
			if !ok {
				continue
			}
			found = true
			if c, bad := slo.CheckGenerated(text, true, raw, hasRules); bad {
				p := rules
				if !hasRules {
					p = sp
				}
				add(p, c.Risk, c.Why)
			}
		}
		if !found {
			if c, bad := slo.CheckGenerated("", false, raw, hasRules); bad {
				add(rules, c.Risk, c.Why)
			}
		}
	}
	return out
}

// agentRisks suma como R2 los archivos de instrucciones de agentes que las
// reglas por ruta no vieron por sus mayúsculas: en un disco que no las
// distingue, agents.md es el AGENTS.md que lee el IDE (ADR-0023).
func agentRisks(changed []string, have []ci.FileRisk) []ci.FileRisk {
	seen := map[string]bool{}
	for _, f := range have {
		if f.Why == ci.WhyAgentInstructions {
			seen[f.Path] = true
		}
	}
	var out []ci.FileRisk
	for _, f := range changed {
		if inject.InstructionFile(f) && !seen[f] {
			out = append(out, ci.FileRisk{Path: f, Risk: ci.R2, Why: ci.WhyAgentInstructions})
		}
	}
	return out
}

// injectionRisks marca como R2 un documento o una instrucción de agentes al
// que el PR le agrega texto con forma de orden para un agente. Las líneas
// seguidas se leen juntas: una orden partida en dos líneas también cuenta.
func injectionRisks(added []product.AddedFile, whole func(string) (string, bool)) []ci.FileRisk {
	var out []ci.FileRisk
	for _, f := range added {
		if !inject.Document(f.Path) {
			// En el código solo se buscan caracteres ocultos: los que
			// reordenan el texto (Trojan Source) son R3; los demás, R2.
			if risk := hiddenRisk(f); risk != nil {
				out = append(out, *risk)
			}
			continue
		}
		type run struct {
			first int
			text  string
		}
		var runs []run
		if f.Whole {
			if text, ok := whole(f.Path); ok {
				runs = append(runs, run{1, text})
			}
		}
		for i, l := range f.Lines {
			if i > 0 && l.Line == f.Lines[i-1].Line+1 {
				runs[len(runs)-1].text += "\n" + l.Text
				continue
			}
			runs = append(runs, run{l.Line, l.Text})
		}
		var first inject.Finding
		n := 0
		for _, r := range runs {
			for _, x := range inject.Scan(r.text) {
				if n == 0 {
					first = x
					first.Line += r.first - 1
				}
				n++
			}
		}
		if n > 0 {
			why := fmt.Sprintf("posible orden para un agente en la línea %d (%s)", first.Line, first.Rule)
			if n > 1 {
				why += fmt.Sprintf(" y %d marcas más", n-1)
			}
			out = append(out, ci.FileRisk{Path: f.Path, Risk: ci.R2, Why: why})
		}
	}
	return out
}

// ledgerRisks marca como R3 un PR que cambia o borra líneas del ledger: el
// ledger solo agrega (docs/specs/ccf-v1.md). Se compara contra el merge-base,
// así lo que la base agregó después no cuenta. La comparación es de bytes:
// recodificar el archivo (UTF-16, CRLF) lo deja ilegible para el ledger
// aunque el texto se vea igual.
func ledgerRisks(dir, left, right string, changed []string) []ci.FileRisk {
	var out []ci.FileRisk
	r3 := func(f, why string) { out = append(out, ci.FileRisk{Path: f, Risk: ci.R3, Why: why}) }
	for _, f := range changed {
		if !strings.HasPrefix(f, "coyote/ledger/") && !strings.Contains(f, "/coyote/ledger/") {
			continue
		}
		oldMode, _, hadOld := product.TreeEntry(dir, left, f)
		newMode, newType, hasNew := product.TreeEntry(dir, right, f)
		switch {
		case hadOld && !hasNew:
			r3(f, "borra un archivo del ledger; el ledger solo agrega")
			continue
		case !hasNew:
			continue
		case newType != "blob" || !regularMode(newMode):
			r3(f, "el ledger tiene que ser un archivo: un symlink, un submódulo o una carpeta lo esconden")
			continue
		case hadOld && !regularMode(oldMode):
			r3(f, "cambia el tipo de un archivo del ledger")
			continue
		}
		nw, ok := product.BlobAt(dir, right, f, fsx.MaxText)
		switch {
		case !ok:
			r3(f, "un archivo del ledger que no se pudo leer entero")
			continue
		case !utf8.Valid(nw) || bytes.HasPrefix(nw, []byte("\xef\xbb\xbf")):
			r3(f, "el ledger tiene que ser UTF-8 sin BOM; recodificado, coyote no lo lee")
			continue
		case !hadOld:
			continue // archivo nuevo: solo agrega
		}
		old, ok := product.BlobAt(dir, left, f, fsx.MaxText)
		switch {
		case !ok:
			r3(f, "un archivo del ledger de la base que no se pudo leer entero")
		case !appendOnly(old, nw):
			r3(f, "cambia o borra líneas del ledger; el ledger solo agrega")
		}
	}
	return out
}

// regularMode dice si un modo de git es el de un archivo normal.
func regularMode(m string) bool { return m == "100644" || m == "100755" }

// appendOnly dice si nw solo agrega a old. Un archivo sin salto final sigue
// valiendo: coyote le agrega el salto antes de la línea nueva.
func appendOnly(old, nw []byte) bool { return bytes.HasPrefix(nw, old) }

// hiddenRisk revisa las líneas que un PR agrega a un archivo que no es un
// documento: caracteres invisibles o que reordenan lo que ve quien revisa.
func hiddenRisk(f product.AddedFile) *ci.FileRisk {
	var first *ci.FileRisk
	for _, l := range f.Lines {
		for _, h := range inject.Hidden(l.Text) {
			if inject.Reorders(h) {
				return &ci.FileRisk{Path: f.Path, Risk: ci.R3, Why: fmt.Sprintf("controles de dirección del texto en la línea %d: lo que se revisa no es lo que corre (Trojan Source)", l.Line)}
			}
			if first == nil {
				first = &ci.FileRisk{Path: f.Path, Risk: ci.R2, Why: fmt.Sprintf("%s en la línea %d", h.Label, l.Line)}
			}
		}
	}
	return first
}
