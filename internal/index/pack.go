package index

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Emmanuel93/coyote/internal/tokens"
)

// PackOptions define qué contexto pedir.
type PackOptions struct {
	Scope  string // ámbito: módulo o tema, p. ej. "pagos"
	Query  string // consulta libre para ordenar ADRs, documentos y entradas
	Budget int    // tope de tokens del paquete (estimador conservador)
	Events int    // eventos recientes del ledger a incluir
	Now    time.Time
}

// Item es una línea del paquete, ya formateada, con su costo en tokens.
type Item struct {
	Chunk
	Line   string
	Tokens int
}

// Section agrupa los ítems de un tipo de contexto.
type Section struct {
	Title string
	Items []Item
}

// Pack es un paquete de contexto acotado para un agente o una persona.
type Pack struct {
	Repo     string
	Scope    string
	Query    string
	Budget   int
	Used     int
	Sections []Section
	Omitted  int
	Built    time.Time
}

// tiers define el orden en que se llena el presupuesto: primero lo que no se
// puede romper, después lo que orienta y al final lo que se puede consultar.
var tiers = []struct {
	title string
	pick  func(c Chunk) bool
}{
	{"Identidad", func(c Chunk) bool {
		return c.Kind == KindReadme && (c.Type == "purpose" || c.Type == "run" || c.Type == "test" || c.Type == "build" || c.Type == "entry")
	}},
	{"Invariantes (no se rompen)", func(c Chunk) bool { return c.Kind == KindContext && c.Type == "inv" }},
	{"Decisiones vigentes", func(c Chunk) bool { return c.Kind == KindContext && c.Type == "dec" }},
	{"Trampas y riesgos", func(c Chunk) bool { return c.Kind == KindContext && (c.Type == "gap" || c.Type == "risk") }},
	{"Cómo hacer y términos", func(c Chunk) bool {
		return c.Kind == KindContext && (c.Type == "how" || c.Type == "term" || c.Type == "todo")
	}},
	{"Módulos y dependencias", func(c Chunk) bool {
		return c.Kind == KindReadme && (c.Type == "mod" || c.Type == "dep" || c.Type == "docs")
	}},
}

// Pack arma el paquete de contexto del proyecto dentro del presupuesto.
func (ix *Index) Pack(repo string, o PackOptions) *Pack {
	if o.Budget <= 0 {
		o.Budget = 2000
	}
	if o.Events <= 0 {
		o.Events = 8
	}
	if o.Now.IsZero() {
		o.Now = time.Now().UTC()
	}
	p := &Pack{Repo: repo, Scope: o.Scope, Query: o.Query, Budget: o.Budget, Built: o.Now}
	p.Used = tokens.Estimate(p.header())
	rank := map[string]float64{}
	if q := strings.TrimSpace(o.Query + " " + o.Scope); q != "" {
		for _, h := range ix.Search(q, SearchOptions{}) {
			rank[h.Ref()] = h.Score
		}
	}
	relevant := func(c Chunk) bool {
		if o.Scope != "" && c.Kind != KindReadme && !InScope(c.Scope, o.Scope) {
			return false
		}
		if o.Scope != "" && c.Kind == KindReadme && c.Type == "mod" && !InScope(c.Scope, o.Scope) && rank[c.Ref()] == 0 {
			return false
		}
		return true
	}
	for i, t := range tiers {
		var cands []Chunk
		for _, c := range ix.Chunks {
			if t.pick(c) && relevant(c) {
				cands = append(cands, c)
			}
		}
		// Con consulta, dentro de cada grupo va primero lo más relevante; las
		// invariantes del ámbito entran todas, sea cual sea la consulta.
		if o.Query != "" && i > 1 {
			sort.SliceStable(cands, func(a, b int) bool { return rank[cands[a].Ref()] > rank[cands[b].Ref()] })
		}
		p.add(t.title, cands, func(c Chunk) string { return lineFor(c) })
	}
	// ADRs y documentos: solo los relevantes para el ámbito o la consulta; sin
	// ninguno de los dos, la lista de ADRs con su estado.
	if o.Query != "" || o.Scope != "" {
		q := strings.TrimSpace(o.Query + " " + o.Scope)
		p.add("Decisiones de arquitectura (ADR)", hitsChunks(ix.Search(q, SearchOptions{Kinds: []string{KindADR}, Limit: 6})), lineFor)
		p.add("Documentos relacionados", hitsChunks(ix.Search(q, SearchOptions{Kinds: []string{KindDoc, KindWorkstream}, Limit: 6})), lineFor)
	} else {
		p.add("Decisiones de arquitectura (ADR)", adrTitles(ix.Chunks), func(c Chunk) string {
			return fmt.Sprintf("- %s (%s) · %s", c.Title, orDash(c.Type), c.Path)
		})
	}
	var events []Chunk
	for _, c := range ix.Chunks {
		if c.Kind == KindEvent && (o.Scope == "" || InScope(c.Scope, o.Scope)) {
			events = append(events, c)
		}
	}
	sort.SliceStable(events, func(a, b int) bool { return events[a].Time.After(events[b].Time) })
	if len(events) > o.Events {
		events = events[:o.Events]
	}
	p.add("Actividad reciente", events, func(c Chunk) string {
		return fmt.Sprintf("- %s %s %s %s: %s", c.Time.Format("2006-01-02"), c.Title, c.Type, orDash(c.Scope), c.Text)
	})
	return p
}

func (p *Pack) add(title string, cands []Chunk, format func(Chunk) string) {
	var items []Item
	headCost := tokens.Estimate("## " + title)
	for _, c := range cands {
		line := format(c)
		cost := tokens.Estimate(line)
		extra := cost
		if len(items) == 0 {
			extra += headCost
		}
		if p.Used+extra > p.Budget {
			p.Omitted++
			continue
		}
		p.Used += extra
		items = append(items, Item{Chunk: c, Line: line, Tokens: cost})
	}
	if len(items) > 0 {
		p.Sections = append(p.Sections, Section{Title: title, Items: items})
	}
}

func hitsChunks(hits []Hit) []Chunk {
	out := make([]Chunk, len(hits))
	for i, h := range hits {
		out[i] = h.Chunk
	}
	return out
}

// adrTitles devuelve un fragmento por ADR (el primero de cada archivo).
func adrTitles(chunks []Chunk) []Chunk {
	seen := map[string]bool{}
	var out []Chunk
	for _, c := range chunks {
		if c.Kind != KindADR || seen[c.Path] {
			continue
		}
		seen[c.Path] = true
		t := c.Title
		if i := strings.Index(t, " — "); i > 0 {
			t = t[:i]
		}
		c.Title = t
		out = append(out, c)
	}
	return out
}

// maxItemWords acota el texto de un documento o ADR dentro del paquete.
const maxItemWords = 60

func lineFor(c Chunk) string {
	text := c.Text
	if w := strings.Fields(text); len(w) > maxItemWords {
		text = strings.Join(w[:maxItemWords], " ") + " …"
	}
	switch c.Kind {
	case KindReadme:
		switch c.Type {
		case "purpose":
			return "- Propósito: " + text
		case "run", "test", "build":
			label := map[string]string{"run": "Correr", "test": "Probar", "build": "Construir"}[c.Type]
			return fmt.Sprintf("- %s: `%s`", label, text)
		case "mod":
			return fmt.Sprintf("- Módulo %s (`%s`): %s", c.Scope, c.Title, text)
		}
		label := map[string]string{"entry": "Entrada", "docs": "Docs", "dep": "Depende de"}[c.Type]
		if label == "" {
			label = c.Type
		}
		if first, rest, ok := strings.Cut(text, " · "); ok {
			return fmt.Sprintf("- %s `%s`: %s", label, first, rest)
		}
		return fmt.Sprintf("- %s: %s", label, text)
	case KindContext:
		ref := ""
		if c.Title != "" {
			ref = " (" + c.Title + ")"
		}
		return fmt.Sprintf("- [%s] %s%s", orDash(c.Scope), text, ref)
	}
	return fmt.Sprintf("- %s: %s (%s)", c.Title, text, c.Ref())
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func (p *Pack) header() string {
	h := "# Contexto: " + p.Repo
	if p.Scope != "" {
		h += " · ámbito " + p.Scope
	}
	if p.Query != "" {
		h += " · consulta \"" + p.Query + "\""
	}
	return h + "\n~0000/0000 tokens · fuente: git (índice local)\n"
}

// Markdown devuelve el paquete para personas y agentes.
func (p *Pack) Markdown() string {
	var b strings.Builder
	title := "# Contexto: " + p.Repo
	if p.Scope != "" {
		title += " · ámbito " + p.Scope
	}
	if p.Query != "" {
		title += " · consulta \"" + p.Query + "\""
	}
	fmt.Fprintf(&b, "%s\n~%d/%d tokens · fuente: git (índice local) · %s\n", title, p.Used, p.Budget, p.Built.Format("2006-01-02 15:04Z"))
	for _, s := range p.Sections {
		fmt.Fprintf(&b, "\n## %s\n", s.Title)
		for _, it := range s.Items {
			b.WriteString(it.Line + "\n")
		}
	}
	if p.Omitted > 0 {
		fmt.Fprintf(&b, "\n_%d fragmentos no cupieron en el presupuesto; pide más con --budget._\n", p.Omitted)
	}
	return b.String()
}

// CCF devuelve el paquete en líneas compactas tipo|ámbito|texto|ref.
func (p *Pack) CCF() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# contexto|%s|%s|%s|~%d/%d tokens\n", p.Repo, orDash(p.Scope), strings.ReplaceAll(orDash(p.Query), "|", "/"), p.Used, p.Budget)
	for _, s := range p.Sections {
		for _, it := range s.Items {
			typ := it.Type
			if it.Kind == KindADR || it.Kind == KindDoc || it.Kind == KindWorkstream {
				typ = it.Kind
			}
			if it.Kind == KindEvent {
				typ = "evt:" + it.Type
			}
			text := strings.ReplaceAll(strings.TrimPrefix(it.Line, "- "), "|", "/")
			fmt.Fprintf(&b, "%s|%s|%s|%s\n", orDash(typ), orDash(it.Scope), text, it.Ref())
		}
	}
	return b.String()
}
