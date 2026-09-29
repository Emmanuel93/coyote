// Package web sirve la vista de operación de coyote (ADR-0019): costos,
// presupuesto, workstreams, gate, SLOs y, para admins, la organización.
// Todo se lee en cada petición del ledger, de los planes y de la cola que ya
// existen. Solo lectura, solo en loopback, con verificación de Host contra DNS
// rebinding y sin JavaScript. Decidir (aprobar, rechazar, revocar) se hace en
// la terminal.
package web

import (
	"embed"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Emmanuel93/coyote/internal/ccf"
	"github.com/Emmanuel93/coyote/internal/safetext"
	"github.com/Emmanuel93/coyote/internal/usage"
)

//go:embed templates/*.html
var templatesFS embed.FS

// Server arma las vistas. La CLI llena las fuentes; una fuente nil muestra
// la sección vacía con cómo llenarla.
type Server struct {
	Title   string
	Sources []string
	Now     func() time.Time
	// Viewer es la persona que corre la web (@usuario, su identidad de git).
	Viewer string
	// Admin dice si Viewer ve el desglose de todas las personas del proyecto
	// (D10): admins del hub o del proyecto.
	Admin bool
	// OrgAdmin dice si Viewer es admin del hub: solo él ve la organización.
	OrgAdmin bool
	// Admins son los admins declarados en el hub y en el proyecto.
	Admins []string
	// Hub describe el hub que rige ("acme (main@abc1234)"), o "".
	Hub string
	// Notices son avisos para todas las páginas (un hub que no se pudo leer).
	Notices []string

	Load   func() ([]usage.Event, error)
	Budget func() (Budget, error)
	Work   func() ([]Workstream, error)
	Gate   func() (Gate, error)
	SLOs   func() ([]SLO, error)
	// Org devuelve la organización con los eventos de todos sus proyectos;
	// solo se llama para un admin.
	Org func() (*Org, error)
}

// Budget son los topes que aplican al proyecto y el gasto del mes, contado
// como lo cuenta coyote run: el ledger del proyecto, en el mes UTC.
type Budget struct {
	MonthlyUSD    float64 // tope mensual del proyecto (project.yaml); 0 = sin tope
	RunUSD        float64 // tope por corrida del router; 0 = sin dato
	OrgMonthlyUSD float64 // tope mensual de la organización (hub); 0 = sin tope
	SpentUSD      float64 // gasto del mes del proyecto
	MineUSD       float64 // la parte de quien mira
}

// Workstream es un plan con el estado de sus pasos.
type Workstream struct {
	ID, Title, Mode, Gate string
	Owner                 string // @persona dueña del plan
	Closed                bool
	SpentUSD, BudgetUSD   float64 // BudgetUSD 0 = sin tope
	Runs                  int
	Steps                 []Step
	Problem               string // el plan no se pudo leer
}

// Step es un paso de un plan.
type Step struct {
	ID, Does, Agent, Status string
	Runs                    int
	CostUSD                 float64
	At                      time.Time
}

// Gate es la cola del gate, las aprobaciones y los gates de release.
type Gate struct {
	Pending  []Pending
	Grants   []Grant
	Releases []Release
	Problems []string
}

// Pending es una acción que espera decisión de una persona.
type Pending struct {
	ID, By, Action, State string
	First                 time.Time
	Attempts              int
}

// Grant es una aprobación firmada.
type Grant struct {
	ID, Action, Approver, State string
	Left                        int
	Expires                     time.Time
}

// Release es la decisión de un gate de release (G1, G2…).
type Release struct {
	ID, Gate, Release, Decision, Approver, Date string
	Authorizes                                  []string
}

// SLO es un objetivo de un servicio y el estado de sus alertas.
type SLO struct {
	Service, Name string
	Objective     float64 // porcentaje
	PeriodDays    int
	Page, Ticket  bool
	Runbook       string
	Current       bool   // las reglas generadas están vigentes
	Problem       string // el archivo no valida o las reglas no están al día
}

// BudgetMinutes es el presupuesto de error del periodo en minutos: lo que
// el servicio puede fallar sin romper el objetivo.
func (s SLO) BudgetMinutes() float64 {
	return (100 - s.Objective) / 100 * float64(s.PeriodDays) * 24 * 60
}

// Org es la organización del hub con los eventos de sus proyectos.
type Org struct {
	Name, Hub  string
	MonthlyUSD float64
	Projects   []OrgProject
	Events     []usage.Event // de todos los proyectos con clon, con Repo = su nombre
}

// OrgProject es un proyecto de la organización.
type OrgProject struct {
	Name, State string // State: "ok" o por qué no se lee
	MonthlyUSD  float64
}

type option struct{ Key, Label string }

var (
	views   = []option{{"project", "Proyecto › Persona"}, {"person", "Persona › Proyecto"}, {"model", "Modelos › Persona"}, {"agent", "Agentes › Proyecto"}}
	periods = []option{{"7d", "7 días"}, {"30d", "30 días"}, {"90d", "90 días"}, {"all", "Todo"}}
)

var funcs = template.FuncMap{
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
	"width": func(part, whole float64) string {
		if whole <= 0 || part <= 0 {
			return "0"
		}
		if part > whole {
			return "100"
		}
		return fmt.Sprintf("%.1f", 100*part/whole)
	},
	"models": func(m map[string]int) string {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return strings.Join(keys, " · ")
	},
	"day": func(t time.Time) string {
		if t.IsZero() {
			return "—"
		}
		return t.UTC().Format("2006-01-02 15:04")
	},
	"minutes": func(m float64) string {
		switch {
		case m >= 120:
			return fmt.Sprintf("%.1f h", m/60)
		case m >= 1:
			return fmt.Sprintf("%.0f min", m)
		}
		return fmt.Sprintf("%.0f s", m*60)
	},
	"objective": func(v float64) string {
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.4f", v), "0"), ".") + " %"
	},
	"status": statusLabel,
	"decision": func(d string) string {
		switch d {
		case "approved":
			return "aprobado"
		case "rejected":
			return "rechazado"
		}
		return d
	},
	"inc": func(i int) int { return i + 1 },
}

// statusLabel traduce el estado de un paso.
func statusLabel(s string) string {
	switch s {
	case "pending":
		return "pendiente"
	case "review":
		return "por revisar"
	case "done":
		return "hecho"
	case "failed":
		return "falló"
	case "blocked":
		return "en la cola del gate"
	case "redo":
		return "rehacer"
	}
	return s
}

var pages = map[string]*template.Template{}

func init() {
	layout := template.Must(template.New("layout.html").Funcs(funcs).ParseFS(templatesFS, "templates/layout.html"))
	for _, name := range []string{"costs", "budget", "work", "gate", "slo", "org", "message"} {
		pages[name] = template.Must(template.Must(layout.Clone()).ParseFS(templatesFS, "templates/"+name+".html"))
	}
}

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
	mux.HandleFunc("/", s.costs)
	mux.HandleFunc("/api/usage", s.api)
	mux.HandleFunc("/presupuesto", s.budget)
	mux.HandleFunc("/workstreams", s.work)
	mux.HandleFunc("/gate", s.gate)
	mux.HandleFunc("/slo", s.slo)
	mux.HandleFunc("/org", s.org)
	return protect(mux)
}

func protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		host := r.Host
		if hh, _, err := net.SplitHostPort(r.Host); err == nil {
			host = hh
		}
		host = strings.Trim(host, "[]")
		if host != "localhost" && !(net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()) {
			http.Error(w, "host no permitido", http.StatusForbidden)
			return
		}
		if r.URL.IsAbs() || r.RequestURI != "" && !strings.HasPrefix(r.RequestURI, "/") {
			http.Error(w, "solo rutas relativas", http.StatusBadRequest) // forma absoluta: puede esquivar Host
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "solo lectura", http.StatusMethodNotAllowed)
			return
		}
		// Ningún dato llega a la página con controles o caracteres de formato
		// invisibles que reordenen o escondan texto (ver safetext).
		sw := &safeResponse{ResponseWriter: w}
		sw.out = safetext.NewWriter(w)
		next.ServeHTTP(sw, r)
		_ = sw.out.Flush()
	})
}

// safeResponse escapa el cuerpo de cada respuesta con safetext.
type safeResponse struct {
	http.ResponseWriter
	out *safetext.Writer
}

func (s *safeResponse) Write(p []byte) (int, error) { return s.out.Write(p) }

type navItem struct {
	Path, Label string
	On          bool
}

// pageData es lo que comparten todas las páginas.
type pageData struct {
	Title, Section, Viewer, Hub, Sources string
	Admin                                bool
	Admins                               []string
	Nav                                  []navItem
	Notices                              []string
	Body                                 any
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Server) page(section string, body any) pageData {
	items := []navItem{{"/", "Costos", false}, {"/presupuesto", "Presupuesto", false}, {"/workstreams", "Workstreams", false},
		{"/gate", "Gate", false}, {"/slo", "SLOs", false}}
	if s.OrgAdmin && s.Org != nil {
		items = append(items, navItem{"/org", "Organización", false})
	}
	for i := range items {
		items[i].On = items[i].Path == section
	}
	return pageData{Title: s.Title, Section: section, Viewer: s.Viewer, Hub: s.Hub, Admin: s.Admin, Admins: s.Admins,
		Sources: strings.Join(s.Sources, ", "), Nav: items, Notices: s.Notices, Body: body}
}

func (s *Server) render(w http.ResponseWriter, name, section string, body any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pages[name].ExecuteTemplate(w, "layout.html", s.page(section, body)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// message muestra una página con un texto, con su código de estado.
func (s *Server) message(w http.ResponseWriter, section string, code int, title, text string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	_ = pages["message"].ExecuteTemplate(w, "layout.html", s.page(section, map[string]string{"Heading": title, "Text": text}))
}

func only(w http.ResponseWriter, r *http.Request, path string) bool {
	if r.URL.Path != path {
		http.NotFound(w, r)
		return false
	}
	return true
}
