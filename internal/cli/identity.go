package cli

import (
	"os"
	"sort"
	"strings"

	"github.com/Emmanuel93/coyote/internal/agents"
	"github.com/Emmanuel93/coyote/internal/identity"
)

// ideNames son las identidades de IDE que el ledger acepta como agente.
var ideNames = map[string]bool{"claude-code": true, "cursor": true, "codex": true, "copilot": true,
	"windsurf": true, "devin": true, "gemini": true, "junie": true, "zed": true}

// sessionIDE dice si coyote corre dentro de una sesión de agente y de qué IDE:
// COYOTE_IDE la pone coyote install; CLAUDECODE la pone Claude Code.
func sessionIDE() string {
	if v := strings.TrimSpace(os.Getenv("COYOTE_IDE")); v != "" {
		return identity.Sanitize(v)
	}
	if os.Getenv("CLAUDECODE") != "" {
		return "claude-code"
	}
	if os.Getenv("CURSOR_AGENT") != "" {
		return "cursor"
	}
	return ""
}

// agentFor valida el agente que declara un comando. Sin declaración, dentro
// de una sesión de agente se usa el IDE: lo que hace un agente nunca queda
// en el ledger como si lo hubiera hecho la persona sola.
func (a *app) agentFor(root, declared string) (string, error) {
	declared = strings.TrimSpace(declared)
	if declared == "" {
		return sessionIDE(), nil
	}
	name := identity.Sanitize(declared)
	if ideNames[name] {
		return name, nil
	}
	set, err := agents.Load(root)
	if err != nil {
		return "", err
	}
	if set.Has(name) {
		return name, nil
	}
	ides := make([]string, 0, len(ideNames))
	for k := range ideNames {
		ides = append(ides, k)
	}
	sort.Strings(ides)
	return "", fail(2, "agente desconocido %q: usa uno de %s, un IDE (%s) o decláralo en coyote/agents/",
		declared, strings.Join(set.Names(), ", "), strings.Join(ides, ", "))
}
