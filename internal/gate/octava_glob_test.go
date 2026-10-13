package gate

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/Emmanuel93/coyote/internal/glob"
)

// TestBracketsAsAnyLeeDeMasQueElLector: dos lectores de comodines conviven: el
// que expande contra el disco (compileGlob) y el que decide si el patrón de una
// búsqueda (rg -g, el glob de un Grep) alcanza un archivo de secretos
// (glob.BracketsAsAny). El segundo nunca puede leer de menos que el primero: todo
// nombre que el primero alcanza lo alcanza el patrón que arma el segundo.
func TestBracketsAsAnyLeeDeMasQueElLector(t *testing.T) {
	rng := rand.New(rand.NewSource(8))
	patAlpha := []string{"a", "b", "[", "]", "!", "^", ":", "=", ".", "-", "*", "?", "[", "]", "[:", ":]", "[=", "=]", "[."}
	nameAlpha := []string{"a", "b", "[", "]", ":", "=", ".", "!"}
	var names []string
	var gen func(prefix string, left int)
	gen = func(prefix string, left int) {
		if prefix != "" && prefix != "." && prefix != ".." {
			names = append(names, prefix)
		}
		if left == 0 {
			return
		}
		for _, c := range nameAlpha {
			gen(prefix+c, left-1)
		}
	}
	gen("", 3)
	for n := 0; n < 12000; n++ {
		var p strings.Builder
		for k := 1 + rng.Intn(6); k > 0; k-- {
			p.WriteString(patAlpha[rng.Intn(len(patAlpha))])
		}
		pat := p.String()
		any := glob.BracketsAsAny(pat)
		for _, name := range names {
			if shellMatch(pat, name) && !glob.Match(any, name) {
				t.Fatalf("el lector alcanza %q con el patrón %q y la búsqueda no: %q", name, pat, any)
			}
		}
	}
}
