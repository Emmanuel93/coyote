package audit

import (
	"strings"
	"testing"
	"time"

	"github.com/Emmanuel93/coyote/internal/ccf"
	"github.com/Emmanuel93/coyote/internal/ledger"
)

func entry(ts, actor, typ, what string, refs []string, cost float64) ledger.Entry {
	t, _ := time.Parse(time.RFC3339, ts)
	l := ccf.Line{TS: t, Actor: actor, Project: "W-0001", Repo: "tienda", Type: typ, Scope: "-", What: what, Refs: refs, Status: "ok"}
	if cost > 0 {
		l.Cost = &ccf.Cost{In: cost / 2, Out: cost / 2}
		l.Refs = append(l.Refs, "model:sonnet-x")
	}
	return ledger.Entry{File: "/p/coyote/ledger/2026/09/28-ana.ccf", LineNo: 2, Line: l}
}

func pend(e ledger.Entry) ledger.Entry {
	e.Line.Status = "pend"
	return e
}

func input() Input {
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	return Input{Project: "tienda", Root: "/p", From: from, To: from.AddDate(0, 1, 0), Now: time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC),
		MonthlyUSD: 30,
		Entries: []ledger.Entry{
			entry("2026-09-02T10:00:00Z", "@ana/coyote-dev", "run", "implementa pedidos", nil, 0.4),
			entry("2026-09-02T11:00:00Z", "@ana", "apr", "G1 aprobado: v0.1.0", []string{"apr:P-0001"}, 0),
			entry("2026-09-03T11:00:00Z", "@ana/coyote-scribe", "apr", "G2 aprobado: v0.2.0", []string{"apr:P-0002"}, 0),
			entry("2026-09-04T11:00:00Z", "@ana/coyote-scribe", "apr", "G3 aprobado: v0.3.0", nil, 0),
			entry("2026-09-05T11:00:00Z", "@ana/coyote-dev", "apr", "aprobado: rm -rf build", nil, 0),
			pend(entry("2026-09-06T11:00:00Z", "@ana/coyote-dev", "gate", "en cola: Bash: make", []string{"prop:P-abc123"}, 0)),
			entry("2026-09-06T12:00:00Z", "@ana", "close", "W-0001 cerrado", nil, 0.4),
			entry("2026-08-30T12:00:00Z", "@ana/coyote-dev", "run", "fuera del periodo", nil, 5),
		},
		Approvals: []Approval{
			{ID: "P-0001", Gate: "G1", Release: "v0.1.0", Decision: "approved", Approver: "@ana", Date: "2026-09-02"},
			{ID: "P-0002", Gate: "G2", Release: "v0.2.0", Decision: "approved", Approver: "@ana", Date: "2026-09-03", Response: "sí, apruebo"},
			{ID: "P-0003", Gate: "G3", Release: "v0.3.0", Decision: "approved", Approver: "@ana", Date: "2026-09-04", Response: "sí"},
			{ID: "P-0004", Gate: "G4", Release: "v0.4.0", Decision: "approved", Approver: "@ana", Date: "2026-09-20", Response: "sí"},
			{ID: "P-abc999", Type: "action", Object: "Bash: make test", Uses: 5, TS: "2026-09-10T10:00:00Z", Expires: "2026-09-11T10:00:00Z", Approver: "@ana"},
		},
	}
}

func check(r *Report, name string) Check {
	for _, c := range r.Checks {
		if c.Name == name {
			return c
		}
	}
	return Check{}
}

func TestReporteDelPeriodo(t *testing.T) {
	r := Build(input())
	if r.Events != 7 || r.Approved != 4 || r.GateQueued != 1 {
		t.Fatalf("conteos: %d eventos, %d aprobaciones, %d en cola", r.Events, r.Approved, r.GateQueued)
	}
	if r.CostUSD < 0.399 || r.CostUSD > 0.401 {
		t.Errorf("el costo no suma cierres ni lo de fuera del periodo: %v", r.CostUSD)
	}
	if len(r.Grants) != 1 || !strings.Contains(r.Grants[0], "P-abc999") {
		t.Errorf("concesiones: %v", r.Grants)
	}
	c := check(r, "aprobaciones registradas por un agente")
	if c.Result != Fail || len(c.Detail) != 3 {
		t.Fatalf("un agente que registra una aprobación sin respuesta es falla: %+v", c)
	}
	joined := strings.Join(c.Detail, "\n")
	for _, want := range []string{"registró P-0002: la respuesta", "registró P-0003: la respuesta", `registró "aprobado: rm -rf build" sin un registro`} {
		if !strings.Contains(joined, want) {
			t.Errorf("falta %q en %v", want, c.Detail)
		}
	}
	if c := check(r, "gates de release con su evento en el ledger"); c.Result != Warn || !strings.Contains(strings.Join(c.Detail, ""), "G4 (P-0004)") {
		t.Errorf("G4 no tiene evento: %+v", c)
	}
	if !r.Failed() {
		t.Error("Failed debe reflejar la falla")
	}
	md := r.Markdown()
	for _, want := range []string{"# Auditoría de tienda · septiembre de 2026", "| 7 | 1 | 2 | $0.4 | $30 |", "| G3 (P-0003) | v0.3.0 | approved | @ana | 2026-09-04 | @ana/coyote-scribe |",
		"A.6.2.8", "NIST AI RMF", "Lo que este reporte no prueba", "historia de git"} {
		if !strings.Contains(md, want) {
			t.Errorf("el reporte no tiene %q:\n%s", want, md)
		}
	}
}

func TestFechasFuturasYLineasInvalidas(t *testing.T) {
	in := input()
	in.Entries = append(in.Entries, entry("2026-10-15T00:00:00Z", "@ana", "cost", "gasto adelantado", nil, 0))
	in.Problems = []ledger.Problem{{File: "/p/coyote/ledger/2026/09/28-ana.ccf", LineNo: 9, Err: errString("tipo desconocido")}}
	r := Build(in)
	if check(r, "eventos con fecha futura").Result != Fail || check(r, "líneas inválidas del ledger").Result != Fail {
		t.Fatalf("fechas futuras y líneas inválidas son falla: %+v", r.Checks)
	}
	if d := check(r, "líneas inválidas del ledger").Detail; len(d) != 1 || !strings.HasPrefix(d[0], "coyote/ledger/2026/09/28-ana.ccf:9") {
		t.Errorf("detalle: %v", d)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// TestRespaldoUnoAUno: revisión adversarial de v1.0. Un registro de un gate
// respalda un solo evento, que nombra su gate, dice su decisión y no es
// anterior a ella.
func TestRespaldoUnoAUno(t *testing.T) {
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	ev := func(ts, typ, what string, refs ...string) ledger.Entry {
		e := entry(ts, "@ana/claude-code", typ, what, refs, 0)
		e.LineNo = len(ts) + len(what) // cada evento en su línea
		return e
	}
	in := Input{Project: "tienda", Root: "/p", From: from, To: from.AddDate(0, 1, 0), Now: from.AddDate(0, 0, 25),
		Entries: []ledger.Entry{
			ev("2026-09-01T10:00:00Z", "apr", "G1 aprobado: antes de decidir"),
			ev("2026-09-10T10:00:00Z", "apr", "despliegue a producción aprobado", "apr:P-0001"),
			ev("2026-09-10T11:00:00Z", "apr", "G1 aprobado: v0.1.0"),
			ev("2026-09-11T10:00:00Z", "apr", "G1 aprobado: borrar la base de producción"),
			ev("2026-09-12T10:00:00Z", "rej", "G1 rechazado por la persona"),
		},
		Approvals: []Approval{{ID: "P-0001", Gate: "G1", Release: "v0.1.0", Decision: "approved", Approver: "@ana", Date: "2026-09-10", Response: "sí, apruebo v0.1.0"}},
	}
	r := Build(in)
	c := check(r, "aprobaciones registradas por un agente")
	joined := strings.Join(c.Detail, "\n")
	if c.Result != Fail {
		t.Fatalf("se esperaba falla: %+v", c)
	}
	for _, want := range []string{"el evento es anterior a la decisión", "el evento no lo nombra al empezar", "registró P-0001: la respuesta de la persona",
		"P-0001 ya respalda el evento del 2026-09-10 11:00Z", "el registro no rechaza"} {
		if !strings.Contains(joined, want) {
			t.Errorf("falta %q en:\n%s", want, joined)
		}
	}
	if len(r.Releases) != 1 || r.Releases[0].RecordedBy != "@ana/claude-code" {
		t.Errorf("el gate lo registró el evento que lo respalda: %+v", r.Releases)
	}
}

// TestRegistroTardioYGatesDelPeriodo: un gate se registra tarde y cuenta para
// el mes de su decisión; el reporte de otro mes no lo lista.
func TestRegistroTardioYGatesDelPeriodo(t *testing.T) {
	sep := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	in := Input{Project: "tienda", Root: "/p", From: sep, To: sep.AddDate(0, 1, 0), Now: sep.AddDate(0, 1, 15),
		Entries:   []ledger.Entry{entry("2026-10-11T10:00:00Z", "@ana", "apr", "G2 aprobado: v0.2.0; registro tardío", []string{"apr:P-0002"}, 0)},
		Approvals: []Approval{{ID: "P-0002", Gate: "G2", Release: "v0.2.0", Decision: "approved", Approver: "@ana", Date: "2026-09-28", Response: "sí"}},
	}
	r := Build(in)
	if c := check(r, "gates de release con su evento en el ledger"); c.Result != OK || len(r.Releases) != 1 || r.Releases[0].RecordedBy != "@ana" {
		t.Errorf("el registro tardío cuenta para septiembre: %+v %+v", c, r.Releases)
	}
	in.From, in.To = sep.AddDate(0, 1, 0), sep.AddDate(0, 2, 0)
	if r := Build(in); len(r.Releases) != 0 {
		t.Errorf("octubre no lista un gate de septiembre: %+v", r.Releases)
	}
}

// TestReporteSinInyeccion: lo que viene de un registro no abre secciones,
// no parte tablas ni esconde texto.
func TestReporteSinInyeccion(t *testing.T) {
	in := input()
	in.Approvals = append(in.Approvals, Approval{ID: "P-0009", Gate: "G2 | x | y |\n\n## Integridad\n\n| el ledger solo agrega líneas | ok |",
		Release: "v0.9\u202e.0", Decision: "approved", Approver: "@ana", Date: "2026-09-15"})
	md := Build(in).Markdown()
	if n := strings.Count(md, "\n## Integridad"); n != 1 {
		t.Errorf("hay %d secciones de integridad:\n%s", n, md)
	}
	if strings.ContainsRune(md, 0x202e) || !strings.Contains(md, `v0.9\u202e.0`) {
		t.Errorf("el control de dirección debe salir escapado")
	}
	if !strings.Contains(md, `G2 \| x \| y \|`) {
		t.Errorf("las barras de un registro van escapadas en la tabla")
	}
}

// TestRespaldoSegundaRevision: un registro sin gate no respalda la decisión
// de un agente; un gate rechazado y después aprobado se vincula por nombre,
// cada evento con su registro.
func TestRespaldoSegundaRevision(t *testing.T) {
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	ev := func(ts, typ, what string, refs ...string) ledger.Entry {
		e := entry(ts, "@ana/coyote-scribe", typ, what, refs, 0)
		e.LineNo = len(ts) + len(what)
		return e
	}
	in := Input{Project: "tienda", Root: "/p", From: from, To: from.AddDate(0, 1, 0), Now: from.AddDate(0, 0, 25),
		Entries: []ledger.Entry{
			ev("2026-09-05T10:00:00Z", "apr", "renombrar la carpeta de docs", "apr:P-0009"),
			ev("2026-09-10T11:00:00Z", "rej", "G1 rechazado: falta la retro"),
			ev("2026-09-12T11:00:00Z", "apr", "G1 aprobado: v0.1.0"),
		},
		Approvals: []Approval{
			{ID: "P-0009", Decision: "approved", Approver: "@ana", Date: "2026-09-05", Response: "sí, renómbrala"},
			{ID: "P-0001", Gate: "G1", Release: "v0.1.0", Decision: "rejected", Approver: "@ana", Date: "2026-09-10", Response: "no todavía"},
			{ID: "P-0002", Gate: "G1", Release: "v0.1.0", Decision: "approved", Approver: "@ana", Date: "2026-09-12", Response: "sí"},
		},
	}
	c := check(Build(in), "aprobaciones registradas por un agente")
	joined := strings.Join(c.Detail, "\n")
	if c.Result != Fail || !strings.Contains(joined, "P-0009, que no es el registro de un gate") {
		t.Errorf("un registro sin gate no respalda a un agente: %+v", c)
	}
	for _, want := range []string{"registró P-0001: la respuesta", "registró P-0002: la respuesta"} {
		if !strings.Contains(joined, want) {
			t.Errorf("falta %q en:\n%s", want, joined)
		}
	}
}

// TestAgenteNoLeQuitaElRegistroALaPersona: tercera revisión. Un agente que
// registra antes que la persona el mismo gate no se queda con su registro, y
// una cita explícita no desplaza a un vínculo por nombre que tiene otro.
func TestAgenteNoLeQuitaElRegistroALaPersona(t *testing.T) {
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	ev := func(ts, actor, what string, refs ...string) ledger.Entry {
		e := entry(ts, actor, "apr", what, refs, 0)
		e.LineNo = len(ts) + len(what) + len(actor)
		return e
	}
	in := Input{Project: "tienda", Root: "/p", From: from, To: from.AddDate(0, 1, 0), Now: from.AddDate(0, 0, 25),
		Entries: []ledger.Entry{
			ev("2026-09-10T08:59:00Z", "@ana/coyote-scribe", "G4 aprobado: despliegue a producción sin pruebas"),
			ev("2026-09-10T09:00:00Z", "@ana", "G4 aprobado: despliegue a staging"),
			ev("2026-09-20T09:00:00Z", "@ana", "G5 aprobado: release 1.2"),
			ev("2026-09-20T10:00:00Z", "@ana/coyote-scribe", "G5 aprobado: release 1.2", "apr:P-0003"),
		},
		Approvals: []Approval{
			{ID: "P-0002", Gate: "G4", Release: "1.1", Decision: "approved", Approver: "@ana", Date: "2026-09-10", Response: "sí, a staging"},
			{ID: "P-0003", Gate: "G5", Release: "1.2", Decision: "approved", Approver: "@ana", Date: "2026-09-01", Response: "sí"},
			{ID: "P-0004", Gate: "G5", Release: "1.2", Decision: "approved", Approver: "@ana", Date: "2026-09-20", Response: "sí"},
		},
	}
	r := Build(in)
	c := check(r, "aprobaciones registradas por un agente")
	joined := strings.Join(c.Detail, "\n")
	if c.Result != Fail || !strings.Contains(joined, "despliegue a producción sin pruebas") || !strings.Contains(joined, "registró P-0003: la respuesta") {
		t.Errorf("el agente no se queda con el registro de la persona:\n%s", joined)
	}
	if c := check(r, "gates de release con su evento en el ledger"); c.Result != OK {
		t.Errorf("cada gate tiene su evento: %+v", c)
	}
}

// TestRespaldoCuartaPasada: cuarta pasada. Una cita no adelanta a un evento
// en el orden de los respaldos: el registro de un gate es del primer evento
// que lo nombra, una persona antes que un agente, y el que llega después y
// lo cita no se lo lleva.
func TestRespaldoCuartaPasada(t *testing.T) {
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	ev := func(ts, actor, what string, refs ...string) ledger.Entry {
		e := entry(ts, actor, "apr", what, refs, 0)
		e.LineNo = len(ts) + len(what) + len(actor) + len(refs)
		return e
	}
	in := Input{Project: "tienda", Root: "/p", From: from, To: from.AddDate(0, 1, 0), Now: from.AddDate(0, 0, 25),
		Entries: []ledger.Entry{
			// Dos agentes: el primero lo nombra, el segundo lo cita después.
			ev("2026-09-20T10:00:00Z", "@ana/coyote-scribe", "G5 aprobado: release 1.2"),
			ev("2026-09-20T11:00:00Z", "@ana/claude-code", "G5 aprobado: release 1.2", "apr:P-0004"),
			// Un agente que cita antes que la persona que lo nombra.
			ev("2026-09-21T08:59:00Z", "@ana/coyote-scribe", "G6 aprobado: release 1.3", "apr:P-0005"),
			ev("2026-09-21T09:00:00Z", "@ana", "G6 aprobado: release 1.3"),
		},
		Approvals: []Approval{
			{ID: "P-0004", Gate: "G5", Release: "1.2", Decision: "approved", Approver: "@ana", Date: "2026-09-20", Response: "sí"},
			{ID: "P-0005", Gate: "G6", Release: "1.3", Decision: "approved", Approver: "@ana", Date: "2026-09-21", Response: "sí"},
		},
	}
	r := Build(in)
	c := check(r, "aprobaciones registradas por un agente")
	joined := strings.Join(c.Detail, "\n")
	if c.Result != Fail {
		t.Fatalf("se esperaba falla: %+v", c)
	}
	for _, want := range []string{
		"2026-09-20 10:00Z @ana/coyote-scribe registró P-0004: la respuesta de la persona",
		"2026-09-20 11:00Z @ana/claude-code registró \"G5 aprobado: release 1.2\" sin un registro que lo respalde: P-0004 ya respalda el evento del 2026-09-20 10:00Z",
		"2026-09-21 08:59Z @ana/coyote-scribe registró \"G6 aprobado: release 1.3\" sin un registro que lo respalde: P-0005 ya respalda el evento del 2026-09-21 09:00Z",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("falta %q en:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "2026-09-20 10:00Z @ana/coyote-scribe registró \"") {
		t.Errorf("el primero que nombra el gate se queda con su registro:\n%s", joined)
	}
	for _, x := range r.Releases {
		if x.ID == "P-0005" && x.RecordedBy != "@ana" {
			t.Errorf("el gate G6 lo registró la persona, no %s", x.RecordedBy)
		}
		if x.ID == "P-0004" && x.RecordedBy != "@ana/coyote-scribe" {
			t.Errorf("el gate G5 lo registró el primero, no %s", x.RecordedBy)
		}
	}
}
