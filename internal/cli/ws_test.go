package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeClaudeWS instala un claude simulado para el motor: cada corrida deja
// sus argumentos y su entrada numerados, responde el texto de out y, si
// existe el archivo propose, deja esa acción en la cola del gate como lo
// haría el hook; si existe mutate, lo corre con el número de la corrida antes
// de responder (alguien edita un archivo mientras el motor trabaja).
func fakeClaudeWS(t *testing.T, base string) (dir string, say func(text string)) {
	t.Helper()
	dir = filepath.Join(base, "claude-ws")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nd=\"" + dir + "\"\n" +
		"n=$(cat \"$d/count\" 2>/dev/null || echo 0); n=$((n+1)); echo \"$n\" > \"$d/count\"\n" +
		"printf '%s\\n' \"$@\" > \"$d/args.$n\"\n" +
		"cat > \"$d/stdin.$n\"\n" +
		"if [ -f \"$d/propose\" ]; then mkdir -p .coyote/proposals && cp \"$d/propose\" .coyote/proposals/P-ws2222.json; fi\n" +
		"if [ -f \"$d/mutate\" ]; then sh \"$d/mutate\" \"$n\"; fi\n" +
		"cat \"$d/out\"\n"
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COYOTE_CLAUDE", bin)
	say = func(text string) {
		t.Helper()
		out := map[string]any{"type": "result", "subtype": "success", "is_error": false, "result": text,
			"session_id": "sesion-123456789", "num_turns": 3, "total_cost_usd": 0.04,
			"usage":      map[string]any{"input_tokens": 1000, "cache_read_input_tokens": 3000, "output_tokens": 500},
			"modelUsage": map[string]any{"claude-sonnet-x": map[string]any{"inputTokens": 1000, "outputTokens": 500, "cacheReadInputTokens": 3000, "costUSD": 0.04}}}
		b, _ := json.Marshal(out)
		if err := os.WriteFile(filepath.Join(dir, "out"), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	say("## Propuesta\n\nlisto")
	return dir, say
}

func calls(t *testing.T, dir string) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "count"))
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return n
}

func callFile(t *testing.T, dir, kind string, n int) string {
	t.Helper()
	return readFile(t, filepath.Join(dir, kind+"."+strconv.Itoa(n)))
}

// wsProject prepara un proyecto con el gate instalado, un coyote falso en el
// PATH (el hook lo llama) y un workstream con su plan.
func wsProject(t *testing.T, autonomy string) (base, root string) {
	t.Helper()
	base, root = gateProject(t)
	bin := filepath.Join(base, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "coyote"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	cfgPath := filepath.Join(root, "coyote", "project.yaml")
	cfg := strings.Replace(readFile(t, cfgPath), "autonomy: manual", "autonomy: "+autonomy, 1)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return base, root
}

func writePlan(t *testing.T, root, dir, plan string) string {
	t.Helper()
	p := filepath.Join(root, "coyote", "workstreams", dir, "plan.yaml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(plan), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const planSupervisado = `id: W-0003
title: pagos con revisión
autonomy: supervised
risk: R2
budget_usd: 1
steps:
  - id: S1
    does: revisar el diseño de pagos
    agent: coyote-reviewer
    input: [docs/pagos.md]
    output: informe de revisión
    sections: [Propuesta]
    max_usd: 0.5
  - id: S2
    does: proponer el cambio
    agent: coyote-dev
    input: [step:S1]
    output: [parche como diff]
    max_usd: 0.3
  - { id: S3, does: probar en staging, agent: persona }
  - { id: S4, does: resumir para el PR, agent: coyote-scribe, input: [step:S2], output: texto del PR, sections: [Resumen], max_usd: 0.2 }
`

func TestMotorSupervisado(t *testing.T) {
	base, root := wsProject(t, "supervised")
	fake, say := fakeClaudeWS(t, base)
	writePlan(t, root, "W-0003-pagos", planSupervisado)
	docs := filepath.Join(root, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, "pagos.md"), []byte("# Pagos\n\nCobro con ```reintentos``` y ````cercas````.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// El plan cumple el contrato; sin el gate instalado, el motor no corre.
	r := run(t, root, "", "ws", "check", "W-0003")
	must(t, r, 0, "ws check")
	if !strings.Contains(r.stdout, "Modo supervised (plan supervised, proyecto supervised)") || !strings.Contains(r.stdout, "el gate no está instalado") {
		t.Errorf("ws check:\n%s", r.stdout)
	}
	r = run(t, root, "", "ws", "run", "W-0003")
	must(t, r, 1, "ws run sin gate")
	if !strings.Contains(r.stderr, "el gate no está instalado") || !strings.Contains(ledgerText(t, root), "sin gate: revisar el diseño de pagos|step:S1") {
		t.Errorf("sin gate:\n%s\n%s", r.stderr, ledgerText(t, root))
	}
	must(t, run(t, root, "", "install", "--ide", "claude-code"), 0, "install")

	// --dry-run muestra el paso y su entrada, sin correr.
	r = run(t, root, "", "ws", "run", "W-0003", "--dry-run")
	must(t, r, 0, "dry-run")
	if !strings.Contains(r.stdout, "Paso S1 de W-0003, pagos con revisión (1 de 4), en modo supervised.") || calls(t, fake) != 0 {
		t.Errorf("dry-run:\n%s", r.stdout)
	}

	// S1 corre y el motor se detiene a revisar.
	r = run(t, root, "", "ws", "run", "W-0003")
	must(t, r, 0, "ws run S1")
	if calls(t, fake) != 1 || !strings.Contains(r.stdout, "S1 espera tu revisión") {
		t.Fatalf("S1:\n%s\n%s", r.stdout, r.stderr)
	}
	args, in := callFile(t, fake, "args", 1), callFile(t, fake, "stdin", 1)
	if !strings.Contains(args, "--agent\ncoyote-reviewer") || !strings.Contains(args, "--max-budget-usd\n0.50") || !strings.Contains(args, "--model\nopus") {
		t.Errorf("argumentos de S1:\n%s", args)
	}
	for _, want := range []string{"# Tarea", "Paso S1 de W-0003", "revisar el diseño de pagos", "# Entradas del paso", "## docs/pagos.md (docs/pagos.md)",
		"`````\n# Pagos", "# Salida esperada", "- informe de revisión", "títulos de Markdown: ## Propuesta", "# Cómo entregar"} {
		if !strings.Contains(in, want) {
			t.Errorf("la entrada de S1 no trae %q:\n%s", want, in)
		}
	}
	lg := ledgerText(t, root)
	if !strings.Contains(lg, "|W-0003|tienda|run|") || !strings.Contains(lg, "step:S1") || !strings.Contains(lg, "mode:supervised") ||
		!strings.Contains(lg, "|plan|-|motor supervised: 1 paso, se detiene por revisión|mode:supervised stop:review at:S1|-|-|ok") {
		t.Errorf("ledger tras S1:\n%s", lg)
	}
	// Sin revisión no sigue.
	must(t, run(t, root, "", "ws", "run", "W-0003"), 0, "ws run sin revisar")
	if calls(t, fake) != 1 {
		t.Fatal("el motor no corre S2 sin la revisión de S1")
	}
	// Revisar es de la persona: ni fuera de una terminal ni desde un agente.
	isTerminal = func() bool { return false }
	must(t, run(t, root, "", "ws", "continue", "W-0003"), 1, "continue sin terminal")
	isTerminal = func() bool { return true }
	t.Setenv("CLAUDECODE", "1")
	must(t, run(t, root, "", "ws", "continue", "W-0003"), 1, "continue desde un agente")
	must(t, run(t, root, "", "ws", "run", "W-0003"), 1, "run desde un agente")
	t.Setenv("CLAUDECODE", "")

	// continue acepta S1 y corre S2 con la entrega de S1 como entrada.
	say("## Parche\n\n```diff\n+ cobro idempotente\n```")
	r = run(t, root, "", "ws", "continue", "W-0003", "--note", "bien, sigue")
	must(t, r, 0, "continue S2")
	if calls(t, fake) != 2 || !strings.Contains(r.stdout, "✓ S1 aceptado") || !strings.Contains(r.stdout, "S2 espera tu revisión") {
		t.Fatalf("S2:\n%s\n%s", r.stdout, r.stderr)
	}
	in = callFile(t, fake, "stdin", 2)
	if !strings.Contains(in, "## Entrega del paso S1 (coyote/workstreams/W-0003-pagos/runs/") || !strings.Contains(in, "## Propuesta") || strings.Contains(in, "session: \"") {
		t.Errorf("S2 recibe la entrega de S1 sin su frontmatter:\n%s", in)
	}
	if !strings.Contains(ledgerText(t, root), "|apr|-|revisado S1: bien, sigue|step:S1 doc:coyote/workstreams/W-0003-pagos/runs/") {
		t.Errorf("revisión en el ledger:\n%s", ledgerText(t, root))
	}

	// --redo guarda lo que la persona pide y el agente ve su entrega anterior.
	r = run(t, root, "", "ws", "continue", "W-0003", "--redo", "usa una llave de idempotencia por cobro")
	must(t, r, 0, "redo S2")
	in = callFile(t, fake, "stdin", 3)
	if !strings.Contains(in, "## Tu entrega anterior") || !strings.Contains(in, "+ cobro idempotente") || !strings.Contains(in, "## Lo que la persona pide cambiar") ||
		!strings.Contains(in, "usa una llave de idempotencia por cobro") {
		t.Errorf("entrada del paso rehecho:\n%s", in)
	}
	revs, _ := filepath.Glob(filepath.Join(root, "coyote", "workstreams", "W-0003-pagos", "runs", "*-S2-revision.md"))
	if len(revs) != 1 || !strings.Contains(ledgerText(t, root), "|rej|-|rehacer S2: usa una llave") {
		t.Errorf("revisión guardada: %v\n%s", revs, ledgerText(t, root))
	}
	// --retry no aplica a un paso que salió bien; --step sin --redo tampoco.
	must(t, run(t, root, "", "ws", "continue", "W-0003", "--retry"), 1, "retry de un paso por revisar")
	must(t, run(t, root, "", "ws", "continue", "W-0003", "--step", "S1"), 2, "step sin redo")

	// S3 es de la persona: el motor se detiene sin correr nada.
	r = run(t, root, "", "ws", "continue", "W-0003")
	must(t, r, 0, "continue hasta S3")
	if calls(t, fake) != 3 || !strings.Contains(r.stdout, "S3 lo haces tú") {
		t.Fatalf("S3:\n%s", r.stdout)
	}
	// La persona termina S3; S4 deja una acción en la cola del gate.
	prop := `{"id":"P-ws2222","hash":"sha256:` + strings.Repeat("ab", 32) + `","object":"git push origin ws/W-0003","tool":"Bash","kind":"shell","cwd":".","command":"git push origin ws/W-0003","requested_by":"@ana/coyote-scribe","first_seen":"2099-01-01T00:00:00Z","last_seen":"2099-01-01T00:00:00Z","attempts":1,"status":"pending"}`
	if err := os.WriteFile(filepath.Join(fake, "propose"), []byte(prop), 0o644); err != nil {
		t.Fatal(err)
	}
	say("## Resumen\n\nfalta publicar la rama")
	r = run(t, root, "", "ws", "continue", "W-0003", "--note", "staging ok")
	must(t, r, 0, "continue S4")
	if calls(t, fake) != 4 || !strings.Contains(r.stdout, "✓ S3 hecho") || !strings.Contains(r.stdout, "S4 dejó acciones en la cola del gate") {
		t.Fatalf("S4 en la cola:\n%s\n%s", r.stdout, r.stderr)
	}
	os.Remove(filepath.Join(fake, "propose"))
	// Con la acción sin decidir no sigue; decidida, retoma la sesión.
	r = run(t, root, "", "ws", "continue", "W-0003")
	must(t, r, 1, "continue con la cola pendiente")
	if !strings.Contains(r.stderr, "quedan 1 acción sin decidir") && !strings.Contains(r.stderr, "sin decidir") {
		t.Errorf("cola pendiente:\n%s", r.stderr)
	}
	must(t, run(t, root, "", "reject", "P-ws2222", "--reason", "publica la persona"), 0, "reject")
	say("## Notas\n\nsin la sección pedida")
	r = run(t, root, "", "ws", "continue", "W-0003")
	must(t, r, 1, "resume sin la sección")
	args, in = callFile(t, fake, "args", 5), callFile(t, fake, "stdin", 5)
	if !strings.Contains(args, "--resume\nsesion-123456789") || !strings.Contains(in, "# Continúa") || strings.Contains(in, "# Entradas del paso") {
		t.Errorf("retomar la sesión:\n%s\n%s", args, in)
	}
	if !strings.Contains(r.stderr, "la entrega no trae la sección: Resumen") || !strings.Contains(r.stdout, "S4 falló") {
		t.Errorf("esquema de salida:\n%s\n%s", r.stdout, r.stderr)
	}
	// Un paso que falló pide --retry o --redo.
	must(t, run(t, root, "", "ws", "continue", "W-0003"), 1, "continue de un paso que falló")
	say("## Resumen\n\nlisto para el PR")
	r = run(t, root, "", "ws", "continue", "W-0003", "--retry")
	must(t, r, 0, "retry S4")
	if args := callFile(t, fake, "args", 6); strings.Contains(args, "--resume") {
		t.Errorf("--retry corre de nuevo, sin retomar:\n%s", args)
	}
	// ws status resume el plan; continue lo completa.
	r = run(t, root, "", "ws", "status", "W-0003")
	must(t, r, 0, "status")
	for _, want := range []string{"W-0003 · pagos con revisión · supervised · $0.24 de $1.00 del plan", "S1 coyote-reviewer hecho 1 $0.0400", "S2 coyote-dev hecho 2 $0.0800",
		"S3 persona hecho - -", "S4 coyote-scribe por revisar 3 $0.1200", "Sigue: S4 espera tu revisión"} {
		if !strings.Contains(strings.Join(strings.Fields(r.stdout), " "), want) {
			t.Errorf("status sin %q:\n%s", want, r.stdout)
		}
	}
	r = run(t, root, "", "ws", "continue", "W-0003")
	must(t, r, 0, "continue final")
	if !strings.Contains(r.stdout, "El plan está completo: ciérralo con coyote close W-0003") {
		t.Errorf("final:\n%s", r.stdout)
	}
	r = run(t, root, "", "ws", "status", "--json")
	must(t, r, 0, "status --json")
	var rows []map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &rows); err != nil || len(rows) != 1 || rows[0]["next"] != "completo" || rows[0]["done"] != float64(4) {
		t.Errorf("status --json: %v\n%s", err, r.stdout)
	}
	must(t, run(t, root, "", "close", "W-0003"), 0, "close")
	if !strings.Contains(ledgerText(t, root), "|close|-|cierre de W-0003: 6 corridas") {
		t.Errorf("cierre:\n%s", ledgerText(t, root))
	}
	cl := readFile(t, filepath.Join(root, "coyote", "workstreams", "W-0003-pagos", "close.md"))
	if !strings.Contains(cl, "### Pasos del plan") || !strings.Contains(cl, "| S2 | coyote-dev | hecho | 2 | $0.0800 |") || strings.Contains(cl, "sin cerrar") {
		t.Errorf("close.md con los pasos:\n%s", cl)
	}
}

func TestMotorTopeDelPlanYManual(t *testing.T) {
	base, root := wsProject(t, "supervised")
	fake, _ := fakeClaudeWS(t, base)
	must(t, run(t, root, "", "install", "--ide", "claude-code"), 0, "install")
	writePlan(t, root, "W-0004", `id: W-0004
autonomy: supervised
budget_usd: 0.06
steps:
  - { id: S1, does: uno, agent: coyote-scribe, output: texto, max_usd: 0.05 }
  - { id: S2, does: dos, agent: coyote-scribe, output: texto, max_usd: 0.05 }
  - { id: S3, does: tres, agent: coyote-scribe, output: texto, max_usd: 0.05 }
`)
	r := run(t, root, "", "ws", "check", "W-0004")
	must(t, r, 0, "check con aviso")
	if !strings.Contains(r.stdout, "los topes de los pasos suman $0.15, más que el del plan ($0.06)") {
		t.Errorf("aviso de topes:\n%s", r.stdout)
	}
	must(t, run(t, root, "", "ws", "run", "W-0004"), 0, "S1")
	must(t, run(t, root, "", "ws", "continue", "W-0004"), 0, "S2")
	if args := callFile(t, fake, "args", 2); !strings.Contains(args, "--max-budget-usd\n0.02") {
		t.Errorf("S2 corre con lo que queda del plan:\n%s", args)
	}
	r = run(t, root, "", "ws", "continue", "W-0004")
	must(t, r, 1, "tope del plan")
	if calls(t, fake) != 2 || !strings.Contains(r.stdout, "El plan llegó a su tope ($0.08 de $0.06 del plan)") {
		t.Errorf("tope:\n%s\n%s", r.stdout, r.stderr)
	}

	// En manual, continue solo registra la revisión; cada paso lo lanza ws run.
	writePlan(t, root, "W-0005", `id: W-0005
steps:
  - { id: M1, does: uno, agent: coyote-scribe, output: texto, max_usd: 0.5 }
  - { id: M2, does: dos, agent: coyote-scribe, output: texto, max_usd: 0.5 }
`)
	must(t, run(t, root, "", "ws", "continue", "W-0005"), 1, "manual sin nada que revisar")
	must(t, run(t, root, "", "ws", "run", "W-0005"), 0, "M1")
	r = run(t, root, "", "ws", "continue", "W-0005")
	must(t, r, 0, "manual acepta M1")
	if calls(t, fake) != 3 || !strings.Contains(r.stdout, "Sigue: M2 (coyote-scribe, dos): corre con coyote ws run W-0005") {
		t.Errorf("manual:\n%s", r.stdout)
	}
	// --mode no sube la autonomía.
	must(t, run(t, root, "", "ws", "run", "W-0005", "--mode", "autonomous"), 2, "--mode más alto")

	// Un plan con errores no corre y una entrada que falta detiene al motor.
	writePlan(t, root, "W-0006", "id: W-0006\nsteps:\n  - { id: X1, does: sin tope, agent: coyote-dev, output: x }\n")
	r = run(t, root, "", "ws", "run", "W-0006")
	must(t, r, 1, "plan inválido")
	if !strings.Contains(r.stdout, "✗ X1: falta max_usd") || !strings.Contains(r.stderr, "el plan tiene 1 error (A2)") {
		t.Errorf("plan inválido:\n%s\n%s", r.stdout, r.stderr)
	}
	writePlan(t, root, "W-0007", "id: W-0007\nsteps:\n  - { id: Y1, does: leer, agent: coyote-dev, output: x, max_usd: 0.5, input: [docs/no-existe.md] }\n")
	must(t, run(t, root, "", "ws", "check", "W-0007"), 0, "entrada que falta es un aviso")
	r = run(t, root, "", "ws", "run", "W-0007")
	must(t, r, 1, "entrada que falta")
	if !strings.Contains(r.stderr, "no puedo leer la entrada docs/no-existe.md") || !strings.Contains(ledgerText(t, root), "sin entrada: leer|step:Y1") {
		t.Errorf("entrada que falta:\n%s\n%s", r.stderr, ledgerText(t, root))
	}
	must(t, run(t, root, "", "ws", "status", "W-0099"), 1, "workstream inexistente")
	must(t, run(t, root, "", "ws", "run"), 2, "sin workstream")
	must(t, run(t, root, "", "ws", "borra", "W-0004"), 2, "subcomando desconocido")
}

func TestMotorAutonomo(t *testing.T) {
	base, root := wsProject(t, "autonomous")
	fake, say := fakeClaudeWS(t, base)
	must(t, run(t, root, "", "install", "--ide", "claude-code"), 0, "install")
	plan := `id: W-0008
title: loop autónomo
autonomy: autonomous
budget_usd: 1
steps:
  - { id: A1, does: analizar, agent: coyote-analyst, output: análisis, max_usd: 0.3 }
  - { id: A2, does: diseñar, agent: coyote-architect, input: [step:A1], output: diseño, max_usd: 0.3, gate: human }
  - { id: A3, does: resumir, agent: coyote-scribe, input: [step:A2], output: resumen, max_usd: 0.3 }
`
	planPath := writePlan(t, root, "W-0008-loop", plan)

	// A4: fuera de la rama ws/W-0008 no corre.
	r := run(t, root, "", "ws", "run", "W-0008")
	must(t, r, 1, "autonomous fuera de su rama")
	if !strings.Contains(r.stderr, "A4: autonomous corre en la rama ws/W-0008") || calls(t, fake) != 0 {
		t.Fatalf("rama:\n%s", r.stderr)
	}
	git(t, root, "symbolic-ref", "HEAD", "refs/heads/ws/W-0008")

	// Sin autorización del plan exacto, la pide en la cola del gate.
	r = run(t, root, "", "ws", "run", "W-0008")
	must(t, r, 1, "sin autorización")
	id := propRe.FindString(r.stderr)
	if id == "" || !strings.Contains(r.stderr, "coyote approve "+id) || calls(t, fake) != 0 {
		t.Fatalf("autorización:\n%s", r.stderr)
	}
	r = run(t, root, "", "review", id)
	must(t, r, 0, "review")
	if !strings.Contains(r.stdout, "motor autónomo de W-0008: 3 pasos, tope del plan $1.00, rama ws/W-0008") || !strings.Contains(r.stdout, "    - { id: A2, does: diseñar") {
		t.Errorf("review del plan:\n%s", r.stdout)
	}
	must(t, run(t, root, "", "approve", id, "--uses", "2"), 0, "approve")

	// Cambiar el plan invalida la autorización.
	if err := os.WriteFile(planPath, []byte(plan+"# otro tope\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r = run(t, root, "", "ws", "run", "W-0008")
	must(t, r, 1, "plan cambiado")
	if other := propRe.FindString(r.stderr); other == "" || other == id {
		t.Errorf("un plan cambiado pide otra autorización:\n%s", r.stderr)
	}
	if err := os.WriteFile(planPath, []byte(plan), 0o644); err != nil {
		t.Fatal(err)
	}

	// Autorizado: corre A1 y A2 sin detenerse; A2 pide revisión humana.
	r = run(t, root, "", "ws", "run", "W-0008")
	must(t, r, 0, "loop autónomo")
	if calls(t, fake) != 2 || !strings.Contains(r.stdout, "A2 espera tu revisión") || !strings.Contains(r.stderr, "autonomous: @ana lo autorizó ("+id+"; quedan 1 uso)") {
		t.Fatalf("loop:\n%s\n%s", r.stdout, r.stderr)
	}
	if in := callFile(t, fake, "stdin", 2); !strings.Contains(in, "## Entrega del paso A1") || !strings.Contains(in, "en modo autonomous") {
		t.Errorf("A2 recibe la entrega de A1:\n%s", in)
	}
	// continue acepta A1 y A2 y sigue con A3; al final queda la evaluación (A4).
	say("## Resumen final")
	r = run(t, root, "", "ws", "continue", "W-0008")
	must(t, r, 0, "continue autónomo")
	if calls(t, fake) != 3 || !strings.Contains(r.stdout, "✓ A1 aceptado") || !strings.Contains(r.stdout, "✓ A2 aceptado") ||
		!strings.Contains(r.stdout, "evalúa A3 en la rama ws/W-0008") {
		t.Fatalf("evaluación:\n%s\n%s", r.stdout, r.stderr)
	}
	lg := ledgerText(t, root)
	if strings.Count(lg, "motor autónomo autorizado") != 2 || !strings.Contains(lg, "apr:"+id) || !strings.Contains(lg, "stop:evaluate") {
		t.Errorf("usos de la autorización:\n%s", lg)
	}
	// La autorización se agotó, pero evaluar no corre nada: no la pide.
	r = run(t, root, "", "ws", "continue", "W-0008")
	must(t, r, 0, "evaluar")
	if !strings.Contains(r.stdout, "✓ A3 aceptado") || !strings.Contains(r.stdout, "El plan está completo") {
		t.Errorf("fin:\n%s\n%s", r.stdout, r.stderr)
	}
	// Con el proyecto en supervised, el mismo plan corre como supervised.
	cfgPath := filepath.Join(root, "coyote", "project.yaml")
	cfg := strings.Replace(readFile(t, cfgPath), "autonomy: autonomous", "autonomy: supervised", 1)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	r = run(t, root, "", "ws", "check", "W-0008")
	if !strings.Contains(r.stdout, "el proyecto permite hasta supervised: el plan corre como supervised") {
		t.Errorf("techo del proyecto:\n%s", r.stdout)
	}
}

// TestWsLosTextosDelPlanNoForjanLineas: quinta revisión de v1.0 (N1). Un valor
// de plan.yaml con un salto de línea (un "\n" en un texto entre comillas) no
// dibuja líneas que coyote no escribió: ni la cabecera de ws check y ws
// status, ni la línea "Sigue:", ni la tabla, ni lo que dicen ws run y ws
// continue. Por cada campo del plan y de sus pasos, cada salida se lee línea
// por línea.
func TestWsLosTextosDelPlanNoForjanLineas(t *testing.T) {
	// Un valor entre comillas con un salto de línea, una línea de mentira y una
	// tabulación que abriría una celda de la tabla.
	const forjado = `"v\nFORJADO: la dibuja el plan, no coyote\n\tcelda"`
	defaults := map[string]string{
		"id": "W-0004", "title": "pagos", "owner": "ana", "risk": "R1", "reasoning": "x", "authorized_by": "ana", "gate": "G5",
		"s1": "S1", "does": "hacer", "task": "hacer", "agent": "coyote-dev", "input": "docs/a.md", "output": "salida", "section": "Propuesta",
		"srisk": "R1", "scope": "pagos", "sgate": "human",
	}
	for field := range defaults {
		t.Run(field, func(t *testing.T) {
			base, root := wsProject(t, "supervised")
			fakeClaudeWS(t, base)
			must(t, run(t, root, "", "install", "--ide", "claude-code"), 0, "install")
			write(t, root, "docs/a.md", "uno\n")
			vals := map[string]string{}
			for k, v := range defaults {
				vals[k] = v
			}
			vals[field] = forjado
			plan := `id: ` + vals["id"] + `
title: ` + vals["title"] + `
owner: ` + vals["owner"] + `
autonomy: supervised
risk: ` + vals["risk"] + `
reasoning: ` + vals["reasoning"] + `
authorized_by: ` + vals["authorized_by"] + `
gate: ` + vals["gate"] + `
budget_usd: 2
steps:
  - { id: ` + vals["s1"] + `, does: ` + vals["does"] + `, task: ` + vals["task"] + `, agent: ` + vals["agent"] + `, input: [` + vals["input"] + `], output: [` + vals["output"] + `], sections: [` + vals["section"] + `], risk: ` + vals["srisk"] + `, scope: ` + vals["scope"] + `, max_usd: 0.3, gate: ` + vals["sgate"] + ` }
  - { id: S2, does: ` + vals["does"] + `, agent: persona }
`
			writePlan(t, root, "W-0004-forjado", plan)
			for _, args := range [][]string{
				{"ws", "check", "W-0004"}, {"ws", "status", "W-0004"}, {"ws", "status"}, {"ws", "run", "W-0004", "--dry-run"},
				{"ws", "run", "W-0004"}, {"ws", "status", "W-0004"}, {"ws", "continue", "W-0004"}, {"ws", "status", "W-0004"},
				{"ws", "continue", "W-0004"}, {"ws", "status", "W-0004"}, {"ws", "status"},
			} {
				r := run(t, root, "", args...)
				if args[len(args)-1] == "--dry-run" && (field == "task" || field == "output") {
					// La tarea y la salida esperada son el texto del prompt: varias
					// líneas son lo que el plan pide, y el prompt sale tal cual.
					continue
				}
				for _, out := range []string{r.stdout, r.stderr} {
					for _, line := range strings.Split(out, "\n") {
						if l := strings.TrimSpace(line); strings.HasPrefix(l, "FORJADO") || strings.HasPrefix(l, "celda") {
							t.Errorf("%s con %s forjado dibuja una línea propia:\n%s", strings.Join(args, " "), field, out)
							return
						}
					}
					// Una tabulación en una tabla abre otra celda: "celda" saldría
					// separada del resto por espacios en vez de escrita como \t.
					if (args[1] == "check" || args[1] == "status") && strings.Contains(out, " celda") {
						t.Errorf("%s con %s forjado abre una celda de la tabla:\n%s", strings.Join(args, " "), field, out)
						return
					}
				}
			}
		})
	}
}

// TestWsElIDEsElDeLaCarpeta: quinta revisión de v1.0 (N1). El workstream es el
// de su carpeta: un id distinto dentro de plan.yaml es un error del plan, que
// ws check dice, y no cambia a qué workstream se atribuyen las corridas ni qué
// comando sugiere la salida.
func TestWsElIDEsElDeLaCarpeta(t *testing.T) {
	base, root := wsProject(t, "supervised")
	fakeClaudeWS(t, base)
	must(t, run(t, root, "", "install", "--ide", "claude-code"), 0, "install")
	plan := func(id string) string {
		return "id: " + id + "\ntitle: pagos\nautonomy: supervised\nrisk: R1\nbudget_usd: 2\nsteps:\n  - { id: S1, does: hacer, agent: coyote-dev, output: salida, max_usd: 0.3 }\n"
	}
	p := writePlan(t, root, "W-0004-pagos", plan("W-0004"))
	must(t, run(t, root, "", "ws", "run", "W-0004"), 0, "S1 corre")
	// El id de adentro cambia: las corridas siguen siendo de W-0004.
	if err := os.WriteFile(p, []byte(plan("W-0099")), 0o644); err != nil {
		t.Fatal(err)
	}
	r := run(t, root, "", "ws", "check", "W-0004")
	must(t, r, 1, "check")
	if !strings.Contains(r.stdout, "no coincide") {
		t.Errorf("ws check dice que el id no coincide con la carpeta:\n%s", r.stdout)
	}
	for _, args := range [][]string{{"ws", "status", "W-0004"}, {"ws", "status"}} {
		r = run(t, root, "", args...)
		must(t, r, 0, strings.Join(args, " "))
		if !strings.Contains(r.stdout, "W-0004") || strings.Contains(r.stdout, "W-0099") || !strings.Contains(r.stdout, "$0.04") {
			t.Errorf("%s atribuye lo de la carpeta W-0004 y no usa el id de adentro:\n%s", strings.Join(args, " "), r.stdout)
		}
	}
}

// TestRunYWebUsanElIDDeLaCarpeta: sexta revisión de v1.0 (N9). El workstream es
// el de su carpeta para todo: una corrida suelta con --ws, close y la web no
// cambian de workstream porque plan.yaml diga otro id (un error típico al
// copiar una carpeta): el tope y el cierre siguen siendo los de W-0004.
func TestRunYWebUsanElIDDeLaCarpeta(t *testing.T) {
	base, root := wsProject(t, "supervised")
	fakeClaudeWS(t, base)
	must(t, run(t, root, "", "install", "--ide", "claude-code"), 0, "install")
	plan := func(id string) string {
		return "id: " + id + "\ntitle: pagos\nautonomy: supervised\nrisk: R1\nbudget_usd: 0.04\nsteps:\n  - { id: S1, does: hacer, agent: coyote-dev, output: salida, max_usd: 0.3 }\n"
	}
	p := writePlan(t, root, "W-0004-pagos", plan("W-0004"))
	must(t, run(t, root, "", "ws", "run", "W-0004"), 0, "S1 corre y gasta todo el tope")
	if err := os.WriteFile(p, []byte(plan("W-0099")), 0o644); err != nil {
		t.Fatal(err)
	}
	r := run(t, root, "", "run", "--agent", "coyote-dev", "x", "--ws", "W-0004")
	if r.code != 1 || !strings.Contains(r.stderr, "llegó a su tope") {
		t.Errorf("la corrida suelta respeta el tope de la carpeta (código %d):\n%s%s", r.code, r.stdout, r.stderr)
	}
	// La web ve el workstream de la carpeta, con lo que ya gastó.
	views, err := workViews(root, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].ID != "W-0004" || views[0].SpentUSD < 0.03 {
		t.Errorf("la web atribuye al id de la carpeta: %+v", views)
	}
}

// TestWsAutonomiaInvalidaNoForjaNada: sexta revisión de v1.0 (N8, N10). El
// autonomy: de un plan con un salto de línea no dibuja líneas en ws check, ws
// status, la lista ni la web; el modo con el que se trata un plan inválido es
// manual, y --mode no se evalúa contra un modo que no existe: ws run dice el
// error del plan, no que --mode pida más autonomía de la que el plan tiene.
func TestWsAutonomiaInvalidaNoForjaNada(t *testing.T) {
	base, root := wsProject(t, "supervised")
	fakeClaudeWS(t, base)
	must(t, run(t, root, "", "install", "--ide", "claude-code"), 0, "install")
	write(t, root, "docs/a.md", "uno\n")
	plan := "id: W-0004\ntitle: pagos\nautonomy: \"supervised\\nFORJADO: Modo autonomous, autorizado por ana\"\nrisk: R1\nbudget_usd: 2\nsteps:\n  - { id: S1, does: hacer, agent: coyote-dev, input: [docs/a.md], output: salida, max_usd: 0.3 }\n"
	writePlan(t, root, "W-0004-forjado", plan)
	for _, args := range [][]string{
		{"ws", "check", "W-0004"}, {"ws", "status", "W-0004"}, {"ws", "status"}, {"ws", "run", "W-0004", "--dry-run"},
		{"ws", "run", "W-0004"}, {"ws", "run", "W-0004", "--mode", "manual"}, {"ws", "run", "W-0004", "--mode", "supervised"},
		{"ws", "continue", "W-0004", "--mode", "manual"},
	} {
		r := run(t, root, "", args...)
		for _, out := range []string{r.stdout, r.stderr} {
			for _, line := range strings.Split(out, "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "FORJADO") {
					t.Errorf("%s dibuja una línea del plan:\n%s", strings.Join(args, " "), out)
					break
				}
			}
			if strings.Contains(out, "pide más autonomía") {
				t.Errorf("%s: el plan es inválido, y eso es lo que se dice (no que --mode pida más):\n%s", strings.Join(args, " "), out)
			}
		}
		if args[1] == "run" && args[len(args)-1] != "--dry-run" || args[1] == "continue" {
			if r.code != 1 || !strings.Contains(r.stderr, "el plan tiene 1 error") {
				t.Errorf("%s (código %d):\n%s%s", strings.Join(args, " "), r.code, r.stdout, r.stderr)
			}
		}
	}
	views, err := workViews(root, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].Mode != "manual" {
		t.Errorf("la web muestra el modo con el que corre, no el texto del plan: %+v", views)
	}
}

// TestWebMuestraElModoConElQueCorre: sexta revisión de v1.0 (N13). La web
// muestra el modo efectivo del plan, con el techo del proyecto, igual que ws
// status.
func TestWebMuestraElModoConElQueCorre(t *testing.T) {
	_, root := wsProject(t, "supervised")
	writePlan(t, root, "W-0004-pagos", "id: W-0004\ntitle: pagos\nautonomy: autonomous\nrisk: R1\nbudget_usd: 2\nsteps:\n  - { id: S1, does: hacer, agent: coyote-dev, output: salida, max_usd: 0.3 }\n")
	views, err := workViews(root, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].Mode != "supervised" {
		t.Errorf("el plan pide autonomous y el proyecto permite supervised: %+v", views)
	}
	st := run(t, root, "", "ws", "status", "W-0004")
	if !strings.Contains(st.stdout, "supervised") {
		t.Errorf("ws status dice el mismo modo:\n%s", st.stdout)
	}
}

// sinBandera devuelve las veces que out sugiere cmd sin lo que debe seguirle
// (el --mode con el que se está corriendo): cada una es una orden que, copiada
// tal cual, corre con otro modo del que la persona eligió.
func sinBandera(out, cmd, flag string) []string {
	var bad []string
	for i := 0; ; {
		j := strings.Index(out[i:], cmd)
		if j < 0 {
			return bad
		}
		i += j + len(cmd)
		if !strings.HasPrefix(out[i:], flag) {
			end := i + 40
			if end > len(out) {
				end = len(out)
			}
			bad = append(bad, out[i-len(cmd):end])
		}
	}
}

// TestWsLasPistasLlevanElModoConElQueSeCorre: sexta revisión de v1.0 (N13). Un
// plan autonomous cuyo PRD lo escribe el primer paso, sin gate: human, no corre
// en autonomous (R20), pero sí con --mode supervised. Las órdenes que coyote
// sugiere llevan ese --mode (copiarlas sin él las rechazaría el plan, o correría
// con más autonomía de la que la persona eligió), y el plan no "corrió en
// autónomo" porque su modo declarado lo sea: lo que corrió fue supervised.
func TestWsLasPistasLlevanElModoConElQueSeCorre(t *testing.T) {
	base, root := wsProject(t, "autonomous")
	fake, say := fakeClaudeWS(t, base)
	must(t, run(t, root, "", "install", "--ide", "claude-code"), 0, "install")
	writePlan(t, root, "W-0004-producto", `id: W-0004
title: producto
autonomy: autonomous
risk: R2
prd: step:P1
budget_usd: 2
steps:
  - { id: P1, does: escribir el PRD, agent: coyote-pm, output: PRD, max_usd: 0.3 }
  - { id: D1, does: implementar, agent: coyote-dev, input: [step:P1], output: parche, max_usd: 0.3 }
`)
	flag := " --mode supervised"
	r := run(t, root, "", "ws", "check", "W-0004")
	must(t, r, 1, "check")
	if !strings.Contains(r.stdout, "en autonomous el PRD pasa por una persona") {
		t.Fatalf("el plan autonomous sin gate: human en el PRD es un error:\n%s", r.stdout)
	}
	// Antes de correr: status no sugiere una orden que el plan rechaza.
	r = run(t, root, "", "ws", "status", "W-0004")
	must(t, r, 0, "status antes de correr")
	if bad := sinBandera(r.stdout, "coyote ws run W-0004", flag); len(bad) > 0 || !strings.Contains(r.stdout, "coyote ws run W-0004"+flag) {
		t.Errorf("status sugiere correr con el modo con el que el plan no tiene errores (%q):\n%s", bad, r.stdout)
	}
	if !strings.Contains(r.stdout, "El plan tiene 1 error: coyote ws check W-0004. Con --mode supervised no los tiene.") {
		t.Errorf("status dice con qué modo no tiene errores:\n%s", r.stdout)
	}
	r = run(t, root, "", "ws", "status", "W-0004", "--json")
	must(t, r, 0, "status --json antes de correr")
	if !strings.Contains(r.stdout, `"mode": "supervised"`) || !strings.Contains(r.stdout, `"run": true`) {
		t.Errorf("--json dice con qué modo se calcula lo que sigue:\n%s", r.stdout)
	}
	must(t, run(t, root, "", "ws", "run", "W-0004"), 1, "autonomous no corre")
	if calls(t, fake) != 0 {
		t.Fatal("el plan con errores no corre")
	}

	r = run(t, root, "", "ws", "run", "W-0004", "--mode", "supervised")
	must(t, r, 0, "run supervised")
	if !strings.Contains(r.stdout, "P1 espera tu revisión") {
		t.Fatalf("parada:\n%s", r.stdout)
	}
	for _, cmd := range []string{"coyote ws continue W-0004", "coyote ws run W-0004"} {
		if bad := sinBandera(r.stdout, cmd, flag); len(bad) > 0 {
			t.Errorf("la parada sugiere una orden sin el modo con el que se corre: %q\n%s", bad, r.stdout)
		}
	}
	// El estado no dice que corrió en autónomo: todo corrió en supervised.
	r = run(t, root, "", "ws", "status", "W-0004")
	must(t, r, 0, "status")
	if strings.Contains(r.stdout, "corrió en autónomo") || !strings.Contains(r.stdout, "P1 espera tu revisión") {
		t.Errorf("P1 corrió en supervised y espera la revisión:\n%s", r.stdout)
	}
	if bad := sinBandera(r.stdout, "coyote ws continue W-0004", flag); len(bad) > 0 {
		t.Errorf("status sugiere continuar sin el modo: %q\n%s", bad, r.stdout)
	}
	r = run(t, root, "", "ws", "status")
	must(t, r, 0, "lista")
	if strings.Contains(r.stdout, "evaluar lo que corrió en autónomo") || !strings.Contains(r.stdout, "P1 · por revisar") {
		t.Errorf("la lista no evalúa lo que no corrió en autónomo:\n%s", r.stdout)
	}
	r = run(t, root, "", "ws", "status", "W-0004", "--json")
	must(t, r, 0, "status --json")
	if !strings.Contains(r.stdout, `"reason": "review"`) || !strings.Contains(r.stdout, "coyote ws continue W-0004"+flag) {
		t.Errorf("--json:\n%s", r.stdout)
	}

	say("## Propuesta\n\nparche")
	r = run(t, root, "", "ws", "continue", "W-0004", "--mode", "supervised")
	must(t, r, 0, "continue supervised")
	if !strings.Contains(r.stdout, "✓ P1 aceptado") || !strings.Contains(r.stdout, "D1 espera tu revisión") || calls(t, fake) != 2 {
		t.Fatalf("continue:\n%s", r.stdout)
	}
	if bad := sinBandera(r.stdout, "coyote ws continue W-0004", flag); len(bad) > 0 {
		t.Errorf("la parada de continue sugiere una orden sin el modo: %q\n%s", bad, r.stdout)
	}
	r = run(t, root, "", "ws", "status", "W-0004")
	must(t, r, 0, "status tras continue")
	if strings.Contains(r.stdout, "corrió en autónomo") || !strings.Contains(r.stdout, "D1 espera tu revisión") {
		t.Errorf("D1 corrió en supervised:\n%s", r.stdout)
	}
	r = run(t, root, "", "ws", "status")
	if strings.Contains(r.stdout, "evaluar lo que corrió en autónomo") || !strings.Contains(r.stdout, "D1 · por revisar") {
		t.Errorf("la lista tras continue:\n%s", r.stdout)
	}
}

// TestWsLaPistaConservaElModoManual: bajar un plan válido a manual y copiar la
// orden que sugiere coyote no lo sube de nuevo a autonomous; y si el modo con
// el que se corre es el del plan, las pistas no llevan --mode.
func TestWsLaPistaConservaElModoManual(t *testing.T) {
	base, root := wsProject(t, "autonomous")
	fakeClaudeWS(t, base)
	must(t, run(t, root, "", "install", "--ide", "claude-code"), 0, "install")
	plan := func(id, mode string) string {
		return "id: " + id + "\ntitle: pagos\nautonomy: " + mode + "\nrisk: R1\nbudget_usd: 2\nsteps:\n" +
			"  - { id: A1, does: analizar, agent: coyote-analyst, output: análisis, max_usd: 0.3 }\n" +
			"  - { id: A2, does: resumir, agent: coyote-scribe, input: [step:A1], output: resumen, max_usd: 0.3 }\n"
	}
	writePlan(t, root, "W-0005-pagos", plan("W-0005", "autonomous"))
	r := run(t, root, "", "ws", "run", "W-0005", "--mode", "manual")
	must(t, r, 0, "run manual")
	if bad := sinBandera(r.stdout, "coyote ws continue W-0005", " --mode manual"); len(bad) > 0 || !strings.Contains(r.stdout, "A1 espera tu revisión") {
		t.Errorf("la parada de manual conserva el modo (%q):\n%s", bad, r.stdout)
	}
	r = run(t, root, "", "ws", "continue", "W-0005", "--mode", "manual")
	must(t, r, 0, "continue manual")
	if bad := sinBandera(r.stdout, "coyote ws run W-0005", " --mode manual"); len(bad) > 0 || !strings.Contains(r.stdout, "coyote ws run W-0005 --mode manual") {
		t.Errorf("el paso siguiente se corre con el mismo modo (%q):\n%s", bad, r.stdout)
	}
	// Con el modo del plan no hace falta la bandera, aunque se pase igual.
	writePlan(t, root, "W-0006-otro", plan("W-0006", "supervised"))
	r = run(t, root, "", "ws", "run", "W-0006", "--mode", "supervised")
	must(t, r, 0, "run supervised")
	if strings.Contains(r.stdout, "--mode") || !strings.Contains(r.stdout, "coyote ws continue W-0006") {
		t.Errorf("sin bajar el modo, las pistas no llevan --mode:\n%s", r.stdout)
	}
}

// TestWsStatusConErroresNoSugiereLoQueSeRechaza: con errores en todos los
// modos, ws status no dice "corre con coyote ws run" (se negaría): dice que se
// corrige el plan.
func TestWsStatusConErroresNoSugiereLoQueSeRechaza(t *testing.T) {
	base, root := wsProject(t, "supervised")
	fakeClaudeWS(t, base)
	must(t, run(t, root, "", "install", "--ide", "claude-code"), 0, "install")
	writePlan(t, root, "W-0005-pagos", "id: W-0005\ntitle: pagos\nautonomy: supervised\nrisk: R1\nbudget_usd: 2\nsteps:\n  - { id: A1, does: analizar, agent: coyote-noexiste, output: análisis, max_usd: 0.3 }\n")
	r := run(t, root, "", "ws", "status", "W-0005")
	must(t, r, 0, "status")
	if strings.Contains(r.stdout, "corre con coyote ws run") || !strings.Contains(r.stdout, "Sigue: corrige el plan") {
		t.Errorf("un plan con errores no se sugiere correr:\n%s", r.stdout)
	}
	r = run(t, root, "", "ws", "status", "W-0005", "--json")
	if strings.Contains(r.stdout, "corre con coyote ws run") {
		t.Errorf("--json:\n%s", r.stdout)
	}
}

// TestWsLosMensajesDeContinueLlevanElModo: lo que ws continue dice cuando se
// niega a algo también conserva el modo con el que se está corriendo.
func TestWsLosMensajesDeContinueLlevanElModo(t *testing.T) {
	base, root := wsProject(t, "autonomous")
	fakeClaudeWS(t, base)
	must(t, run(t, root, "", "install", "--ide", "claude-code"), 0, "install")
	writePlan(t, root, "W-0005-pagos", `id: W-0005
title: pagos
autonomy: autonomous
risk: R1
budget_usd: 2
steps:
  - { id: A1, does: resumir, agent: coyote-scribe, output: resumen, sections: [Resumen], max_usd: 0.3 }
  - { id: A2, does: probar en staging, agent: persona }
`)
	flag := " --mode manual"
	r := run(t, root, "", "ws", "continue", "W-0005", "--mode", "manual")
	must(t, r, 1, "nada que revisar")
	if !strings.Contains(r.stderr, "corre el paso siguiente con coyote ws run W-0005"+flag) {
		t.Errorf("nada que revisar:\n%s", r.stderr)
	}
	// A1 no trae su sección: falla.
	r = run(t, root, "", "ws", "run", "W-0005", "--mode", "manual")
	if !strings.Contains(r.stdout, "A1 falló") || len(sinBandera(r.stdout, "coyote ws continue W-0005", flag)) > 0 {
		t.Errorf("A1 falla:\n%s%s", r.stdout, r.stderr)
	}
	r = run(t, root, "", "ws", "continue", "W-0005", "--mode", "manual")
	must(t, r, 1, "continue de un paso que falló")
	if !strings.Contains(r.stderr, "repítelo con coyote ws continue W-0005"+flag+" --retry") {
		t.Errorf("paso que falló:\n%s", r.stderr)
	}
	r = run(t, root, "", "ws", "continue", "W-0005", "--retry", "--step", "A2", "--mode", "manual")
	must(t, r, 1, "paso de la persona")
	if !strings.Contains(r.stderr, "A2 lo haces tú: cuando termines, coyote ws continue W-0005"+flag) {
		t.Errorf("paso de la persona:\n%s", r.stderr)
	}
}

// ledgerEvents devuelve las líneas del ledger de un tipo (el campo 5 de CCF).
func ledgerEvents(t *testing.T, root, typ string) []string {
	t.Helper()
	var out []string
	for _, l := range strings.Split(ledgerText(t, root), "\n") {
		if f := strings.Split(l, "|"); len(f) > 4 && f[4] == typ {
			out = append(out, l)
		}
	}
	return out
}

// TestWsLaDetencionA4QuedaEnElLedger: sexta revisión de v1.0 (N15). Cuando el
// loop autónomo se detiene por A4 (el plan o la rama cambiaron, una entrada
// cambió desde que la persona la autorizó), queda un evento en el ledger con
// dónde y por qué: quien audita ve una detención, no un hueco entre dos
// corridas (A3).
func TestWsLaDetencionA4QuedaEnElLedger(t *testing.T) {
	plan := func(id string) string {
		return "id: " + id + "\ntitle: loop\nautonomy: autonomous\nrisk: R2\nbudget_usd: 2\nsteps:\n" +
			"  - { id: D1, does: analizar, agent: coyote-analyst, input: [docs/a.md], output: análisis, max_usd: 0.3 }\n" +
			"  - { id: D2, does: resumir, agent: coyote-scribe, input: [step:D1], output: resumen, max_usd: 0.3 }\n" +
			"  - { id: D3, does: revisar, agent: coyote-reviewer, input: [docs/a.md], output: informe, max_usd: 0.3 }\n"
	}
	for _, c := range []struct {
		name, id, mutate, stderr, stop, at string
	}{
		{"el plan cambia", "W-0014", `printf '# editado\n' >> "$PLAN"`, "el plan cambió desde que lo autorizaste", "authorization", "D2"},
		{"la rama cambia", "W-0015", `git -C "$ROOT" symbolic-ref HEAD refs/heads/otra`, "la rama cambió a otra", "authorization", "D2"},
		{"una entrada cambia", "W-0016", `printf 'otro contenido\n' > "$ROOT/docs/a.md"`, "docs/a.md cambió desde que lo autorizaste", "input", "D3"},
	} {
		t.Run(c.name, func(t *testing.T) {
			base, root := wsProject(t, "autonomous")
			fake, _ := fakeClaudeWS(t, base)
			must(t, run(t, root, "", "install", "--ide", "claude-code"), 0, "install")
			write(t, root, "docs/a.md", "uno\n")
			planPath := writePlan(t, root, c.id+"-loop", plan(c.id))
			git(t, root, "symbolic-ref", "HEAD", "refs/heads/ws/"+c.id)
			r := run(t, root, "", "ws", "run", c.id)
			must(t, r, 1, "sin autorización")
			must(t, run(t, root, "", "approve", propRe.FindString(r.stderr), "--uses", "5"), 0, "approve")
			// El paso 1 corre; mientras tanto algo cambia lo que la persona autorizó. La
			// entrada que cambia la lee D3, no D1 ni D2: el motor llega hasta D2.
			script := "PLAN='" + planPath + "'; ROOT='" + root + "'\nif [ \"$1\" = 1 ] || { [ \"$1\" = 2 ] && [ '" + c.stop + "' = input ]; }; then " + c.mutate + "; fi\nexit 0\n"
			if err := os.WriteFile(filepath.Join(fake, "mutate"), []byte(script), 0o644); err != nil {
				t.Fatal(err)
			}
			r = run(t, root, "", "ws", "run", c.id)
			must(t, r, 1, "A4")
			if !strings.Contains(r.stderr, c.stderr) {
				t.Fatalf("la detención dice por qué (%q):\n%s", c.stderr, r.stderr)
			}
			events := ledgerEvents(t, root, "plan")
			var halt string
			for _, l := range events {
				if strings.Contains(l, "stop:"+c.stop) {
					halt = l
				}
			}
			if want := "A4 detiene antes de " + c.at + ": " + c.stderr; !strings.Contains(halt, want) {
				t.Errorf("el evento dice el paso y el motivo (%q):\n%s", want, halt)
			}
			if halt == "" || !strings.Contains(halt, "at:"+c.at) || !strings.Contains(halt, "mode:autonomous") || !strings.HasSuffix(halt, "|skip") {
				t.Errorf("el ledger registra la detención A4 (stop:%s, at:%s):\n%s", c.stop, c.at, ledgerText(t, root))
			}
			if strings.Contains(ledgerText(t, root), "sin entrada") {
				t.Error("detenerse por A4 no es un paso sin entrada")
			}
		})
	}
}
