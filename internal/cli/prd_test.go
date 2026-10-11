package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// prdProducto es un PRD del producto con la forma de la skill coyote-prd.
const prdProducto = `# PRD: Tienda en línea

Estado: aprobado
Responsable: @ana
Fecha: 2026-10-11

## Problema

Los pedidos pagados tardan horas en confirmarse porque la conciliación nocturna corre una vez al día.

## Metas y métricas

- M1: confirmar el pedido en menos de un minuto. Métrica: p95 del tiempo de confirmación, línea base 6 h, objetivo 1 min.

## Requisitos

| ID | Requisito | Prioridad | Meta |
|----|-----------|-----------|------|
| RQ-01 | Un pedido con pago capturado se confirma sin esperar la conciliación nocturna | debe | M1 |

## Criterios de aceptación

` + "```gherkin" + `
Escenario: pedido pagado se confirma sin conciliación nocturna [RQ-01]
  Dado un pedido con un pago capturado
  Cuando se procesa la confirmación
  Entonces el pedido queda confirmado
` + "```" + `
`

// prdPlan arma un plan de riesgo R2 de dos pasos con el PRD que se pida.
func prdPlan(prd, input string) string {
	if prd != "" {
		prd = "prd: " + prd + "\n"
	}
	return `id: W-0004
title: confirmación sin conciliación
autonomy: supervised
risk: R2
` + prd + `budget_usd: 2
steps:
  - id: S1
    does: escribir el PRD de la feature
    agent: coyote-pm
    input: [coyote/prd/producto.md]
    output: PRD de la feature
    sections: [Problema, Requisitos]
    max_usd: 0.5
  - id: S2
    does: diseñar la confirmación
    agent: coyote-architect
    input: [` + input + `]
    output: diseño
    max_usd: 1
`
}

func TestWsCheckAvisaSiFaltaElPRD(t *testing.T) {
	_, root := wsProject(t, "supervised")
	prdDir := filepath.Join(root, "coyote", "prd")
	if err := os.MkdirAll(prdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prdDir, "producto.md"), []byte(prdProducto), 0o644); err != nil {
		t.Fatal(err)
	}

	// Un plan R2 que no declara de qué PRD parte: aviso, no error.
	writePlan(t, root, "W-0004-confirmacion", prdPlan("", "step:S1"))
	r := run(t, root, "", "ws", "check", "W-0004")
	must(t, r, 0, "ws check sin PRD")
	if !strings.Contains(r.stdout, "el plan es R2 y no parte de un PRD (R20)") || strings.Contains(r.stdout, "PRD: ") {
		t.Errorf("sin prd: se esperaba el aviso de R20 y ninguna línea PRD:\n%s", r.stdout)
	}

	// El primer paso lo escribe y el siguiente lo recibe.
	writePlan(t, root, "W-0004-confirmacion", prdPlan("step:S1", "step:S1"))
	r = run(t, root, "", "ws", "check", "W-0004")
	must(t, r, 0, "ws check con step:S1")
	if !strings.Contains(r.stdout, "PRD: la entrega del paso S1") || strings.Contains(r.stdout, "R20") {
		t.Errorf("con prd: step:S1 no debe avisar de R20 y sí decir de dónde parte:\n%s", r.stdout)
	}
	r = run(t, root, "", "ws", "check", "W-0004", "--json")
	must(t, r, 0, "ws check --json")
	var got struct {
		PRD      string `json:"prd"`
		Findings []struct{ Level, Msg string }
	}
	if err := json.Unmarshal([]byte(r.stdout), &got); err != nil || got.PRD != "step:S1" {
		t.Fatalf("el JSON de ws check lleva prd: %v\n%s", err, r.stdout)
	}
	for _, f := range got.Findings {
		if strings.Contains(f.Msg, "PRD") {
			t.Errorf("hallazgo inesperado sobre el PRD: %+v", f)
		}
	}

	// Un archivo que el paso recibe y que todavía no existe: un solo aviso.
	writePlan(t, root, "W-0004-confirmacion", prdPlan("coyote/prd/confirmacion.md", "coyote/prd/confirmacion.md"))
	r = run(t, root, "", "ws", "check", "W-0004")
	must(t, r, 0, "ws check con un archivo que falta")
	if strings.Count(r.stdout, "coyote/prd/confirmacion.md no existe todavía") != 1 || !strings.Contains(r.stdout, "PRD: coyote/prd/confirmacion.md") {
		t.Errorf("se esperaba un solo aviso de entrada que falta:\n%s", r.stdout)
	}
	if err := os.WriteFile(filepath.Join(prdDir, "confirmacion.md"), []byte(prdProducto), 0o644); err != nil {
		t.Fatal(err)
	}
	r = run(t, root, "", "ws", "check", "W-0004")
	must(t, r, 0, "ws check con el archivo")
	if strings.Contains(r.stdout, "no existe todavía") || strings.Contains(r.stdout, "R20") {
		t.Errorf("con el archivo no debe avisar de nada del PRD:\n%s", r.stdout)
	}

	// Un prd: que no dice lo que quiere decir es un error y el plan no corre.
	writePlan(t, root, "W-0004-confirmacion", prdPlan("step:S9", "step:S1"))
	r = run(t, root, "", "ws", "check", "W-0004")
	must(t, r, 1, "ws check con un paso que no existe")
	if !strings.Contains(r.stdout, "prd step:S9: ese paso no existe") {
		t.Errorf("se esperaba el error del paso inexistente:\n%s", r.stdout)
	}
	r = run(t, root, "", "ws", "run", "W-0004", "--dry-run")
	must(t, r, 1, "ws run con un prd inválido")
}

func TestElPRDEntraAlContexto(t *testing.T) {
	_, root := gateProject(t)
	prdDir := filepath.Join(root, "coyote", "prd")
	if err := os.MkdirAll(prdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prdDir, "producto.md"), []byte(prdProducto), 0o644); err != nil {
		t.Fatal(err)
	}
	r := run(t, root, "", "ask", "conciliación nocturna RQ-01")
	must(t, r, 0, "ask")
	if !strings.Contains(r.stdout, "coyote/prd/producto.md#L") {
		t.Errorf("ask debe citar el PRD con su ruta y su línea:\n%s", r.stdout)
	}
	r = run(t, root, "", "get", "context", "--query", "confirmar el pedido sin esperar la conciliación nocturna", "--budget", "3000")
	must(t, r, 0, "get context")
	if !strings.Contains(r.stdout, "coyote/prd/producto.md") {
		t.Errorf("el paquete de contexto debe traer el PRD cuando es lo relevante:\n%s", r.stdout)
	}
}

// prdSecciones son las ocho secciones de la plantilla, en orden.
var prdSecciones = []string{"Problema", "Usuarios", "Metas y métricas", "Alcance", "Requisitos", "Criterios de aceptación", "Riesgos", "Preguntas abiertas"}

var (
	prdReqRe   = regexp.MustCompile(`(?m)^\| (RQ-\d+) \|[^|]*\|[^|]*\|`)
	prdCiteRe  = regexp.MustCompile(`(?m)^\s*Escenario: .*\[(RQ-\d+)\]\s*$`)
	prdDebeRe  = regexp.MustCompile(`(?m)^\| (RQ-\d+) \|[^|]*\| debe \|`)
	prdTitleRe = regexp.MustCompile(`(?m)^## (.+)$`)
)

// La skill, el agente, la guía y el ejemplo dicen lo mismo: si una cambia las
// secciones de la plantilla, las demás tienen que cambiar con ella.
func TestLaPlantillaDelPRDEsUna(t *testing.T) {
	skill := readFile(t, "../../skills/coyote-prd/SKILL.md")
	for i, s := range prdSecciones {
		if !strings.Contains(skill, fmt.Sprintf("%d. **%s.**", i+1, s)) {
			t.Errorf("la skill coyote-prd no trae la sección %d, %s", i+1, s)
		}
	}
	for _, f := range []string{"../../agents/coyote-pm.md", "../../docs/guides/prd.md", "../../coyote/decisions/ADR-0028-prd-como-primera-fase.md"} {
		text := readFile(t, f)
		for _, s := range prdSecciones {
			if !strings.Contains(text, s) {
				t.Errorf("%s no nombra la sección %s", f, s)
			}
		}
	}
	if want := "sections: [" + strings.Join(prdSecciones, ", ") + "]"; !strings.Contains(readFile(t, "../../docs/guides/prd.md"), want) {
		t.Errorf("la guía debe exigir las ocho secciones en el paso del PRD: %s", want)
	}
	for _, f := range []string{"producto.md", "alta-de-pedidos.md"} {
		text := readFile(t, "../../examples/acme-shop/coyote/prd/"+f)
		var got []string
		for _, m := range prdTitleRe.FindAllStringSubmatch(text, -1) {
			got = append(got, m[1])
		}
		want := append([]string{}, prdSecciones...)
		if f == "producto.md" {
			want = append(want, "Features")
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: secciones %q, se esperaba %q", f, got, want)
		}
		reqs := map[string]bool{}
		for _, m := range prdReqRe.FindAllStringSubmatch(text, -1) {
			reqs[m[1]] = true
		}
		if len(reqs) == 0 {
			t.Errorf("%s: sin requisitos con ID", f)
		}
		cited := map[string]bool{}
		for _, m := range prdCiteRe.FindAllStringSubmatch(text, -1) {
			if !reqs[m[1]] {
				t.Errorf("%s: un escenario cita %s y no está entre los requisitos", f, m[1])
			}
			cited[m[1]] = true
		}
		if f == "alta-de-pedidos.md" {
			for _, m := range prdDebeRe.FindAllStringSubmatch(text, -1) {
				if !cited[m[1]] {
					t.Errorf("%s: el requisito %s es \"debe\" y ningún escenario lo cita", f, m[1])
				}
			}
		}
		if !strings.Contains(text, "```gherkin") {
			t.Errorf("%s: sin criterios en Gherkin", f)
		}
	}
}
