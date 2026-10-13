package gate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Emmanuel93/coyote/internal/secrets"
)

// Octava revisión de v1.0 (una pasada corta sobre las correcciones de la
// séptima): el gate corre dentro del hook de un IDE, que lo corta a los 30 s y
// deja pasar la acción. Estas pruebas fijan que ninguna forma de entrada lo
// haga esperar de más, y que lo que el shell expande y el gate no veía (una
// equivalencia seguida de otro corchete, las llaves de un secreto, `?` por bytes
// en un locale C) se lee ahora de más y no de menos.

// dentroDe corre f y falla la prueba si tarda más de d. Si f no termina queda
// corriendo, pero la prueba falla en lugar de colgarse.
func dentroDe(t *testing.T, d time.Duration, que string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	d = holgura(d)
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s: tardó más de %v", que, d)
	}
}

func bashDe(t *testing.T, root, cmd string) Action {
	t.Helper()
	return claude(t, "Bash", map[string]any{"command": cmd}, root)
}

// TestExpandBracesComoBash: las llaves se expanden como el shell, con el
// producto de todos los grupos, los anidados y las secuencias. Un {x} sin coma
// ni rango es texto.
func TestExpandBracesComoBash(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"abc", []string{"abc"}},
		{"{a,b}", []string{"a", "b"}},
		{"x{a,b}y", []string{"xay", "xby"}},
		{"{a,b}{c,d}", []string{"ac", "ad", "bc", "bd"}},
		{"{a,{b,c}}", []string{"a", "b", "c"}},
		{"a{b,{c,d}e}f", []string{"abf", "acef", "adef"}},
		{"{x}", []string{"{x}"}},
		{"{x}.{a,b}", []string{"{x}.a", "{x}.b"}},
		{"{a{b,c}}", []string{"{ab}", "{ac}"}},
		{"{a,b", []string{"{a,b"}},
		{"a}b", []string{"a}b"}},
		{"{1..3}", []string{"1", "2", "3"}},
		{"{3..1}", []string{"3", "2", "1"}},
		{"{1..7..3}", []string{"1", "4", "7"}},
		{"{01..03}", []string{"01", "02", "03"}},
		{"{a..c}", []string{"a", "b", "c"}},
		{".e{n..n}v", []string{".env"}},
		{".env{,.bak}", []string{".env", ".env.bak"}},
		{"{,}", nil},
	}
	for _, c := range cases {
		got := expandBraces(c.in)
		if strings.Join(got, "\x00") != strings.Join(c.want, "\x00") {
			t.Errorf("expandBraces(%q) = %q, quiero %q", c.in, got, c.want)
		}
	}
}

// TestLlavesNoSonExponenciales: `cat {a,b}{a,b}…` con 26 grupos (130 bytes)
// hacía que el gate tardara minutos: expandBraces volvía a expandir el resto de
// la palabra por cada alternativa. Viene desde el rc.1. Con más de 64
// combinaciones devuelve las primeras y, además, la palabra con cada grupo
// cambiado por *, que alcanza todo lo que dejó fuera.
func TestLlavesNoSonExponenciales(t *testing.T) {
	var got []string
	dentroDe(t, 5*time.Second, "expandBraces de 60 grupos", func() {
		got = expandBraces(strings.Repeat("{a,b}", 60))
	})
	if len(got) < 64 || len(got) > 65 {
		t.Fatalf("se esperaban 64 combinaciones y la palabra amplia: %d resultados", len(got))
	}
	if last := got[len(got)-1]; last != strings.Repeat("*", 60) {
		t.Errorf("el último resultado es la palabra con * por grupo: %q", last)
	}
	if got[0] != strings.Repeat("a", 60) || got[1] != strings.Repeat("a", 59)+"b" {
		t.Errorf("las combinaciones salen en el orden del shell: %q %q", got[0], got[1])
	}

	ps, root := secretPaths(t)
	for _, n := range []int{26, 200, 5000} {
		cmd := "cat " + strings.Repeat("{a,b}", n)
		var d Decision
		dentroDe(t, 10*time.Second, fmt.Sprintf("cat con %d grupos de llaves", n), func() {
			d = ps.Evaluate(bashDe(t, root, cmd))
		})
		if d.Verdict == Allow {
			t.Errorf("%d grupos de llaves: no puede pasar sin aprobación (%s)", n, d.Reason)
		}
	}
}

// TestLlavesMuyHondasSeLeenAmplias: el expansor sigue hasta maxBraceDepth grupos
// anidados; más hondo, el grupo queda como texto y la palabra con * por grupo
// alcanza lo que no se enumeró. Una pila de decenas de miles no cuesta tiempo.
func TestLlavesMuyHondasSeLeenAmplias(t *testing.T) {
	deep := func(n int) string { return "." + strings.Repeat("{a,", n) + "env" + strings.Repeat("}", n) }
	got := expandBraces(deep(10))
	if len(got) != 11 || got[len(got)-1] != ".env" {
		t.Errorf("10 niveles se enumeran enteros: %q", got)
	}
	for _, w := range got {
		if w == ".*" {
			t.Errorf("10 niveles caben: no hace falta la palabra amplia: %q", got)
		}
	}
	got = expandBraces(deep(maxBraceDepth + 4))
	wild, text := false, false
	for _, w := range got {
		wild = wild || w == ".*"
		text = text || strings.Contains(w, "{a,")
	}
	if !wild || !text {
		t.Errorf("%d niveles pasan de lo que el gate sigue: el grupo queda como texto (%v) y se agrega .* (%v): %q", maxBraceDepth+4, text, wild, got)
	}
	dentroDe(t, 3*time.Second, "30 000 grupos anidados", func() { got = expandBraces(deep(30000)) })
	if len(got) > maxBraceWords+1 {
		t.Errorf("30 000 niveles dan %d palabras", len(got))
	}
}

// TestLlavesQueNoSeLeenEnterasSeLeenAmplias: un grupo demasiado hondo o una
// secuencia de más de 64 elementos no se enumeran; el gate agrega la palabra con
// cada grupo cambiado por *, que alcanza lo que se recortó. Sin ella, el archivo
// que está más allá del recorte se lee sin que el gate lo vea.
func TestLlavesQueNoSeLeenEnterasSeLeenAmplias(t *testing.T) {
	ps, root := secretPaths(t)
	// Solo app70.yaml está declarado como secreto: el elemento 70 de una secuencia.
	ps.Secrets = secrets.Rules{Files: []string{"config/prod/app70.yaml"}}
	mkTree(t, root, map[string]string{"config/prod/app70.yaml": "db: x\n"})

	// Una secuencia: bash lee config/prod/app70.yaml; el gate enumera 64.
	seq := "cat config/prod/app{1..1000}.yaml"
	if d := ps.Evaluate(bashDe(t, root, seq)); d.Verdict != Block {
		t.Errorf("%q: el elemento 70 de la secuencia es un secreto declarado: %s (%s)", seq, d.Verdict, d.Reason)
	}
	// Una secuencia que cabe se enumera entera, y lo que no alcanza no bloquea.
	if d := ps.Evaluate(bashDe(t, root, "cat config/prod/app{1..9}.yaml")); d.Verdict == Block {
		t.Errorf("app1..app9 no alcanzan ningún secreto: %s (%s)", d.Verdict, d.Reason)
	}

	// Un grupo más hondo de lo que el gate sigue: {a,{a,{a,…{a,env}}}} expande a
	// .env aunque quede a 20 niveles.
	for _, n := range []int{maxBraceDepth + 1, maxBraceDepth + 4, 40} {
		deep := strings.Repeat("{a,", n) + "env" + strings.Repeat("}", n)
		cmd := "cat ." + deep
		if d := ps.Evaluate(bashDe(t, root, cmd)); d.Verdict != Block {
			t.Errorf("%d niveles: .env quedó más hondo de lo que el gate sigue: %s (%s)", n, d.Verdict, d.Reason)
		}
	}
}

// TestLlavesQueOcultanUnaAlternativaSeLeenAmplias: más de 64 alternativas no
// dejan una fuera sin avisar: la palabra amplia alcanza la que se recortó.
func TestLlavesQueOcultanUnaAlternativaSeLeenAmplias(t *testing.T) {
	ps, root := secretPaths(t)
	var alts []string
	for i := 0; i < 70; i++ {
		alts = append(alts, fmt.Sprintf("a%d", i))
	}
	alts = append(alts, "env")
	cmd := "cat ." + "{" + strings.Join(alts, ",") + "}"
	if d := ps.Evaluate(bashDe(t, root, cmd)); d.Verdict != Block {
		t.Errorf("la alternativa .env quedó después de la 64: %s (%s)", d.Verdict, d.Reason)
	}
}

// TestLlavesDeUnSecretoSeBloquean: octava revisión, F3. secretWord revisaba el
// texto de la palabra sin expandir las llaves: `cat .{env,x}` bajaba de
// bloqueo a aprobación, y la persona veía un comando que no dice `.env`.
func TestLlavesDeUnSecretoSeBloquean(t *testing.T) {
	ps, root := secretPaths(t)
	blocked := []string{
		"cat .{env,x}", "cat .e{n,m}v", "cat .{e,f}nv", "head .{env,x}", "sed -n 1p .{env,x}", "tar cf - .e{n,m}v",
		"cp x .e{n,m}v", "cat .{e{n,m}v,x}", "cat .e{n..n}v", "cat {x}.env", "cat {y,.env}", "cat .env{,.bak}",
		"cat config/prod/{app,x}.yaml", "cat config/{prod,x}/app.yaml", "docker run --env-file=.{env,x} imagen",
		"grep -r foo .{env,x}", "cat < .e{n,m}v", "echo x > .e{n,m}v", "wc -c .e{n,m}v | cat .e{n,m}v",
		"cat 'a' .{env,x}", `cat ".e"{n,m}v`,
	}
	for _, c := range blocked {
		if d := ps.Evaluate(bashDe(t, root, c)); d.Verdict != Block {
			t.Errorf("%q debe bloquearse siempre: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
	// Lo que no nombra un secreto no se bloquea por llevar llaves: sigue pidiendo
	// la aprobación de siempre.
	for _, c := range []string{"ls {src,docs}", "cat src/{app,x}.go", "grep -r foo {src,docs}", "rm -rf {build,dist}",
		"git add src/{a,b}.go", "cp src/app.go{,.bak}", "echo {a,b}", "cat {1..3}.txt"} {
		if d := ps.Evaluate(bashDe(t, root, c)); d.Verdict != NeedsApproval {
			t.Errorf("%q no nombra un secreto: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
}

// TestComodinesLargosNoCuelganAlGate: octava revisión, F2. parseGlobSet buscaba
// el cierre de cada [: [= [. hasta el final del patrón y compileGlob volvía a
// intentarlo en cada [: el tiempo crecía con el cubo del largo, y un comando de
// 12 KB pasaba de los 30 s del hook. El rc.3 no tenía el problema.
func TestComodinesLargosNoCuelganAlGate(t *testing.T) {
	ps, root := secretPaths(t)
	shapes := map[string]string{
		"[[:":  strings.Repeat("[[:", 5000),
		"[[=":  strings.Repeat("[[=", 5000),
		"[[.":  strings.Repeat("[[.", 5000),
		"[":    strings.Repeat("[", 20000),
		"[a":   strings.Repeat("[a", 8000),
		"[\\]": strings.Repeat(`[\]`, 5000),
	}
	for name, long := range shapes {
		var d Decision
		dentroDe(t, 10*time.Second, "cat con un comodín largo de "+name, func() {
			d = ps.Evaluate(bashDe(t, root, "cat "+long))
		})
		if d.Verdict != NeedsApproval {
			t.Errorf("%s: un comodín que el gate no puede leer pide aprobación, no pasa: %s (%s)", name, d.Verdict, d.Reason)
		}
		if len(d.Reason) > 600 {
			t.Errorf("%s: el motivo no puede llevar el comodín entero (%d bytes)", name, len(d.Reason))
		}
		dentroDe(t, 10*time.Second, "cat con un comodín largo y .env, "+name, func() {
			d = ps.Evaluate(bashDe(t, root, "cat "+long+" .env"))
		})
		if d.Verdict != Block {
			t.Errorf("%s: .env al final del comando se bloquea: %s (%s)", name, d.Verdict, d.Reason)
		}
		dentroDe(t, 10*time.Second, "Read con un comodín largo, "+name, func() {
			d = ps.Evaluate(claude(t, "Read", map[string]any{"file_path": root + "/" + long}, root))
		})
		if d.Verdict == Allow {
			t.Errorf("%s: Read de un comodín que no se pudo expandir: %s (%s)", name, d.Verdict, d.Reason)
		}
		dentroDe(t, 10*time.Second, "Grep con un comodín largo, "+name, func() {
			d = ps.Evaluate(claude(t, "Grep", map[string]any{"pattern": "x", "glob": long}, root))
		})
		if d.Verdict == Allow {
			t.Errorf("%s: Grep con un glob que no se pudo expandir: %s (%s)", name, d.Verdict, d.Reason)
		}
	}
	var complete bool
	dentroDe(t, 5*time.Second, "globAll de 15000 caracteres", func() {
		_, complete = globAll(root + "/" + strings.Repeat("[[:", 5000))
	})
	if complete {
		t.Errorf("un comodín de 15000 caracteres no se expande entero")
	}
}

// TestEquivalenciaSeguidaDeOtroCorcheteAlcanzaElArchivo: octava revisión, F1.
// bash 5.2 lee `[[=x=]][n]` como un solo carácter (la equivalencia seguida de
// otro grupo consume uno); el lector del gate leía dos y no alcanzaba .env.
// Un tramo con una equivalencia o un símbolo de colación se lee entonces como
// cualquier nombre: de más y no de menos.
func TestEquivalenciaSeguidaDeOtroCorcheteAlcanzaElArchivo(t *testing.T) {
	dir := t.TempDir()
	mkTree(t, dir, map[string]string{".env": "x", ".envrc": "x", "otro": "x", "src/app.go": "x"})
	pats := []string{
		`.e[[=x=]][n]v`, `.e[[=x=]][[:alpha:]]v`, `.e[[=x=]][a-z]v`, `.e[[=x=]][n-n]v`, `.e[[=x=]][\n]v`,
		`.e[[=x=]][[.n.]]v`, `.e[[=]=]][n]v`, `.e[[=x=]][[=x=]][n]v`, `.e[[=x=]][n]*`, `.e[[:alpha:][[.]][[:alpha:]]v`,
		`.e[[.x.]][n]v`, `.e[[.n.]]v`, `.e[[=n=]]v`,
	}
	for _, p := range pats {
		matches, complete := globAll(dir + "/" + p)
		if !complete {
			t.Errorf("%q: la expansión se cortó", p)
		}
		found := false
		for _, m := range matches {
			if m == dir+"/.env" {
				found = true
			}
		}
		if !found {
			t.Errorf("%q: bash alcanza .env y el lector del gate no (%v)", p, matches)
		}
	}

	ps, root := secretPaths(t)
	for _, p := range pats {
		for _, c := range []string{"cat " + p, "head -3 " + p, "grep -r x " + p} {
			if d := ps.Evaluate(bashDe(t, root, c)); d.Verdict != Block {
				t.Errorf("%q debe bloquearse siempre: %s (%s)", c, d.Verdict, d.Reason)
			}
		}
	}
}

// TestEquivalenciaNoEscondeElGobierno: lo mismo para el gobierno del proyecto.
func TestEquivalenciaNoEscondeElGobierno(t *testing.T) {
	ps, root := secretPaths(t)
	for _, d := range []string{"coyote/ledger", ".claude", ".git"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".claude", "settings.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"rm -rf coyo[[=x=]][t]e", "mv coyot[[=x=]][e] x", "echo x > .cla[[=x=]][u]de/settings.json",
		"rm -rf .gi[[=x=]][t]", "rm -rf coyo[[.t.]][t]e"} {
		if d := ps.Evaluate(bashDe(t, root, c)); d.Verdict != Block {
			t.Errorf("%q toca el gobierno del proyecto: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
}

// TestInterrogacionPorBytesEnLocaleC: octava revisión, F4. En un shell con
// LC_ALL=C un ? casa con un byte: `se??al.txt` alcanza señal.txt. El gate
// cuenta caracteres; ahora lee también por bytes, de más y no de menos.
func TestInterrogacionPorBytesEnLocaleC(t *testing.T) {
	dir := t.TempDir()
	mkTree(t, dir, map[string]string{"secretos/señal.txt": "x", "secretos/signal.txt": "x", "secretos/ab": "x"})
	for _, p := range []string{"se??al.txt", "se?al.txt", "se[!x][!x]al.txt", "se[!x]?al.txt", "se*al.txt", "?????????", "??????????", "s???al.txt", "s??al.txt"} {
		matches, _ := globAll(dir + "/secretos/" + p)
		found := false
		for _, m := range matches {
			if m == dir+"/secretos/señal.txt" {
				found = true
			}
		}
		if !found {
			t.Errorf("%q: bash (locale C o UTF-8) alcanza señal.txt y el gate no (%v)", p, matches)
		}
	}
	// Dos bytes no son tres caracteres: lo que ninguna lectura alcanza no se agrega.
	for _, p := range []string{"se???al.txt", "se[a-z][a-z]al.txt"} {
		matches, _ := globAll(dir + "/secretos/" + p)
		for _, m := range matches {
			if strings.HasSuffix(m, "señal.txt") && strings.Contains(p, "???") {
				t.Errorf("%q: no alcanza señal.txt en ninguna lectura: %v", p, matches)
			}
		}
	}

	ps, root, _ := testPaths(t)
	ps.Secrets = secrets.Rules{Files: []string{"secretos/**"}}
	mkTree(t, root, map[string]string{"secretos/señal.txt": "x"})
	for _, c := range []string{"cat secretos/se??al.txt", "cat secretos/se?al.txt", "cat secretos/se[!x][!x]al.txt"} {
		if d := ps.Evaluate(bashDe(t, root, c)); d.Verdict != Block {
			t.Errorf("%q debe bloquearse siempre: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
}

// TestConjuntoConLetraMultibyteEnLocaleC: en LC_ALL=C un [ñ] es un conjunto de dos
// bytes y cada uno casa con un byte, así que `se[ñ][ñ]` alcanza señ; en UTF-8 lo
// alcanza `se[ñ]`. El gate lee el patrón por caracteres y también por bytes (la
// lectura por bytes compila el patrón aparte: con una letra no ASCII da otro conjunto).
func TestConjuntoConLetraMultibyteEnLocaleC(t *testing.T) {
	dir := t.TempDir()
	mkTree(t, dir, map[string]string{"secretos/señ": "x"})
	for _, p := range []string{"se[ñ][ñ]", "se[!x][ñ]", "se[ñ]", "s?[ñ]", "se[!x]", "señ", "s[a-z][ñ][ñ]"} {
		matches, _ := globAll(dir + "/secretos/" + p)
		found := false
		for _, m := range matches {
			if m == dir+"/secretos/señ" {
				found = true
			}
		}
		if !found {
			t.Errorf("%q: bash (en C o en UTF-8) alcanza señ y el gate no (%v)", p, matches)
		}
	}
	ps, root, _ := testPaths(t)
	ps.Secrets = secrets.Rules{Files: []string{"secretos/**"}}
	mkTree(t, root, map[string]string{"secretos/señ": "x"})
	for _, c := range []string{"cat secretos/se[ñ][ñ]", "cat secretos/se[!x][ñ]", "cat secretos/se[ñ]"} {
		if d := ps.Evaluate(bashDe(t, root, c)); d.Verdict != Block {
			t.Errorf("%q debe bloquearse siempre: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
}

// TestFinalDeRangoConSimboloDeColacion: bash lee [a-[.o.]] como el rango a..o y
// alcanza .env (la n está entre la a y la o). El gate no imita los símbolos de
// colación ni las equivalencias ni las clases: un rango que termina en una de
// ellas hace que el tramo alcance cualquier nombre.
func TestFinalDeRangoConSimboloDeColacion(t *testing.T) {
	dir := t.TempDir()
	mkTree(t, dir, map[string]string{".env": "x"})
	pats := []string{".e[a-[.o.]]v", ".e[m-[.o.]]v", ".e[a-[.n.]]v", ".e[a-[=o=]]v", ".e[a-[:alpha:]]v", ".e[x-[.o.]n]v", ".e[!a-[.m.]]v"}
	for _, p := range pats {
		matches, _ := globAll(dir + "/" + p)
		found := false
		for _, m := range matches {
			if m == dir+"/.env" {
				found = true
			}
		}
		if !found {
			t.Errorf("%q: el gate debe leerlo de más y alcanzar .env (%v)", p, matches)
		}
	}
	ps, root := secretPaths(t)
	for _, p := range pats {
		c := "cat " + p
		if d := ps.Evaluate(bashDe(t, root, c)); d.Verdict != Block {
			t.Errorf("%q debe bloquearse siempre: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
}

// TestMayusculasEnLaExpansion: `shopt -s nocaseglob` hace que `.E?V` alcance
// .env. El gate lee los comodines sin distinguir mayúsculas: de más y no de
// menos, y sin depender de que el comando diga que activa la opción.
func TestMayusculasEnLaExpansion(t *testing.T) {
	dir := t.TempDir()
	mkTree(t, dir, map[string]string{".env": "x"})
	for _, p := range []string{".E?V", ".ENV*", ".[E]NV", ".E[[:alpha:]]V", "*.ENV", ".[a-z]NV", ".E[!x]V"} {
		matches, _ := globAll(dir + "/" + p)
		found := false
		for _, m := range matches {
			if m == dir+"/.env" {
				found = true
			}
		}
		if !found {
			t.Errorf("%q: con nocaseglob alcanza .env y el gate no (%v)", p, matches)
		}
	}
	ps, root := secretPaths(t)
	for _, c := range []string{"shopt -s nocaseglob; cat .E?V", "cat .E?V", "cat .E[N]V"} {
		if d := ps.Evaluate(bashDe(t, root, c)); d.Verdict != Block {
			t.Errorf("%q debe bloquearse siempre: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
}

// bashDeVersion devuelve bash y su versión mayor; el bash de macOS es el 3.2 y
// lee los corchetes de otra manera: la comparación con bash solo vale con 4 o más.
func bashDeVersion(t *testing.T) (string, int) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no hay bash")
	}
	out, err := exec.Command(bash, "-c", "echo ${BASH_VERSINFO[0]}").Output()
	if err != nil {
		t.Skip("bash no responde")
	}
	var v int
	fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &v)
	return bash, v
}

// TestShellGlobGeneradoContraBash: octava revisión. Patrones armados con las
// piezas que ya dieron un hueco (equivalencias, símbolos de colación, clases,
// negaciones, corchetes sueltos) contra un árbol de nombres difíciles, con bash
// real y los dos locales: el gate alcanza, como mínimo, lo que alcanza bash.
func TestShellGlobGeneradoContraBash(t *testing.T) {
	bash, ver := bashDeVersion(t)
	if ver < 5 {
		t.Skipf("bash %d: la comparación vale con bash 5", ver)
	}
	dir := t.TempDir()
	names := []string{".env", ".envrc", ".eNv", ".e]v", ".e[v", ".e-v", "x.env", "a", "b", "ab", "a b", "[x]", "]", "[", "-", "!", "^",
		"señal.txt", "señ", "ñ", "é", "é", "dir/f", "dir/.hidden", ".hidden/x", "Env", "ENV"}
	files := map[string]string{}
	for _, n := range names {
		files[n] = "x"
	}
	mkTree(t, dir, files)

	pieces := []string{"[", "]", "[=x=]", "[=n=]", "[=]=]", "[.n.]", "[.x.]", "[[.]", "[:alpha:]", "[:digit:]", "[:", "n", "v", "e", ".", "*", "?", "!", "^",
		"-", `\`, "a", "b", "x", "señ", "ñ", "é", "[n]", "[!x]", "[a-z]", "[[:alpha:]]", "[[=x=]]", "[[.n.]]", "env",
		"[ñ]", "[!ñ]", "[a-[.o.]]", "[m-[.o.]]", "[a-[=o=]]", "[a-[:alpha:]]", "[[.o.]-z]"}
	next := uint64(0x9e3779b97f4a7c15)
	rnd := func(n int) int {
		next ^= next << 13
		next ^= next >> 7
		next ^= next << 17
		return int(next % uint64(n))
	}
	var pats []string
	seen := map[string]bool{}
	for len(pats) < 1500 {
		var b strings.Builder
		if rnd(3) > 0 {
			b.WriteString(".e")
		}
		for k, n := 0, 1+rnd(5); k < n; k++ {
			b.WriteString(pieces[rnd(len(pieces))])
		}
		if rnd(3) == 0 {
			b.WriteString("v")
		}
		p := b.String()
		if !seen[p] {
			seen[p] = true
			pats = append(pats, p)
		}
	}
	// Los hallazgos de la revisión, tal cual.
	pats = append(pats, `.e[[=x=]][n]v`, `.e[[=x=]][[:alpha:]]v`, `.e[[=x=]][a-z]v`, `.e[[=x=]][n-n]v`, `.e[[=x=]][\n]v`, `.e[[=x=]][[.n.]]v`,
		`.e[[=]=]][n]v`, `.e[[=x=]][[=x=]][n]v`, `.e[[=x=]][n]*`, `.e[[:alpha:][[.]][[:alpha:]]v`, "se??al.txt", "se?al.txt", ".E?V",
		`.e[a-[.o.]]v`, `.e[m-[.o.]]v`, `.e[a-[.n.]]v`, `.e[a-[=o=]]v`, `.e[a-[:alpha:]]v`, `.e[[.o.]-z]v`, "se[ñ][ñ]", "se[!x][ñ]", "se[ñ]", "s?[ñ]")

	for _, loc := range []string{"C", "C.UTF-8"} {
		if loc != "C" {
			if out, err := exec.Command("locale", "-a").Output(); err != nil || !strings.Contains(strings.ToLower(string(out)), "c.utf") {
				continue
			}
		}
		// Un proceso de bash para todos los patrones: eval los lee como los
		// leería la línea de comandos. Cada registro es el patrón y lo que expandió.
		// El guion va en un archivo: como argumento de -c pasaría del tope de un
		// argumento (128 KiB) y bash no correría.
		var script strings.Builder
		script.WriteString("cd \"$1\" && shopt -s dotglob nullglob nocaseglob\n")
		for i, p := range pats {
			script.WriteString("printf '@%d\\n' " + fmt.Sprint(i) + "\n")
			script.WriteString("eval 'for f in " + strings.ReplaceAll(p, "'", `'\''`) + "; do printf \"%s\\n\" \"$f\"; done' 2>/dev/null\n")
		}
		sf := filepath.Join(t.TempDir(), "expandir.sh")
		if err := os.WriteFile(sf, []byte(script.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(bash, sf, dir)
		cmd.Env = []string{"LC_ALL=" + loc, "PATH=" + os.Getenv("PATH")}
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("LC_ALL=%s: bash no corrió el guion: %v", loc, err)
		}
		want := map[int]map[string]bool{}
		cur := -1
		for _, l := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(l, "@") {
				fmt.Sscanf(l, "@%d", &cur)
				want[cur] = map[string]bool{}
				continue
			}
			if cur < 0 || l == "" || l == "." || l == ".." {
				continue
			}
			if _, err := os.Lstat(filepath.Join(dir, l)); err == nil {
				want[cur][l] = true
			}
		}
		// Que la comparación no sea vacía: bash tiene que haber contestado cada
		// patrón, y los de la revisión tienen que alcanzar .env.
		if len(want) != len(pats) {
			t.Fatalf("LC_ALL=%s: bash contestó %d de %d patrones", loc, len(want), len(pats))
		}
		for i, p := range pats {
			if (strings.HasPrefix(p, ".e[[=x=]][n]v") || p == ".e[a-[.o.]]v" || p == ".e[m-[.o.]]v") && !want[i][".env"] {
				t.Fatalf("LC_ALL=%s: bash no expandió %q a .env: la prueba no compara nada", loc, p)
			}
		}
		bad := 0
		for i, p := range pats {
			matches, _ := globAll(dir + "/" + p)
			got := map[string]bool{}
			for _, m := range matches {
				got[strings.TrimPrefix(m, dir+"/")] = true
			}
			var missing []string
			for w := range want[i] {
				if !got[w] {
					missing = append(missing, w)
				}
			}
			sort.Strings(missing)
			if len(missing) > 0 {
				bad++
				if bad <= 12 {
					t.Errorf("LC_ALL=%s, %q: bash alcanza %q y globAll no", loc, p, missing)
				}
			}
		}
		if bad > 12 {
			t.Errorf("LC_ALL=%s: %d patrones más pierden algo que bash alcanza", loc, bad-12)
		}
	}
}
