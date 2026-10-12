package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// escribirPagos deja la entrada que pide el primer paso de planSupervisado.
func escribirPagos(t *testing.T, root string) {
	t.Helper()
	write(t, root, "docs/pagos.md", "# Pagos\n\nCobro con reintentos.\n")
}

const planAutonomo = `id: W-0008
title: loop autónomo
autonomy: autonomous
budget_usd: 1
steps:
  - { id: A1, does: analizar, agent: coyote-analyst, output: análisis, max_usd: 0.3 }
  - { id: A2, does: diseñar, agent: coyote-architect, input: [step:A1], output: diseño, max_usd: 0.3, gate: human }
  - { id: A3, does: resumir, agent: coyote-scribe, input: [step:A2], output: resumen, max_usd: 0.3 }
`

// planAutonomoTres corre A1 y A2 sin revisión y se detiene en A3, que pide gate: human.
const planAutonomoTres = `id: W-0008
title: loop autónomo
autonomy: autonomous
budget_usd: 1
steps:
  - { id: A1, does: analizar, agent: coyote-analyst, output: análisis, max_usd: 0.3 }
  - { id: A2, does: diseñar, agent: coyote-architect, input: [step:A1], output: diseño, max_usd: 0.3 }
  - { id: A3, does: resumir, agent: coyote-scribe, input: [step:A2], output: resumen, max_usd: 0.3, gate: human }
`

// autonomoConPlan deja un loop autónomo autorizado por ana y detenido en la
// revisión humana del paso stop; n es cuántos pasos corrieron.
func autonomoConPlan(t *testing.T, plan string, n int, stop string) (base, root, auth string) {
	t.Helper()
	base, root = wsProject(t, "autonomous")
	fake, _ := fakeClaudeWS(t, base)
	must(t, run(t, root, "", "install", "--ide", "claude-code"), 0, "install")
	writePlan(t, root, "W-0008-loop", plan)
	git(t, root, "symbolic-ref", "HEAD", "refs/heads/ws/W-0008")
	r := run(t, root, "", "ws", "run", "W-0008")
	must(t, r, 1, "sin autorización")
	auth = propRe.FindString(r.stderr)
	must(t, run(t, root, "", "approve", auth, "--uses", "3"), 0, "approve")
	r = run(t, root, "", "ws", "run", "W-0008")
	must(t, r, 0, "loop autónomo")
	if n != calls(t, fake) || !strings.Contains(r.stdout, stop+" espera tu revisión") {
		t.Fatalf("loop:\n%s\n%s", r.stdout, r.stderr)
	}
	return base, root, auth
}

// autonomoEnRevision deja un loop autónomo autorizado por ana y detenido en la
// revisión humana de A2: A1 corrió sin revisión.
func autonomoEnRevision(t *testing.T) (base, root, auth string) {
	t.Helper()
	return autonomoConPlan(t, planAutonomo, 2, "A2")
}

// TestCloseSoloHablaDeA4EnLoQueCorrioEnAutonomo: séptima revisión de v1.0 (P3).
// close.md decía siempre que lo que corrió sin revisión lo evalúa quien
// autorizó el plan (A4), aunque el plan fuera supervised o manual y nada
// hubiera corrido sin revisión ni nadie autorizado.
func TestCloseSoloHablaDeA4EnLoQueCorrioEnAutonomo(t *testing.T) {
	base, root := wsProject(t, "supervised")
	fakeClaudeWS(t, base)
	must(t, run(t, root, "", "install", "--ide", "claude-code"), 0, "install")
	writePlan(t, root, "W-0003-pagos", planSupervisado)
	escribirPagos(t, root)
	must(t, run(t, root, "", "ws", "run", "W-0003"), 0, "ws run S1")
	r := run(t, root, "", "close", "W-0003", "--dry-run")
	must(t, r, 0, "close supervised")
	if !strings.Contains(r.stdout, "4 pasos quedan sin cerrar.") || strings.Contains(r.stdout, "A4") || strings.Contains(r.stdout, "sin revisión") {
		t.Errorf("un plan supervised no tiene nada sin revisión:\n%s", r.stdout)
	}

	_, root, _ = autonomoEnRevision(t)
	r = run(t, root, "", "close", "W-0008", "--dry-run")
	must(t, r, 0, "close autónomo")
	if !strings.Contains(r.stdout, "3 pasos quedan sin cerrar. De ellos, 1 corrió sin revisión en el loop autónomo: lo evalúa quien autorizó el plan (A4).") {
		t.Errorf("lo que corrió en el loop autónomo lo evalúa quien lo autorizó:\n%s", r.stdout)
	}

	// En plural, y un paso con gate: human no cuenta: el motor se detuvo a esperar a la persona.
	_, root, _ = autonomoConPlan(t, planAutonomoTres, 3, "A3")
	r = run(t, root, "", "close", "W-0008", "--dry-run")
	must(t, r, 0, "close autónomo con dos")
	if !strings.Contains(r.stdout, "3 pasos quedan sin cerrar. De ellos, 2 corrieron sin revisión en el loop autónomo: los evalúa quien autorizó el plan (A4).") {
		t.Errorf("dos pasos corrieron sin revisión:\n%s", r.stdout)
	}
}

// TestWsElAvisoDelEvaluadorNoSeCallaConElModo: séptima revisión de v1.0 (P4).
// Aceptar con --mode supervised lo que corrió en el loop autónomo no calla el
// aviso de que A4 pide que lo evalúe quien lo autorizó.
func TestWsElAvisoDelEvaluadorNoSeCallaConElModo(t *testing.T) {
	_, root, auth := autonomoEnRevision(t)
	t.Setenv("COYOTE_USER", "luis")
	r := run(t, root, "", "ws", "continue", "W-0008", "--mode", "supervised")
	must(t, r, 0, "continue supervised")
	want := "aviso: @ana autorizó el loop autónomo (" + auth + ") y lo evalúa @luis; A4 pide que lo evalúe quien lo autorizó"
	if !strings.Contains(r.stderr, want) {
		t.Errorf("--mode supervised:\n%s", r.stderr)
	}

	// Con dos pasos del loop aceptados de una vez, el aviso sale una sola vez.
	_, root, _ = autonomoEnRevision(t)
	t.Setenv("COYOTE_USER", "luis")
	r = run(t, root, "", "ws", "continue", "W-0008")
	must(t, r, 0, "continue en autonomous")
	if !strings.Contains(r.stdout, "✓ A1 aceptado") || !strings.Contains(r.stdout, "✓ A2 aceptado") || strings.Count(r.stderr, "A4 pide") != 1 {
		t.Errorf("aceptar A1 y A2 avisa una vez:\n%s%s", r.stdout, r.stderr)
	}

	// Quien lo autorizó no recibe el aviso, en ningún modo.
	_, root, _ = autonomoEnRevision(t)
	t.Setenv("COYOTE_USER", "ana")
	r = run(t, root, "", "ws", "continue", "W-0008", "--mode", "supervised")
	must(t, r, 0, "continue de quien autorizó")
	if strings.Contains(r.stderr, "A4 pide") {
		t.Errorf("la misma persona no recibe el aviso:\n%s", r.stderr)
	}

	// Lo que se rehizo sin el loop autónomo (supervised) tampoco lo da, aunque
	// el plan tenga un loop autorizado por otra persona.
	_, root, _ = autonomoEnRevision(t)
	t.Setenv("COYOTE_USER", "ana")
	must(t, run(t, root, "", "ws", "continue", "W-0008", "--redo", "otra vez", "--step", "A1", "--mode", "supervised"), 0, "redo supervised")
	t.Setenv("COYOTE_USER", "luis")
	r = run(t, root, "", "ws", "continue", "W-0008", "--mode", "supervised")
	must(t, r, 0, "continue de lo rehecho en supervised")
	if !strings.Contains(r.stdout, "✓ A1 aceptado") || strings.Contains(r.stdout, "✓ A2 aceptado") || strings.Contains(r.stderr, "A4 pide") {
		t.Errorf("A1 se rehízo en supervised: no hay aviso del loop (stderr):\n%s%s", r.stdout, r.stderr)
	}

	// Un plan supervised, sin loop autónomo, tampoco lo da.
	base, root := wsProject(t, "supervised")
	fakeClaudeWS(t, base)
	must(t, run(t, root, "", "install", "--ide", "claude-code"), 0, "install")
	writePlan(t, root, "W-0003-pagos", planSupervisado)
	escribirPagos(t, root)
	must(t, run(t, root, "", "ws", "run", "W-0003"), 0, "ws run S1")
	t.Setenv("COYOTE_USER", "luis")
	r = run(t, root, "", "ws", "continue", "W-0003")
	if strings.Contains(r.stderr, "A4 pide") {
		t.Errorf("un plan supervised no tiene loop autónomo:\n%s", r.stderr)
	}
}

// TestWsStatusJSONNoDiceQueSePuedeCorrerConErrores: séptima revisión de v1.0
// (P5). Con errores en el plan que ningún modo salva, ws run y ws continue se
// niegan; el campo next.run para máquinas lo dice igual que el texto.
func TestWsStatusJSONNoDiceQueSePuedeCorrerConErrores(t *testing.T) {
	base, root := wsProject(t, "supervised")
	fakeClaudeWS(t, base)
	// El id de adentro no es el de la carpeta: un error de cualquier modo.
	writePlan(t, root, "W-0007-x", strings.Replace(planSupervisado, "id: W-0003", "id: W-0099", 1))
	r := run(t, root, "", "ws", "status", "W-0007", "--json")
	must(t, r, 0, "ws status --json")
	var out struct {
		Next struct {
			Run    bool   `json:"run"`
			Reason string `json:"reason"`
			Hint   string `json:"hint"`
		} `json:"next"`
		Findings []struct {
			Level string `json:"level"`
		} `json:"findings"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil {
		t.Fatalf("json: %v\n%s", err, r.stdout)
	}
	errs := 0
	for _, f := range out.Findings {
		if f.Level == "error" {
			errs++
		}
	}
	if errs == 0 || out.Next.Run || out.Next.Reason != "plan" || !strings.Contains(out.Next.Hint, "corrige el plan") {
		t.Errorf("con errores no se puede correr (errores %d): %+v\n%s", errs, out.Next, r.stdout)
	}
	// El paso que muestra es donde se detendría el motor en el modo del plan: con
	// un loop autónomo, el que espera la revisión humana y no el primero por revisar.
	_, raiz, _ := autonomoEnRevision(t)
	writePlan(t, raiz, "W-0008-loop", strings.Replace(planAutonomo, "title: loop autónomo", "title: loop autónomo\nrisk: R9", 1))
	r = run(t, raiz, "", "ws", "status", "W-0008", "--json")
	must(t, r, 0, "ws status --json autónomo con errores")
	var auto struct {
		Next struct {
			Run    bool   `json:"run"`
			Step   string `json:"step"`
			Reason string `json:"reason"`
		} `json:"next"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &auto); err != nil {
		t.Fatalf("json: %v\n%s", err, r.stdout)
	}
	if auto.Next.Run || auto.Next.Reason != "plan" || auto.Next.Step != "A2" {
		t.Errorf("con errores, el motor autónomo se detendría en A2 y no corre: %+v\n%s", auto.Next, r.stdout)
	}
	// Corregido el plan, vuelve a poder correr.
	writePlan(t, root, "W-0007-x", strings.Replace(planSupervisado, "id: W-0003", "id: W-0007", 1))
	r = run(t, root, "", "ws", "status", "W-0007", "--json")
	must(t, r, 0, "ws status --json corregido")
	out.Next.Run, out.Next.Reason = false, "plan"
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Next.Run || out.Next.Reason != "" {
		t.Errorf("sin errores el siguiente paso corre:\n%s", r.stdout)
	}
}

// TestRunDeUnWsConCarpetasDuplicadas: séptima revisión de v1.0 (P2). Una carpeta
// huérfana con el mismo id no esconde el plan: run --ws respeta su tope. Con
// dos carpetas con plan, o dos sin plan, no se elige a ciegas.
func TestRunDeUnWsConCarpetasDuplicadas(t *testing.T) {
	base, root := wsProject(t, "supervised")
	fakeClaudeWS(t, base)
	must(t, run(t, root, "", "install", "--ide", "claude-code"), 0, "install")
	write(t, root, "coyote/workstreams/W-0003-a/runs/x.md", "x\n")
	tope := strings.Replace(planSupervisado, "budget_usd: 1", "budget_usd: 0.005", 1)
	writePlan(t, root, "W-0003-b", tope)
	r := run(t, root, "", "run", "--agent", "coyote-dev", "--ws", "W-0003", "--dry-run", "algo")
	if r.code != 1 || !strings.Contains(r.stderr, "llegó a su tope") {
		t.Errorf("run --ws con un plan sin presupuesto (código %d):\n%s%s", r.code, r.stdout, r.stderr)
	}
	r = run(t, root, "", "ws", "status", "W-0003")
	must(t, r, 0, "ws status")
	if strings.Contains(r.stdout, "no tiene plan") || !strings.Contains(r.stdout, "S1") {
		t.Errorf("el plan de W-0003 se ve:\n%s", r.stdout)
	}

	// Dos carpetas con plan.
	writePlan(t, root, "W-0003-c", tope)
	for name, args := range map[string][]string{
		"ws check":  {"ws", "check", "W-0003"},
		"ws status": {"ws", "status", "W-0003"},
		"ws run":    {"ws", "run", "W-0003"},
		"run --ws":  {"run", "--agent", "coyote-dev", "--ws", "W-0003", "--dry-run", "algo"},
	} {
		r := run(t, root, "", args...)
		if r.code == 0 || !strings.Contains(r.stderr, "2 carpetas para W-0003") {
			t.Errorf("%s con dos carpetas con plan (código %d):\n%s%s", name, r.code, r.stdout, r.stderr)
		}
	}

	// Dos carpetas sin plan: run --ws no lo toma por un workstream libre.
	write(t, root, "coyote/workstreams/W-0005-a/runs/x.md", "x\n")
	write(t, root, "coyote/workstreams/W-0005-b/runs/y.md", "y\n")
	r = run(t, root, "", "run", "--agent", "coyote-dev", "--ws", "W-0005", "--dry-run", "algo")
	if r.code != 1 || !strings.Contains(r.stderr, "2 carpetas para W-0005") {
		t.Errorf("run --ws con dos carpetas sin plan (código %d):\n%s%s", r.code, r.stdout, r.stderr)
	}
	// Una sola carpeta sin plan sí es un workstream libre, como antes.
	write(t, root, "coyote/workstreams/W-0006-a/runs/x.md", "x\n")
	if r := run(t, root, "", "run", "--agent", "coyote-dev", "--ws", "W-0006", "--dry-run", "algo"); r.code != 0 {
		t.Errorf("run --ws de una carpeta sin plan (código %d):\n%s%s", r.code, r.stdout, r.stderr)
	}
}
