package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Emmanuel93/coyote/internal/fsx"
	"github.com/Emmanuel93/coyote/internal/hub"
	"github.com/Emmanuel93/coyote/internal/identity"
	"github.com/Emmanuel93/coyote/internal/ledger"
	"github.com/Emmanuel93/coyote/internal/project"
	"github.com/Emmanuel93/coyote/internal/router"
	"github.com/Emmanuel93/coyote/internal/secrets"
	"github.com/Emmanuel93/coyote/internal/usage"
	"github.com/Emmanuel93/coyote/internal/web"
	"github.com/Emmanuel93/coyote/internal/workstream"
)

// webServe arranca el servidor; las pruebas lo reemplazan.
var webServe = func(srv *http.Server, ln net.Listener) error { return srv.Serve(ln) }

func cmdWeb(a *app, args []string) error {
	fs := a.flags("web", "[--addr 127.0.0.1:7410]")
	addr := fs.String("addr", "127.0.0.1:7410", "dirección de escucha; solo loopback")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	if err := web.Loopback(*addr); err != nil {
		return fail(2, "%v", err)
	}
	if host, port, err := net.SplitHostPort(*addr); err == nil && host == "localhost" {
		*addr = net.JoinHostPort("127.0.0.1", port) // no se confía en lo que resuelva localhost
	}
	root, cfg, err := a.project()
	if err != nil {
		return err
	}
	s, names, err := a.webServer(root, cfg)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	role := "ves tu gasto y los totales (D10)"
	if s.Admin {
		role = "eres admin: ves el gasto de todas las personas"
	}
	fmt.Fprintf(a.stdout, "coyote web en http://%s · %d fuentes (%v) · %s %s · solo esta máquina · Ctrl+C para salir\n",
		ln.Addr(), len(names), names, s.Viewer, role)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	if err := webServe(srv, ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// webServer arma el servidor con sus fuentes. Lo que falla al abrir (un hub
// que no se lee) queda como aviso y quien mira no es admin: por defecto se
// muestra menos, no más (D10).
func (a *app) webServer(root string, cfg *project.Config) (*web.Server, []string, error) {
	type source struct{ name, root string }
	sources := []source{{cfg.Name, root}}
	names := []string{cfg.Name}
	for _, r := range cfg.Repos {
		dir := ""
		if r.Path != "" {
			p := r.Path
			if !filepath.IsAbs(p) {
				p = filepath.Join(root, p)
			}
			dir = p
		} else if _, d := docsDir(root, r.Name); d != "" {
			dir = d
		}
		if dir == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "coyote", "ledger")); err == nil {
			sources = append(sources, source{r.Name, dir})
			names = append(names, r.Name)
		}
	}
	viewer := identity.Resolve(root).Actor("")
	s := &web.Server{Title: cfg.Name, Sources: names, Now: a.now, Viewer: viewer}
	s.Load = func() ([]usage.Event, error) {
		var all []usage.Event
		for _, src := range sources {
			entries, _, err := ledger.Open(src.root).ReadAll()
			if err != nil {
				return nil, err
			}
			evs := usage.FromLedger(src.name, entries)
			if src.root != root {
				// Un repo registrado solo aporta sus propios eventos: su ledger no
				// puede atribuir consumo a otro proyecto.
				for i := range evs {
					evs[i].Repo = src.name
				}
			}
			all = append(all, evs...)
		}
		return all, nil
	}

	admins := append([]string{}, cfg.Admins...)
	h, herr := a.openHub(root, cfg)
	if herr != nil {
		s.Notices = append(s.Notices, "El hub no se pudo leer: "+herr.Error()+". Sin él no hay admins de la organización.")
	}
	if h != nil {
		s.Hub = h.String()
		admins = append(admins, h.Conf.Admins...)
	}
	sort.Strings(admins)
	for i, x := range admins {
		if i == 0 || x != admins[i-1] {
			s.Admins = append(s.Admins, x)
		}
	}
	for _, x := range s.Admins {
		s.Admin = s.Admin || x == viewer
	}

	s.Budget = func() (web.Budget, error) {
		b := web.Budget{MonthlyUSD: cfg.Budgets.MonthlyUSD}
		if rc, err := router.Load(root); err == nil {
			b.RunUSD = rc.Limits.MaxUSD
		}
		if h != nil {
			b.OrgMonthlyUSD = h.Conf.Budgets.MonthlyUSD
		}
		return b, nil
	}
	s.Work = func() ([]web.Workstream, error) { return workViews(root) }
	s.Gate = func() (web.Gate, error) { return a.gateView(root) }
	s.SLOs = func() ([]web.SLO, error) { return sloViews(root) }
	if h != nil {
		s.Org = func() (*web.Org, error) { return a.orgView(h) }
	}
	return s, names, nil
}

// workViews lee los planes y su estado desde el ledger.
func workViews(root string) ([]web.Workstream, error) {
	ids, err := workstream.All(root)
	if err != nil {
		return nil, err
	}
	entries, _, err := ledger.Open(root).ReadAll()
	if err != nil {
		return nil, err
	}
	var out []web.Workstream
	for _, id := range ids {
		p, err := workstream.Load(root, id)
		if err != nil {
			out = append(out, web.Workstream{ID: id, Problem: err.Error()})
			continue
		}
		st := workstream.Fold(p, entries)
		w := web.Workstream{ID: p.ID, Title: p.Title, Mode: p.Autonomy, Gate: p.Gate, Closed: st.Closed,
			SpentUSD: st.Spent, BudgetUSD: p.BudgetUSD, Runs: st.Runs}
		for _, x := range st.Steps {
			w.Steps = append(w.Steps, web.Step{ID: x.ID, Does: x.Does, Agent: x.Agent, Status: x.Status, Runs: x.Runs, CostUSD: x.CostUSD, At: x.At})
		}
		out = append(out, w)
	}
	return out, nil
}

// webAction resume una acción para la web: una línea visible, corta y sin
// secretos a la vista; el detalle completo se ve con coyote review.
func webAction(id, object string) string {
	text := oneLineVisible(shortText(object, 140))
	if len(secrets.ScanLine("accion", 1, object)) > 0 {
		return "(parece llevar un secreto; revísala con coyote review " + id + ")"
	}
	return text
}

// gateView arma la cola, las aprobaciones firmadas en esta máquina y los
// gates de release.
func (a *app) gateView(root string) (web.Gate, error) {
	var g web.Gate
	c, err := a.approvalCtx()
	if err != nil {
		return g, err
	}
	queue, err := c.store.Queue()
	if err != nil {
		return g, err
	}
	for _, p := range queue {
		state := "pendiente (" + plural(p.Attempts, "intento", "intentos") + ")"
		if p.Status == "rejected" {
			state = "rechazada: " + shortText(p.RejectReason, 60)
		}
		g.Pending = append(g.Pending, web.Pending{ID: p.ID, By: oneLineVisible(p.RequestedBy), Action: webAction(p.ID, p.Object),
			State: oneLineVisible(state), First: p.First, Attempts: p.Attempts})
	}
	sts, problems, err := c.statuses()
	if err != nil {
		return g, err
	}
	for _, st := range sts {
		state := "vigente"
		switch {
		case st.Revoked:
			state = "revocada"
		case st.Expired:
			state = "vencida"
		case st.Left <= 0:
			state = "agotada"
		}
		exp, _ := time.Parse(time.RFC3339, st.Expires)
		left := st.Left
		if left < 0 {
			left = 0
		}
		g.Grants = append(g.Grants, web.Grant{ID: st.ID, Action: webAction(st.ID, st.Object), Approver: oneLineVisible(st.Approver), State: state, Left: left, Expires: exp})
	}
	sort.SliceStable(g.Grants, func(i, j int) bool { return g.Grants[i].Expires.After(g.Grants[j].Expires) })
	for name, perr := range problems {
		g.Problems = append(g.Problems, fmt.Sprintf("%s: %v", name, perr))
	}
	sort.Strings(g.Problems)
	g.Releases = releaseGates(root)
	return g, nil
}

// releaseGates lee las decisiones de los gates de release en coyote/approvals.
func releaseGates(root string) []web.Release {
	dir := filepath.Join(root, "coyote", "approvals")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []web.Release
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := fsx.ReadFile(root, "coyote/approvals/"+e.Name(), 1<<20)
		if err != nil {
			continue
		}
		var r struct {
			ID         string   `json:"id"`
			Type       string   `json:"type"`
			Gate       string   `json:"gate"`
			Release    string   `json:"release"`
			Decision   string   `json:"decision"`
			Approver   string   `json:"approver"`
			Date       string   `json:"date"`
			Authorizes []string `json:"authorizes"`
		}
		if json.Unmarshal(data, &r) != nil || r.Type == "action" || r.Gate == "" {
			continue
		}
		rel := web.Release{ID: oneLineVisible(r.ID), Gate: oneLineVisible(r.Gate), Release: oneLineVisible(r.Release),
			Decision: oneLineVisible(r.Decision), Approver: oneLineVisible(r.Approver), Date: oneLineVisible(r.Date)}
		for _, x := range r.Authorizes {
			rel.Authorizes = append(rel.Authorizes, oneLineVisible(x))
		}
		out = append(out, rel)
	}
	return out
}

// orgView lee los ledgers de los proyectos de la organización que tienen
// clon en esta máquina.
func (a *app) orgView(h *hub.Hub) (*web.Org, error) {
	org := &web.Org{Name: h.Conf.Org, Hub: h.String(), MonthlyUSD: h.Conf.Budgets.MonthlyUSD}
	for _, p := range h.Conf.Projects {
		row := web.OrgProject{Name: p.Name, State: "ok"}
		dir := h.ProjectDir(p)
		if dir == "" {
			row.State = "sin ruta en hub.yaml"
			org.Projects = append(org.Projects, row)
			continue
		}
		cfg, err := project.Load(dir)
		switch {
		case errors.Is(err, project.ErrNotProject):
			row.State = "sin clon en esta máquina"
		case err != nil:
			row.State = "no se lee: " + shortText(err.Error(), 80)
		default:
			row.MonthlyUSD = cfg.Budgets.MonthlyUSD
			entries, _, err := ledger.Open(dir).ReadAll()
			if err != nil {
				row.State = "ledger ilegible: " + shortText(err.Error(), 80)
				break
			}
			evs := usage.FromLedger(p.Name, entries)
			for i := range evs {
				evs[i].Repo = p.Name // el ledger de un proyecto no atribuye consumo a otro
			}
			org.Events = append(org.Events, evs...)
		}
		org.Projects = append(org.Projects, row)
	}
	return org, nil
}

// sloViews se llena con coyote/slo (ADR-0020).
var sloViews = func(root string) ([]web.SLO, error) { return nil, nil }
