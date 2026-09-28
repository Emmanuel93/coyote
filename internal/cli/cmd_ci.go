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
	sources, err := ciSources(repos)
	if err != nil {
		return err
	}
	var pr ci.PR
	if *event != "" {
		if pr, err = ci.ReadPR(*event); err != nil {
			return fail(1, "%v", err)
		}
	}
	if *base != "" {
		pr.BaseSHA = *base
	}
	if *head != "" {
		pr.HeadSHA = *head
	}
	if pr.BaseSHA == "" || pr.HeadSHA == "" {
		return fail(2, "faltan los commits del PR: corre dentro de GitHub Actions (pull_request) o pasa --base y --head")
	}
	for _, s := range []string{pr.BaseSHA, pr.HeadSHA} {
		if strings.HasPrefix(s, "-") || strings.ContainsAny(s, " .:\n") {
			return fail(2, "commit inválido %q", s)
		}
	}
	if pr.FromFork() {
		fmt.Fprintln(a.stdout, "coyote: el PR viene de un fork; el pipeline de impacto no corre con código de forks")
		return nil
	}
	selfName := *self
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
		return fail(2, "el repo del PR %q no está entre los --repo (%s)", selfName, strings.Join(sortedSourceNames(sources), ", "))
	}
	scans, err := a.scanAll(sources)
	if err != nil {
		return err
	}
	m := product.Build(scans)
	rng := pr.BaseSHA + "..." + pr.HeadSHA
	im, err := m.Impact(product.Query{Changes: []product.Change{{Repo: selfName, Diff: rng}}}, src)
	if err != nil {
		return fail(1, "%v", err)
	}
	report := ciReport(im, m, len(sources), pr, selfName, *policy)
	fmt.Fprint(a.stdout, report)
	if err := ci.AppendSummary(*summary, report); err != nil {
		fmt.Fprintf(a.stderr, "aviso: no pude escribir el resumen del job: %v\n", err)
	}
	if *comment {
		if err := ciComment(pr, report); err != nil {
			fmt.Fprintf(a.stderr, "aviso: no pude comentar en el PR: %v\n", err)
		}
	}
	if n := breakingCount(im); *policy == "fail" && n > 0 {
		return fail(1, "coyote: el cambio rompe %d %s de otros módulos (política fail)", n, pluralWord(n, "interfaz", "interfaces"))
	}
	return nil
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
	n := len(im.Touched) + len(im.Direct) + len(im.Indirect)
	others := 0
	for _, r := range im.Repos {
		if r != self {
			others++
		}
	}
	br := breakingCount(im)
	switch {
	case n == 0:
		b.WriteString("### coyote: sin cambios de contrato\n\nEl PR no toca endpoints ni tópicos del producto.\n")
	case br > 0:
		fmt.Fprintf(&b, "### coyote: el cambio rompe %d %s\n\n", br, pluralWord(br, "interfaz", "interfaces"))
	case others > 0:
		fmt.Fprintf(&b, "### coyote: el cambio llega a %d %s\n\n", others, pluralWord(others, "repo más", "repos más"))
	default:
		b.WriteString("### coyote: el cambio queda dentro de este repo\n\n")
	}
	if n > 0 {
		fmt.Fprintf(&b, "%d %s · %d %s · %d a través de un BFF · política `%s`\n\n",
			len(im.Touched), pluralWord(len(im.Touched), "interfaz tocada", "interfaces tocadas"),
			len(im.Direct), pluralWord(len(im.Direct), "consumidor directo", "consumidores directos"), len(im.Indirect), policy)
		b.WriteString(clipSections(impactMarkdown(im, m, total)))
	}
	fmt.Fprintf(&b, "\n<sub>coyote impact · %s...%s · el mapa sale del código de los %d repos, sin modelo</sub>\n", short(pr.BaseSHA), short(pr.HeadSHA), total)
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
