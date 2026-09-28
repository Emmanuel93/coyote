package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Emmanuel93/coyote/internal/ccf"
	"github.com/Emmanuel93/coyote/internal/ccfdoc"
	"github.com/Emmanuel93/coyote/internal/fsx"
	"github.com/Emmanuel93/coyote/internal/identity"
	"github.com/Emmanuel93/coyote/internal/ledger"
	"github.com/Emmanuel93/coyote/internal/product"
	"github.com/Emmanuel93/coyote/internal/project"
)

// Producto multi-repo (ADR-0011): el mapa de interfaces sale del código de
// los repos, que se leen sin modificarlos; lo que coyote escribe queda en el
// proyecto del producto.
const (
	mapDir   = "coyote/map"
	reposDir = "coyote/repos"
)

// productSources resuelve los repos del producto a sus copias locales. Los
// repos sin copia local se devuelven aparte: de ellos solo sirve el mapa
// guardado.
func productSources(root string, cfg *project.Config, only []string) ([]product.Source, []string, error) {
	want := map[string]bool{}
	for _, n := range only {
		want[n] = true
	}
	var out []product.Source
	var noPath []string
	for _, r := range cfg.Repos {
		if len(only) > 0 && !want[r.Name] {
			continue
		}
		delete(want, r.Name)
		if r.Path == "" {
			noPath = append(noPath, r.Name)
			continue
		}
		dir := r.Path
		if strings.HasPrefix(dir, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				dir = filepath.Join(home, dir[2:])
			}
		}
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(root, filepath.FromSlash(dir))
		}
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			return nil, nil, fail(1, "%s: no encuentro su copia local %s", r.Name, dir)
		}
		out = append(out, product.Source{Name: r.Name, Dir: dir})
	}
	for n := range want {
		return nil, nil, fail(1, "no conozco el repo %q: regístralo con coyote repo add %s --path <ruta>", n, n)
	}
	return out, noPath, nil
}

// scanAll lee los repos en paralelo y cuenta lo encontrado en stderr.
func (a *app) scanAll(sources []product.Source) ([]*product.Scan, error) {
	scans := make([]*product.Scan, len(sources))
	errs := make([]error, len(sources))
	took := make([]time.Duration, len(sources))
	var wg sync.WaitGroup
	for i, s := range sources {
		wg.Add(1)
		go func(i int, s product.Source) {
			defer wg.Done()
			start := time.Now()
			scans[i], errs[i] = product.Extract(s)
			took[i] = time.Since(start)
		}(i, s)
	}
	wg.Wait()
	for i, s := range sources {
		if errs[i] != nil {
			return nil, errs[i]
		}
		sha := scans[i].SHA
		if sha == "" {
			sha = "sin git"
		}
		wip := ""
		if n := scans[i].Dirty; n > 0 {
			wip = fmt.Sprintf("; %d con cambios sin commit", n)
		}
		fmt.Fprintf(a.stderr, "%s@%s: %d archivos de código%s, %d interfaces (%s)\n", s.Name, sha, scans[i].Files, wip, len(scans[i].Entries), took[i].Round(10*time.Millisecond))
	}
	return scans, nil
}

// storedMap es el mapa guardado en coyote/map/, por repo.
type storedMap struct {
	shas    map[string]string
	entries map[string][]product.Entry
}

func loadStoredMap(root string) (*storedMap, error) {
	sm := &storedMap{shas: map[string]string{}, entries: map[string][]product.Entry{}}
	if err := fsx.NoSymlinks(root, mapDir); err != nil {
		return nil, fail(1, "%v", err)
	}
	list, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(mapDir)))
	if os.IsNotExist(err) {
		return sm, nil
	}
	if err != nil {
		return nil, err
	}
	for _, f := range list {
		if !strings.HasSuffix(f.Name(), ".map") || !f.Type().IsRegular() {
			continue
		}
		data, err := fsx.ReadFile(root, mapDir+"/"+f.Name(), 64<<20)
		if err != nil {
			return nil, fail(1, "%s/%s: %v", mapDir, f.Name(), err)
		}
		repo, sha, entries, err := product.Decode(string(data))
		if err != nil {
			return nil, fail(1, "%s/%s: %v", mapDir, f.Name(), err)
		}
		if repo+".map" != f.Name() {
			return nil, fail(1, "%s/%s declara el repo %q", mapDir, f.Name(), repo)
		}
		sm.shas[repo], sm.entries[repo] = sha, entries
	}
	return sm, nil
}

// productMap arma el mapa con los repos leídos y, para los que no tienen
// copia local, con lo guardado.
func productMap(scans []*product.Scan, stored *storedMap, noPath []string) (*product.Map, []string) {
	shas := map[string]string{}
	var entries []product.Entry
	var notes []string
	for _, sc := range scans {
		shas[sc.Repo] = sc.SHA
		entries = append(entries, sc.Entries...)
	}
	for _, n := range noPath {
		if e, ok := stored.entries[n]; ok {
			shas[n] = stored.shas[n]
			entries = append(entries, e...)
			notes = append(notes, n+": sin copia local; uso su mapa guardado")
		} else {
			notes = append(notes, n+": sin copia local ni mapa guardado; queda fuera del análisis")
		}
	}
	return product.Load(shas, entries), notes
}

// writeProductFile escribe un archivo del proyecto del producto si cambió.
func writeProductFile(root, relPath, content string) (string, error) {
	if err := fsx.NoSymlinks(root, relPath); err != nil {
		return "", fail(1, "%v", err)
	}
	p := filepath.Join(root, filepath.FromSlash(relPath))
	old, err := os.ReadFile(p)
	status := "creado"
	if err == nil {
		if string(old) == content {
			return "sin cambios", nil
		}
		status = "actualizado"
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return "", err
	}
	return status, os.Rename(tmp, p)
}

// ---- coyote map ----

func cmdMap(a *app, args []string) error {
	fs := a.flags("map", "[--check] [--json] [--top N]")
	check := fs.Bool("check", false, "falla si el mapa guardado no refleja el código (para la CI); no escribe")
	asJSON := fs.Bool("json", false, "salida JSON")
	top := fs.Int("top", 10, "grupos de llamadas sin proveedor a mostrar")
	showAmbiguous := fs.Bool("ambiguous", false, "lista las llamadas que coinciden igual con más de un servicio")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return fail(2, "coyote map lee todos los repos del producto: los enlaces cruzan repos")
	}
	root, cfg, err := a.project()
	if err != nil {
		return err
	}
	sources, noPath, err := productSources(root, cfg, nil)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		return fail(1, "el proyecto no tiene repos con copia local: regístralos con coyote repo add <nombre> --path <ruta>")
	}
	stored, err := loadStoredMap(root)
	if err != nil {
		return err
	}
	scans, err := a.scanAll(sources)
	if err != nil {
		return err
	}
	m, notes := productMap(scans, stored, noPath)
	if *check {
		return a.mapCheck(scans, stored, m)
	}
	status := map[string]string{}
	changed := false
	for _, sc := range scans {
		st, err := writeProductFile(root, mapDir+"/"+sc.Repo+".map", sc.EncodeMap())
		if err != nil {
			return err
		}
		status[sc.Repo] = st
		changed = changed || st != "sin cambios"
	}
	stats := m.Summary()
	unlinked := unlinkedGroups(m, *top)
	cross, ambiguous := 0, 0
	for _, l := range m.Links {
		if m.Entries[l.From].Repo != m.Entries[l.To].Repo {
			cross++
		}
		if l.Ambiguous {
			ambiguous++
		}
	}
	if changed {
		if err := a.recordMap(root, cfg, m, len(m.Links)); err != nil {
			return err
		}
	}
	if *asJSON {
		type repoOut struct {
			product.Stats
			SHA    string `json:"sha"`
			File   string `json:"file,omitempty"`
			Status string `json:"status,omitempty"`
		}
		var repos []repoOut
		for _, st := range stats {
			r := repoOut{Stats: st, SHA: m.SHAs[st.Repo], Status: status[st.Repo]}
			if r.Status != "" {
				r.File = mapDir + "/" + st.Repo + ".map"
			}
			repos = append(repos, r)
		}
		return writeJSON(a, map[string]any{"repos": repos, "links": len(m.Links), "cross_repo_links": cross, "ambiguous_links": ambiguous,
			"unlinked": unlinked, "notes": notes})
	}
	tw := table(a.stdout)
	fmt.Fprintln(tw, "repo\tcommit\texpone\tllama\tpublica\tescucha\tenlazadas\tsin enlace\tsin resolver\tmapa")
	for _, st := range stats {
		file := "guardado"
		if s := status[st.Repo]; s != "" {
			file = mapDir + "/" + st.Repo + ".map (" + s + ")"
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%s\n", st.Repo, orDash(m.SHAs[st.Repo]), st.Exposes, st.Calls,
			st.Publishes, st.Listens, st.Linked, st.Unlinked, st.Unresolved, file)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "\n%d interfaces · %d enlaces entre módulos, %d entre repos", len(m.Entries), len(m.Links), cross)
	if ambiguous > 0 {
		fmt.Fprintf(a.stdout, " · %d ambiguos (la llamada coincide igual con más de un servicio)", ambiguous)
	}
	fmt.Fprintln(a.stdout)
	if len(unlinked) > 0 {
		fmt.Fprintln(a.stdout, "\nLlamadas sin proveedor en el producto (servicios externos o huecos del extractor):")
		tw = table(a.stdout)
		for _, g := range unlinked {
			fmt.Fprintf(tw, "  %d\t%s\t%s\t%s\n", g.Count, g.Where, g.Prefix, g.Example)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	if *showAmbiguous {
		printAmbiguous(a, m)
	}
	for _, n := range notes {
		fmt.Fprintln(a.stdout, "nota: "+n)
	}
	return nil
}

// printAmbiguous muestra cada llamada ambigua con los servicios candidatos.
func printAmbiguous(a *app, m *product.Map) {
	by := map[int][]int{}
	var order []int
	for _, l := range m.Links {
		if !l.Ambiguous {
			continue
		}
		if _, ok := by[l.From]; !ok {
			order = append(order, l.From)
		}
		by[l.From] = append(by[l.From], l.To)
	}
	if len(order) == 0 {
		return
	}
	fmt.Fprintf(a.stdout, "\nLlamadas ambiguas (%d): el mapa las enlaza con todos los candidatos\n", len(order))
	for _, i := range order {
		e := m.Entries[i]
		fmt.Fprintf(a.stdout, "  %s  llama %s  (%s)\n", hitWhere(e), e.Describe(), e.Ref(""))
		for _, j := range by[i] {
			p := m.Entries[j]
			fmt.Fprintf(a.stdout, "      ↳ %s  expone %s  (%s:%d)\n", hitWhere(p), p.Describe(), p.File, p.Line)
		}
	}
}

// unlinkedGroup agrupa llamadas sin proveedor por repo, módulo y prefijo.
type unlinkedGroup struct {
	Count   int    `json:"count"`
	Where   string `json:"where"`
	Prefix  string `json:"prefix"`
	Example string `json:"example"`
}

func unlinkedGroups(m *product.Map, top int) []unlinkedGroup {
	by := map[string]*unlinkedGroup{}
	for _, e := range m.Unlinked() {
		where := e.Repo
		if e.Module != "." && e.Module != "" {
			where += ": " + product.ModuleName(e.Module)
		}
		pre := e.Path
		if e.Kind() == "http" {
			segs := strings.Split(strings.Trim(e.Path, "/"), "/")
			if len(segs) > 2 {
				segs = segs[:2]
			}
			pre = "/" + strings.Join(segs, "/")
			if strings.Count(strings.Trim(e.Path, "/"), "/") >= 2 {
				pre += "/**"
			}
		} else {
			pre = "tópico " + pre
		}
		k := where + "|" + pre
		g := by[k]
		if g == nil {
			g = &unlinkedGroup{Where: where, Prefix: pre, Example: e.Describe() + " " + e.Ref("")}
			by[k] = g
		}
		g.Count++
	}
	out := make([]unlinkedGroup, 0, len(by))
	for _, g := range by {
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Where+out[i].Prefix < out[j].Where+out[j].Prefix
	})
	if top >= 0 && len(out) > top {
		out = out[:top]
	}
	return out
}

// mapCheck compara el código con el mapa guardado, sin contar cambios de línea.
func (a *app) mapCheck(scans []*product.Scan, stored *storedMap, m *product.Map) error {
	stale := 0
	for _, sc := range scans {
		old, ok := stored.entries[sc.Repo]
		if !ok {
			fmt.Fprintf(a.stdout, "%s: sin mapa guardado\n", sc.Repo)
			stale++
			continue
		}
		added, removed := product.Compare(old, sc.Entries)
		if len(added)+len(removed) == 0 {
			continue
		}
		stale++
		fmt.Fprintf(a.stdout, "%s: +%d −%d interfaces desde %s\n", sc.Repo, len(added), len(removed), orDash(stored.shas[sc.Repo]))
		for i, e := range added {
			if i == 5 {
				fmt.Fprintf(a.stdout, "  + … y %d más\n", len(added)-5)
				break
			}
			fmt.Fprintf(a.stdout, "  + %s %s (%s)\n", e.Role, e.Describe(), e.Ref(""))
		}
		for i, e := range removed {
			if i == 5 {
				fmt.Fprintf(a.stdout, "  − … y %d más\n", len(removed)-5)
				break
			}
			fmt.Fprintf(a.stdout, "  − %s %s (%s)\n", e.Role, e.Describe(), e.Ref(""))
		}
	}
	if stale > 0 {
		return fail(1, "el mapa no está al día: corre coyote map y versiona %s/", mapDir)
	}
	fmt.Fprintf(a.stdout, "el mapa está al día: %d interfaces, %d enlaces\n", len(m.Entries), len(m.Links))
	return nil
}

func (a *app) recordMap(root string, cfg *project.Config, m *product.Map, links int) error {
	person := identity.Resolve(root)
	agent, err := a.agentFor(root, "")
	if err != nil {
		return err
	}
	var refs []string
	repos := make([]string, 0, len(m.SHAs))
	for r := range m.SHAs {
		repos = append(repos, r)
	}
	sort.Strings(repos)
	for _, r := range repos {
		if sha := m.SHAs[r]; sha != "" {
			refs = append(refs, "sha:"+safeRef(r+"@"+sha))
		}
	}
	line := ccf.Line{TS: a.now(), Actor: person.Actor(agent), Project: "-", Repo: cfg.Name, Type: "idx", Scope: "mapa",
		What: fmt.Sprintf("mapa del producto: %d interfaces, %d enlaces", len(m.Entries), links), Refs: refs, Status: "ok"}
	_, err = ledger.Open(root).Append(line, person.Slug)
	return err
}

func writeJSON(a *app, v any) error {
	enc := json.NewEncoder(a.stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// ---- coyote extract ----

func cmdExtract(a *app, args []string) error {
	fs := a.flags("extract", "[repo...] [--stdout] [--check] [--force]")
	toStdout := fs.Bool("stdout", false, "muestra las propuestas sin escribirlas")
	check := fs.Bool("check", false, "falla si alguna propuesta no está al día; no escribe")
	force := fs.Bool("force", false, "reemplaza también las propuestas que la persona editó")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	root, cfg, err := a.project()
	if err != nil {
		return err
	}
	if _, _, err := productSources(root, cfg, pos); err != nil {
		return err // valida los nombres pedidos antes de leer nada
	}
	sources, noPath, err := productSources(root, cfg, nil)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		return fail(1, "el proyecto no tiene repos con copia local: regístralos con coyote repo add <nombre> --path <ruta>")
	}
	stored, err := loadStoredMap(root)
	if err != nil {
		return err
	}
	scans, err := a.scanAll(sources)
	if err != nil {
		return err
	}
	m, _ := productMap(scans, stored, noPath)
	want := map[string]bool{}
	for _, n := range pos {
		want[n] = true
	}
	today := a.now().Format("2006-01-02")
	stale, edited := 0, 0
	for _, sc := range scans {
		if len(want) > 0 && !want[sc.Repo] {
			continue
		}
		p := product.Propose(sc, m, today)
		for _, doc := range []struct{ name, content string }{{ccfdoc.ReadmeFile, p.Readme}, {ccfdoc.ContextFile, p.Context}} {
			d, issues := ccfdoc.Parse(doc.name, []byte(doc.content))
			issues = append(issues, d.Validate()...)
			if ccfdoc.HasErrors(issues) {
				return fail(1, "%s: la propuesta de %s no es válida: %v", sc.Repo, doc.name, issues)
			}
			relPath := reposDir + "/" + sc.Repo + "/" + doc.name
			if *toStdout {
				fmt.Fprintf(a.stdout, "==> %s <==\n%s\n", relPath, doc.content)
				continue
			}
			status, err := a.writeProposal(root, relPath, doc.content, *force, *check)
			if err != nil {
				return err
			}
			switch status {
			case "editado":
				edited++
				status = "editado por una persona: no se reemplaza (usa --force)"
			case "creado", "actualizado":
				stale++
				if *check {
					status = "no está al día"
				}
			}
			warn := ""
			if n := len(issues); n > 0 {
				warn = fmt.Sprintf(", %d %s", n, pluralWord(n, "advertencia", "advertencias"))
			}
			fmt.Fprintf(a.stdout, "%s: %s (~%d/%d tokens%s)\n", relPath, status, d.Tokens, d.Limit(), warn)
		}
		if p.Omitted > 0 && !*toStdout {
			fmt.Fprintf(a.stdout, "%s: %d entradas no cupieron en el tope; están en %s/%s.map\n", sc.Repo, p.Omitted, mapDir, sc.Repo)
		}
	}
	if *check && stale > 0 {
		return fail(1, "hay propuestas que no están al día: corre coyote extract")
	}
	if !*toStdout && !*check && stale > 0 {
		fmt.Fprintln(a.stdout, "revisa las propuestas; para llevarlas a un repo, cópialas en su raíz con un PR de ese repo")
	}
	return nil
}

// writeProposal escribe una propuesta sin pisar lo que la persona editó. Si
// solo cambian la fecha o el commit de origen, se deja la versión anterior.
func (a *app) writeProposal(root, relPath, content string, force, check bool) (string, error) {
	if err := fsx.NoSymlinks(root, relPath); err != nil {
		return "", fail(1, "%v", err)
	}
	old, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relPath)))
	if err == nil {
		if sameProposal(string(old), content) {
			return "sin cambios", nil
		}
		if product.Edited(string(old)) && !force {
			return "editado", nil
		}
	}
	if check {
		if err == nil {
			return "actualizado", nil
		}
		return "creado", nil
	}
	return writeProductFile(root, relPath, content)
}

// sameProposal compara dos propuestas sin su huella ni la fecha updated.
func sameProposal(a, b string) bool {
	norm := func(s string) string {
		doc, _ := product.StripMarker(s)
		var out []string
		for _, l := range strings.Split(doc, "\n") {
			if !strings.HasPrefix(l, "updated:") {
				out = append(out, l)
			}
		}
		return strings.Join(out, "\n")
	}
	return norm(a) == norm(b)
}

func pluralWord(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// ---- coyote impact ----

func cmdImpact(a *app, args []string) error {
	fs := a.flags("impact", "<endpoint|texto> | --topic T | --diff [repo=]RANGO | --files repo:ruta [--format text|md|json] [--record]")
	topic := fs.String("topic", "", "tópico de eventos")
	repo := fs.String("repo", "", "repo de un --diff o --files sin prefijo")
	var diffs, files multiFlag
	fs.Var(&diffs, "diff", "cambios de un repo: repo=RANGO (p. ej. servicios=main...HEAD); se repite para un cambio coordinado")
	fs.Var(&files, "files", "archivos cambiados: repo:ruta; se repite")
	format := fs.String("format", "text", "text, md (para un PR) o json")
	asJSON := fs.Bool("json", false, "igual que --format json")
	fresh := fs.Bool("fresh", false, "lee los repos en lugar del mapa guardado")
	record := fs.Bool("record", false, "registra el análisis en el ledger (tipo rev)")
	ws := fs.String("ws", "-", "workstream, con --record")
	agent := fs.String("agent", "", "agente, con --record")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if *asJSON {
		*format = "json"
	}
	switch *format {
	case "text", "md", "json":
	default:
		return fail(2, "--format %q inválido; usa text, md o json", *format)
	}
	root, cfg, err := a.project()
	if err != nil {
		return err
	}
	q, err := impactQuery(strings.TrimSpace(strings.Join(pos, " ")), *topic, *repo, diffs, files)
	if err != nil {
		return err
	}
	sources, noPath, err := productSources(root, cfg, nil)
	if err != nil {
		return err
	}
	stored, err := loadStoredMap(root)
	if err != nil {
		return err
	}
	src := product.Sources{}
	for _, s := range sources {
		src[s.Name] = s.Dir
	}
	var m *product.Map
	var notes []string
	if *fresh || len(stored.entries) == 0 {
		if len(sources) == 0 {
			return fail(1, "no hay mapa guardado ni repos con copia local: corre coyote map")
		}
		scans, err := a.scanAll(sources)
		if err != nil {
			return err
		}
		m, notes = productMap(scans, stored, noPath)
		if len(stored.entries) == 0 {
			notes = append(notes, "sin mapa guardado: se leyeron los repos; guárdalo con coyote map")
		}
	} else {
		var entries []product.Entry
		shas := map[string]string{}
		for r, e := range stored.entries {
			entries = append(entries, e...)
			shas[r] = stored.shas[r]
		}
		m = product.Load(shas, entries)
		for _, s := range sources {
			if _, ok := stored.entries[s.Name]; !ok {
				notes = append(notes, s.Name+": no está en el mapa guardado; corre coyote map")
				continue
			}
			if head := product.HeadSHA(s.Dir); head != "" && stored.shas[s.Name] != "" && head != stored.shas[s.Name] {
				notes = append(notes, fmt.Sprintf("el mapa de %s es de %s y el repo está en %s: corre coyote map o usa --fresh", s.Name, stored.shas[s.Name], head))
			}
		}
	}
	im, err := m.Impact(q, src)
	if err != nil {
		return fail(1, "%v", err)
	}
	im.Notes = append(notes, im.Notes...)
	switch *format {
	case "json":
		if err := writeJSON(a, impactJSON(im, m)); err != nil {
			return err
		}
	case "md":
		fmt.Fprint(a.stdout, impactMarkdown(im, m, len(m.SHAs)))
	default:
		fmt.Fprint(a.stdout, impactText(im, m, len(m.SHAs)))
	}
	if *record {
		return a.recordImpact(root, cfg, im, *ws, *agent)
	}
	return nil
}

// impactQuery interpreta la consulta: un endpoint (con o sin verbo), un
// tópico, cambios por repo o, si no, texto libre.
func impactQuery(text, topic, repo string, diffs, files []string) (product.Query, error) {
	var q product.Query
	byRepo := map[string]*product.Change{}
	var order []string
	change := func(r string) *product.Change {
		c := byRepo[r]
		if c == nil {
			c = &product.Change{Repo: r}
			byRepo[r] = c
			order = append(order, r)
		}
		return c
	}
	for _, d := range diffs {
		r, rng := repo, d
		if i := strings.Index(d, "="); i > 0 {
			r, rng = d[:i], d[i+1:]
		}
		if r == "" {
			return q, fail(2, "--diff %q: indica el repo (repo=RANGO o --repo)", d)
		}
		if rng == "" || strings.HasPrefix(rng, "-") {
			return q, fail(2, "--diff %q: rango de git inválido", d)
		}
		c := change(r)
		if c.Diff != "" {
			return q, fail(2, "--diff: un solo rango por repo (%s)", r)
		}
		c.Diff = rng
	}
	for _, f := range files {
		r, p := repo, f
		if i := strings.Index(f, ":"); i > 0 {
			r, p = f[:i], f[i+1:]
		}
		if r == "" || p == "" {
			return q, fail(2, "--files %q: usa repo:ruta", f)
		}
		clean := filepath.ToSlash(filepath.Clean(p))
		if strings.HasPrefix(clean, "../") || clean == ".." || filepath.IsAbs(p) {
			return q, fail(2, "--files %q: la ruta es relativa al repo", f)
		}
		c := change(r)
		c.Files = append(c.Files, clean)
	}
	for _, r := range order {
		c := byRepo[r]
		if c.Diff != "" && len(c.Files) > 0 {
			return q, fail(2, "%s: usa --diff o --files, no los dos", r)
		}
		q.Changes = append(q.Changes, *c)
	}
	kinds := 0
	for _, set := range []bool{len(q.Changes) > 0, topic != "", text != ""} {
		if set {
			kinds++
		}
	}
	switch {
	case kinds == 0:
		return q, fail(2, "uso: coyote impact <endpoint|texto> | --topic T | --diff repo=RANGO | --files repo:ruta")
	case kinds > 1:
		return q, fail(2, "coyote impact analiza un tipo de cambio a la vez: endpoint o texto, --topic, o --diff/--files")
	case topic != "":
		q.Topic = topic
	case text != "":
		if looksLikeEndpoint(text) {
			q.Endpoint = text
		} else {
			q.Text = text
		}
	}
	return q, nil
}

var httpVerbs = map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true}

func looksLikeEndpoint(s string) bool {
	f := strings.Fields(s)
	switch len(f) {
	case 1:
		return strings.HasPrefix(f[0], "/") || strings.HasPrefix(f[0], "http://") || strings.HasPrefix(f[0], "https://")
	case 2:
		return httpVerbs[strings.ToUpper(f[0])] && (strings.HasPrefix(f[1], "/") || strings.HasPrefix(f[1], "http"))
	}
	return false
}

func impactSections(im *product.Impact) []struct {
	title string
	hits  []product.Hit
} {
	return []struct {
		title string
		hits  []product.Hit
	}{
		{"Cambia", im.Touched},
		{"Afecta directo", im.Direct},
		{"Afecta a través de un servicio intermedio (BFF)", im.Indirect},
	}
}

func hitWhere(e product.Entry) string {
	if e.Module == "." || e.Module == "" {
		return e.Repo
	}
	return e.Repo + " · " + product.ModuleName(e.Module)
}

func impactText(im *product.Impact, m *product.Map, total int) string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "Impacto de: %s\n", im.Query)
	fmt.Fprintf(&b, "Mapa: %s\n", mapVersions(m))
	if n := breakingCount(im); n > 0 {
		fmt.Fprintf(&b, "Atención: %d %s usan algo que el cambio elimina\n", n, pluralWord(n, "interfaz", "interfaces"))
	}
	for _, s := range impactSections(im) {
		if len(s.hits) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n%s (%d)\n", s.title, len(s.hits))
		for _, h := range s.hits {
			amb := ""
			if h.Breaking {
				amb += " [SE ROMPE]"
			}
			if h.Ambiguous {
				amb += " [ambiguo: más de un servicio coincide]"
			}
			fmt.Fprintf(&b, "  %s  %s %s — %s%s\n      %s\n", hitWhere(h.Entry), h.Entry.Role, h.Entry.Describe(), h.Why, amb, h.Entry.Ref(m.SHAs[h.Entry.Repo]))
		}
	}
	if len(im.Unresolved) > 0 {
		fmt.Fprintf(&b, "\nSin resolver en los módulos afectados (%d): revísalos a mano\n", len(im.Unresolved))
		for _, e := range im.Unresolved {
			fmt.Fprintf(&b, "  %s  %s %s\n      %s\n", hitWhere(e), e.Role, e.Raw, e.Ref(m.SHAs[e.Repo]))
		}
	}
	if len(im.Notes) > 0 {
		b.WriteString("\nNotas\n")
		for _, n := range im.Notes {
			fmt.Fprintf(&b, "  - %s\n", n)
		}
	}
	fmt.Fprintf(&b, "\nRepos afectados: %s (%d de %d)\n", orDash(strings.Join(im.Repos, ", ")), len(im.Repos), total)
	return b.String()
}

func breakingCount(im *product.Impact) int {
	n := 0
	for _, h := range im.Direct {
		if h.Breaking {
			n++
		}
	}
	return n
}

func impactMarkdown(im *product.Impact, m *product.Map, total int) string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "### Impacto en el producto\n\n**Cambio:** %s  \n**Repos afectados:** %s (%d de %d)  \n**Mapa:** %s\n",
		mdCell(im.Query), mdCell(orDash(strings.Join(im.Repos, ", "))), len(im.Repos), total, mdCell(mapVersions(m)))
	if n := breakingCount(im); n > 0 {
		fmt.Fprintf(&b, "\n> **Atención:** %d %s usan algo que el cambio elimina.\n", n, pluralWord(n, "interfaz", "interfaces"))
	}
	for _, s := range impactSections(im) {
		if len(s.hits) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n#### %s (%d)\n\n| Repo | Módulo | Interfaz | Por qué | Dónde |\n|---|---|---|---|---|\n", s.title, len(s.hits))
		for _, h := range s.hits {
			why := mdCell(h.Why)
			if h.Breaking {
				why = "**se rompe:** " + why
			}
			if h.Ambiguous {
				why += " (ambiguo)"
			}
			mod := product.ModuleName(h.Entry.Module)
			if h.Entry.Module == "." || h.Entry.Module == "" {
				mod = "—"
			}
			fmt.Fprintf(&b, "| %s | %s | %s %s | %s | `%s:%d` |\n", mdCell(h.Entry.Repo), mdCell(mod),
				h.Entry.Role, mdCell(h.Entry.Describe()), why, mdCell(h.Entry.File), h.Entry.Line)
		}
	}
	if len(im.Unresolved) > 0 {
		fmt.Fprintf(&b, "\n#### Sin resolver en los módulos afectados (%d)\n\n", len(im.Unresolved))
		for _, e := range im.Unresolved {
			fmt.Fprintf(&b, "- %s · %s: %s %s (`%s:%d`)\n", mdCell(e.Repo), mdCell(product.ModuleName(e.Module)), e.Role, mdCell(e.Raw), mdCell(e.File), e.Line)
		}
	}
	if len(im.Notes) > 0 {
		b.WriteString("\n#### Notas\n\n")
		for _, n := range im.Notes {
			fmt.Fprintf(&b, "- %s\n", mdCell(n))
		}
	}
	return b.String()
}

// mdCell deja un texto apto para una celda de tabla Markdown.
func mdCell(s string) string {
	return strings.NewReplacer("|", "\\|", "\n", " ", "\r", " ", "`", "'").Replace(s)
}

func mapVersions(m *product.Map) string {
	repos := make([]string, 0, len(m.SHAs))
	for r := range m.SHAs {
		repos = append(repos, r)
	}
	sort.Strings(repos)
	parts := make([]string, len(repos))
	for i, r := range repos {
		parts[i] = r + "@" + orDash(m.SHAs[r])
	}
	return strings.Join(parts, " · ")
}

func impactJSON(im *product.Impact, m *product.Map) map[string]any {
	type hit struct {
		Repo      string `json:"repo"`
		Module    string `json:"module"`
		Role      string `json:"role"`
		Method    string `json:"method,omitempty"`
		Path      string `json:"path"`
		Why       string `json:"why,omitempty"`
		Ambiguous bool   `json:"ambiguous,omitempty"`
		Breaking  bool   `json:"breaking,omitempty"`
		Ref       string `json:"ref"`
	}
	conv := func(hs []product.Hit) []hit {
		out := make([]hit, 0, len(hs))
		for _, h := range hs {
			e := h.Entry
			out = append(out, hit{e.Repo, e.Module, e.Role, e.Method, e.Path, h.Why, h.Ambiguous, h.Breaking, e.Ref(m.SHAs[e.Repo])})
		}
		return out
	}
	unres := make([]hit, 0, len(im.Unresolved))
	for _, e := range im.Unresolved {
		unres = append(unres, hit{e.Repo, e.Module, e.Role, e.Method, e.Raw, "", false, false, e.Ref(m.SHAs[e.Repo])})
	}
	return map[string]any{"query": im.Query, "map": m.SHAs, "touched": conv(im.Touched), "direct": conv(im.Direct),
		"indirect": conv(im.Indirect), "unresolved": unres, "repos": im.Repos, "notes": im.Notes}
}

func (a *app) recordImpact(root string, cfg *project.Config, im *product.Impact, ws, agent string) error {
	agent, err := a.agentFor(root, agent)
	if err != nil {
		return err
	}
	person := identity.Resolve(root)
	refs := []string{}
	if len(im.Repos) > 0 {
		refs = append(refs, "repos:"+safeRef(strings.Join(im.Repos, ",")))
	}
	n := len(im.Touched) + len(im.Direct) + len(im.Indirect)
	what := ccf.ShortWhat(fmt.Sprintf("impacto: %d interfaces en %d repos por %s", n, len(im.Repos), im.Query), ccf.MaxWhatWords)
	line := ccf.Line{TS: a.now(), Actor: person.Actor(agent), Project: ledgerID(ws), Repo: cfg.Name, Type: "rev",
		Scope: "impacto", What: what, Refs: refs, Status: "ok"}
	_, err = ledger.Open(root).Append(line, person.Slug)
	return err
}
