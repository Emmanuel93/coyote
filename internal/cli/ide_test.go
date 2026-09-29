package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func ideInput(t *testing.T, m map[string]any) string {
	t.Helper()
	b, _ := json.Marshal(m)
	return string(b)
}

var canaryRe = regexp.MustCompile(`coyote doctor canary ([a-z0-9]+)`)

func TestIDEsYCanario(t *testing.T) {
	base, root := gateProject(t)
	bin := filepath.Join(base, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "coyote"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	must(t, run(t, root, "", "install", "--ide", "all"), 0, "install all")
	for _, rel := range []string{".codex/hooks.json", ".gemini/settings.json", ".github/hooks/coyote.json", ".windsurf/hooks.json"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Errorf("falta %s", rel)
		}
	}
	must(t, run(t, root, "", "install", "--ide", "all", "--check"), 0, "check")
	must(t, run(t, root, "", "install", "--ide", "devin"), 2, "devin no tiene instalación propia")

	codex := func(cmd string) string {
		return ideInput(t, map[string]any{"session_id": "thr", "hook_event_name": "PreToolUse", "turn_id": "t1", "cwd": root,
			"tool_name": "Bash", "tool_use_id": "u1", "tool_input": map[string]any{"command": cmd}, "model": "m"})
	}
	must(t, run(t, root, codex("git status"), "gate", "check", "--ide", "codex"), 0, "codex lee")
	r := run(t, root, codex("make deploy"), "gate", "check", "--ide", "codex")
	must(t, r, 2, "codex con efectos")
	if !strings.Contains(ledgerText(t, root), "@ana/codex") {
		t.Error("el ledger debe decir que la propuesta vino de codex")
	}
	cop := ideInput(t, map[string]any{"sessionId": "s", "timestamp": 1, "cwd": root, "toolName": "create",
		"toolArgs": `{"path":"x.txt","file_text":"hola"}`})
	r = run(t, root, cop, "gate", "check", "--ide", "copilot")
	must(t, r, 2, "copilot escribe")
	if !strings.Contains(r.stdout, `"permissionDecision":"deny"`) {
		t.Errorf("Copilot necesita permissionDecision: %q", r.stdout)
	}
	wind := ideInput(t, map[string]any{"agent_action_name": "pre_read_code", "trajectory_id": "tr",
		"tool_info": map[string]any{"file_path": filepath.Join(root, "README.md")}})
	must(t, run(t, root, wind, "gate", "check", "--ide", "windsurf"), 0, "windsurf lee")
	// Un --ide desconocido no apaga el gate: manda el formato de la entrada.
	must(t, run(t, root, codex("make deploy"), "gate", "check", "--ide", "vim"), 2, "ide desconocido")

	pulses := readFile(t, filepath.Join(root, ".coyote", "gate", "ides.json"))
	for _, want := range []string{`"codex"`, `"copilot"`, `"windsurf"`, `"read_code"`} {
		if !strings.Contains(pulses, want) {
			t.Errorf("el latido no anota %s:\n%s", want, pulses)
		}
	}

	// Canario: el IDE llama al gate y respeta la negación → nivel 1.
	c := run(t, root, "", "doctor", "--ide", "codex", "--canary")
	must(t, c, 0, "pedir canario")
	code := canaryRe.FindStringSubmatch(c.stdout)
	if code == nil {
		t.Fatalf("doctor no dio el canario:\n%s", c.stdout)
	}
	r = run(t, root, codex("coyote doctor canary "+code[1]), "gate", "check", "--ide", "codex")
	must(t, r, 2, "el gate niega el canario")
	if !strings.Contains(r.stderr, "canario") {
		t.Errorf("el agente debe saber que es el canario: %s", r.stderr)
	}
	d := run(t, root, "", "doctor", "--ide", "codex")
	if !strings.Contains(d.stdout, "✓ nivel de codex") || !strings.Contains(d.stdout, "nivel 1") {
		t.Errorf("codex debe medir nivel 1:\n%s", d.stdout)
	}
	// Si el canario llega a correr, el IDE no respetó la negación → nivel 2.
	must(t, run(t, root, "", "doctor", "canary", code[1]), 3, "el canario corrió")
	d = run(t, root, "", "doctor", "--ide", "codex")
	must(t, d, 1, "doctor con nivel 2")
	if !strings.Contains(d.stdout, "✗ nivel de codex") || !strings.Contains(d.stdout, "nivel 2") || !strings.Contains(d.stdout, "R17") {
		t.Errorf("codex debe medir nivel 2:\n%s", d.stdout)
	}
	// Gemini: el canario corre y el gate nunca se entera → nivel 3.
	c = run(t, root, "", "doctor", "--ide", "gemini", "--canary")
	code = canaryRe.FindStringSubmatch(c.stdout)
	must(t, run(t, root, "", "doctor", "canary", code[1]), 3, "canario sin gate")
	if d = run(t, root, "", "doctor", "--ide", "gemini"); !strings.Contains(d.stdout, "nivel 3") {
		t.Errorf("gemini debe medir nivel 3:\n%s", d.stdout)
	}
	must(t, run(t, root, "", "doctor", "canary", "NO"), 2, "código inválido")
	must(t, run(t, root, "", "doctor", "--ide", "all", "--canary"), 2, "canario de todos")
	// Devin CLI se revisa con la instalación de Claude Code; Junie se conecta a mano.
	if d = run(t, root, "", "doctor", "--ide", "devin"); !strings.Contains(d.stdout, "instalación devin") || !strings.Contains(d.stdout, "nivel de devin") {
		t.Errorf("doctor --ide devin:\n%s", d.stdout)
	}
	if d = run(t, root, "", "doctor", "--ide", "junie"); !strings.Contains(d.stdout, "hooks de usuario") {
		t.Errorf("doctor --ide junie:\n%s", d.stdout)
	}
	// VS Code con chat.useClaudeHooks correría el gate dos veces.
	if err := os.MkdirAll(filepath.Join(root, ".vscode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".vscode", "settings.json"), []byte("{\n  // hooks de Claude\n  \"chat.useClaudeHooks\": true\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if d = run(t, root, "", "doctor", "--ide", "copilot"); !strings.Contains(d.stdout, "dos veces") {
		t.Errorf("doctor debe avisar del doble hook en VS Code:\n%s", d.stdout)
	}
	// Un agente no escribe los hooks de ningún IDE, ni con aprobación.
	for _, rel := range []string{".codex/hooks.json", ".gemini/settings.json", ".github/hooks/coyote.json", ".windsurf/hooks.json", ".codex/config.toml"} {
		in := hook(t, root, "Write", map[string]any{"file_path": filepath.Join(root, filepath.FromSlash(rel)), "content": "{}"}, nil)
		r := run(t, root, in, "gate", "check")
		must(t, r, 2, "escribir "+rel)
		if !strings.Contains(r.stderr, "bloqueado siempre") {
			t.Errorf("%s debe bloquearse siempre: %s", rel, r.stderr)
		}
	}
}
