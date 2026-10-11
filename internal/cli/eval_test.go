package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEvalConLineaBase cubre ADR-0025 en la CLI: medir, fijar la línea base y
// fallar cuando el contexto empeora.
func TestEvalConLineaBase(t *testing.T) {
	base := setup(t)
	root := filepath.Join(base, "tienda")
	must(t, run(t, base, "", "init", "tienda", "--purpose", "API de pedidos de la tienda demo"), 0, "init")
	must(t, run(t, root, "", "note", "los pedidos se confirman al capturar el pago", "--type", "inv", "--scope", "pedidos"), 0, "nota")
	must(t, run(t, root, "", "note", "los reembolsos pasan por la mesa de control", "--type", "dec", "--scope", "reembolsos"), 0, "nota 2")
	must(t, run(t, root, "", "eval"), 1, "sin golden")
	write(t, root, "evals/golden.v1.jsonl", `{"id":"g1","q":"¿cuándo se confirma un pedido?","expect":["ctx:pedidos"]}`+"\n"+
		`{"id":"g2","q":"¿quién aprueba un reembolso?","expect":["ctx:reembolsos"]}`+"\n")
	r := run(t, root, "", "eval")
	must(t, r, 1, "eval sin línea base: mide, pero no hay contra qué")
	if !strings.Contains(r.stdout, "recall@5               100.0 %") || !strings.Contains(r.stdout, "sin línea base") {
		t.Fatalf("eval:\n%s", r.stdout)
	}
	// El golden y la línea base viven en el proyecto; la línea base, en evals/.
	must(t, run(t, root, "", "eval", "--golden", "/dev/zero"), 2, "golden fuera del proyecto")
	must(t, run(t, root, "", "eval", "--golden", "../x.jsonl"), 2, "golden fuera del proyecto")
	must(t, run(t, root, "", "eval", "--update-baseline", "--baseline", ".claude//settings.json"), 2, "línea base fuera de evals/")
	must(t, run(t, root, "", "eval", "--update-baseline", "--baseline", "evals/x.txt"), 2, "línea base que no es JSON")
	must(t, run(t, root, "", "eval", "--update-baseline"), 0, "línea base")
	if b := readFile(t, filepath.Join(root, "evals", "baseline.json")); !strings.Contains(b, `"recall_at_k": 100`) || !strings.Contains(b, `"budget": 2000`) {
		t.Fatalf("baseline:\n%s", b)
	}
	r = run(t, root, "", "eval")
	must(t, r, 0, "eval igual a la base")
	if !strings.Contains(r.stdout, "✓ sin regresiones") {
		t.Errorf("eval:\n%s", r.stdout)
	}
	// Si el contexto pierde la fuente, la CI falla.
	ctx := filepath.Join(root, "CONTEXT.coyote.md")
	if err := os.WriteFile(ctx, []byte(strings.Replace(readFile(t, ctx), "dec|reembolsos|los reembolsos pasan por la mesa de control|-\n", "", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	r = run(t, root, "", "eval")
	must(t, r, 1, "regresión")
	if !strings.Contains(r.stdout, "✗ recall@k bajó de 100.0 a 50.0") {
		t.Errorf("regresión:\n%s", r.stdout)
	}
	must(t, run(t, root, "", "eval", "--budget", "3000"), 2, "otro presupuesto que la base")
	// Una pregunta nueva que sale bien no tapa la regresión: se compara sobre
	// las preguntas de la línea base.
	write(t, root, "evals/golden.v1.jsonl", `{"id":"g1","q":"¿cuándo se confirma un pedido?","expect":["ctx:pedidos"]}`+"\n"+
		`{"id":"g2","q":"¿quién aprueba un reembolso?","expect":["ctx:reembolsos"]}`+"\n"+
		`{"id":"g3","q":"¿qué pasa al capturar el pago de un pedido?","expect":["ctx:pedidos"]}`+"\n")
	if r = run(t, root, "", "eval"); r.code != 1 || !strings.Contains(r.stdout, "✗ recall@k bajó de 100.0 a 50.0") {
		t.Errorf("una pregunta nueva no tapa la regresión: %d\n%s", r.code, r.stdout)
	}
	// Una copia de otra pregunta no entra al golden.
	write(t, root, "evals/golden.v1.jsonl", `{"id":"g1","q":"¿cuándo se confirma un pedido?","expect":["ctx:pedidos"]}`+"\n"+
		`{"id":"g9","q":"¿Cuándo  se confirma un pedido?","expect":["ctx:pedidos"]}`+"\n")
	if r = run(t, root, "", "eval"); r.code != 1 || !strings.Contains(r.stderr, "repite la pregunta g1") {
		t.Errorf("pregunta repetida: %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	// Quitar una pregunta del golden para esconder la regresión no pasa.
	write(t, root, "evals/golden.v1.jsonl", `{"id":"g1","q":"¿cuándo se confirma un pedido?","expect":["ctx:pedidos"]}`+"\n")
	r = run(t, root, "", "eval")
	must(t, r, 1, "pregunta quitada")
	if !strings.Contains(r.stdout, "faltan 1 preguntas de la línea base (g2)") {
		t.Errorf("pregunta quitada:\n%s", r.stdout)
	}
	write(t, root, "evals/golden.v1.jsonl", "# sin preguntas\n")
	must(t, run(t, root, "", "eval", "--update-baseline"), 1, "golden vacío")
}
