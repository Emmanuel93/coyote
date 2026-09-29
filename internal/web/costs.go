package web

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/Emmanuel93/coyote/internal/usage"
)

// costsData es la página de costos. Con D10, una persona que no es admin ve
// sus eventos y, aparte, los totales del proyecto sin desglose.
type costsData struct {
	View, Since, PeriodLabel, Heading string
	All                               usage.Totals // lo que muestra la tabla
	Project                           usage.Totals // todas las personas, sin desglose
	Mine                              bool         // la tabla es solo de quien mira
	Rows                              []usage.Row
	Max                               float64
	Views, Periods                    []option
}

// since devuelve el inicio del periodo de ?since=; cero es todo.
func since(key string, now time.Time) time.Time {
	switch key {
	case "7d":
		return now.AddDate(0, 0, -7)
	case "30d":
		return now.AddDate(0, 0, -30)
	case "90d":
		return now.AddDate(0, 0, -90)
	}
	return time.Time{}
}

// visible deja los eventos que quien mira puede ver con desglose (D10).
func (s *Server) visible(events []usage.Event) []usage.Event {
	if s.Admin {
		return events
	}
	out := make([]usage.Event, 0, len(events))
	for _, e := range events {
		if usage.Person(e.Line.Actor) == s.Viewer {
			out = append(out, e)
		}
	}
	return out
}

// events lee el ledger; sin fuente no hay eventos.
func (s *Server) events() ([]usage.Event, error) {
	if s.Load == nil {
		return nil, nil
	}
	return s.Load()
}

func total(events []usage.Event) usage.Totals {
	_, all := usage.Group(events, usage.ByRepo, usage.ByRepo)
	return all
}

func (s *Server) buildCosts(r *http.Request) (*costsData, error) {
	view := r.URL.Query().Get("view")
	if !valid(views, view) {
		view = "project"
	}
	per := r.URL.Query().Get("since")
	if !valid(periods, per) {
		per = "30d"
	}
	events, err := s.events()
	if err != nil {
		return nil, err
	}
	events = usage.Between(events, since(per, s.now()), s.now().Add(time.Minute))
	d := &costsData{View: view, Since: per, Views: views, Periods: periods, Project: total(events), Mine: !s.Admin}
	if d.Mine {
		// Los totales del proyecto van sin desglose: tampoco dicen qué
		// modelos usaron las demás personas.
		d.Project.Models = nil
	}
	events = s.visible(events)
	outer, inner, heading := usage.ByRepo, usage.ByPerson, "Proyecto › persona"
	switch view {
	case "person":
		outer, inner, heading = usage.ByPerson, usage.ByRepo, "Persona › proyecto"
	case "model":
		outer, inner, heading = usage.ByModel, usage.ByPerson, "Modelo › persona"
	case "agent":
		outer, inner, heading = usage.ByAgent, usage.ByRepo, "Agente › proyecto"
	}
	d.Heading = heading
	d.Rows, d.All = usage.Group(events, outer, inner)
	if view == "model" || view == "agent" {
		// Estas vistas son de consumo: los eventos sin tokens ni costo (notas,
		// aprobaciones) no tienen modelo y solo agregarían ruido.
		kept := d.Rows[:0]
		for _, row := range d.Rows {
			if row.Tokens.In+row.Tokens.Out > 0 || row.CostTotal() > 0 {
				kept = append(kept, row)
			}
		}
		d.Rows = kept
	}
	for _, p := range periods {
		if p.Key == per {
			d.PeriodLabel = p.Label
		}
	}
	for _, row := range d.Rows {
		v := row.CostTotal()
		if v == 0 {
			v = float64(row.Tokens.In + row.Tokens.Out)
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

func (s *Server) costs(w http.ResponseWriter, r *http.Request) {
	if !only(w, r, "/") {
		return
	}
	d, err := s.buildCosts(r)
	if err != nil {
		s.message(w, "/", http.StatusInternalServerError, "No se pudo leer el ledger", err.Error())
		return
	}
	s.render(w, "costs", "/", d)
}

func (s *Server) api(w http.ResponseWriter, r *http.Request) {
	d, err := s.buildCosts(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	scope := "todas las personas"
	if d.Mine {
		scope = s.Viewer
	}
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(map[string]any{"view": d.View, "since": d.Since, "viewer": s.Viewer, "scope": scope,
		"totals": d.All, "project_totals": d.Project, "rows": d.Rows})
}
