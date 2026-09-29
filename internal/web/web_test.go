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
	return &Server{Title: "tienda", Sources: []string{"tienda"}, Viewer: "@ana", Admin: true, Admins: []string{"@ana"}, Now: func() time.Time { return now }, Load: func() ([]usage.Event, error) {
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

func TestVisibilidadD10(t *testing.T) {
	s := server()
	s.Admin = false // @ana sin ser admin: ve lo suyo y los totales
	h := s.Handler()
	body := get(t, h, "127.0.0.1", "/?view=person&since=all").Body.String()
	if strings.Contains(body, "@luis") || !strings.Contains(body, "@ana") {
		t.Fatalf("una persona que no es admin no ve el desglose de otras:\n%s", body)
	}
	if !strings.Contains(body, "total del proyecto") || !strings.Contains(body, "$0.034") {
		t.Fatal("sí ve el total del proyecto, sin desglose")
	}
	if !strings.Contains(body, "no control de acceso") {
		t.Fatal("la página dice que D10 es visibilidad por defecto")
	}
	var out struct {
		Scope         string
		Rows          []usage.Row
		ProjectTotals usage.Totals `json:"project_totals"`
	}
	w := get(t, h, "127.0.0.1", "/api/usage?view=person&since=all")
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.Scope != "@ana" || len(out.Rows) != 1 || out.Rows[0].Key != "@ana" || out.ProjectTotals.Events != 4 {
		t.Fatalf("la API aplica D10: %s", w.Body)
	}
	// La organización es solo para admins.
	s.Org = func() (*Org, error) { return &Org{Name: "acme"}, nil }
	if w := get(t, s.Handler(), "127.0.0.1", "/org"); w.Code != http.StatusForbidden {
		t.Fatalf("/org sin ser admin: %d", w.Code)
	}
	s.Admin = true
	if w := get(t, s.Handler(), "127.0.0.1", "/org"); w.Code != 200 {
		t.Fatalf("/org de admin: %d %s", w.Code, w.Body)
	}
	s.Org = nil
	if w := get(t, s.Handler(), "127.0.0.1", "/org"); w.Code != http.StatusNotFound {
		t.Fatalf("/org sin hub: %d", w.Code)
	}
}

func TestVistasDeOperacion(t *testing.T) {
	s := server()
	now := s.Now()
	s.Budget = func() (Budget, error) { return Budget{MonthlyUSD: 0.03, RunUSD: 2, OrgMonthlyUSD: 100}, nil }
	s.Work = func() ([]Workstream, error) {
		return []Workstream{
			{ID: "W-0002", Title: "pagos", Mode: "supervised", BudgetUSD: 5, SpentUSD: 1.25, Runs: 3, Steps: []Step{
				{ID: "S1", Does: "diseña <b>pagos</b>", Agent: "coyote-architect", Status: "done", Runs: 1, CostUSD: 0.5, At: now},
				{ID: "S2", Does: "implementa", Agent: "coyote-dev", Status: "blocked", Runs: 2, CostUSD: 0.75},
				{ID: "S3", Does: "revisa", Agent: "persona", Status: "pending"}}},
			{ID: "W-0001", Title: "arranque", Closed: true},
		}, nil
	}
	s.Gate = func() (Gate, error) {
		return Gate{
			Pending:  []Pending{{ID: "P-abc234", By: "@ana/coyote-dev", Action: "rm -rf <script>alert(1)</script>", State: "pendiente (2 intentos)", First: now}},
			Grants:   []Grant{{ID: "P-xyz789", Action: "make test", Approver: "@ana", State: "vigente", Left: 3, Expires: now.Add(time.Hour)}},
			Releases: []Release{{ID: "P-0001", Gate: "G1", Release: "v0.1.0", Decision: "approved", Approver: "@ana", Date: "2026-09-01", Authorizes: []string{"etiquetar v0.1.0"}}},
		}, nil
	}
	s.SLOs = func() ([]SLO, error) {
		return []SLO{{Service: "pagos-api", Name: "disponibilidad", Objective: 99.9, PeriodDays: 30, Page: true, Ticket: true, Runbook: "coyote/runbooks/pagos.md", Current: true}}, nil
	}
	h := s.Handler()
	cases := map[string][]string{
		"/presupuesto": {"Presupuesto de septiembre de 2026", "pasó el tope", "$2.00", "$100.00", "W-0002"},
		"/workstreams": {"W-0002", "en la cola del gate", "1 hechos", "1 con problemas", "diseña &lt;b&gt;pagos&lt;/b&gt;", "cerrado"},
		"/gate":        {"P-abc234", "&lt;script&gt;", "coyote approve", "P-xyz789", "vigente", "G1", "etiquetar v0.1.0"},
		"/slo":         {"pagos-api · disponibilidad", "99.9 %", "43 min", "vigentes", "coyote/runbooks/pagos.md"},
	}
	for path, wants := range cases {
		w := get(t, h, "127.0.0.1", path)
		body := w.Body.String()
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", path, w.Code, body)
		}
		for _, want := range wants {
			if !strings.Contains(body, want) {
				t.Errorf("%s: falta %q", path, want)
			}
		}
		if strings.Contains(body, "<script>") || strings.Contains(body, "<b>pagos") || strings.Contains(body, "<form") {
			t.Errorf("%s: sin scripts, sin HTML del ledger y sin formularios", path)
		}
	}
	// Sin fuentes, cada sección dice cómo llenarla.
	empty := &Server{Title: "x", Viewer: "@ana", Load: func() ([]usage.Event, error) { return nil, nil }}
	for _, path := range []string{"/presupuesto", "/workstreams", "/gate", "/slo"} {
		if w := get(t, empty.Handler(), "127.0.0.1", path); w.Code != 200 {
			t.Fatalf("%s vacío: %d %s", path, w.Code, w.Body)
		}
	}
	if w := get(t, h, "127.0.0.1", "/nada"); w.Code != http.StatusNotFound {
		t.Fatalf("ruta desconocida: %d", w.Code)
	}
}

func TestProyeccionDelMes(t *testing.T) {
	start, name, elapsed, days := monthOf(time.Date(2026, 2, 15, 12, 0, 0, 0, time.UTC))
	if start.Day() != 1 || name != "febrero de 2026" || days != 28 || elapsed < 14.4 || elapsed > 14.6 {
		t.Fatalf("mes: %v %s %v %d", start, name, elapsed, days)
	}
	for _, c := range []struct {
		spent, proj, cap float64
		class            string
	}{{1, 2, 0, ""}, {5, 6, 10, "ok"}, {8.5, 9, 10, "warn"}, {4, 11, 10, "warn"}, {10, 20, 10, "bad"}} {
		if _, class := capState(c.spent, c.proj, c.cap); class != c.class {
			t.Errorf("%+v: %s", c, class)
		}
	}
}
