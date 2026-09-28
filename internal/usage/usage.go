// Package usage agrega el consumo del ledger para FinOps: tokens de entrada,
// caché y salida, costo de entrada y salida, eventos y modelos, en dos vistas
// que se leen al revés una de la otra: proyecto > persona y persona > proyecto.
// Los cierres de workstream (close) resumen otros eventos y no se suman.
package usage

import (
	"sort"
	"strings"
	"time"

	"github.com/Emmanuel93/coyote/internal/ccf"
	"github.com/Emmanuel93/coyote/internal/ledger"
)

// Totals es el consumo acumulado de un grupo.
type Totals struct {
	Events int            `json:"events"`
	Tokens ccf.Tokens     `json:"tokens"`
	Cost   ccf.Cost       `json:"cost"`
	Models map[string]int `json:"models,omitempty"` // eventos por modelo
}

// CostTotal suma entrada y salida.
func (t Totals) CostTotal() float64 { return t.Cost.In + t.Cost.Out }

// CacheShare es la fracción de la entrada que vino de caché.
func (t Totals) CacheShare() float64 {
	if t.Tokens.In == 0 {
		return 0
	}
	return float64(t.Tokens.Cache) / float64(t.Tokens.In)
}

func (t *Totals) add(l ccf.Line) {
	t.Events++
	if l.Tokens != nil {
		t.Tokens.In += l.Tokens.In
		t.Tokens.Cache += l.Tokens.Cache
		t.Tokens.Out += l.Tokens.Out
	}
	if l.Cost != nil {
		t.Cost.In += l.Cost.In
		t.Cost.Out += l.Cost.Out
	}
	if m := Model(l); m != "" {
		if t.Models == nil {
			t.Models = map[string]int{}
		}
		t.Models[m]++
	}
}

// Row es un grupo con sus subgrupos.
type Row struct {
	Key      string `json:"key"`
	Totals   `json:"totals"`
	Children []Row `json:"children,omitempty"`
}

// Event es una línea del ledger con el repo del que viene.
type Event struct {
	Repo string
	Line ccf.Line
}

// FromLedger convierte entradas del ledger de un repo en eventos.
func FromLedger(repo string, entries []ledger.Entry) []Event {
	out := make([]Event, 0, len(entries))
	for _, e := range entries {
		r := e.Line.Repo
		if r == "" || r == "-" {
			r = repo
		}
		out = append(out, Event{Repo: r, Line: e.Line})
	}
	return out
}

// Person devuelve la persona de un actor: @ana/coyote-dev → @ana.
func Person(actor string) string {
	if i := strings.Index(actor, "/"); i > 0 {
		return actor[:i]
	}
	return actor
}

// Agent devuelve el agente de un actor, o "" si actuó la persona.
func Agent(actor string) string {
	if i := strings.Index(actor, "/"); i > 0 {
		return actor[i+1:]
	}
	return ""
}

// Model devuelve el modelo de la referencia model:… del evento.
func Model(l ccf.Line) string {
	for _, r := range l.Refs {
		if strings.HasPrefix(r, "model:") {
			return strings.TrimPrefix(r, "model:")
		}
	}
	return ""
}

// Filter deja los eventos desde since (cero = todos) y sin resúmenes.
func Filter(events []Event, since time.Time) []Event {
	var out []Event
	for _, e := range events {
		if ledger.SummaryTypes[e.Line.Type] {
			continue
		}
		if !since.IsZero() && e.Line.TS.Before(since) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// Group agrupa por dos claves: primero outer, luego inner.
func Group(events []Event, outer, inner func(Event) string) ([]Row, Totals) {
	type acc struct {
		t        Totals
		children map[string]*Totals
	}
	groups := map[string]*acc{}
	var all Totals
	for _, e := range events {
		o, i := outer(e), inner(e)
		g := groups[o]
		if g == nil {
			g = &acc{children: map[string]*Totals{}}
			groups[o] = g
		}
		g.t.add(e.Line)
		c := g.children[i]
		if c == nil {
			c = &Totals{}
			g.children[i] = c
		}
		c.add(e.Line)
		all.add(e.Line)
	}
	rows := make([]Row, 0, len(groups))
	for k, g := range groups {
		r := Row{Key: k, Totals: g.t}
		for ck, ct := range g.children {
			r.Children = append(r.Children, Row{Key: ck, Totals: *ct})
		}
		sortRows(r.Children)
		rows = append(rows, r)
	}
	sortRows(rows)
	return rows, all
}

// sortRows ordena por costo, luego por tokens y luego por nombre.
func sortRows(rows []Row) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.CostTotal() != b.CostTotal() {
			return a.CostTotal() > b.CostTotal()
		}
		if a.Tokens.In+a.Tokens.Out != b.Tokens.In+b.Tokens.Out {
			return a.Tokens.In+a.Tokens.Out > b.Tokens.In+b.Tokens.Out
		}
		return a.Key < b.Key
	})
}

// Claves de agrupación.
var (
	ByRepo   = func(e Event) string { return e.Repo }
	ByPerson = func(e Event) string { return Person(e.Line.Actor) }
	ByModel  = func(e Event) string {
		if m := Model(e.Line); m != "" {
			return m
		}
		return "sin modelo"
	}
	ByAgent = func(e Event) string {
		if a := Agent(e.Line.Actor); a != "" {
			return a
		}
		return "persona"
	}
)
