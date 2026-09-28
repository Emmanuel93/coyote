package product

import (
	"fmt"
	"sort"
	"strings"
)

// Link une a quien consume (llama o escucha) con quien provee (expone o
// publica). Ambiguous indica que la llamada coincide igual de bien con más de
// un servicio.
type Link struct {
	From      int // consumidor, índice en Map.Entries
	To        int // proveedor
	Score     int
	Ambiguous bool
}

// Map es el mapa del producto: las interfaces de todos sus repos y los
// enlaces entre ellas.
type Map struct {
	SHAs    map[string]string
	Entries []Entry
	Links   []Link
}

// Build arma el mapa a partir de lo que se sacó de cada repo.
func Build(scans []*Scan) *Map {
	m := &Map{SHAs: map[string]string{}}
	for _, sc := range scans {
		m.SHAs[sc.Repo] = sc.SHA
		m.Entries = append(m.Entries, sc.Entries...)
	}
	m.link()
	return m
}

func methodsMatch(a, b string) bool { return a == "" || b == "" || a == b }

func (m *Map) link() {
	m.Links = nil
	var exposes, publishes []int
	for i, e := range m.Entries {
		switch e.Role {
		case Exposes:
			if !e.Unresolved && !Trivial(e.Path) {
				exposes = append(exposes, i)
			}
		case Publishes:
			if !e.Unresolved {
				publishes = append(publishes, i)
			}
		}
	}
	type pending struct {
		from, score int
		to          []int
	}
	var calls []pending
	for i, e := range m.Entries {
		if e.Unresolved {
			continue
		}
		switch e.Role {
		case Calls:
			if onlyParams(e.Path) {
				continue // sin un segmento fijo no hay con qué comparar
			}
			best, cands := -1, []int(nil)
			for _, j := range exposes {
				p := m.Entries[j]
				if p.Repo == e.Repo && p.Module == e.Module {
					continue // un servicio que se llama a sí mismo no es interfaz entre módulos
				}
				if !methodsMatch(e.Method, p.Method) {
					continue
				}
				s := MatchScore(e.Path, p.Path)
				switch {
				case s > best:
					best, cands = s, []int{j}
				case s == best && s >= 0:
					cands = append(cands, j)
				}
			}
			if best >= 0 {
				calls = append(calls, pending{i, best, cands})
			}
		case Listens:
			for _, j := range publishes {
				if m.Entries[j].Path == e.Path {
					m.Links = append(m.Links, Link{From: i, To: j, Score: 2})
				}
			}
		}
	}
	// Afinidad: si una llamada coincide igual con varios servicios (dos BFF
	// que exponen /notifications), gana el servicio con el que su módulo, o
	// si no su repo, ya habla sin ambigüedad.
	modAff := map[string]map[string]int{}
	repoAff := map[string]map[string]int{}
	bump := func(aff map[string]map[string]int, k, v string) {
		if aff[k] == nil {
			aff[k] = map[string]int{}
		}
		aff[k][v]++
	}
	for _, c := range calls {
		if len(c.to) == 1 {
			from, to := m.Entries[c.from], m.Entries[c.to[0]]
			bump(modAff, from.Repo+"|"+from.Module, to.Repo+"|"+to.Module)
			bump(repoAff, from.Repo, to.Repo+"|"+to.Module)
		}
	}
	for _, c := range calls {
		to := c.to
		if len(to) > 1 {
			from := m.Entries[c.from]
			to = m.prefer(to, modAff[from.Repo+"|"+from.Module])
			if len(to) > 1 {
				to = m.prefer(to, repoAff[from.Repo])
			}
		}
		for _, j := range to {
			m.Links = append(m.Links, Link{From: c.from, To: j, Score: c.score, Ambiguous: len(to) > 1})
		}
	}
}

// prefer deja los candidatos del módulo proveedor con más afinidad.
func (m *Map) prefer(cands []int, aff map[string]int) []int {
	best := 0
	for _, j := range cands {
		if n := aff[m.Entries[j].Repo+"|"+m.Entries[j].Module]; n > best {
			best = n
		}
	}
	if best == 0 {
		return cands
	}
	var out []int
	for _, j := range cands {
		if aff[m.Entries[j].Repo+"|"+m.Entries[j].Module] == best {
			out = append(out, j)
		}
	}
	return out
}

// onlyParams informa si una ruta no tiene ningún segmento fijo.
func onlyParams(p string) bool {
	for _, s := range strings.Split(strings.Trim(p, "/"), "/") {
		if s != "" && s != Param {
			return false
		}
	}
	return true
}

// Stats resume el mapa por repo.
type Stats struct {
	Repo       string `json:"repo"`
	Exposes    int    `json:"exposes"`
	Calls      int    `json:"calls"`
	Publishes  int    `json:"publishes"`
	Listens    int    `json:"listens"`
	Linked     int    `json:"linked"`
	Unlinked   int    `json:"unlinked"`
	Unresolved int    `json:"unresolved"`
}

// Summary cuenta interfaces, enlaces y pendientes por repo.
func (m *Map) Summary() []Stats {
	linked := map[int]bool{}
	for _, l := range m.Links {
		linked[l.From] = true
	}
	by := map[string]*Stats{}
	for i, e := range m.Entries {
		st := by[e.Repo]
		if st == nil {
			st = &Stats{Repo: e.Repo}
			by[e.Repo] = st
		}
		switch e.Role {
		case Exposes:
			st.Exposes++
		case Calls:
			st.Calls++
		case Publishes:
			st.Publishes++
		case Listens:
			st.Listens++
		}
		switch {
		case e.Unresolved:
			st.Unresolved++
		case (e.Role == Calls || e.Role == Listens) && linked[i]:
			st.Linked++
		case e.Role == Calls || e.Role == Listens:
			st.Unlinked++
		}
	}
	var out []Stats
	for _, st := range by {
		out = append(out, *st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Repo < out[j].Repo })
	return out
}

// ---- formato de archivo ----

// El mapa se guarda por repo en coyote/map/<repo>.map: una línea por
// interfaz, ordenada, para que un diff muestre qué cambió en los contratos.
//
//	# coyote map v1|<repo>|<sha>
//	expone|GET|/credit/applications/{}|services/bff|src/…/CreditController.java|42|/credit/applications/{id}
const mapHeader = "# coyote map v1"

// Encode serializa las entradas de un repo.
func Encode(repo, sha string, entries []Entry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s|%s|%s\n", mapHeader, repo, sha)
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Repo != repo {
			continue
		}
		role := e.Role
		if e.Unresolved {
			role += "?"
		}
		lines = append(lines, strings.Join([]string{role, dashIf(e.Method), clean(e.Path), e.Module, e.File, fmt.Sprint(e.Line), clean(e.Raw)}, "|"))
	}
	sort.Strings(lines)
	for _, l := range lines {
		b.WriteString(l + "\n")
	}
	return b.String()
}

func dashIf(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func clean(s string) string {
	return strings.NewReplacer("|", "¦", "\n", " ", "\r", " ").Replace(s)
}

// Decode lee un archivo de mapa.
func Decode(text string) (repo, sha string, entries []Entry, err error) {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], mapHeader+"|") {
		return "", "", nil, fmt.Errorf("no es un mapa de coyote")
	}
	h := strings.Split(lines[0], "|")
	if len(h) != 3 {
		return "", "", nil, fmt.Errorf("encabezado de mapa inválido")
	}
	repo, sha = h[1], h[2]
	for n, l := range lines[1:] {
		if strings.TrimSpace(l) == "" || strings.HasPrefix(l, "#") {
			continue
		}
		f := strings.Split(l, "|")
		if len(f) != 7 {
			return "", "", nil, fmt.Errorf("línea %d: se esperaban 7 campos", n+2)
		}
		e := Entry{Repo: repo, Role: strings.TrimSuffix(f[0], "?"), Unresolved: strings.HasSuffix(f[0], "?"),
			Method: strings.Trim(f[1], "-"), Path: f[2], Module: f[3], File: f[4], Raw: f[6]}
		fmt.Sscan(f[5], &e.Line)
		switch e.Role {
		case Exposes, Calls, Publishes, Listens:
		default:
			return "", "", nil, fmt.Errorf("línea %d: rol desconocido %q", n+2, f[0])
		}
		entries = append(entries, e)
	}
	return repo, sha, entries, nil
}

// Load arma el mapa desde las entradas guardadas en coyote/map/ y recalcula
// los enlaces, que no se guardan: se deducen siempre de las entradas.
func Load(shas map[string]string, entries []Entry) *Map {
	m := &Map{SHAs: shas, Entries: entries}
	if m.SHAs == nil {
		m.SHAs = map[string]string{}
	}
	m.link()
	return m
}

// contractKey identifica una interfaz sin su número de línea: mover código
// no cambia el contrato.
func contractKey(e Entry) string {
	u := ""
	if e.Unresolved {
		u = "?"
	}
	return strings.Join([]string{e.Role + u, e.Method, e.Path, e.Module, e.File, e.Raw}, "|")
}

// Compare devuelve las interfaces que aparecieron y las que desaparecieron
// entre dos versiones del mapa de un repo, sin contar cambios de línea.
func Compare(old, cur []Entry) (added, removed []Entry) {
	count := map[string]int{}
	for _, e := range old {
		count[contractKey(e)]++
	}
	for _, e := range cur {
		k := contractKey(e)
		if count[k] > 0 {
			count[k]--
			continue
		}
		added = append(added, e)
	}
	left := map[string]int{}
	for _, e := range cur {
		left[contractKey(e)]++
	}
	for _, e := range old {
		k := contractKey(e)
		if left[k] > 0 {
			left[k]--
			continue
		}
		removed = append(removed, e)
	}
	return added, removed
}

// Unlinked devuelve las llamadas y suscripciones que no encontraron quien las
// provea en el producto: servicios externos o huecos del extractor.
func (m *Map) Unlinked() []Entry {
	linked := map[int]bool{}
	for _, l := range m.Links {
		linked[l.From] = true
	}
	var out []Entry
	for i, e := range m.Entries {
		if (e.Role == Calls || e.Role == Listens) && !e.Unresolved && !linked[i] {
			out = append(out, e)
		}
	}
	return out
}
