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
	IDEGemini     = "gemini"
	IDEWindsurf   = "windsurf" // Windsurf, hoy Devin Desktop (Cascade)
	IDEDevin      = "devin"    // Devin CLI: lee los hooks de Claude Code
	IDEJunie      = "junie"    // Junie CLI: sus hooks se conectan a mano
)

// IDEs son todos los que el gate reconoce, en el orden en que se muestran.
var IDEs = []string{IDEClaudeCode, IDECursor, IDECodex, IDECopilot, IDEGemini, IDEWindsurf, IDEDevin, IDEJunie}

// KnownIDE informa si ide es uno de los que el gate reconoce.
func KnownIDE(ide string) bool {
	for _, x := range IDEs {
		if x == ide {
			return true
		}
	}
	return false
}

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

// Parse lee la entrada JSON de un hook. ide es el IDE para el que se instaló
// el hook; si viene vacío, o si la entrada es claramente de otro IDE que lee
// ese mismo archivo (Devin CLI y VS Code leen el hook de Claude Code), manda
// el formato de la entrada.
func Parse(data []byte, ide string) (Action, error) {
	var m map[string]any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil || m == nil {
		return Action{}, errors.New("la entrada del hook no es un objeto JSON")
	}
	a := Action{}
	a.Event = str(m, "hook_event_name", "hookEventName", "agent_action_name")
	switch detected := detectIDE(m, a.Event); {
	case ide == "":
		a.IDE = detected
	case detected != IDEClaudeCode && detected != ide:
		a.IDE = detected
	default:
		a.IDE = ide
	}
	a.Session = str(m, "session_id", "conversation_id", "sessionId", "trajectory_id")
	a.AgentType = str(m, "agent_type")
	a.AgentID = str(m, "agent_id")
	if roots, ok := m["workspace_roots"].([]any); ok {
		for _, r := range roots {
			if s, ok := r.(string); ok {
				a.Roots = append(a.Roots, s)
			}
		}
	}
	if _, ok := m["agent_action_name"]; ok {
		cascade(&a, m)
	} else {
		a.Tool = str(m, "tool_name", "toolName", "tool")
		a.Input = inputOf(firstOf(m, "tool_input", "toolArgs", "toolInput", "input", "arguments"))
	}
	switch a.Event {
	case "beforeShellExecution": // Cursor, formato anterior a preToolUse
		a.Tool = "Shell"
		a.Input = map[string]any{"command": m["command"]}
	case "beforeMCPExecution":
		a.Tool = "MCP:" + a.Tool
	}
	if a.IDE == IDEGemini {
		a.Tool = geminiMCP(a.Tool, m["mcp_context"])
	}
	if a.Cwd == "" {
		a.Cwd = str(a.Input, "working_directory", "workdir", "cwd", "dir_path")
	}
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

// inputOf normaliza la entrada de una herramienta: un objeto, o un texto con
// JSON adentro (Copilot manda toolArgs como texto).
func inputOf(input any) map[string]any {
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
		return v
	case nil:
		return map[string]any{}
	default:
		return map[string]any{"value": v}
	}
}

// cascade traduce los hooks de Windsurf (Cascade, hoy Devin Desktop): cada
// evento es una herramienta y sus datos vienen en tool_info.
func cascade(a *Action, m map[string]any) {
	info, _ := m["tool_info"].(map[string]any)
	if info == nil {
		info = map[string]any{}
	}
	switch a.Event {
	case "pre_run_command":
		a.Tool = "run_command"
		a.Input = map[string]any{"command": info["command_line"]}
		a.Cwd = str(info, "cwd")
	case "pre_write_code":
		a.Tool, a.Input = "write_code", info
	case "pre_read_code":
		a.Tool, a.Input = "read_code", info
	case "pre_mcp_tool_use":
		a.Tool = "mcp__" + str(info, "mcp_server_name") + "__" + str(info, "mcp_tool_name")
		a.Input, _ = info["mcp_tool_arguments"].(map[string]any)
		if a.Input == nil {
			a.Input = map[string]any{}
		}
	default:
		a.Tool, a.Input = a.Event, info
	}
}

// geminiMCP lleva el nombre de una herramienta MCP de Gemini CLI
// (mcp_<servidor>_<herramienta>) a la forma mcp__servidor__herramienta, si
// la entrada dice cuál es el servidor. Sin eso, el nombre queda como viene y
// la herramienta pide aprobación.
func geminiMCP(tool string, ctx any) string {
	if !strings.HasPrefix(tool, "mcp_") || strings.HasPrefix(tool, "mcp__") {
		return tool
	}
	c, _ := ctx.(map[string]any)
	server := str(c, "server_name", "serverName", "server")
	if server == "" {
		return tool
	}
	if name, ok := strings.CutPrefix(tool, "mcp_"+server+"_"); ok && name != "" {
		return "mcp__" + server + "__" + name
	}
	return tool
}

func detectIDE(m map[string]any, event string) string {
	switch {
	case m["agent_action_name"] != nil:
		return IDEWindsurf
	case event == "preToolUse" || event == "beforeShellExecution" || event == "beforeMCPExecution":
		return IDECursor
	case m["toolName"] != nil || m["toolArgs"] != nil:
		return IDECopilot
	case m["conversation_id"] != nil && m["cursor_version"] != nil:
		return IDECursor
	case event == "BeforeTool":
		return IDEGemini
	case m["turn_id"] != nil:
		return IDECodex
	case m["prompt_id"] != nil:
		return IDEDevin
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
	// Devin CLI y Windsurf (pre_run_command)
	"exec": true, "run_command": true,
}

func isShellTool(tool string) bool { return shellTools[strings.ToLower(tool)] }
