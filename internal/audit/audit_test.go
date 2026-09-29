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
