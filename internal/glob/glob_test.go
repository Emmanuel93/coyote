package glob

import (
	"strings"
	"testing"
	"time"
)

func TestMatch(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"**/respaldos/**", "respaldos/dump.sql", true},
		{"**/respaldos/**", "a/b/respaldos/c/d.sql", true},
		{"**/respaldos/**", "a/respaldosx/d.sql", false},
		{"*.dump", "db/prod.dump", true},
		{"*.dump", "db/prod.dump.txt", false},
		{".github/workflows/*.yml", ".github/workflows/ci.yml", true},
		{".github/workflows/*.yml", ".github/workflows/sub/ci.yml", false},
		{"docs/**", "docs/specs/ccf-v1.md", true},
		{"docs/**", "docs", true},
		{"**/*.md", "README.md", true},
		{"**/*.md", "a/b/c.md", true},
		{"**/*.md", "a/b/c.mdx", false},
		{"**/Claude outputs/**", "x/Claude outputs/informe.pdf", true},
		{"internal/{cli,ccf}/**", "internal/ccf/ccf.go", true},
		{"internal/{cli,ccf}/**", "internal/glob/glob.go", false},
		{"README.md", "README.md", true},
		{"README.md", "docs/README.md", true},
		{"src/?.ts", "src/a.ts", true},
		{"src/?.ts", "src/ab.ts", false},
		{"**/*.{sql,{bak,dump}}", "db/prod.dump", true},
		{"**/*.{sql,{bak,dump}}", "db/prod.txt", false},
		{"backups/", "backups/x.sql", true},
		{"backups/", "a/backups/b/x.sql", true},
		{"backups/", "backups.sql", false},
		{"data/raw/", "data/raw/x.csv", true},
		{"data/raw/", "other/data/raw/x.csv", false},
		{"{a,b", "a", true},
		{"a}b", "a}b", true},
	}
	for _, c := range cases {
		if got := Match(c.pattern, c.path); got != c.want {
			t.Errorf("Match(%q, %q) = %v, se esperaba %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestBracketsAsAny(t *testing.T) {
	for in, want := range map[string]string{
		"a":                 "a",
		".e[n]v":            ".e?v",
		".e[[:alpha:]]v":    ".e?v",
		".e[]n]v":           ".e?v",
		".e[!x]v":           ".e?v",
		".e[^x]v":           ".e?v",
		".e[[=n=]]v":        "*", // una equivalencia o un símbolo de colación: el tramo se lee entero como *
		".e[[.n.]]v":        "*",
		".e[[=x=]][n]v":     "*",
		"a/.e[[=x=]][n]v/b": "a/*/b",
		"a[[:b":             "*", // una clase sin cierre
		"a[[:b]":            "*",
		"[[:alpha:]":        "[?",    // sin el ] final: el primer [ es un carácter y [:alpha:] un conjunto
		"[a/b]":             "[a/b]", // un corchete no cruza una barra
		"x/[a/b]c":          "x/[a/b]c",
		"[:]":               "?",
		"[!:]":              "?",
		"a/":                "a/",
		"/[a]":              "/?",
		"[a]/":              "?/",
		// Bash lee [a-[.o.]] como el rango a..o (alcanza .env): el final de un rango
		// que empieza como una equivalencia, un símbolo de colación o una clase no se
		// imita, y el tramo se lee entero como *.
		".e[a-[.o.]]v":          "*",
		".e[m-[=o=]]v":          "*",
		".e[a-[:alpha:]]v":      "*",
		"x/[a-[:alpha:]]":       "x/*",
		"**/[a-z]*.txt":         "**/?*.txt",
		`\[x\].env`:             `\[x\].env`,
		"a[b":                   "a[b",
		"[a":                    "[a",
		"a[":                    "a[",
		"[[]x[]]":               "?x?",
		"[[:alpha:][:digit:]]x": "?x",
		"[a][b]":                "??",
	} {
		if got := BracketsAsAny(in); got != want {
			t.Errorf("BracketsAsAny(%q) = %q, quiero %q", in, got, want)
		}
	}
}

// TestBracketsAsAnyEsLineal: octava revisión. El análisis de los corchetes buscaba
// el cierre de cada [: [= [. hasta el final del patrón y lo repetía desde cada [
// sin cerrar: 15 000 caracteres tardaban 17 s en el glob de un Grep. Ahora el
// tiempo es proporcional al largo, y un tramo que pasa de maxBracketSeg se lee
// como *.
func TestBracketsAsAnyEsLineal(t *testing.T) {
	units := []string{"[", "[[:", "[[=", "[[.", "[a", `[\]`, "[[:alpha:]", "[!", "[]", "[[", "[a[a]"}
	for _, u := range units {
		for _, n := range []int{maxBracketSeg / len(u), 4 * maxBracketSeg / len(u)} {
			in := strings.Repeat(u, n)
			start := time.Now()
			out := BracketsAsAny(in)
			if el := time.Since(start); el > holgura(2*time.Second) {
				t.Errorf("%q×%d (%d bytes): tardó %v", u, n, len(in), el)
			}
			if len(in) > maxBracketSeg && out != "*" {
				t.Errorf("%q×%d: un tramo de más de %d bytes se lee como *, no %.20q…", u, n, maxBracketSeg, out)
			}
		}
	}
	// Muchos tramos, cada uno corto: el costo se suma, no se multiplica.
	in := strings.Repeat("[a[a[a/", 200000)
	start := time.Now()
	BracketsAsAny(in)
	if el := time.Since(start); el > holgura(2*time.Second) {
		t.Errorf("200 000 tramos cortos: tardó %v", el)
	}
}

func FuzzBracketsAsAny(f *testing.F) {
	for _, s := range []string{"[[:alpha:]]", "[!x]", "a[", `[\]`, "[[=", "[[.", "[[:", "a/[b/c]", "[]]", "[!]a]", "x[:", "[[:a:][:b:]]"} {
		f.Add(s)
		f.Add(strings.Repeat(s, 3000))
	}
	f.Fuzz(func(t *testing.T, p string) {
		start := time.Now()
		out := BracketsAsAny(p)
		if el := time.Since(start); el > holgura(2*time.Second) {
			t.Fatalf("tardó %v con %d bytes", el, len(p))
		}
		if len(out) > len(p) {
			t.Fatalf("la salida (%d bytes) es más larga que el patrón (%d)", len(out), len(p))
		}
		if strings.Count(out, "/") != strings.Count(p, "/") {
			t.Fatalf("las barras cambiaron: %q → %q", p, out)
		}
	})
}
