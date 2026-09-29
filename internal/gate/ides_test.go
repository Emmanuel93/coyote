package gate

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Las entradas de cada IDE copian las que documenta cada uno (ADR-0015).

func hook(t *testing.T, m map[string]any, ide string) Action {
	t.Helper()
	b, _ := json.Marshal(m)
	a, err := Parse(b, ide)
	if err != nil {
		t.Fatalf("%s: %v", string(b), err)
	}
	return a
}

func TestParseIDEs(t *testing.T) {
	_, root, _ := testPaths(t)
	cases := []struct {
		name    string
		in      map[string]any
		flag    string
		ide     string
		tool    string
		kind    Kind
		command string
		cwd     string
		paths   []string
	}{
		{"codex bash", map[string]any{"session_id": "thr_123", "hook_event_name": "PreToolUse", "turn_id": "turn_456",
			"tool_name": "Bash", "tool_use_id": "use_789", "tool_input": map[string]any{"command": "ls -la"},
			"permission_mode": "default", "cwd": root, "model": "m"}, "codex", IDECodex, "Bash", KindShell, "ls -la", root, nil},
		{"codex apply_patch", map[string]any{"hook_event_name": "PreToolUse", "turn_id": "t", "tool_name": "apply_patch",
			"tool_input": map[string]any{"command": "*** Begin Patch\n*** Update File: internal/x.go\n@@\n-a\n+b\n*** End Patch\n"}, "cwd": root},
			"codex", IDECodex, "apply_patch", KindFile, "", root, []string{"internal/x.go"}},
		{"gemini shell con dir_path", map[string]any{"session_id": "s", "hook_event_name": "BeforeTool", "cwd": root,
			"tool_name": "run_shell_command", "tool_input": map[string]any{"command": "go test ./...", "description": "pruebas", "dir_path": "internal"}},
			"gemini", IDEGemini, "run_shell_command", KindShell, "go test ./...", "internal", nil},
		{"gemini write_file", map[string]any{"hook_event_name": "BeforeTool", "cwd": root, "tool_name": "write_file",
			"tool_input": map[string]any{"file_path": filepath.Join(root, "a.txt"), "content": "hola"}},
			"gemini", IDEGemini, "write_file", KindFile, "", root, []string{filepath.Join(root, "a.txt")}},
		{"gemini read_many_files", map[string]any{"hook_event_name": "BeforeTool", "cwd": root, "tool_name": "read_many_files",
			"tool_input": map[string]any{"paths": []any{"docs/"}}}, "gemini", IDEGemini, "read_many_files", KindRead, "", root, nil},
		{"gemini mcp con servidor", map[string]any{"hook_event_name": "BeforeTool", "cwd": root, "tool_name": "mcp_github_get_issue",
			"mcp_context": map[string]any{"server_name": "github"}, "tool_input": map[string]any{"number": 3}},
			"gemini", IDEGemini, "mcp__github__get_issue", KindRead, "", root, nil},
		{"gemini mcp sin servidor", map[string]any{"hook_event_name": "BeforeTool", "cwd": root, "tool_name": "mcp_github_get_issue",
			"tool_input": map[string]any{}}, "gemini", IDEGemini, "mcp_github_get_issue", KindOther, "", root, nil},
		{"copilot texto", map[string]any{"sessionId": "s", "timestamp": 1773370259963, "cwd": root, "toolName": "bash",
			"toolArgs": `{"command":"printf PROBE","description":"x"}`}, "copilot", IDECopilot, "bash", KindShell, "printf PROBE", root, nil},
		{"copilot objeto", map[string]any{"sessionId": "s", "timestamp": 1, "cwd": root, "toolName": "create",
			"toolArgs": map[string]any{"path": "b.txt", "file_text": "x"}}, "copilot", IDECopilot, "create", KindFile, "", root, []string{"b.txt"}},
		{"copilot view", map[string]any{"sessionId": "s", "timestamp": 1, "cwd": root, "toolName": "str_replace_editor",
			"toolArgs": map[string]any{"command": "view", "path": "README.md"}}, "copilot", IDECopilot, "str_replace_editor", KindRead, "", root, nil},
		{"windsurf comando", map[string]any{"agent_action_name": "pre_run_command", "trajectory_id": "tr", "execution_id": "ex",
			"tool_info": map[string]any{"command_line": "npm install left-pad", "cwd": root}}, "windsurf", IDEWindsurf, "run_command", KindShell, "npm install left-pad", root, nil},
		{"windsurf escritura", map[string]any{"agent_action_name": "pre_write_code", "trajectory_id": "tr",
			"tool_info": map[string]any{"file_path": filepath.Join(root, "f.py"), "edits": []any{map[string]any{"old_string": "a", "new_string": "b"}}}},
			"windsurf", IDEWindsurf, "write_code", KindFile, "", "", []string{filepath.Join(root, "f.py")}},
		{"windsurf lectura", map[string]any{"agent_action_name": "pre_read_code", "tool_info": map[string]any{"file_path": filepath.Join(root, "f.py")}},
			"windsurf", IDEWindsurf, "read_code", KindRead, "", "", nil},
		{"windsurf mcp", map[string]any{"agent_action_name": "pre_mcp_tool_use", "tool_info": map[string]any{"mcp_server_name": "github",
			"mcp_tool_name": "create_issue", "mcp_tool_arguments": map[string]any{"title": "x"}}},
			"windsurf", IDEWindsurf, "mcp__github__create_issue", KindOther, "", "", nil},
		{"devin por el hook de claude", map[string]any{"hook_event_name": "PreToolUse", "tool_name": "exec", "prompt_id": "p1",
			"session_id": "s", "tool_input": map[string]any{"command": "git status"}}, "claude-code", IDEDevin, "exec", KindShell, "git status", "", nil},
		{"junie por la bandera", map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash",
			"tool_input": map[string]any{"command": "ls", "run_in_background": false, "timeout": 30}}, "junie", IDEJunie, "Bash", KindShell, "ls", "", nil},
		{"vs code con el hook de claude", map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash", "session_id": "s",
			"tool_input": map[string]any{"command": "ls"}, "cwd": root}, "claude-code", IDEClaudeCode, "Bash", KindShell, "ls", root, nil},
		{"bandera desconocida manda el formato", map[string]any{"agent_action_name": "pre_read_code",
			"tool_info": map[string]any{"file_path": "x"}}, "", IDEWindsurf, "read_code", KindRead, "", "", nil},
		{"bandera windsurf con entrada de Claude Code", map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash",
			"tool_input": map[string]any{"command": "ls"}}, "windsurf", IDEWindsurf, "Bash", KindShell, "ls", "", nil},
	}
	for _, c := range cases {
		a := hook(t, c.in, c.flag)
		if a.IDE != c.ide || a.Tool != c.tool || Classify(a) != c.kind || a.Command != c.command || a.Cwd != c.cwd {
			t.Errorf("%s: IDE %q herramienta %q clase %s comando %q carpeta %q", c.name, a.IDE, a.Tool, Classify(a), a.Command, a.Cwd)
		}
		if c.paths != nil {
			if got := targetPaths(a); strings.Join(got, ",") != strings.Join(c.paths, ",") {
				t.Errorf("%s: rutas %v, se esperaba %v", c.name, got, c.paths)
			}
		}
	}
	// Windsurf sin comando es un error: el gate bloquea por seguridad.
	b, _ := json.Marshal(map[string]any{"agent_action_name": "pre_run_command", "tool_info": map[string]any{}})
	if _, err := Parse(b, "windsurf"); err == nil {
		t.Error("un pre_run_command sin comando se aceptó")
	}
}

func TestEvaluateIDEs(t *testing.T) {
	ps, root, _ := testPaths(t)
	gem := func(tool string, in map[string]any) Action {
		return hook(t, map[string]any{"hook_event_name": "BeforeTool", "cwd": root, "tool_name": tool, "tool_input": in}, "gemini")
	}
	cop := func(tool string, in map[string]any) Action {
		return hook(t, map[string]any{"sessionId": "s", "timestamp": 1, "cwd": root, "toolName": tool, "toolArgs": in}, "copilot")
	}
	wind := func(ev string, info map[string]any) Action {
		return hook(t, map[string]any{"agent_action_name": ev, "trajectory_id": "t", "tool_info": info}, "windsurf")
	}
	codex := func(tool string, in map[string]any) Action {
		return hook(t, map[string]any{"hook_event_name": "PreToolUse", "turn_id": "t", "cwd": root, "tool_name": tool, "tool_input": in}, "codex")
	}
	cases := []struct {
		name string
		a    Action
		want Verdict
	}{
		{"gemini lee", gem("read_file", map[string]any{"file_path": filepath.Join(root, "README.md")}), Allow},
		{"gemini busca", gem("grep_search", map[string]any{"pattern": "func", "path": "internal"}), Allow},
		{"gemini escribe", gem("write_file", map[string]any{"file_path": filepath.Join(root, "a.go"), "content": "x"}), NeedsApproval},
		{"gemini edita su configuración", gem("replace", map[string]any{"file_path": filepath.Join(root, ".gemini", "settings.json"),
			"old_string": "a", "new_string": "b"}), Block},
		{"gemini lee git status en subcarpeta", gem("run_shell_command", map[string]any{"command": "git status", "dir_path": "internal"}), Allow},
		{"gemini guarda memoria", gem("save_memory", map[string]any{"fact": "x"}), NeedsApproval},
		{"copilot lee", cop("view", map[string]any{"path": "README.md"}), Allow},
		{"copilot crea", cop("create", map[string]any{"path": "x.txt", "file_text": "x"}), NeedsApproval},
		{"copilot toca sus hooks", cop("create", map[string]any{"path": ".github/hooks/otro.json", "file_text": "{}"}), Block},
		{"copilot borra sus hooks por shell", cop("bash", map[string]any{"command": "rm .github/hooks/coyote.json"}), Block},
		{"windsurf lee", wind("pre_read_code", map[string]any{"file_path": filepath.Join(root, "main.go")}), Allow},
		{"windsurf corre", wind("pre_run_command", map[string]any{"command_line": "make build", "cwd": root}), NeedsApproval},
		{"windsurf escribe sus hooks", wind("pre_write_code", map[string]any{"file_path": filepath.Join(root, ".windsurf", "hooks.json"),
			"edits": []any{map[string]any{"old_string": "", "new_string": "{}"}}}), Block},
		{"windsurf mcp", wind("pre_mcp_tool_use", map[string]any{"mcp_server_name": "gh", "mcp_tool_name": "create_issue",
			"mcp_tool_arguments": map[string]any{}}), NeedsApproval},
		{"windsurf mcp de lectura", wind("pre_mcp_tool_use", map[string]any{"mcp_server_name": "gh", "mcp_tool_name": "get_issue",
			"mcp_tool_arguments": map[string]any{}}), Allow},
		{"codex parche", codex("apply_patch", map[string]any{"command": "*** Begin Patch\n*** Add File: nuevo.go\n+package x\n*** End Patch\n"}), NeedsApproval},
		{"codex parche a su configuración", codex("apply_patch", map[string]any{"command": "*** Begin Patch\n*** Update File: .codex/config.toml\n@@\n-a\n+b\n*** End Patch\n"}), Block},
		{"codex apaga hooks por shell", codex("Bash", map[string]any{"command": "echo 'hooks = false' >> .codex/config.toml"}), Block},
		{"devin escribe los hooks de devin", hook(t, map[string]any{"hook_event_name": "PreToolUse", "prompt_id": "p", "tool_name": "exec",
			"tool_input": map[string]any{"command": "echo '{}' > .devin/hooks.v1.json"}, "cwd": root}, "claude-code"), Block},
		{"devin lee los hooks de devin", hook(t, map[string]any{"hook_event_name": "PreToolUse", "prompt_id": "p", "tool_name": "exec",
			"tool_input": map[string]any{"command": "cat .devin/hooks.v1.json"}, "cwd": root}, "claude-code"), Allow},
		{"devin lee su configuración, que puede llevar tokens", hook(t, map[string]any{"hook_event_name": "PreToolUse", "prompt_id": "p", "tool_name": "exec",
			"tool_input": map[string]any{"command": "cat .devin/config.json"}, "cwd": root}, "claude-code"), Block},
		{"vs code: settings sin hooks", claude(t, "Edit", map[string]any{"file_path": filepath.Join(root, ".vscode", "settings.json"),
			"old_string": "a", "new_string": `"editor.tabSize": 2`}, root), NeedsApproval},
		{"vs code: apagar hooks", claude(t, "Edit", map[string]any{"file_path": filepath.Join(root, ".vscode", "settings.json"),
			"old_string": "a", "new_string": `"chat.useHooks": false`}, root), Block},
		{"vs code: aprobar solo", claude(t, "Write", map[string]any{"file_path": filepath.Join(root, ".vscode", "settings.json"),
			"content": `{"chat.tools.global.autoApprove": true}`}, root), Block},
		{"escribir la configuración global de gemini", claude(t, "Write", map[string]any{"file_path": "~/.gemini/settings.json",
			"content": "{}"}, root), Block},
		{"escribir los hooks de usuario de copilot", claude(t, "Write", map[string]any{"file_path": "~/.copilot/hooks/x.json",
			"content": "{}"}, root), Block},
		{"hook de codex en una subcarpeta", claude(t, "Write", map[string]any{"file_path": filepath.Join(root, "services", "pagos", ".codex", "hooks", "coyote-gate.sh"),
			"content": "exit 0"}, root), Block},
		{"configuración de gemini en una subcarpeta", codex("apply_patch", map[string]any{"command": "*** Begin Patch\n*** Add File: app/.gemini/settings.json\n+{}\n*** End Patch\n"}), Block},
		{"hooks de copilot en una subcarpeta", cop("create", map[string]any{"path": "web/.github/hooks/x.json", "file_text": "{}"}), Block},
		{"un .github normal se edita", cop("create", map[string]any{"path": ".github/workflows/ci.yml", "file_text": "x"}), NeedsApproval},
		{"copilot rg con preprocesador", cop("rg", map[string]any{"pattern": "x", "args": "--pre=sh"}), NeedsApproval},
		{"copilot rg normal", cop("rg", map[string]any{"pattern": "func main", "path": "internal"}), Allow},
		{"web fetch con -z en el texto sigue siendo lectura", claude(t, "WebFetch", map[string]any{"url": "https://example.com", "prompt": "qué hace tar -z"}, root), Allow},
	}
	for _, c := range cases {
		if d := ps.Evaluate(c.a); d.Verdict != c.want {
			t.Errorf("%s: %s (%s), se esperaba %s", c.name, d.Verdict, d.Reason, c.want)
		}
	}
}

func TestCanary(t *testing.T) {
	ps, root, _ := testPaths(t)
	blocked := []Action{
		claude(t, "Bash", map[string]any{"command": "coyote doctor canary 7f3a9c2b"}, root),
		claude(t, "Bash", map[string]any{"command": "cd /tmp && coyote doctor canary 7f3a9c2b"}, root),
		claude(t, "Bash", map[string]any{"command": "/usr/local/bin/coyote doctor canary 7f3a9c2b"}, root),
		hook(t, map[string]any{"agent_action_name": "pre_run_command", "tool_info": map[string]any{"command_line": "coyote doctor canary 7f3a9c2b"}}, "windsurf"),
		hook(t, map[string]any{"toolName": "rara", "toolArgs": map[string]any{"script": "coyote doctor canary 7f3a9c2b"}}, "copilot"),
	}
	for _, a := range blocked {
		d := ps.Evaluate(a)
		if d.Verdict != Block || !strings.Contains(d.Reason, "canario") {
			t.Errorf("el canario debe bloquearse siempre: %+v → %s %s", a.Input, d.Verdict, d.Reason)
		}
		if code, ok := CanaryOf(a); !ok || code != "7f3a9c2b" {
			t.Errorf("el canario no se reconoce: %+v", a.Input)
		}
	}
	for _, s := range []string{"coyote doctor", "coyote doctor --ide codex --canary", "coyote doctor canary AB", "micoyote doctor canary 7f3a9c2b"} {
		if _, ok := Canary(s); ok {
			t.Errorf("%q no es el canario", s)
		}
	}
}

func TestHashAcrossIDEs(t *testing.T) {
	ps, root, _ := testPaths(t)
	cmd := "make build"
	actions := []Action{
		claude(t, "Bash", map[string]any{"command": cmd, "description": "compila"}, root),
		hook(t, map[string]any{"hook_event_name": "PreToolUse", "turn_id": "t", "cwd": root, "tool_name": "Bash", "tool_input": map[string]any{"command": cmd}}, "codex"),
		hook(t, map[string]any{"hook_event_name": "BeforeTool", "cwd": root, "tool_name": "run_shell_command", "tool_input": map[string]any{"command": cmd, "description": "x"}}, "gemini"),
		hook(t, map[string]any{"sessionId": "a", "timestamp": 1, "cwd": root, "toolName": "bash", "toolArgs": map[string]any{"command": cmd, "sessionId": "otra", "initial_wait": 30}}, "copilot"),
		hook(t, map[string]any{"agent_action_name": "pre_run_command", "tool_info": map[string]any{"command_line": cmd, "cwd": root}}, "windsurf"),
	}
	want := ps.Evaluate(actions[0]).Hash
	for i, a := range actions[1:] {
		if h := ps.Evaluate(a).Hash; h != want {
			t.Errorf("acción %d: el mismo comando da otro hash en %s", i+1, a.IDE)
		}
	}
	// En segundo plano sí es otra acción.
	bg := hook(t, map[string]any{"hook_event_name": "BeforeTool", "cwd": root, "tool_name": "run_shell_command",
		"tool_input": map[string]any{"command": cmd, "is_background": true}}, "gemini")
	if ps.Evaluate(bg).Hash == want {
		t.Error("un comando en segundo plano no puede usar la aprobación del normal")
	}
}

func TestRespondCopilot(t *testing.T) {
	var out, errb strings.Builder
	if code := Respond(IDECopilot, false, "motivo", &out, &errb); code != 2 ||
		!strings.Contains(out.String(), `"permissionDecision":"deny"`) || !strings.Contains(errb.String(), "motivo") {
		t.Errorf("Copilot niega con permissionDecision y salida 2: %d %q %q", code, out.String(), errb.String())
	}
	out.Reset()
	if code := Respond(IDECopilot, true, "", &out, &errb); code != 0 || out.Len() != 0 {
		t.Errorf("dejar pasar no aprueba en nombre de Copilot: %d %q", code, out.String())
	}
	for _, ide := range []string{IDECodex, IDEGemini, IDEWindsurf, IDEDevin, IDEJunie} {
		out.Reset()
		errb.Reset()
		if code := Respond(ide, false, "motivo", &out, &errb); code != 2 || out.Len() != 0 || !strings.Contains(errb.String(), "motivo") {
			t.Errorf("%s bloquea con salida 2 y el motivo en stderr: %d %q", ide, code, out.String())
		}
	}
}

func TestPulseAndMeasure(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	p := LoadPulses(root)
	read := Action{IDE: IDECodex, Tool: "Bash", Command: "ls", Input: map[string]any{"command": "ls"}}
	p.Record(read, now)
	p.Record(Action{IDE: IDECodex, Tool: "herramienta_rara", Input: map[string]any{}}, now)
	p.Record(Action{IDE: IDECodex, Tool: "mcp__gh__crear", Input: map[string]any{}}, now)
	if err := SavePulses(root, p); err != nil {
		t.Fatal(err)
	}
	p = LoadPulses(root)
	x := p.IDEs[IDECodex]
	if x == nil || x.Calls != 3 || x.Tools["Bash"] != 1 || strings.Join(x.Unknown, ",") != "herramienta_rara" {
		t.Fatalf("latido mal guardado: %+v", x)
	}
	if l := Measure(p, nil, nil, IDECodex, now); l.Level != 0 || !strings.Contains(l.Detail, "--canary") {
		t.Errorf("sin canario no hay nivel: %+v", l)
	}
	if l := Measure(p, nil, nil, IDEGemini, now); l.Level != 0 || !strings.Contains(l.Detail, "no tiene llamadas") {
		t.Errorf("un IDE sin llamadas: %+v", l)
	}
	if err := AddCanary(root, CanaryRequest{Code: "abc123de", IDE: IDECodex, Created: now}); err != nil {
		t.Fatal(err)
	}
	reqs := LoadCanaries(root)
	if l := Measure(p, reqs, nil, IDECodex, now.Add(time.Minute)); l.Level != 0 || !strings.Contains(l.Detail, "pendiente") {
		t.Errorf("canario pendiente: %+v", l)
	}
	// El gate negó el canario y no corrió: nivel 1.
	p.Record(Action{IDE: IDECodex, Tool: "Bash", Command: "coyote doctor canary abc123de", Input: map[string]any{"command": "coyote doctor canary abc123de"}}, now.Add(2*time.Minute))
	if l := Measure(p, reqs, CanariesRan(root), IDECodex, now.Add(3*time.Minute)); l.Level != 1 {
		t.Errorf("negado y sin correr es nivel 1: %+v", l)
	}
	// Corrió de todos modos: nivel 2.
	if err := MarkCanaryRan(root, "abc123de", now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if l := Measure(p, reqs, CanariesRan(root), IDECodex, now.Add(5*time.Minute)); l.Level != 2 {
		t.Errorf("negado pero corrió es nivel 2: %+v", l)
	}
	// Otro IDE: corrió y el gate nunca se enteró: nivel 3.
	if err := AddCanary(root, CanaryRequest{Code: "zzz999yy", IDE: IDEGemini, Created: now}); err != nil {
		t.Fatal(err)
	}
	if err := MarkCanaryRan(root, "zzz999yy", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if l := Measure(p, LoadCanaries(root), CanariesRan(root), IDEGemini, now.Add(2*time.Minute)); l.Level != 3 {
		t.Errorf("corrió sin pasar por el gate es nivel 3: %+v", l)
	}
	// El canario de Copilot llegó por el hook de Claude Code (VS Code con chat.useClaudeHooks).
	if err := AddCanary(root, CanaryRequest{Code: "vsc0de12", IDE: IDECopilot, Created: now}); err != nil {
		t.Fatal(err)
	}
	p.Record(Action{IDE: IDEClaudeCode, Tool: "Bash", Command: "coyote doctor canary vsc0de12", Input: map[string]any{"command": "coyote doctor canary vsc0de12"}}, now)
	if l := Measure(p, LoadCanaries(root), CanariesRan(root), IDECopilot, now); l.Level != 1 || !strings.Contains(l.Detail, "claude-code") {
		t.Errorf("el canario que llega por otro hook se reporta: %+v", l)
	}
	if err := AddCanary(root, CanaryRequest{Code: "NO VALE", IDE: IDECodex}); err == nil {
		t.Error("un código inválido se guardó")
	}
	// Una lista acotada: el latido no crece sin fin.
	for i := 0; i < 100; i++ {
		p.Record(Action{IDE: IDEJunie, Tool: "t" + strings.Repeat("x", i), Input: map[string]any{}}, now)
	}
	if n := len(p.IDEs[IDEJunie].Tools); n > maxTools {
		t.Errorf("herramientas sin tope: %d", n)
	}
	if n := len(p.IDEs[IDEJunie].Unknown); n > maxUnknown {
		t.Errorf("desconocidas sin tope: %d", n)
	}
}
