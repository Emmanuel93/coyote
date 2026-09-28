// Package ci corre coyote dentro de un pipeline (ADR-0013): lee el evento del
// PR de GitHub Actions, escribe el resumen del job y deja un solo comentario
// en el PR, que se actualiza en cada push en lugar de repetirse.
package ci

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"

	"github.com/Emmanuel93/coyote/internal/github"
)

// PR es lo que coyote necesita del evento pull_request.
type PR struct {
	Number   int
	BaseSHA  string
	HeadSHA  string
	BaseRef  string
	HeadRef  string
	Repo     string // owner/nombre del repo del PR
	HeadRepo string // owner/nombre del repo de la rama; distinto si viene de un fork
	Draft    bool
}

// FromFork informa si el PR viene de otro repo.
func (p PR) FromFork() bool { return p.HeadRepo != "" && !strings.EqualFold(p.HeadRepo, p.Repo) }

var shaRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

// ReadPR lee el evento de GitHub Actions (GITHUB_EVENT_PATH).
func ReadPR(path string) (PR, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return PR{}, fmt.Errorf("no puedo leer el evento de GitHub: %w", err)
	}
	var ev struct {
		Number      int `json:"number"`
		PullRequest *struct {
			Number int  `json:"number"`
			Draft  bool `json:"draft"`
			Base   struct {
				SHA  string `json:"sha"`
				Ref  string `json:"ref"`
				Repo struct {
					FullName string `json:"full_name"`
				} `json:"repo"`
			} `json:"base"`
			Head struct {
				SHA  string `json:"sha"`
				Ref  string `json:"ref"`
				Repo *struct {
					FullName string `json:"full_name"`
				} `json:"repo"`
			} `json:"head"`
		} `json:"pull_request"`
	}
	if err := json.Unmarshal(data, &ev); err != nil {
		return PR{}, fmt.Errorf("evento de GitHub inválido: %w", err)
	}
	if ev.PullRequest == nil {
		return PR{}, fmt.Errorf("el evento no es de un pull request")
	}
	pr := ev.PullRequest
	out := PR{Number: pr.Number, BaseSHA: pr.Base.SHA, HeadSHA: pr.Head.SHA, BaseRef: pr.Base.Ref, HeadRef: pr.Head.Ref,
		Repo: pr.Base.Repo.FullName, Draft: pr.Draft}
	if pr.Head.Repo != nil {
		out.HeadRepo = pr.Head.Repo.FullName
	}
	if out.Number == 0 {
		out.Number = ev.Number
	}
	if !shaRe.MatchString(out.BaseSHA) || !shaRe.MatchString(out.HeadSHA) {
		return PR{}, fmt.Errorf("el evento no trae los commits base y head del PR")
	}
	return out, nil
}

// Marker identifica el comentario de coyote en un PR.
const Marker = "<!-- coyote:impacto -->"

// maxComment deja margen bajo el límite de 65 536 caracteres de GitHub.
const maxComment = 60000

// Clip recorta un comentario largo en un borde de línea y apunta al resumen del job.
func Clip(body, runURL string) string {
	if len(body) <= maxComment {
		return body
	}
	cut := strings.LastIndex(body[:maxComment], "\n")
	if cut < 0 {
		cut = maxComment
	}
	more := "\n\n_El reporte completo no cabe en un comentario"
	if runURL != "" {
		more += ": está en el [resumen del job](" + runURL + ")"
	}
	return body[:cut] + more + "._\n"
}

type comment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
	URL  string `json:"html_url"`
	User struct {
		Type string `json:"type"`
	} `json:"user"`
}

// UpsertComment crea el comentario de coyote en el PR o actualiza el que ya
// dejó (se reconoce por el marcador). Devuelve su URL y si fue nuevo.
func UpsertComment(ctx context.Context, c *github.Client, repo string, number int, body string) (string, bool, error) {
	if strings.Count(repo, "/") != 1 || strings.ContainsAny(repo, " ?#") || number <= 0 {
		return "", false, fmt.Errorf("repo o PR inválido: %q #%d", repo, number)
	}
	if !strings.Contains(body, Marker) {
		body = Marker + "\n" + body
	}
	for page := 1; page <= 20; page++ {
		var list []comment
		path := fmt.Sprintf("/repos/%s/issues/%d/comments?per_page=100&page=%d", repo, number, page)
		if _, err := c.Do(ctx, http.MethodGet, path, nil, &list); err != nil {
			return "", false, err
		}
		for _, cm := range list {
			if strings.Contains(cm.Body, Marker) && cm.User.Type == "Bot" {
				var out comment
				if _, err := c.Do(ctx, http.MethodPatch, fmt.Sprintf("/repos/%s/issues/comments/%d", repo, cm.ID), map[string]string{"body": body}, &out); err != nil {
					return "", false, err
				}
				return out.URL, false, nil
			}
		}
		if len(list) < 100 {
			break
		}
	}
	var out comment
	if _, err := c.Do(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/comments", repo, number), map[string]string{"body": body}, &out); err != nil {
		return "", false, err
	}
	return out.URL, true, nil
}

// AppendSummary agrega texto al resumen del job (GITHUB_STEP_SUMMARY).
func AppendSummary(path, text string) error {
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(text + "\n")
	return err
}

// RunURL es el enlace a la corrida del job actual, si se conoce.
func RunURL() string {
	server, repo, id := os.Getenv("GITHUB_SERVER_URL"), os.Getenv("GITHUB_REPOSITORY"), os.Getenv("GITHUB_RUN_ID")
	if server == "" || repo == "" || id == "" {
		return ""
	}
	return fmt.Sprintf("%s/%s/actions/runs/%s", strings.TrimRight(server, "/"), repo, id)
}
