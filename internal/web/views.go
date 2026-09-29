package web

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"time"

	"github.com/Emmanuel93/coyote/internal/usage"
)

var months = []string{"enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"}

// monthOf devuelve el mes (UTC): su inicio, el límite de lo que cuenta, su
// nombre, los días que van y los que tiene.
func monthOf(now time.Time) (start, end time.Time, name string, elapsed float64, days int) {
	start, end = usage.Month(now)
	days = start.AddDate(0, 1, 0).Add(-time.Nanosecond).Day()
	elapsed = math.Max(now.Sub(start).Hours()/24, 1)
	return start, end, fmt.Sprintf("%s de %d", months[now.Month()-1], now.Year()), elapsed, days
}

// budgetData es la página de presupuesto del mes en curso.
type budgetData struct {
	Month                         string
	DaysElapsed, DaysInMonth      int
	Spent, Mine, Projection       float64
	Cap, RunCap, OrgCap, OrgSpent float64
	OrgKnown                      bool
	State, StateClass             string
	Workstreams                   []workView
}

// capState dice cómo va un gasto contra su tope.
func capState(spent, projection, cap float64) (string, string) {
	switch {
	case cap <= 0:
		return "sin tope declarado", ""
	case spent >= cap:
		return "pasó el tope", "bad"
	case projection > cap:
		return "a este ritmo pasa el tope antes de fin de mes", "warn"
	case spent >= 0.8*cap:
		return "cerca del tope", "warn"
	}
	return "dentro del tope", "ok"
}

func (s *Server) budget(w http.ResponseWriter, r *http.Request) {
	if !only(w, r, "/presupuesto") {
		return
	}
	now := s.now()
	start, end, name, elapsed, days := monthOf(now)
	d := budgetData{Month: name, DaysElapsed: int(math.Ceil(elapsed)), DaysInMonth: days}
	if s.Budget != nil {
		b, err := s.Budget()
		if err != nil {
			s.message(w, "/presupuesto", http.StatusInternalServerError, "No se pudo leer el presupuesto", err.Error())
			return
		}
		d.Cap, d.RunCap, d.OrgCap, d.Spent, d.Mine = b.MonthlyUSD, b.RunUSD, b.OrgMonthlyUSD, b.SpentUSD, b.MineUSD
	} else {
		// Sin fuente de presupuesto, el gasto sale de los eventos que se ven.
		events, err := s.events()
		if err != nil {
			s.message(w, "/presupuesto", http.StatusInternalServerError, "No se pudo leer el ledger", err.Error())
			return
		}
		month := usage.Between(events, start, end)
		d.Spent = usage.Cost(month)
		for _, e := range month {
			if usage.Person(e.Line.Actor) == s.Viewer {
				d.Mine += costOf(e)
			}
		}
	}
	d.Projection = d.Spent / elapsed * float64(days)
	d.State, d.StateClass = capState(d.Spent, d.Projection, d.Cap)
	if s.OrgAdmin && s.Org != nil {
		if org, err := s.Org(); err == nil && org != nil {
			d.OrgSpent, d.OrgKnown = usage.Cost(usage.Between(org.Events, start, end)), true
		}
	}
	if s.Work != nil {
		if ws, err := s.Work(); err == nil {
			for _, x := range ws {
				if !x.Closed && (x.BudgetUSD > 0 || x.SpentUSD > 0) {
					d.Workstreams = append(d.Workstreams, s.workView(x))
				}
			}
		}
	}
	s.render(w, "budget", "/presupuesto", d)
}

// workView cuenta los pasos de un plan y decide si quien mira ve sus costos:
// un plan suele ser de una persona, así que su gasto lo ven su dueño y los
// admins (D10).
func (s *Server) workView(x Workstream) workView {
	v := workView{Workstream: x, CostsVisible: s.Admin || (x.Owner != "" && x.Owner == s.Viewer)}
	for _, st := range x.Steps {
		switch st.Status {
		case "done":
			v.Done++
		case "review":
			v.Review++
		case "failed", "blocked", "redo":
			v.Trouble++
		default:
			v.Pending++
		}
	}
	return v
}

func costOf(e usage.Event) float64 {
	if e.Line.Cost == nil {
		return 0
	}
	return e.Line.Cost.In + e.Line.Cost.Out
}

// workData es la página de workstreams.
type workData struct {
	Workstreams []workView
}

type workView struct {
	Workstream
	Done, Review, Pending, Trouble int
	CostsVisible                   bool
}

func (s *Server) work(w http.ResponseWriter, r *http.Request) {
	if !only(w, r, "/workstreams") {
		return
	}
	var d workData
	if s.Work != nil {
		ws, err := s.Work()
		if err != nil {
			s.message(w, "/workstreams", http.StatusInternalServerError, "No se pudieron leer los workstreams", err.Error())
			return
		}
		for _, x := range ws {
			d.Workstreams = append(d.Workstreams, s.workView(x))
		}
		// Los abiertos primero, del más nuevo al más viejo.
		sort.SliceStable(d.Workstreams, func(i, j int) bool {
			a, b := d.Workstreams[i], d.Workstreams[j]
			if a.Closed != b.Closed {
				return !a.Closed
			}
			return a.ID > b.ID
		})
	}
	s.render(w, "work", "/workstreams", d)
}

func (s *Server) gate(w http.ResponseWriter, r *http.Request) {
	if !only(w, r, "/gate") {
		return
	}
	var g Gate
	if s.Gate != nil {
		var err error
		if g, err = s.Gate(); err != nil {
			s.message(w, "/gate", http.StatusInternalServerError, "No se pudo leer la cola del gate", err.Error())
			return
		}
	}
	sort.SliceStable(g.Releases, func(i, j int) bool { return g.Releases[i].ID > g.Releases[j].ID })
	s.render(w, "gate", "/gate", g)
}

func (s *Server) slo(w http.ResponseWriter, r *http.Request) {
	if !only(w, r, "/slo") {
		return
	}
	var slos []SLO
	if s.SLOs != nil {
		var err error
		if slos, err = s.SLOs(); err != nil {
			s.message(w, "/slo", http.StatusInternalServerError, "No se pudieron leer los SLOs", err.Error())
			return
		}
	}
	s.render(w, "slo", "/slo", slos)
}

// orgData es la vista de la organización, solo para admins.
type orgData struct {
	*Org
	Month      string
	Spent      float64
	Projects   []orgRow
	People     []usage.Row
	State      string
	StateClass string
}

type orgRow struct {
	OrgProject
	Spent  float64
	Events int
}

func (s *Server) org(w http.ResponseWriter, r *http.Request) {
	if !only(w, r, "/org") {
		return
	}
	if s.Org == nil {
		s.message(w, "/org", http.StatusNotFound, "Sin hub", "Este proyecto no declara el hub de la organización (ADR-0018).")
		return
	}
	if !s.OrgAdmin {
		s.message(w, "/org", http.StatusForbidden, "Solo para admins del hub",
			"La vista de la organización muestra el gasto de todas las personas en todos sus proyectos: la ven los admins de hub.yaml (D10).")
		return
	}
	org, err := s.Org()
	if err != nil || org == nil {
		msg := "sin datos"
		if err != nil {
			msg = err.Error()
		}
		s.message(w, "/org", http.StatusInternalServerError, "No se pudo leer la organización", msg)
		return
	}
	start, end, name, elapsed, days := monthOf(s.now())
	month := usage.Between(org.Events, start, end)
	d := orgData{Org: org, Month: name, Spent: usage.Cost(month)}
	d.State, d.StateClass = capState(d.Spent, d.Spent/elapsed*float64(days), org.MonthlyUSD)
	byProject := map[string]*orgRow{}
	for _, p := range org.Projects {
		row := &orgRow{OrgProject: p}
		byProject[p.Name] = row
	}
	for _, e := range month {
		if row := byProject[e.Repo]; row != nil {
			row.Spent += costOf(e)
			row.Events++
		}
	}
	for _, p := range org.Projects {
		d.Projects = append(d.Projects, *byProject[p.Name])
	}
	d.People, _ = usage.Group(month, usage.ByPerson, usage.ByRepo)
	s.render(w, "org", "/org", d)
}
