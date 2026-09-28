package gate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testPaths(t *testing.T) (Paths, string, string) {
	t.Helper()
	home := t.TempDir()
	root := filepath.Join(home, "Documents", "tienda")
	for _, d := range []string{root, filepath.Join(home, ".ssh"), filepath.Join(home, ".config", "gh")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	state := filepath.Join(home, ".config", "coyote")
	return NewPaths(root, home, state), root, home
}

func claude(t *testing.T, tool string, input map[string]any, cwd string) Action {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"session_id": "s1", "hook_event_name": "PreToolUse",
		"tool_name": tool, "tool_input": input, "cwd": cwd})
	a, err := Parse(b, "")
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestReadOnlyShell(t *testing.T) {
	ps, root, _ := testPaths(t)
	allow := []string{
		"ls -la", "git status", "git log --oneline -5", "git diff HEAD~1 -- internal/", "git show HEAD:README.md",
		"cat README.md | head -20", "grep -rn 'func main' --include='*.go' .", "rg TODO internal", "wc -l *.go",
		"find . -name '*.go' -type f", "git branch -a", "git branch --list 'feat*'", "git tag -l", "git remote -v",
		"git config --get user.email", "go version", "go env GOPATH", "go vet ./...", "coyote status",
		"coyote get context --scope pagos", "coyote ask \"cómo se cobra\"", "coyote standards lint", "coyote attribution check",
		"coyote approvals", "coyote review P-abc123", "coyote install --check", "coyote generate agents --check",
		"git log --format='%h %s' 2>/dev/null", "go vet ./... 2>&1 | tail -5", "echo listo && git status --short",
		"sort go.mod | uniq -c", "jq .version package.json", "diff a.txt b.txt", "cd internal && ls", "tree -L 2",
		"git -C internal log -1", "sleep 1",
	}
	for _, c := range allow {
		a := claude(t, "Bash", map[string]any{"command": c, "description": "x"}, root)
		if d := ps.Evaluate(a); d.Verdict != Allow {
			t.Errorf("%q debería pasar sin aprobación: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
	needs := []string{
		"rm -rf build", "go test ./...", "make check", "npm install", "git commit -m 'x'", "git push origin main",
		"git branch nueva", "git branch -D vieja", "git tag v1.0.0", "git config user.name Otro", "git stash",
		"git -c core.pager=less log", "git diff --output=x.patch", "git log --ext-diff", "find . -delete",
		"find . -name x -exec rm {} \\;", "sort -o out.txt in.txt", "sort -uo out.txt in.txt", "uniq in out",
		"echo hola > archivo", "echo hola >> archivo", "cat x | tee y", "ls; rm x", "ls && rm -rf /",
		"ls $(whoami)", "ls `whoami`", "echo $HOME", "FOO=1 ls", "GIT_EXTERNAL_DIFF=x git diff", "bash -c 'ls'",
		"./ls", "/bin/ls", "ls &", "curl https://example.com", "sed -i s/a/b/ x", "awk '{print}' x",
		"xargs rm < lista", "rg --pre ./x foo", "cat <(ls)", "coyote commit -m 'x'", "coyote ask x --record",
		"coyote standards lint --scripts", "coyote install --ide cursor", "coyote install --check=false --ide cursor",
		"go test -run X ./...", "go run .", "ls\nrm -rf x", "ls # comentario", "git worktree add x",
		"coyote push", "xxd -r a b", "tree -o salida.txt", "date -s 2020-01-01", "file -C -m x",
		"git reflog expire --all", "git remote add x y", "unterminated 'quote", "rg --hostname-bin=./x foo",
		"jq -n 'env'", "jq -n '$ENV.GITHUB_TOKEN'",
	}
	for _, c := range needs {
		a := claude(t, "Bash", map[string]any{"command": c}, root)
		if d := ps.Evaluate(a); d.Verdict == Allow {
			t.Errorf("%q no debería pasar sin aprobación", c)
		}
	}
}

func TestCredentialsAndGateAreBlocked(t *testing.T) {
	ps, root, home := testPaths(t)
	blocked := []struct {
		tool  string
		input map[string]any
	}{
		{"Bash", map[string]any{"command": "cat ~/.ssh/id_rsa"}},
		{"Bash", map[string]any{"command": "cat " + filepath.Join(home, ".config", "gh", "hosts.yml")}},
		{"Bash", map[string]any{"command": "coyote approve P-abc123"}},
		{"Bash", map[string]any{"command": "git add . && coyote approve --all"}},
		{"Bash", map[string]any{"command": "go run ./cmd/coyote approve P-1"}},
		{"Bash", map[string]any{"command": "./bin/coyote -C . revoke P-1 --reason x"}},
		{"Bash", map[string]any{"command": "coyote auth logout"}},
		{"Bash", map[string]any{"command": "rm .claude/settings.json"}},
		{"Bash", map[string]any{"command": "echo '{\"disableAllHooks\": true}' > x.json"}},
		{"Bash", map[string]any{"command": "git config core.hooksPath /tmp/h"}},
		{"Bash", map[string]any{"command": "cp /tmp/hook .git/hooks/commit-msg"}},
		{"Bash", map[string]any{"command": "rm -rf .coyote"}},
		{"Bash", map[string]any{"command": "security find-generic-password -s coyote -w"}},
		{"Bash", map[string]any{"command": "echo 'export PATH=/tmp:$PATH' >> ~/.zshrc"}},
		{"Write", map[string]any{"file_path": filepath.Join(root, ".claude", "settings.json"), "content": "{}"}},
		{"Write", map[string]any{"file_path": filepath.Join(root, ".Claude", "Settings.json"), "content": "{}"}},
		{"Edit", map[string]any{"file_path": filepath.Join(root, ".git", "config"), "old_string": "a", "new_string": "b"}},
		{"Write", map[string]any{"file_path": filepath.Join(root, "coyote", "approvals", "P-x.json"), "content": "{}"}},
		{"Write", map[string]any{"file_path": filepath.Join(root, "coyote", "ledger", "2026", "09", "28-ana.ccf"), "content": ""}},
		{"Write", map[string]any{"file_path": filepath.Join(home, ".claude", "settings.json"), "content": "{}"}},
		{"Write", map[string]any{"file_path": filepath.Join(home, ".ssh", "authorized_keys"), "content": "k"}},
		{"Read", map[string]any{"file_path": filepath.Join(home, ".ssh", "id_rsa")}},
		{"Read", map[string]any{"file_path": filepath.Join(home, ".config", "coyote", "approvals.key")}},
		{"Grep", map[string]any{"pattern": "password", "path": home}},
		{"Bash", map[string]any{"command": "grep -r password ~"}},
		{"Bash", map[string]any{"command": "cd ~ && rg password"}},
		{"Bash", map[string]any{"command": "cat ~/.*/*"}},
		{"Bash", map[string]any{"command": "diff -r ~ /tmp/x"}},
		{"mcp__shell__run", map[string]any{"command": "coyote approve P-9"}},
		{"Delete", map[string]any{"target": filepath.Join(root, ".git", "HEAD")}},
	}
	for _, c := range blocked {
		a := claude(t, c.tool, c.input, root)
		if d := ps.Evaluate(a); d.Verdict != Block {
			t.Errorf("%s %v debería bloquearse siempre: %s (%s)", c.tool, c.input, d.Verdict, d.Reason)
		}
	}
	// Un symlink dentro del proyecto no esconde el destino.
	if err := os.Symlink(filepath.Join(home, ".ssh"), filepath.Join(root, "llaves")); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"cat llaves/id_rsa", "cat " + filepath.Join(root, "llaves", "id_rsa")} {
		if d := ps.Evaluate(claude(t, "Bash", map[string]any{"command": c}, root)); d.Verdict == Allow {
			t.Errorf("%q leyó credenciales a través de un symlink", c)
		}
	}
	// Un commit que solo menciona el comando no es un agente aprobando.
	msg := claude(t, "Bash", map[string]any{"command": `git commit -m "docs: explica coyote approve"`}, root)
	if d := ps.Evaluate(msg); d.Verdict != NeedsApproval {
		t.Errorf("un mensaje que menciona coyote approve se trató como %s: %s", d.Verdict, d.Reason)
	}
}

func TestReadToolsAndClassification(t *testing.T) {
	ps, root, _ := testPaths(t)
	for _, tool := range []string{"Read", "Grep", "Glob", "WebFetch", "Task", "TodoWrite", "mcp__github__get_file_contents",
		"mcp__linear__list_issues", "MCP:search_code"} {
		a := claude(t, tool, map[string]any{"path": root, "pattern": "x"}, root)
		if d := ps.Evaluate(a); d.Verdict != Allow {
			t.Errorf("%s debería pasar: %s %s", tool, d.Verdict, d.Reason)
		}
	}
	for _, in := range []map[string]any{
		{"file_path": filepath.Join(root, "main.go"), "content": "package main\n"},
		{"file_path": "/tmp/fuera.txt", "content": "x"},
		{"destino_raro": "docs/a.md", "texto": "x"},
	} {
		if d := ps.Evaluate(claude(t, "Write", in, root)); d.Verdict != NeedsApproval {
			t.Errorf("Write %v debería necesitar aprobación: %s %s", in, d.Verdict, d.Reason)
		}
	}
	for _, tool := range []string{"mcp__github__create_pull_request", "mcp__db__query", "mcp__slack__post_message", "NuevaHerramienta", "PowerShell"} {
		a := claude(t, tool, map[string]any{"command": "Get-ChildItem", "body": "x"}, root)
		if d := ps.Evaluate(a); d.Verdict != NeedsApproval {
			t.Errorf("%s debería necesitar aprobación: %s %s", tool, d.Verdict, d.Reason)
		}
	}
}

func TestHashIsExact(t *testing.T) {
	ps, root, _ := testPaths(t)
	h := func(tool string, in map[string]any, cwd string) string {
		return ps.Evaluate(claude(t, tool, in, cwd)).Hash
	}
	base := h("Bash", map[string]any{"command": "go test ./...", "description": "pruebas"}, root)
	if base == "" || !strings.HasPrefix(base, "sha256:") {
		t.Fatalf("hash vacío o sin prefijo: %q", base)
	}
	if h("Bash", map[string]any{"command": "go test ./...", "description": "otra descripción", "timeout": 1000}, root) != base {
		t.Error("la descripción o el timeout no deben cambiar el hash")
	}
	if h("Bash", map[string]any{"command": "go test ./... ", "description": "x"}, root) != base {
		t.Error("los espacios al borde no deben cambiar el hash")
	}
	if h("Bash", map[string]any{"command": "go test ./...;"}, root) == base {
		t.Error("un carácter del comando debe cambiar el hash")
	}
	if h("Bash", map[string]any{"command": "go test ./..."}, filepath.Join(root, "internal")) == base {
		t.Error("otra carpeta debe cambiar el hash")
	}
	if h("Bash", map[string]any{"command": "go test ./...", "dangerouslyDisableSandbox": true}, root) == base {
		t.Error("correr fuera del sandbox es otra acción")
	}
	// Cursor y Claude Code aprueban lo mismo con el mismo hash.
	cur, _ := json.Marshal(map[string]any{"hook_event_name": "preToolUse", "tool_name": "Shell",
		"tool_input": map[string]any{"command": "go test ./...", "working_directory": root}, "conversation_id": "c"})
	ca, err := Parse(cur, "")
	if err != nil || ca.IDE != IDECursor {
		t.Fatalf("Cursor mal reconocido: %+v %v", ca, err)
	}
	if ps.Evaluate(ca).Hash != base {
		t.Error("el mismo comando desde Cursor debe tener el mismo hash")
	}
	w1 := h("Write", map[string]any{"file_path": filepath.Join(root, "a.go"), "content": "package a\n"}, root)
	if w1 != h("Write", map[string]any{"file_path": "a.go", "content": "package a\n"}, root) {
		t.Error("ruta absoluta y relativa del mismo archivo deben coincidir")
	}
	if w1 == h("Write", map[string]any{"file_path": filepath.Join(root, "a.go"), "content": "package b\n"}, root) {
		t.Error("otro contenido debe cambiar el hash")
	}
	if w1 == h("Write", map[string]any{"file_path": filepath.Join(root, "a.go"), "content": "package a\n", "mode": "append"}, root) {
		t.Error("un campo desconocido debe entrar en el hash")
	}
	e := func(all bool) string {
		return h("Edit", map[string]any{"file_path": filepath.Join(root, "a.go"), "old_string": "a", "new_string": "b", "replace_all": all}, root)
	}
	if e(false) == e(true) {
		t.Error("replace_all cambia el efecto")
	}
	d := ps.Evaluate(claude(t, "Edit", map[string]any{"file_path": filepath.Join(root, "a.go"), "old_string": "a\nb", "new_string": "c"}, root))
	if d.Object != "Edit a.go (-2 +1 líneas)" || d.Path != "a.go" {
		t.Errorf("descripción inesperada: %q %q", d.Object, d.Path)
	}
}

func TestIdentityMismatchAndParse(t *testing.T) {
	ps, root, _ := testPaths(t)
	b, _ := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash", "agent_type": "coyote-reviewer",
		"agent_id": "a1", "cwd": root, "tool_input": map[string]any{"command": "coyote commit -m 'fix(x): y' --agent coyote-dev"}})
	a, err := Parse(b, "")
	if err != nil {
		t.Fatal(err)
	}
	if d := ps.Evaluate(a); d.Verdict != Block || !strings.Contains(d.Reason, "coyote-reviewer") {
		t.Errorf("un agente no puede declararse otro: %s %s", d.Verdict, d.Reason)
	}
	b, _ = json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash", "agent_type": "coyote-dev",
		"cwd": root, "tool_input": map[string]any{"command": "coyote commit -m 'fix(x): y' --agent coyote-dev"}})
	a, _ = Parse(b, "")
	if d := ps.Evaluate(a); d.Verdict != NeedsApproval {
		t.Errorf("el mismo agente declarado debe pedir aprobación, no bloquearse: %s %s", d.Verdict, d.Reason)
	}
	for _, bad := range []string{"", "no json", "[]", `{"tool_name":"Bash","tool_input":{}}`, `{"hook_event_name":"PreToolUse"}`} {
		if _, err := Parse([]byte(bad), ""); err == nil {
			t.Errorf("entrada inválida aceptada: %q", bad)
		}
	}
	// Codex: comando como lista de argumentos.
	b, _ = json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "shell",
		"tool_input": map[string]any{"command": []any{"bash", "-lc", "git status"}}, "cwd": root})
	a, err = Parse(b, "")
	if err != nil || a.Command != "git status" || ps.Evaluate(a).Verdict != Allow {
		t.Errorf("comando de Codex mal leído: %+v %v", a, err)
	}
	// Copilot: toolArgs como JSON dentro de un texto.
	b, _ = json.Marshal(map[string]any{"toolName": "bash", "toolArgs": `{"command":"rm -rf x"}`, "cwd": root})
	a, err = Parse(b, "")
	if err != nil || a.IDE != IDECopilot || ps.Evaluate(a).Verdict != NeedsApproval {
		t.Errorf("entrada de Copilot mal leída: %+v %v", a, err)
	}
}

func TestRedact(t *testing.T) {
	cases := map[string]string{
		"curl -H 'Authorization: token ghp_abcdefghijklmnopqrstuvwxyz0123' x": "ghp_",
		"export API_KEY=supersecreto123":                                      "supersecreto123",
		"git clone https://ana:clave123@github.com/x/y":                       "clave123",
		"echo AbCdEfGhIjKlMnOpQrStUvWxYz0123456789":                           "AbCdEfGh",
	}
	for in, leaked := range cases {
		if out := Redact(in); strings.Contains(out, leaked) {
			t.Errorf("Redact(%q) = %q; se filtró %q", in, out, leaked)
		}
	}
	if out := Redact("git show 1bf32e6741780bc3d1b7a632abc6f16bce080af2"); !strings.Contains(out, "1bf32e67") {
		t.Errorf("un sha no es un secreto: %q", out)
	}
}

func TestRespond(t *testing.T) {
	var out, errb strings.Builder
	if code := Respond(IDECursor, false, "no", &out, &errb); code != 2 || !strings.Contains(out.String(), `"permission":"deny"`) {
		t.Errorf("Cursor debe recibir deny en JSON y salir con 2: %d %s", code, out.String())
	}
	out.Reset()
	if code := Respond(IDECursor, true, "", &out, &errb); code != 0 || !strings.Contains(out.String(), `"permission":"allow"`) {
		t.Errorf("Cursor necesita allow explícito: %d %s", code, out.String())
	}
	out.Reset()
	errb.Reset()
	if code := Respond(IDEClaudeCode, false, "motivo", &out, &errb); code != 2 || out.Len() != 0 || !strings.Contains(errb.String(), "motivo") {
		t.Errorf("Claude Code bloquea con salida 2 y el motivo en stderr: %d %q %q", code, out.String(), errb.String())
	}
}
