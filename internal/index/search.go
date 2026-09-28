package index

import (
	"math"
	"sort"
	"strings"
)

// Pesos por clase: las invariantes y decisiones del proyecto pesan más que un
// evento suelto del ledger.
var kindWeight = map[string]float64{
	KindContext: 1.4, KindReadme: 1.2, KindADR: 1.15, KindDoc: 1.0, KindWorkstream: 0.9, KindEvent: 0.8,
}

var typeWeight = map[string]float64{"inv": 1.25, "dec": 1.15, "gap": 1.1, "risk": 1.05, "purpose": 1.1}

type bm25 struct {
	df     map[string]int
	tf     []map[string]int
	length []int
	avg    float64
}

const (
	k1 = 1.2
	b  = 0.75
)

func newBM25(chunks []Chunk) *bm25 {
	m := &bm25{df: map[string]int{}, tf: make([]map[string]int, len(chunks)), length: make([]int, len(chunks))}
	total := 0
	for i, c := range chunks {
		terms := Tokens(c.Title + " " + c.Type + " " + c.Scope + " " + c.Text)
		// El ámbito y el título cuentan doble: nombran de qué trata el fragmento.
		terms = append(terms, Tokens(c.Scope+" "+c.Title)...)
		tf := map[string]int{}
		for _, t := range terms {
			tf[t]++
		}
		for t := range tf {
			m.df[t]++
		}
		m.tf[i], m.length[i] = tf, len(terms)
		total += len(terms)
	}
	if len(chunks) > 0 {
		m.avg = float64(total) / float64(len(chunks))
	}
	return m
}

// Hit es un resultado de búsqueda.
type Hit struct {
	Chunk
	Score float64
}

// SearchOptions acota una búsqueda.
type SearchOptions struct {
	Limit int
	Kinds []string // vacío = todas
	Scope string   // prefijo de ámbito, p. ej. "pagos"
}

// Search ordena los fragmentos por relevancia BM25 para la consulta.
func (ix *Index) Search(query string, o SearchOptions) []Hit {
	q := Tokens(query)
	if len(q) == 0 || ix.bm25 == nil {
		return nil
	}
	uniq := map[string]bool{}
	var terms []string
	for _, t := range q {
		if !uniq[t] {
			uniq[t] = true
			terms = append(terms, t)
		}
	}
	allowed := map[string]bool{}
	for _, k := range o.Kinds {
		allowed[k] = true
	}
	n := float64(len(ix.Chunks))
	var hits []Hit
	for i, c := range ix.Chunks {
		if len(allowed) > 0 && !allowed[c.Kind] {
			continue
		}
		if o.Scope != "" && !InScope(c.Scope, o.Scope) {
			continue
		}
		score := 0.0
		for _, t := range terms {
			f := float64(ix.bm25.tf[i][t])
			if f == 0 {
				continue
			}
			df := float64(ix.bm25.df[t])
			idf := math.Log(1 + (n-df+0.5)/(df+0.5))
			norm := 1 - b + b*float64(ix.bm25.length[i])/math.Max(ix.bm25.avg, 1)
			score += idf * f * (k1 + 1) / (f + k1*norm)
		}
		if score == 0 {
			continue
		}
		w := kindWeight[c.Kind]
		if w == 0 {
			w = 1
		}
		if tw := typeWeight[c.Type]; tw > 0 && (c.Kind == KindContext || c.Kind == KindReadme) {
			w *= tw
		}
		hits = append(hits, Hit{Chunk: c, Score: score * w})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Ref() < hits[j].Ref()
	})
	if o.Limit > 0 && len(hits) > o.Limit {
		hits = hits[:o.Limit]
	}
	return hits
}

// InScope informa si el ámbito de un fragmento cae dentro del ámbito pedido:
// igual, o un subámbito ("pagos/reembolsos" dentro de "pagos"). Los ámbitos
// generales ("general", "-", vacío) aplican a todo.
func InScope(chunkScope, want string) bool {
	cs := strings.ToLower(fold.Replace(strings.TrimSpace(chunkScope)))
	w := strings.ToLower(fold.Replace(strings.Trim(strings.TrimSpace(want), "/")))
	switch cs {
	case "", "-", "general", "global", "todo", "all":
		return true
	}
	return cs == w || strings.HasPrefix(cs, w+"/") || strings.HasPrefix(w, cs+"/")
}
