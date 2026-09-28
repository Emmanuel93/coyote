package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Emmanuel93/coyote/internal/pace"
)

func TestClientPaceAndErrors(t *testing.T) {
	t.Setenv("COYOTE_STATE_DIR", t.TempDir())
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if got := r.Header.Get("Authorization"); got != "Bearer tok_0123456789abcdefghij" {
			t.Errorf("Authorization inesperado %q", got)
		}
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", "4999")
		w.Header().Set("X-RateLimit-Reset", "4102444800")
		switch r.URL.Path {
		case "/user":
			w.Header().Set("X-OAuth-Scopes", "repo, read:org")
			w.Write([]byte(`{"login":"ana"}`))
		case "/repos/acme/tienda":
			w.Write([]byte(`{"full_name":"acme/tienda","private":true,"default_branch":"main"}`))
		case "/limitado":
			w.Header().Set("Retry-After", "90")
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"message":"You have exceeded a secondary rate limit"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"message":"Not Found"}`))
		}
	}))
	defer srv.Close()
	lim, err := pace.Open("human", "test")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	lim.Now = func() time.Time { return now }
	lim.Sleep = func(d time.Duration) { now = now.Add(d) }
	c := &Client{Base: srv.URL, Token: "tok_0123456789abcdefghij", Pace: lim}
	login, scopes, err := c.User(context.Background())
	if err != nil || login != "ana" || strings.Join(scopes, ",") != "repo,read:org" {
		t.Fatalf("User: %q %v %v", login, scopes, err)
	}
	r, err := c.GetRepo(context.Background(), "acme/tienda")
	if err != nil || !r.Private || r.DefaultBranch != "main" {
		t.Fatalf("GetRepo: %+v %v", r, err)
	}
	if _, err := c.GetRepo(context.Background(), "acme/tienda?x=1"); err == nil {
		t.Fatal("un nombre de repo con consulta debe rechazarse")
	}
	_, err = c.Do(context.Background(), http.MethodGet, "/limitado", nil, nil)
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Status != 403 || apiErr.Until.IsZero() {
		t.Fatalf("límite secundario sin fecha de reintento: %v", err)
	}
	before := calls
	// La siguiente llamada espera el Retry-After (90 s) en vez de insistir.
	if _, _, err := c.User(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != before+1 || now.Before(time.Date(2026, 9, 28, 12, 1, 30, 0, time.UTC)) {
		t.Fatalf("no respetó Retry-After: reloj %v", now)
	}
}
