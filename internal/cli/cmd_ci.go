package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Emmanuel93/coyote/internal/ci"
	"github.com/Emmanuel93/coyote/internal/github"
	"github.com/Emmanuel93/coyote/internal/product"
)

// coyote ci corre dentro del pipeline de un repo del producto (ADR-0013). No
// necesita un proyecto coyote: recibe las carpetas de los repos, lee el evento
// del PR y reporta. No escribe en los repos ni en el ledger.

func cmdCI(a *app, args []string) error {
	if len(args) == 0 || args[0] != "impact" {
		return fail(2, "uso: coyote ci impact --repo nombre=ruta... [--self nombre] [--policy warn|fail] [--comment]")
	}
	fs := a.flags("ci impact", "--repo nombre=ruta... [--self nombre] [--base SHA --head SHA] [--policy warn|fail] [--comment]")
	var repos multiFlag
	fs.Var(&repos, "repo", "repo del producto: nombre=ruta; se repite, uno por repo")
	self := fs.String("self", "", "repo del PR (por defecto, el de GITHUB_REPOSITORY)")
	event := fs.String("event", os.Getenv("GITHUB_EVENT_PATH"), "evento del PR de GitHub Actions")
	base := fs.String("base", "", "commit base; manda sobre el evento")
	head := fs.String("head", "", "commit del PR; manda sobre el evento")
	policy := fs.String("policy", "warn", "warn: solo reporta; fail: el chequeo falla si el cambio rompe a otro módulo")
	comment := fs.Bool("comment", false, "crea o actualiza el comentario de coyote en el PR (usa GITHUB_TOKEN)")
	summary := fs.String("summary", os.Getenv("GITHUB_STEP_SUMMARY"), "archivo del resumen del job")
	if _, err := parseArgs(fs, args[1:]); err != nil {
		return err
	}
	if *policy != "warn" && *policy != "fail" {
		return fail(2, "--policy %q inválida: warn o fail", *policy)
	}
	p, err := a.prAnalysis(repos, *self, *event, *base, *head, false)
	if err != nil {
		return err
	}
	if p == nil {
		// Un fork no se analiza con pull_request: con fail el chequeo no puede
		// quedar verde por omisión (coyote gate pr, con pull_request_target, sí lo evalúa).
		if *policy == "fail" {
			return fail(1, "coyote: el PR viene de un fork y ci impact no lo evalúa; con la política fail no pasa (usa coyote gate pr)")
		}
		return nil
	}
	report := ciReport(p.im, p.m, len(p.sources), p.pr, p.self, *policy)
	a.publishReport(report, *summary, *comment, p.pr)
	if n := breakingCount(p.im); *policy == "fail" && n > 0 {
		return fail(1, "coyote: el cambio rompe %d %s de otros módulos (política fail)", n, pluralWord(n, "interfaz", "interfaces"))
	}
	return nil
}

// prRun es el análisis de un PR: sus repos, el evento, el mapa y el impacto.
type prRun struct {
	sources []product.Source
	src     product.Sources
	pr      ci.PR
	self    string
	m       *product.Map
	im      *product.Impact
}

// prAnalysis lee el evento, arma el mapa con los repos y calcula el impacto
// del diff del PR. Sin allowFork, devuelve nil, sin error, si el PR viene de
// un fork.
func (a *app) prAnalysis(repos []string, self, event, base, head string, allowFork bool) (*prRun, error) {
	sources, err := ciSources(repos)
	if err != nil {
		return nil, err
	}
	var pr ci.PR
	if event != "" {
		if pr, err = ci.ReadPR(event); err != nil {
			return nil, fail(1, "%v", err)
		}
	}
	if base != "" {
		pr.BaseSHA = base
	}
	if head != "" {
		pr.HeadSHA = head
	}
	if pr.BaseSHA == "" || pr.HeadSHA == "" {
		return nil, fail(2, "faltan los commits del PR: corre dentro de GitHub Actions (pull_request) o pasa --base y --head")
	}
	for _, s := range []string{pr.BaseSHA, pr.HeadSHA} {
		if strings.HasPrefix(s, "-") || strings.ContainsAny(s, " .:\n") {
			return nil, fail(2, "commit inválido %q", s)
		}
	}
	if pr.FromFork() && !allowFork {
		fmt.Fprintln(a.stdout, "coyote: el PR viene de un fork; ci impact no corre con código de forks")
		return nil, nil
	}
	selfName := self
	if selfName == "" {
		if gh := os.Getenv("GITHUB_REPOSITORY"); gh != "" {
			selfName = gh[strings.LastIndex(gh, "/")+1:]
		}
	}
	src := product.Sources{}
	for _, s := range sources {
		src[s.Name] = s.Dir
	}
	if _, ok := src[selfName]; !ok {
		return nil, fail(2, "el repo del PR %q no está entre los --repo (%s)", selfName, strings.Join(sortedSourceNames(sources), ", "))
	}
	scans, err := a.scanAll(sources)
	if err != nil {
		return nil, err
	}
	m := product.Build(scans)
	rng := pr.BaseSHA + "..." + pr.HeadSHA
	im, err := m.Impact(product.Query{Changes: []product.Change{{Repo: selfName, Diff: rng}}}, src)
	if err != nil {
		return nil, fail(1, "%v", err)
	}
	return &prRun{sources: sources, src: src, pr: pr, self: selfName, m: m, im: im}, nil
}

// publishReport deja el reporte en la salida, en el resumen del job y, si se
// pide, en el comentario del PR.
func (a *app) publishReport(report, summary string, comment bool, pr ci.PR) {
	fmt.Fprint(a.stdout, report)
	if err := ci.AppendSummary(summary, report); err != nil {
		fmt.Fprintf(a.stderr, "aviso: no pude escribir el resumen del job: %v\n", err)
	}
	if comment {
		if err := ciComment(pr, report); err != nil {
			fmt.Fprintf(a.stderr, "aviso: no pude comentar en el PR: %v\n", err)
		}
	}
}

// ciSources lee los --repo nombre=ruta.
func ciSources(repos []string) ([]product.Source, error) {
	if len(repos) == 0 {
		return nil, fail(2, "indica los repos del producto con --repo nombre=ruta, uno por repo")
	}
	seen := map[string]bool{}
	var out []product.Source
	for _, r := range repos {
		name, dir, ok := strings.Cut(r, "=")
		name, dir = strings.TrimSpace(name), strings.TrimSpace(dir)
		if !ok || name == "" || dir == "" {
			return nil, fail(2, "--repo %q: usa nombre=ruta", r)
		}
		if !repoNameOK(name) {
			return nil, fail(2, "--repo %q: nombre inválido", r)
		}
		if seen[name] {
			return nil, fail(2, "--repo %q repetido", name)
		}
		seen[name] = true
		abs, err := filepath.Abs(dir)
		if err != nil {
			return nil, err
		}
		if info, err := os.Stat(abs); err != nil || !info.IsDir() {
			return nil, fail(1, "%s: no encuentro la carpeta %s", name, dir)
		}
		out = append(out, product.Source{Name: name, Dir: abs})
	}
	return out, nil
}

func repoNameOK(s string) bool {
	for i, r := range s {
		ok := r == '.' || r == '_' || r == '-' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !ok || (i == 0 && (r == '.' || r == '-' || r == '_')) {
			return false
		}
	}
	return s != ""
}

func sortedSourceNames(ss []product.Source) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.Name
	}
	sort.Strings(out)
	return out
}

// ciMaxRows acota las filas de cada sección en el comentario.
const ciMaxRows = 30

// ciReport arma el reporte del PR: el veredicto primero y el detalle después.
func ciReport(im *product.Impact, m *product.Map, total int, pr ci.PR, self, policy string) string {
	var b strings.Builder
	b.WriteString(ci.Marker + "\n")
	b.WriteString("### coyote: " + impactVerdict(im, self) + "\n\n")
	b.WriteString(impactBody(im, m, total, policy))
	return finishReport(&b, "coyote impact", pr, total)
}

// otherRepos cuenta los repos, además del del PR, a los que llega el cambio.
func otherRepos(im *product.Impact, self string) int {
	n := 0
	for _, r := range im.Repos {
		if r != self {
			n++
		}
	}
	return n
}

// impactVerdict es el titular del impacto.
func impactVerdict(im *product.Impact, self string) string {
	n := len(im.Touched) + len(im.Direct) + len(im.Indirect)
	br, others := breakingCount(im), otherRepos(im, self)
	switch {
	case n == 0:
		return "sin cambios de contrato"
	case br > 0:
		return fmt.Sprintf("el cambio rompe %d %s", br, pluralWord(br, "interfaz", "interfaces"))
	case others > 0:
		return fmt.Sprintf("el cambio llega a %d %s", others, pluralWord(others, "repo más", "repos más"))
	}
	return "el cambio queda dentro de este repo"
}

// impactBody son los conteos y las tablas del impacto, recortadas.
func impactBody(im *product.Impact, m *product.Map, total int, policy string) string {
	n := len(im.Touched) + len(im.Direct) + len(im.Indirect)
	if n == 0 {
		return "El PR no toca endpoints ni tópicos del producto.\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d %s · %d %s · %d a través de un BFF · política `%s`\n\n",
		len(im.Touched), pluralWord(len(im.Touched), "interfaz tocada", "interfaces tocadas"),
		len(im.Direct), pluralWord(len(im.Direct), "consumidor directo", "consumidores directos"), len(im.Indirect), policy)
	b.WriteString(clipSections(impactMarkdown(im, m, total)))
	return b.String()
}

// finishReport cierra el reporte con su pie, acorta los commits y lo recorta
// al tamaño de un comentario.
func finishReport(b *strings.Builder, what string, pr ci.PR, total int) string {
	fmt.Fprintf(b, "\n<sub>%s · %s...%s · el mapa sale del código de los %d repos, sin modelo</sub>\n", what, short(pr.BaseSHA), short(pr.HeadSHA), total)
	out := strings.NewReplacer(pr.BaseSHA, short(pr.BaseSHA), pr.HeadSHA, short(pr.HeadSHA)).Replace(b.String())
	return ci.Clip(out, ci.RunURL())
}

// clipSections deja las primeras filas de cada tabla del reporte.
func clipSections(md string) string {
	var out []string
	rows := 0
	for _, l := range strings.Split(md, "\n") {
		switch {
		case strings.HasPrefix(l, "#### "):
			rows = 0
		case strings.HasPrefix(l, "| ") && !strings.HasPrefix(l, "| Repo |"):
			rows++
			if rows == ciMaxRows+1 {
				out = append(out, "| … | | más filas en el resumen del job | | |")
			}
			if rows > ciMaxRows {
				continue
			}
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// ciComment deja el reporte en el PR con el token del job.
func ciComment(pr ci.PR, report string) error {
	token := os.Getenv("GITHUB_TOKEN")
	repo := pr.Repo
	if repo == "" {
		repo = os.Getenv("GITHUB_REPOSITORY")
	}
	if token == "" || repo == "" || pr.Number == 0 {
		return fmt.Errorf("faltan GITHUB_TOKEN, el repo o el número del PR")
	}
	c := &github.Client{Base: os.Getenv("GITHUB_API_URL"), Token: token}
	url, created, err := ci.UpsertComment(context.Background(), c, repo, pr.Number, report)
	if err != nil {
		return err
	}
	verb := "actualizado"
	if created {
		verb = "creado"
	}
	fmt.Fprintf(os.Stderr, "coyote: comentario %s en %s\n", verb, url)
	return nil
}
