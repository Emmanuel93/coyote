// Package gate decide si un agente puede usar una herramienta (ADR-0009):
// deja pasar lo que solo lee, bloquea siempre lo que desarmaría el gate o
// expondría credenciales y, para todo lo demás, exige una aprobación humana de
// la acción exacta, identificada por su hash.
package gate

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

// IDEs que el gate reconoce por el formato de su hook.
const (
	IDEClaudeCode = "claude-code"
	IDECursor     = "cursor"
	IDECodex      = "codex"
	IDECopilot    = "copilot"
)

// Action es una llamada de herramienta tal como la reporta el hook del IDE.
type Action struct {
	IDE       string
	Event     string
	Tool      string
	Input     map[string]any
	Command   string // comando de shell, si la herramienta es una shell
	argv      []string
	Cwd       string
	Session   string
	AgentType string // agente que reporta el IDE (subagente de Claude Code)
	AgentID   string
	Roots     []string
}

// MaxInput acota la entrada de un hook: un Write de varios megas es posible,
// uno de cientos no.
const MaxInput = 16 << 20

// Parse lee la entrada JSON de un hook. ide puede venir vacío: se deduce del
// formato (Claude Code y Codex usan PreToolUse; Cursor, preToolUse y
// beforeShellExecution; Copilot, toolName y toolArgs).
func Parse(data []byte, ide string) (Action, error) {
	var m map[string]any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil || m == nil {
		return Action{}, errors.New("la entrada del hook no es un objeto JSON")
	}
	a := Action{IDE: ide}
	a.Event = str(m, "hook_event_name", "hookEventName")
	if a.IDE == "" {
		a.IDE = detectIDE(m, a.Event)
	}
	a.Tool = str(m, "tool_name", "toolName", "tool")
	a.Session = str(m, "session_id", "conversation_id", "sessionId")
	a.AgentType = str(m, "agent_type")
	a.AgentID = str(m, "agent_id")
	if roots, ok := m["workspace_roots"].([]any); ok {
		for _, r := range roots {
			if s, ok := r.(string); ok {
				a.Roots = append(a.Roots, s)
			}
		}
	}
	input := firstOf(m, "tool_input", "toolArgs", "toolInput", "input", "arguments")
	if s, ok := input.(string); ok {
		var inner any
		d := json.NewDecoder(strings.NewReader(s))
		d.UseNumber()
		if d.Decode(&inner) == nil {
			input = inner
		}
	}
	switch v := input.(type) {
	case map[string]any:
		a.Input = v
	case nil:
		a.Input = map[string]any{}
	default:
		a.Input = map[string]any{"value": v}
	}
	switch a.Event {
	case "beforeShellExecution": // Cursor, formato anterior a preToolUse
		a.Tool = "Shell"
		a.Input = map[string]any{"command": m["command"]}
	case "beforeMCPExecution":
		a.Tool = "MCP:" + a.Tool
	}
	a.Cwd = str(a.Input, "working_directory", "workdir", "cwd")
	if a.Cwd == "" {
		a.Cwd = str(m, "cwd")
	}
	if isShellTool(a.Tool) {
		a.Command, a.argv = commandOf(a.Input)
		if a.Command == "" {
			return a, errors.New("la herramienta de shell no trae un comando")
		}
	}
	if a.Tool == "" {
		return a, errors.New("la entrada del hook no dice qué herramienta se usa")
	}
	return a, nil
}

func detectIDE(m map[string]any, event string) string {
	switch {
	case event == "preToolUse" || event == "beforeShellExecution" || event == "beforeMCPExecution":
		return IDECursor
	case m["toolName"] != nil || m["toolArgs"] != nil:
		return IDECopilot
	case m["conversation_id"] != nil && m["cursor_version"] != nil:
		return IDECursor
	}
	return IDEClaudeCode
}

func str(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func firstOf(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			return v
		}
	}
	return nil
}

// commandOf devuelve el comando de una herramienta de shell. Si viene como
// lista de argumentos (Codex), la reconstruye con comillas; bash -c "..." se
// trata como el script que corre.
func commandOf(input map[string]any) (string, []string) {
	switch c := firstOf(input, "command", "cmd").(type) {
	case string:
		return c, nil
	case []any:
		argv := make([]string, 0, len(c))
		for _, p := range c {
			s, ok := p.(string)
			if !ok {
				return "", nil
			}
			argv = append(argv, s)
		}
		if len(argv) >= 3 && (argv[0] == "bash" || argv[0] == "sh" || argv[0] == "zsh") &&
			(argv[1] == "-c" || argv[1] == "-lc" || argv[1] == "-lic") && len(argv) == 3 {
			return argv[2], argv
		}
		quoted := make([]string, len(argv))
		for i, s := range argv {
			quoted[i] = shellQuote(s)
		}
		return strings.Join(quoted, " "), argv
	}
	return "", nil
}

func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./=:,@+%") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

var shellTools = map[string]bool{
	"bash": true, "shell": true, "powershell": true, "run_shell_command": true, "exec_command": true,
	"local_shell": true, "terminal": true, "run_terminal_cmd": true, "run_in_terminal": true,
}

func isShellTool(tool string) bool { return shellTools[strings.ToLower(tool)] }
