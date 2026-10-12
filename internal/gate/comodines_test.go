package gate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Emmanuel93/coyote/internal/secrets"
)

// mkTree crea los archivos de files (ruta relativa a base → contenido).
func mkTree(t *testing.T, base string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(base, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// Los comodines de un comando se leen como los lee el shell: las clases
// POSIX ([[:alpha:]], [[=n=]], [[.n.]]), el ] como primer miembro ([]n]) y la
// negación con ! ([!x]) también alcanzan el archivo de secretos. filepath.Glob
// de Go no entiende ninguna de esas formas y dejaba pasar `cat .e[[:alpha:]]v`.
func TestClasesPosixYNegacionesEnComodines(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "tienda")
	mkTree(t, root, map[string]string{
		".env":                "A=1\n",
		"src/app.go":          "package app\n",
		"src/util.go":         "package app\n",
		"claves/api.txt":      "k\n",
		"claves/1.txt":        "k\n",
		"docs/secrets.md":     "# secretos de ejemplo\n",
		"README.md":           "hola\n",
		"main.go":             "package main\n",
		".git/HEAD":           "ref: refs/heads/main\n",
		"coyote/project.yaml": "version: 1\n",
	})
	ps := NewPaths(root, home, filepath.Join(home, ".config", "coyote"))
	ps.Secrets = secrets.Rules{Files: []string{"claves/*.txt"}}
	bash := func(cmd string) Action { return claude(t, "Bash", map[string]any{"command": cmd}, root) }

	for _, c := range []string{
		"cat .e[[:alpha:]]v",
		"cat .[[:alpha:]]nv",
		"cat .e[[:alpha:][:digit:]]v",
		"cat .e[[:lower:]]v",
		"cat .e[]n]v",
		"cat .[]e]nv",
		"cat .e[]m-o]v",
		"cat .e[[=n=]]v",
		"cat .e[[.n.]]v",
		"cat .e[!x]v",
		"cat .e[^x]v",
		"cat .e[a-z]v",
		"cat .e[m-o]v",
		"grep -h A .e[[:alpha:]]v",
		"head -c 20 .[[:alpha:]]nv",
		"head -c 20 .e[[:punct:]nv]v",
		"cat claves/[[:alpha:]]*.txt",
		"cat claves/[![:digit:]]*",
		"cat claves/[[:alnum:]].txt",
		"cat claves/[[:alpha:]][[:alpha:]][[:alpha:]].txt",
	} {
		if d := ps.Evaluate(bash(c)); d.Verdict != Block {
			t.Errorf("%q debe bloquearse siempre: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
	// Lo que no alcanza ningún archivo de secretos sigue pasando.
	for _, c := range []string{
		"cat src/[[:alpha:]]*.go",
		"cat src/[]a]pp.go",
		"cat [[:upper:]]*.md",
		"cat docs/[[:alpha:]]*.md",
		"cat m[[:alpha:]]in.go",
		"ls -a .[[:alpha:]]it",
	} {
		if d := ps.Evaluate(bash(c)); d.Verdict != Allow {
			t.Errorf("%q debe pasar: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
}

// Un comodín mezclado con corchetes escritos entre comillas o escapados: la
// carpeta del proyecto se llama "Proyectos [cliente]" y el shell expande el ?
// pero deja los corchetes como letras. Leerlos como una clase no alcanzaba la
// carpeta, y `cat "Proyectos"?"[cliente]"/tienda/.env` salía con 0.
func TestComodinMezcladoConCorchetesLiterales(t *testing.T) {
	home := t.TempDir()
	parent := filepath.Join(home, "trabajo")
	root := filepath.Join(parent, "Proyectos [cliente]", "tienda")
	mkTree(t, root, map[string]string{
		".env":                "A=1\n",
		"claves/api.txt":      "k\n",
		"src/app.go":          "package app\n",
		".git/HEAD":           "ref: refs/heads/main\n",
		"coyote/project.yaml": "version: 1\n",
	})
	ps := NewPaths(root, home, filepath.Join(home, ".config", "coyote"))
	ps.Secrets = secrets.Rules{Files: []string{"claves/*.txt"}}
	// El comando corre en la carpeta que contiene a "Proyectos [cliente]".
	bash := func(cmd string) Action { return claude(t, "Bash", map[string]any{"command": cmd}, parent) }

	for _, c := range []string{
		`cat "Proyectos"?"[cliente]"/tienda/.env`,
		`cat Proyectos?\[cliente\]/tienda/.env`,
		`cat Proyectos*\[cliente\]/tienda/.env`,
		`cat Proyectos\ \[cl*\]/tienda/.env`,
		`cat Proyectos?[[]cliente[]]/tienda/.env`,
		`cat 'Proyectos'?'[cliente]'/tienda/.e*`,
		`cat "Proyectos [cliente]"/tienda/.e?v`,
		`cat "Proyectos [cliente]"/*/.env`,
		`cat Proyectos?"[cliente]"/tienda/claves/*.txt`,
		`cat Proyectos?\[cliente\]/tienda/claves/api.txt`,
		`cat ?royectos?"[cliente]"/t*/.env`,
	} {
		if d := ps.Evaluate(bash(c)); d.Verdict != Block {
			t.Errorf("%q debe bloquearse siempre: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
	for _, c := range []string{
		`cat "Proyectos"?"[cliente]"/tienda/src/app.go`,
		`cat Proyectos?\[cliente\]/tienda/src/*.go`,
		`cat "Proyectos [cliente]"/tienda/src/a?p.go`,
		`ls Proyectos?\[cliente\]`,
	} {
		if d := ps.Evaluate(bash(c)); d.Verdict != Allow {
			t.Errorf("%q debe pasar: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
}

// Los patrones de nombres de una búsqueda (rg -g, grep --include) llevan
// corchetes en ripgrep y en grep; un patrón con **, o que no alcanza nada en el
// disco, no debe esconder el archivo de secretos.
func TestPatronesDeBusquedaConCorchetes(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "tienda")
	mkTree(t, root, map[string]string{
		".env":           "A=1\n",
		"src/app.go":     "package app\n",
		"claves/api.txt": "k\n",
	})
	ps := NewPaths(root, home, filepath.Join(home, ".config", "coyote"))
	ps.Secrets = secrets.Rules{Files: []string{"claves/*.txt"}}
	bash := func(cmd string) Action { return claude(t, "Bash", map[string]any{"command": cmd}, root) }
	for _, c := range []string{
		`rg -g '**/.e[n]v' TOKEN .`,
		`rg -g '.e[n]v' TOKEN .`,
		`rg --glob='**/.e[[:alpha:]]v' TOKEN .`,
		`rg -g '**/.[e]nv' TOKEN .`,
		`rg -g '!**/x' -g '**/.e[!x]v' TOKEN .`,
		`grep -r --include='.e[n]v' TOKEN .`,
		`grep -r --include='**/.e[a-z]v' TOKEN .`,
	} {
		if d := ps.Evaluate(bash(c)); d.Verdict != Block {
			t.Errorf("%q debe bloquearse siempre: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
	for _, c := range []string{
		`rg -g '**/[a-z]*.go' TODO .`,
		`rg -g '*.go' TODO src`,
		`grep -r --include='*.go' TODO src`,
	} {
		if d := ps.Evaluate(bash(c)); d.Verdict != Allow {
			t.Errorf("%q debe pasar: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
}

// Cada capa que lee un comodín ve los corchetes citados por su cuenta: el
// recorrido de cualquier comando (secretWord), el de los comandos de solo
// lectura (secretArg), y el nombre de un archivo de secretos al final del
// patrón, esté donde esté.
func TestCadaCapaVeLosCorchetesCitados(t *testing.T) {
	home := t.TempDir()
	parent := filepath.Join(home, "trabajo")
	root := filepath.Join(parent, "Proyectos [cliente]", "tienda")
	mkTree(t, root, map[string]string{
		".env":                "A=1\n",
		"claves/api.txt":      "k\n",
		".git/HEAD":           "ref: refs/heads/main\n",
		"coyote/project.yaml": "version: 1\n",
	})
	ps := NewPaths(root, home, filepath.Join(home, ".config", "coyote"))
	ps.Secrets = secrets.Rules{Files: []string{"claves/*.txt"}}

	// secretWord: con un `echo $HOME` el recorrido estricto se rinde y solo
	// queda el de cualquier comando.
	for _, c := range []string{
		`cat "Proyectos"?"[cliente]"/tienda/claves/api.txt; echo $HOME`,
		`cat Proyectos?\[cliente\]/tienda/claves/*.txt; echo $HOME`,
		`cat .e[!x]v; echo $HOME`,
		`cat .e[[:alpha:]]v; echo $HOME`,
	} {
		cwd := parent
		if strings.HasPrefix(c, "cat .e") {
			cwd = root
		}
		if _, ok := ps.secretWord(c, cwd); !ok {
			t.Errorf("secretWord no ve el secreto de %q", c)
		}
	}
	// secretArg: la palabra del recorrido estricto, con su forma de patrón.
	w := word{s: "Proyectos?[cliente]/tienda/claves/api.txt", glob: true, pat: `Proyectos?\[cliente\]/tienda/claves/api.txt`}
	if _, ok := ps.secretArg(w, parent); !ok {
		t.Errorf("secretArg no ve el secreto de %+v", w)
	}
	// El nombre de un archivo de secretos al final del patrón cuenta aunque el
	// camino no se pueda expandir.
	for _, p := range []string{filepath.Join(parent, "nada*", ".env"), filepath.Join(parent, "[[:foo:]]", ".env"), `**/Zz?[a]/.env`} {
		if _, ok := ps.secretGlobWord(p, parent); !ok {
			t.Errorf("secretGlobWord no ve el .env al final de %q", p)
		}
	}
	// Y lo que no es un secreto no se marca.
	for _, p := range []string{filepath.Join(parent, "nada*", "app.go"), `**/Zz?[a]/app.go`} {
		if what, ok := ps.secretGlobWord(p, parent); ok {
			t.Errorf("secretGlobWord marca %q: %s", p, what)
		}
	}
}

// Las dos lecturas de un comando guardan, junto al texto sin comillas de cada
// palabra, el patrón: lo citado o escapado lleva una barra invertida delante.
func TestLasPalabrasGuardanSuPatron(t *testing.T) {
	cmd := `cat "Proyectos"?"[cliente]"/a\*b 'c*d' e[ab]f`
	segs, err := splitShell(cmd)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		s, pat string
		glob   bool
	}{
		{"cat", "cat", false},
		{"Proyectos?[cliente]/a*b", `Proyectos?\[cliente\]/a\*b`, true},
		{"c*d", `c\*d`, false},
		{"e[ab]f", "e[ab]f", true},
	}
	if len(segs) != 1 || len(segs[0]) != len(want) {
		t.Fatalf("segmentos inesperados: %+v", segs)
	}
	for i, w := range segs[0] {
		if w.s != want[i].s || w.pat != want[i].pat || w.glob != want[i].glob {
			t.Errorf("palabra %d: %+v, quiero %+v", i, w, want[i])
		}
	}
	ls := lenientSegs(cmd)
	if len(ls) != 1 || len(ls[0].words) != len(want) {
		t.Fatalf("segmentos laxos inesperados: %+v", ls)
	}
	for i, w := range ls[0].words {
		if w.text != want[i].s || w.pat != want[i].pat {
			t.Errorf("palabra laxa %d: %+v, quiero %+v", i, w, want[i])
		}
	}
}
