package standards

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDefault(t *testing.T) {
	st, err := Load(t.TempDir(), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Rules) < 20 || st.Language.Docs != "es" || len(st.Layers) != 1 {
		t.Fatalf("default incompleto: %d reglas, idioma %q, capas %v", len(st.Rules), st.Language.Docs, st.Layers)
	}
	for _, id := range []string{"R2", "R14", "R15", "A1"} {
		if r := st.Find(id); r == nil || r.Level != "MUST" {
			t.Errorf("%s debe existir y ser MUST", id)
		}
	}
}

func TestLayers(t *testing.T) {
	tmp := t.TempDir()
	write(t, tmp, "org/rules.yaml", `version: 1
extends: coyote:default
rules:
  - id: K1
    title: Toda llamada de pago usa la pasarela interna
    level: MUST
    check: { type: regex_absent, paths: ["src/**"], pattern: 'http\.Post\(' }
  - id: R10
    override: { level: MAY, reason: "README heredado", adr: ADR-0003 }
  - id: R9
    override: { level: MAY }
  - id: R3.ci
    override: { level: MAY, until: 2026-01-31, reason: "sin CI todavía" }
`)
	write(t, tmp, "proj/coyote/standards/rules.yaml", `version: 1
extends: ../../../org/rules.yaml
rules:
  - id: P1
    title: Regla del proyecto
    level: SHOULD
`)
	st, err := Load(filepath.Join(tmp, "proj"), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Layers) != 3 || st.Layers[0] != DefaultRef || st.Layers[2] != "proyecto" {
		t.Fatalf("capas: %v", st.Layers)
	}
	if st.Find("K1") == nil || st.Find("P1") == nil {
		t.Fatal("faltan reglas agregadas por las capas")
	}
	if r := st.Find("R10"); r.Level != "MAY" || !strings.Contains(r.Note, "README heredado") {
		t.Fatalf("R10 no se relajó con motivo: %+v", r)
	}
	if r := st.Find("R9"); r.Level != "SHOULD" {
		t.Fatalf("R9 se relajó sin motivo: %s", r.Level)
	}
	if r := st.Find("R3.ci"); r.Level != "SHOULD" {
		t.Fatalf("un ajuste vencido no debe aplicarse: %s", r.Level)
	}
	joined := strings.Join(st.Warnings, "\n")
	for _, want := range []string{"R9 se relaja sin un reason real", "venció"} {
		if !strings.Contains(joined, want) {
			t.Errorf("falta advertencia %q en:\n%s", want, joined)
		}
	}
}

func TestLint(t *testing.T) {
	Register("agents_md_current", func(*Context, Check) []Finding { return nil })
	root := t.TempDir()
	write(t, root, "README.md", "# demo\n")
	write(t, root, ".gitignore", ".coyote/\n")
	write(t, root, ".coyoteignore", ".env\n")
	write(t, root, "AGENTS.md", "x\n")
	write(t, root, "README.coyote.md", "---\ncoyote: 1\nrepo: demo\ntype: backend\nowners: [\"@ana\"]\nstandards: { waive: [{id: R10, reason: heredado}] }\n---\npurpose|demo de pruebas\nrun|make run\ntest|make test\n")
	write(t, root, "CONTEXT.coyote.md", "---\ncoyote: 1\nrepo: demo\n---\ndec|x|una decisión|-\n")
	st, err := Load(root, now)
	if err != nil {
		t.Fatal(err)
	}
	lint := func() *Result {
		ctx, err := NewContext(root, st, "backend", map[string]string{"R10": "heredado"}, "manual", now)
		if err != nil {
			t.Fatal(err)
		}
		return Lint(ctx, st)
	}
	if res := lint(); res.Failing(false) != 0 {
		t.Fatalf("un proyecto válido no debe fallar: %+v", res.Findings)
	}
	write(t, root, "respaldos/prod.sql", "select 1;\n")
	write(t, root, "docs/nota.md", "Resumen del cambio.\n\nCo-Authored-By: "+"Cla"+"ude <noreply@"+"anthropic.com>\n")
	write(t, root, "README.md", strings.Repeat("línea\n", 200))
	res := lint()
	got := map[string]bool{}
	for _, f := range res.Findings {
		got[f.RuleID] = true
		if f.RuleID == "R10" && !f.Waived {
			t.Error("R10 debería estar dispensada")
		}
	}
	for _, id := range []string{"R5", "R15", "R10"} {
		if !got[id] {
			t.Errorf("falta hallazgo de %s: %+v", id, res.Findings)
		}
	}
	if res.Failing(false) != 2 {
		t.Errorf("se esperaban 2 MUST (R5 y R15), hay %d", res.Failing(false))
	}

	// Una dispensa sin motivo no aplica a reglas MUST, pero sí a SHOULD.
	ctx, err := NewContext(root, st, "backend", map[string]string{"R5": "", "R10": ""}, "manual", now)
	if err != nil {
		t.Fatal(err)
	}
	res = Lint(ctx, st)
	for _, f := range res.Findings {
		switch {
		case f.RuleID == "R5" && f.Waived:
			t.Error("R5 (MUST) no debe dispensarse sin motivo")
		case f.RuleID == "R10" && (!f.Waived || f.WaiveReason != "sin motivo"):
			t.Errorf("R10 (SHOULD) debería dispensarse sin motivo: %+v", f)
		}
	}
	if res.Failing(false) != 2 {
		t.Errorf("con dispensas sin motivo siguen 2 MUST, hay %d", res.Failing(false))
	}
}

func TestScriptsSoloConPermiso(t *testing.T) {
	root := t.TempDir()
	write(t, root, "coyote/standards/rules.yaml", "version: 1\nextends: none\nrules:\n  - id: P1\n    title: script de prueba\n    level: MUST\n    check: { type: script, script: 'touch ejecutado' }\n")
	st, err := Load(root, now)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := NewContext(root, st, "", nil, "manual", now)
	if err != nil {
		t.Fatal(err)
	}
	res := Lint(ctx, st)
	if _, err := os.Stat(filepath.Join(root, "ejecutado")); err == nil {
		t.Fatal("el script corrió sin permiso")
	}
	if res.ScriptsSkipped() != 1 {
		t.Fatalf("se esperaba 1 script omitido: %v", res.Skipped)
	}
	ctx.AllowScripts = true
	Lint(ctx, st)
	if _, err := os.Stat(filepath.Join(root, "ejecutado")); err != nil {
		t.Fatal("con --scripts el script debe correr")
	}
}

func TestRedefinicionVisible(t *testing.T) {
	root := t.TempDir()
	rules := "version: 1\nrules:\n  - id: R15\n    title: atribución solo en docs/\n    level: MUST\n    check: { type: attribution, paths: [\"docs/**\"] }\n"
	write(t, root, "coyote/standards/rules.yaml", rules)
	st, err := Load(root, now)
	if err != nil {
		t.Fatal(err)
	}
	// Sin reason, cambiar los checks de una MUST no se aplica y el lint falla con S1.
	if r := st.Find("R15"); r == nil || r.Source != DefaultRef || len(st.Unjustified) != 1 {
		t.Fatalf("sin reason rige la definición anterior: %+v %v", r, st.Unjustified)
	}
	ctx, err := NewContext(root, st, "", nil, "manual", now)
	if err != nil {
		t.Fatal(err)
	}
	if res := Lint(ctx, st); res.Failing(false) == 0 {
		t.Fatal("una redefinición sin reason debe hacer fallar el lint (S1)")
	}
	for _, trivial := range []string{"    profiles: [nadie]\n", "    reason: TODO\n"} {
		write(t, root, "coyote/standards/rules.yaml", "version: 1\nrules:\n  - id: R15\n    level: MUST\n"+trivial)
		st, err := Load(root, now)
		if err != nil {
			t.Fatal(err)
		}
		if trivial == "    profiles: [nadie]\n" && len(st.Unjustified) != 1 {
			t.Errorf("profiles: [nadie] sin reason debe rechazarse: %v", st.Unjustified)
		}
	}
	// Con un motivo real se aplica, queda anotada y avisa.
	write(t, root, "coyote/standards/rules.yaml", rules+"    reason: la spec de atribución vive fuera de docs\n")
	st, err = Load(root, now)
	if err != nil {
		t.Fatal(err)
	}
	r := st.Find("R15")
	if r == nil || r.Source != "proyecto" || !strings.Contains(r.Note, "redefinida en proyecto") || len(st.Unjustified) != 0 {
		t.Fatalf("la redefinición con reason debe aplicarse y quedar anotada: %+v", r)
	}
	if !strings.Contains(strings.Join(st.Warnings, " "), "R15") {
		t.Fatalf("redefinir una MUST del default debe avisar: %v", st.Warnings)
	}
}

func TestMeaningful(t *testing.T) {
	for _, bad := range []string{"", "-", "TODO", "xxx", "n/a", "ok", "  todo.  ", "Pendiente"} {
		if Meaningful(bad) {
			t.Errorf("%q no es un motivo", bad)
		}
	}
	for _, good := range []string{"README heredado", "la spec cita los patrones", "ADR-0003"} {
		if !Meaningful(good) {
			t.Errorf("%q es un motivo", good)
		}
	}
}

func TestHubDentroDelRepo(t *testing.T) {
	root := t.TempDir()
	write(t, root, "coyote/project.yaml", "version: 1\nname: demo\nhub: coyote/fakehub\n")
	write(t, root, "coyote/fakehub/coyote/standards/rules.yaml", "version: 1\nrules:\n  - id: R15\n    level: MUST\n    profiles: [nadie]\n")
	write(t, root, "coyote/standards/rules.yaml", "version: 1\nextends: hub\nrules: []\n")
	st, err := Load(root, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Unjustified) != 1 {
		t.Fatalf("un hub dentro del repo no tiene la exención del hub: %v %v", st.Layers, st.Unjustified)
	}
}
