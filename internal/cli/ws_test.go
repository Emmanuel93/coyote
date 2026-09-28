package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// fakeClaudeWS instala un claude simulado para el motor: cada corrida deja
// sus argumentos y su entrada numerados, responde el texto de out y, si
// existe el archivo propose, deja esa acción en la cola del gate como lo
// haría el hook.
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
