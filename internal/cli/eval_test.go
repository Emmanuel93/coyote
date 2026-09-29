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
	must(t, r, 0, "eval sin línea base")
	if !strings.Contains(r.stdout, "recall@5               100.0 %") || !strings.Contains(r.stdout, "sin línea base") {
		t.Fatalf("eval:\n%s", r.stdout)
	}
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
}
