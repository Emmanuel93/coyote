// Package golden mide la calidad del contexto que entrega coyote con un
// golden: preguntas con las fuentes que deben aparecer (ADR-0025). Las
// métricas no usan ningún modelo: dicen si la fuente llega al agente, no si
// el agente la usa bien.
package golden

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"path"
	"regexp"
	"strings"

	"github.com/Emmanuel93/coyote/internal/fsx"
	"github.com/Emmanuel93/coyote/internal/index"
)

// Question es una línea del golden.
type Question struct {
	ID     string   `json:"id"`
	Q      string   `json:"q"`
	Scope  string   `json:"scope,omitempty"`
	Expect []string `json:"expect"`
	K      int      `json:"k,omitempty"`
}

// MaxFile es el tope de un golden o de una línea base: miles de preguntas
// caben de sobra, y /dev/zero no cuelga a eval.
const MaxFile = 4 << 20

// Load lee un golden en JSONL de hasta MaxFile bytes.
func Load(path string) ([]Question, error) {
	data, err := fsx.ReadCapped(path, MaxFile)
	if err != nil {
		return nil, err
	}
	return Parse(path, data)
}

// Parse lee un golden en JSONL: una pregunta por línea; las líneas vacías y
// las que empiezan con # se saltan. Un golden sin preguntas es un error: con
// él, cualquier línea base pasaría.
func Parse(path string, data []byte) ([]Question, error) {
	var out []Question
	seen := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var q Question
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&q); err != nil {
			return nil, fmt.Errorf("%s:%d: %v", path, n, err)
		}
		switch {
		case q.ID == "" || strings.TrimSpace(q.Q) == "" || len(q.Expect) == 0:
			return nil, fmt.Errorf("%s:%d: cada pregunta necesita id, q y expect", path, n)
		case seen[q.ID]:
			return nil, fmt.Errorf("%s:%d: id repetido %q", path, n, q.ID)
		}
		for _, e := range q.Expect {
			if _, _, ok := parseRef(e); !ok {
				return nil, fmt.Errorf("%s:%d: referencia %q inválida; usa doc:ruta, adr:ADR-0000 o ctx:ámbito", path, n, e)
			}
		}
		if q.K <= 0 {
			q.K = 5
		}
		seen[q.ID] = true
		out = append(out, q)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s no tiene preguntas", path)
	}
	return out, nil
}

var adrRef = regexp.MustCompile(`^ADR-[0-9]{4}$`)

// parseRef valida una referencia: doc:ruta del repo, adr:ADR-0000 o
// ctx:ámbito. Una referencia laxa (adr:-, doc:../x) haría pasar en falso.
func parseRef(e string) (kind, val string, ok bool) {
	kind, val, ok = strings.Cut(e, ":")
	if !ok || strings.TrimSpace(val) != val || val == "" {
		return kind, val, false
	}
	switch kind {
	case "doc":
		clean := path.Clean(val)
		ok = clean == val && !path.IsAbs(val) && val != "." && !strings.HasPrefix(val, "../")
	case "adr":
		ok = adrRef.MatchString(val)
	case "ctx":
		ok = !strings.ContainsAny(val, " \t")
	default:
		ok = false
	}
	return
}

// adrID es el número de un ADR por el nombre de su archivo (ADR-0009-gate.md).
func adrID(p string) string {
	base := path.Base(p)
	if len(base) >= 8 && adrRef.MatchString(base[:8]) && (len(base) == 8 || base[8] == '-' || base[8] == '.') {
		return base[:8]
	}
	return ""
}

// Matches dice si un fragmento es la fuente esperada.
func Matches(c index.Chunk, ref string) bool {
	kind, val, ok := parseRef(ref)
	if !ok {
		return false
	}
	switch kind {
	case "doc":
		return c.Path == val
	case "adr":
		return c.Kind == index.KindADR && adrID(c.Path) == val
	case "ctx":
		return c.Kind == index.KindContext && index.ScopeWithin(c.Scope, val)
	}
	return false
}

// Result es la medida de una pregunta.
type Result struct {
	ID       string   `json:"id"`
	Recall   float64  `json:"recall"`   // parte de lo esperado entre los k primeros de ask
	RR       float64  `json:"rr"`       // 1/puesto de lo primero esperado, 0 si no sale en 10
	Coverage float64  `json:"coverage"` // parte de lo esperado dentro del paquete
	Missing  []string `json:"missing,omitempty"`
}

// Summary son las métricas del golden, en puntos porcentuales.
type Summary struct {
	Questions int     `json:"questions"`
	Recall    float64 `json:"recall_at_k"`
	MRR       float64 `json:"mrr"`
	Coverage  float64 `json:"pack_coverage"`
}

// Run mide cada pregunta contra el índice con el presupuesto de paquete dado.
func Run(ix *index.Index, repo string, qs []Question, budget int) ([]Result, Summary) {
	var out []Result
	var sum Summary
	for _, q := range qs {
		hits := ix.Search(q.Q, index.SearchOptions{Scope: q.Scope})
		r := Result{ID: q.ID}
		found := 0
		for _, e := range q.Expect {
			in := false
			for i, h := range hits {
				if i >= q.K {
					break
				}
				if Matches(h.Chunk, e) {
					in = true
					break
				}
			}
			if in {
				found++
			} else {
				r.Missing = append(r.Missing, e)
			}
		}
		r.Recall = float64(found) / float64(len(q.Expect))
		for i, h := range hits {
			if i >= 10 {
				break
			}
			if matchesAny(h.Chunk, q.Expect) {
				r.RR = 1 / float64(i+1)
				break
			}
		}
		p := ix.Pack(repo, index.PackOptions{Query: q.Q, Scope: q.Scope, Budget: budget})
		inPack := 0
		for _, e := range q.Expect {
			if packHas(p, e) {
				inPack++
			}
		}
		r.Coverage = float64(inPack) / float64(len(q.Expect))
		sum.Recall += r.Recall
		sum.MRR += r.RR
		sum.Coverage += r.Coverage
		out = append(out, r)
	}
	if n := float64(len(qs)); n > 0 {
		sum.Questions = len(qs)
		sum.Recall = round(100 * sum.Recall / n)
		sum.MRR = round(100 * sum.MRR / n)
		sum.Coverage = round(100 * sum.Coverage / n)
	}
	return out, sum
}

func matchesAny(c index.Chunk, refs []string) bool {
	for _, e := range refs {
		if Matches(c, e) {
			return true
		}
	}
	return false
}

// packHas dice si alguna línea del paquete, dentro del presupuesto, sale de
// la fuente esperada.
func packHas(p *index.Pack, ref string) bool {
	for _, s := range p.Fitted() {
		for _, it := range s.Items {
			if Matches(it.Chunk, ref) {
				return true
			}
		}
	}
	return false
}

func round(v float64) float64 { return math.Round(v*10) / 10 }

// Regression compara contra la línea base y devuelve las métricas que
// bajaron más de tol puntos.
func Regression(base, now Summary, tol float64) []string {
	var out []string
	check := func(name string, b, n float64) {
		if n < b-tol {
			out = append(out, fmt.Sprintf("%s bajó de %.1f a %.1f", name, b, n))
		}
	}
	check("recall@k", base.Recall, now.Recall)
	check("MRR", base.MRR, now.MRR)
	check("cobertura del paquete", base.Coverage, now.Coverage)
	return out
}
