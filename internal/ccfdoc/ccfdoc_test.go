package ccfdoc

import (
	"strings"
	"testing"
)

const readme = `---
coyote: 1
repo: shop-api
type: backend
stack: [go, postgres]
owners: ["@ana"]
standards: { profile: backend, waive: [R9, {id: R10, reason: "README heredado", adr: ADR-0003}] }
---
# shop-api
purpose|API de pedidos, pagos y envíos de la tienda
run|make run
test|make test
entry|cmd/api|API pública
mod|payments|cobros y reembolsos|internal/payments|events:payments.*
docs|docs/|decisiones y flujos
`

const context = `---
coyote: 1
repo: shop-api
updated: 2026-09-01
---
# CONTEXT.coyote.md
inv|payments|un cobro nunca se repite; idempotencia por clave externa|internal/payments/charge.go#L40
gap|cierres|el corte diario corre en UTC|-
`

func TestReadmeValid(t *testing.T) {
	d, issues := Parse(ReadmeFile, []byte(readme))
	issues = append(issues, d.Validate()...)
	if HasErrors(issues) {
		t.Fatalf("README válido con errores: %v", issues)
	}
	if d.Profile() != "backend" || d.First("purpose") == nil || len(d.All("mod")) != 1 {
		t.Fatal("no se leyeron perfil, purpose o módulos")
	}
	w := d.Waivers()
	if len(w) != 2 || w[0].ID != "R9" || w[1].Reason != "README heredado" {
		t.Fatalf("dispensas mal leídas: %+v", w)
	}
	if d.Limit() != ReadmeMaxTokensWithModules {
		t.Fatalf("tope con módulos esperado, obtenido %d", d.Limit())
	}
}

func TestReadmeErrors(t *testing.T) {
	bad := strings.Replace(readme, "purpose|API de pedidos, pagos y envíos de la tienda\n", "", 1)
	bad = strings.Replace(bad, "run|make run", "inv|x|y|z", 1)
	d, issues := Parse(ReadmeFile, []byte(bad))
	issues = append(issues, d.Validate()...)
	joined := ""
	for _, i := range issues {
		joined += i.String() + "\n"
	}
	for _, want := range []string{"falta purpose", "tipo de CONTEXT.coyote.md"} {
		if !strings.Contains(joined, want) {
			t.Errorf("se esperaba %q en:\n%s", want, joined)
		}
	}
}

func TestContext(t *testing.T) {
	d, issues := Parse(ContextFile, []byte(context))
	issues = append(issues, d.Validate()...)
	if HasErrors(issues) {
		t.Fatalf("CONTEXT válido con errores: %v", issues)
	}
	long := context + "dec|x|" + strings.Repeat("palabra ", 21) + "|-\n"
	d, issues = Parse(ContextFile, []byte(long))
	if issues = append(issues, d.Validate()...); !HasErrors(issues) {
		t.Fatal("se esperaba error por texto de más de 20 palabras")
	}
	huge := context + strings.Repeat("how|dev|corre las pruebas con make test antes de proponer cambios al equipo|Makefile\n", 150)
	d, issues = Parse(ContextFile, []byte(huge))
	if issues = append(issues, d.Validate()...); !HasErrors(issues) {
		t.Fatal("se esperaba error por exceder el tope de tokens")
	}
}

func TestFormatAndAppend(t *testing.T) {
	line, err := FormatEntry("gap", "cierres", "el corte  corre en | UTC", "")
	if err != nil || line != "gap|cierres|el corte corre en / UTC|-" {
		t.Fatalf("FormatEntry = %q, %v", line, err)
	}
	if _, err := FormatEntry("nota", "x", "y", "-"); err == nil {
		t.Fatal("tipo inválido aceptado")
	}
	out := AppendEntry(context, line, "2026-09-27")
	if !strings.Contains(out, "updated: 2026-09-27") || !strings.HasSuffix(out, line+"\n") {
		t.Fatalf("AppendEntry no actualizó:\n%s", out)
	}
	noUpdated := strings.Replace(context, "updated: 2026-09-01\n", "", 1)
	out = AppendEntry(noUpdated, line, "2026-09-27")
	if !strings.Contains(out, "updated: 2026-09-27\n---") {
		t.Fatalf("AppendEntry no agregó updated:\n%s", out)
	}
}
