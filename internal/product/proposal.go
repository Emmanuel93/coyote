package product

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/Emmanuel93/coyote/internal/tokens"
)

// Topes de las propuestas: por debajo de los de CCF-doc v1 (300 tokens, 400
// con módulos y 1500 para el contexto) para dejar margen a la persona.
const (
	readmeBudget        = 280
	readmeBudgetModules = 380
	contextBudget       = 1300
)

// MarkerPrefix abre la línea que identifica una propuesta de coyote extract.
// La huella permite saber si la persona la editó: una propuesta editada no se
// vuelve a escribir.
const MarkerPrefix = "# Propuesta de coyote extract"

var huellaRe = regexp.MustCompile(`huella ([0-9a-f]{12})\b`)

// Proposal es el par de documentos propuestos para un repo.
type Proposal struct {
	Repo    string
	Readme  string
	Context string
	Omitted int // módulos, documentos o entradas que no cupieron en el tope
}

// modInfo resume un módulo para las propuestas.
type modInfo struct {
	path, name, desc                          string
	exposes, calls, publishes, listens, unres int
	prefixes                                  map[string]int
	longer                                    map[string]bool // el prefijo tiene rutas más largas
	topics                                    []string
	consumers                                 map[string]int // "repo: módulo" → enlaces
	providers                                 map[string]int // de quién depende: "repo: módulo" → enlaces
	firstExposed, firstUnresolved             *Entry
}

func (mi *modInfo) weight() int { return mi.exposes + mi.calls + mi.publishes + mi.listens }

// label nombra a un módulo de otro repo: "app" o "servicios: bff-movil".
func label(e Entry) string {
	if e.Module == "." || e.Module == "" {
		return e.Repo
	}
	return e.Repo + ": " + ModuleName(e.Module)
}

// scopeName es el ámbito de un módulo en CONTEXT.coyote.md: sin espacios.
func scopeName(repo, mod string) string {
	s := ModuleName(mod)
	if mod == "." || mod == "" {
		s = repo
	}
	return strings.Join(strings.Fields(s), "-")
}

// prefixOf agrupa una ruta por sus primeros segmentos fijos (/api/v1/pedidos)
// e informa si la ruta sigue después de ellos.
func prefixOf(p string) (string, bool) {
	all := strings.Split(strings.Trim(p, "/"), "/")
	var segs []string
	for _, s := range all {
		if s == "" || s == Param || len(segs) == 3 {
			break
		}
		segs = append(segs, s)
	}
	return "/" + strings.Join(segs, "/"), len(segs) < len(all)
}

// modules resume los módulos del repo con lo que el mapa sabe de ellos.
func (sc *Scan) moduleInfo(m *Map) []*modInfo {
	by := map[string]*modInfo{}
	get := func(p string) *modInfo {
		mi := by[p]
		if mi == nil {
			mi = &modInfo{path: p, name: ModuleName(p), prefixes: map[string]int{}, longer: map[string]bool{}, consumers: map[string]int{}, providers: map[string]int{}}
			by[p] = mi
		}
		return mi
	}
	for _, md := range sc.Identity.Modules {
		get(md.Path).desc = md.Description
	}
	for i := range sc.Entries {
		e := &sc.Entries[i]
		mi := get(e.Module)
		if e.Unresolved {
			mi.unres++
			if mi.firstUnresolved == nil {
				mi.firstUnresolved = e
			}
			continue
		}
		switch e.Role {
		case Exposes:
			mi.exposes++
			pre, more := prefixOf(e.Path)
			mi.prefixes[pre]++
			mi.longer[pre] = mi.longer[pre] || more
			if mi.firstExposed == nil {
				mi.firstExposed = e
			}
		case Calls:
			mi.calls++
		case Publishes:
			mi.publishes++
			mi.topics = appendUnique(mi.topics, e.Path)
		case Listens:
			mi.listens++
		}
	}
	if m != nil {
		for _, l := range m.Links {
			to, from := m.Entries[l.To], m.Entries[l.From]
			if from.Repo == sc.Repo && to.Repo != sc.Repo {
				get(from.Module).providers[label(to)]++ // depende de otro repo del producto
			}
			if to.Repo != sc.Repo || (from.Repo == to.Repo && from.Module == to.Module) {
				continue
			}
			who := label(from)
			if from.Repo == sc.Repo {
				who = scopeName(from.Repo, from.Module) // del mismo repo basta el módulo
			}
			get(to.Module).consumers[who]++
		}
	}
	out := make([]*modInfo, 0, len(by))
	for _, mi := range by {
		out = append(out, mi)
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := out[i].weight()+len(out[i].consumers)*10, out[j].weight()+len(out[j].consumers)*10; a != b {
			return a > b
		}
		return out[i].path < out[j].path
	})
	return out
}

func appendUnique(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

// topKeys devuelve las claves con más cuenta, hasta n.
func topKeys(m map[string]int, n int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	if len(keys) > n {
		keys = keys[:n]
	}
	return keys
}

// listOf une hasta n nombres y dice cuántos quedan: "a, b y 3 más".
func listOf(names []string, n int) string {
	if len(names) <= n {
		if len(names) <= 1 {
			return strings.Join(names, "")
		}
		return strings.Join(names[:len(names)-1], ", ") + " y " + names[len(names)-1]
	}
	return strings.Join(names[:n], ", ") + fmt.Sprintf(" y %d más", len(names)-n)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// describe genera la descripción de un módulo sin una propia.
func (mi *modInfo) describe() string {
	var parts []string
	if mi.exposes > 0 {
		parts = append(parts, "expone "+plural(mi.exposes, "endpoint", "endpoints"))
	}
	if mi.publishes > 0 {
		parts = append(parts, "publica "+plural(len(mi.topics), "tópico", "tópicos"))
	}
	if mi.calls > 0 {
		parts = append(parts, "llama "+plural(mi.calls, "endpoint", "endpoints"))
	}
	if mi.listens > 0 {
		parts = append(parts, "escucha "+plural(mi.listens, "tópico", "tópicos"))
	}
	if len(parts) == 0 {
		return "TODO: qué hace este módulo"
	}
	return listOf(parts, 4)
}

// iface resume la interfaz de un módulo: sus rutas y tópicos principales.
func (mi *modInfo) iface() string {
	var parts []string
	for _, k := range topKeys(mi.prefixes, 2) {
		if mi.longer[k] {
			k += "/**"
		}
		parts = append(parts, k)
	}
	if len(mi.prefixes) > 2 {
		parts[len(parts)-1] += " …"
	}
	topics := append([]string(nil), mi.topics...)
	sort.Strings(topics)
	if len(topics) > 2 {
		topics = append(topics[:2], "…")
	}
	parts = append(parts, topics...)
	return clean(strings.Join(parts, ", "))
}

// Propose arma las propuestas de README.coyote.md y CONTEXT.coyote.md de un
// repo con lo que se sacó de su código y con el mapa del producto (quién lo
// usa y a quién usa). today va en formato AAAA-MM-DD.
func Propose(sc *Scan, m *Map, today string) Proposal {
	p := Proposal{Repo: sc.Repo}
	mods := sc.moduleInfo(m)
	p.Readme = sc.readme(m, mods, &p.Omitted)
	p.Context = sc.context(mods, today, &p.Omitted)
	return p
}

// budgeted agrega líneas mientras el documento quepa en el tope.
type budgeted struct {
	head   string
	lines  []string
	budget int
}

// markerCost reserva lo que ocupará la línea de la huella.
var markerCost = tokens.Estimate(MarkerPrefix + " (un-repo-de-nombre-largo@0000000, huella 000000000000): revísala antes de llevarla al repo")

func (b *budgeted) try(line string) bool {
	text := b.head + strings.Join(append(append([]string{}, b.lines...), line), "\n") + "\n"
	if tokens.Estimate(text)+markerCost > b.budget {
		return false
	}
	b.lines = append(b.lines, line)
	return true
}

func (b *budgeted) String() string {
	return b.head + strings.Join(b.lines, "\n") + "\n"
}

func (sc *Scan) readme(m *Map, mods []*modInfo, omitted *int) string {
	id := sc.Identity
	var front strings.Builder
	front.WriteString("---\ncoyote: 1\n")
	fmt.Fprintf(&front, "repo: %s\ntype: %s\n", sc.Repo, id.Type)
	fmt.Fprintf(&front, "stack: [%s]\n", strings.Join(id.Stacks, ", "))
	if len(id.Owners) > 0 {
		q := make([]string, len(id.Owners))
		for i, o := range id.Owners {
			q[i] = fmt.Sprintf("%q", o)
		}
		fmt.Fprintf(&front, "owners: [%s]\n", strings.Join(q, ", "))
	}
	front.WriteString("---\n")
	b := &budgeted{head: front.String(), budget: readmeBudget}
	purpose := id.Purpose
	if purpose == "" {
		purpose = "TODO: qué hace este repo en una línea"
	}
	b.lines = append(b.lines, "purpose|"+clean(purpose))
	for _, kv := range [][2]string{{"run", id.Run}, {"test", id.Test}, {"build", id.Build}} {
		if kv[1] != "" {
			b.lines = append(b.lines, kv[0]+"|"+clean(kv[1]))
		}
	}
	// De qué otros repos del producto depende: lo que más importa para ver un
	// cambio de forma holística.
	for _, d := range sc.deps(m) {
		if !b.try(d) {
			*omitted++
		}
	}
	for _, mi := range mods {
		if mi.path == "." || mi.path == "" {
			continue // el repo entero ya es el módulo
		}
		desc := mi.desc
		if desc == "" {
			desc = mi.describe()
		}
		line := fmt.Sprintf("mod|%s|%s|%s", clean(mi.name), clean(firstWords(desc, 20)), clean(mi.path))
		if f := mi.iface(); f != "" {
			line += "|" + f
		}
		if b.budget == readmeBudget {
			b.budget = readmeBudgetModules // con módulos el tope de CCF-doc es 400
		}
		if !b.try(line) {
			*omitted++
		}
	}
	for _, c := range id.Contracts {
		if !b.try(fmt.Sprintf("docs|%s|contrato %s", clean(c), strings.TrimSuffix(path.Base(c), path.Ext(c)))) {
			*omitted++
		}
	}
	docs := append([]Doc(nil), id.Docs...)
	sort.SliceStable(docs, func(i, j int) bool {
		di, dj := strings.Count(docs[i].Path, "/"), strings.Count(docs[j].Path, "/")
		if di != dj {
			return di < dj
		}
		return docs[i].Path < docs[j].Path
	})
	for _, d := range docs {
		if !b.try(fmt.Sprintf("docs|%s|%s", clean(d.Path), clean(firstWords(d.Title, 20)))) {
			*omitted++
		}
	}
	return withMarker(b.String(), sc.Repo, sc.SHA)
}

// deps resume de qué otros repos del producto depende este: a quién llama y
// qué tópicos escucha.
func (sc *Scan) deps(m *Map) []string {
	if m == nil {
		return nil
	}
	type dep struct {
		endpoints, topics map[string]bool
		mods              map[string]int
	}
	by := map[string]*dep{}
	for _, l := range m.Links {
		from, to := m.Entries[l.From], m.Entries[l.To]
		if from.Repo != sc.Repo || to.Repo == sc.Repo {
			continue
		}
		d := by[to.Repo]
		if d == nil {
			d = &dep{endpoints: map[string]bool{}, topics: map[string]bool{}, mods: map[string]int{}}
			by[to.Repo] = d
		}
		if to.Kind() == "event" {
			d.topics[to.Path] = true
		} else {
			d.endpoints[to.Describe()] = true
		}
		d.mods[scopeName(to.Repo, to.Module)]++
	}
	repos := make([]string, 0, len(by))
	for r := range by {
		repos = append(repos, r)
	}
	sort.Strings(repos)
	var out []string
	for _, r := range repos {
		d := by[r]
		var parts []string
		if n := len(d.endpoints); n > 0 {
			parts = append(parts, "llama "+plural(n, "endpoint", "endpoints"))
		}
		if n := len(d.topics); n > 0 {
			parts = append(parts, "escucha "+plural(n, "tópico", "tópicos"))
		}
		out = append(out, fmt.Sprintf("dep|%s|%s de %s", r, strings.Join(parts, " y "), listOf(topKeys(d.mods, 3), 3)))
	}
	return out
}

func (sc *Scan) context(mods []*modInfo, today string, omitted *int) string {
	head := fmt.Sprintf("---\ncoyote: 1\nrepo: %s\nupdated: %s\n---\n", sc.Repo, today)
	b := &budgeted{head: head, budget: contextBudget}
	for _, mi := range mods {
		if len(mi.consumers) == 0 {
			continue
		}
		users := topKeys(mi.consumers, len(mi.consumers))
		ref := "-"
		if mi.firstExposed != nil {
			ref = fmt.Sprintf("%s#L%d", mi.firstExposed.File, mi.firstExposed.Line)
		}
		line := fmt.Sprintf("inv|%s|lo usan %s; un cambio de contrato los afecta|%s", scopeName(sc.Repo, mi.path), clean(listOf(users, 3)), clean(ref))
		if !b.try(line) {
			*omitted++
		}
	}
	// Los módulos que dependen de otro repo del producto: un cambio allá los afecta.
	for _, mi := range mods {
		if len(mi.providers) == 0 {
			continue
		}
		total := 0
		for _, n := range mi.providers {
			total += n
		}
		ref := "-"
		for _, e := range sc.Entries {
			if e.Module == mi.path && (e.Role == Calls || e.Role == Listens) && !e.Unresolved {
				ref = fmt.Sprintf("%s#L%d", e.File, e.Line)
				break
			}
		}
		line := fmt.Sprintf("inv|%s|usa %s de %s; un cambio en ellos lo afecta|%s", scopeName(sc.Repo, mi.path),
			plural(total, "interfaz", "interfaces"), clean(listOf(topKeys(mi.providers, len(mi.providers)), 3)), clean(ref))
		if !b.try(line) {
			*omitted++
		}
	}
	if len(sc.Identity.Contracts) == 0 && sc.hasInterfaces() {
		b.try("gap|contratos|sin OpenAPI ni AsyncAPI: los contratos se infieren del código|-")
	}
	for _, mi := range mods {
		if mi.unres == 0 || mi.firstUnresolved == nil {
			continue
		}
		e := mi.firstUnresolved
		line := fmt.Sprintf("gap|%s|%s sin resolver: variables o propiedades que el mapa no lee|%s#L%d",
			scopeName(sc.Repo, mi.path), plural(mi.unres, "ruta o tópico", "rutas o tópicos"), clean(e.File), e.Line)
		if !b.try(line) {
			*omitted++
		}
	}
	if sc.hasInterfaces() {
		b.try("how|impacto|antes de cambiar un contrato corre coyote impact desde el proyecto del producto|-")
	}
	b.try("todo|contexto|agrega invariantes, decisiones y términos del dominio; esta propuesta solo trae lo del código|-")
	return withMarker(b.String(), sc.Repo, sc.SHA)
}

func (sc *Scan) hasInterfaces() bool {
	for _, e := range sc.Entries {
		if !e.Unresolved {
			return true
		}
	}
	return false
}

// withMarker inserta, después del frontmatter, la línea con el origen y la
// huella del documento.
func withMarker(doc, repo, sha string) string {
	sum := sha256.Sum256([]byte(doc))
	if sha == "" {
		sha = "sin-git"
	}
	marker := fmt.Sprintf("%s (%s@%s, huella %s): revísala antes de llevarla al repo", MarkerPrefix, repo, sha, hex.EncodeToString(sum[:])[:12])
	end := strings.Index(doc[4:], "\n---\n")
	if !strings.HasPrefix(doc, "---\n") || end < 0 {
		return marker + "\n" + doc
	}
	cut := 4 + end + len("\n---\n")
	return doc[:cut] + marker + "\n" + doc[cut:]
}

// StripMarker quita la línea de la huella; devuelve el documento y la huella
// que declaraba ("" si no había marcador).
func StripMarker(content string) (string, string) {
	lines := strings.SplitAfter(content, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, MarkerPrefix) {
			h := ""
			if m := huellaRe.FindStringSubmatch(l); m != nil {
				h = m[1]
			}
			return strings.Join(append(append([]string{}, lines[:i]...), lines[i+1:]...), ""), h
		}
	}
	return content, ""
}

// Edited informa si la persona cambió un documento propuesto: sin marcador,
// o con un contenido que ya no coincide con su huella.
func Edited(content string) bool {
	doc, h := StripMarker(content)
	if h == "" {
		return true
	}
	sum := sha256.Sum256([]byte(doc))
	return hex.EncodeToString(sum[:])[:12] != h
}
