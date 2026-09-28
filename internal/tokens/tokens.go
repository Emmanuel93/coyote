// Package tokens estima tokens de modelos Claude sin llamar a la API.
//
// La estimación es deliberadamente conservadora (cuenta de más) para que los
// topes de README.coyote.md y CONTEXT.coyote.md se disparen antes que el conteo
// real. En v0.2 se contrasta con el endpoint count_tokens (ADR-0006).
package tokens

import (
	"math"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Estimate devuelve una estimación de tokens para s.
func Estimate(s string) int {
	if strings.TrimSpace(s) == "" {
		return 0
	}
	runes := utf8.RuneCountInString(s)
	words := len(strings.Fields(s))
	marks := 0
	for _, r := range s {
		if unicode.IsPunct(r) || unicode.IsSymbol(r) {
			marks++
		}
	}
	byChars := math.Ceil(float64(runes) / 3.5)
	byWords := math.Ceil(float64(words)*1.3 + float64(marks)*0.5)
	return int(math.Max(byChars, byWords))
}
