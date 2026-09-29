package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// gateProject crea un proyecto coyote y deja el entorno como el de una
// persona en su terminal (sin variables de sesión de agente).
func gateProject(t *testing.T) (string, string) {
	t.Helper()
	base := setup(t)
	for _, k := range agentEnv {
		t.Setenv(k, "")
	}
	for _, k := range []string{"CLAUDE_PROJECT_DIR", "CURSOR_PROJECT_DIR", "GEMINI_PROJECT_DIR", "DEVIN_PROJECT_DIR"} {
		t.Setenv(k, "")
	}
	t.Setenv("COYOTE_STATE_DIR", filepath.Join(base, "state"))
	root := filepath.Join(base, "tienda")
	must(t, run(t, base, "", "init", "tienda", "--type", "backend", "--purpose", "API de pedidos de la tienda demo"), 0, "init")
	old := isTerminal
	isTerminal = func() bool { return true }
	t.Cleanup(func() { isTerminal = old })
	return base, root
}

func hook(t *testing.T, root, tool string, input map[string]any, extra map[string]any) string {
	t.Helper()
	m := map[string]any{"session_id": "s1", "hook_event_name": "PreToolUse", "cwd": root, "tool_name": tool, "tool_input": input}
	for k, v := range extra {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return string(b)
}

var propRe = regexp.MustCompile(`P-[a-z0-9]{6}`)

func TestGateFlujoDeAprobacion(t *testing.T) {
	_, root := gateProject(t)
	bash := func(cmd string) string {
		return hook(t, root, "Bash", map[string]any{"command": cmd, "description": "x"}, nil)
	}

	// Lectura: pasa sin registro.
	before := ledgerText(t, root)
	must(t, run(t, root, bash("git status"), "gate", "check"), 0, "lectura")
	if ledgerText(t, root) != before {
		t.Error("una lectura no debe escribir en el ledger")
	}

	// Sin aprobación: bloquea, encola y registra la propuesta una sola vez.
	r := run(t, root, bash("go test ./..."), "gate", "check")
	must(t, r, 2, "sin aprobación")
	id := propRe.FindString(r.stderr)
	if id == "" || !strings.Contains(r.stderr, "coyote approve "+id) {
		t.Fatalf("el mensaje debe decir cómo aprobar: %s", r.stderr)
	}
	must(t, run(t, root, bash("go test ./..."), "gate", "check"), 2, "segundo intento")
	if n := strings.Count(ledgerText(t, root), "prop:"+id); n != 1 {
		t.Errorf("la propuesta debe quedar una vez en el ledger, quedó %d", n)
	}
	out := run(t, root, "", "approvals")
	must(t, out, 0, "approvals")
	if !strings.Contains(out.stdout, id) || !strings.Contains(out.stdout, "2 intentos") || !strings.Contains(out.stdout, "@ana/claude-code") {
		t.Errorf("la cola no muestra la propuesta:\n%s", out.stdout)
	}
	rv := run(t, root, "", "review", id)
	must(t, rv, 0, "review")
	if !strings.Contains(rv.stdout, "$ go test ./...") {
		t.Errorf("review debe mostrar el comando:\n%s", rv.stdout)
	}

	// Un agente no puede aprobar, ni desde el IDE ni sin terminal.
	t.Setenv("CLAUDECODE", "1")
	must(t, run(t, root, "", "approve", id), 1, "aprobar desde una sesión de agente")
	t.Setenv("CLAUDECODE", "")
	isTerminal = func() bool { return false }
	must(t, run(t, root, "", "approve", id), 1, "aprobar sin terminal")
	isTerminal = func() bool { return true }

	// La persona aprueba dos usos.
	must(t, run(t, root, "", "approve", id[:5], "--uses", "2"), 0, "approve")
	rec := filepath.Join(root, "coyote", "approvals", id+".json")
	if _, err := os.Stat(rec); err != nil {
		t.Fatalf("falta el registro: %v", err)
	}
	must(t, run(t, root, bash("go test ./... "), "gate", "check"), 0, "primer uso")
	must(t, run(t, root, bash("go test ./..."), "gate", "check"), 0, "segundo uso")
	r = run(t, root, bash("go test ./..."), "gate", "check")
	must(t, r, 2, "usos agotados")
	if !strings.Contains(ledgerText(t, root), "|gate|gate|aprobado: Bash: go test ./...|apr:"+id) {
		t.Errorf("el uso no quedó en el ledger:\n%s", ledgerText(t, root))
	}

	// Un registro modificado a mano deja de valer.
	id2 := propRe.FindString(r.stderr)
	must(t, run(t, root, "", "approve", id2, "--uses", "5"), 0, "approve 2")
	data := readFile(t, filepath.Join(root, "coyote", "approvals", id2+".json"))
	if err := os.WriteFile(filepath.Join(root, "coyote", "approvals", id2+".json"), []byte(strings.Replace(data, `"uses": 5`, `"uses": 50`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	must(t, run(t, root, bash("go test ./..."), "gate", "check"), 2, "registro alterado")
	all := run(t, root, "", "approvals", "--all")
	if !strings.Contains(all.stdout, "firma inválida") {
		t.Errorf("approvals --all debe señalar el registro alterado:\n%s", all.stdout)
	}

	// Revocar corta una aprobación vigente.
	r = run(t, root, bash("make check"), "gate", "check")
	id3 := propRe.FindString(r.stderr)
	must(t, run(t, root, "", "approve", id3, "--uses", "3"), 0, "approve 3")
	must(t, run(t, root, bash("make check"), "gate", "check"), 0, "uso antes de revocar")
	must(t, run(t, root, "", "revoke", id3, "--reason", "ya no hace falta"), 0, "revoke")
	must(t, run(t, root, bash("make check"), "gate", "check"), 2, "revocada")

	// Rechazar devuelve el motivo al agente.
	r = run(t, root, bash("rm -rf vendor"), "gate", "check")
	id4 := propRe.FindString(r.stderr)
	must(t, run(t, root, "", "reject", id4, "--reason", "no borres vendor, usa go mod tidy"), 0, "reject")
	r = run(t, root, bash("rm -rf vendor"), "gate", "check")
	must(t, r, 2, "rechazada")
	if !strings.Contains(r.stderr, "usa go mod tidy") {
		t.Errorf("el agente debe ver el motivo: %s", r.stderr)
	}
	must(t, run(t, root, "", "reject", id4, "--reason", "x"), 2, "motivo sin sentido")

	// Aprobar un comando antes de que se pida.
	must(t, run(t, root, "", "approve", "--bash", "go vet ./... && go test ./...", "--uses", "1", "--for", "30m"), 0, "approve --bash")
	must(t, run(t, root, bash("go vet ./... && go test ./..."), "gate", "check"), 0, "comando preaprobado")
	must(t, run(t, root, "", "approve", "--bash", "git status"), 0, "lectura no se aprueba")
	must(t, run(t, root, "", "approve", "--bash", "cat ~/.ssh/id_rsa"), 1, "credenciales no se aprueban")
	must(t, run(t, root, "", "approve", "--bash", "make", "--for", "48h"), 2, "más de 24 h")
	must(t, run(t, root, "", "approve", "--bash", "make", "--uses", "500"), 2, "más de 100 usos")
}

func TestGateEditaYBloquea(t *testing.T) {
	_, root := gateProject(t)
	file := filepath.Join(root, "main.go")
	if err := os.WriteFile(file, []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	edit := hook(t, root, "Edit", map[string]any{"file_path": file, "old_string": "func main() {}", "new_string": "func main() { println(1) }"},
		map[string]any{"agent_type": "coyote-dev", "agent_id": "a1"})
	r := run(t, root, edit, "gate", "check")
	must(t, r, 2, "edición sin aprobación")
	id := propRe.FindString(r.stderr)
	rv := run(t, root, "", "review", id)
	if !strings.Contains(rv.stdout, "-func main() {}") || !strings.Contains(rv.stdout, "+func main() { println(1) }") ||
		!strings.Contains(rv.stdout, "@ana/coyote-dev") {
		t.Errorf("review debe mostrar el diff y el agente:\n%s", rv.stdout)
	}
	must(t, run(t, root, "", "approve", "--all"), 0, "approve --all")
	must(t, run(t, root, edit, "gate", "check"), 0, "edición aprobada")
	if !strings.Contains(ledgerText(t, root), "|@ana/coyote-dev|-|tienda|gate|gate|aprobado: Edit main.go") {
		t.Errorf("el ledger debe registrar al agente que reportó el IDE:\n%s", ledgerText(t, root))
	}

	// Bloqueos que ninguna aprobación levanta.
	for _, c := range []string{
		hook(t, root, "Write", map[string]any{"file_path": filepath.Join(root, ".claude", "settings.json"), "content": "{}"}, nil),
		hook(t, root, "Bash", map[string]any{"command": "coyote approve --all"}, nil),
		hook(t, root, "Bash", map[string]any{"command": "git commit -m 'feat(x): y' -m 'Co-Authored-By: Claude <noreply@anthropic.com>'"}, nil),
		hook(t, root, "Bash", map[string]any{"command": "git -c user.email=noreply@anthropic.com commit -m 'feat(x): y'"}, nil),
		hook(t, root, "Bash", map[string]any{"command": "coyote commit -m 'fix(x): y' --agent coyote-reviewer"}, map[string]any{"agent_type": "coyote-dev"}),
	} {
		r := run(t, root, c, "gate", "check")
		must(t, r, 2, "bloqueo: "+c)
		if !strings.Contains(r.stderr, "bloqueado") {
			t.Errorf("se esperaba un bloqueo, no una propuesta: %s", r.stderr)
		}
	}
	q := run(t, root, "", "approvals")
	if propRe.MatchString(q.stdout) {
		t.Errorf("un bloqueo no debe dejar propuestas en la cola:\n%s", q.stdout)
	}
	if strings.Contains(ledgerText(t, root), "Co-Authored-By") {
		t.Error("el ledger no debe copiar la atribución que bloqueó")
	}
}

func TestGateFallaCerrado(t *testing.T) {
	base, root := gateProject(t)
	outside := filepath.Join(base, "sin-proyecto")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		dir, stdin, what string
		want             int
	}{
		{root, "no es json", "entrada ilegible", 2},
		{root, `{"hook_event_name":"PreToolUse","tool_input":{}}`, "sin herramienta", 2},
		{outside, hook(t, outside, "Bash", map[string]any{"command": "rm -rf x"}, nil), "fuera de un proyecto", 2},
		{outside, hook(t, outside, "Read", map[string]any{"file_path": "x"}, nil), "lectura fuera de un proyecto", 0},
	}
	for _, c := range cases {
		r := run(t, c.dir, c.stdin, "gate", "check")
		must(t, r, c.want, c.what)
	}
	// Cursor recibe JSON y el mismo criterio.
	cur, _ := json.Marshal(map[string]any{"hook_event_name": "preToolUse", "tool_name": "Shell", "conversation_id": "c1",
		"tool_input": map[string]any{"command": "npm install", "working_directory": root}, "workspace_roots": []string{root}})
	r := run(t, root, string(cur), "gate", "check", "--ide", "cursor")
	must(t, r, 2, "Cursor sin aprobación")
	if !strings.Contains(r.stdout, `"permission":"deny"`) {
		t.Errorf("Cursor necesita deny en JSON: %s", r.stdout)
	}
	cur, _ = json.Marshal(map[string]any{"hook_event_name": "preToolUse", "tool_name": "Read", "conversation_id": "c1",
		"tool_input": map[string]any{"file_path": filepath.Join(root, "README.md")}, "workspace_roots": []string{root}})
	r = run(t, root, string(cur), "gate", "check", "--ide", "cursor")
	must(t, r, 0, "Cursor lectura")
	if !strings.Contains(r.stdout, `"permission":"allow"`) {
		t.Errorf("Cursor necesita allow en JSON: %s", r.stdout)
	}
	// La aprobación de otra máquina (otra clave) no vale aquí.
	r = run(t, root, hook(t, root, "Bash", map[string]any{"command": "make"}, nil), "gate", "check")
	id := propRe.FindString(r.stderr)
	must(t, run(t, root, "", "approve", id), 0, "approve")
	t.Setenv("COYOTE_STATE_DIR", filepath.Join(base, "otra-maquina"))
	must(t, run(t, root, hook(t, root, "Bash", map[string]any{"command": "make"}, nil), "gate", "check"), 2, "clave de otra máquina")
}

func TestGateUsosEnParalelo(t *testing.T) {
	_, root := gateProject(t)
	in := hook(t, root, "Bash", map[string]any{"command": "go test ./..."}, nil)
	must(t, run(t, root, "", "approve", "--bash", "go test ./...", "--uses", "3"), 0, "approve")
	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := run(t, root, in, "gate", "check")
			if r.code == 0 {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if allowed != 3 {
		t.Errorf("con 3 usos pasaron %d llamadas en paralelo", allowed)
	}
}

func TestInstallYDoctor(t *testing.T) {
	base, root := gateProject(t)
	bin := filepath.Join(base, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "coyote"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	must(t, run(t, root, "", "install", "--ide", "all", "--check"), 1, "check antes de instalar")
	dry := run(t, root, "", "install", "--ide", "claude-code", "--dry-run")
	must(t, dry, 0, "dry-run")
	if _, err := os.Stat(filepath.Join(root, ".claude", "hooks", "coyote-gate.sh")); err == nil {
		t.Fatal("--dry-run no debe escribir")
	}
	t.Setenv("CLAUDECODE", "1")
	must(t, run(t, root, "", "install", "--ide", "claude-code"), 1, "instalar desde una sesión de agente")
	t.Setenv("CLAUDECODE", "")
	r := run(t, root, "", "install", "--ide", "all")
	must(t, r, 0, "install all")
	if !strings.Contains(r.stdout, ".claude/agents/") || !strings.Contains(r.stdout, "14 creado") {
		t.Errorf("salida de install inesperada:\n%s", r.stdout)
	}
	for _, rel := range []string{".claude/settings.json", ".claude/hooks/coyote-gate.sh", ".cursor/hooks.json", ".cursor/hooks/coyote-gate.sh",
		".claude/agents/coyote-sr-solution-architect.md", ".cursor/agents/coyote-sre.md", ".claude/skills/coyote-patch/SKILL.md",
		".agents/skills/coyote-context/SKILL.md", "CLAUDE.md", "AGENTS.md"} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("falta %s", rel)
		}
	}
	must(t, run(t, root, "", "install", "--ide", "all", "--check"), 0, "check después de instalar")
	d := run(t, root, "", "doctor", "--ide", "all")
	must(t, d, 0, "doctor --ide all")
	for _, want := range []string{"gate: autoaprobación", "gate: credenciales", "gate: atribución", "instalación cursor", "gate humano activo"} {
		if !strings.Contains(d.stdout, want) {
			t.Errorf("doctor no reporta %q:\n%s", want, d.stdout)
		}
	}
	if strings.Contains(d.stdout, "✗") {
		t.Errorf("doctor reporta fallas:\n%s", d.stdout)
	}
	// Un agente del proyecto se instala junto a los de la herramienta.
	if err := os.MkdirAll(filepath.Join(root, "coyote", "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "coyote", "agents", "tienda-pagos.md"),
		[]byte("---\nname: tienda-pagos\ndescription: experto en el flujo de cobro\nmodel: sonnet\nmax_turns: 4\ntools: [Read, Grep]\nskills: [coyote-context]\n---\nConoce el cobro.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	must(t, run(t, root, "", "install", "--ide", "claude-code", "--check"), 1, "check con un agente nuevo")
	must(t, run(t, root, "", "install", "--ide", "claude-code"), 0, "reinstalar")
	if _, err := os.Stat(filepath.Join(root, ".claude", "agents", "tienda-pagos.md")); err != nil {
		t.Error("el agente del proyecto no se instaló")
	}
	// Con el gate instalado, lo que un agente escribe en su configuración se bloquea.
	in := hook(t, root, "Edit", map[string]any{"file_path": filepath.Join(root, ".claude", "hooks", "coyote-gate.sh"), "old_string": "exit 2", "new_string": "exit 0"}, nil)
	must(t, run(t, root, in, "gate", "check"), 2, "editar el hook del gate")
}

func TestIdentidadDeAgente(t *testing.T) {
	_, root := gateProject(t)
	must(t, run(t, root, "", "record", "feat", "algo", "--agent", "agente-inventado"), 2, "agente fuera del roster")
	must(t, run(t, root, "", "record", "feat", "cambio del revisor", "--agent", "coyote-reviewer"), 0, "agente del roster")
	must(t, run(t, root, "", "record", "feat", "cambio desde cursor", "--agent", "cursor"), 0, "IDE como agente")
	t.Setenv("CLAUDECODE", "1")
	must(t, run(t, root, "", "record", "feat", "cambio sin declarar"), 0, "sesión de agente")
	t.Setenv("CLAUDECODE", "")
	t.Setenv("COYOTE_IDE", "cursor")
	must(t, run(t, root, "", "note", "los cobros son idempotentes", "--type", "inv", "--scope", "cobros"), 0, "nota desde cursor")
	t.Setenv("COYOTE_IDE", "")
	led := ledgerText(t, root)
	for _, want := range []string{"|@ana/coyote-reviewer|", "|@ana/cursor|-|tienda|feat|", "|@ana/claude-code|-|tienda|feat|-|cambio sin declarar|",
		"|@ana/cursor|-|tienda|note|cobros|"} {
		if !strings.Contains(led, want) {
			t.Errorf("falta %q en el ledger:\n%s", want, led)
		}
	}
	if strings.Contains(led, "agente-inventado") {
		t.Error("un agente inventado no debe llegar al ledger")
	}
	// Un agente del proyecto es válido.
	if err := os.MkdirAll(filepath.Join(root, "coyote", "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "coyote", "agents", "tienda-pagos.md"),
		[]byte("---\nname: tienda-pagos\ndescription: cobro\nmodel: sonnet\nmax_turns: 3\ntools: [Read]\n---\nConoce el cobro.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	must(t, run(t, root, "", "record", "feat", "cambio de pagos", "--agent", "tienda-pagos"), 0, "agente del proyecto")
}

func TestGateProyectoIlegibleYEscapes(t *testing.T) {
	_, root := gateProject(t)
	// Un project.yaml inválido no abre las lecturas: todo se bloquea.
	cfg := filepath.Join(root, "coyote", "project.yaml")
	orig := readFile(t, cfg)
	if err := os.WriteFile(cfg, []byte("version: 1\nname: tienda\nautonomy: inventada\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, in := range []string{
		hook(t, root, "Read", map[string]any{"file_path": filepath.Join(root, "README.md")}, nil),
		hook(t, root, "Read", map[string]any{"file_path": filepath.Join(os.Getenv("HOME"), ".ssh", "id_rsa")}, nil),
		hook(t, root, "WebFetch", map[string]any{"url": "https://example.com/?x=1", "prompt": "x"}, nil),
	} {
		must(t, run(t, root, in, "gate", "check"), 2, "proyecto ilegible")
	}
	if err := os.WriteFile(cfg, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	// Lo que escribió el agente no redibuja la terminal de quien revisa.
	evil := "rm -rf /datos \x1b[2K\r$ git status  ‮"
	r := run(t, root, hook(t, root, "Bash", map[string]any{"command": evil}, nil), "gate", "check")
	must(t, r, 2, "comando con escapes")
	id := propRe.FindString(r.stderr)
	for _, args := range [][]string{{"review", id}, {"approvals"}, {"review"}} {
		out := run(t, root, "", args...)
		if strings.ContainsAny(out.stdout, "\x1b\r‮") {
			t.Errorf("%v imprimió caracteres de control:\n%q", args, out.stdout)
		}
	}
	if rv := run(t, root, "", "review", id); !strings.Contains(rv.stdout, `rm -rf /datos \x1b[2K\x0d$ git status`) {
		t.Errorf("review debe mostrar los escapes visibles:\n%s", rv.stdout)
	}
	if strings.ContainsAny(ledgerText(t, root), "\x1b\r") {
		t.Error("el ledger no debe guardar caracteres de control")
	}
}
