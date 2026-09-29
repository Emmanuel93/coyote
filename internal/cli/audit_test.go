package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAuditConHistoriaDeGit cubre ADR-0024: el reporte encuentra un ledger
// reescrito (con commit o sin él), un commit con atribución a IA y una línea
// en el archivo de otra persona.
func TestAuditConHistoriaDeGit(t *testing.T) {
	base := setup(t)
	root := filepath.Join(base, "tienda")
	must(t, run(t, base, "", "init", "tienda", "--purpose", "API de pedidos de la tienda demo"), 0, "init")
	must(t, run(t, root, "", "record", "run", "implementa pedidos", "--agent", "coyote-dev", "--cost", "0.01+0.02", "--refs", "model:sonnet-x"), 0, "record")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "chore: adopta coyote")

	r := run(t, root, "", "audit")
	must(t, r, 0, "audit limpio")
	for _, want := range []string{"# Auditoría de tienda", "| el ledger solo agrega líneas | ok |", "| actor, archivo y autor del commit coinciden | ok |",
		"| @ana/coyote-dev | 1 | $0.03 |", "A.6.2.8 registro de eventos"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("falta %q:\n%s", want, r.stdout)
		}
	}
	files, _ := filepath.Glob(filepath.Join(root, "coyote", "ledger", "*", "*", "*.ccf"))
	if len(files) != 1 {
		t.Fatalf("ledger: %v", files)
	}
	orig := readFile(t, files[0])

	// Sin commit: cambiar una línea ya es falla.
	if err := os.WriteFile(files[0], []byte(strings.Replace(orig, "implementa pedidos", "no pasó nada", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	r = run(t, root, "", "audit")
	must(t, r, 1, "ledger cambiado sin commit")
	if !strings.Contains(r.stdout, "tiene líneas cambiadas o borradas sin commit") {
		t.Errorf("reescritura sin commit:\n%s", r.stdout)
	}
	// Con commit: la historia lo guarda aunque el árbol quede limpio.
	git(t, root, "commit", "-qam", "chore: arreglo")
	r = run(t, root, "", "audit")
	must(t, r, 1, "ledger reescrito en un commit")
	if !strings.Contains(r.stdout, "cambia o borra líneas de coyote/ledger/") {
		t.Errorf("reescritura en la historia:\n%s", r.stdout)
	}

	// Un commit con atribución a IA también es falla (R15).
	git(t, root, "commit", "-q", "--allow-empty", "--no-verify", "-m", "feat: algo", "-m", aiTrailer)
	r = run(t, root, "", "audit", "--json")
	must(t, r, 1, "atribución")
	var rep struct {
		Checks []struct{ Name, Result string } `json:"checks"`
		Commits int                            `json:"commits"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &rep); err != nil {
		t.Fatalf("--json: %v\n%s", err, r.stdout)
	}
	got := map[string]string{}
	for _, c := range rep.Checks {
		got[c.Name] = c.Result
	}
	if got["commits del periodo sin atribución a IA (R15)"] != "falla" || got["el ledger solo agrega líneas"] != "falla" || rep.Commits != 3 {
		t.Errorf("verificaciones: %v, %d commits", got, rep.Commits)
	}

	// --write deja el reporte para revisarlo.
	r = run(t, root, "", "audit", "--write")
	must(t, r, 1, "write con fallas")
	written, _ := filepath.Glob(filepath.Join(root, "coyote", "audit", "*.md"))
	if len(written) != 1 || !strings.Contains(readFile(t, written[0]), "# Auditoría de tienda") || !strings.Contains(r.stdout, "revísalo y hazle commit") {
		t.Errorf("--write: %v\n%s", written, r.stdout)
	}
	must(t, run(t, root, "", "audit", "--month", "2026-13"), 2, "mes inválido")
}
