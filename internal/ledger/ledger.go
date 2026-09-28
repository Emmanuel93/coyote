// Package ledger escribe y lee el registro append-only del proyecto:
// coyote/ledger/AAAA/MM/DD-<usuario>.ccf. Un archivo por día y persona hace
// que git nunca produzca conflictos de merge en el ledger.
package ledger

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Emmanuel93/coyote/internal/ccf"
)

// Ledger es el ledger de un proyecto.
type Ledger struct{ Dir string }

// Open devuelve el ledger del proyecto en root.
func Open(root string) Ledger { return Ledger{Dir: filepath.Join(root, "coyote", "ledger")} }

// PathFor devuelve el archivo del día y del usuario.
func (l Ledger) PathFor(ts time.Time, user string) string {
	t := ts.UTC()
	return filepath.Join(l.Dir, t.Format("2006"), t.Format("01"), t.Format("02")+"-"+user+".ccf")
}

// Append valida y agrega la línea; devuelve la ruta del archivo. La línea se
// vuelve a leer tal como quedará escrita: lo que no se puede leer no se escribe.
func (l Ledger) Append(line ccf.Line, user string) (string, error) {
	if err := line.Validate(); err != nil {
		return "", err
	}
	text := line.String()
	if _, err := ccf.Parse(text); err != nil {
		return "", err
	}
	p := l.PathFor(line.TS, user)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	prev, statErr := os.ReadFile(p)
	if statErr != nil && !os.IsNotExist(statErr) {
		return "", statErr
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	defer f.Close()
	var b strings.Builder
	switch {
	case os.IsNotExist(statErr) || len(prev) == 0:
		b.WriteString(ccf.Header + "\n")
	case prev[len(prev)-1] != '\n':
		b.WriteString("\n") // un archivo editado a mano sin salto final no une dos eventos
	}
	b.WriteString(text + "\n")
	if _, err := f.WriteString(b.String()); err != nil {
		return "", err
	}
	return p, nil
}

// Entry es un evento leído con su origen.
type Entry struct {
	File   string
	LineNo int
	Line   ccf.Line
}

// Problem es una línea inválida del ledger.
type Problem struct {
	File   string
	LineNo int
	Err    error
}

// ReadAll recorre el ledger completo, en orden cronológico.
func (l Ledger) ReadAll() ([]Entry, []Problem, error) {
	if _, err := os.Stat(l.Dir); os.IsNotExist(err) {
		return nil, nil, nil
	}
	var entries []Entry
	var probs []Problem
	err := filepath.WalkDir(l.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".ccf") {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for i, raw := range strings.Split(string(data), "\n") {
			s := strings.TrimSpace(raw)
			if s == "" || strings.HasPrefix(s, "#") {
				continue
			}
			ln, err := ccf.Parse(s)
			if err != nil {
				probs = append(probs, Problem{p, i + 1, err})
				continue
			}
			entries = append(entries, Entry{p, i + 1, ln})
		}
		return nil
	})
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Line.TS.Before(entries[j].Line.TS) })
	return entries, probs, err
}

// SummaryTypes son tipos de evento que resumen otros eventos (el cierre de un
// workstream); Totals no los suma para no contar dos veces.
var SummaryTypes = map[string]bool{"close": true}

// Totals suma tokens y costo de un conjunto de eventos, sin los resúmenes.
func Totals(entries []Entry) (ccf.Tokens, ccf.Cost) { return sum(entries, false) }

// SummaryTotals suma solo los resúmenes (cierres de workstream).
func SummaryTotals(entries []Entry) (ccf.Tokens, ccf.Cost) { return sum(entries, true) }

func sum(entries []Entry, summaries bool) (ccf.Tokens, ccf.Cost) {
	var t ccf.Tokens
	var c ccf.Cost
	for _, e := range entries {
		if SummaryTypes[e.Line.Type] != summaries {
			continue
		}
		if e.Line.Tokens != nil {
			t.In += e.Line.Tokens.In
			t.Cache += e.Line.Tokens.Cache
			t.Out += e.Line.Tokens.Out
		}
		if e.Line.Cost != nil {
			c.In += e.Line.Cost.In
			c.Out += e.Line.Cost.Out
		}
	}
	return t, c
}
