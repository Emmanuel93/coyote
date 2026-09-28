package ci

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/Emmanuel93/coyote/internal/github"
)

// Review es la última palabra de una persona sobre el PR.
type Review struct {
	User     string // login en minúsculas
	State    string // APPROVED o CHANGES_REQUESTED
	CommitID string // commit que revisó
}

// Reviews lee las revisiones del PR y deja la última decisión de cada
// persona: un comentario después de aprobar no quita la aprobación; una
// revisión descartada sí.
func Reviews(ctx context.Context, c *github.Client, repo string, number int) ([]Review, error) {
	if strings.Count(repo, "/") != 1 || strings.ContainsAny(repo, " ?#") || number <= 0 {
		return nil, fmt.Errorf("repo o PR inválido: %q #%d", repo, number)
	}
	last := map[string]Review{}
	for page := 1; page <= 30; page++ {
		var list []struct {
			User struct {
				Login string `json:"login"`
			} `json:"user"`
			State    string `json:"state"`
			CommitID string `json:"commit_id"`
		}
		path := fmt.Sprintf("/repos/%s/pulls/%d/reviews?per_page=100&page=%d", repo, number, page)
		if _, err := c.Do(ctx, http.MethodGet, path, nil, &list); err != nil {
			return nil, err
		}
		for _, r := range list {
			u := strings.ToLower(r.User.Login)
			switch r.State {
			case "APPROVED", "CHANGES_REQUESTED":
				last[u] = Review{User: u, State: r.State, CommitID: r.CommitID}
			case "DISMISSED":
				delete(last, u)
			}
		}
		if len(list) < 100 {
			break
		}
	}
	out := make([]Review, 0, len(last))
	for _, r := range last {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].User < out[j].User })
	return out, nil
}

// CommitAuthors lista quién escribió o subió los commits del PR (sus logins
// de GitHub): nadie revisa su propio código, aunque el PR sea de otra persona.
func CommitAuthors(ctx context.Context, c *github.Client, repo string, number int) ([]string, error) {
	if strings.Count(repo, "/") != 1 || strings.ContainsAny(repo, " ?#") || number <= 0 {
		return nil, fmt.Errorf("repo o PR inválido: %q #%d", repo, number)
	}
	seen := map[string]bool{}
	for page := 1; page <= 3; page++ { // GitHub lista hasta 250 commits de un PR
		var list []struct {
			Author *struct {
				Login string `json:"login"`
			} `json:"author"`
			Committer *struct {
				Login string `json:"login"`
			} `json:"committer"`
		}
		path := fmt.Sprintf("/repos/%s/pulls/%d/commits?per_page=100&page=%d", repo, number, page)
		if _, err := c.Do(ctx, http.MethodGet, path, nil, &list); err != nil {
			return nil, err
		}
		for _, cm := range list {
			for _, u := range []*struct {
				Login string `json:"login"`
			}{cm.Author, cm.Committer} {
				// web-flow es quien firma los commits hechos desde la web de GitHub.
				if u != nil && u.Login != "" && !strings.EqualFold(u.Login, "web-flow") {
					seen[strings.ToLower(u.Login)] = true
				}
			}
		}
		if len(list) < 100 {
			break
		}
	}
	out := make([]string, 0, len(seen))
	for u := range seen {
		out = append(out, u)
	}
	sort.Strings(out)
	return out, nil
}

// ErrUnverifiable es un equipo cuya membresía el token no puede leer.
var ErrUnverifiable = errors.New("no se puede verificar el equipo con este token")

// TeamMember consulta si una persona es miembro activo de @org/equipo.
func TeamMember(ctx context.Context, c *github.Client, team, user string) (bool, error) {
	org, slug, ok := strings.Cut(strings.TrimPrefix(team, "@"), "/")
	if !ok || org == "" || slug == "" || strings.ContainsAny(org+slug+user, " ?#/") {
		return false, fmt.Errorf("equipo o persona inválidos: %q %q", team, user)
	}
	var m struct {
		State string `json:"state"`
	}
	_, err := c.Do(ctx, http.MethodGet, fmt.Sprintf("/orgs/%s/teams/%s/memberships/%s", org, slug, user), nil, &m)
	var ge *github.Error
	switch {
	case err == nil:
		return m.State == "active", nil
	case errors.As(err, &ge) && ge.Status == http.StatusNotFound:
		// GitHub responde 404 igual si no es miembro o si el token no ve el
		// equipo: si el equipo se ve, la persona no es miembro.
		if _, err := c.Do(ctx, http.MethodGet, fmt.Sprintf("/orgs/%s/teams/%s", org, slug), nil, nil); err == nil {
			return false, nil
		}
		return false, ErrUnverifiable
	case errors.As(err, &ge) && (ge.Status == http.StatusForbidden || ge.Status == http.StatusUnauthorized):
		return false, ErrUnverifiable
	}
	return false, err
}
