// Package github es un cliente mínimo de la API de GitHub que respeta el ritmo
// humano (internal/pace) y los límites del proveedor. Nunca reintenta en un
// bucle: si GitHub pide esperar, informa hasta cuándo.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Emmanuel93/coyote/internal/pace"
)

// DefaultAPI es la API pública de GitHub.
const DefaultAPI = "https://api.github.com"

// Client llama a la API con el token de la persona.
type Client struct {
	Base      string
	Token     string
	HTTP      *http.Client
	Pace      *pace.Limiter
	UserAgent string
}

// Error es una respuesta de error de la API.
type Error struct {
	Status  int
	Message string
	Until   time.Time // si GitHub pidió esperar
}

func (e *Error) Error() string {
	if !e.Until.IsZero() {
		return fmt.Sprintf("GitHub %d: %s; reintenta después de %s", e.Status, e.Message, e.Until.Format("15:04:05"))
	}
	return fmt.Sprintf("GitHub %d: %s", e.Status, e.Message)
}

// Do hace una llamada. method distinto de GET cuenta como escritura para el ritmo.
func (c *Client) Do(ctx context.Context, method, path string, body, out any) (*http.Response, error) {
	kind := "read"
	if method != http.MethodGet && method != http.MethodHead {
		kind = "write"
	}
	if c.Pace != nil {
		if _, err := c.Pace.Wait(kind); err != nil {
			return nil, err
		}
	}
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(data)
	}
	base := c.Base
	if base == "" {
		base = DefaultAPI
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	ua := c.UserAgent
	if ua == "" {
		ua = "coyote"
	}
	req.Header.Set("User-Agent", ua)
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if c.Pace != nil {
		_ = c.Pace.Observe(resp.Header, resp.StatusCode)
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &e)
		apiErr := &Error{Status: resp.StatusCode, Message: e.Message}
		if apiErr.Message == "" {
			apiErr.Message = resp.Status
		}
		if (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests) && c.Pace != nil {
			if t := c.Pace.RetryAt(); t.After(c.Pace.Now()) {
				apiErr.Until = t
			}
		}
		return resp, apiErr
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return resp, fmt.Errorf("respuesta inesperada de %s: %w", path, err)
		}
	}
	return resp, nil
}

// User devuelve el login de la persona dueña del token y sus scopes.
func (c *Client) User(ctx context.Context) (string, []string, error) {
	var u struct {
		Login string `json:"login"`
	}
	resp, err := c.Do(ctx, http.MethodGet, "/user", nil, &u)
	if err != nil {
		return "", nil, err
	}
	var scopes []string
	for _, s := range strings.Split(resp.Header.Get("X-OAuth-Scopes"), ",") {
		if s = strings.TrimSpace(s); s != "" {
			scopes = append(scopes, s)
		}
	}
	return u.Login, scopes, nil
}

// Repo es lo que coyote necesita saber de un repo.
type Repo struct {
	FullName      string    `json:"full_name"`
	Private       bool      `json:"private"`
	DefaultBranch string    `json:"default_branch"`
	PushedAt      time.Time `json:"pushed_at"`
	HTMLURL       string    `json:"html_url"`
}

// GetRepo lee un repo por owner/nombre.
func (c *Client) GetRepo(ctx context.Context, fullName string) (*Repo, error) {
	if strings.Count(fullName, "/") != 1 || strings.ContainsAny(fullName, " ?#") {
		return nil, fmt.Errorf("repo inválido %q; usa owner/nombre", fullName)
	}
	var r Repo
	_, err := c.Do(ctx, http.MethodGet, "/repos/"+fullName, nil, &r)
	return &r, err
}
