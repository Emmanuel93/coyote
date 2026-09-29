package gate

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Emmanuel93/coyote/internal/fsx"
	"github.com/Emmanuel93/coyote/internal/infra"
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
	// Infra es el inventario de la infraestructura (ADR-0017); InfraErr, si
	// existe pero no se puede leer.
	Infra    *infra.Inventory
	InfraErr error
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
	p.Protected = []string{".git", ".coyote", "coyote/approvals", "coyote/ledger", "coyote/project.yaml", "coyote/infra.yaml", ".claude/settings.json",
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
// credenciales, aunque el comando no se pueda analizar. cred marca lo que se
// bloquea aun para leer: credenciales y la configuración de un IDE que puede
// llevar tokens (el env de sus servidores MCP); de esta última, cfg, un
// programa que solo ve nombres (ls, stat) puede mirar el nombre. Lo demás es
// la configuración del gate, que un comando de solo lectura puede mirar.
// data marca las claves que desarman el gate: cuentan también dentro de un
// dato (echo '{"disableAllHooks": true}' > x.json).
var shellProtected = []struct {
	re   *regexp.Regexp
	why  string
	cred bool
	cfg  bool
	data bool
}{
	{re: regexp.MustCompile(`(?i)\.git/config\b`), why: "la configuración de git, que puede llevar tokens", cred: true},
	{re: regexp.MustCompile(`(?i)\.git/hooks\b`), why: "los hooks de git"},
	{re: regexp.MustCompile(`(?i)hookspath`), why: "core.hooksPath de git", data: true},
	{re: regexp.MustCompile(`(?i)\.claude/settings`), why: "la configuración de Claude Code", cred: true, cfg: true},
	{re: regexp.MustCompile(`(?i)\.claude/hooks`), why: "los hooks de Claude Code"},
	{re: regexp.MustCompile(`(?i)\.claude/\.credentials`), why: "las credenciales de Claude Code", cred: true},
	{re: regexp.MustCompile(`(?i)managed-settings`), why: "la configuración administrada del IDE", cred: true, cfg: true},
	{re: regexp.MustCompile(`(?i)disableallhooks`), why: "el apagado de hooks", data: true},
	{re: regexp.MustCompile(`(?i)\.cursor/hooks`), why: "los hooks de Cursor"},
	{re: regexp.MustCompile(`(?i)\.codex/config`), why: "la configuración de Codex", cred: true, cfg: true},
	{re: regexp.MustCompile(`(?i)\.codex/(hooks|requirements)`), why: "los hooks de Codex"},
	{re: regexp.MustCompile(`(?i)\.gemini/settings`), why: "la configuración de Gemini CLI", cred: true, cfg: true},
	{re: regexp.MustCompile(`(?i)\.gemini/hooks`), why: "los hooks de Gemini CLI"},
	{re: regexp.MustCompile(`(?i)\.github/hooks`), why: "los hooks de Copilot"},
	{re: regexp.MustCompile(`(?i)\.copilot/config`), why: "la configuración de Copilot", cred: true, cfg: true},
	{re: regexp.MustCompile(`(?i)\.copilot/hooks`), why: "los hooks de Copilot"},
	{re: regexp.MustCompile(`(?i)\.(windsurf|devin)/hooks|\.codeium/(windsurf/)?hooks|\.windsurf/config`), why: "los hooks de Windsurf o de Devin"},
	{re: regexp.MustCompile(`(?i)\.devin/config|\.junie/config`), why: "la configuración de Devin o de Junie", cred: true, cfg: true},
	{re: regexp.MustCompile(`(?i)\.kiro/hooks|\.clinerules/hooks`), why: "los hooks de Kiro o Cline"},
	{re: regexp.MustCompile(`(?i)chat\.(use(claude)?hooks|hookfileslocations)`), why: "los hooks de VS Code", data: true},
	{re: regexp.MustCompile(`(?i)(^|[\s/'"=~])\.claude\.json\b`), why: "la configuración global de Claude Code", cred: true, cfg: true},
	{re: regexp.MustCompile(`(?i)coyote/approvals`), why: "los registros de aprobación"},
	{re: regexp.MustCompile(`(?i)coyote/project\.yaml`), why: "la configuración del proyecto (autonomía)"},
	{re: regexp.MustCompile(`(?i)coyote/infra\.yaml`), why: "el inventario de la infraestructura"},
	{re: regexp.MustCompile(`(?i)(^|[\s/'"=])\.coyote($|[\s/'";|&)])`), why: "el estado local de coyote"},
	{re: regexp.MustCompile(`(?i)\.ssh/|\.gnupg|\.aws/|\.config/gh\b|\.git-credentials|\.netrc|\.docker/config\.json|\.kube/config|keychains/|\.password-store|\.vault-token`), why: "credenciales", cred: true},
	{re: regexp.MustCompile(`(?i)\bsecurity\s+(find|dump|export)-|\bsecret-tool\s+lookup|\bgh\s+auth\s+token`), why: "credenciales del llavero o de gh", cred: true},
	{re: regexp.MustCompile(`(?i)coyote/[a-z0-9_-]+\.key\b`), why: "las claves locales de coyote", cred: true},
	{re: regexp.MustCompile(`(?i)(^|[\s/'"=~])\.(zshrc|zshenv|zprofile|bashrc|bash_profile|profile|gitconfig)\b`), why: "la configuración del shell o de git", cred: true},
}

// protectedIn busca en un comando un texto protegido, segmento por segmento
// y sin sus datos: mencionar una ruta en un mensaje, en lo que imprime echo o
// en el patrón de grep no la toca. Un comando de solo lectura puede mirar la
// configuración del gate, no credenciales.
func (ps Paths) protectedIn(cmd string, readOnly bool) (string, bool) {
	check := func(segs [][]string, data bool) (string, bool) {
		for _, seg := range segs {
			line := segText(seg)
			prog, _ := mainProg(seg)
			for _, p := range shellProtected {
				if (readOnly && (!p.cred || (p.cfg && nameOnly[prog]))) || (data && !p.data) {
					continue
				}
				if p.re.MatchString(line) {
					return p.why, true
				}
			}
			if strings.Contains(line, ps.Home) {
				for _, s := range ps.Sensitive {
					if strings.Contains(strings.ToLower(line), strings.ToLower(s)) {
						return "credenciales (" + s + ")", true
					}
				}
			}
		}
		return "", false
	}
	if why, ok := check(view(cmd, true), false); ok {
		return why, true
	}
	return check(view(cmd, false), true)
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
	_, notRead := analyzeShell(cmd)
	if notRead != nil && isCoyoteAdmin(cmd) {
		return hard("un agente no aprueba, rechaza ni revoca, no instala el gate ni credenciales y no lanza otros agentes (coyote run, coyote ws run o continue); eso lo hace la persona en su terminal")
	}
	if why, ok := ps.protectedIn(cmd, notRead == nil); ok {
		return hard("el comando toca %s", why)
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
	if h := ps.hardInfra(cmd); h != nil {
		return h
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

// hardInfra aplica el gate por ambiente (ADR-0017): un agente nunca aplica
// infraestructura como código, ni corre los comandos de apply del
// inventario, ni cambia un ambiente que se aplica con revisor. El comando se
// revisa como lo correría el shell: terraform "apply" y make -C . apply
// cuentan.
func (ps Paths) hardInfra(cmd string) *Hard {
	texts := cmdTexts(cmd)
	effect := false
	for _, t := range texts {
		if what, ok := infra.ApplyCommand(t); ok {
			return hard("un agente nunca aplica infraestructura (%s): lo corre la persona en su terminal o un pipeline con revisor", what)
		}
		effect = effect || infra.Effect(t)
	}
	if ps.InfraErr != nil && effect {
		return hard("el comando cambia infraestructura y coyote/infra.yaml no se puede leer (%v): corrígelo antes", ps.InfraErr)
	}
	if ps.Infra == nil {
		return nil
	}
	segs := view(cmd, true)
	if dynamic(cmd) {
		// make $(echo apply): el target llega por una sustitución.
		var words []string
		for _, f := range strings.Fields(neutralize(cmd)) {
			words = append(words, strings.Trim(f, "\"'();&|$`"))
		}
		segs = append(segs, words)
	}
	for _, seg := range segs {
		if c, ok := ps.Infra.DeclaredApply(seg); ok {
			return hard("%q aplica infraestructura según coyote/infra.yaml: lo corre la persona o un pipeline con revisor", c)
		}
	}
	if effect {
		joined := strings.Join(texts, " ; ")
		if len(joined) > maxInfraCommand {
			return hard("el comando cambia infraestructura y es demasiado largo para saber a qué ambiente toca (%d bytes)", len(joined))
		}
		if env, ok := ps.Infra.EnvFor(joined); ok && ps.Infra.Environments[env].Apply == infra.Reviewed {
			return hard("el comando cambia el ambiente %s, que solo aplica la persona o un pipeline con revisor (coyote/infra.yaml)", env)
		}
	}
	return nil
}

// maxInfraCommand es el largo máximo de un comando de infraestructura cuyo
// ambiente se busca entre las marcas del inventario.
const maxInfraCommand = 64 << 10

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
		args := make([]string, len(s.args))
		for i, w := range s.args {
			args[i] = w.s
		}
		segCwd := cwd
		recursive := s.prog == "rg" || s.prog == "find" || s.prog == "tree" || s.prog == "du"
		if s.prog == "grep" || s.prog == "egrep" || s.prog == "fgrep" || s.prog == "ls" || s.prog == "diff" {
			_, recursive = hasFlag(s.args, "-r", "-R", "--recursive", "--dereference-recursive")
		}
		// Lee contenidos sin respetar .gitignore: grep -r y diff -r siempre; rg
		// con --hidden, --no-ignore o -u; git grep con --no-index o
		// --no-exclude-standard.
		readsAll := recursive && (s.prog == "grep" || s.prog == "egrep" || s.prog == "fgrep" || s.prog == "diff")
		if s.prog == "rg" {
			if _, ok := hasFlag(s.args, "--hidden", "--no-ignore", "-u", "-uu", "-uuu", "-.", "--no-ignore-vcs", "--no-ignore-dot",
				"--no-ignore-exclude", "--no-ignore-files", "--no-ignore-global", "--no-ignore-parent"); ok {
				readsAll = true
			}
		}
		// La búsqueda: su patrón es dato y sus operandos son lo que lee.
		var spec *searchSpec
		shift := 0
		switch s.prog {
		case "grep", "egrep", "fgrep", "rg":
			sp := parseSearch(s.prog, args)
			spec = &sp
		case "git":
			if sub, off := gitSub(args); sub == "grep" {
				for i := 0; i+1 < off; i++ {
					if args[i] == "-C" {
						segCwd = resolve(args[i+1], segCwd, ps.Home)
					}
				}
				rest := s.args[off+1:]
				_, noIndex := hasFlag(rest, "--no-index")
				_, noStd := hasFlag(rest, "--no-exclude-standard")
				_, std := hasFlag(rest, "--exclude-standard")
				if (noIndex && !std) || noStd {
					recursive, readsAll = true, true
				}
				sp := parseSearch("git-grep", args[off+1:])
				spec, shift = &sp, off+1
			}
		}
		if recursive {
			// sin rutas, recorre la carpeta actual
			for _, c := range ps.Sensitive {
				if within(c, segCwd) {
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
		if s.prog == "echo" || s.prog == "printf" {
			continue // imprimen sus argumentos: no leen archivos
		}
		var targets []string
		for i, w := range s.args {
			if why, bad := ps.readArg(w, segCwd, recursive); bad {
				return false, why, true
			}
			if strings.HasPrefix(w.s, "-") {
				continue
			}
			if spec != nil && i >= shift && (spec.pattern[i-shift] || spec.globWords[i-shift]) {
				continue // el patrón es dato; los patrones de nombres los revisó secretWord
			}
			if !names {
				if why, bad := ps.secretArg(w, segCwd); bad {
					return false, why, true
				}
			}
			if spec == nil {
				targets = append(targets, w.s)
			}
		}
		if spec != nil {
			for _, i := range spec.files {
				targets = append(targets, args[shift+i])
			}
			for _, r := range spec.reads {
				if why, bad := ps.secretArg(word{s: r}, segCwd); bad {
					return false, why, true
				}
			}
		}
		if readsAll {
			if len(targets) == 0 {
				targets = []string{"."}
			}
			for _, t := range targets {
				contentDirs = append(contentDirs, resolve(t, segCwd, ps.Home))
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
	if ps.keyUnderHeader(a, cwd) || ps.patchUnderHeader(a, cwd) {
		return hard("el cambio pega el cuerpo de una llave privada bajo el encabezado que ya está en el archivo; los valores los pone la persona, o, si es un dato de prueba, marca la línea con %s", secrets.AllowMarker)
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
	// En un parche, el encabezado de una llave puede ser una línea de
	// contexto y el cuerpo, una agregada.
	for _, text := range patchTexts(a) {
		var window []string
		for _, l := range patchLines(text) {
			if l.added && secrets.KeyBody(l.text) && !strings.Contains(l.text, secrets.AllowMarker) {
				for _, w := range window {
					if secrets.PEMHeader(w) {
						return secrets.Finding{Kind: "llave privada", Hint: "BEGIN … PRIVATE KEY"}, true
					}
				}
			}
			window = append(window, l.text)
			if len(window) > 4 {
				window = window[1:]
			}
		}
	}
	return secrets.Finding{}, false
}

// patchTexts son los textos de parche de una herramienta.
func patchTexts(a Action) []string {
	var out []string
	for k, v := range a.Input {
		lk := strings.ToLower(k)
		if s, ok := v.(string); ok && (lk == "patch" || lk == "diff" || (strings.EqualFold(a.Tool, "apply_patch") && (lk == "input" || lk == "command"))) {
			out = append(out, s)
		}
	}
	return out
}

// pline es una línea de un parche: agregada o de contexto.
type pline struct {
	added bool
	text  string
}

var hunkHeader = regexp.MustCompile(`^@@ -\d+(?:,(\d+))? \+\d+(?:,(\d+))? @@`)

// patchLines lee las líneas agregadas y de contexto de un parche: el formato
// de Codex (*** Begin Patch, donde toda línea con + es agregada) o un diff
// unificado, contando las líneas de cada hunk para que una línea agregada que
// empieza con "++" no se confunda con un encabezado.
func patchLines(text string) []pline {
	var out []pline
	lines := strings.Split(text, "\n")
	if strings.Contains(text, "*** Begin Patch") {
		for _, l := range lines {
			switch {
			case strings.HasPrefix(l, "*** "):
			case strings.HasPrefix(l, "+"):
				out = append(out, pline{true, l[1:]})
			case strings.HasPrefix(l, " "):
				out = append(out, pline{false, l[1:]})
			}
		}
		return out
	}
	oldLeft, newLeft, hunks := 0, 0, 0
	for _, l := range lines {
		in := oldLeft > 0 || newLeft > 0
		switch {
		case in && strings.HasPrefix(l, "+"):
			out = append(out, pline{true, l[1:]})
			newLeft--
		case in && strings.HasPrefix(l, "-"):
			oldLeft--
		case in && strings.HasPrefix(l, " "):
			out = append(out, pline{false, l[1:]})
			oldLeft--
			newLeft--
		case strings.HasPrefix(l, "@@"):
			hunks++
			oldLeft, newLeft = 1, 1
			if m := hunkHeader.FindStringSubmatch(l); m != nil {
				if m[1] != "" {
					oldLeft, _ = strconv.Atoi(m[1])
				}
				if m[2] != "" {
					newLeft, _ = strconv.Atoi(m[2])
				}
			} else {
				oldLeft, newLeft = 1<<30, 1<<30 // sin conteo: hasta el próximo encabezado
			}
		case strings.HasPrefix(l, "diff ") || strings.HasPrefix(l, "--- ") || strings.HasPrefix(l, "+++ ") && !in:
			oldLeft, newLeft = 0, 0
		}
	}
	if hunks == 0 {
		// Sin hunks, cuenta como agregada toda línea con + que no sea encabezado.
		for _, l := range lines {
			if strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "+++ ") {
				out = append(out, pline{true, l[1:]})
			}
		}
	}
	return out
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
				for _, l := range patchLines(t) {
					if l.added {
						added = append(added, l.text)
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

// keyUnderHeader dice si una edición pega el cuerpo de una llave justo debajo
// del encabezado de llave privada que ya está en el archivo: el encabezado
// no viene en el texto nuevo, pero el resultado es una llave.
func (ps Paths) keyUnderHeader(a Action, cwd string) bool {
	targets := targetPaths(a)
	if len(targets) == 0 {
		return false
	}
	var pairs [][2]string
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			var oldText, newText string
			var hasOld, hasNew bool
			for k, x := range t {
				s, ok := x.(string)
				lk := strings.ToLower(k)
				switch {
				case ok && strings.HasPrefix(lk, "old"):
					oldText, hasOld = s, true
				case ok && strings.HasPrefix(lk, "new"):
					newText, hasNew = s, true
				}
				walk(x)
			}
			if hasOld && hasNew && oldText != "" {
				pairs = append(pairs, [2]string{oldText, newText})
			}
		case []any:
			for _, x := range t {
				walk(x)
			}
		}
	}
	walk(map[string]any(a.Input))
	if len(pairs) == 0 {
		return false
	}
	data, err := fsx.ReadCapped(resolve(targets[0], cwd, ps.Home), 4<<20)
	if err != nil {
		return false
	}
	content := string(data)
	for _, p := range pairs {
		// Todas las apariciones: con replace_all se reemplazan todas.
		for from, n := 0, 0; n < 64; n++ {
			idx := strings.Index(content[from:], p[0])
			if idx < 0 {
				break
			}
			idx += from
			start := 1 + strings.Count(content[:idx], "\n")
			for k, l := range strings.Split(p[1], "\n") {
				if secrets.KeyBody(l) && !strings.Contains(l, secrets.AllowMarker) && secrets.HeaderBefore(content, start+k) {
					return true
				}
			}
			from = idx + max(len(p[0]), 1)
		}
	}
	return false
}

// patchUnderHeader dice si un parche agrega el cuerpo de una llave justo
// debajo del encabezado que ya está en el archivo que actualiza: las líneas
// de contexto o las que quita del hunk están a pocas líneas del encabezado.
func (ps Paths) patchUnderHeader(a Action, cwd string) bool {
	for _, text := range patchTexts(a) {
		for _, sec := range patchSections(text) {
			body := false
			for _, l := range sec.lines {
				if l.kind == '+' && secrets.KeyBody(l.text) && !strings.Contains(l.text, secrets.AllowMarker) {
					body = true
				}
			}
			if !body || sec.path == "" {
				continue
			}
			data, err := fsx.ReadCapped(resolve(sec.path, cwd, ps.Home), 4<<20)
			if err != nil {
				continue
			}
			lines := strings.Split(string(data), "\n")
			old := map[string]bool{}
			for _, l := range sec.lines {
				if l.kind != '+' && strings.TrimSpace(l.text) != "" {
					old[strings.TrimSpace(l.text)] = true
				}
			}
			for h, l := range lines {
				if !secrets.PEMHeader(l) {
					continue
				}
				for j := h + 1; j < len(lines) && j <= h+4+1; j++ {
					if old[strings.TrimSpace(lines[j])] {
						return true
					}
				}
			}
		}
	}
	return false
}

// patchSection es la parte de un parche que toca un archivo.
type patchSection struct {
	path  string
	lines []struct {
		kind byte // '+', '-' o ' '
		text string
	}
}

// patchSections parte un parche por archivo: el formato de Codex (*** Update
// File: ruta) o un diff unificado (+++ b/ruta).
func patchSections(text string) []patchSection {
	var out []patchSection
	codex := strings.Contains(text, "*** Begin Patch")
	cur := -1
	add := func(kind byte, t string) {
		if cur >= 0 {
			out[cur].lines = append(out[cur].lines, struct {
				kind byte
				text string
			}{kind, t})
		}
	}
	for _, l := range strings.Split(text, "\n") {
		switch {
		case codex && (strings.HasPrefix(l, "*** Update File:") || strings.HasPrefix(l, "*** Add File:")):
			out = append(out, patchSection{path: strings.TrimSpace(l[strings.Index(l, ":")+1:])})
			cur = len(out) - 1
		case codex && strings.HasPrefix(l, "*** "):
		case !codex && strings.HasPrefix(l, "+++ "):
			out = append(out, patchSection{path: strings.TrimPrefix(strings.TrimSpace(l[4:]), "b/")})
			cur = len(out) - 1
		case !codex && (strings.HasPrefix(l, "--- ") || strings.HasPrefix(l, "diff ") || strings.HasPrefix(l, "@@")):
		case strings.HasPrefix(l, "+"), strings.HasPrefix(l, "-"), strings.HasPrefix(l, " "):
			add(l[0], l[1:])
		}
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
	// Un secreto literal tampoco sale por una herramienta MCP (un issue, un
	// mensaje, un archivo remoto).
	if f, ok := secretContent(a); ok {
		return hard("la herramienta lleva un secreto literal (%s); si es un dato de prueba, marca la línea con %s", f.Kind, secrets.AllowMarker)
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
		if why, ok := ps.protectedIn(t, false); ok {
			return hard("la herramienta toca %s", why)
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
