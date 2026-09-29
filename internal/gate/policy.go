package gate

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Emmanuel93/coyote/internal/secrets"
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
	// Gemini CLI
	"read_many_files": true, "google_web_search": true, "web_fetch": true, "write_todos": true,
	// Copilot (CLI y agente de GitHub)
	"rg": true, "ask_user": true, "update_todo": true,
	// VS Code
	"semantic_search": true, "fetch_webpage": true, "get_errors": true, "get_terminal_output": true,
	"get_changed_files": true, "list_code_usages": true, "think": true, "manage_todo_list": true,
	// Windsurf (pre_read_code)
	"read_code": true,
}

var fileTools = map[string]bool{
	"write": true, "edit": true, "multiedit": true, "notebookedit": true, "delete": true,
	"edit_file": true, "write_file": true, "replace": true, "apply_patch": true,
	"search_replace": true, "create_file": true, "delete_file": true, "str_replace_editor": true,
	"str_replace_based_edit_tool": true,
	// Copilot
	"create": true,
	// VS Code
	"replace_string_in_file": true, "multi_replace_string_in_file": true, "insert_edit_into_file": true,
	"edit_notebook_file": true, "create_directory": true,
	"write_code": true, // Windsurf (pre_write_code)
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
	case (t == "str_replace_editor" || t == "str_replace_based_edit_tool") && a.Input["command"] == "view":
		// El editor de Copilot y de la API también lee: command view.
		return KindRead
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
	// Secrets son las reglas de archivos de secretos del proyecto (ADR-0016).
	Secrets secrets.Rules
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
	// Lo que desarma el gate de cualquier IDE: sus hooks y la configuración que
	// los apaga o aprueba sola.
	p.Protected = []string{".git", ".coyote", "coyote/approvals", "coyote/ledger", "coyote/project.yaml", ".claude/settings.json",
		".claude/settings.local.json", ".claude/hooks", ".cursor/hooks.json", ".cursor/hooks",
		".codex/hooks.json", ".codex/hooks", ".codex/config.toml", ".gemini/settings.json", ".gemini/hooks",
		".github/hooks", ".windsurf/hooks.json", ".windsurf/hooks", ".devin/hooks.json", ".devin/hooks.v1.json",
		".devin/hooks", ".devin/config.json", ".devin/config.local.json", ".junie/config.json", ".kiro/hooks",
		".clinerules/hooks"}
	for _, rel := range []string{".claude/settings.json", ".claude/settings.local.json", ".claude/managed-settings.json",
		".claude.json", ".cursor/hooks.json", ".codex/hooks.json", ".codex/config.toml", ".codex/requirements.toml",
		".gemini/settings.json", ".copilot/hooks", ".copilot/config.json", ".codeium/windsurf/hooks.json",
		".codeium/hooks.json", ".config/devin/config.json", ".junie/config.json",
		"Library/Application Support/Code/User/settings.json", ".config/Code/User/settings.json",
		".gitconfig", ".config/git/config", ".bashrc", ".bash_profile", ".profile", ".zshrc",
		".zshenv", ".zprofile"} {
		p.Global = append(p.Global, filepath.Join(p.Home, rel))
	}
	p.Global = append(p.Global, "/etc/claude-code", "/Library/Application Support/ClaudeCode", "/etc/cursor",
		"/etc/gitconfig", "/etc/codex", "/etc/gemini-cli", "/etc/devin", "/Library/Application Support/Devin")
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
		// Los hooks de un IDE en una subcarpeta también cuentan: Codex carga
		// las capas .codex/ desde la carpeta donde arranca, y el lanzador busca
		// el hook subiendo desde ahí.
		if rel, err := filepath.Rel(r, abs); err == nil && !strings.HasPrefix(rel, "..") {
			if what, ok := nestedHook(filepath.ToSlash(rel)); ok {
				return what, true
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

// absCwd vuelve absoluta la carpeta de una acción: Gemini CLI la manda
// relativa a la raíz del proyecto (dir_path).
func (ps Paths) absCwd(a Action) Action {
	if a.Cwd != "" && !filepath.IsAbs(a.Cwd) && !strings.HasPrefix(a.Cwd, "~") {
		a.Cwd = filepath.Join(ps.Root, a.Cwd)
	}
	return a
}

// execFlags reconoce, en una herramienta de lectura, argumentos que hacen
// que la búsqueda corra programas: el preprocesador de ripgrep (--pre) o sus
// descompresores (-z). Una herramienta de lectura de otro IDE puede pasar sus
// argumentos a rg tal cual.
func execFlags(a Action) string {
	if !searchTools[strings.ToLower(a.Tool)] {
		return ""
	}
	var texts []string
	allStrings(a.Input, &texts)
	for _, t := range texts {
		for _, f := range strings.Fields(t) {
			switch {
			case f == "--pre" || strings.HasPrefix(f, "--pre=") || f == "--pre-glob" || strings.HasPrefix(f, "--pre-glob="),
				f == "-z" || f == "--search-zip":
				return "la búsqueda trae argumentos que corren programas (" + f + ")"
			}
		}
	}
	return ""
}

// ideDirs son las carpetas de configuración de los IDEs cuyos hooks o
// configuración desarman el gate si un agente los escribe, a cualquier nivel.
var ideDirs = map[string]bool{".claude": true, ".cursor": true, ".codex": true, ".gemini": true, ".windsurf": true,
	".devin": true, ".junie": true, ".kiro": true, ".clinerules": true}

// nestedHook reconoce, en una ruta relativa al proyecto, los hooks o la
// configuración de un IDE dentro de una subcarpeta (sub/.codex/hooks.json).
func nestedHook(rel string) (string, bool) {
	parts := strings.Split(rel, "/")
	for i := 1; i+1 < len(parts); i++ {
		dir, next := strings.ToLower(parts[i]), strings.ToLower(parts[i+1])
		switch {
		case ideDirs[dir] && (strings.HasPrefix(next, "hooks") || strings.HasPrefix(next, "settings") ||
			strings.HasPrefix(next, "config") || strings.HasPrefix(next, "managed-settings") || next == "requirements.toml"):
			return strings.Join(parts[:i+2], "/"), true
		case dir == ".github" && next == "hooks":
			return strings.Join(parts[:i+2], "/"), true
		}
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
var pathFields = []string{"file_path", "path", "notebook_path", "target_file", "filePath", "filename", "file", "dirPath"}

// targetPaths devuelve los archivos que una herramienta de archivos toca,
// incluidos los de un parche (apply_patch) y los de MultiEdit.
func targetPaths(a Action) []string {
	var out []string
	for _, k := range pathFields {
		if s, ok := a.Input[k].(string); ok && s != "" {
			out = append(out, s)
		}
	}
	patchFields := []string{"patch", "input", "diff"}
	if strings.EqualFold(a.Tool, "apply_patch") {
		patchFields = append(patchFields, "command") // Codex manda el parche en command
	}
	for _, k := range patchFields {
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
	{regexp.MustCompile(`(?i)\.codex/(hooks|config|requirements)`), "los hooks o la configuración de Codex"},
	{regexp.MustCompile(`(?i)\.gemini/(settings|hooks)`), "los hooks o la configuración de Gemini CLI"},
	{regexp.MustCompile(`(?i)\.github/hooks`), "los hooks de Copilot"},
	{regexp.MustCompile(`(?i)\.copilot/(hooks|config)`), "los hooks o la configuración de Copilot"},
	{regexp.MustCompile(`(?i)\.(windsurf|devin)/(hooks|config)|\.codeium/(windsurf/)?hooks`), "los hooks de Windsurf o de Devin"},
	{regexp.MustCompile(`(?i)\.junie/config|\.kiro/hooks|\.clinerules/hooks`), "los hooks de Junie, Kiro o Cline"},
	{regexp.MustCompile(`(?i)chat\.(use(claude)?hooks|hookfileslocations)`), "los hooks de VS Code"},
	{regexp.MustCompile(`(?i)(^|[\s/'"=~])\.claude\.json\b`), "la configuración global de Claude Code"},
	{regexp.MustCompile(`(?i)coyote/approvals`), "los registros de aprobación"},
	{regexp.MustCompile(`(?i)coyote/project\.yaml`), "la configuración del proyecto (autonomía)"},
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
	if code, ok := Canary(cmd); ok {
		return hard("%s", canaryReason(code))
	}
	// Si el comando se analiza como de solo lectura, coyote solo aparece en
	// subcomandos de lectura (install --check, por ejemplo).
	if _, err := analyzeShell(cmd); err != nil && isCoyoteAdmin(cmd) {
		return hard("un agente no aprueba, rechaza ni revoca, no instala el gate ni credenciales y no lanza otros agentes (coyote run, coyote ws run o continue); eso lo hace la persona en su terminal")
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
	if what, ok := credCommand(cmd); ok {
		return hard("el comando imprime o crea %s; las credenciales las maneja la persona", what)
	}
	cwd := a.Cwd
	if cwd == "" {
		cwd = ps.Root
	}
	if what, ok := ps.secretWord(cmd, cwd); ok {
		return hard("el comando toca %s: un agente no lee ni escribe archivos de secretos; pide los nombres con coyote secrets list", what)
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
	var contentDirs []string
	for _, s := range segs {
		recursive := s.prog == "rg" || s.prog == "find" || s.prog == "tree" || s.prog == "du"
		if s.prog == "grep" || s.prog == "egrep" || s.prog == "fgrep" || s.prog == "ls" || s.prog == "diff" {
			_, recursive = hasFlag(s.args, "-r", "-R", "--recursive", "--dereference-recursive")
		}
		// Lee contenidos sin respetar .gitignore: grep -r y diff -r siempre; rg
		// solo con --hidden, --no-ignore o -u.
		readsAll := recursive && (s.prog == "grep" || s.prog == "egrep" || s.prog == "fgrep" || s.prog == "diff")
		if s.prog == "rg" {
			if _, ok := hasFlag(s.args, "--hidden", "--no-ignore", "-u", "-uu", "-uuu", "-.", "--no-ignore-vcs"); ok {
				readsAll = true
			}
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
		names := nameOnly[s.prog] // ls, find, stat…: ven nombres, no contenidos
		patternFirst := s.prog == "grep" || s.prog == "egrep" || s.prog == "fgrep" || s.prog == "rg"
		if _, ok := hasFlag(s.args, "-e", "--regexp", "-f", "--file"); ok {
			patternFirst = false // el patrón va en una opción: los posicionales son archivos
		}
		var targets []string
		for _, w := range s.args {
			if why, bad := ps.readArg(w, cwd, recursive); bad {
				return false, why, true
			}
			if strings.HasPrefix(w.s, "-") {
				continue
			}
			if patternFirst {
				patternFirst = false // el patrón de la búsqueda es dato
				continue
			}
			if !names {
				if why, bad := ps.secretArg(w, cwd); bad {
					return false, why, true
				}
			}
			targets = append(targets, w.s)
		}
		if readsAll {
			if len(targets) == 0 {
				targets = []string{"."}
			}
			for _, t := range targets {
				contentDirs = append(contentDirs, resolve(t, cwd, ps.Home))
			}
		}
	}
	for _, d := range contentDirs {
		if what, ok := ps.dirSecret(d); ok {
			return false, "busca en todos los archivos de una carpeta con archivos de secretos (" + what + "); usa rg o git grep, que respetan .gitignore", false
		}
	}
	return true, "", false
}

// secretArg revisa un argumento de un comando de lectura: un archivo de
// secretos, o un comodín que los alcanza.
func (ps Paths) secretArg(w word, cwd string) (string, bool) {
	if w.glob {
		return ps.secretGlobWord(w.s, cwd)
	}
	if kind, rel, ok := ps.secretFile(resolve(w.s, cwd, ps.Home)); ok {
		return "lee un archivo de secretos: " + kind + " (" + rel + "); pide los nombres con coyote secrets list", true
	}
	return "", false
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
	"list_directory": true, "semanticsearch": true, "rg": true, "semantic_search": true, "read_many_files": true}

// patternFields son textos de búsqueda o de instrucciones, no rutas.
var patternFields = map[string]bool{"pattern": true, "query": true, "regex": true, "prompt": true, "description": true,
	"instruction": true, "url": true}

// readTool revisa las rutas de una herramienta de lectura.
func (ps Paths) readTool(a Action) *Hard {
	if s, ok := secretGlob(a); ok {
		return hard("la búsqueda alcanza archivos de secretos (%s); pide los nombres con coyote secrets list", s)
	}
	base := a.Cwd
	if base == "" {
		base = ps.Root
	}
	for k, v := range a.Input {
		if patternFields[k] {
			continue
		}
		var paths []string
		allStrings(v, &paths)
		for _, t := range paths {
			if t == "" || strings.ContainsAny(t, "\n*?[") {
				continue
			}
			if kind, rel, ok := ps.secretFile(resolve(t, base, ps.Home)); ok && looksLikePath(t) {
				return hard("lee un archivo de secretos: %s (%s); pide los nombres con coyote secrets list", kind, rel)
			}
		}
	}
	var texts []string
	allStrings(a.Input, &texts)
	cwd := a.Cwd
	if cwd == "" {
		cwd = ps.Root
	}
	if searchTools[strings.ToLower(a.Tool)] {
		roots := []string{cwd}
		for _, k := range []string{"path", "directory", "dir", "target_directory", "relative_workspace_path", "dir_path", "dirPath"} {
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
		abs := resolve(p, cwd, ps.Home)
		if cred, ok := ps.sensitive(abs); ok {
			return hard("lee credenciales (%s)", cred)
		}
		// La configuración de git del proyecto puede llevar un token en la URL
		// de un remoto: leerla se bloquea igual que cat .git/config. Buscar en
		// el proyecto sigue libre (los buscadores no entran a .git).
		if strings.EqualFold(abs, filepath.Join(ps.Root, ".git", "config")) || strings.EqualFold(abs, filepath.Join(resolve(ps.Root, "/", ps.Home), ".git", "config")) {
			return hard("lee la configuración de git del proyecto, que puede llevar credenciales")
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
	if f, ok := secretContent(a); ok {
		line := ""
		if f.Line > 0 {
			line = fmt.Sprintf(", línea %d", f.Line)
		}
		return hard("el cambio escribe un secreto literal (%s%s); usa una variable de entorno o el gestor de secretos, o, si es un dato de prueba, marca la línea con %s", f.Kind, line, secrets.AllowMarker)
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
		if kind, rel, ok := ps.secretFile(abs); ok {
			return hard("escribe un archivo de secretos: %s (%s); los valores los pone la persona", kind, rel)
		}
		// La configuración de VS Code se edita, pero no para apagar sus hooks
		// ni para aprobar herramientas solas.
		if filepath.Base(abs) == "settings.json" && filepath.Base(filepath.Dir(abs)) == ".vscode" {
			var texts []string
			allStrings(a.Input, &texts)
			for _, x := range texts {
				if vscodeGateKeys.MatchString(x) {
					return hard("el cambio a .vscode/settings.json toca los hooks o la aprobación automática de VS Code")
				}
			}
		}
	}
	return nil
}

// secretContent busca secretos literales en lo que una herramienta escribiría:
// el contenido nuevo, nunca el que reemplaza.
func secretContent(a Action) (secrets.Finding, bool) {
	for _, text := range newContent(a) {
		if f := secrets.Scan("", text); len(f) > 0 {
			return f[0], true
		}
	}
	return secrets.Finding{}, false
}

// newContent junta los textos nuevos de una escritura: sin los campos old*
// (lo que se reemplaza) y, de un parche, solo las líneas que agrega.
func newContent(a Action) []string {
	var out []string
	patch := strings.EqualFold(a.Tool, "apply_patch")
	var walk func(k string, v any)
	walk = func(k string, v any) {
		lk := strings.ToLower(k)
		if strings.HasPrefix(lk, "old") {
			return
		}
		switch t := v.(type) {
		case string:
			if lk == "patch" || lk == "diff" || (patch && (lk == "input" || lk == "command")) {
				var added []string
				for _, l := range strings.Split(t, "\n") {
					if strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "+++") {
						added = append(added, l[1:])
					}
				}
				out = append(out, strings.Join(added, "\n"))
				return
			}
			out = append(out, t)
		case map[string]any:
			for kk, vv := range t {
				walk(kk, vv)
			}
		case []any:
			for _, x := range t {
				walk(k, x)
			}
		}
	}
	for k, v := range a.Input {
		walk(k, v)
	}
	return out
}

// looksLikePath distingue una ruta de un texto cualquiera.
func looksLikePath(s string) bool {
	return !strings.ContainsAny(s, " \t\n") || strings.Contains(s, "/")
}

// vscodeGateKeys son las opciones de VS Code que apagan los hooks o aprueban
// herramientas sin preguntar.
var vscodeGateKeys = regexp.MustCompile(`(?i)chat\.(use(claude)?hooks|hookfileslocations|tools\.(global\.)?autoapprove|tools\.[a-z.]*autoapprove)`)

// commandFields son los campos con los que una herramienta desconocida o MCP
// podría correr un comando.
var commandFields = []string{"command", "cmd", "script", "shell", "args", "argv", "commands"}

// hardOther revisa herramientas desconocidas o MCP: si traen un comando que
// nombra el gate o credenciales, se bloquean.
func (ps Paths) hardOther(a Action) *Hard {
	var all []string
	allStrings(a.Input, &all)
	for _, t := range all {
		if code, ok := Canary(t); ok {
			return hard("%s", canaryReason(code))
		}
	}
	var texts []string
	for _, k := range commandFields {
		allStrings(a.Input[k], &texts)
	}
	for _, t := range texts {
		if isCoyoteAdmin(t) {
			return hard("un agente no aprueba ni instala el gate")
		}
		if what, ok := credCommand(t); ok {
			return hard("la herramienta imprime o crea %s", what)
		}
		if what, ok := ps.secretWord(t, ps.Root); ok {
			return hard("la herramienta toca %s", what)
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
