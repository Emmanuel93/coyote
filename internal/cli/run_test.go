package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fakeResult = `{"type":"result","subtype":"success","is_error":false,"result":"## Propuesta\n\nUsar un módulo de pagos aparte (src/pagos.go#L1).","session_id":"sesion-123456789","num_turns":5,"total_cost_usd":0.04,"usage":{"input_tokens":1000,"cache_creation_input_tokens":200,"cache_read_input_tokens":3000,"output_tokens":800},"modelUsage":{"claude-opus-x":{"inputTokens":1000,"outputTokens":800,"cacheReadInputTokens":3000,"cacheCreationInputTokens":200,"costUSD":0.04}}}`

// fakeClaude instala un claude simulado que guarda argumentos y entrada.
func fakeClaude(t *testing.T, base, out string) string {
	t.Helper()
	dir := filepath.Join(base, "claude-fake")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "claude")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"" + dir + "/args\"\ncat > \"" + dir + "/stdin\"\nprintf '%s' '" + out + "'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COYOTE_CLAUDE", bin)
	return dir
}

func TestRunRouterClose(t *testing.T) {
	base, root := gateProject(t)
	bin := filepath.Join(base, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "coyote"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	fake := fakeClaude(t, base, fakeResult)
	ws := filepath.Join(root, "coyote", "workstreams", "W-0001-pagos")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}

	// Sin el gate instalado no corre: un agente podría correr comandos sin aprobación.
	must(t, run(t, root, "", "run", "--agent", "coyote-architect", "--ws", "W-0001", "diseña pagos"), 1, "sin gate")
	must(t, run(t, root, "", "install", "--ide", "claude-code"), 0, "install")

	// --dry-run muestra la decisión y no corre.
	r := run(t, root, "", "run", "--agent", "coyote-scribe", "--risk", "R3", "--dry-run", "resume el diseño")
	must(t, r, 0, "dry-run")
	if !strings.Contains(r.stdout, "Router: opus") || !strings.Contains(r.stdout, "el riesgo R3 pide al menos opus") || !strings.Contains(r.stdout, "# Tarea") {
		t.Errorf("dry-run:\n%s", r.stdout)
	}
	if _, err := os.Stat(filepath.Join(fake, "args")); err == nil {
		t.Fatal("--dry-run no corre Claude Code")
	}

	// Una corrida real con el claude simulado.
	r = run(t, root, "", "run", "--agent", "coyote-architect", "--ws", "W-0001", "--risk", "R2", "diseña el módulo de pagos")
	must(t, r, 0, "run")
	if !strings.Contains(r.stdout, "coyote-architect terminó con claude-opus-x en 5 turnos") || !strings.Contains(r.stdout, "artefacto: coyote/workstreams/W-0001-pagos/runs/") {
		t.Errorf("salida de run:\n%s", r.stdout)
	}
	args, _ := os.ReadFile(filepath.Join(fake, "args"))
	for _, want := range []string{"--agent\ncoyote-architect", "--model\nopus", "--permission-mode\ndontAsk", "--max-budget-usd\n2.00", "--output-format\njson"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("falta %q en los argumentos:\n%s", want, args)
		}
	}
	stdin, _ := os.ReadFile(filepath.Join(fake, "stdin"))
	if !strings.Contains(string(stdin), "diseña el módulo de pagos") || !strings.Contains(string(stdin), "# Contexto: tienda") || !strings.Contains(string(stdin), "# Cómo entregar") {
		t.Errorf("entrada del agente:\n%s", stdin)
	}
	runs, _ := filepath.Glob(filepath.Join(ws, "runs", "*-coyote-architect.md"))
	if len(runs) != 1 || !strings.Contains(readFile(t, runs[0]), "Usar un módulo de pagos aparte") {
		t.Fatalf("artefacto de la corrida: %v", runs)
	}
	lg := ledgerText(t, root)
	if !strings.Contains(lg, "@ana/coyote-architect|W-0001|tienda|run|") || !strings.Contains(lg, "model:claude-opus-x") ||
		!strings.Contains(lg, "4.2k/3k/800|$0.04+$0|ok") {
		t.Errorf("evento run:\n%s", lg)
	}

	// Presupuesto: al 80 % baja un nivel; al 100 % no corre.
	cfgPath := filepath.Join(root, "coyote", "project.yaml")
	cfg := strings.Replace(readFile(t, cfgPath), "monthly_usd: 0", "monthly_usd: 0.05", 1)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	r = run(t, root, "", "run", "--agent", "coyote-architect", "--ws", "W-0001", "revisa el diseño")
	must(t, r, 0, "run al 80 %")
	args, _ = os.ReadFile(filepath.Join(fake, "args"))
	if !strings.Contains(string(args), "--model\nsonnet") || !strings.Contains(string(args), "--max-budget-usd\n0.01") {
		t.Errorf("al 80 %% baja a sonnet con el tope de lo que queda:\n%s", args)
	}
	r = run(t, root, "", "run", "--agent", "coyote-architect", "--ws", "W-0001", "otra vuelta")
	must(t, r, 1, "run al 100 %")
	if !strings.Contains(r.stderr, "no se corren agentes") || !strings.Contains(ledgerText(t, root), "presupuesto: otra vuelta|-|-|-|skip") {
		t.Errorf("al 100 %% no corre y queda en el ledger:\n%s\n%s", r.stderr, ledgerText(t, root))
	}
	r = run(t, root, "", "router")
	must(t, r, 0, "router")
	if !strings.Contains(r.stdout, "coyote-scribe") || !strings.Contains(r.stdout, "no corre:") {
		t.Errorf("router:\n%s", r.stdout)
	}

	// Dentro de una sesión de agente no corre.
	t.Setenv("CLAUDECODE", "1")
	must(t, run(t, root, "", "run", "--agent", "coyote-dev", "algo"), 1, "run desde un agente")
	t.Setenv("CLAUDECODE", "")

	// El cierre suma el consumo del workstream y conserva lo que escribió la persona.
	closePath := filepath.Join(ws, "close.md")
	if err := os.WriteFile(closePath, []byte("# Cierre de W-0001\n\nLo que aprendimos: pagos va aparte.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r = run(t, root, "", "close", "W-0001")
	must(t, r, 0, "close")
	cl := readFile(t, closePath)
	for _, want := range []string{"Lo que aprendimos: pagos va aparte.", "## Consumo", "| claude-opus-x |", "| coyote-architect |", "### Corridas"} {
		if !strings.Contains(cl, want) {
			t.Errorf("close.md sin %q:\n%s", want, cl)
		}
	}
	must(t, run(t, root, "", "close", "W-0001"), 0, "close de nuevo")
	if n := strings.Count(readFile(t, closePath), "## Consumo"); n != 1 {
		t.Errorf("el bloque de consumo se reemplaza, no se repite: %d", n)
	}
	if !strings.Contains(ledgerText(t, root), "|close|-|cierre de W-0001: 2 corridas") {
		t.Errorf("evento close:\n%s", ledgerText(t, root))
	}
	must(t, run(t, root, "", "close", "W-9999"), 1, "workstream inexistente")
}
