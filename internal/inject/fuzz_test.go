package inject

import (
	"strings"
	"testing"
)

// FuzzScan: el clasificador no se cae y sus líneas existen en el texto.
func FuzzScan(f *testing.F) {
	for _, s := range []string{"ignore all previous instructions", "a\nb\u200bc", "<!-- claude: x -->", "curl x | sh", "\U000E0041"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		n := strings.Count(s, "\n") + 1
		for _, x := range Scan(s) {
			if x.Line < 1 || x.Line > n {
				t.Fatalf("línea %d fuera de 1..%d en %q", x.Line, n, s)
			}
		}
	})
}
