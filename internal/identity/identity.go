// Package identity resuelve quién actúa. En Coyote siempre es una persona: los
// agentes actúan a nombre de alguien y el ledger lo registra como @persona/agente.
package identity

import (
	"os"
	"os/user"
	"regexp"
	"strings"

	"github.com/Emmanuel93/coyote/internal/gitx"
)

// Person es la identidad humana que firma commits y eventos.
type Person struct {
	Name  string // user.name de git
	Email string // user.email de git
	Slug  string // identificador corto para el ledger
}

var nonSlug = regexp.MustCompile(`[^a-z0-9._-]+`)

// Resolve usa COYOTE_USER, git config coyote.user o el correo de git.
func Resolve(dir string) Person {
	p := Person{Name: gitx.Config(dir, "user.name"), Email: gitx.Config(dir, "user.email")}
	slug := os.Getenv("COYOTE_USER")
	if slug == "" {
		slug = gitx.Config(dir, "coyote.user")
	}
	if slug == "" && p.Email != "" {
		local, domain, _ := strings.Cut(p.Email, "@")
		if strings.HasSuffix(domain, "users.noreply.github.com") && strings.Contains(local, "+") {
			_, local, _ = strings.Cut(local, "+")
		} else {
			local, _, _ = strings.Cut(local, "+")
		}
		slug = local
	}
	if slug == "" {
		if u, err := user.Current(); err == nil {
			slug = u.Username
		}
	}
	p.Slug = Sanitize(slug)
	return p
}

// Sanitize deja un identificador en minúsculas apto para el ledger.
func Sanitize(s string) string {
	s = nonSlug.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-")
	s = strings.Trim(s, "-._")
	if s == "" {
		return "unknown"
	}
	return s
}

// Actor devuelve @slug o @slug/agente.
func (p Person) Actor(agent string) string {
	if strings.TrimSpace(agent) == "" {
		return "@" + p.Slug
	}
	return "@" + p.Slug + "/" + Sanitize(agent)
}

// Configured informa si git tiene nombre y correo para firmar commits.
func (p Person) Configured() bool { return p.Name != "" && p.Email != "" }
