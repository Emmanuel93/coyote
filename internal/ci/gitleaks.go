package ci

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode"
)

// Leak es un hallazgo de gitleaks en los commits del PR, sin su valor: de su
// reporte solo se leen archivo, línea, regla y commit (ADR-0021).
type Leak struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Rule   string `json:"rule"`
	Commit string `json:"commit"`
}

// maxLeaks acota los hallazgos que se guardan; el total se cuenta igual.
const maxLeaks = 1000

func clean(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	if r := []rune(s); len(r) > n {
		s = string(r[:n]) + "…"
	}
	return s
}

// ReadGitleaks lee el reporte JSON de gitleaks. Devuelve los hallazgos
// (hasta maxLeaks) y cuántos había. Los campos con el valor (Secret, Match,
// Line) nunca se leen.
func ReadGitleaks(r io.Reader) ([]Leak, int, error) {
	var raw []struct {
		File      string `json:"File"`
		StartLine int    `json:"StartLine"`
		RuleID    string `json:"RuleID"`
		Commit    string `json:"Commit"`
	}
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		return nil, 0, fmt.Errorf("el reporte de gitleaks no es un JSON válido: %w", err)
	}
	out := make([]Leak, 0, min(len(raw), maxLeaks))
	for i, x := range raw {
		if i == maxLeaks {
			break
		}
		if x.File == "" || x.RuleID == "" {
			return nil, 0, fmt.Errorf("el reporte de gitleaks tiene un hallazgo sin archivo o sin regla (%d)", i)
		}
		c := clean(x.Commit, 40)
		if len(c) > 12 {
			c = c[:12]
		}
		out = append(out, Leak{File: clean(x.File, 300), Line: max(x.StartLine, 0), Rule: clean(x.RuleID, 80), Commit: c})
	}
	return out, len(raw), nil
}
