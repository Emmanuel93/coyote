package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Emmanuel93/coyote/internal/ccf"
	"github.com/Emmanuel93/coyote/internal/usage"
)

func server() *Server {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	mk := func(repo, actor, model, what string, in, out int64, cin, cout float64, daysAgo int) usage.Event {
		return usage.Event{Repo: repo, Line: ccf.Line{TS: now.AddDate(0, 0, -daysAgo), Actor: actor, Repo: repo, Type: "run", Scope: "-",
			What: what, Status: "ok", Refs: []string{"model:" + model}, Tokens: &ccf.Tokens{In: in, Out: out}, Cost: &ccf.Cost{In: cin, Out: cout}}}
	}
	return &Server{Title: "tienda", Sources: []string{"tienda"}, Now: func() time.Time { return now }, Load: func() ([]usage.Event, error) {
		return []usage.Event{
			mk("tienda", "@ana/coyote-dev", "sonnet-5", "<script>alert(1)</script>", 12000, 1100, 0.009, 0.011, 1),
			mk("pagos", "@luis", "opus-5.5", "x", 2000, 300, 0.008, 0.006, 2),
			mk("tienda", "@luis", "haiku-4.5", "viejo", 500, 50, 0.0005, 0.00025, 60),
			mk("tienda", "@ana", "<img src=x onerror=alert(1)>", "x", 1, 1, 0, 0, 1),
		}, nil
	}}
}

func get(t *testing.T, h http.Handler, host, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Host = host
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestPageViews(t *testing.T) {
	h := server().Handler()
	w := get(t, h, "127.0.0.1:7410", "/")
	if w.Code != 200 {
		t.Fatalf("GET /: %d %s", w.Code, w.Body)
	}
	body := w.Body.String()
	for _, want := range []string{"Costos de agentes", "tienda", "@ana", "@luis", "$0.034", "30 días", "sonnet-5"} {
		if !strings.Contains(body, want) {
			t.Errorf("falta %q en la página", want)
		}
	}
	if strings.Contains(body, "<img") || !strings.Contains(body, "&lt;img") {
		t.Error("los nombres de modelo del ledger deben escaparse")
	}
	if strings.Contains(body, "<script>") || strings.Contains(body, "viejo") {
		t.Error("la página no debe tener scripts ni eventos fuera del periodo")
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "default-src 'none'") {
		t.Error("falta CSP")
	}
	w = get(t, h, "localhost:7410", "/?view=person&since=all")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Persona › proyecto") || !strings.Contains(w.Body.String(), "haiku-4.5") {
		t.Fatalf("vista persona: %d", w.Code)
	}
}

func TestAPIAndProtections(t *testing.T) {
	h := server().Handler()
	w := get(t, h, "127.0.0.1:7410", "/api/usage?view=model&since=7d")
	var out struct {
		View   string
		Totals usage.Totals
		Rows   []usage.Row
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.View != "model" || out.Totals.Events != 3 || len(out.Rows) != 3 {
		t.Fatalf("API: %s %v", w.Body, err)
	}
	if w := get(t, h, "evil.example.com", "/"); w.Code != http.StatusForbidden {
		t.Fatalf("un Host ajeno (DNS rebinding) debe rechazarse: %d", w.Code)
	}
	if w := get(t, h, "[::1]:7410", "/"); w.Code != 200 {
		t.Fatalf("::1 es loopback: %d", w.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Host = "127.0.0.1"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST debe rechazarse: %d", rec.Code)
	}
	for addr, ok := range map[string]bool{"127.0.0.1:7410": true, "localhost:0": true, "[::1]:80": true, "0.0.0.0:7410": false, "192.168.1.5:80": false, ":7410": false} {
		if (Loopback(addr) == nil) != ok {
			t.Errorf("Loopback(%q) debería ser %v", addr, ok)
		}
	}
}
