package slo

import (
	"bytes"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// windows son las ventanas del SLI, de la más corta a la más larga; el
// periodo se agrega al final.
var windows = []string{"5m", "30m", "1h", "2h", "6h", "1d", "3d"}

var windowHours = map[string]float64{"5m": 5.0 / 60, "30m": 0.5, "1h": 1, "2h": 2, "6h": 6, "1d": 24, "3d": 72}

// burn es una condición de alerta: la tasa de consumo que gasta Budget del
// presupuesto del periodo en Long, confirmada en Short (libro de SRE de
// Google, alertas de varias ventanas y varias tasas).
type burn struct {
	Short, Long string
	Budget      float64 // parte del presupuesto del periodo
}

var (
	pageBurns   = []burn{{"5m", "1h", 0.02}, {"30m", "6h", 0.05}}
	ticketBurns = []burn{{"2h", "1d", 0.10}, {"6h", "3d", 0.10}}
)

// Factor es la tasa de consumo que gasta b.Budget del periodo en b.Long:
// con 30 días, 14.4, 6, 3 y 1.
func (b burn) Factor(periodDays int) float64 {
	return round(b.Budget*float64(periodDays)*24/windowHours[b.Long], 6)
}

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}

// num escribe un número para PromQL sin ruido de coma flotante.
func num(v float64) string {
	return strconv.FormatFloat(round(v, 12), 'f', -1, 64)
}

// ID identifica un SLO en las reglas: servicio-nombre.
func (s *Spec) ID(o SLO) string { return s.Service + "-" + o.Name }

// ErrorBudget es la proporción de eventos que pueden fallar.
func ErrorBudget(o SLO) float64 { return round((100-o.Objective)/100, 12) }

// AlertName es el nombre de la alerta: el declarado o el servicio y el SLO en
// CamelCase.
func (s *Spec) AlertName(o SLO) string {
	if o.Alerts.Name != "" {
		return o.Alerts.Name
	}
	var b strings.Builder
	for _, part := range strings.FieldsFunc(s.Service+"-"+o.Name, func(r rune) bool { return r == '-' }) {
		b.WriteString(strings.ToUpper(part[:1]) + part[1:])
	}
	name := b.String()
	if name != "" && name[0] >= '0' && name[0] <= '9' {
		name = "SLO" + name
	}
	return name
}

type rule struct {
	Record      string            `yaml:"record,omitempty"`
	Alert       string            `yaml:"alert,omitempty"`
	Expr        string            `yaml:"expr"`
	For         string            `yaml:"for,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty"`
}

type group struct {
	Name     string `yaml:"name"`
	Interval string `yaml:"interval,omitempty"`
	Rules    []rule `yaml:"rules"`
}

// Header encabeza el archivo generado.
func Header(service string) string {
	return fmt.Sprintf("# Generado por coyote slo rules desde %s/%s.yaml (ADR-0020). No lo edites: cambia el SLO y vuelve a generar.\n# Reglas de Prometheus para el ruler de Mimir o de Prometheus; se cargan con mimirtool rules o con la configuración del ruler.\n", Dir, service)
}

func merge(ms ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, m := range ms {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

// selector filtra las series de un SLO por sus etiquetas de coyote.
func selector(id, service, name string) string {
	return fmt.Sprintf(`{slo_id=%q, slo_service=%q, slo_name=%q}`, id, service, name)
}

// Rules genera el archivo de reglas de Prometheus de un servicio. La salida
// es determinista: el mismo archivo de SLOs da los mismos bytes.
func Rules(s *Spec) ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	days := s.Days()
	period := fmt.Sprintf("%dd", days)
	var groups []group
	for _, o := range s.SLOs {
		id := s.ID(o)
		base := merge(s.Labels, map[string]string{"slo_id": id, "slo_service": s.Service, "slo_name": o.Name})
		sel := selector(id, s.Service, o.Name)
		budget := num(ErrorBudget(o))

		sli := group{Name: "slo-" + id + "-sli"}
		for _, w := range windows {
			sli.Rules = append(sli.Rules, rule{Record: "slo:sli_error:ratio_rate" + w, Expr: sliExpr(o.SLI, w),
				Labels: merge(base, map[string]string{"slo_window": w})})
		}
		// El periodo se promedia sobre la ventana de 5m: evaluar la consulta
		// sobre 30 días en cada ciclo sería caro.
		sli.Rules = append(sli.Rules, rule{Record: "slo:sli_error:ratio_rate" + period,
			Expr:   fmt.Sprintf("sum_over_time(slo:sli_error:ratio_rate5m%s[%s])\n/ ignoring (slo_window)\ncount_over_time(slo:sli_error:ratio_rate5m%s[%s])", sel, period, sel, period),
			Labels: merge(base, map[string]string{"slo_window": period})})

		on := "on (slo_id, slo_service, slo_name) group_left"
		meta := group{Name: "slo-" + id + "-meta", Interval: "5m", Rules: []rule{
			{Record: "slo:objective:ratio", Expr: "vector(" + num(o.Objective/100) + ")", Labels: base},
			{Record: "slo:error_budget:ratio", Expr: "vector(" + budget + ")", Labels: base},
			{Record: "slo:time_period:days", Expr: "vector(" + strconv.Itoa(days) + ")", Labels: base},
			{Record: "slo:current_burn_rate:ratio", Expr: fmt.Sprintf("slo:sli_error:ratio_rate5m%s\n/ %s\nslo:error_budget:ratio%s", sel, on, sel), Labels: base},
			{Record: "slo:period_burn_rate:ratio", Expr: fmt.Sprintf("slo:sli_error:ratio_rate%s%s\n/ %s\nslo:error_budget:ratio%s", period, sel, on, sel), Labels: base},
			{Record: "slo:period_error_budget_remaining:ratio", Expr: fmt.Sprintf("1 - slo:period_burn_rate:ratio%s", sel), Labels: base},
		}}

		alerts := group{Name: "slo-" + id + "-alerts"}
		name := s.AlertName(o)
		for _, sev := range []struct {
			key   string
			a     Alert
			burns []burn
		}{{"page", o.Alerts.Page, pageBurns}, {"ticket", o.Alerts.Ticket, ticketBurns}} {
			if sev.a.Disable {
				continue
			}
			ann := merge(map[string]string{
				"summary":     fmt.Sprintf("%s %s: el presupuesto de error se consume demasiado rápido", s.Service, o.Name),
				"description": fmt.Sprintf("Objetivo %s %% en %d días. %s", num(o.Objective), days, strings.TrimSpace(o.Description)),
			}, o.Alerts.Annotations, sev.a.Annotations)
			ann["description"] = strings.TrimSpace(ann["description"])
			if rb := strings.TrimSpace(o.Alerts.Runbook); rb != "" {
				ann["runbook_url"] = rb
			}
			alerts.Rules = append(alerts.Rules, rule{Alert: name, Expr: alertExpr(sel, budget, days, sev.burns),
				Labels:      merge(s.Labels, map[string]string{"slo_id": id, "slo_service": s.Service, "slo_name": o.Name}, o.Alerts.Labels, sev.a.Labels, map[string]string{"slo_severity": sev.key}),
				Annotations: ann})
		}
		groups = append(groups, sli, meta)
		if len(alerts.Rules) > 0 {
			groups = append(groups, alerts)
		}
	}
	var buf bytes.Buffer
	buf.WriteString(Header(s.Service))
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(struct {
		Groups []group `yaml:"groups"`
	}{groups}); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func sliExpr(q SLI, w string) string {
	fill := func(s string) string { return strings.TrimSpace(strings.ReplaceAll(s, Window, w)) }
	if q.ErrorRatio != "" {
		return "(" + fill(q.ErrorRatio) + ")"
	}
	return "(" + fill(q.Errors) + ")\n/\n(" + fill(q.Total) + ")"
}

func alertExpr(sel, budget string, days int, burns []burn) string {
	parts := make([]string, 0, len(burns))
	for _, b := range burns {
		f := num(b.Factor(days))
		parts = append(parts, fmt.Sprintf("(\n  max(slo:sli_error:ratio_rate%s%s > (%s * %s)) without (slo_window)\n  and\n  max(slo:sli_error:ratio_rate%s%s > (%s * %s)) without (slo_window)\n)",
			b.Short, sel, f, budget, b.Long, sel, f, budget))
	}
	return strings.Join(parts, "\nor\n")
}

// Summary describe las alertas de un SLO para la web y los reportes.
type Summary struct {
	Service, Name      string
	Objective          float64
	PeriodDays         int
	Page, Ticket       bool
	Runbook            string
	PageFactors        []float64
	TicketFactors      []float64
	ErrorBudgetMinutes float64
}

// Summaries resume los SLOs de un archivo.
func Summaries(s *Spec) []Summary {
	var out []Summary
	for _, o := range s.SLOs {
		sm := Summary{Service: s.Service, Name: o.Name, Objective: o.Objective, PeriodDays: s.Days(),
			Page: !o.Alerts.Page.Disable, Ticket: !o.Alerts.Ticket.Disable, Runbook: strings.TrimSpace(o.Alerts.Runbook),
			ErrorBudgetMinutes: ErrorBudget(o) * float64(s.Days()) * 24 * 60}
		for _, b := range pageBurns {
			sm.PageFactors = append(sm.PageFactors, b.Factor(s.Days()))
		}
		for _, b := range ticketBurns {
			sm.TicketFactors = append(sm.TicketFactors, b.Factor(s.Days()))
		}
		out = append(out, sm)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
