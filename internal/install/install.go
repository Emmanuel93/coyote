// Package install genera la configuración de cada IDE desde una sola fuente
// (ADR-0010): el hook del gate que falla cerrado, la atribución de IA apagada,
// los agentes y las skills. Fusiona la configuración existente: cambia solo
// las entradas de coyote y conserva las de la persona.
package install

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Emmanuel93/coyote/internal/agents"
	"github.com/Emmanuel93/coyote/internal/fsx"
)

// IDEs que coyote install sabe configurar en esta versión.
var IDEs = []string{"claude-code", "cursor"}

// Estados de un cambio.
const (
	Created = "creado"
	Updated = "actualizado"
	Current = "vigente"
	Removed = "borrado"
	Skipped = "omitido"
)

// Change es un archivo que install crea, actualiza, borra o deja igual.
type Change struct {
	Path   string // relativo a la raíz, con barras
	State  string
	Detail string
	data   []byte
	mode   os.FileMode
}

// Pending informa si el cambio escribe o borra algo.
func (c Change) Pending() bool { return c.State == Created || c.State == Updated || c.State == Removed }

// Options describe una instalación.
type Options struct {
	Root     string
	IDE      string
	Set      *agents.Set
	AgentsMD string // AGENTS.md generado; vacío si no se toca
	OwnsMD   bool   // el AGENTS.md actual lo generó coyote (o no existe)
}

// GateCommand es el comando que el hook de Claude Code ejecuta.
const GateCommand = `"$CLAUDE_PROJECT_DIR"/.claude/hooks/coyote-gate.sh`

// CursorCommand es el comando del hook de Cursor, relativo a la raíz.
const CursorCommand = ".cursor/hooks/coyote-gate.sh"

// GateScript es el hook que llama a coyote gate check y, si coyote no está,
// bloquea: un gate que se apaga solo no es gate.
func GateScript(ide string) string {
	deny := `echo "coyote no está instalado o no está en el PATH: el gate humano bloquea esta acción. Instálalo (make install en el repo de coyote) y vuelve a intentar." >&2`
	if ide == "cursor" {
		deny = `echo '{"permission":"deny","user_message":"coyote no está instalado: el gate humano bloquea esta acción","agent_message":"coyote no está instalado: el gate humano bloquea esta acción"}'` + "\n" + deny
	}
	return `#!/bin/sh
# Generado por coyote install: gate humano de coyote (ADR-0009). No lo edites; corre coyote install.
# Sin coyote instalado, el IDE no ejecuta herramientas en este proyecto: el gate falla cerrado.
for c in "$(command -v coyote 2>/dev/null)" "$HOME/go/bin/coyote" /opt/homebrew/bin/coyote /usr/local/bin/coyote; do
  if [ -n "$c" ] && [ -x "$c" ]; then
    exec "$c" gate check --ide ` + ide + `
  fi
done
` + deny + `
exit 2
`
}

// Plan calcula lo que install haría, sin escribir nada.
func Plan(o Options) ([]Change, error) {
	var out []Change
	add := func(c Change, err error) error {
		if err != nil {
			return err
		}
		out = append(out, c)
		return nil
	}
	switch o.IDE {
	case "claude-code":
		if err := add(file(o.Root, ".claude/hooks/coyote-gate.sh", []byte(GateScript("claude-code")), 0o755, "hook del gate")); err != nil {
			return nil, err
		}
		if err := add(jsonFile(o.Root, ".claude/settings.json", mergeClaude, "gate, atribución apagada y COYOTE_IDE")); err != nil {
			return nil, err
		}
		gen := map[string][]byte{}
		for _, a := range o.Set.Agents {
			gen[a.Name+".md"] = []byte(agents.ClaudeAgent(a))
		}
		cs, err := generatedDir(o.Root, ".claude/agents", gen)
		if err != nil {
			return nil, err
		}
		out = append(out, cs...)
		if cs, err = skillsDir(o.Root, ".claude/skills", o.Set); err != nil {
			return nil, err
		}
		out = append(out, cs...)
		if err := add(claudeMD(o.Root)); err != nil {
			return nil, err
		}
	case "cursor":
		if err := add(file(o.Root, ".cursor/hooks/coyote-gate.sh", []byte(GateScript("cursor")), 0o755, "hook del gate")); err != nil {
			return nil, err
		}
		if err := add(jsonFile(o.Root, ".cursor/hooks.json", mergeCursor, "gate en preToolUse, falla cerrado")); err != nil {
			return nil, err
		}
		gen := map[string][]byte{}
		for _, a := range o.Set.Agents {
			gen[a.Name+".md"] = []byte(agents.CursorAgent(a))
		}
		cs, err := generatedDir(o.Root, ".cursor/agents", gen)
		if err != nil {
			return nil, err
		}
		out = append(out, cs...)
		if cs, err = skillsDir(o.Root, ".agents/skills", o.Set); err != nil {
			return nil, err
		}
		out = append(out, cs...)
	default:
		return nil, fmt.Errorf("IDE desconocido %q (usa %s o all)", o.IDE, strings.Join(IDEs, ", "))
	}
	if o.AgentsMD != "" {
		if !o.OwnsMD {
			out = append(out, Change{Path: "AGENTS.md", State: Skipped, Detail: "no lo generó coyote; corre coyote generate agents --force"})
		} else if err := add(file(o.Root, "AGENTS.md", []byte(o.AgentsMD), 0o644, "instrucciones para todos los IDEs")); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// read lee un archivo del proyecto sin seguir symlinks; nil si no existe.
func read(root, rel string) ([]byte, bool, error) {
	if err := fsx.NoSymlinks(root, rel); err != nil {
		return nil, false, err
	}
	p := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Lstat(p)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("%s no es un archivo regular", rel)
	}
	data, err := fsx.ReadFile(root, rel, 8<<20)
	return data, true, err
}

func file(root, rel string, want []byte, mode os.FileMode, detail string) (Change, error) {
	got, exists, err := read(root, rel)
	if err != nil {
		return Change{}, err
	}
	c := Change{Path: rel, Detail: detail, data: want, mode: mode}
	switch {
	case !exists:
		c.State = Created
	case bytes.Equal(got, want):
		c.State = Current
		if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err == nil && mode&0o111 != 0 && info.Mode().Perm()&0o100 == 0 {
			c.State, c.Detail = Updated, detail+" (permiso de ejecución)"
		}
	default:
		c.State = Updated
	}
	return c, nil
}

func jsonFile(root, rel string, merge func([]byte) ([]byte, error), detail string) (Change, error) {
	got, exists, err := read(root, rel)
	if err != nil {
		return Change{}, err
	}
	want, err := merge(got)
	if err != nil {
		return Change{}, fmt.Errorf("%s: %w; corrígelo a mano, coyote no lo pisa", rel, err)
	}
	c := Change{Path: rel, Detail: detail, data: want, mode: 0o644}
	switch {
	case !exists:
		c.State = Created
	default:
		cur, err := parseJSON(got)
		if err == nil && bytes.Equal(encodeJSON(cur), want) {
			c.State = Current
		} else {
			c.State = Updated
		}
	}
	return c, nil
}

// generatedDir escribe los archivos generados de una carpeta y borra los que
// coyote generó antes y ya no existen. Un archivo propio con el mismo nombre
// no se pisa.
func generatedDir(root, dir string, gen map[string][]byte) ([]Change, error) {
	var out []Change
	names := make([]string, 0, len(gen))
	for n := range gen {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		rel := path.Join(dir, n)
		got, exists, err := read(root, rel)
		if err != nil {
			return nil, err
		}
		if exists && !agents.Generated(got) {
			out = append(out, Change{Path: rel, State: Skipped, Detail: "existe y no lo generó coyote"})
			continue
		}
		c, err := file(root, rel, gen[n], 0o644, "")
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || gen[e.Name()] != nil {
			continue
		}
		rel := path.Join(dir, e.Name())
		if got, exists, err := read(root, rel); err == nil && exists && agents.Generated(got) {
			out = append(out, Change{Path: rel, State: Removed, Detail: "ya no existe en la definición"})
		}
	}
	return out, nil
}

func skillsDir(root, dir string, set *agents.Set) ([]Change, error) {
	var out []Change
	want := map[string]bool{}
	for _, s := range set.Skills {
		want[s.Name] = true
		rel := path.Join(dir, s.Name, "SKILL.md")
		got, exists, err := read(root, rel)
		if err != nil {
			return nil, err
		}
		if exists && !agents.Generated(got) {
			out = append(out, Change{Path: rel, State: Skipped, Detail: "existe y no lo generó coyote"})
			continue
		}
		c, err := file(root, rel, []byte(agents.SkillFile(s)), 0o644, "")
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() || want[e.Name()] {
			continue
		}
		rel := path.Join(dir, e.Name(), "SKILL.md")
		if got, exists, err := read(root, rel); err == nil && exists && agents.Generated(got) {
			out = append(out, Change{Path: rel, State: Removed, Detail: "ya no existe en la definición"})
		}
	}
	return out, nil
}

func claudeMD(root string) (Change, error) {
	got, exists, err := read(root, "CLAUDE.md")
	if err != nil {
		return Change{}, err
	}
	c := Change{Path: "CLAUDE.md", Detail: "importa @AGENTS.md", mode: 0o644}
	switch {
	case !exists:
		c.State, c.data = Created, []byte("@AGENTS.md\n")
	case strings.Contains("\n"+string(got)+"\n", "\n@AGENTS.md\n"):
		c.State, c.data = Current, got
	default:
		s := string(got)
		if !strings.HasSuffix(s, "\n") {
			s += "\n"
		}
		c.State, c.data = Updated, []byte(s+"\n@AGENTS.md\n")
	}
	return c, nil
}

// Apply escribe los cambios pendientes de un plan.
func Apply(root string, changes []Change) error {
	for _, c := range changes {
		if !c.Pending() {
			continue
		}
		if err := fsx.NoSymlinks(root, c.Path); err != nil {
			return err
		}
		p := filepath.Join(root, filepath.FromSlash(c.Path))
		if c.State == Removed {
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				return err
			}
			if strings.HasSuffix(c.Path, "/SKILL.md") {
				_ = os.Remove(filepath.Dir(p)) // solo si quedó vacía
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		tmp := p + ".coyote-tmp"
		if err := os.WriteFile(tmp, c.data, c.mode); err != nil {
			return err
		}
		if err := os.Chmod(tmp, c.mode); err != nil {
			return err
		}
		if err := os.Rename(tmp, p); err != nil {
			return err
		}
	}
	return nil
}

// ---- fusiones ----

func isCoyoteHook(v any) bool {
	o, ok := v.(*object)
	if !ok {
		return false
	}
	cmd, _ := o.get("command")
	s, _ := cmd.(string)
	// El hook de v0.1 llamaba a "$c" gate attribution dentro de un for.
	return strings.Contains(s, "coyote-gate") ||
		(strings.Contains(s, "coyote") && (strings.Contains(s, "gate attribution") || strings.Contains(s, "gate check")))
}

func mergeClaude(data []byte) ([]byte, error) {
	o, err := parseJSON(data)
	if err != nil {
		return nil, err
	}
	attr, err := o.child("attribution")
	if err != nil {
		return nil, err
	}
	attr.set("commit", "")
	attr.set("pr", "")
	o.set("includeCoAuthoredBy", false)
	env, err := o.child("env")
	if err != nil {
		return nil, err
	}
	env.set("COYOTE_IDE", "claude-code")
	hooks, err := o.child("hooks")
	if err != nil {
		return nil, err
	}
	ours := &object{members: []member{{"matcher", ""}, {"hooks", []any{&object{members: []member{
		{"type", "command"}, {"command", GateCommand}, {"timeout", json.Number("30")}}}}}}}
	pre, _ := hooks.get("PreToolUse")
	list, ok := pre.([]any)
	if pre != nil && !ok {
		return nil, fmt.Errorf("hooks.PreToolUse no es una lista")
	}
	var kept []any
	at := -1
	for _, e := range list {
		eo, ok := e.(*object)
		if !ok {
			kept = append(kept, e)
			continue
		}
		hs, _ := eo.get("hooks")
		arr, _ := hs.([]any)
		var rest []any
		removed := false
		for _, h := range arr {
			if isCoyoteHook(h) {
				removed = true
				continue
			}
			rest = append(rest, h)
		}
		if removed && at < 0 {
			at = len(kept)
		}
		if removed && len(rest) == 0 {
			continue
		}
		if removed {
			eo.set("hooks", rest)
		}
		kept = append(kept, eo)
	}
	if at < 0 {
		at = len(kept)
	}
	kept = append(kept[:at], append([]any{ours}, kept[at:]...)...)
	hooks.set("PreToolUse", kept)
	return encodeJSON(o), nil
}

func mergeCursor(data []byte) ([]byte, error) {
	o, err := parseJSON(data)
	if err != nil {
		return nil, err
	}
	if _, ok := o.get("version"); !ok {
		o.members = append([]member{{"version", json.Number("1")}}, o.members...)
	}
	hooks, err := o.child("hooks")
	if err != nil {
		return nil, err
	}
	ours := &object{members: []member{{"command", CursorCommand}, {"failClosed", true}, {"timeout", json.Number("30")}}}
	var events []member
	for _, m := range hooks.members {
		arr, ok := m.Value.([]any)
		if !ok {
			events = append(events, m)
			continue
		}
		var rest []any
		at := -1
		for _, h := range arr {
			if isCoyoteHook(h) {
				if at < 0 {
					at = len(rest)
				}
				continue
			}
			rest = append(rest, h)
		}
		if m.Key == "preToolUse" {
			if at < 0 {
				at = len(rest)
			}
			rest = append(rest[:at], append([]any{ours}, rest[at:]...)...)
		}
		if len(rest) == 0 && at >= 0 {
			continue // el evento solo tenía hooks de coyote
		}
		events = append(events, member{m.Key, rest})
	}
	hooks.members = events
	if _, ok := hooks.get("preToolUse"); !ok {
		hooks.set("preToolUse", []any{ours})
	}
	return encodeJSON(o), nil
}
