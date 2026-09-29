package gate

import (
	"path/filepath"
	"testing"
)

// FuzzShell: el lector de shell del gate recibe lo que escribe un agente. Con
// cualquier comando no se cae y decide lo mismo dos veces (ADR-0023).
func FuzzShell(f *testing.F) {
	for _, s := range []string{"git status", "ls -la | grep x", "echo \"$(cat ~/.ssh/id_rsa)\"", "a && b || c; d &",
		"bash -c 'rm -rf /'", "x=$'\\x41' y", "cat <<EOF\nhola\nEOF", "(cd .. && ls)", "`id`", "a\\\nb", "'", "\"", "$(", "${a:-$(b)}",
		"git -c core.pager=less log", "coyote approve --all", "find . -exec rm {} \\;", "echo \u202e"} {
		f.Add(s)
	}
	root := filepath.Join(f.TempDir(), "p")
	paths := NewPaths(root, filepath.Join(f.TempDir(), "home"), filepath.Join(f.TempDir(), "state"))
	f.Fuzz(func(t *testing.T, cmd string) {
		_, _ = splitShell(cmd)
		_, _ = analyzeShell(cmd)
		act := Action{Tool: "Bash", Command: cmd, Cwd: root}
		a, b := paths.Evaluate(act), paths.Evaluate(act)
		if a.Verdict != b.Verdict || a.Hash != b.Hash {
			t.Fatalf("decisión no determinista para %q: %v / %v", cmd, a, b)
		}
	})
}

// FuzzHookInput: el JSON del hook llega del IDE; nunca tumba al gate.
func FuzzHookInput(f *testing.F) {
	for _, s := range []string{`{"tool_name":"Bash","tool_input":{"command":"ls"}}`, `{"tool_name":"Edit","tool_input":{"file_path":"a","old_string":"x","new_string":"y"}}`,
		`{}`, `[]`, `null`, `{"tool_name":1}`, `{"tool_input":{"command":["a"]}}`} {
		f.Add([]byte(s), "claude-code")
		f.Add([]byte(s), "cursor")
	}
	f.Fuzz(func(t *testing.T, data []byte, ide string) {
		if a, err := Parse(data, ide); err == nil {
			_ = Classify(a)
		}
	})
}
