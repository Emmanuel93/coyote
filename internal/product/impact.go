package product

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// Sources resuelve cada repo a su carpeta local (de solo lectura).
type Sources map[string]string

// Query describe un cambio: un endpoint, un tópico, los cambios de uno o
// varios repos (diff o archivos) o un texto libre.
type Query struct {
	Endpoint string // "POST /credit/applications" o "/credit/applications/{id}"
	Topic    string
	Repo     string // un solo repo, con Diff o Files
	Diff     string // rango de git: main...rama, HEAD~3..HEAD
	Files    []string
	Changes  []Change // varios repos a la vez: un cambio coordinado
	Text     string
}

// Change es el cambio de un repo: un rango de git o una lista de archivos.
type Change struct {
	Repo  string
	Diff  string
	Files []string
}

func (c Change) String() string {
	if c.Diff != "" {
		return fmt.Sprintf("diff %s en %s", c.Diff, c.Repo)
	}
	return fmt.Sprintf("%d archivos de %s", len(c.Files), c.Repo)
}

// changes junta el cambio de un repo y los de varios.
func (q Query) changes() []Change {
	out := append([]Change(nil), q.Changes...)
	if q.Repo != "" && (q.Diff != "" || len(q.Files) > 0) {
		out = append([]Change{{Repo: q.Repo, Diff: q.Diff, Files: q.Files}}, out...)
	}
	return out
}

// String describe el cambio en una línea.
func (q Query) String() string {
	switch {
	case q.Endpoint != "":
		return "endpoint " + q.Endpoint
	case q.Topic != "":
		return "tópico " + q.Topic
	case len(q.changes()) > 0:
		var parts []string
		for _, c := range q.changes() {
			parts = append(parts, c.String())
		}
		return strings.Join(parts, "; ")
	}
	return "texto: " + q.Text
}

// Hit es una interfaz afectada, con el porqué.
type Hit struct {
	Entry     Entry
	Why       string
	Ambiguous bool
	Breaking  bool // usa algo que el cambio elimina
}

// Impact es el resultado de cruzar un cambio contra el mapa.
type Impact struct {
	Query      string
	Touched    []Hit // interfaces que el cambio toca
	Direct     []Hit // quienes las usan o las proveen, en otros módulos
	Indirect   []Hit // quienes las usan a través de un servicio intermedio (un BFF)
	Unresolved []Entry
	Repos      []string
	Notes      []string
}

// Describe devuelve "GET /x" o el tópico.
func (e Entry) Describe() string {
	if e.Kind() == "event" {
		return e.Path
	}
	if e.Method == "" {
		return e.Path
	}
	return e.Method + " " + e.Path
}

// Impact cruza el cambio contra el mapa.
func (m *Map) Impact(q Query, src Sources) (*Impact, error) {
	im := &Impact{Query: q.String()}
	rd := newReader()
	touched := map[int]string{}
	switch {
	case q.Endpoint != "":
		method, p := "", q.Endpoint
		if f := strings.Fields(q.Endpoint); len(f) == 2 {
			method, p = strings.ToUpper(f[0]), f[1]
		}
		norm := NormPath(p)
		if norm == "" {
			return nil, fmt.Errorf("%q no parece una ruta", q.Endpoint)
		}
		best := -1
		var cands []int
		for i, e := range m.Entries {
			if e.Role != Exposes || !methodsMatch(method, e.Method) {
				continue
			}
			s := MatchScore(norm, e.Path)
			switch {
			case s > best:
				best, cands = s, []int{i}
			case s == best && s >= 0:
				cands = append(cands, i)
			}
		}
		for _, i := range cands {
			touched[i] = "expone el endpoint"
		}
		if len(cands) == 0 {
			for i, e := range m.Entries {
				if e.Role == Calls && e.Path == norm && methodsMatch(method, e.Method) {
					touched[i] = "llama el endpoint"
				}
			}
			im.Notes = append(im.Notes, "ningún servicio del producto expone "+norm+"; se muestran quienes lo llaman")
		}
	case q.Topic != "":
		for i, e := range m.Entries {
			if e.Kind() == "event" && e.Path == q.Topic {
				touched[i] = map[string]string{Publishes: "publica el tópico", Listens: "escucha el tópico"}[e.Role]
			}
		}
	case len(q.changes()) > 0:
		type prepared struct {
			repo, dir, rev string
			ranges         map[string][]lineRange
		}
		var preps []prepared
		var extra []Entry
		marks := map[string]string{} // contractID → qué le hace el diff
		for _, c := range q.changes() {
			dir := src[c.Repo]
			if dir == "" {
				return nil, fmt.Errorf("no conozco la carpeta del repo %q", c.Repo)
			}
			ch, err := changedLines(dir, c.Diff, c.Files)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", c.Repo, err)
			}
			if len(ch.old)+len(ch.new) == 0 {
				im.Notes = append(im.Notes, c.String()+": no hay cambios")
				continue
			}
			side, rev, note := m.sideFor(dir, c)
			if note != "" {
				im.Notes = append(im.Notes, note)
			}
			preps = append(preps, prepared{c.Repo, dir, rev, ch.pick(side)})
			if c.Diff == "" {
				continue
			}
			// Lo que el diff agrega o elimina del contrato, aunque el mapa sea
			// de otro commit: se compara cada archivo antes y después.
			left, right, worktree := diffSides(dir, c.Diff)
			added, removed := m.contractChanges(rd, dir, c.Repo, ch, left, right, worktree)
			known := map[string]bool{}
			for _, e := range m.Entries {
				known[contractID(e)] = true
			}
			for _, e := range removed {
				marks[contractID(e)] = "se elimina en " + e.File
				if !known[contractID(e)] {
					extra = append(extra, e)
				}
			}
			for _, e := range added {
				marks[contractID(e)] = "se agrega en " + e.File
				if !known[contractID(e)] {
					extra = append(extra, e)
				}
			}
		}
		if len(extra) > 0 {
			m = Load(m.SHAs, append(append([]Entry(nil), m.Entries...), extra...))
		}
		for _, p := range preps {
			m.touchByFiles(rd, p.repo, p.dir, p.rev, p.ranges, touched, im)
		}
		for i, e := range m.Entries {
			if why, ok := marks[contractID(e)]; ok {
				touched[i] = why
			}
		}
	default:
		m.touchByText(q.Text, touched)
		im.Notes = append(im.Notes, "búsqueda por texto: revisa que las interfaces encontradas sean las del cambio")
	}
	if len(touched) == 0 {
		im.Notes = append(im.Notes, "el cambio no toca ninguna interfaz conocida del producto")
	}
	idx := make([]int, 0, len(touched))
	for i := range touched {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	seen := map[int]bool{}
	for _, i := range idx {
		im.Touched = append(im.Touched, Hit{Entry: m.Entries[i], Why: touched[i]})
		seen[i] = true
	}
	// Directo: quienes consumen lo que el cambio provee, y a quién llama lo que el cambio consume.
	direct := map[int]Hit{}
	for _, l := range m.Links {
		if _, ok := touched[l.To]; ok && !seen[l.From] {
			to := m.Entries[l.To]
			why := "usa " + label(to)
			breaking := strings.HasPrefix(touched[l.To], "se elimina")
			switch {
			case breaking && to.Kind() == "event":
				why = "escucha " + to.Path + ", que " + label(to) + " deja de publicar"
			case breaking:
				why = "llama " + to.Describe() + ", que el cambio elimina de " + label(to)
			case to.Kind() == "event":
				why = "escucha lo que publica " + label(to)
			}
			direct[l.From] = Hit{Entry: m.Entries[l.From], Why: why, Ambiguous: l.Ambiguous, Breaking: breaking}
		}
		if _, ok := touched[l.From]; ok && !seen[l.To] {
			from := m.Entries[l.From]
			why := "atiende la llamada de " + from.Repo + ": " + ModuleName(from.Module)
			if from.Kind() == "event" {
				why = "publica lo que escucha " + from.Repo + ": " + ModuleName(from.Module)
			}
			direct[l.To] = Hit{Entry: m.Entries[l.To], Why: why, Ambiguous: l.Ambiguous}
		}
	}
	for _, i := range sortedKeys(direct) {
		im.Direct = append(im.Direct, direct[i])
		seen[i] = true
	}
	// Indirecto: un consumidor directo que vive en un servicio que también
	// expone endpoints (un BFF) propaga el cambio a sus propios clientes.
	indirect := map[int]Hit{}
	graphs := map[string]*mentionGraph{}
	for _, i := range sortedKeys(direct) {
		h := direct[i]
		e := h.Entry
		if e.Role != Calls || !m.moduleExposes(e.Repo, e.Module) {
			continue
		}
		dir := src[e.Repo]
		if dir == "" {
			im.Notes = append(im.Notes, "sin la carpeta de "+e.Repo+" no se sigue el impacto a través de "+ModuleName(e.Module))
			continue
		}
		key := e.Repo + "|" + e.Module
		g := graphs[key]
		if g == nil {
			g = newMentionGraph(rd, dir, e.Module)
			graphs[key] = g
		}
		// Por método: el endpoint del módulo cuya implementación llega, por
		// llamadas, al método que hace la llamada afectada. Si no se puede
		// seguir por método, se cae a los archivos que usan al cliente.
		exposedIn := map[string][]Entry{}
		for _, x := range m.Entries {
			if x.Repo == e.Repo && x.Module == e.Module && x.Role == Exposes {
				exposedIn[x.File] = append(exposedIn[x.File], x)
			}
		}
		endpoints, precise := g.endpointsReaching(e.File, e.Line, exposedIn, 3)
		if !precise {
			files := g.usedBy(e.File, 3)
			for f := range files {
				endpoints = append(endpoints, exposedIn[f]...)
			}
		}
		for _, x := range endpoints {
			for _, l := range m.Links {
				to := m.Entries[l.To]
				if to.Repo != x.Repo || to.File != x.File || to.Line != x.Line || to.Path != x.Path || to.Method != x.Method || seen[l.From] {
					continue
				}
				why := fmt.Sprintf("llama %s de %s, que usa %s", x.Describe(), ModuleName(x.Module), e.Describe())
				if !precise {
					why += " (posible: se siguió por archivo)"
				}
				indirect[l.From] = Hit{Entry: m.Entries[l.From], Ambiguous: l.Ambiguous, Why: why}
			}
		}
	}
	for _, i := range sortedKeys(indirect) {
		im.Indirect = append(im.Indirect, indirect[i])
	}
	// Lo que no se pudo leer en los módulos afectados.
	mods := map[string]bool{}
	repos := map[string]bool{}
	for _, list := range [][]Hit{im.Touched, im.Direct, im.Indirect} {
		for _, h := range list {
			mods[h.Entry.Repo+"|"+h.Entry.Module] = true
			repos[h.Entry.Repo] = true
		}
	}
	for _, e := range m.Entries {
		if e.Unresolved && mods[e.Repo+"|"+e.Module] {
			im.Unresolved = append(im.Unresolved, e)
		}
	}
	for r := range repos {
		im.Repos = append(im.Repos, r)
	}
	sort.Strings(im.Repos)
	return im, nil
}

func sortedKeys(m map[int]Hit) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

func (m *Map) moduleExposes(repo, mod string) bool {
	for _, e := range m.Entries {
		if e.Repo == repo && e.Module == mod && e.Role == Exposes {
			return true
		}
	}
	return false
}

// touchByText busca interfaces cuyo texto comparte palabras con la consulta.
func (m *Map) touchByText(text string, touched map[int]string) {
	words := wordsOf(text)
	if len(words) == 0 {
		return
	}
	type scored struct{ i, s int }
	var all []scored
	for i, e := range m.Entries {
		have := map[string]bool{}
		for _, w := range wordsOf(e.Path + " " + e.Module + " " + e.Raw) {
			have[w] = true
		}
		s := 0
		for _, w := range words {
			if have[w] {
				s++
			}
		}
		if s > 0 {
			all = append(all, scored{i, s})
		}
	}
	sort.SliceStable(all, func(a, b int) bool { return all[a].s > all[b].s })
	for n, x := range all {
		if n == 12 || x.s < all[0].s-1 {
			break
		}
		touched[x.i] = "coincide con el texto"
	}
}

var splitRe = regexp.MustCompile(`[^\p{L}\p{N}]+`)

func wordsOf(s string) []string {
	var out []string
	for _, w := range splitRe.Split(strings.ToLower(s), -1) {
		if len(w) < 3 {
			continue
		}
		w = strings.TrimSuffix(w, "es")
		w = strings.TrimSuffix(w, "s")
		out = append(out, w)
	}
	return out
}

// ---- menciones entre archivos de un módulo ----

// mentionGraph sabe qué archivos de un módulo mencionan los tipos declarados
// en otros: sirve para seguir un cambio desde un cliente hasta los endpoints
// que lo usan.
type mentionGraph struct {
	types    map[string][]string // archivo → tipos que declara
	texts    map[string]string
	mentions map[string][]string // archivo → archivos que lo mencionan
}

func newMentionGraph(rd *reader, dir, mod string) *mentionGraph {
	g := &mentionGraph{types: map[string][]string{}, texts: map[string]string{}, mentions: map[string][]string{}}
	for _, rel := range rd.moduleFiles(dir, mod) {
		ext := path.Ext(rel)
		if (ext != ".java" && ext != ".kt") || isTestPath(rel) {
			continue
		}
		data, ok := readRegular(dir, rel)
		if !ok {
			continue
		}
		g.texts[rel] = string(data)
		for _, m := range typeDeclRe.FindAllStringSubmatch(string(data), -1) {
			g.types[rel] = append(g.types[rel], m[1])
		}
	}
	// Índice identificador → archivos que lo mencionan: un recorrido por
	// archivo en lugar de una expresión por tipo y archivo.
	uses := map[string][]string{}
	for f, text := range g.texts {
		for w := range identifiers(text) {
			uses[w] = append(uses[w], f)
		}
	}
	for f, ts := range g.types {
		added := map[string]bool{}
		for _, t := range ts {
			for _, other := range uses[t] {
				if other != f && !added[other] {
					added[other] = true
					g.mentions[f] = append(g.mentions[f], other)
				}
			}
		}
		sort.Strings(g.mentions[f])
	}
	return g
}

var identRe = regexp.MustCompile(`\b[A-Z][A-Za-z0-9_]*`)

// identifiers devuelve los nombres con mayúscula inicial (tipos) de un texto.
func identifiers(text string) map[string]bool {
	out := map[string]bool{}
	for _, w := range identRe.FindAllString(text, -1) {
		out[w] = true
	}
	return out
}

// usedBy devuelve los archivos que usan, directa o indirectamente (hasta
// depth saltos), a file, incluido file.
func (g *mentionGraph) usedBy(file string, depth int) map[string]bool {
	out := map[string]bool{file: true}
	frontier := []string{file}
	for d := 0; d < depth && len(frontier) > 0; d++ {
		var next []string
		for _, f := range frontier {
			for _, u := range g.mentions[f] {
				if !out[u] {
					out[u] = true
					next = append(next, u)
				}
			}
		}
		frontier = next
	}
	return out
}

var (
	// methodDeclRe reconoce la declaración de un método en Java o Kotlin.
	methodDeclRe = regexp.MustCompile(`^\s*(?:@\w+(?:\([^)]*\))?\s*)*(?:(?:public|protected|private|static|final|abstract|synchronized|override|suspend|open|internal|default)\s+)*(?:fun\s+|[\w<>\[\],.?]+(?:\s*<[^>]*>)?\s+)([a-z][A-Za-z0-9_]*)\s*\(`)
	notMethod    = map[string]bool{"if": true, "for": true, "while": true, "switch": true, "catch": true, "return": true, "new": true, "else": true, "synchronized": true}
)

// enclosingMethod devuelve el método que contiene la línea (desde 1) de un
// archivo, buscando hacia arriba la última declaración.
func enclosingMethod(text string, line int) string {
	lines := strings.Split(text, "\n")
	if line > len(lines) {
		line = len(lines)
	}
	for i := line - 1; i >= 0; i-- {
		if name, ok := isDecl(lines[i]); ok {
			return name
		}
	}
	return ""
}

// isDecl informa si una línea declara un método y devuelve su nombre. Las
// sentencias (terminan en ; o empiezan con return) y los comentarios no cuentan.
func isDecl(l string) (string, bool) {
	t := strings.TrimSpace(l)
	if strings.HasPrefix(t, "return") || strings.HasSuffix(t, ";") || strings.HasPrefix(t, "//") || strings.HasPrefix(t, "*") ||
		strings.HasPrefix(t, "throw") || strings.HasPrefix(t, "else") {
		return "", false
	}
	if m := methodDeclRe.FindStringSubmatch(l); m != nil && !notMethod[m[1]] {
		return m[1], true
	}
	return "", false
}

// endpointsReaching sigue por nombre de método, hasta depth saltos, quién
// llama al método que contiene la línea: si un endpoint expuesto en el
// módulo contiene una de esas llamadas, es afectado. precise es false si no
// se pudo identificar el método de partida.
func (g *mentionGraph) endpointsReaching(file string, line int, exposedIn map[string][]Entry, depth int) ([]Entry, bool) {
	text, ok := g.texts[file]
	if !ok {
		return nil, false
	}
	start := enclosingMethod(text, line)
	if start == "" {
		return nil, false
	}
	type site struct{ file, method string }
	var out []Entry
	seen := map[string]bool{}
	frontier := []site{{file, start}}
	visited := map[site]bool{{file, start}: true}
	// Un endpoint cuyo método es el de partida también cuenta.
	addEndpointAt := func(f string, ln int) {
		// la última anotación antes de la llamada; una anotación puede declarar varias rutas
		bestLine := 0
		for _, x := range exposedIn[f] {
			if x.Line <= ln && x.Line > bestLine {
				bestLine = x.Line
			}
		}
		for _, x := range exposedIn[f] {
			if x.Line != bestLine || bestLine == 0 {
				continue
			}
			k := fmt.Sprintf("%s:%d:%s:%s", x.File, x.Line, x.Method, x.Path)
			if !seen[k] {
				seen[k] = true
				out = append(out, x)
			}
		}
	}
	for d := 0; d < depth && len(frontier) > 0; d++ {
		var next []site
		for _, s := range frontier {
			call := regexp.MustCompile(`\.` + regexp.QuoteMeta(s.method) + `\s*\(`)
			for _, user := range g.mentions[s.file] {
				ut := g.texts[user]
				for _, loc := range call.FindAllStringIndex(ut, -1) {
					ln := lineOf(ut, loc[0])
					addEndpointAt(user, ln)
					if m := enclosingMethod(ut, ln); m != "" {
						n := site{user, m}
						if !visited[n] {
							visited[n] = true
							next = append(next, n)
						}
					}
				}
			}
		}
		frontier = next
	}
	return out, true
}
