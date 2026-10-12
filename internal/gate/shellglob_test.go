package gate

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestShellMatch(t *testing.T) {
	cases := []struct {
		pat, name string
		want      bool
	}{
		{"*", "", true},
		{"*", ".env", true},
		{"a*b", "ab", true},
		{"a*b", "acb", true},
		{"a*b", "ac", false},
		{"a*a*a*b", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaac", false},
		{"?", "ñ", true},
		{"??", "ñ", false},
		{".e?v", ".env", true},
		{"[ab]", "a", true},
		{"[ab]", "c", false},
		{"[a-c]", "b", true},
		{"[a-c]", "d", false},
		{"[c-a]", "b", false},
		{"[!a]", "b", true},
		{"[!a]", "a", false},
		{"[^a]", "b", true},
		{"[^a]", "a", false},
		{"[]]", "]", true},
		{"[]a]", "]", true},
		{"[]a]", "a", true},
		{"[!]a]", "]", false},
		{"[!]a]", "b", true},
		{"[a-]", "-", true},
		{"[-a]", "-", true},
		{"[a-]", "b", false},
		{"[[:alpha:]]", "n", true},
		{"[[:alpha:]]", "ñ", true},
		{"[[:alpha:]]", "1", false},
		{"[[:digit:]]", "7", true},
		{"[[:upper:]]", "a", false},
		{"[[:lower:]]", "a", true},
		{"[![:alpha:]]", "1", true},
		{"[![:alpha:]]", "a", false},
		{"[[:alpha:][:digit:]]", "9", true},
		{"[[:alnum:]_]", "_", true},
		{"[[:punct:]]", ".", true},
		{"[[:punct:]]", "$", true},
		{"[[:space:]]", " ", true},
		{"[[:blank:]]", "\t", true},
		{"[[:xdigit:]]", "f", true},
		{"[[:xdigit:]]", "g", false},
		{"[[=n=]]", "n", true},
		{"[[=n=]]", "m", false},
		{"[[=n=]]", "ñ", true}, // de más: las equivalencias alcanzan las letras con acento
		{"[[.n.]]", "n", true},
		{"[[.n.]]", "m", false},
		{"[[.a.]-c]", "b", true},
		{"[[.hyphen.]]", "z", true}, // un nombre de colación que no se conoce alcanza cualquiera
		{"[[:foo:]]", "z", true},    // y también una clase que no se conoce
		{`\[x\]`, "[x]", true},
		{`\[x\]`, "x", false},
		{"[x]", "[x]", false},
		{`a\ b`, "a b", true},
		{`\*`, "*", true},
		{`\*`, "a", false},
		{"a[", "a[", true},   // un [ que no cierra es un caracter
		{"[a", "[a", true},   // idem
		{"a[b", "a[b", true}, // idem
		{"[[]", "[", true},
		{"[[]cliente[]]", "[cliente]", true},
		{`Proyectos?\[cliente\]`, "Proyectos [cliente]", true},
		{`Proyectos*\[cliente\]`, "Proyectos [cliente]", true},
		{"Proyectos?[[]cliente[]]", "Proyectos [cliente]", true},
	}
	for _, c := range cases {
		if got := shellMatch(c.pat, c.name); got != c.want {
			t.Errorf("shellMatch(%q, %q) = %v, quiero %v", c.pat, c.name, got, c.want)
		}
	}
}

func TestQuoteGlobYGlobUnquote(t *testing.T) {
	for _, s := range []string{"a", "[cliente]", `a\b`, "x*y?z", "]", "ñ[1]"} {
		q := quoteGlob(s)
		if globHasMeta(q) {
			t.Errorf("quoteGlob(%q) = %q todavía tiene comodines sin citar", s, q)
		}
		if got := globUnquote(q); got != s {
			t.Errorf("globUnquote(quoteGlob(%q)) = %q", s, got)
		}
		if !shellMatch(q, s) {
			t.Errorf("el patrón %q no coincide con el nombre %q que cita", q, s)
		}
	}
}

// globAll alcanza, como mínimo, lo que alcanza bash. Que alcance de más
// (una equivalencia con acento, el nombre tal cual si existe) es a propósito;
// lo que no puede es dejar fuera algo que el shell sí expande.
func TestShellGlobAlcanzaLoQueBash(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no hay bash")
	}
	dir := t.TempDir()
	files := []string{"a", "b", "ab", "abc", "A", "1", "x.txt", "X.TXT", "a b", "a-b", "a]b", "[", "]", "!", "^", "-", "a.b",
		".env", ".envrc", ".hidden", "[x].env", "dir/f1", "dir/f2", "dir2/.hidden", "dir2/g", "Proyectos [cliente]/tienda/.env",
		"Proyectos [cliente]/tienda/src/app.go", "claves/api.txt", "claves/1.txt", "x", "x.env"}
	for _, f := range files {
		p := filepath.Join(dir, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pats := []string{
		"*", "a*", "*b", "a?", "?", "??", "[ab]", "[a-b]", "[!a]", "[^a]", "[]]", "[]a]", "[!]a]", "[a-]", "[-a]",
		"[[:alpha:]]", "[[:digit:]]", "[[:upper:]]*", "[![:alpha:]]", "[[:alpha:][:digit:]]", "[[:punct:]]",
		".e[[:alpha:]]v", ".[[:alpha:]]nv", ".e[]n]v", ".e[!x]v", ".e[^x]v", ".e[[=n=]]v", ".e[[.n.]]v", ".e[m-o]v",
		"[[.a.]-b]", `\[x\].env`, "[x].env", `a\ b`, "a?b", "a-?", `Proyectos?\[cliente\]/tienda/.env`,
		`Proyectos*\[cliente\]/*/.env`, `Proyectos\ \[cl*\]/tienda/.env`, `Proyectos?[[]cliente[]]/tienda/.env`,
		"dir/*", "dir*/f?", "*/*", "*/.hidden", ".*", "[.]env", "a[b", "a[", "[a", "[a-c]*", "[c-a]", "*.[tT]*", "*.[tT][xX][tT]",
		"claves/[[:alpha:]]*.txt", "claves/[![:digit:]]*", "*/[[:alpha:]]*", "?l?ves/*",
	}
	for _, pat := range pats {
		script := `cd "$1" && shopt -s dotglob nullglob && for f in ` + pat + `; do printf '%s\n' "$f"; done`
		cmd := exec.Command(bash, "-c", script, "_", dir)
		cmd.Env = []string{"LC_ALL=C", "PATH=" + os.Getenv("PATH")}
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("bash %q: %v", pat, err)
		}
		want := map[string]bool{}
		for _, l := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
			if l == "" || l == "." || l == ".." {
				continue
			}
			// Un patrón con un [ que no cierra no es un comodín: bash lo deja
			// como palabra aunque no sea un archivo.
			if _, err := os.Lstat(filepath.Join(dir, l)); err == nil {
				want[l] = true
			}
		}
		got := map[string]bool{}
		matches, complete := globAll(dir + "/" + pat)
		if !complete {
			t.Errorf("%q: la expansión se cortó en un árbol de 30 archivos", pat)
		}
		for _, m := range matches {
			got[strings.TrimPrefix(m, dir+"/")] = true
		}
		var missing []string
		for w := range want {
			if !got[w] {
				missing = append(missing, w)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			t.Errorf("%q: bash alcanza %v y globAll no", pat, missing)
		}
	}
}

// FuzzShellGlob: el lector de comodines recibe lo que escribe un agente. Con
// cualquier patrón no se cae ni se cuelga; y un nombre citado con quoteGlob es
// ese nombre y nada más, con las letras que sean.
func FuzzShellGlob(f *testing.F) {
	for _, s := range []string{"[[:alpha:]]", "[!x]", "a*b*c", `\[x\]`, "[]a]", "[[=n=]]", "[[.a.]-c]", "[", "[[:", "[[.", "*?*?*",
		"[a-", "[\\", "[[:alpha:]", "[[=", "[!", "[^]", "\xff[\xfe]", "**", "a\\"} {
		f.Add(s, "abc")
		f.Add(s, s)
	}
	f.Fuzz(func(t *testing.T, pat, name string) {
		_ = shellMatch(pat, name)
		_ = globHasMeta(pat)
		_ = globUnquote(pat)
		_ = compileGlob(pat)
		q := quoteGlob(name)
		if globHasMeta(q) {
			t.Fatalf("quoteGlob(%q) = %q todavía tiene comodines sin citar", name, q)
		}
		if got := globUnquote(q); got != name {
			t.Fatalf("globUnquote(quoteGlob(%q)) = %q", name, got)
		}
		if !shellMatch(q, name) {
			t.Fatalf("el patrón %q no coincide con el nombre %q que cita", q, name)
		}
	})
}
