package workstream

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Emmanuel93/coyote/internal/ccf"
	"github.com/Emmanuel93/coyote/internal/ledger"
)

const planYAML = `id: W-0007
title: revisión de pagos
autonomy: supervised
risk: R2
budget_usd: 3
steps:
  - id: S1
    does: revisar el impacto del cambio
    agent: coyote-reviewer
    input: [diff:servicios=main...HEAD, docs/pagos.md]
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
	ev = append(ev, line(t0, "run", "ok", 0.4, "step:S1", "doc:runs/a.md"))
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
	ev = append(ev, line(t0, "run", "ok", 0.1, "step:S4", "doc:runs/e.md"))
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
	if a.AuthHash("tienda") == b.AuthHash("tienda") || a.AuthHash("tienda") == a.AuthHash("otra") {
		t.Error("cambiar el plan o el proyecto cambia la autorización")
	}
	if h := a.AuthHash("tienda"); !strings.HasPrefix(h, "sha256:") || len(h) != 71 || h != mustParse(t, planYAML).AuthHash("tienda") {
		t.Errorf("hash: %s", h)
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
