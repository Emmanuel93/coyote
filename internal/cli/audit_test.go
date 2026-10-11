package cli

import (
	"encoding/json"
	"os"
	"os/exec"
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
	if !strings.Contains(r.stdout, "cambia, borra o esconde líneas de coyote/ledger/") {
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
	falla(root, "submódulo", "cambia, borra o esconde líneas de")

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
	falla(sub, "subcarpeta con acentos", "cambia, borra o esconde líneas de")

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

// TestAuditSegundaRevision: segunda revisión adversarial de v1.0. Un merge
// con -s ours y un fast-forward, un commit con fecha vieja, un
// .gitattributes con -diff, un squash y un checkout con CRLF.
func TestAuditSegundaRevision(t *testing.T) {
	base := setup(t)
	nuevo := func(name string) (string, string) {
		t.Helper()
		must(t, run(t, base, "", "init", name, "--purpose", "API de pedidos de la tienda demo"), 0, "init "+name)
		root := filepath.Join(base, name)
		git(t, root, "add", "-A")
		git(t, root, "commit", "-qm", "chore: adopta coyote")
		files, _ := filepath.Glob(filepath.Join(root, "coyote", "ledger", "*", "*", "*.ccf"))
		rel, _ := filepath.Rel(root, files[0])
		return root, filepath.ToSlash(rel)
	}
	report := func(root string, args ...string) result {
		t.Helper()
		return run(t, root, "", append([]string{"audit"}, args...)...)
	}

	// Un merge -s ours cuyo primer padre no tiene la línea, y main que avanza por fast-forward.
	root, f := nuevo("ours")
	c0 := strings.TrimSpace(git(t, root, "rev-parse", "HEAD"))
	clean := readFile(t, filepath.Join(root, f))
	must(t, run(t, root, "", "record", "fix", "el agente borró la base de producción"), 0, "record")
	git(t, root, "commit", "-qam", "chore: registro")
	git(t, root, "checkout", "-q", "-b", "limpieza", c0)
	if err := os.WriteFile(filepath.Join(root, f), []byte(clean+"2026-10-01T10:00Z|@ana|-|ours|feat|-|otra cosa|-|-|-|ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "commit", "-qam", "chore: limpieza")
	git(t, root, "merge", "-q", "-s", "ours", "main", "-m", "merge")
	git(t, root, "checkout", "-q", "main")
	git(t, root, "merge", "-q", "--ff-only", "limpieza")
	if r := report(root); r.code != 1 || !strings.Contains(r.stdout, "frente a su padre") {
		t.Errorf("merge -s ours con fast-forward: %d\n%s", r.code, r.stdout)
	}

	// Un commit con fecha vieja no esconde los anteriores del mes, ni un -diff.
	root, _ = nuevo("fechas")
	past := time.Now().UTC().AddDate(0, 0, -40).Format(time.RFC3339)
	write(t, root, ".gitattributes", "coyote/ledger/** -diff\n")
	must(t, run(t, root, "", "record", "apr", "despliegue aprobado", "--agent", "coyote-scribe", "--ts", past), 0, "record")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "chore: registro", "-m", aiTrailer, "--no-verify")
	old := exec.Command("git", "-C", root, "commit", "-q", "--allow-empty", "-m", "chore: reloj atrasado")
	old.Env = append(os.Environ(), "GIT_COMMITTER_DATE="+time.Now().UTC().AddDate(0, -2, 0).Format(time.RFC3339))
	if out, err := old.CombinedOutput(); err != nil {
		t.Fatalf("commit con fecha vieja: %v\n%s", err, out)
	}
	git(t, root, "commit", "-q", "--allow-empty", "-m", "chore: hoy")
	r := report(root)
	for _, want := range []string{"| eventos con fecha anterior a su commit | falla |", "| commits del periodo sin atribución a IA (R15) | falla |", "tiene fecha anterior a la de su padre"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("falta %q:\n%s", want, r.stdout)
		}
	}

	// Una aprobación de la rama, registrada el mismo mes e integrada con
	// --no-ff días después, se mide contra el commit que la trajo.
	root, _ = nuevo("rama")
	git(t, root, "checkout", "-q", "-b", "feature")
	day := time.Now().UTC()
	if day.Day() < 4 {
		t.Skip("al principio del mes la prueba cruzaría de mes")
	}
	must(t, run(t, root, "", "record", "fix", "un arreglo en la rama", "--ts", day.Add(-2*time.Hour).Format(time.RFC3339)), 0, "record")
	git(t, root, "commit", "-qam", "fix: rama")
	git(t, root, "checkout", "-q", "main")
	merge := exec.Command("git", "-C", root, "merge", "-q", "--no-ff", "feature", "-m", "merge")
	merge.Env = append(os.Environ(), "GIT_COMMITTER_DATE="+day.Add(72*time.Hour).Format(time.RFC3339))
	if out, err := merge.CombinedOutput(); err != nil {
		t.Fatalf("merge: %v\n%s", err, out)
	}
	if r := report(root); strings.Contains(r.stdout, "| eventos con fecha anterior a su commit | falla |") {
		t.Errorf("lo que llega por un merge se mide contra su commit:\n%s", r.stdout)
	}

	// Un checkout con CRLF no es un ledger reescrito.
	root, f = nuevo("crlf")
	write(t, root, ".gitattributes", "* text=auto eol=crlf\n")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "chore: atributos")
	if err := os.Remove(filepath.Join(root, f)); err != nil {
		t.Fatal(err)
	}
	git(t, root, "checkout", "--", f)
	if r := report(root); strings.Contains(r.stdout, "sin commit") {
		t.Errorf("CRLF del checkout:\n%s", r.stdout)
	}

	// La huella de un mes no cambia cuando llegan eventos de otro mes.
	root, _ = nuevo("huella")
	lastMonth := time.Now().UTC().AddDate(0, -1, 0).Format("2006-01")
	sum := func() string {
		t.Helper()
		r := report(root, "--month", lastMonth, "--json")
		var rep struct {
			Sum string `json:"ledger_sha256"`
		}
		if err := json.Unmarshal([]byte(r.stdout), &rep); err != nil {
			t.Fatalf("--json: %v\n%s", err, r.stdout)
		}
		return rep.Sum
	}
	before := sum()
	must(t, run(t, root, "", "record", "feat", "algo de este mes"), 0, "record")
	git(t, root, "commit", "-qam", "chore: registro")
	if after := sum(); after != before {
		t.Errorf("la huella del mes pasado cambió con un evento de este mes: %s → %s", before, after)
	}
}

// TestAuditTerceraRevision: tercera revisión adversarial de v1.0. Lo que
// llega por un merge cuenta cuando llega; un archivo sin salto final no es
// una reescritura; la huella es la de lo que el reporte leyó; los injertos no
// esconden la historia.
func TestAuditTerceraRevision(t *testing.T) {
	base := setup(t)
	nuevo := func(name string) string {
		t.Helper()
		must(t, run(t, base, "", "init", name, "--purpose", "API de pedidos de la tienda demo"), 0, "init "+name)
		root := filepath.Join(base, name)
		git(t, root, "add", "-A")
		git(t, root, "commit", "-qm", "chore: adopta coyote")
		return root
	}
	at := func(root string, date time.Time, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_COMMITTER_DATE="+date.Format(time.RFC3339), "GIT_AUTHOR_DATE="+date.Format(time.RFC3339))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	now := time.Now().UTC()
	lastMonth := now.AddDate(0, -1, 0)

	// Una aprobación de un agente con fecha del mes pasado, dentro de un merge.
	root := nuevo("merge")
	git(t, root, "checkout", "-q", "-b", "lado")
	git(t, root, "commit", "-q", "--allow-empty", "-m", "chore: lado")
	git(t, root, "checkout", "-q", "main")
	git(t, root, "merge", "-q", "--no-ff", "--no-commit", "lado")
	must(t, run(t, root, "", "record", "apr", "G4 aprobado: release 1.1", "--agent", "coyote-scribe", "--ts", lastMonth.Format(time.RFC3339)), 0, "record")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "merge: lado")
	if r := run(t, root, "", "audit"); !strings.Contains(r.stdout, "cae en un mes anterior") {
		t.Errorf("la aprobación dentro del merge:\n%s", r.stdout)
	}

	// Una rama con fechas del mes pasado que se integra hoy: cuenta hoy.
	root = nuevo("rama")
	git(t, root, "checkout", "-q", "-b", "vieja")
	must(t, run(t, root, "", "record", "apr", "G4 aprobado: release 1.1", "--agent", "coyote-scribe", "--ts", lastMonth.Format(time.RFC3339)), 0, "record")
	git(t, root, "add", "-A")
	at(root, lastMonth.Add(30*time.Minute), "commit", "-qm", "feat: algo", "-m", aiTrailer, "--no-verify")
	git(t, root, "checkout", "-q", "main")
	git(t, root, "merge", "-q", "--no-ff", "vieja", "-m", "merge: vieja")
	r := run(t, root, "", "audit")
	for _, want := range []string{"cae en un mes anterior", "| commits del periodo sin atribución a IA (R15) | falla |"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("rama vieja integrada hoy: falta %q\n%s", want, r.stdout)
		}
	}

	// Un commit con fecha futura se avisa.
	root = nuevo("futuro")
	at(root, now.AddDate(0, 5, 0), "commit", "-q", "--allow-empty", "-m", "chore: futuro")
	if r := run(t, root, "", "audit"); !strings.Contains(r.stdout, "fecha futura") {
		t.Errorf("fecha futura:\n%s", r.stdout)
	}

	// Un archivo del ledger sin salto final, y después una línea con coyote.
	root = nuevo("salto")
	files, _ := filepath.Glob(filepath.Join(root, "coyote", "ledger", "*", "*", "*.ccf"))
	text := strings.TrimRight(readFile(t, files[0]), "\n") + "\n" + now.Format("2006-01-02T15:04Z") + "|@ana|-|salto|feat|-|a mano|-|-|-|ok"
	if err := os.WriteFile(files[0], []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "commit", "-qam", "chore: a mano")
	must(t, run(t, root, "", "record", "feat", "alta de pedidos"), 0, "record")
	git(t, root, "commit", "-qam", "chore: registro")
	if r := run(t, root, "", "audit"); strings.Contains(r.stdout, "| el ledger solo agrega líneas | falla |") {
		t.Errorf("agregar el salto final no es una reescritura:\n%s", r.stdout)
	}

	// La huella es la misma con la línea con commit o sin él.
	root = nuevo("huella")
	sum := func() string {
		t.Helper()
		var rep struct {
			Sum string `json:"ledger_sha256"`
		}
		r := run(t, root, "", "audit", "--json")
		if err := json.Unmarshal([]byte(r.stdout), &rep); err != nil {
			t.Fatalf("--json: %v\n%s", err, r.stdout)
		}
		return rep.Sum
	}
	must(t, run(t, root, "", "record", "feat", "algo"), 0, "record")
	before := sum()
	git(t, root, "commit", "-qam", "chore: registro")
	if after := sum(); after != before {
		t.Errorf("la huella cambió al hacer commit del mismo contenido")
	}

	// Un injerto no esconde un commit que borra una línea.
	root = nuevo("injerto")
	must(t, run(t, root, "", "record", "fix", "corrige totales"), 0, "record")
	git(t, root, "commit", "-qam", "chore: registro")
	files, _ = filepath.Glob(filepath.Join(root, "coyote", "ledger", "*", "*", "*.ccf"))
	if err := os.WriteFile(files[0], []byte(strings.Replace(readFile(t, files[0]), "corrige totales", "otra cosa", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "commit", "-qam", "chore: borra")
	x := strings.TrimSpace(git(t, root, "rev-parse", "HEAD"))
	orphan := strings.TrimSpace(git(t, root, "commit-tree", x+"^{tree}", "-m", "raíz falsa"))
	if err := os.WriteFile(filepath.Join(root, ".git", "info", "grafts"), []byte(x+" "+orphan+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := run(t, root, "", "audit"); !strings.Contains(r.stdout, "| el ledger solo agrega líneas | falla |") {
		t.Errorf("un injerto escondió la reescritura:\n%s", r.stdout)
	}
}
