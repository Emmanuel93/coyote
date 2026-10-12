package workstream

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"

	"github.com/Emmanuel93/coyote/internal/ccf"
	"github.com/Emmanuel93/coyote/internal/ledger"
)

const planYAML = `id: W-0007
title: revisión de pagos
autonomy: supervised
risk: R2
prd: coyote/prd/pagos.md
budget_usd: 3
steps:
  - id: S1
    does: revisar el impacto del cambio
    agent: coyote-reviewer
    input: [diff:servicios=main...HEAD, docs/pagos.md, coyote/prd/pagos.md]
    output: informe de impacto
    sections: [Hallazgos, Riesgos]
    max_usd: 1
  - id: S2
    does: proponer el parche
    agent: coyote-dev
    input: [step:S1]
    output: [parche como diff, pruebas]
    max_usd: 1.5
    max_turns: 30
    gate: human
  - id: S3
    does: probar en staging
    agent: persona
  - { id: S4, does: resumen para el PR, agent: coyote-scribe, input: [step:S2], output: texto del PR, max_usd: 0.5, risk: R1 }
`

func mustParse(t *testing.T, s string) *Plan {
	t.Helper()
	p, err := Parse([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	p.Dir = Base + "/W-0007-pagos"
	return p
}

var knownAgents = map[string]bool{"coyote-reviewer": true, "coyote-dev": true, "coyote-scribe": true}

func TestPlanValido(t *testing.T) {
	p := mustParse(t, planYAML)
	if len(p.Steps) != 4 || p.Steps[0].Input[1] != "docs/pagos.md" || p.Steps[0].Output[0] != "informe de impacto" || len(p.Steps[1].Output) != 2 {
		t.Fatalf("plan mal leído: %+v", p.Steps)
	}
	if p.StepRisk(p.Steps[0]) != "R2" || p.StepRisk(p.Steps[3]) != "R1" {
		t.Error("el riesgo del paso manda sobre el del plan")
	}
	fs := p.Check(CheckOptions{Agents: knownAgents, Project: "supervised", Exists: func(string) bool { return true }})
	if Errors(fs) != 0 || len(fs) != 0 {
		t.Fatalf("un plan válido no tiene hallazgos: %v", fs)
	}
}

func TestPlanPRD(t *testing.T) {
	const head = "id: W-0007\nautonomy: supervised\n"
	step := func(id, agent, input string) string {
		in := ""
		if input != "" {
			in = ", input: [" + input + "]"
		}
		return "  - { id: " + id + ", does: hacer, agent: " + agent + ", output: x, max_usd: 1" + in + " }\n"
	}
	human := "  - { id: H, does: la persona, agent: persona }\n"
	cases := []struct {
		name, yaml string
		exists     bool
		want       []string // "aviso ...", "error ..." o nada si no hay hallazgos
	}{
		{"R1 sin PRD no avisa", head + "risk: R1\nsteps:\n" + step("S1", "coyote-dev", ""), true, nil},
		{"sin riesgo sin PRD no avisa", head + "steps:\n" + step("S1", "coyote-dev", ""), true, nil},
		{"R2 sin PRD avisa", head + "risk: R2\nsteps:\n" + step("S1", "coyote-dev", ""), true,
			[]string{"aviso el plan es R2 y no parte de un PRD (R20)"}},
		{"el riesgo de un paso cuenta", head + "steps:\n  - { id: S1, does: hacer, agent: coyote-dev, output: x, max_usd: 1, risk: R3 }\n", true,
			[]string{"aviso el plan es R3 y no parte de un PRD (R20)"}},
		{"un riesgo inválido no cuenta como R2", head + "risk: R9\nsteps:\n" + step("S1", "coyote-dev", ""), true,
			[]string{"error risk \"R9\" inválido"}},
		{"un archivo que algún paso recibe", head + "risk: R2\nprd: coyote/prd/pagos.md\nsteps:\n" + step("S1", "coyote-dev", "coyote/prd/pagos.md"), true, nil},
		{"file: delante de la ruta vale igual", head + "risk: R3\nprd: file:coyote/prd/pagos.md\nsteps:\n" + step("S1", "coyote-dev", "coyote/prd/pagos.md"), true, nil},
		{"un archivo que no existe todavía avisa una vez", head + "risk: R2\nprd: coyote/prd/pagos.md\nsteps:\n" + step("S1", "coyote-dev", "coyote/prd/pagos.md"), false,
			[]string{"aviso S1: la entrada coyote/prd/pagos.md no existe todavía"}},
		{"un archivo que no existe y nadie recibe", head + "risk: R2\nprd: coyote/prd/pagos.md\nsteps:\n" + step("S1", "coyote-dev", ""), false,
			[]string{"aviso el PRD coyote/prd/pagos.md no existe todavía", "aviso ningún paso recibe el PRD como entrada"}},
		{"nadie recibe el archivo", head + "risk: R2\nprd: coyote/prd/pagos.md\nsteps:\n" + step("S1", "coyote-dev", "docs/otro.md"), true,
			[]string{"aviso ningún paso recibe el PRD como entrada: agrega coyote/prd/pagos.md"}},
		{"el primer paso lo escribe", head + "risk: R2\nprd: step:S0\nsteps:\n" + step("S0", "coyote-pm", "") + step("S1", "coyote-dev", "step:S0"), true, nil},
		{"el paso del PRD no es el primero", head + "risk: R2\nprd: step:S1\nsteps:\n" + step("S0", "coyote-dev", "") + step("S1", "coyote-pm", "") + step("S2", "coyote-dev", "step:S1"), true,
			[]string{"aviso prd step:S1 no es el primer paso"}},
		{"nadie recibe la entrega del paso", head + "risk: R2\nprd: step:S0\nsteps:\n" + step("S0", "coyote-pm", "") + step("S1", "coyote-dev", ""), true,
			[]string{"aviso ningún paso recibe el PRD como entrada: agrega step:S0"}},
		{"el paso no existe", head + "risk: R2\nprd: step:S9\nsteps:\n" + step("S1", "coyote-pm", ""), true,
			[]string{"error prd step:S9: ese paso no existe"}},
		{"el paso es de la persona", head + "risk: R2\nprd: step:H\nsteps:\n" + human + step("S1", "coyote-dev", ""), true,
			[]string{"error prd step:H: es un paso de la persona"}},
		{"un diff no es un PRD", head + "risk: R2\nprd: diff:servicios=main...HEAD\nsteps:\n" + step("S1", "coyote-dev", ""), true,
			[]string{"error prd \"diff:servicios=main...HEAD\" inválido"}},
		{"una ruta que sale del proyecto", head + "risk: R2\nprd: ../prd.md\nsteps:\n" + step("S1", "coyote-dev", ""), true,
			[]string{"error prd \"../prd.md\" inválido"}},
		{"una ruta de git", head + "risk: R2\nprd: .GIT/prd.md\nsteps:\n" + step("S1", "coyote-dev", ""), true,
			[]string{"error prd \".GIT/prd.md\" inválido"}},
		{"con PRD y en R1 también se revisa", head + "risk: R1\nprd: step:S9\nsteps:\n" + step("S1", "coyote-dev", ""), true,
			[]string{"error prd step:S9: ese paso no existe"}},
		// Cuarta pasada: prd: se lee estricto.
		{"step: con otras mayúsculas", head + "risk: R2\nprd: STEP:S1\nsteps:\n" + step("S1", "coyote-pm", ""), true,
			[]string{"error prd \"STEP:S1\" inválido"}},
		{"un número no es un PRD", head + "risk: R2\nprd: 42\nsteps:\n" + step("S1", "coyote-dev", ""), true,
			[]string{"error prd \"42\" inválido: usa una ruta .md"}},
		{"el PRD es Markdown", head + "risk: R2\nprd: coyote/prd/pagos.txt\nsteps:\n" + step("S1", "coyote-dev", "coyote/prd/pagos.txt"), true,
			[]string{"error prd \"coyote/prd/pagos.txt\" inválido: usa una ruta .md"}},
		{"file:step: no es un paso", head + "risk: R2\nprd: file:step:S1\nsteps:\n" + step("S1", "coyote-pm", ""), true,
			[]string{"error prd \"file:step:S1\" inválido"}},
		{"./step: tampoco es un archivo", head + "risk: R2\nprd: ./x.md/../step:S1.md\nsteps:\n" + step("S1", "coyote-pm", ""), true,
			[]string{"error prd \"./x.md/../step:S1.md\" inválido"}},
		{"un salto de línea no inventa líneas", head + "risk: R2\nprd: \"coyote/prd/x.md\\n  ! fin de avisos\"\nsteps:\n" + step("S1", "coyote-dev", ""), true,
			[]string{"error prd \"coyote/prd/x.md\\n  ! fin de avisos\" inválido"}},
		{"una marca que no se ve", head + "risk: R2\nprd: \"coyote/prd/x\\u200b.md\"\nsteps:\n" + step("S1", "coyote-dev", ""), true,
			[]string{"error prd \"coyote/prd/x\\u200b.md\" inválido"}},
		// Solo un agente lee el PRD: un paso de la persona no cuenta.
		{"solo la persona recibe el archivo", head + "risk: R2\nprd: coyote/prd/pagos.md\nsteps:\n  - { id: H, does: la persona lo lee, agent: persona, input: [coyote/prd/pagos.md] }\n" + step("S1", "coyote-dev", ""), true,
			[]string{"aviso ningún paso recibe el PRD como entrada"}},
		{"solo la persona recibe la entrega", head + "risk: R2\nprd: step:S1\nsteps:\n" + step("S1", "coyote-pm", "") + "  - { id: H, does: la persona lo lee, agent: persona, input: [step:S1] }\n", true,
			[]string{"aviso ningún paso recibe el PRD como entrada: agrega step:S1"}},
		{"antes del paso del PRD solo hay un paso de la persona", head + "risk: R2\nprd: step:S1\nsteps:\n" + human + step("S1", "coyote-pm", "") + step("S2", "coyote-dev", "step:S1"), true, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := mustParse(t, c.yaml)
			exists := func(string) bool { return c.exists }
			fs := p.Check(CheckOptions{Agents: map[string]bool{"coyote-dev": true, "coyote-pm": true}, Project: "supervised", Exists: exists})
			var got []string
			for _, f := range fs {
				got = append(got, f.Level+" "+f.String())
			}
			if len(got) != len(c.want) {
				t.Fatalf("hallazgos %q, se esperaba %q", got, c.want)
			}
			for i, w := range c.want {
				if !strings.HasPrefix(got[i], w) {
					t.Errorf("hallazgo %d: %q no empieza con %q", i, got[i], w)
				}
			}
		})
	}
}

// TestPlanPRDAutonomo: cuarta pasada de v1.0. En autonomous solo corren sin
// detenerse los pasos que no piden revisión: un PRD que escribe un agente y
// que el paso siguiente usa sin pasar por una persona rompe R20 ("un PRD que
// una persona revisó").
func TestPlanPRDAutonomo(t *testing.T) {
	plan := func(gate string) string {
		g := ""
		if gate != "" {
			g = ", gate: " + gate
		}
		return "id: W-0007\nautonomy: autonomous\nrisk: R2\nprd: step:P1\nbudget_usd: 3\nsteps:\n" +
			"  - { id: P1, does: escribir el PRD, agent: coyote-pm, output: PRD, max_usd: 1" + g + " }\n" +
			"  - { id: D1, does: diseñar, agent: coyote-dev, input: [step:P1], output: diseño, max_usd: 1 }\n"
	}
	findings := func(yaml, project string) string {
		var b strings.Builder
		for _, f := range mustParse(t, yaml).Check(CheckOptions{Agents: map[string]bool{"coyote-pm": true, "coyote-dev": true}, Project: project}) {
			b.WriteString(f.Level + " " + f.String() + "\n")
		}
		return b.String()
	}
	if got := findings(plan(""), "autonomous"); !strings.Contains(got, "error prd step:P1: en autonomous el PRD pasa por una persona") || !strings.Contains(got, "gate: human") {
		t.Errorf("autonomous sin revisión del PRD:\n%s", got)
	}
	if got := findings(plan("human"), "autonomous"); got != "" {
		t.Errorf("con gate: human en el paso del PRD no hay hallazgos:\n%s", got)
	}
	// Si el proyecto lo baja a supervised, la persona acepta cada paso: no hace falta el gate.
	if got := findings(plan(""), "supervised"); strings.Contains(got, "error") {
		t.Errorf("en supervised la persona acepta cada paso:\n%s", got)
	}
	// Quinta revisión (N5): manda el modo con el que corre esta vez. Un plan
	// autonomous que se corre con --mode supervised no pide el gate, y con
	// --mode autonomous (o sin modo) sí.
	at := func(mode string) string {
		var b strings.Builder
		for _, f := range mustParse(t, plan("")).Check(CheckOptions{Agents: map[string]bool{"coyote-pm": true, "coyote-dev": true}, Project: "autonomous", Mode: mode}) {
			b.WriteString(f.Level + " " + f.String() + "\n")
		}
		return b.String()
	}
	if got := at("supervised"); strings.Contains(got, "error") {
		t.Errorf("con el modo supervised la persona acepta cada paso:\n%s", got)
	}
	for _, mode := range []string{"autonomous", ""} {
		if got := at(mode); !strings.Contains(got, "error prd step:P1") {
			t.Errorf("con el modo %q sigue pidiendo el gate:\n%s", mode, got)
		}
	}
	// Un PRD que es un archivo lo revisa quien lo guarda: no pide gate.
	file := strings.Replace(strings.Replace(plan(""), "prd: step:P1", "prd: coyote/prd/pagos.md", 1), "input: [step:P1]", "input: [coyote/prd/pagos.md]", 1)
	if got := findings(file, "autonomous"); strings.Contains(got, "error") {
		t.Errorf("un PRD en un archivo no pide gate:\n%s", got)
	}
}

// TestEntradasQueNoSePuedenLeer: cuarta pasada de v1.0. Un enlace, una carpeta
// o un archivo demasiado grande no es "todavía no existe": el paso no lo puede
// leer y el plan no corre. Un binario avisa que el paso lo recibe sin contenido.
func TestEntradasQueNoSePuedenLeer(t *testing.T) {
	yaml := "id: W-0007\nautonomy: supervised\nrisk: R2\nprd: coyote/prd/pagos.md\nsteps:\n" +
		"  - { id: S1, does: leer, agent: coyote-dev, output: x, max_usd: 1, input: [coyote/prd/pagos.md, docs/foto.png, docs/falta.md] }\n"
	states := map[string]InputState{"coyote/prd/pagos.md": InputUnusable, "docs/foto.png": InputBinary, "docs/falta.md": InputMissing}
	fs := mustParse(t, yaml).Check(CheckOptions{Agents: map[string]bool{"coyote-dev": true}, Project: "supervised",
		Inspect: func(rel string) InputState { return states[rel] }})
	var got []string
	for _, f := range fs {
		got = append(got, f.Level+" "+f.String())
	}
	text := strings.Join(got, "\n")
	for _, want := range []string{"error S1: la entrada coyote/prd/pagos.md no es un archivo regular dentro del proyecto",
		"aviso S1: la entrada docs/foto.png no es texto: el paso la recibe sin contenido", "aviso S1: la entrada docs/falta.md no existe todavía"} {
		if !strings.Contains(text, want) {
			t.Errorf("falta %q en:\n%s", want, text)
		}
	}
	if strings.Contains(text, "no existe todavía") && strings.Contains(text, "coyote/prd/pagos.md no existe") {
		t.Errorf("un enlace no es un archivo que falta:\n%s", text)
	}
}

// TestEntradaQueFaltaEnAutonomous: sexta revisión de v1.0 (N13). En autonomous la
// persona autoriza los archivos que existen al autorizar (A4): una entrada que
// todavía no existe detiene al loop antes de ese paso, y ws check lo dice en vez
// de un "no existe todavía" que suena a que el paso la creará. En los otros
// modos sigue siendo el aviso de siempre.
func TestEntradaQueFaltaEnAutonomous(t *testing.T) {
	yaml := "id: W-0007\nautonomy: autonomous\nbudget_usd: 2\nrisk: R1\nsteps:\n" +
		"  - { id: S1, does: leer, agent: coyote-dev, output: x, max_usd: 1, input: [docs/falta.md] }\n"
	at := func(mode string) string {
		var got []string
		for _, f := range mustParse(t, yaml).Check(CheckOptions{Agents: map[string]bool{"coyote-dev": true}, Project: "autonomous", Mode: mode,
			Inspect: func(string) InputState { return InputMissing }}) {
			got = append(got, f.Level+" "+f.String())
		}
		return strings.Join(got, "\n")
	}
	for _, mode := range []string{"autonomous", ""} {
		got := at(mode)
		if !strings.Contains(got, "aviso S1: la entrada docs/falta.md no existe todavía: en autonomous el loop se detiene antes de este paso (A4)") || strings.Contains(got, "error") {
			t.Errorf("modo %q: una entrada que falta detiene al loop autónomo:\n%s", mode, got)
		}
	}
	for _, mode := range []string{"supervised", "manual"} {
		if got := at(mode); got != "aviso S1: la entrada docs/falta.md no existe todavía" {
			t.Errorf("modo %q conserva el aviso de siempre:\n%s", mode, got)
		}
	}
}

func TestMaxRisk(t *testing.T) {
	p := mustParse(t, "id: W-0007\nrisk: R2\nsteps:\n  - { id: S1, does: x, agent: persona, risk: R3 }\n  - { id: S2, does: x, agent: persona, risk: R9 }\n")
	if got := p.MaxRisk(); got != "R3" {
		t.Errorf("MaxRisk = %s, se esperaba R3", got)
	}
	if got := mustParse(t, "id: W-0007\nrisk: R9\n").MaxRisk(); got != "R1" {
		t.Errorf("un riesgo inválido no sube el plan: %s", got)
	}
}

func TestPlanContratoA2(t *testing.T) {
	bad := `id: W-0008
autonomy: autonomous
steps:
  - { id: S1, agent: coyote-dev, output: x, max_usd: 1 }
  - { id: S1, does: repetido, agent: coyote-nadie, max_usd: 2000 }
  - { id: "S 3", does: sin tope ni salida, agent: coyote-dev, gate: siempre }
  - { id: S4, does: entradas malas, agent: coyote-dev, output: x, max_usd: 1, input: [step:S9, step:S5, "../fuera", .coyote/x, "diff:repo", step:S6] }
  - { id: S5, does: más tarde, agent: coyote-dev, output: x, max_usd: 1, max_turns: 900, sections: ["# mal"] }
  - { id: S6, does: la persona, agent: persona }
`
	p := mustParse(t, bad)
	fs := p.Check(CheckOptions{Agents: knownAgents, Project: "manual"})
	text := ""
	for _, f := range fs {
		text += f.Level + " " + f.String() + "\n"
	}
	for _, want := range []string{
		"error el id W-0008 no coincide con la carpeta W-0007-pagos",
		"error autonomous exige budget_usd",
		"aviso el proyecto permite hasta manual: el plan corre como manual",
		"error S1: falta does",
		"error S1: id repetido",
		"error S1: el agente coyote-nadie no existe",
		"error S1: max_usd inválido",
		"error S1: falta output",
		"error S 3: id inválido",
		"error S 3: falta max_usd",
		"error S 3: gate \"siempre\" inválido",
		"error S4: entrada step:S9: ese paso no existe",
		"error S4: entrada step:S5: ese paso corre después",
		"error S4: entrada \"../fuera\": la ruta sale del proyecto",
		"error S4: entrada \".coyote/x\": .coyote no es una entrada",
		"error S4: entrada \"diff:repo\": usa diff:repo=RANGO",
		"error S4: entrada step:S6: ese paso corre después",
		"error S5: max_turns inválido",
		"error S5: sección inválida",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("falta el hallazgo %q en:\n%s", want, text)
		}
	}
	if strings.Contains(text, "error S6:") || strings.Contains(text, "aviso S6:") {
		t.Errorf("un paso de la persona no lleva tope ni salida:\n%s", text)
	}
	// Una entrada de un paso de la persona no tiene artefacto.
	p2 := mustParse(t, `id: W-0007
steps:
  - { id: H, does: la persona prueba, agent: persona }
  - { id: S2, does: resumir, agent: coyote-dev, output: x, max_usd: 1, input: [step:H] }
`)
	if fs := p2.Check(CheckOptions{}); Errors(fs) != 1 || !strings.Contains(fs[0].Msg, "no deja artefacto") {
		t.Errorf("entrada de un paso de la persona: %v", fs)
	}
	// Campos desconocidos son un error de lectura.
	if _, err := Parse([]byte("id: W-0007\nsteps:\n  - { id: S1, does: x, agent: coyote-dev, max_dollars: 3 }\n")); err == nil {
		t.Error("un campo desconocido en un paso debe fallar")
	}
	if _, err := Parse([]byte("id: W-0007\noutput: { a: b }\n")); err == nil {
		t.Error("un campo desconocido en el plan debe fallar")
	}
}

func TestPlanesAnteriores(t *testing.T) {
	// El formato preliminar de v0.1 a v0.4 se lee: output como texto o
	// lista, gate del plan, close y pasos de la persona.
	old := `# comentario
id: W-0005
title: v0.5
owner: "@ana"
autonomy: manual
risk: R2
reasoning: docs/plan.md#v05
authorized_by: coyote/approvals/P-0004.json
gate: G5
steps:
  - { id: T32, does: plan, agent: coyote-sr-solution-architect, output: [docs/plan.md, coyote/decisions/] }
  - { id: T37, does: corridas medidas, agent: persona, output: ledger del piloto, gate: human }
close: { summary: close.md }
`
	p, err := Parse([]byte(old))
	if err != nil {
		t.Fatal(err)
	}
	p.Dir = Base + "/W-0005-team"
	fs := p.Check(CheckOptions{})
	if Errors(fs) != 1 || !strings.Contains(fs[0].String(), "T32: falta max_usd") {
		t.Errorf("un plan anterior solo debe pedir el tope que le falta: %v", fs)
	}
}

func TestEntradas(t *testing.T) {
	for in, want := range map[string]Input{
		"step:S1":               {"step", "S1"},
		"diff:api=main...HEAD":  {"diff", "api=main...HEAD"},
		"file:docs/a.md":        {"file", "docs/a.md"},
		"docs/./b.md":           {"file", "docs/b.md"},
		" src/pagos/x.go ":      {"file", "src/pagos/x.go"},
		"coyote/decisions/A.md": {"file", "coyote/decisions/A.md"},
	} {
		got, err := ParseInput(in)
		if err != nil || got != want {
			t.Errorf("%q: %v %v, se esperaba %v", in, got, err, want)
		}
	}
	for _, in := range []string{"/etc/passwd", "~/.ssh/id", "../x", "a/../../x", ".git/config", ".coyote/proposals/P-1.json", ".GIT/config", ".Coyote/x", "vendor/m/.git/config", "diff:-x=y", "diff:a=--output=/tmp/x", "diff:a=b c", "step:", `a\b`, ""} {
		if _, err := ParseInput(in); err == nil {
			t.Errorf("%q debería ser inválida", in)
		}
	}
}

// TestEntradasEstrictas: cuarta pasada de v1.0. Una entrada con un salto de
// línea, un control o una marca que no se ve redibuja la salida de ws check; y
// un step: o diff: con otras mayúsculas, o dentro de file:, no puede pasar por
// un archivo con ese nombre.
func TestEntradasEstrictas(t *testing.T) {
	for _, in := range []string{
		"docs/a.md\n  ! fin de avisos", "docs/a.md\r\nx", "docs/a\u202e.md", "docs/a\x1b[2J.md", "docs/a\u200b.md", "docs/\ta.md",
		"docs/a\u2028.md", "docs/a\U000e0041.md", "docs/a\u3164.md", "docs/\xff.md", "docs/a\u0085b.md",
		"STEP:S1", "Step:S1", "sTeP:S1", "DIFF:r=main...HEAD", "Diff:r=main...HEAD", "file:step:S1", "file:STEP:S1", "file:diff:r=main...HEAD", "file: step:S1",
		// Quinta revisión (N3): el prefijo se mira en la ruta ya limpia, como la
		// leen quienes ven el aviso: ./step:S1 es step:S1.
		"./step:S1", "./diff:r=main...HEAD", "x/../file:step:S1", "./STEP:S1", "file:./step:S1", "./file:step:S1", "a/b/../../diff:r=a...b", "file:x/../DIFF:r=a...b",
		// Sexta revisión (N12): un espacio al borde de un nombre se come el
		// prefijo en la salida (" step:S1" se lee como step:S1). Ningún nombre de
		// la ruta empieza ni termina con espacios, del tipo que sean.
		"a/../ step:S1", "./ step:S1", "./\u3000step:S1", "./\u2007step:S1", "x/../\u00a0diff:r=a...b", "./\u2003STEP:S1", "file:./ step:S1", "./ file:step:S1",
		"docs/ a.md", "docs /a.md", "docs/a.md /b.md", "a/ /b.md", "docs/\u00a0a.md", "docs/a.md\u3000/b.md",
		"docs/ /../x.md", "docs/\u3000/../x.md", // aunque el .. lo cancele, el nombre se escribió así
	} {
		if got, err := ParseInput(in); err == nil {
			t.Errorf("%q debería ser inválida y dio %v", in, got)
		}
	}
	for in, want := range map[string]Input{
		"docs/ñandú.md":       {"file", "docs/ñandú.md"},
		"docs/con espacio.md": {"file", "docs/con espacio.md"},
		"file:docs/a.md":      {"file", "docs/a.md"},
		"stepper/notas.md":    {"file", "stepper/notas.md"},
		"docs/step:S1.md":     {"file", "docs/step:S1.md"},
		"./docs/a.md":         {"file", "docs/a.md"},
		"docs/a b/c d.md":     {"file", "docs/a b/c d.md"},
		" docs/a.md\u3000":    {"file", "docs/a.md"},
		"file: docs/a.md":     {"file", "docs/a.md"},
	} {
		got, err := ParseInput(in)
		if err != nil || got != want {
			t.Errorf("%q: %v %v, se esperaba %v", in, got, err, want)
		}
	}
}

func line(ts time.Time, typ, status string, cost float64, refs ...string) ledger.Entry {
	l := ccf.Line{TS: ts, Actor: "@ana", Project: "W-0007", Repo: "tienda", Type: typ, Scope: "-", What: typ + " algo", Refs: refs, Status: status}
	if cost > 0 {
		l.Cost = &ccf.Cost{In: cost}
	}
	return ledger.Entry{Line: l}
}

func TestEstadoDesdeElLedger(t *testing.T) {
	p := mustParse(t, planYAML)
	t0 := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	var ev []ledger.Entry
	st := Fold(p, ev)
	if n := st.Next("supervised"); !n.Run || n.Index != 0 {
		t.Fatalf("un plan nuevo corre S1: %+v", n)
	}
	// S1 corre bien: espera revisión.
	ev = append(ev, line(t0, "run", "ok", 0.4, "step:S1", "doc:runs/a.md", "mode:autonomous"))
	st = Fold(p, ev)
	if st.Steps[0].Status != Review || st.Steps[0].Doc != "runs/a.md" || st.Spent != 0.4 {
		t.Fatalf("S1: %+v", st.Steps[0])
	}
	if n := st.Next("supervised"); n.Run || n.Reason != StopReview || n.Index != 0 {
		t.Errorf("supervised se detiene a revisar: %+v", n)
	}
	if n := st.Next("autonomous"); !n.Run || n.Index != 1 {
		t.Errorf("autonomous sigue con S2: %+v", n)
	}
	// Una corrida de otro workstream o sin paso no cambia los pasos; sin paso sí suma al plan.
	other := line(t0, "run", "ok", 9, "step:S1")
	other.Line.Project = "W-0001"
	ev = append(ev, other, line(t0, "run", "ok", 0.1))
	st = Fold(p, ev)
	if st.Spent < 0.49 || st.Spent > 0.51 || st.Steps[0].Runs != 1 {
		t.Errorf("gasto del plan: %v, corridas de S1: %d", st.Spent, st.Steps[0].Runs)
	}
	// Una aceptación de otro artefacto (una corrida anterior) no cuenta.
	if st := Fold(p, append(ev[:len(ev):len(ev)], line(t0, "apr", "ok", 0, "step:S1", "doc:runs/viejo.md"))); st.Steps[0].Status != Review {
		t.Errorf("aceptación de otro artefacto: %s", st.Steps[0].Status)
	}
	// La persona lo acepta; S2 corre y deja acciones en la cola.
	ev = append(ev, line(t0, "apr", "ok", 0, "step:S1", "doc:runs/a.md"), line(t0, "run", "pend", 0.3, "step:S2", "doc:runs/b.md"))
	st = Fold(p, ev)
	if st.Steps[0].Status != Done || st.Steps[1].Status != Blocked {
		t.Fatalf("estados: %s %s", st.Steps[0].Status, st.Steps[1].Status)
	}
	if n := st.Next("autonomous"); n.Run || n.Reason != StopBlocked || n.Index != 1 {
		t.Errorf("con acciones en la cola se detiene en cualquier modo: %+v", n)
	}
	// Retoma, termina bien; S2 pide gate human: autonomous también se detiene.
	ev = append(ev, line(t0, "run", "ok", 0.2, "step:S2", "doc:runs/c.md"))
	st = Fold(p, ev)
	if n := st.Next("autonomous"); n.Run || n.Reason != StopReview || n.Index != 1 {
		t.Errorf("gate: human detiene a autonomous: %+v", n)
	}
	if st.Steps[1].Runs != 2 || st.Steps[1].Doc != "runs/c.md" {
		t.Errorf("S2: %+v", st.Steps[1])
	}
	// La persona pide rehacerlo; una corrida que no arrancó no lo cambia.
	ev = append(ev, line(t0, "rej", "ok", 0, "step:S2", "doc:runs/rev.md"), line(t0, "run", "skip", 0, "step:S2"))
	st = Fold(p, ev)
	if st.Steps[1].Status != Redo || st.Steps[1].Feedback != "runs/rev.md" || st.Steps[1].Why == "" {
		t.Fatalf("S2 por rehacer: %+v", st.Steps[1])
	}
	if n := st.Next("supervised"); !n.Run || n.Index != 1 {
		t.Errorf("un paso por rehacer corre: %+v", n)
	}
	// Falla; luego sale bien y se acepta: sigue un paso de la persona.
	ev = append(ev, line(t0, "run", "fail", 0.1, "step:S2"))
	if n := Fold(p, ev).Next("autonomous"); n.Run || n.Reason != StopFailed {
		t.Errorf("una falla detiene: %+v", n)
	}
	ev = append(ev, line(t0, "run", "ok", 0.2, "step:S2", "doc:runs/d.md"), line(t0, "apr", "ok", 0, "step:S2"))
	st = Fold(p, ev)
	if st.Steps[1].Feedback != "" || st.Steps[1].Status != Done {
		t.Errorf("aceptar limpia la revisión: %+v", st.Steps[1])
	}
	if n := st.Next("autonomous"); n.Run || n.Reason != StopHuman || n.Index != 2 {
		t.Errorf("un paso de la persona detiene: %+v", n)
	}
	// Con el paso de la persona hecho, el gasto ya pasó el tope del plan: no corre S4.
	ev = append(ev, line(t0, "apr", "ok", 0, "step:S3"), line(t0, "run", "ok", 2, ""))
	st = Fold(p, ev)
	if n := st.Next("supervised"); n.Run || n.Reason != StopBudget || n.Index != 3 {
		t.Errorf("tope del plan: %+v (gastado %.2f)", n, st.Spent)
	}
	if r, capped := st.Remaining(); !capped || r >= 0 {
		t.Errorf("queda %.2f (tope %v)", r, capped)
	}
	// Sin tope en el plan, corre; en autonomous, al terminar queda la evaluación.
	p.BudgetUSD = 0
	st = Fold(p, ev)
	if n := st.Next("autonomous"); !n.Run || n.Index != 3 {
		t.Errorf("sin tope corre S4: %+v", n)
	}
	ev = append(ev, line(t0, "run", "ok", 0.1, "step:S4", "doc:runs/e.md", "mode:autonomous"))
	st = Fold(p, ev)
	if n := st.Next("autonomous"); n.Run || n.Reason != StopEvaluate || n.Index != 3 {
		t.Errorf("autonomous termina en evaluación: %+v", n)
	}
	if got := st.ToReview(3); len(got) != 1 || got[0] != 3 {
		t.Errorf("por revisar: %v", got)
	}
	ev = append(ev, line(t0, "apr", "ok", 0, "step:S4"))
	if n := Fold(p, ev).Next("autonomous"); n.Reason != StopDone || n.Index != -1 {
		t.Errorf("plan completo: %+v", n)
	}
	// Cerrado con coyote close, el motor ya no corre aunque falten pasos.
	st = Fold(p, append(ev[:2:2], line(t0, "close", "ok", 0)))
	if n := st.Next("supervised"); n.Run || n.Reason != StopClosed || !st.Closed {
		t.Errorf("cerrado: %+v", n)
	}
}

// TestEvaluarSoloLoQueCorrioEnAutonomous: sexta revisión de v1.0 (N13). La
// evaluación final de A4 es de lo que el loop autónomo hizo por su cuenta: un
// paso que corrió en supervised (o en manual, o bajando el modo con --mode)
// espera la revisión de la persona, no se pasa de largo, y el plan no "corrió
// en autónomo" por mirarlo con el modo del plan.
func TestEvaluarSoloLoQueCorrioEnAutonomous(t *testing.T) {
	p := mustParse(t, strings.Replace(planYAML, "    gate: human\n", "", 1))
	t0 := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	for _, mode := range []string{"supervised", "manual", ""} {
		refs := []string{"step:S1", "doc:runs/a.md"}
		if mode != "" {
			refs = append(refs, "mode:"+mode)
		}
		st := Fold(p, []ledger.Entry{line(t0, "run", "ok", 0.4, refs...)})
		if st.Steps[0].Auto {
			t.Errorf("mode:%q no es autonomous: %+v", mode, st.Steps[0])
		}
		if n := st.Next("autonomous"); n.Run || n.Reason != StopReview || n.Index != 0 {
			t.Errorf("mode:%q: un paso que no corrió en autonomous lo revisa una persona: %+v", mode, n)
		}
	}
	// Lo que corrió en autonomous se pasa de largo y se evalúa al final.
	ev := []ledger.Entry{line(t0, "run", "ok", 0.4, "step:S1", "doc:runs/a.md", "mode:autonomous")}
	st := Fold(p, ev)
	if !st.Steps[0].Auto {
		t.Fatalf("mode:autonomous: %+v", st.Steps[0])
	}
	if n := st.Next("autonomous"); !n.Run || n.Index != 1 {
		t.Errorf("autonomous sigue con S2: %+v", n)
	}
	// La última corrida manda: S1 rehecho en supervised ya no es del loop.
	ev = append(ev, line(t0, "rej", "ok", 0, "step:S1", "doc:runs/rev.md"), line(t0, "run", "ok", 0.4, "step:S1", "doc:runs/b.md", "mode:supervised"))
	st = Fold(p, ev)
	if st.Steps[0].Auto || st.Steps[0].Status != Review {
		t.Errorf("S1 rehecho en supervised: %+v", st.Steps[0])
	}
	if n := st.Next("autonomous"); n.Run || n.Reason != StopReview || n.Index != 0 {
		t.Errorf("lo rehecho en supervised lo revisa una persona: %+v", n)
	}
	// Una corrida que no arrancó no cambia con qué modo corrió el paso.
	ev = []ledger.Entry{
		line(t0, "run", "ok", 0.4, "step:S1", "doc:runs/a.md", "mode:autonomous"),
		line(t0, "run", "skip", 0, "step:S1", "mode:supervised"),
	}
	if st := Fold(p, ev); !st.Steps[0].Auto {
		t.Errorf("una corrida skip no cambia el modo del paso: %+v", st.Steps[0])
	}
}

func TestModo(t *testing.T) {
	for _, c := range []struct{ plan, project, want string }{
		{"autonomous", "manual", "manual"},
		{"autonomous", "supervised", "supervised"},
		{"supervised", "autonomous", "supervised"},
		{"", "autonomous", "manual"},
		{"autonomous", "", "manual"},
		{"autonomous", "autonomous", "autonomous"},
	} {
		if got, _ := Mode(c.plan, c.project); got != c.want {
			t.Errorf("plan %q, proyecto %q: %s, se esperaba %s", c.plan, c.project, got, c.want)
		}
	}
}

func TestAutorizacionPorHash(t *testing.T) {
	a := mustParse(t, planYAML)
	b := mustParse(t, strings.Replace(planYAML, "max_usd: 0.5", "max_usd: 5", 1))
	if a.AuthHash("tienda", nil) == b.AuthHash("tienda", nil) || a.AuthHash("tienda", nil) == a.AuthHash("otra", nil) {
		t.Error("cambiar el plan o el proyecto cambia la autorización")
	}
	if h := a.AuthHash("tienda", nil); !strings.HasPrefix(h, "sha256:") || len(h) != 71 || h != mustParse(t, planYAML).AuthHash("tienda", nil) {
		t.Errorf("hash: %s", h)
	}
}

// TestAutorizacionCubreLosArchivos: cuarta pasada de v1.0. La autorización es
// del plan exacto y de lo que lee: si el PRD o una entrada de archivo cambia
// después de aprobar, el hash cambia y hay que autorizar de nuevo.
func TestAutorizacionCubreLosArchivos(t *testing.T) {
	a := mustParse(t, planYAML)
	if got, want := strings.Join(a.Files(), " "), "coyote/prd/pagos.md docs/pagos.md"; got != want {
		t.Fatalf("archivos que lee el plan: %q, se esperaba %q", got, want)
	}
	files := map[string]string{"coyote/prd/pagos.md": "sha256:aa", "docs/pagos.md": "sha256:bb"}
	base := a.AuthHash("tienda", files)
	if base == a.AuthHash("tienda", nil) {
		t.Error("los archivos que el plan lee entran al hash")
	}
	if base != a.AuthHash("tienda", map[string]string{"docs/pagos.md": "sha256:bb", "coyote/prd/pagos.md": "sha256:aa"}) {
		t.Error("el orden del mapa no cambia el hash")
	}
	for name, changed := range map[string]map[string]string{
		"el PRD cambió":       {"coyote/prd/pagos.md": "sha256:cc", "docs/pagos.md": "sha256:bb"},
		"una entrada cambió":  {"coyote/prd/pagos.md": "sha256:aa", "docs/pagos.md": "sha256:cc"},
		"el PRD ya no existe": {"coyote/prd/pagos.md": "ausente", "docs/pagos.md": "sha256:bb"},
	} {
		if a.AuthHash("tienda", changed) == base {
			t.Errorf("%s y el hash sigue igual", name)
		}
	}
	// Un archivo que el plan no lee no entra al hash.
	extra := map[string]string{"coyote/prd/pagos.md": "sha256:aa", "docs/pagos.md": "sha256:bb", "otro.md": "sha256:dd"}
	if a.AuthHash("tienda", extra) != base {
		t.Error("solo cuentan los archivos que el plan lee")
	}
}

func TestSecciones(t *testing.T) {
	text := "# Informe\n\n## Hallazgos (3)\n- uno\n\n```sh\n# Riesgos\n```\n### **Recomendación**\n#Riesgos sin espacio\n"
	got := MissingSections(text, []string{"Hallazgos", "Riesgos", "recomendación"})
	if len(got) != 1 || got[0] != "Riesgos" {
		t.Errorf("faltantes: %v", got)
	}
	if got := MissingSections("", nil); len(got) != 0 {
		t.Error("sin esquema no falta nada")
	}
}

func TestBuscarYListar(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"W-0001-uno", "W-0002", "W-0003-sin-plan", "W-00040-raro", "notas", "W-0006-con espacio"} {
		if err := os.MkdirAll(filepath.Join(root, Base, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []string{"W-0001-uno", "W-0002", "W-00040-raro", "W-0006-con espacio"} {
		if err := os.WriteFile(filepath.Join(root, Base, d, "plan.yaml"), []byte("id: "+d[:6]+"\nsteps: []\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := All(root)
	if err != nil || strings.Join(ids, ",") != "W-0001,W-0002" {
		t.Errorf("workstreams con plan: %v %v", ids, err)
	}
	p, err := Load(root, "W-0001")
	if err != nil || p.Rel != Base+"/W-0001-uno/plan.yaml" || p.ID != "W-0001" {
		t.Fatalf("Load: %+v %v", p, err)
	}
	if _, err := Load(root, "W-0003"); !NoPlan(err) {
		t.Errorf("sin plan: %v", err)
	}
	if _, err := Load(root, "W-0009"); err == nil || NoPlan(err) {
		t.Errorf("inexistente: %v", err)
	}
	if _, err := Load(root, "../x"); err == nil {
		t.Error("id inválido")
	}
	if _, err := Load(root, "W-0006"); err == nil || !strings.Contains(err.Error(), "no sirve para un workstream") {
		t.Errorf("carpeta con espacio: %v", err)
	}
}

// TestLoadDejaElIDDeLaCarpeta: sexta revisión de v1.0 (N9). El workstream es el
// de su carpeta: Load deja su id en el plan, y el que escribe plan.yaml (otro,
// o ninguno) es un error que Check dice.
func TestLoadDejaElIDDeLaCarpeta(t *testing.T) {
	findingsText := func(fs []Finding) string {
		var out []string
		for _, f := range fs {
			out = append(out, f.String())
		}
		return strings.Join(out, "\n")
	}
	root := t.TempDir()
	for dir, id := range map[string]string{"W-0001-uno": "id: W-0099\n", "W-0002-dos": ""} {
		if err := os.MkdirAll(filepath.Join(root, Base, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		plan := id + "title: x\nbudget_usd: 1\nsteps:\n  - { id: S1, does: hacer, agent: coyote-dev, output: salida, max_usd: 0.3 }\n"
		if err := os.WriteFile(filepath.Join(root, Base, dir, "plan.yaml"), []byte(plan), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, err := Load(root, "W-0001")
	if err != nil || p.ID != "W-0001" || p.Declared != "W-0099" {
		t.Fatalf("el id es el de la carpeta: %+v %v", p, err)
	}
	if got := findingsText(p.Check(CheckOptions{})); !strings.Contains(got, "el id W-0099 no coincide con la carpeta W-0001-uno") {
		t.Errorf("Check dice el id de adentro:\n%s", got)
	}
	p, err = Load(root, "W-0002")
	if err != nil || p.ID != "W-0002" || p.Declared != "" {
		t.Fatalf("sin id adentro: %+v %v", p, err)
	}
	if got := findingsText(p.Check(CheckOptions{})); !strings.Contains(got, "falta id") {
		t.Errorf("Check dice que falta el id:\n%s", got)
	}
}

func TestLock(t *testing.T) {
	root := t.TempDir()
	unlock, err := Lock(root, "W-0001")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(root, "W-0001"); err == nil || !strings.Contains(err.Error(), "ya corre") {
		t.Errorf("dos motores sobre el mismo workstream: %v", err)
	}
	other, err := Lock(root, "W-0002")
	if err != nil {
		t.Fatalf("otro workstream: %v", err)
	}
	other()
	unlock()
	// Suelto, se vuelve a tomar; un archivo que quedó de antes no lo impide.
	unlock, err = Lock(root, "W-0001")
	if err != nil {
		t.Fatalf("lock liberado: %v", err)
	}
	unlock()
	if _, err := Lock(root, "../W"); err == nil {
		t.Error("id inválido")
	}
}

// Dos motores que arrancan a la vez: solo uno toma el lock.
func TestLockEnCarrera(t *testing.T) {
	root := t.TempDir()
	for round := 0; round < 50; round++ {
		var wg sync.WaitGroup
		var mu sync.Mutex
		owners := 0
		unlocks := []func(){}
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if u, err := Lock(root, "W-0003"); err == nil {
					mu.Lock()
					owners++
					unlocks = append(unlocks, u)
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		if owners != 1 {
			t.Fatalf("ronda %d: %d dueños del lock", round, owners)
		}
		for _, u := range unlocks {
			u()
		}
	}
}

func TestFoldAtIgnoraEventosFuturos(t *testing.T) {
	p := mustParse(t, planYAML)
	now := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	ev := []ledger.Entry{
		line(now.Add(-time.Hour), "run", "ok", 0.4, "step:S1", "doc:runs/a.md"),
		line(now.AddDate(73, 0, 0), "run", "ok", 100, "step:S1", "doc:runs/b.md"),
		line(now.AddDate(73, 0, 0), "apr", "ok", 0, "step:S1", "doc:runs/b.md"),
	}
	st := FoldAt(p, ev, now)
	// La aceptación futura no deja avanzar; el gasto futuro cuenta para el tope.
	if st.Spent != 100.4 || st.Steps[0].Status != Review {
		t.Fatalf("aceptación futura fuera, gasto futuro adentro: gasto %v, S1 %s", st.Spent, st.Steps[0].Status)
	}
}

// TestPlanPRDArchivoQueNingunPasoRecibe: quinta revisión de v1.0 (N6). Cuando
// ningún paso recibe el PRD, la revisión del PRD dice cómo está el archivo (un
// enlace o un archivo enorme es un error; un binario, un aviso); cuando un paso
// lo recibe, lo dice solo la revisión de sus entradas, una sola vez.
func TestPlanPRDArchivoQueNingunPasoRecibe(t *testing.T) {
	yaml := func(input string) string {
		in := ""
		if input != "" {
			in = ", input: [" + input + "]"
		}
		return "id: W-0007\nautonomy: supervised\nrisk: R2\nprd: coyote/prd/pagos.md\nsteps:\n" +
			"  - { id: S1, does: leer, agent: coyote-dev, output: x, max_usd: 1" + in + " }\n"
	}
	check := func(input string, state InputState) []string {
		var got []string
		for _, f := range mustParse(t, yaml(input)).Check(CheckOptions{Agents: map[string]bool{"coyote-dev": true}, Project: "supervised",
			Inspect: func(string) InputState { return state }}) {
			got = append(got, f.Level+" "+f.String())
		}
		return got
	}
	has := func(got []string, prefix string) int {
		n := 0
		for _, g := range got {
			if strings.HasPrefix(g, prefix) {
				n++
			}
		}
		return n
	}
	// Nadie lo recibe: el PRD dice cómo está el archivo.
	for state, want := range map[InputState]string{
		InputMissing:  "aviso el PRD coyote/prd/pagos.md no existe todavía",
		InputUnusable: "error el PRD coyote/prd/pagos.md no es un archivo regular dentro del proyecto",
		InputBinary:   "aviso el PRD coyote/prd/pagos.md no es texto: ningún paso podría leerlo",
	} {
		got := check("docs/otro.md", state)
		if has(got, want) != 1 || has(got, "aviso ningún paso recibe el PRD como entrada") != 1 {
			t.Errorf("estado %d: se esperaba %q y el aviso de que nadie lo recibe, y salió %q", state, want, got)
		}
	}
	if got := check("docs/otro.md", InputOK); len(got) != 1 || !strings.HasPrefix(got[0], "aviso ningún paso recibe el PRD") {
		t.Errorf("un archivo legible que nadie recibe solo avisa eso: %q", got)
	}
	// Un paso lo recibe: lo dice la entrada, una vez, y el PRD no lo repite.
	got := check("coyote/prd/pagos.md", InputUnusable)
	if has(got, "error S1: la entrada coyote/prd/pagos.md no es un archivo regular") != 1 || has(got, "error el PRD") != 0 || len(got) != 1 {
		t.Errorf("un PRD ilegible que un paso recibe se dice una vez, en la entrada: %q", got)
	}
}

// TestEntradasSinCaracteresInvisibles: séptima revisión de v1.0 (P1). Un nombre
// que empieza con un carácter sin asignar o sin forma (U+2065, U+FFF0, las
// etiquetas U+E0002, un selector de variación o una marca tras la barra) se
// ve como step:S1 y es un archivo: ninguna ruta de entrada lleva nada que
// Unicode no dibuje, y el barrido lo prueba con todos los puntos de código.
func TestEntradasSinCaracteresInvisibles(t *testing.T) {
	for _, in := range []string{
		"⁥step:S1", "./⁥step:S1", "docs/⁥a.md", "docs/a￰.md", "docs/\U000e0002.md", "docs/\U000e0080.md",
		"./️step:S1", "docs/\U000e0100a.md", "docs/́a.md", "x/‍step:S1", "docs/a.md", "docs/a\U000f0000.md",
		"./︀diff:r=main...HEAD", "file:️step:S1",
	} {
		if got, err := ParseInput(in); err == nil {
			t.Errorf("%q debería ser inválida y dio %v", in, got)
		}
	}
	// Un emoji con su forma de color, un acento combinado y un nombre con ñ siguen valiendo.
	for in, want := range map[string]string{"docs/⚠️ aviso.md": "docs/⚠️ aviso.md", "docs/café.md": "docs/café.md", "docs/a b.md": "docs/a b.md"} {
		if got, err := ParseInput(in); err != nil || got.Value != want {
			t.Errorf("%q: %v %v", in, got, err)
		}
	}
	// Barrido: no se salta ningún punto de código.
	defaultIgnorable := func(r rune) bool {
		return unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Variation_Selector, r)
	}
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if r >= 0xd800 && r <= 0xdfff {
			continue // los sustitutos no son UTF-8 válido
		}
		s := string(r)
		printable := unicode.IsPrint(r)
		for _, in := range []string{"docs/a" + s + "b.md", "docs/" + s + "step:S1"} {
			_, err := ParseInput(in)
			switch {
			case !printable && err == nil:
				t.Fatalf("U+%04X (%q) no se dibuja y pasó en %q", r, s, in)
			case defaultIgnorable(r) && err == nil && strings.HasPrefix(in, "docs/"+s):
				// Al borde de un nombre, un carácter ignorable no puede esconder el prefijo.
				t.Fatalf("U+%04X es ignorable por defecto y pasó al borde de un nombre en %q", r, in)
			}
		}
	}
}

// TestFindConVariasCarpetas: séptima revisión de v1.0 (P2). Find elegía la
// primera carpeta W-NNNN* aunque no tuviera plan.yaml: una carpeta huérfana
// (corridas sueltas, un nombre viejo) escondía el plan de otra, y run --ws lo
// tomaba por un workstream sin plan, sin tope ni cierre.
func TestFindConVariasCarpetas(t *testing.T) {
	root := t.TempDir()
	mk := func(rel string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(Base), filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("W-0001-a/runs/x.md")
	mk("W-0001-b/plan.yaml")
	if got, err := Find(root, "W-0001"); err != nil || got != Base+"/W-0001-b" {
		t.Errorf("con una huérfana y una con plan, la del plan: %q %v", got, err)
	}
	mk("W-0002-a/runs/x.md")
	if got, err := Find(root, "W-0002"); err != nil || got != Base+"/W-0002-a" {
		t.Errorf("una sola carpeta, aunque no tenga plan: %q %v", got, err)
	}
	mk("W-0003/plan.yaml")
	mk("W-0003-pagos/plan.yaml")
	_, err := Find(root, "W-0003")
	if err == nil || !strings.Contains(err.Error(), "2 carpetas para W-0003") || !strings.Contains(err.Error(), "W-0003-pagos") {
		t.Errorf("dos carpetas con plan: %v", err)
	}
	mk("W-0004-a/runs/x.md")
	mk("W-0004-b/runs/y.md")
	if _, err := Find(root, "W-0004"); err == nil || !strings.Contains(err.Error(), "2 carpetas para W-0004") {
		t.Errorf("dos carpetas sin plan: %v", err)
	}
	// W-0005 no es W-00051: el prefijo lleva guion.
	mk("W-00051/plan.yaml")
	if _, err := Find(root, "W-0005"); err == nil || !strings.Contains(err.Error(), "no existe") {
		t.Errorf("W-0005 no existe: %v", err)
	}
	// Un nombre que no sirve se dice, como antes.
	mk("W-0006-mal nombre/plan.yaml")
	if _, err := Find(root, "W-0006"); err == nil || !strings.Contains(err.Error(), "no sirve") {
		t.Errorf("un nombre con espacios: %v", err)
	}
	// Load usa la del plan y deja el id de la carpeta.
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(Base), "W-0001-b", "plan.yaml"), []byte(planYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := Load(root, "W-0001")
	if err != nil || p.Dir != Base+"/W-0001-b" {
		t.Errorf("Load: %v %v", p, err)
	}
	if _, err := Load(root, "W-0002"); !NoPlan(err) {
		t.Errorf("la carpeta sin plan sigue siendo NoPlan: %v", err)
	}
}
