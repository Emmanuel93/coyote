package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadToolSet(t *testing.T) {
	set, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Agents) != 14 || len(set.Skills) != 8 {
		t.Fatalf("se esperaban 14 agentes y 8 skills: %d y %d", len(set.Agents), len(set.Skills))
	}
	for _, name := range []string{"coyote-sr-solution-architect", "coyote-architect", "coyote-dev", "coyote-devsecops", "coyote-sre"} {
		if !set.Has(name) {
			t.Errorf("falta %s", name)
		}
	}
	for _, a := range set.Agents {
		out := ClaudeAgent(a)
		if !strings.HasPrefix(out, "---\nname: "+a.Name+"\n") || !strings.Contains(out, "disallowedTools: Write, Edit") ||
			!strings.Contains(out, Marker) || !strings.Contains(out, "## Reglas de coyote") {
			t.Errorf("formato de Claude Code inesperado para %s:\n%s", a.Name, out[:200])
		}
		if c := CursorAgent(a); !strings.Contains(c, "readonly: true") {
			t.Errorf("Cursor debe recibir agentes de solo lectura: %s", a.Name)
		}
		if a.MaxTurns == 0 || a.Model == "" || len(a.Tools) == 0 {
			t.Errorf("%s debe declarar modelo, turnos y herramientas (A2)", a.Name)
		}
	}
}

func TestProjectOverridesAndValidation(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("coyote/agents/coyote-dev.md", "---\nname: coyote-dev\ndescription: versión del proyecto\nmodel: sonnet\nmax_turns: 5\ntools: [Read]\nskills: [pagos-flujo]\n---\nPrompt propio.\n")
	write("coyote/skills/pagos-flujo/SKILL.md", "---\nname: pagos-flujo\ndescription: cómo se cobra\n---\nPasos.\n")
	set, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range set.Agents {
		if a.Name == "coyote-dev" && (a.Source != "proyecto" || a.MaxTurns != 5) {
			t.Errorf("el agente del proyecto debe ganar: %+v", a)
		}
	}
	bad := map[string]string{
		"tools":    "---\nname: x-agent\ndescription: d\ntools: [Write]\n---\nP\n",
		"campo":    "---\nname: x-agent\ndescription: d\ncolor: red\n---\nP\n",
		"nombre":   "---\nname: X\ndescription: d\n---\nP\n",
		"modelo":   "---\nname: x-agent\ndescription: d\nmodel: gpt\n---\nP\n",
		"vacío":    "---\nname: x-agent\ndescription: d\n---\n",
		"sinfront": "hola",
	}
	for why, content := range bad {
		if _, err := ParseAgent("x-agent.md", []byte(content)); err == nil {
			t.Errorf("definición inválida aceptada (%s)", why)
		}
	}
	write("coyote/agents/roto.md", "---\nname: roto\ndescription: d\nskills: [no-existe]\n---\nP\n")
	if _, err := Load(root); err == nil {
		t.Error("una skill inexistente debe fallar")
	}
}
