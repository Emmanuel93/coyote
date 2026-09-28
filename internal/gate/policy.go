package gate

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Kind clasifica una herramienta.
type Kind int

const (
	KindRead  Kind = iota // solo lee: pasa sin aprobación
	KindShell             // comando de shell
	KindFile              // escribe, edita o borra archivos
	KindOther             // MCP y cualquier otra herramienta: se aprueba
)

func (k Kind) String() string {
	return [...]string{"lectura", "shell", "archivo", "herramienta"}[k]
}

// readTools son herramientas que no cambian nada fuera de la conversación:
// leer, buscar, planear, preguntar o delegar en un subagente (cuyas
// herramientas pasan a su vez por el gate).
var readTools = map[string]bool{
	// Claude Code
	"read": true, "grep": true, "glob": true, "ls": true, "notebookread": true, "webfetch": true,
	"websearch": true, "todowrite": true, "todoread": true, "task": true, "agent": true,
	"exitplanmode": true, "enterplanmode": true, "askuserquestion": true, "bashoutput": true,
	"taskoutput": true, "killshell": true, "killbash": true, "listmcpresourcestool": true,
	"readmcpresourcetool": true, "skill": true, "slashcommand": true, "toolsearch": true,
	// Cursor, Codex, Copilot y Gemini CLI
	"read_file": true, "list_dir": true, "list_directory": true, "codebase_search": true,
	"grep_search": true, "file_search": true, "search_file_content": true, "web_search": true,
	"view": true, "update_plan": true, "semanticsearch": true,
}

var fileTools = map[string]bool{
	"write": true, "edit": true, "multiedit": true, "notebookedit": true, "delete": true,
	"edit_file": true, "write_file": true, "replace": true, "apply_patch": true,
	"search_replace": true, "create_file": true, "delete_file": true, "str_replace_editor": true,
	"str_replace_based_edit_tool": true,
}

// mcpRead reconoce herramientas MCP de lectura por el verbo con que empieza su
// nombre (get_, list_, search_...). Es una heurística: una herramienta mal
// nombrada queda del lado de la lectura, por eso solo se aplica a MCP, que la
// persona configuró.
var mcpRead = regexp.MustCompile(`^(get|list|search|read|fetch|view|show|describe)([_\-]|[A-Z]|$)`)

// Classify dice qué clase de herramienta es.
func Classify(a Action) Kind {
	t := strings.ToLower(a.Tool)
	switch {
	case isShellTool(a.Tool):
		return KindShell
	case fileTools[t]:
		return KindFile
	case readTools[t]:
		return KindRead
	}
	if name, ok := mcpName(a.Tool); ok && mcpRead.MatchString(name) {
		return KindRead
	}
	return KindOther
}

// mcpName devuelve el nombre de la herramienta dentro de su servidor MCP:
// mcp__servidor__herramienta (Claude Code) o MCP:herramienta (Cursor).
func mcpName(tool string) (string, bool) {
	if strings.HasPrefix(tool, "mcp__") {
		parts := strings.Split(tool, "__")
		return parts[len(parts)-1], len(parts) >= 3
	}
	if strings.HasPrefix(tool, "MCP:") {
		return strings.TrimPrefix(tool, "MCP:"), true
	}
	return "", false
}

// Paths resuelve rutas con el proyecto y la carpeta personal de la persona.
type Paths struct {
	Root      string   // raíz del proyecto
	Home      string   // carpeta personal
	Sensitive []string // carpetas y archivos con credenciales (absolutos)
	Protected []string // rutas del proyecto que ningún agente escribe (relativas)
	Global    []string // configuración fuera del proyecto que desarma el gate (absolutas)
}

// NewPaths arma las listas para root. stateDir es donde coyote guarda sus claves.
func NewPaths(root, home, stateDir string) Paths {
	p := Paths{Root: filepath.Clean(root), Home: filepath.Clean(home)}
	for _, rel := range []string{".ssh", ".gnupg", ".aws", ".azure", ".config/gcloud", ".kube", ".docker",
		".netrc", ".git-credentials", ".config/gh", ".config/hub", ".npmrc", ".pypirc", ".gem/credentials",
		"Library/Keychains", ".password-store", ".vault-token", ".config/git/credentials"} {
		p.Sensitive = append(p.Sensitive, filepath.Join(p.Home, rel))
	}
	p.Sensitive = append(p.Sensitive, "/etc/shadow", "/etc/sudoers", "/private/etc/shadow", "/private/etc/sudoers")
	if stateDir != "" {
		p.Sensitive = append(p.Sensitive, filepath.Clean(stateDir))
	}
	p.Protected = []string{".git", ".coyote", "coyote/approvals", "coyote/ledger", ".claude/settings.json",
		".claude/settings.local.json", ".claude/hooks", ".cursor/hooks.json", ".cursor/hooks"}
	for _, rel := range []string{".claude/settings.json", ".claude/settings.local.json", ".claude/managed-settings.json",
		".cursor/hooks.json", ".gitconfig", ".config/git/config", ".bashrc", ".bash_profile", ".profile", ".zshrc",
		".zshenv", ".zprofile"} {
		p.Global = append(p.Global, filepath.Join(p.Home, rel))
	}
	p.Global = append(p.Global, "/etc/claude-code", "/Library/Application Support/ClaudeCode", "/etc/cursor",
		"/etc/gitconfig")
	return p
}

// resolve vuelve absoluta una ruta y sigue los symlinks de la parte que existe,
// así un enlace dentro del proyecto no esconde un destino protegido.
func resolve(p, cwd, home string) string {
	switch {
	case p == "~":
		p = home
	case strings.HasPrefix(p, "~/"):
		p = filepath.Join(home, p[2:])
	case !filepath.IsAbs(p):
		p = filepath.Join(cwd, p)
	}
	p = filepath.Clean(p)
	rest := ""
	cur := p
	for i := 0; i < 64; i++ {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
	return p
}

// within informa si p es base o está dentro de base, sin distinguir
// mayúsculas (el disco de macOS no las distingue).
func within(p, base string) bool {
	p, base = strings.ToLower(filepath.Clean(p)), strings.ToLower(filepath.Clean(base))
	if p == base {
		return true
	}
	if base == string(filepath.Separator) {
		return true
	}
	return strings.HasPrefix(p, base+string(filepath.Separator))
}

// sensitive devuelve la credencial que toca p, si toca alguna.
func (ps Paths) sensitive(abs string) (string, bool) {
	for _, s := range ps.Sensitive {
		if within(abs, s) || within(abs, resolve(s, "/", ps.Home)) {
			return s, true
		}
	}
	return "", false
}

// protected dice si escribir en abs desarmaría el gate.
func (ps Paths) protected(abs string) (string, bool) {
	root := resolve(ps.Root, "/", ps.Home)
	for _, r := range []string{ps.Root, root} {
		for _, rel := range ps.Protected {
			if within(abs, filepath.Join(r, filepath.FromSlash(rel))) {
				return rel, true
			}
		}
	}
	for _, g := range ps.Global {
		if within(abs, g) || within(abs, resolve(g, "/", ps.Home)) {
			return g, true
		}
	}
	if s, ok := ps.sensitive(abs); ok {
		return s, true
	}
	return "", false
}

// Rel devuelve abs relativa a la raíz del proyecto, con barras; si queda
// fuera, la ruta absoluta.
func (ps Paths) Rel(abs string) string {
	for _, r := range []string{ps.Root, resolve(ps.Root, "/", ps.Home)} {
		if rel, err := filepath.Rel(r, abs); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(abs)
}

// pathFields son los campos de entrada que nombran un archivo.
var pathFields = []string{"file_path", "path", "notebook_path", "target_file", "filePath", "filename", "file"}

// targetPaths devuelve los archivos que una herramienta de archivos toca,
// incluidos los de un parche (apply_patch) y los de MultiEdit.
func targetPaths(a Action) []string {
	var out []string
	for _, k := range pathFields {
		if s, ok := a.Input[k].(string); ok && s != "" {
			out = append(out, s)
		}
	}
	for _, k := range []string{"patch", "input", "diff"} {
		if s, ok := a.Input[k].(string); ok {
			for _, m := range patchFileRe.FindAllStringSubmatch(s, -1) {
				out = append(out, strings.TrimSpace(m[1]))
			}
		}
	}
	return out
}

var patchFileRe = regexp.MustCompile(`(?m)^(?:\*\*\* (?:Add|Update|Delete) File:|\*\*\* Move to:|\+\+\+ b/|--- a/)\s*(\S.*)$`)

// allStrings junta los textos de una entrada, para buscar rutas o comandos
// escondidos en herramientas desconocidas.
func allStrings(v any, out *[]string) {
	switch t := v.(type) {
	case string:
		*out = append(*out, t)
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			allStrings(t[k], out)
		}
	case []any:
		for _, x := range t {
			allStrings(x, out)
		}
	}
}

// Hard es un bloqueo que ninguna aprobación levanta.
type Hard struct{ Reason string }

func (h *Hard) Error() string { return h.Reason }

func hard(format string, a ...any) *Hard { return &Hard{fmt.Sprintf(format, a...)} }

// coyoteAdmin reconoce a coyote aprobando, revocando, instalando el gate o
// tocando credenciales cuando está en posición de comando (al inicio o después
// de ;, &&, ||, |, paréntesis o un envoltorio como sudo, env o go run), lo
// corra quien lo corra. Un mensaje de commit que solo lo menciona no cuenta.
var coyoteAdmin = regexp.MustCompile(`(?i)(^|[;&|(\n` + "`" + `]|\$\()\s*([A-Za-z_][A-Za-z0-9_]*=\S*\s+)*` +
	`((sudo|exec|env|nohup|time|command|xargs|nice|go\s+run)(\s+-\S+)*\s+)*` +
	`(\S*/)?coyote(\s+-C\s+\S+)*\s+(approve|reject|revoke|auth|hooks|install)\b`)

// shellProtected son textos que en un comando delatan que toca el gate o
// credenciales, aunque el comando no se pueda analizar.
var shellProtected = []struct {
	re  *regexp.Regexp
	why string
}{
	{regexp.MustCompile(`(?i)\.git/(hooks|config)\b`), "los hooks o la configuración de git"},
	{regexp.MustCompile(`(?i)hookspath`), "core.hooksPath de git"},
	{regexp.MustCompile(`(?i)\.claude/(settings|hooks)`), "la configuración de Claude Code"},
	{regexp.MustCompile(`(?i)managed-settings`), "la configuración administrada del IDE"},
	{regexp.MustCompile(`(?i)disableallhooks`), "el apagado de hooks"},
	{regexp.MustCompile(`(?i)\.cursor/hooks`), "los hooks de Cursor"},
	{regexp.MustCompile(`(?i)coyote/approvals`), "los registros de aprobación"},
	{regexp.MustCompile(`(?i)(^|[\s/'"=])\.coyote($|[\s/'";|&)])`), "el estado local de coyote"},
	{regexp.MustCompile(`(?i)\.ssh/|\.gnupg|\.aws/|\.config/gh\b|\.git-credentials|\.netrc|\.docker/config\.json|\.kube/config|keychains/|\.password-store|\.vault-token`), "credenciales"},
	{regexp.MustCompile(`(?i)\bsecurity\s+(find|dump|export)-|\bsecret-tool\s+lookup|\bgh\s+auth\s+token`), "credenciales del llavero o de gh"},
	{regexp.MustCompile(`(?i)coyote/[a-z0-9_-]+\.key\b`), "las claves locales de coyote"},
	{regexp.MustCompile(`(?i)(^|[\s/'"=~])\.(zshrc|zshenv|zprofile|bashrc|bash_profile|profile|gitconfig)\b`), "la configuración del shell o de git"},
}

// agentFlag extrae el agente que declara un comando de coyote.
var agentFlag = regexp.MustCompile(`--?agent(?:=|\s+)["']?([A-Za-z0-9._/-]+)`)

// hardShell revisa un comando antes de pensar en aprobaciones.
func (ps Paths) hardShell(a Action) *Hard {
	cmd := strings.NewReplacer("\\\r\n", "", "\\\n", "").Replace(a.Command)
	// Si el comando se analiza como de solo lectura, coyote solo aparece en
	// subcomandos de lectura (install --check, por ejemplo).
	if _, err := analyzeShell(cmd); err != nil && coyoteAdmin.MatchString(cmd) {
		return hard("un agente no aprueba, rechaza ni revoca, ni instala el gate o credenciales; eso lo hace la persona en su terminal")
	}
	for _, p := range shellProtected {
		if p.re.MatchString(cmd) {
			return hard("el comando toca %s", p.why)
		}
	}
	if strings.Contains(cmd, ps.Home) {
		for _, s := range ps.Sensitive {
			if strings.Contains(strings.ToLower(cmd), strings.ToLower(s)) {
				return hard("el comando toca credenciales (%s)", s)
			}
		}
	}
	if a.AgentType != "" {
		for _, m := range agentFlag.FindAllStringSubmatch(cmd, -1) {
			if !sameAgent(m[1], a.AgentType) {
				return hard("el comando declara el agente %s, pero el IDE reporta %s", m[1], a.AgentType)
			}
		}
	}
	return nil
}

// sameAgent compara nombres de agente sin mayúsculas ni prefijos de plugin.
func sameAgent(declared, reported string) bool {
	norm := func(s string) string {
		s = strings.ToLower(strings.TrimSpace(s))
		if i := strings.LastIndexAny(s, ":/"); i >= 0 {
			s = s[i+1:]
		}
		return s
	}
	return norm(declared) == norm(reported)
}

// readShell decide si un comando de solo lectura puede correr: no debe leer
// credenciales ni recorrer carpetas que las contienen. cred indica que el
// problema son credenciales, lo que bloquea aun con aprobación.
func (ps Paths) readShell(a Action) (ok bool, why string, cred bool) {
	if strings.EqualFold(a.Tool, "powershell") {
		return false, "PowerShell no se analiza; necesita aprobación", false
	}
	segs, err := analyzeShell(a.Command)
	if err != nil {
		return false, err.Error(), false
	}
	cwd := a.Cwd
	if cwd == "" {
		cwd = ps.Root
	}
	for _, s := range segs {
		recursive := s.prog == "rg" || s.prog == "find" || s.prog == "tree" || s.prog == "du"
		if s.prog == "grep" || s.prog == "egrep" || s.prog == "fgrep" || s.prog == "ls" {
			_, recursive = hasFlag(s.args, "-r", "-R", "--recursive", "--dereference-recursive")
		}
		if recursive {
			// sin rutas, recorre la carpeta actual
			for _, c := range ps.Sensitive {
				if within(c, cwd) {
					return false, "recorre una carpeta que contiene credenciales (" + c + ")", true
				}
			}
		}
		if s.prog == "cd" {
			target := ps.Home
			if len(s.args) > 0 {
				target = s.args[0].s
			}
			abs := resolve(target, cwd, ps.Home)
			if c, ok := ps.sensitive(abs); ok {
				return false, "entra a una carpeta con credenciales (" + c + ")", true
			}
			cwd = abs
			continue
		}
		for _, w := range s.args {
			if why, bad := ps.readArg(w, cwd, recursive); bad {
				return false, why, true
			}
		}
	}
	return true, "", false
}

func (ps Paths) readArg(w word, cwd string, recursive bool) (string, bool) {
	cands := []string{w.s}
	if i := strings.IndexByte(w.s, '='); i >= 0 {
		cands = append(cands, w.s[i+1:])
	}
	if len(w.s) > 2 && w.s[0] == '-' && w.s[1] != '-' {
		cands = append(cands, w.s[2:])
	}
	for _, c := range cands {
		if c == "" || (strings.HasPrefix(c, "-") && c == w.s) {
			continue
		}
		p := c
		if w.glob {
			if i := strings.IndexAny(p, "*?["); i >= 0 {
				p = path.Dir(p[:i] + "x")
			}
		}
		abs := resolve(p, cwd, ps.Home)
		if cred, ok := ps.sensitive(abs); ok {
			return "lee credenciales (" + cred + ")", true
		}
		if recursive || w.glob {
			for _, s := range ps.Sensitive {
				if within(s, abs) {
					return "recorre una carpeta que contiene credenciales (" + s + ")", true
				}
			}
		}
	}
	return "", false
}

// searchTools recorren carpetas: además de no leer credenciales, no pueden
// partir de una carpeta que las contenga.
var searchTools = map[string]bool{"grep": true, "glob": true, "ls": true, "codebase_search": true,
	"grep_search": true, "file_search": true, "search_file_content": true, "list_dir": true,
	"list_directory": true, "semanticsearch": true}

// readTool revisa las rutas de una herramienta de lectura.
func (ps Paths) readTool(a Action) *Hard {
	var texts []string
	allStrings(a.Input, &texts)
	cwd := a.Cwd
	if cwd == "" {
		cwd = ps.Root
	}
	if searchTools[strings.ToLower(a.Tool)] {
		roots := []string{cwd}
		for _, k := range []string{"path", "directory", "dir", "target_directory", "relative_workspace_path"} {
			if s, ok := a.Input[k].(string); ok && s != "" {
				roots = append(roots, resolve(s, cwd, ps.Home))
			}
		}
		for _, r := range roots {
			for _, cred := range ps.Sensitive {
				if within(cred, r) {
					return hard("busca en una carpeta que contiene credenciales (%s)", cred)
				}
			}
		}
	}
	for _, t := range texts {
		if !(strings.HasPrefix(t, "/") || strings.HasPrefix(t, "~") || strings.Contains(t, "/") || strings.HasPrefix(t, ".")) {
			continue
		}
		p := t
		if i := strings.IndexAny(p, "*?[{"); i >= 0 {
			p = path.Dir(p[:i] + "x")
		}
		if cred, ok := ps.sensitive(resolve(p, cwd, ps.Home)); ok {
			return hard("lee credenciales (%s)", cred)
		}
	}
	return nil
}

// hardFile revisa los archivos que una herramienta escribiría.
func (ps Paths) hardFile(a Action) *Hard {
	cwd := a.Cwd
	if cwd == "" {
		cwd = ps.Root
	}
	targets := targetPaths(a)
	if len(targets) == 0 {
		// Formato desconocido: se revisa todo texto que parezca una ruta.
		var texts []string
		allStrings(a.Input, &texts)
		for _, t := range texts {
			if !strings.ContainsAny(t, "\n") && (strings.Contains(t, "/") || strings.HasPrefix(t, ".") || strings.HasPrefix(t, "~")) {
				targets = append(targets, t)
			}
		}
	}
	for _, t := range targets {
		abs := resolve(t, cwd, ps.Home)
		if what, ok := ps.protected(abs); ok {
			return hard("escribir en %s desarmaría el gate o tocaría credenciales", what)
		}
	}
	return nil
}

// commandFields son los campos con los que una herramienta desconocida o MCP
// podría correr un comando.
var commandFields = []string{"command", "cmd", "script", "shell", "args", "argv", "commands"}

// hardOther revisa herramientas desconocidas o MCP: si traen un comando que
// nombra el gate o credenciales, se bloquean.
func (ps Paths) hardOther(a Action) *Hard {
	var texts []string
	for _, k := range commandFields {
		allStrings(a.Input[k], &texts)
	}
	for _, t := range texts {
		if coyoteAdmin.MatchString(t) {
			return hard("un agente no aprueba ni instala el gate")
		}
		for _, p := range shellProtected {
			if p.re.MatchString(t) {
				return hard("la herramienta toca %s", p.why)
			}
		}
	}
	return nil
}

// Home devuelve la carpeta personal, o / si no se conoce.
func Home() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return "/"
}
