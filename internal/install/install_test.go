package install

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Emmanuel93/coyote/internal/agents"
)

func plan(t *testing.T, root, ide string) []Change {
	t.Helper()
	set, err := agents.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := Plan(Options{Root: root, IDE: ide, Set: set, AgentsMD: "<!-- generado por coyote -->\n# AGENTS.md\n", OwnsMD: true})
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

func pending(cs []Change) []string {
	var out []string
	for _, c := range cs {
		if c.Pending() {
			out = append(out, c.State+" "+c.Path)
		}
	}
	return out
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeCodeInstallMergesAndIsIdempotent(t *testing.T) {
	root := t.TempDir()
	// Configuración de la persona: permisos, un hook propio y el hook viejo de v0.1.
	write(t, root, ".claude/settings.json", `{
  "permissions": {"allow": ["Bash(npm test)"]},
  "attribution": {"commit": "Hecho por el equipo", "sessionUrl": false},
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash|mcp__.*", "hooks": [{"type": "command", "command": "for c in \"$(command -v coyote)\" /usr/local/bin/coyote; do if [ -x \"$c\" ]; then exec \"$c\" gate attribution; fi; done"}]},
      {"matcher": "Write", "hooks": [{"type": "command", "command": "./mi-formateador.sh"}]}
    ],
    "PostToolUse": [{"matcher": "", "hooks": [{"type": "command", "command": "echo listo"}]}]
  }
}`)
	write(t, root, "CLAUDE.md", "# Notas del equipo\n")
	write(t, root, ".claude/agents/coyote-dev.md", "mi propio agente\n")
	cs := plan(t, root, "claude-code")
	if err := Apply(root, cs); err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	data, _ := os.ReadFile(filepath.Join(root, ".claude", "settings.json"))
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("settings inválido: %v\n%s", err, data)
	}
	s := string(data)
	for _, want := range []string{`"Bash(npm test)"`, `"./mi-formateador.sh"`, `"echo listo"`, `"sessionUrl": false`,
		`"commit": ""`, `"pr": ""`, `"COYOTE_IDE": "claude-code"`, GateCommandJSON()} {
		if !strings.Contains(s, want) {
			t.Errorf("falta %s en settings.json:\n%s", want, s)
		}
	}
	if strings.Contains(s, "gate attribution") {
		t.Errorf("el hook viejo de coyote debe reemplazarse:\n%s", s)
	}
	if strings.Index(s, `"permissions"`) > strings.Index(s, `"hooks"`) {
		t.Error("el orden de las claves de la persona debe conservarse")
	}
	if got, _ := os.ReadFile(filepath.Join(root, ".claude", "agents", "coyote-dev.md")); string(got) != "mi propio agente\n" {
		t.Error("un archivo propio con el mismo nombre no se pisa")
	}
	if got, _ := os.ReadFile(filepath.Join(root, "CLAUDE.md")); !strings.Contains(string(got), "# Notas del equipo") || !strings.Contains(string(got), "\n@AGENTS.md\n") {
		t.Errorf("CLAUDE.md debe conservar lo suyo e importar AGENTS.md:\n%s", got)
	}
	info, err := os.Stat(filepath.Join(root, ".claude", "hooks", "coyote-gate.sh"))
	if err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("el hook debe existir y ser ejecutable: %v", err)
	}
	if n := len(pending(plan(t, root, "claude-code"))); n != 0 {
		t.Errorf("la segunda instalación no debe cambiar nada: %v", pending(plan(t, root, "claude-code")))
	}
	// Un agente que desaparece de la definición se borra si lo generó coyote.
	write(t, root, ".claude/agents/coyote-viejo.md", "---\nname: coyote-viejo\n---\n"+agents.Marker+"\n")
	write(t, root, ".claude/agents/propio.md", "propio\n")
	got := strings.Join(pending(plan(t, root, "claude-code")), "\n")
	if !strings.Contains(got, "borrado .claude/agents/coyote-viejo.md") || strings.Contains(got, "propio.md") {
		t.Errorf("limpieza inesperada:\n%s", got)
	}
}

// GateCommandJSON es el comando del hook tal como queda escrito en JSON.
func GateCommandJSON() string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(GateCommand)
	return strings.TrimSpace(b.String())
}

func TestCursorInstall(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".cursor/hooks.json", `{"version": 1, "hooks": {"afterFileEdit": [{"command": "./fmt.sh"}], "beforeShellExecution": [{"command": ".cursor/hooks/coyote-gate.sh"}]}}`)
	cs := plan(t, root, "cursor")
	if err := Apply(root, cs); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(root, ".cursor", "hooks.json"))
	s := string(data)
	if !strings.Contains(s, `"./fmt.sh"`) || !strings.Contains(s, `"preToolUse"`) || !strings.Contains(s, `"failClosed": true`) || strings.Contains(s, "beforeShellExecution") {
		t.Errorf("hooks.json inesperado:\n%s", s)
	}
	for _, rel := range []string{".cursor/agents/coyote-reviewer.md", ".agents/skills/coyote-review/SKILL.md", "AGENTS.md"} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("falta %s", rel)
		}
	}
	if n := len(pending(plan(t, root, "cursor"))); n != 0 {
		t.Errorf("la segunda instalación no debe cambiar nada: %v", pending(plan(t, root, "cursor")))
	}
}

func TestInvalidConfigIsNotOverwritten(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".claude/settings.json", "{ esto no es json")
	set, _ := agents.Load("")
	if _, err := Plan(Options{Root: root, IDE: "claude-code", Set: set}); err == nil || !strings.Contains(err.Error(), "no lo pisa") {
		t.Fatalf("un settings.json inválido no se pisa: %v", err)
	}
	write(t, root, ".claude/settings.json", `{"hooks": "texto"}`)
	if _, err := Plan(Options{Root: root, IDE: "claude-code", Set: set}); err == nil {
		t.Fatal("hooks que no es un objeto no se pisa")
	}
	if _, err := Plan(Options{Root: root, IDE: "vim", Set: set}); err == nil {
		t.Fatal("un IDE desconocido debe fallar")
	}
}

func TestGateScriptFailsClosed(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sin sh")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "gate.sh")
	if err := os.WriteFile(script, []byte(GateScript("claude-code")), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", script)
	cmd.Env = []string{"PATH=" + dir, "HOME=" + dir}
	out, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 2 || !strings.Contains(string(out), "no está instalado") {
		t.Fatalf("sin coyote el hook debe salir con 2: %v %s", err, out)
	}
	// Con un coyote en el PATH, el hook le pasa la llamada.
	fake := filepath.Join(dir, "coyote")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho \"llamado: $*\"\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command("sh", script)
	cmd.Env = []string{"PATH=" + dir + ":/usr/bin:/bin", "HOME=" + dir}
	out, err = cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "llamado: gate check --ide claude-code") {
		t.Fatalf("el hook debe llamar a coyote gate check: %v %s", err, out)
	}
}

func TestNewIDEInstalls(t *testing.T) {
	root := t.TempDir()
	// Configuración propia de la persona en cada IDE: se conserva.
	write(t, root, ".codex/hooks.json", `{"hooks": {"PreToolUse": [{"matcher": "^Bash$", "hooks": [{"type": "command", "command": "./mio.sh"}]}], "Stop": [{"hooks": [{"type": "command", "command": "./fin.sh"}]}]}}`)
	write(t, root, ".gemini/settings.json", `{"theme": "Dracula", "context": {"fileName": "GEMINI.md"}, "hooks": {"AfterTool": [{"matcher": ".*", "hooks": [{"type": "command", "command": "./log.sh"}]}]}}`)
	write(t, root, ".windsurf/hooks.json", `{"hooks": {"post_write_code": [{"command": "./fmt.sh"}], "pre_run_command": [{"command": "./mio.sh", "show_output": true}]}}`)
	for _, ide := range []string{"codex", "gemini", "copilot", "windsurf"} {
		if err := Apply(root, plan(t, root, ide)); err != nil {
			t.Fatalf("%s: %v", ide, err)
		}
		if p := pending(plan(t, root, ide)); len(p) != 0 {
			t.Errorf("%s: la segunda instalación no debe cambiar nada: %v", ide, p)
		}
	}
	read := func(rel string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	codex := read(".codex/hooks.json")
	for _, want := range []string{`"./mio.sh"`, `"./fin.sh"`, `"PreToolUse"`, CodexHook, `"matcher": ".*"`} {
		if !strings.Contains(codex, want) {
			t.Errorf(".codex/hooks.json sin %s:\n%s", want, codex)
		}
	}
	gemini := read(".gemini/settings.json")
	for _, want := range []string{`"Dracula"`, `"./log.sh"`, `"BeforeTool"`, GeminiHook, `GEMINI_PROJECT_DIR:-`, `"AGENTS.md"`, `"GEMINI.md"`} {
		if !strings.Contains(gemini, want) {
			t.Errorf(".gemini/settings.json sin %s:\n%s", want, gemini)
		}
	}
	var gs struct {
		Context struct {
			FileName []string `json:"fileName"`
		} `json:"context"`
	}
	if err := json.Unmarshal([]byte(gemini), &gs); err != nil || strings.Join(gs.Context.FileName, ",") != "AGENTS.md,GEMINI.md" {
		t.Errorf("context.fileName debe leer AGENTS.md y conservar GEMINI.md: %v %v", gs.Context.FileName, err)
	}
	copilot := read(CopilotFile)
	for _, want := range []string{`"version": 1`, `"preToolUse"`, CopilotHook, `"timeoutSec": 30`, `"powershell"`} {
		if !strings.Contains(copilot, want) {
			t.Errorf("%s sin %s:\n%s", CopilotFile, want, copilot)
		}
	}
	wind := read(".windsurf/hooks.json")
	for _, want := range append([]string{`"./fmt.sh"`, `"./mio.sh"`, WindsurfHook}, windsurfEvents...) {
		if !strings.Contains(wind, want) {
			t.Errorf(".windsurf/hooks.json sin %s:\n%s", want, wind)
		}
	}
	var ws struct {
		Hooks map[string][]struct {
			Command string `json:"command"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(wind), &ws); err != nil {
		t.Fatal(err)
	}
	for _, ev := range windsurfEvents {
		n := 0
		for _, h := range ws.Hooks[ev] {
			if strings.Contains(h.Command, WindsurfHook) {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s: %d hooks de coyote, se esperaba uno", ev, n)
		}
	}
	for _, rel := range []string{CodexHook, GeminiHook, CopilotHook, WindsurfHook, ".agents/skills/coyote-review/SKILL.md"} {
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("falta %s", rel)
			continue
		}
		if strings.HasSuffix(rel, ".sh") && info.Mode()&0o100 == 0 {
			t.Errorf("%s sin permiso de ejecución", rel)
		}
	}
	for ide, want := range map[string]string{CodexHook: "--ide codex", GeminiHook: "--ide gemini", CopilotHook: "--ide copilot", WindsurfHook: "--ide windsurf"} {
		if !strings.Contains(read(ide), want) {
			t.Errorf("%s no llama a gate check %s", ide, want)
		}
	}
	// Un archivo de Copilot con el nombre de coyote que no generó coyote no se
	// pisa, pero deja a Copilot sin gate: es un conflicto, no un omitido.
	other := t.TempDir()
	write(t, other, CopilotFile, `{"version": 1, "hooks": {"preToolUse": [{"type": "command", "bash": "./notify-coyote-gateway.sh"}]}}`)
	for _, c := range plan(t, other, "copilot") {
		if c.Path == CopilotFile && (c.State != Conflict || !c.Blocking() || c.Pending()) {
			t.Errorf("%s ajeno: %s", CopilotFile, c.State)
		}
	}
	// Un context.fileName con otra forma no se pisa.
	bad := t.TempDir()
	write(t, bad, ".gemini/settings.json", `{"context": {"fileName": 3}}`)
	set, _ := agents.Load("")
	if _, err := Plan(Options{Root: bad, IDE: "gemini", Set: set}); err == nil {
		t.Error("un context.fileName inválido se pisó")
	}
}

func TestLauncherFailsClosed(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sin sh")
	}
	root := t.TempDir()
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(cwd, launcher string, env ...string) (string, int) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "sh", "-c", launcher)
		cmd.Dir = cwd
		cmd.Env = append([]string{"PATH=/usr/bin:/bin"}, env...)
		out, err := cmd.CombinedOutput()
		if ctx.Err() != nil {
			t.Fatalf("el lanzador no terminó: %s", out)
		}
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return string(out), code
	}
	hook := func(dir, rel, body string, mode os.FileMode) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		_ = os.RemoveAll(p)
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	codex := Launcher(CodexHook, ".codex/hooks.json")
	// Sin el hook en ninguna carpeta: niega con 2, no deja pasar.
	if out, code := run(sub, codex); code != 2 || !strings.Contains(out, "no encuentro") {
		t.Fatalf("sin hook el lanzador debe salir con 2: %d %s", code, out)
	}
	// Con el hook en la raíz, lo encuentra desde una subcarpeta.
	fake := func(exit int) string {
		return "#!/bin/sh\n# coyote gate check --ide codex\necho hook-de-coyote\nexit " + fmt.Sprint(exit) + "\n"
	}
	hook(root, CodexHook, fake(0), 0o755)
	if out, code := run(sub, codex); code != 0 || !strings.Contains(out, "hook-de-coyote") {
		t.Fatalf("el lanzador debe encontrar el hook subiendo: %d %s", code, out)
	}
	// Cualquier salida distinta de 0 del hook niega con 2.
	for _, exit := range []int{1, 2, 126, 127} {
		hook(root, CodexHook, fake(exit), 0o755)
		if _, code := run(sub, codex); code != 2 {
			t.Errorf("el hook salió con %d y el lanzador con %d; se esperaba 2", exit, code)
		}
	}
	// Un hook con CRLF, vacío, sin la marca de coyote, una carpeta o sin
	// permiso de ejecución: nunca deja pasar sin correr el gate.
	cases := map[string]func(){
		"CRLF": func() {
			hook(root, CodexHook, strings.ReplaceAll("#!/bin/sh\nfor x in 1; do\n  echo gate check --ide codex\ndone\nexit 0\n", "\n", "\r\n"), 0o755)
		},
		"vacío":        func() { hook(root, CodexHook, "", 0o755) },
		"ajeno":        func() { hook(root, CodexHook, "#!/bin/sh\nexit 0\n", 0o755) },
		"una carpeta":  func() { _ = os.RemoveAll(filepath.Join(root, CodexHook)); _ = os.MkdirAll(filepath.Join(root, CodexHook), 0o755) },
		"sin permisos": func() { hook(root, CodexHook, fake(3), 0o644) },
	}
	for name, setup := range cases {
		setup()
		if out, code := run(sub, codex); code != 2 {
			t.Errorf("hook %s: el lanzador salió con %d (%s); se esperaba 2", name, code, out)
		}
	}
	// Sin permiso de ejecución, el hook igual corre con sh.
	hook(root, CodexHook, fake(0), 0o644)
	if out, code := run(sub, codex); code != 0 || !strings.Contains(out, "hook-de-coyote") {
		t.Errorf("un hook sin permiso de ejecución corre con sh: %d %s", code, out)
	}
	// No sube más allá del proyecto: el hook de una carpeta padre no corre.
	parent := t.TempDir()
	hook(parent, CodexHook, "#!/bin/sh\n# gate check --ide codex\necho hook-ajeno\nexit 0\n", 0o755)
	proj := filepath.Join(parent, "proyecto")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, code := run(proj, codex); code != 2 || strings.Contains(out, "hook-ajeno") {
		t.Errorf("corrió el hook de la carpeta padre: %d %s", code, out)
	}
	// Gemini da la carpeta del proyecto en una variable; relativa, no se cuelga.
	gemini := Launcher(GeminiHook, ".gemini/settings.json", "GEMINI_PROJECT_DIR")
	hook(root, GeminiHook, "#!/bin/sh\n# gate check --ide gemini\necho hook-gemini\nexit 0\n", 0o755)
	if out, code := run(t.TempDir(), gemini, "GEMINI_PROJECT_DIR="+root); code != 0 || !strings.Contains(out, "hook-gemini") {
		t.Fatalf("el lanzador debe usar GEMINI_PROJECT_DIR: %d %s", code, out)
	}
	if out, code := run(filepath.Dir(root), gemini, "GEMINI_PROJECT_DIR="+filepath.Base(root)); code != 0 || !strings.Contains(out, "hook-gemini") {
		t.Errorf("GEMINI_PROJECT_DIR relativo: %d %s", code, out)
	}
	if _, code := run(t.TempDir(), gemini, "GEMINI_PROJECT_DIR=no/existe"); code != 2 {
		t.Errorf("GEMINI_PROJECT_DIR relativo que no existe: %d", code)
	}
	// El script de Copilot niega en JSON si coyote no está.
	dir := t.TempDir()
	script := filepath.Join(dir, "gate.sh")
	if err := os.WriteFile(script, []byte(GateScript("copilot")), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", script)
	cmd.Env = []string{"PATH=" + dir, "HOME=" + dir}
	out, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 2 || !strings.Contains(string(out), `"permissionDecision":"deny"`) {
		t.Fatalf("sin coyote, el hook de Copilot niega: %v %s", err, out)
	}
	// Un coyote que no puede correr (roto o de otra arquitectura) niega con 2.
	broken := filepath.Join(dir, "coyote")
	if err := os.WriteFile(broken, []byte("\x7fELF basura"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command("sh", script)
	cmd.Env = []string{"PATH=" + dir + ":/usr/bin:/bin", "HOME=" + dir}
	if _, err := cmd.CombinedOutput(); err == nil {
		t.Fatal("con un coyote roto, el hook debe negar")
	} else if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 2 {
		t.Fatalf("con un coyote roto, el hook sale con 2: %v", err)
	}
}

func TestHooksDeLaPersonaConNombreParecido(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".claude/settings.json", `{"hooks": {"PreToolUse": [{"matcher": "", "hooks": [
	  {"type": "command", "command": "./scripts/notify-coyote-gateway.sh"},
	  {"type": "command", "command": "\"$CLAUDE_PROJECT_DIR\"/.claude/hooks/coyote-gate.sh"}]}]}}`)
	if err := Apply(root, plan(t, root, "claude-code")); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(root, ".claude", "settings.json"))
	s := string(data)
	if !strings.Contains(s, "notify-coyote-gateway.sh") {
		t.Errorf("se borró un hook de la persona:\n%s", s)
	}
	if strings.Contains(s, `"\"$CLAUDE_PROJECT_DIR\"/.claude/hooks/coyote-gate.sh"`) || !strings.Contains(s, GateCommandJSON()) {
		t.Errorf("el hook anterior de coyote se reemplaza por el lanzador:\n%s", s)
	}
}

func TestApplyNoEscribePorUnTemporalPlantado(t *testing.T) {
	root := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victima.txt")
	if err := os.WriteFile(victim, []byte("intacto"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(root, ".codex", "hooks.json.coyote-tmp")); err != nil {
		t.Fatal(err)
	}
	if err := Apply(root, plan(t, root, "codex")); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(victim); string(got) != "intacto" {
		t.Errorf("la instalación escribió por el symlink: %q", got)
	}
	if info, err := os.Lstat(filepath.Join(root, ".codex", "hooks.json")); err != nil || !info.Mode().IsRegular() {
		t.Errorf(".codex/hooks.json es un archivo regular: %v", err)
	}
}
