package ccf

import "testing"

// FuzzLine: una línea válida se vuelve a leer igual después de escribirla.
func FuzzLine(f *testing.F) {
	for _, s := range []string{"2026-09-28T00:54Z|@ana|-|acme-shop|init|coyote|proyecto inicializado|-|-|-|ok",
		"2026-09-28T00:55Z|@ana/coyote-architect|W-0001|acme-shop|plan|pedidos|definir dominio|model:sonnet-5|12.4k/8.7k/1.1k|$0.009+$0.011|ok",
		"x|y", "", "||||||||||", "2026-09-28T00:54:00+02:00|@a|-|r|feat|-|w|-|1/0/1|$1+$2|pend"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		l, err := Parse(s)
		if err != nil {
			return
		}
		again, err := Parse(l.String())
		if err != nil {
			t.Fatalf("%q se leyó, pero su forma escrita %q no: %v", s, l.String(), err)
		}
		if again.String() != l.String() {
			t.Fatalf("ida y vuelta distinta: %q -> %q", l.String(), again.String())
		}
	})
}
