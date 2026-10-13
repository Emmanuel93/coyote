package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Emmanuel93/coyote/internal/approval"
	"github.com/Emmanuel93/coyote/internal/gate"
)

// Octava revisión, topes del hook. El filtro de R15 cuesta cerca de 1,4 s por
// MiB: con un cuerpo de 14 MiB el hook pasaba del plazo del IDE (30 s) y el IDE,
// al matarlo, dejaba publicar lo que no se revisó. Ahora hay un tope al texto
// que se revisa (más grande se bloquea con un mensaje claro), el latido no mira
// comandos enormes y el hook entero tiene un plazo propio.

func lowerAttributionCap(t *testing.T, n int) {
	t.Helper()
	old := maxAttributionText
	maxAttributionText = n
	t.Cleanup(func() { maxAttributionText = old })
}

// plazoDelHook estira el plazo total del hook mientras dura la prueba si corre con
// -race: revisar R15 cuesta 1,4 s por MiB y con el detector de carreras unas doce
// veces más, y lo que estas pruebas miran es el veredicto, no el plazo.
func plazoDelHook(t *testing.T) {
	t.Helper()
	old := gate.HookDeadline
	gate.HookDeadline = holgura(old)
	t.Cleanup(func() { gate.HookDeadline = old })
}

func plainText(n int) string {
	return strings.Repeat("Una línea de texto cualquiera, sin nada raro.\n", n/46+1)[:n]
}

// lineasCortas es un relleno de n bytes que el filtro de R15 recorre en una fracción
// del tiempo (líneas de un carácter: 77 ms por MiB contra 1,8 s con plainText). Las
// pruebas que solo necesitan pasar de un tamaño, no medir el filtro, lo usan.
func lineasCortas(n int) string { return strings.Repeat("a\n", n/2+1)[:n] }

// TestR15TextoQueSePublicaTieneTope: un texto de una herramienta que publica se
// revisa entero hasta el tope; más grande se bloquea, no se deja sin revisar.
func TestR15TextoQueSePublicaTieneTope(t *testing.T) {
	plazoDelHook(t)
	_, root := gateProject(t)
	lowerAttributionCap(t, 64<<10)
	mcp := func(body string) string {
		return hook(t, root, "mcp__github__create_pull_request", map[string]any{"title": "x", "body": body}, nil)
	}
	r := run(t, root, mcp(plainText(100<<10)), "gate", "check")
	must(t, r, 2, "texto más largo que el tope")
	if !strings.Contains(r.stderr, "R15") || !strings.Contains(r.stderr, "partes más cortas") {
		t.Errorf("un texto que no se alcanza a revisar se bloquea con el motivo: %s", r.stderr)
	}
	// Dentro del tope se revisa entero: sin atribución no lo bloquea R15 (queda como cualquier herramienta que publica)...
	r = run(t, root, mcp(plainText(40<<10)), "gate", "check")
	if strings.Contains(r.stderr, "R15") {
		t.Errorf("40 KiB de texto limpio no son un caso de R15: %s", r.stderr)
	}
	// ... y con la atribución en la última línea, sí.
	r = run(t, root, mcp(plainText(40<<10)+"\n"+aiTrailer+"\n"), "gate", "check")
	must(t, r, 2, "atribución al final de un texto largo")
	if !strings.Contains(r.stderr, "R15") || !strings.Contains(r.stderr, "atribución") {
		t.Errorf("la atribución al final se bloquea por R15: %s", r.stderr)
	}
}

// TestR15UnCuerpoDeDocePasaDelPlazoSinElTope: con el tope de verdad, un cuerpo de
// 12 MiB (24 s de filtro antes) se bloquea en menos de unos segundos.
func TestR15UnCuerpoEnormeSeBloqueaRapido(t *testing.T) {
	plazoDelHook(t)
	_, root := gateProject(t)
	in := hook(t, root, "mcp__github__add_issue_comment", map[string]any{"body": plainText(12 << 20)}, nil)
	start := time.Now()
	r := run(t, root, in, "gate", "check")
	el := time.Since(start)
	must(t, r, 2, "cuerpo de 12 MiB")
	if !strings.Contains(r.stderr, "R15") || !strings.Contains(r.stderr, "partes más cortas") {
		t.Errorf("el cuerpo enorme se bloquea con el motivo: %s", r.stderr)
	}
	if el > holgura(8*time.Second) {
		t.Errorf("el hook tardó %v con un cuerpo de 12 MiB", el)
	}
}

// TestR15ArchivoDeMensajeNoSeLeeCortado: antes solo se leía el primer MiB de un
// archivo de mensaje (-F, --body-file): una atribución después quedaba sin ver.
func TestR15ArchivoDeMensajeNoSeLeeCortado(t *testing.T) {
	plazoDelHook(t)
	_, root := gateProject(t)
	msg := filepath.Join(root, "msg.txt")
	commit := hook(t, root, "Bash", map[string]any{"command": "git commit -F msg.txt"}, nil)

	if err := os.WriteFile(msg, []byte(lineasCortas(1200<<10)+"\n"+aiTrailer+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := run(t, root, commit, "gate", "check")
	must(t, r, 2, "atribución después del primer MiB")
	if !strings.Contains(r.stderr, "R15") || !strings.Contains(r.stderr, "atribución") {
		t.Errorf("la atribución pasado el primer MiB se ve: %s", r.stderr)
	}

	// Un archivo más grande que el tope no se deja pasar sin mirar.
	lowerAttributionCap(t, 256<<10)
	if err := os.WriteFile(msg, []byte(lineasCortas(300<<10)), 0o644); err != nil {
		t.Fatal(err)
	}
	r = run(t, root, commit, "gate", "check")
	must(t, r, 2, "archivo de mensaje más grande que el tope")
	if !strings.Contains(r.stderr, "R15") || !strings.Contains(r.stderr, "partes más cortas") {
		t.Errorf("un archivo de mensaje enorme se bloquea con el motivo: %s", r.stderr)
	}
}

// TestFileContentsLeeHastaElTope: lo que se lee de los archivos de mensaje cabe
// en el espacio que queda, y pasado el tope el resultado lo supera.
func TestFileContentsLeeHastaElTope(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(big, []byte(strings.Repeat("x", 5000)), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := "git commit -F big.txt -F big.txt"
	if got := fileContents(cmd, dir, 1000); len(got) < 1000 || len(got) > 1002 {
		t.Errorf("con espacio para 1000 bytes se leyeron %d", len(got))
	}
	if got := fileContents(cmd, dir, 0); got != "" {
		t.Errorf("sin espacio no se lee nada: %d bytes", len(got))
	}
	if got := fileContents(cmd, dir, 100000); len(got) != 2*5001 {
		t.Errorf("con espacio de sobra se leen los dos archivos enteros: %d", len(got))
	}
}

// TestGateAttributionLeeLaEntradaEntera: gate attribution (el hook de v0.1) leía
// solo el primer MiB de la entrada.
func TestGateAttributionLeeLaEntradaEntera(t *testing.T) {
	plazoDelHook(t)
	_, root := gateProject(t)
	in := hook(t, root, "mcp__github__add_issue_comment", map[string]any{"body": lineasCortas(1500<<10) + "\n" + aiTrailer + "\n"}, nil)
	r := run(t, root, in, "gate", "attribution")
	must(t, r, 2, "atribución pasado el primer MiB de la entrada")
	if !strings.Contains(r.stderr, "R15") {
		t.Errorf("gate attribution ve la atribución al final: %s", r.stderr)
	}
}

// TestGatePlazoTotalDelHook: si el hook no responde en gate.HookDeadline (aquí,
// una entrada que no termina), niega por su cuenta en el formato del IDE.
func TestGatePlazoTotalDelHook(t *testing.T) {
	_, root := gateProject(t)
	old := gate.HookDeadline
	gate.HookDeadline = 300 * time.Millisecond
	t.Cleanup(func() { gate.HookDeadline = old })

	pr, pw := io.Pipe()
	t.Cleanup(func() { pw.Close() })
	for _, c := range []struct{ ide, want string }{
		{"claude", ""},
		{"cursor", `"permission":"deny"`},
		{"copilot", `"permissionDecision":"deny"`},
	} {
		var out, errb bytes.Buffer
		start := time.Now()
		code := Main([]string{"-C", root, "gate", "check", "--ide", c.ide}, pr, &out, &errb)
		if el := time.Since(start); el > 3*time.Second {
			t.Errorf("%s: tardó %v con un plazo de 300 ms", c.ide, el)
		}
		if code != 2 {
			t.Errorf("%s: código %d, se esperaba 2 (negar)", c.ide, code)
		}
		if !strings.Contains(errb.String(), "no terminó de revisar") {
			t.Errorf("%s: la negación dice por qué: %q", c.ide, errb.String())
		}
		if c.want != "" && !strings.Contains(out.String(), c.want) {
			t.Errorf("%s: la respuesta va en el formato del IDE (%s): %q", c.ide, c.want, out.String())
		}
		if c.want == "" && out.Len() != 0 {
			t.Errorf("%s: no escribe nada en la salida estándar: %q", c.ide, out.String())
		}
	}
}

// TestGatePlazoTotalRespetaElPasoYaDado: si el hook ya dejó pasar la acción y solo
// le falta anotar el latido (aquí, el candado ocupado), el plazo no lo convierte
// en una negación: sale con lo que ya respondió. El candado no se suelta: el
// cuerpo, ya sin hook, sigue esperándolo unos segundos y no debe escribir en la
// carpeta mientras la prueba la borra.
func TestGatePlazoTotalRespetaElPasoYaDado(t *testing.T) {
	_, root := gateProject(t)
	old := gate.HookDeadline
	gate.HookDeadline = 600 * time.Millisecond
	t.Cleanup(func() { gate.HookDeadline = old })
	if _, err := approval.Lock(root); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	r := run(t, root, hook(t, root, "Bash", map[string]any{"command": "git status"}, nil), "gate", "check")
	el := time.Since(start)
	must(t, r, 0, "una lectura con el candado ocupado")
	if strings.Contains(r.stderr, "no terminó") || r.stdout != "" {
		t.Errorf("la lectura ya había pasado: stdout=%q stderr=%q", r.stdout, r.stderr)
	}
	if el > 3*time.Second {
		t.Errorf("tardó %v: el plazo era de 600 ms", el)
	}
}

// TestGatePlazoTotalNiegaEnElFormatoDeLaEntrada: sin --ide, el IDE sale de la
// entrada; si el hook se detiene después de leerla (aquí, esperando el candado de
// las aprobaciones), la negación del plazo va en el formato de ese IDE.
func TestGatePlazoTotalNiegaEnElFormatoDeLaEntrada(t *testing.T) {
	_, root := gateProject(t)
	old := gate.HookDeadline
	gate.HookDeadline = 400 * time.Millisecond
	t.Cleanup(func() { gate.HookDeadline = old })
	if _, err := approval.Lock(root); err != nil {
		t.Fatal(err)
	}
	cur, _ := json.Marshal(map[string]any{"hook_event_name": "preToolUse", "tool_name": "Shell", "conversation_id": "c1",
		"tool_input": map[string]any{"command": "npm install", "working_directory": root}, "workspace_roots": []string{root}})
	start := time.Now()
	r := run(t, root, string(cur), "gate", "check")
	must(t, r, 2, "acción que espera el candado")
	if !strings.Contains(r.stdout, `"permission":"deny"`) || !strings.Contains(r.stderr, "no terminó de revisar") {
		t.Errorf("Cursor necesita el deny en JSON y el motivo: stdout=%q stderr=%q", r.stdout, r.stderr)
	}
	if el := time.Since(start); el > 3*time.Second {
		t.Errorf("tardó %v: el plazo era de 400 ms", el)
	}
}

type lectorRoto struct{}

func (lectorRoto) Read([]byte) (int, error) { panic("lector roto") }

// TestGateUnPanicDelCuerpoNiega: el cuerpo del hook corre en su propia
// goroutine; un panic ahí, sin recuperar, tumbaría el proceso sin la respuesta
// del IDE. Se recupera y se niega, en el formato del IDE.
func TestGateUnPanicDelCuerpoNiega(t *testing.T) {
	_, root := gateProject(t)
	var out, errb bytes.Buffer
	code := Main([]string{"-C", root, "gate", "check", "--ide", "cursor"}, lectorRoto{}, &out, &errb)
	if code != 2 || !strings.Contains(out.String(), `"permission":"deny"`) || !strings.Contains(errb.String(), "el gate falló") {
		t.Errorf("un panic del gate niega: código %d, stdout=%q stderr=%q", code, out.String(), errb.String())
	}
}
