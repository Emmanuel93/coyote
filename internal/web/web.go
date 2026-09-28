// Package web sirve la vista FinOps de coyote: consumo de tokens y costo por
// proyecto y por persona, leído del ledger en cada petición. Solo lectura,
// solo en la interfaz de loopback y con verificación de Host contra DNS
// rebinding; sin JavaScript.
package web

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Emmanuel93/coyote/internal/ccf"
	"github.com/Emmanuel93/coyote/internal/usage"
)

//go:embed page.html
var pageHTML string

// Server arma las vistas a partir de los eventos que entrega Load.
type Server struct {
	Title   string
	Sources []string
	Load    func() ([]usage.Event, error)
	Now     func() time.Time
}

type option struct{ Key, Label string }

var (
	views = []option{{"project", "Proyecto › Persona"}, {"person", "Persona › Proyecto"}, {"model", "Modelos › Persona"}, {"agent", "Agentes › Proyecto"}}
	// periods: la clave es lo que va en ?since=.
	periods = []option{{"7d", "7 días"}, {"30d", "30 días"}, {"90d", "90 días"}, {"all", "Todo"}}
)

var page = template.Must(template.New("page").Funcs(template.FuncMap{
	"usd":   func(v float64) string { return "$" + usd(v) },
	"count": func(n int64) string { return ccf.FormatCount(n) },
	"pct":   func(v float64) string { return fmt.Sprintf("%.0f%%", 100*v) },
	"bar": func(t usage.Totals, max float64) string {
		if max <= 0 {
			return "0"
		}
		v := t.CostTotal()
		if v == 0 {
			v = float64(t.Tokens.In + t.Tokens.Out)
		}
		return fmt.Sprintf("%.1f", 100*v/max)
	},
	"models": func(m map[string]int) string {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return strings.Join(keys, " · ")
	},
}).Parse(pageHTML))

func usd(v float64) string {
	s := ccf.FormatUSD(v)
	if !strings.Contains(s, ".") {
		return s + ".00"
	}
	if i := strings.Index(s, "."); len(s)-i-1 < 2 {
		s += "0"
	}
	return s
}

// Loopback valida que la dirección escuche solo en esta máquina.
func Loopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("coyote web solo escucha en loopback (127.0.0.1 o ::1), no en %q", host)
	}
	return nil
}

// Handler devuelve el manejador HTTP con las protecciones aplicadas.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.page)
	mux.HandleFunc("/api/usage", s.api)
	return protect(mux)
}

func protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		host := r.Host
		if hh, _, err := net.SplitHostPort(r.Host); err == nil {
			host = hh
		}
		host = strings.Trim(host, "[]")
		if host != "localhost" && !(net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()) {
			http.Error(w, "host no permitido", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "solo lectura", http.StatusMethodNotAllowed)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type viewData struct {
	Title, View, Since, PeriodLabel, Heading, Sources string
	All                                               usage.Totals
	Rows                                              []usage.Row
	Max                                               float64
	Views, Periods                                    []option
}

func (s *Server) build(r *http.Request) (*viewData, error) {
	view := r.URL.Query().Get("view")
	if !valid(views, view) {
		view = "project"
	}
	since := r.URL.Query().Get("since")
	if !valid(periods, since) {
		since = "30d"
	}
	events, err := s.Load()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now()
	}
	var from time.Time
	switch since {
	case "7d":
		from = now.AddDate(0, 0, -7)
	case "30d":
		from = now.AddDate(0, 0, -30)
	case "90d":
		from = now.AddDate(0, 0, -90)
	}
	events = usage.Filter(events, from)
	outer, inner, heading := usage.ByRepo, usage.ByPerson, "Proyecto › persona"
	switch view {
	case "person":
		outer, inner, heading = usage.ByPerson, usage.ByRepo, "Persona › proyecto"
	case "model":
		outer, inner, heading = usage.ByModel, usage.ByPerson, "Modelo › persona"
	case "agent":
		outer, inner, heading = usage.ByAgent, usage.ByRepo, "Agente › proyecto"
	}
	rows, all := usage.Group(events, outer, inner)
	if view == "model" || view == "agent" {
		// Estas vistas son de consumo: los eventos sin tokens ni costo (notas,
		// aprobaciones) no tienen modelo y solo agregarían ruido.
		kept := rows[:0]
		for _, r := range rows {
			if r.Tokens.In+r.Tokens.Out > 0 || r.CostTotal() > 0 {
				kept = append(kept, r)
			}
		}
		rows = kept
	}
	d := &viewData{Title: s.Title, View: view, Since: since, Heading: heading, All: all, Rows: rows,
		Views: views, Periods: periods, Sources: strings.Join(s.Sources, ", ")}
	for _, p := range periods {
		if p.Key == since {
			d.PeriodLabel = p.Label
		}
	}
	for _, r := range rows {
		v := r.CostTotal()
		if v == 0 {
			v = float64(r.Tokens.In + r.Tokens.Out)
		}
		if v > d.Max {
			d.Max = v
		}
	}
	return d, nil
}

func valid(opts []option, key string) bool {
	for _, o := range opts {
		if o.Key == key {
			return true
		}
	}
	return false
}

func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	d, err := s.build(r)
	if err != nil {
		http.Error(w, "no se pudo leer el ledger: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := page.Execute(w, d); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) api(w http.ResponseWriter, r *http.Request) {
	d, err := s.build(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(map[string]any{"view": d.View, "since": d.Since, "totals": d.All, "rows": d.Rows})
}
