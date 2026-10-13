package gate

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Emmanuel93/coyote/internal/secrets"
)

// Octava revisión de v1.0: los topes del gate se cuentan (rutas distintas que se
// resuelven, trabajo de los comodines, tamaño del comando) y, por si algo se
// escapa, el hook tiene un plazo propio que pide aprobación en lugar de dejar
// que el IDE lo corte a los 30 s y deje pasar la acción.

// topesDeTrabajo baja los topes de esta revisión mientras dura la prueba.
func topesDeTrabajo(t *testing.T, part, work, resolves, bytes int) {
	t.Helper()
	oP, oW, oR, oB := maxGlobPart, maxGlobWork, maxResolves, maxShellBytes
	maxGlobPart, maxGlobWork, maxResolves, maxShellBytes = part, work, resolves, bytes
	t.Cleanup(func() { maxGlobPart, maxGlobWork, maxResolves, maxShellBytes = oP, oW, oR, oB })
}

func TestEvaluateWithinPideAprobacionSiSePasaDelPlazo(t *testing.T) {
	ps, root := secretPaths(t)
	ps.SecretsFor = func(rel string) (secrets.Rules, string, string, error) {
		time.Sleep(3 * time.Second)
		return secrets.Rules{}, "", rel, nil
	}
	a := bashDe(t, root, "cat src/app.go")
	start := time.Now()
	d := ps.EvaluateWithin(a, 200*time.Millisecond)
	if el := time.Since(start); el > 2*time.Second {
		t.Fatalf("el plazo de 200 ms tardó %v", el)
	}
	if d.Verdict != NeedsApproval {
		t.Fatalf("pasado el plazo se pide la aprobación, nunca se deja pasar: %s (%s)", d.Verdict, d.Reason)
	}
	if !strings.Contains(d.Reason, "no alcanzó") || d.Hash == "" || d.Object == "" {
		t.Errorf("el motivo dice que no alcanzó a leerla y trae el hash y el objeto para la cola: %+v", d)
	}
	if d.Hash != ps.Normalize(a).Hash {
		t.Errorf("la propuesta lleva el hash de la acción exacta: %s", d.Hash)
	}
}

// TestEvaluateWithinBloqueaSiNiIdentificarCabe: si ni el hash de la acción se
// puede calcular a tiempo (un disco que no responde), no se encola nada y nada
// pasa: se bloquea sin hash.
func TestEvaluateWithinBloqueaSiNiIdentificarCabe(t *testing.T) {
	lento := func(Action) Decision { time.Sleep(5 * time.Second); return Decision{Verdict: Allow} }
	lentoNorm := func(Action) Normalized { time.Sleep(5 * time.Second); return Normalized{Hash: "sha256:x"} }
	rapidoNorm := func(Action) Normalized { return Normalized{Hash: "sha256:abc", Object: "Bash: x"} }
	a := Action{Tool: "Bash", Command: "cat x"}

	start := time.Now()
	d := withinDeadline(a, 50*time.Millisecond, lento, lentoNorm)
	if el := time.Since(start); el > 3*time.Second {
		t.Errorf("tardó %v: no debe esperar a ninguna de las dos tareas", el)
	}
	if d.Verdict != Block || d.Hash != "" || !strings.Contains(d.Reason, "ni a identificarla") {
		t.Errorf("sin poder identificarla se bloquea, sin hash: %+v", d)
	}
	d = withinDeadline(a, 50*time.Millisecond, lento, rapidoNorm)
	if d.Verdict != NeedsApproval || d.Hash != "sha256:abc" || d.Object != "Bash: x" || !strings.Contains(d.Reason, "no alcanzó") {
		t.Errorf("identificada a tiempo, pide aprobación con su hash: %+v", d)
	}
	rapido := func(Action) Decision { return Decision{Verdict: Allow} }
	if d = withinDeadline(a, 5*time.Second, rapido, lentoNorm); d.Verdict != Allow {
		t.Errorf("lo que termina a tiempo es lo que decidió: %+v", d)
	}
	// Un pánico al identificar no deja pasar nada.
	d = withinDeadline(a, 50*time.Millisecond, lento, func(Action) Normalized { panic("falla de prueba") })
	if d.Verdict != Block {
		t.Errorf("un pánico al identificar bloquea: %+v", d)
	}
}

func TestEvaluateWithinDevuelveLoQueEvaluate(t *testing.T) {
	ps, root := secretPaths(t)
	for _, c := range []string{"cat src/app.go", "cat .env", "rm -rf build", "git status"} {
		a := bashDe(t, root, c)
		want := ps.Evaluate(a)
		got := ps.EvaluateWithin(a, 10*time.Second)
		if got.Verdict != want.Verdict || got.Reason != want.Reason || got.Hash != want.Hash {
			t.Errorf("%q: con plazo %+v, sin plazo %+v", c, got, want)
		}
	}
}

func TestEvaluateWithinPropagaUnPanico(t *testing.T) {
	ps, root := secretPaths(t)
	ps.SecretsFor = func(rel string) (secrets.Rules, string, string, error) { panic("falla de prueba") }
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("un pánico del gate llega a quien lo llamó, que bloquea por seguridad")
		}
	}()
	ps.EvaluateWithin(bashDe(t, root, "cat src/app.go"), 10*time.Second)
}

// TestComandosEnormesNoSePasanDelPlazo: a 100 KB cada palabra de más sumaba
// decenas de consultas al disco (el rc.3 tardaba 30 s con 50 000 palabras): el
// hook de un IDE se corta a los 30 s y la acción pasa.
func TestComandosEnormesNoSePasanDelPlazo(t *testing.T) {
	ps, root := secretPaths(t)
	if raceOn {
		// Con -race las entradas son más chicas y el tope de rutas distintas baja con ellas.
		topesDeTrabajo(t, maxGlobPart, maxGlobWork, escala(maxResolves), maxShellBytes)
	}
	var uniq strings.Builder
	for i := 0; i < escala(16000); i++ {
		fmt.Fprintf(&uniq, "w%d ", i)
	}
	shapes := []struct {
		name, cmd string
		notAllow  bool
	}{
		{"palabras repetidas", "cat " + strings.Repeat("a ", escala(50000)), false},
		{"palabras distintas", "cat " + uniq.String(), true},
		{"llaves", "cat " + strings.Repeat("{a,b}", escala(20000)), true},
		{"llaves por palabra", strings.Repeat("cat {a,b}{c,d}{e,f}{g,h}{i,j}{k,l}{m,n} ", escala(2000)), true},
		{"rutas a/", "cat " + strings.Repeat("a/", escala(50000)), false},
		{"rutas [a]/", "cat " + strings.Repeat("[a]/", escala(25000)), false},
		{"tuberías", "cat " + strings.Repeat("a|", escala(50000)), true},
		{"punto y coma", "cat " + strings.Repeat("a;", escala(50000)), true},
		{"asignaciones", strings.Repeat("a=b ", escala(25000)) + "cat", true},
		{"redirecciones", "cat " + strings.Repeat(">a ", escala(33000)), true},
		{"variables", "cat " + strings.Repeat("$a", escala(50000)), true},
		{"sustituciones", "cat " + strings.Repeat("$(a)", escala(25000)), true},
		{"heredoc", "cat <<EOF\n" + strings.Repeat("linea de texto\n", escala(6600)) + "EOF", true},
		{"tilde", "cat " + strings.Repeat("~/a ", escala(25000)), false},
	}
	for _, s := range shapes {
		var d Decision
		start := time.Now()
		dentroDe(t, 8*time.Second, s.name+" de 100 KB", func() { d = ps.Evaluate(bashDe(t, root, s.cmd)) })
		t.Logf("%-20s %8v  %s", s.name, time.Since(start).Round(time.Millisecond), d.Verdict)
		if s.notAllow && d.Verdict == Allow {
			t.Errorf("%s: no puede pasar sin aprobación (%s)", s.name, d.Reason)
		}
	}
}

// TestUnComandoMuyLargoNoSeLee: pasado el tope de tamaño el gate no lo lee y lo
// pide a la persona, que lo ve entero con coyote review. Es un límite, y está
// dicho: por debajo se lee todo, con los bloqueos duros.
func TestUnComandoMuyLargoNoSeLee(t *testing.T) {
	ps, root := secretPaths(t)
	// El relleno son más argumentos: la misma ruta repetida se resuelve una vez.
	pad := func(n int) string { return strings.Repeat("src/app.go ", n/len("src/app.go ")+1) }
	for _, c := range []string{"cat src/app.go " + pad(maxShellBytes), "cat .env " + pad(maxShellBytes)} {
		if len(c) <= maxShellBytes {
			t.Fatalf("el comando de prueba mide %d y el tope es %d", len(c), maxShellBytes)
		}
		var d Decision
		dentroDe(t, 5*time.Second, "comando de más del tope", func() { d = ps.Evaluate(bashDe(t, root, c)) })
		if d.Verdict != NeedsApproval || !strings.Contains(d.Reason, "demasiado largo") {
			t.Errorf("%.20q…: un comando que el gate no lee pide aprobación: %s (%s)", c, d.Verdict, d.Reason)
		}
		if len(d.Reason) > 600 || len(d.Object) > 400 || d.Hash == "" {
			t.Errorf("el motivo y la descripción no llevan el comando (%d y %d bytes) y la propuesta trae su hash", len(d.Reason), len(d.Object))
		}
	}
	// Por debajo del tope se lee entero: el bloqueo duro sigue, aun con relleno.
	if d := ps.Evaluate(bashDe(t, root, "cat .env "+pad(maxShellBytes/2))); d.Verdict != Block {
		t.Errorf("un secreto con relleno bajo el tope se bloquea: %s (%s)", d.Verdict, d.Reason)
	}
	if d := ps.Evaluate(bashDe(t, root, "cat src/app.go "+pad(maxShellBytes/2))); d.Verdict != Allow {
		t.Errorf("una lectura larga bajo el tope sigue siendo una lectura: %s (%s)", d.Verdict, d.Reason)
	}
}

// TestResolveSeMemoizaYSeAcota: cada palabra del comando se resuelve contra el
// disco (los enlaces simbólicos pueden esconder un secreto). La misma palabra
// repetida cuesta una vez; las distintas tienen tope, y pasado el tope el gate
// sigue leyendo las rutas como texto, sin disco: un secreto nombrado sigue
// bloqueándose y lo demás ya no puede pasar sin aprobación.
func TestResolveSeMemoizaYSeAcota(t *testing.T) {
	ps, root := secretPaths(t)
	var d Decision
	rep := "cat " + strings.Repeat("src/app.go ", escala(11000)) // bajo el tope de tamaño del comando
	if len(rep) > maxShellBytes {
		t.Fatalf("el comando de prueba mide %d y el tope es %d", len(rep), maxShellBytes)
	}
	dentroDe(t, 8*time.Second, "palabras repetidas", func() { d = ps.Evaluate(bashDe(t, root, rep)) })
	if d.Verdict != Allow {
		t.Errorf("la misma ruta repetida es una lectura: %s (%s)", d.Verdict, d.Reason)
	}

	words := make([]string, maxResolves+1000)
	for i := range words {
		words[i] = fmt.Sprintf("src/w%d.go", i)
	}
	many := strings.Join(words, " ")
	dentroDe(t, 8*time.Second, "palabras distintas", func() { d = ps.Evaluate(bashDe(t, root, "cat "+many)) })
	if d.Verdict != NeedsApproval || !strings.Contains(d.Reason, "demasiadas rutas") {
		t.Errorf("más rutas distintas que el tope piden aprobación: %s (%s)", d.Verdict, d.Reason)
	}
	dentroDe(t, 8*time.Second, "palabras distintas y un secreto", func() { d = ps.Evaluate(bashDe(t, root, "cat "+many+" .env")) })
	if d.Verdict != Block {
		t.Errorf("el secreto nombrado al final se bloquea aunque el disco ya no se consulte: %s (%s)", d.Verdict, d.Reason)
	}
	dentroDe(t, 8*time.Second, "palabras distintas y el gobierno", func() { d = ps.Evaluate(bashDe(t, root, "rm -rf "+many+" coyote")) })
	if d.Verdict != Block {
		t.Errorf("el gobierno nombrado al final se bloquea: %s (%s)", d.Verdict, d.Reason)
	}
}

// TestElTrabajoDeUnComodinEstaAcotado: el tiempo de una coincidencia crece con
// el producto del largo del patrón y el del nombre; la suma de todas se cuenta y,
// pasado el tope, la expansión se declara cortada.
func TestElTrabajoDeUnComodinEstaAcotado(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{}
	for i := 0; i < 1000; i++ {
		files[fmt.Sprintf("%s%04d", strings.Repeat("a", 240), i)] = "x"
	}
	mkTree(t, dir, files)
	pat := strings.Repeat("*a", 450) + "*b"
	var complete bool
	dentroDe(t, 8*time.Second, "patrón de 900 elementos contra 1000 nombres de 244 caracteres", func() { _, complete = globAll(dir + "/" + pat) })
	if complete {
		t.Errorf("la expansión pasó del tope de trabajo y no lo dijo")
	}

	topesDeTrabajo(t, 4096, 50, 3000, 128<<10)
	if _, complete = globAll(dir + "/a*"); complete {
		t.Errorf("con 50 unidades de tope, 1000 nombres no se leen enteros")
	}
	topesDeTrabajo(t, 8, 100_000_000, 3000, 128<<10)
	if _, complete = globAll(dir + "/" + strings.Repeat("a", 9) + "*"); complete {
		t.Errorf("un tramo de 10 caracteres con tope de 8 no se lee")
	}
	if got, complete := globAll(dir + "/" + strings.Repeat("a", 3) + "*"); !complete || len(got) != 1000 {
		t.Errorf("un tramo de 4 caracteres con tope de 8 sí: %d coincidencias, completa=%v", len(got), complete)
	}
}

// TestUnaRutaMasLargaQueElSistemaNoSeSigue: una palabra de más de PATH_MAX bytes
// no abre nada y no cuesta disco; una palabra corta que junto a su carpeta pasa del
// tope (una carpeta hondísima) no se sigue, y la evaluación pide aprobación.
func TestUnaRutaMasLargaQueElSistemaNoSeSigue(t *testing.T) {
	ps, _ := secretPaths(t)
	ps.glob = &globWork{}
	big := strings.Repeat("a/", maxPathBytes)
	var got string
	dentroDe(t, 3*time.Second, "palabra de 8 KB", func() { got = ps.resolve(big, "/tmp") })
	if len(got) <= maxPathBytes || ps.glob.cut {
		t.Errorf("una palabra de %d bytes no lee nada y no corta la evaluación (cut=%v)", len(big), ps.glob.cut)
	}
	ps.glob = &globWork{}
	deep := strings.Repeat("/a", maxPathBytes/2+10)
	dentroDe(t, 3*time.Second, "carpeta hondísima", func() { got = ps.resolve("x", deep) })
	if !ps.glob.cut || ps.glob.cutWhy != whyLongPath || got != deep+"/x" {
		t.Errorf("una carpeta de %d bytes corta la evaluación: cut=%v why=%q %q", len(deep), ps.glob.cut, ps.glob.cutWhy, got)
	}
	if r := ps.globCutReason(); !strings.Contains(r, "ruta demasiado larga") {
		t.Errorf("el motivo dice por qué: %s", r)
	}
}

// TestElLatidoNoMiraUnComandoEnorme: octava revisión. El latido corre después de
// decidir y miraba el comando entero para ver si era el canario (16 s con 15 MB):
// si el IDE mataba el hook en ese rato, una negación se convertía en un paso. El
// canario que pide coyote doctor mide unas decenas de bytes.
func TestElLatidoNoMiraUnComandoEnorme(t *testing.T) {
	code := "7f3a9c2b"
	canary := "coyote doctor canary " + code
	if got, ok := CanaryRun(canary); !ok || got != code {
		t.Fatalf("el canario de siempre se reconoce: %q %v", got, ok)
	}
	if got, ok := CanaryRun("cd /tmp && " + canary); !ok || got != code {
		t.Fatalf("el canario tras un cd se reconoce: %q %v", got, ok)
	}
	// Un comando largo, pero dentro del tope, sigue siendo mirado.
	if _, ok := CanaryRun(strings.Repeat("a ", 1000) + "; " + canary); !ok {
		t.Error("un comando de 2 KB con el canario al final se mira")
	}
	// Un comando enorme no es el canario y no cuesta lo que pesa.
	huge := strings.Repeat("a ", 4<<20) + "; " + canary
	start := time.Now()
	if _, ok := CanaryRun(huge); ok {
		t.Error("un comando de 8 MB no es el canario de coyote doctor")
	}
	var p Pulses
	p.Record(Action{IDE: IDEClaudeCode, Tool: "Bash", Command: huge, Input: map[string]any{"command": huge}}, time.Now())
	if el := time.Since(start); el > holgura(time.Second) {
		t.Errorf("anotar el latido de un comando de 8 MB tardó %v", el)
	}
	if len(p.IDEs[IDEClaudeCode].Canary) != 0 {
		t.Error("el comando enorme no deja un canario anotado")
	}
	if p.IDEs[IDEClaudeCode].Calls != 1 {
		t.Error("pero la llamada sí se cuenta")
	}
}

// TestElCompiladorDeComodinesEstaAcotado: el cierre de cada [ se busca hacia
// adelante, así que muchos [ sin cerrar cuestan el cuadrado del largo (32 KB, 2,7 s;
// lo encontró el fuzzing de la verificación final). Quien expande comodines corta
// antes de llegar aquí, pero el compilador no se cuelga con la entrada que sea: un
// patrón de más de maxGlobPart bytes no se compila y alcanza cualquier nombre.
func TestElCompiladorDeComodinesEstaAcotado(t *testing.T) {
	for _, u := range []string{"[a", "[[:alpha:]", "[[:", "[!", "[\\"} {
		p := strings.Repeat(u, 80000/len(u))
		var wild bool
		dentroDe(t, time.Second, fmt.Sprintf("compilar %d bytes de %q", len(p), u), func() {
			_, wild = compileGlob(p, false)
			_, _ = compileGlob(p, true)
			_ = newGlobPat(p).match("abc")
			_ = shellMatch(p, "abc")
		})
		if !wild {
			t.Errorf("un patrón de %d bytes no se compila: alcanza cualquier nombre", len(p))
		}
	}
	// Dentro del tope se compila como siempre.
	if elems, wild := compileGlob(strings.Repeat("[a]", 300), false); wild || len(elems) != 300 {
		t.Errorf("900 bytes sí se compilan: %d elementos, wild=%v", len(elems), wild)
	}
}

// TestUnaExpansionDeLlavesMuyLargaNoSeBusca: braceWordEnd busca dónde cierra una
// llave; sin tope, cada { sin cerrar recorría el resto del comando y 100 KB de
// llaves costaban el cuadrado de su largo. Una expansión de más de maxBraceSpan
// caracteres no es una que alguien escriba: no se lee como expansión, cada llave
// separa comandos y lo que queda pide aprobación (el bloqueo duro de un .env en un
// grupo así baja a aprobación; un límite, y está dicho).
func TestUnaExpansionDeLlavesMuyLargaNoSeBusca(t *testing.T) {
	short := []rune("{a,b}")
	if got := braceWordEnd(short, 0); got != 4 {
		t.Errorf("{a,b} cierra en 4: %d", got)
	}
	long := []rune("{" + strings.Repeat("a", 2*maxBraceSpan) + ",b}")
	if got := braceWordEnd(long, 0); got != -1 {
		t.Errorf("un grupo de %d caracteres no se busca hasta el cierre: %d", len(long), got)
	}
	// Una llave que cierra justo dentro del tope sigue siendo una expansión.
	edge := []rune("{" + strings.Repeat("a", maxBraceSpan-10) + ",b}")
	if got := braceWordEnd(edge, 0); got != len(edge)-1 {
		t.Errorf("un grupo de %d caracteres, dentro del tope, cierra en %d: %d", len(edge), len(edge)-1, got)
	}

	// Y una pila de llaves abiertas más honda que maxBraceScanDepth tampoco: nadie
	// escribe 64 grupos anidados, y seguirlos costaba maxBraceSpan pasos por llave.
	deep := func(n int) []rune {
		return []rune(strings.Repeat("{", n) + "a,b" + strings.Repeat("}", n))
	}
	if got := braceWordEnd(deep(maxBraceScanDepth), 0); got != 2*maxBraceScanDepth+2 {
		t.Errorf("%d grupos anidados, dentro del tope, cierran en %d: %d", maxBraceScanDepth, 2*maxBraceScanDepth+2, got)
	}
	if got := braceWordEnd(deep(maxBraceScanDepth+1), 0); got != -1 {
		t.Errorf("%d grupos anidados no se siguen: %d", maxBraceScanDepth+1, got)
	}

	ps, root := secretPaths(t)
	for _, c := range []string{
		"cat .{env," + strings.Repeat("a", 2*maxBraceSpan) + "}",
		"cat {" + strings.Repeat("a", 2*maxBraceSpan) + ",x}",
	} {
		if d := ps.Evaluate(bashDe(t, root, c)); d.Verdict == Allow {
			t.Errorf("%.30q… (%d bytes): un grupo de llaves que el gate no lee no pasa", c, len(c))
		}
	}
	// Cien mil llaves sin cerrar: el costo es proporcional, no el cuadrado.
	var d Decision
	dentroDe(t, 8*time.Second, "100 000 llaves sin cerrar", func() {
		d = ps.Evaluate(bashDe(t, root, "cat "+strings.Repeat("{", escala(100000))))
		_ = ps.Evaluate(bashDe(t, root, "cat "+strings.Repeat("{a", escala(50000))))
		_ = ps.Evaluate(bashDe(t, root, "cat "+strings.Repeat("{a,", escala(40000))))
	})
	if d.Verdict == Allow {
		t.Errorf("llaves sin cerrar no pasan: %s", d.Reason)
	}
}

// TestUnaRutaEnormeSeJuzgaPorSuNombre: secretFile buscaba los proyectos de cada
// carpeta de la ruta (cuadrado de su largo: decenas de segundos con 100 KB). Una
// ruta de más de maxPathBytes no se abre; se juzga por su nombre, que sigue
// bloqueando un .env al final, y no consulta las reglas de ninguna carpeta.
func TestUnaRutaEnormeSeJuzgaPorSuNombre(t *testing.T) {
	ps, root := secretPaths(t)
	calls := 0
	ps.SecretsFor = func(rel string) (secrets.Rules, string, string, error) {
		calls++
		return secrets.Rules{}, "", rel, nil
	}
	long := root + "/" + strings.Repeat("a/", maxPathBytes)
	var kind string
	var ok bool
	dentroDe(t, 3*time.Second, "ruta de 8 KB", func() { kind, _, ok = ps.secretFile(long + ".env") })
	if !ok || kind == "" {
		t.Errorf("un .env al final de una ruta enorme sigue siendo un archivo de secretos: %q %v", kind, ok)
	}
	if _, _, ok = ps.secretFile(long + "app.go"); ok {
		t.Error("app.go al final de una ruta enorme no es un archivo de secretos")
	}
	if calls != 0 {
		t.Errorf("una ruta enorme no consulta las reglas de cada carpeta: %d llamadas", calls)
	}
	// Con una ruta normal, sí se consultan: la prueba no mide nada si no.
	if _, _, ok = ps.secretFile(root + "/src/app.go"); ok || calls == 0 {
		t.Errorf("una ruta normal consulta las reglas del proyecto (llamadas=%d, ok=%v)", calls, ok)
	}
}

// TestUnComandoDeMegasSeDescribePorSuComienzo: Redact cuesta medio segundo por
// megabyte y la descripción muestra 140 caracteres; la de un comando de 16 MiB no
// puede pagar el comando entero (10 s: pasado el plazo del IDE).
func TestUnComandoDeMegasSeDescribePorSuComienzo(t *testing.T) {
	filler := strings.Repeat("a ", 8<<20) // 16 MiB
	var got string
	dentroDe(t, 3*time.Second, "describir 16 MiB", func() {
		got = describeShell("DB_PASSWORD=hunter2 echo " + filler)
	})
	if strings.Contains(got, "hunter2") {
		t.Errorf("la descripción no lleva el secreto: %q", got)
	}
	if !strings.HasPrefix(got, "DB_PASSWORD=***") || len([]rune(got)) > 141 {
		t.Errorf("la descripción empieza como el comando y muestra 140 caracteres: %q", got)
	}
	// Se corta en un blanco, no en medio de una palabra: sin un blanco en los
	// primeros maxObjectScan bytes no queda nada que mostrar.
	if got := strings.TrimSpace(describeShell(strings.Repeat("a", 2*maxObjectScan))); got != "…" {
		t.Errorf("una sola palabra de %d bytes no se muestra partida: %.30q", 2*maxObjectScan, got)
	}
	if got := describeShell(strings.Repeat("ab ", maxObjectScan)); !strings.HasPrefix(got, "ab ab ab ") || !strings.HasSuffix(got, "…") {
		t.Errorf("un comando largo se describe por su comienzo: %.30q", got)
	}
	// Un comando corto se describe tal cual.
	if got := describeShell("  ls -la \n"); got != "ls -la" {
		t.Errorf("describeShell(ls -la) = %q", got)
	}
	// Y la acción entera, con el comando de megas, se identifica deprisa.
	ps, root := secretPaths(t)
	var d Decision
	dentroDe(t, 5*time.Second, "evaluar 16 MiB", func() { d = ps.Evaluate(bashDe(t, root, "cat src/app.go "+filler)) })
	if d.Verdict != NeedsApproval || d.Hash == "" {
		t.Errorf("un comando de 16 MiB pide aprobación con su hash: %s %q", d.Verdict, d.Hash)
	}
}
