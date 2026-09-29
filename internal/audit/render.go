package audit

import (
	"fmt"
	"strings"
	"time"

	"github.com/Emmanuel93/coyote/internal/ccf"
)

// Control es un control de un marco y lo que el reporte aporta como evidencia.
type Control struct {
	Framework, ID, Name, Evidence string
}

// Controls son los controles que el reporte apoya. Solo los que tienen
// evidencia en él: ISO/IEC 42001:2023, Anexo A, y NIST AI RMF 1.0.
var Controls = []Control{
	{"ISO/IEC 42001", "A.6.2.8", "registro de eventos del sistema de IA", "Eventos e Integridad: cada acción, aprobación y costo, en un ledger que solo agrega"},
	{"ISO/IEC 42001", "A.3.2", "roles y responsabilidades", "Aprobaciones: quién decide cada gate y cada acción, y que un agente no decide"},
	{"ISO/IEC 42001", "A.9.2", "procesos para el uso responsable", "Gate: negaciones, cola y concesiones de autonomía con su límite"},
	{"ISO/IEC 42001", "A.6.2.6", "operación y monitoreo", "Costo frente al presupuesto, por proyecto, persona, agente y modelo"},
	{"ISO/IEC 42001", "A.4.4", "recursos de herramientas", "Agentes y modelos usados en el periodo"},
	{"NIST AI RMF", "GOVERN", "responsabilidades y rendición de cuentas", "Aprobaciones y actores; firmas de los commits"},
	{"NIST AI RMF", "MEASURE", "seguimiento de riesgos y desempeño", "Costo, negaciones y verificaciones de integridad"},
	{"NIST AI RMF", "MANAGE", "respuesta a riesgos", "Topes, cola del gate y decisiones de cada gate de release"},
}

// Limits es lo que el reporte no puede probar.
var Limits = []string{
	"La identidad de cada actor es la que declara la máquina; solo un commit firmado la prueba.",
	"Lo que un agente hizo por fuera de coyote no está en el ledger.",
	"El costo es la estimación de Claude Code, no la factura.",
	"Una aprobación del gate vale solo en la máquina que la firmó; el reporte ve su registro, no la firma.",
	"Es evidencia para una auditoría, no una certificación (ADR-0024).",
}

var months = []string{"enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"}

// Period describe el periodo en palabras.
func (r *Report) Period() string {
	if r.To.Sub(r.From) < 32*24*time.Hour && r.From.Day() == 1 {
		return fmt.Sprintf("%s de %d", months[r.From.Month()-1], r.From.Year())
	}
	return r.From.Format("2006-01-02") + " a " + r.To.Format("2006-01-02")
}

func usd(v float64) string { return "$" + strings.TrimPrefix(ccf.FormatUSD(v), "$") }

// Markdown devuelve el reporte para personas.
func (r *Report) Markdown() string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	fails, warns := 0, 0
	for _, c := range r.Checks {
		switch c.Result {
		case Fail:
			fails++
		case Warn:
			warns++
		}
	}
	w("# Auditoría de %s · %s\n\n", r.Project, r.Period())
	w("Generada por `coyote audit` el %s desde el ledger, `coyote/approvals/` y la historia de git. Es evidencia para una auditoría, no una certificación (ADR-0024).\n\n", r.Generated.Format("2006-01-02 15:04Z"))
	w("| Eventos | Personas | Agentes | Costo | Tope del mes | Integridad |\n| --- | --- | --- | --- | --- | --- |\n")
	cap := "sin tope"
	if r.MonthlyUSD > 0 {
		cap = usd(r.MonthlyUSD)
	}
	w("| %d | %d | %d | %s | %s | %d fallas · %d avisos |\n\n", r.Events, len(r.People), len(r.Agents), usd(r.CostUSD), cap, fails, warns)

	w("## Eventos\n\n")
	w("| Tipo | Eventos | Costo |\n| --- | --- | --- |\n")
	for _, c := range r.ByType {
		w("| %s | %d | %s |\n", c.Key, c.Events, usd(c.Cost))
	}
	w("\n| Actor | Eventos | Costo |\n| --- | --- | --- |\n")
	for _, c := range r.ByActor {
		w("| %s | %d | %s |\n", c.Key, c.Events, usd(c.Cost))
	}

	w("\n## Aprobaciones y gate\n\n")
	w("- Aprobaciones registradas: %d. Rechazos: %d.\n", r.Approved, r.Rejected)
	w("- Gate: %d usos de aprobaciones, %d acciones en la cola y %d bloqueadas.\n", r.GateUses, r.GateQueued, r.GateDenied)
	if len(r.Grants) == 0 {
		w("- Concesiones (varios usos o más de una hora): ninguna.\n")
	} else {
		w("- Concesiones (varios usos o más de una hora):\n")
		for _, g := range r.Grants {
			w("  - %s\n", g)
		}
	}
	if len(r.Autonomous) == 0 {
		w("- Planes `autonomous` autorizados: ninguno.\n")
	} else {
		w("- Planes `autonomous` autorizados:\n")
		for _, a := range r.Autonomous {
			w("  - %s\n", a)
		}
	}
	if len(r.Releases) > 0 {
		w("\n| Gate | Release | Decisión | Aprobó | Fecha | Registró |\n| --- | --- | --- | --- | --- | --- |\n")
		for _, x := range r.Releases {
			w("| %s (%s) | %s | %s | %s | %s | %s |\n", x.Gate, x.ID, x.Release, x.Decision, x.Approver, x.Date, orDash(x.RecordedBy))
		}
	}

	w("\n## Costo\n\n")
	if r.CostUSD == 0 {
		w("Sin costo registrado en el periodo.\n")
	} else {
		for _, t := range []struct {
			title string
			rows  []Count
		}{{"Persona", r.ByPerson}, {"Agente", r.ByAgent}, {"Modelo", r.ByModel}, {"Workstream", r.ByWS}} {
			w("\n| %s | Eventos con costo | Costo |\n| --- | --- | --- |\n", t.title)
			for _, c := range t.rows {
				w("| %s | %d | %s |\n", c.Key, c.Events, usd(c.Cost))
			}
		}
	}

	w("\n## Integridad\n\n| Verificación | Resultado |\n| --- | --- |\n")
	for _, c := range r.Checks {
		w("| %s | %s |\n", c.Name, c.Result)
	}
	if r.Commits > 0 {
		w("| commits firmados | %d de %d |\n", r.Signed, r.Commits)
	}
	for _, c := range r.Checks {
		if len(c.Detail) == 0 {
			continue
		}
		w("\n**%s** (%s):\n\n", c.Name, c.Result)
		for _, d := range c.Detail {
			w("- %s\n", d)
		}
	}

	w("\n## Controles\n\n| Marco | Control | Evidencia en este reporte |\n| --- | --- | --- |\n")
	for _, c := range Controls {
		w("| %s | %s %s | %s |\n", c.Framework, c.ID, c.Name, c.Evidence)
	}
	w("\n## Lo que este reporte no prueba\n\n")
	for _, l := range Limits {
		w("- %s\n", l)
	}
	return b.String()
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
