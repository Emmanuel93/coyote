package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
		Checks  []struct{ Name, Result string } `json:"checks"`
		Commits int                             `json:"commits"`
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

// TestAuditNoSeEvade: revisión adversarial de v1.0. Las formas de esconder
// una reescritura del ledger o una aprobación que encontró la revisión.
func TestAuditNoSeEvade(t *testing.T) {
	base := setup(t)
	nuevo := func(name string) (string, string) {
		t.Helper()
		must(t, run(t, base, "", "init", name, "--purpose", "API de pedidos de la tienda demo"), 0, "init "+name)
		root := filepath.Join(base, name)
		must(t, run(t, root, "", "record", "feat", "alta de pedidos"), 0, "record")
		must(t, run(t, root, "", "record", "fix", "corrige totales"), 0, "record")
		git(t, root, "add", "-A")
		git(t, root, "commit", "-qm", "chore: adopta coyote")
		files, _ := filepath.Glob(filepath.Join(root, "coyote", "ledger", "*", "*", "*.ccf"))
		if len(files) != 1 {
			t.Fatalf("ledger: %v", files)
		}
		rel, _ := filepath.Rel(root, files[0])
		return root, filepath.ToSlash(rel)
	}
	falla := func(root, que, want string) {
		t.Helper()
		r := run(t, root, "", "audit")
		if r.code != 1 || !strings.Contains(r.stdout, want) {
			t.Errorf("%s: se esperaba falla con %q (código %d):\n%s", que, want, r.code, r.stdout)
		}
	}

	// Un archivo del ledger que pasa a ser un submódulo.
	root, f := nuevo("gitlink")
	head := strings.TrimSpace(git(t, root, "rev-parse", "HEAD"))
	git(t, root, "rm", "-q", "--cached", f)
	if err := os.Remove(filepath.Join(root, f)); err != nil {
		t.Fatal(err)
	}
	git(t, root, "update-index", "--add", "--cacheinfo", "160000,"+head+","+f)
	git(t, root, "commit", "-qm", "chore: limpieza")
	falla(root, "submódulo", "cambia el tipo de")

	// Un renombre preparado con una edición, y skip-worktree.
	root, f = nuevo("renombre")
	moved := strings.Replace(f, ".ccf", "-x.ccf", 1)
	git(t, root, "mv", f, moved)
	p := filepath.Join(root, moved)
	if err := os.WriteFile(p, []byte(strings.Replace(readFile(t, p), "corrige totales", "corrige otra cosa", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	falla(root, "renombre preparado", "se borró o se movió y no tiene commit")
	root, f = nuevo("skip")
	git(t, root, "update-index", "--skip-worktree", f)
	p = filepath.Join(root, f)
	if err := os.WriteFile(p, []byte(strings.Replace(readFile(t, p), "corrige totales", "corrige otra cosa", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	falla(root, "skip-worktree", "tiene líneas cambiadas o borradas sin commit")

	// Un proyecto en una subcarpeta con acentos, con commit y sin él.
	mono := filepath.Join(base, "mono")
	if err := os.MkdirAll(mono, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, mono, "init", "-q")
	git(t, mono, "commit", "-q", "--allow-empty", "-m", "chore: inicio")
	must(t, run(t, mono, "", "init", "facturación", "--no-git", "--purpose", "facturación de la tienda"), 0, "init en subcarpeta")
	sub := filepath.Join(mono, "facturación")
	must(t, run(t, sub, "", "record", "feat", "alta de pedidos"), 0, "record")
	must(t, run(t, sub, "", "record", "fix", "corrige totales"), 0, "record")
	git(t, mono, "add", "-A")
	git(t, mono, "commit", "-qm", "chore: facturación")
	files, _ := filepath.Glob(filepath.Join(sub, "coyote", "ledger", "*", "*", "*.ccf"))
	if len(files) != 1 {
		t.Fatalf("ledger de la subcarpeta: %v", files)
	}
	if err := os.WriteFile(files[0], []byte(strings.Replace(readFile(t, files[0]), "corrige totales", "corrige otra cosa", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	falla(sub, "subcarpeta sin commit", "tiene líneas cambiadas o borradas sin commit")
	git(t, mono, "commit", "-qam", "chore: arreglo")
	falla(sub, "subcarpeta con acentos", "cambia o borra líneas de")

	// Un agente que registra una aprobación con fecha pasada.
	root, _ = nuevo("atrasado")
	past := time.Now().UTC().AddDate(0, 0, -20).Format(time.RFC3339)
	must(t, run(t, root, "", "record", "apr", "despliegue a producción aprobado", "--agent", "coyote-scribe", "--ts", past), 0, "record atrasado")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "chore: registro")
	falla(root, "aprobación con fecha pasada", "con fecha "+past[:10])

	// Un clon superficial avisa que no revisó toda la historia.
	root, _ = nuevo("original")
	git(t, root, "commit", "-q", "--allow-empty", "-m", "chore: otro")
	shallow := filepath.Join(base, "superficial")
	git(t, base, "clone", "-q", "--depth", "1", "file://"+root, shallow)
	r := run(t, shallow, "", "audit")
	if !strings.Contains(r.stdout, "| el ledger solo agrega líneas | aviso |") || !strings.Contains(r.stdout, "superficial") {
		t.Errorf("clon superficial:\n%s", r.stdout)
	}
}
