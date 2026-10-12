package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

// TestAuditCuartaRevision: cuarta pasada adversarial de v1.0. Una fecha
// futura no esconde los commits que le siguen, un borrado anterior al
// periodo se avisa y la verificación de firmas no corre los programas que
// declara la configuración del repo.
func TestAuditCuartaRevision(t *testing.T) {
	base := setup(t)
	now := time.Now().UTC()
	at := func(root string, date time.Time, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_COMMITTER_DATE="+date.Format(time.RFC3339), "GIT_AUTHOR_DATE="+date.Format(time.RFC3339))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// nuevo deja un proyecto con dos eventos y un commit de adopción con la fecha dada.
	nuevo := func(name string, when time.Time) (string, string) {
		t.Helper()
		must(t, run(t, base, "", "init", name, "--purpose", "API de pedidos de la tienda demo"), 0, "init "+name)
		root := filepath.Join(base, name)
		must(t, run(t, root, "", "record", "feat", "alta de pedidos"), 0, "record")
		must(t, run(t, root, "", "record", "fix", "corrige totales"), 0, "record")
		git(t, root, "add", "-A")
		at(root, when, "commit", "-qm", "chore: adopta coyote")
		files, _ := filepath.Glob(filepath.Join(root, "coyote", "ledger", "*", "*", "*.ccf"))
		if len(files) != 1 {
			t.Fatalf("ledger: %v", files)
		}
		rel, _ := filepath.Rel(root, files[0])
		return root, filepath.ToSlash(rel)
	}
	rewrite := func(root, f string) {
		t.Helper()
		p := filepath.Join(root, f)
		if err := os.WriteFile(p, []byte(strings.Replace(readFile(t, p), "corrige totales", "otra cosa", 1)), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Un commit con fecha de 2099 no esconde los que le siguen: llega, a más
	// tardar, ahora. Lo que viene después, con el borrado y la atribución, se ve.
	root, f := nuevo("futuro", now)
	at(root, time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC), "commit", "-q", "--allow-empty", "-m", "chore: futuro")
	rewrite(root, f)
	git(t, root, "commit", "-qam", "chore: borra", "-m", aiTrailer, "--no-verify")
	r := run(t, root, "", "audit")
	must(t, r, 1, "fecha futura")
	for _, want := range []string{"cambia, borra o esconde líneas de", "| commits del periodo sin atribución a IA (R15) | falla |", "fecha futura"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("fecha de 2099: falta %q\n%s", want, r.stdout)
		}
	}

	// Un borrado en un commit que llegó antes del periodo se avisa, con el mes
	// en que llegó: es historia que un reporte anterior pudo no ver.
	root, f = nuevo("viejo", now.AddDate(0, 0, -90))
	rewrite(root, f)
	at(root, now.AddDate(0, 0, -70), "commit", "-qam", "chore: borra")
	at(root, now, "commit", "-q", "--allow-empty", "-m", "chore: hoy")
	r = run(t, root, "", "audit")
	must(t, r, 0, "borrado anterior al periodo")
	for _, want := range []string{"| el ledger solo agrega líneas | aviso |", "cambia, borra o esconde líneas de", "llegó en " + now.AddDate(0, 0, -70).Format("2006-01")} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("borrado anterior al periodo: falta %q\n%s", want, r.stdout)
		}
	}

	// Los programas de firma de la configuración del repo no corren: un repo
	// ajeno no ejecuta nada al auditarlo. Una firma se cuenta, no se verifica.
	root, _ = nuevo("firma", now)
	ran := filepath.Join(root, ".git", "programa-corrio")
	prog := filepath.Join(root, ".git", "programa.sh")
	if err := os.WriteFile(prog, []byte("#!/bin/sh\ntouch '"+ran+"'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	signers := filepath.Join(root, ".git", "firmantes")
	if err := os.WriteFile(signers, []byte("ana@example.com ssh-ed25519 AAAA\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"gpg.program", "gpg.ssh.program", "gpg.x509.program", "gpg.openpgp.program"} {
		git(t, root, "config", k, prog)
	}
	git(t, root, "config", "gpg.ssh.allowedSignersFile", signers)
	git(t, root, "config", "log.showSignature", "true")
	for _, kind := range []string{"PGP SIGNATURE", "SSH SIGNATURE", "SIGNED MESSAGE"} {
		tree := strings.TrimSpace(git(t, root, "rev-parse", "HEAD^{tree}"))
		parent := strings.TrimSpace(git(t, root, "rev-parse", "HEAD"))
		stamp := strconv.FormatInt(now.Unix(), 10) + " +0000"
		obj := "tree " + tree + "\nparent " + parent + "\nauthor Ana <ana@example.com> " + stamp + "\ncommitter Ana <ana@example.com> " + stamp +
			"\ngpgsig -----BEGIN " + kind + "-----\n \n AAAA\n -----END " + kind + "-----\n\nchore: firmado\n"
		cmd := exec.Command("git", "-C", root, "hash-object", "-t", "commit", "-w", "--stdin")
		cmd.Stdin = strings.NewReader(obj)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("hash-object: %v", err)
		}
		git(t, root, "update-ref", "refs/heads/main", strings.TrimSpace(string(out)))
	}
	r = run(t, root, "", "audit", "--json")
	must(t, r, 0, "commits con firma")
	if _, err := os.Stat(ran); err == nil {
		t.Errorf("la auditoría corrió el programa de firma que declara el repo")
	}
	var rep struct {
		Commits int `json:"commits"`
		Signed  int `json:"signed"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &rep); err != nil {
		t.Fatalf("--json: %v\n%s", err, r.stdout)
	}
	if rep.Commits != 4 || rep.Signed != 3 {
		t.Errorf("4 commits con 3 firmas: %+v\n%s", rep, r.stdout)
	}
}

// auditAt corre git en root con la fecha de autor y de committer dadas.
func auditAt(t *testing.T, root string, date time.Time, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_COMMITTER_DATE="+date.Format(time.RFC3339), "GIT_AUTHOR_DATE="+date.Format(time.RFC3339))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// auditNuevo deja un proyecto con dos eventos y un commit de adopción con la
// fecha dada. Devuelve la raíz y la ruta de su archivo del ledger.
func auditNuevo(t *testing.T, base, name string, when time.Time) (string, string) {
	t.Helper()
	must(t, run(t, base, "", "init", name, "--purpose", "API de pedidos de la tienda demo"), 0, "init "+name)
	root := filepath.Join(base, name)
	must(t, run(t, root, "", "record", "feat", "alta de pedidos"), 0, "record")
	must(t, run(t, root, "", "record", "fix", "corrige totales"), 0, "record")
	git(t, root, "add", "-A")
	auditAt(t, root, when, "commit", "-qm", "chore: adopta coyote")
	files, _ := filepath.Glob(filepath.Join(root, "coyote", "ledger", "*", "*", "*.ccf"))
	if len(files) != 1 {
		t.Fatalf("ledger: %v", files)
	}
	rel, _ := filepath.Rel(root, files[0])
	return root, filepath.ToSlash(rel)
}

// auditRewrite cambia una línea del ledger sin agregar nada.
func auditRewrite(t *testing.T, root, f string) {
	t.Helper()
	p := filepath.Join(root, f)
	if err := os.WriteFile(p, []byte(strings.Replace(readFile(t, p), "corrige totales", "otra cosa", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// auditCommits devuelve cuántos commits cuenta el reporte JSON del mes.
func auditCommits(t *testing.T, root, month string) int {
	t.Helper()
	r := run(t, root, "", "audit", "--month", month, "--json")
	var rep struct {
		Commits int `json:"commits"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &rep); err != nil {
		t.Fatalf("--json: %v\n%s", err, r.stdout)
	}
	return rep.Commits
}

// TestAuditQuintaRevision: quinta pasada adversarial de v1.0. Una fecha
// futura no saca del mes cerrado a los commits que le siguen.
func TestAuditQuintaRevision(t *testing.T) {
	base := setup(t)
	now := time.Now().UTC()
	closed := time.Date(now.Year(), now.Month()-2, 1, 12, 0, 0, 0, time.UTC)
	month := closed.Format("2006-01")

	// El reporte de un mes cerrado, que es como se escribe con --write, ve los
	// commits que le siguen a uno con la fecha adelantada: el de fecha futura
	// llega con su sucesor y el resto conserva su fecha.
	root, f := auditNuevo(t, base, "cerrado", closed)
	auditAt(t, root, time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC), "commit", "-q", "--allow-empty", "-m", "chore: reloj adelantado")
	auditAt(t, root, time.Date(2098, 1, 1, 0, 0, 0, 0, time.UTC), "commit", "-q", "--allow-empty", "-m", "chore: otro reloj adelantado")
	auditRewrite(t, root, f)
	auditAt(t, root, closed.AddDate(0, 0, 4), "commit", "-qam", "chore: borra", "--no-verify")
	auditAt(t, root, closed.AddDate(0, 0, 9), "commit", "-q", "--allow-empty", "--no-verify", "-m", "feat: algo", "-m", aiTrailer)
	r := run(t, root, "", "audit", "--month", month)
	must(t, r, 1, "mes cerrado con fechas futuras")
	for _, want := range []string{"cambia, borra o esconde líneas de", "| commits del periodo sin atribución a IA (R15) | falla |", "fecha futura"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("mes cerrado: falta %q\n%s", want, r.stdout)
		}
	}
	if n := auditCommits(t, root, month); n != 5 {
		t.Errorf("el mes cerrado cuenta 5 commits (adopción, dos con fecha adelantada, borrado y atribución), cuenta %d", n)
	}
	// El mes en curso no repite lo que ya llegó en el cerrado.
	if n := auditCommits(t, root, now.Format("2006-01")); n != 0 {
		t.Errorf("el mes en curso no tiene commits, cuenta %d", n)
	}

	// El textconv que declara la configuración del repo no corre: auditar no
	// ejecuta nada ajeno. git blame lo corre por defecto.
	root, _ = auditNuevo(t, base, "textconv", now)
	ranConv := filepath.Join(root, ".git", "textconv-corrio")
	conv := filepath.Join(root, ".git", "textconv.sh")
	if err := os.WriteFile(conv, []byte("#!/bin/sh\ntouch '"+ranConv+"'\ncat \"$1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "info", "attributes"), []byte("* diff=drv\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "config", "diff.drv.textconv", conv)
	for _, args := range [][]string{{"audit"}, {"audit", "--json"}} {
		must(t, run(t, root, "", args...), 0, "audit con textconv")
		if _, err := os.Stat(ranConv); err == nil {
			t.Fatalf("coyote %v corrió el textconv que declara el repo", args)
		}
	}

	// diff.noprefix en la configuración no cambia lo que se lee de la historia
	// anterior al periodo: sin los prefijos a/ y b/ el aviso salía sin nombre
	// de archivo y una línea que un commit movió a otro archivo se compensaba.
	root, f = auditNuevo(t, base, "noprefix", now.AddDate(0, 0, -70))
	git(t, root, "config", "diff.noprefix", "true")
	lines := strings.Split(strings.TrimRight(readFile(t, filepath.Join(root, f)), "\n"), "\n")
	if err := os.WriteFile(filepath.Join(root, f), []byte(strings.Join(lines[:len(lines)-1], "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	otro := filepath.Join(root, "coyote", "ledger", "2020", "01", "01-ana.ccf")
	if err := os.MkdirAll(filepath.Dir(otro), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otro, []byte(lines[len(lines)-1]+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "-A")
	auditAt(t, root, now.AddDate(0, 0, -69), "commit", "-qm", "chore: mueve una línea")
	auditAt(t, root, now, "commit", "-q", "--allow-empty", "-m", "chore: hoy")
	r = run(t, root, "", "audit")
	for _, want := range []string{"| el ledger solo agrega líneas | aviso |", "cambia, borra o esconde líneas de " + f} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("diff.noprefix: falta %q\n%s", want, r.stdout)
		}
	}

	// Un merge anterior al periodo que quita una línea de uno de sus padres se
	// avisa contra ese padre. Lo lee el mismo git log que el resto de la
	// historia anterior: un diff por padre.
	root, f = auditNuevo(t, base, "merge-previo", now.AddDate(0, 0, -90))
	// La configuración de la persona no cambia el formato que se lee: hashes
	// abreviados, decoraciones y color forzado.
	for k, v := range map[string]string{"log.abbrevCommit": "true", "log.decorate": "full", "color.ui": "always", "diff.mnemonicPrefix": "true"} {
		git(t, root, "config", k, v)
	}
	git(t, root, "checkout", "-q", "-b", "lado")
	lado := filepath.Join(root, "coyote", "ledger", "2020", "01", "02-ana.ccf")
	if err := os.MkdirAll(filepath.Dir(lado), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lado, []byte("# otro archivo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "-A")
	auditAt(t, root, now.AddDate(0, 0, -80), "commit", "-qm", "chore: lado")
	git(t, root, "checkout", "-q", "main")
	orig := readFile(t, filepath.Join(root, f))
	if err := os.WriteFile(filepath.Join(root, f), []byte(orig+"extra\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	auditAt(t, root, now.AddDate(0, 0, -79), "commit", "-qam", "chore: main")
	padre := strings.TrimSpace(git(t, root, "rev-parse", "--short=7", "HEAD"))
	git(t, root, "merge", "-q", "--no-ff", "--no-commit", "lado")
	if err := os.WriteFile(filepath.Join(root, f), []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	auditAt(t, root, now.AddDate(0, 0, -70), "commit", "-qam", "merge: lado")
	auditAt(t, root, now, "commit", "-q", "--allow-empty", "-m", "chore: hoy")
	r = run(t, root, "", "audit")
	for _, want := range []string{"| el ledger solo agrega líneas | aviso |", "cambia, borra o esconde líneas de " + f + " frente a su padre " + padre} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("merge anterior al periodo: falta %q\n%s", want, r.stdout)
		}
	}

	// Un mensaje en otra codificación (encoding IBM037) se lee como lo lee
	// quien lo ve: git lo convierte a UTF-8. Los bytes crudos no muestran el
	// trailer, y el hook commit-msg tampoco lo vio.
	root, _ = auditNuevo(t, base, "ebcdic", now)
	msg := ebcdic037("feat: algo\n\nCo-Authored-By: Claude <noreply@anthropic.com>\n")
	sha := commitCrudo(t, root, "encoding IBM037\n", msg)
	if out := git(t, root, "log", "-1", "--format=%B", "--encoding=UTF-8", sha); !strings.Contains(out, "Co-Authored-By") {
		t.Skip("este git no convierte IBM037: la prueba no puede armar el caso")
	}
	r = run(t, root, "", "audit")
	must(t, r, 1, "mensaje en IBM037")
	if !strings.Contains(r.stdout, "| commits del periodo sin atribución a IA (R15) | falla |") {
		t.Errorf("mensaje en IBM037: R15 no falla\n%s", r.stdout)
	}

	// Un mensaje con bytes nulos no se puede revisar: una verificación que no
	// pudo correr no da ok.
	root, _ = auditNuevo(t, base, "nulos", now)
	commitCrudo(t, root, "", []byte("feat: algo\n\n\x00Co-Authored-By: Claude\n"))
	r = run(t, root, "", "audit")
	must(t, r, 1, "mensaje con bytes nulos")
	if !strings.Contains(r.stdout, "| commits del periodo sin atribución a IA (R15) | falla |") || !strings.Contains(r.stdout, "bytes nulos") {
		t.Errorf("mensaje con bytes nulos:\n%s", r.stdout)
	}
}

// commitCrudo escribe un commit con el árbol de HEAD, a HEAD como padre y los
// encabezados extra y el mensaje tal cual, y mueve main a él.
func commitCrudo(t *testing.T, root, extra string, msg []byte) string {
	t.Helper()
	tree := strings.TrimSpace(git(t, root, "rev-parse", "HEAD^{tree}"))
	parent := strings.TrimSpace(git(t, root, "rev-parse", "HEAD"))
	stamp := strconv.FormatInt(time.Now().Unix(), 10) + " +0000"
	obj := append([]byte("tree "+tree+"\nparent "+parent+"\nauthor Ana <ana@example.com> "+stamp+"\ncommitter Ana <ana@example.com> "+stamp+"\n"+extra+"\n"), msg...)
	cmd := exec.Command("git", "-C", root, "hash-object", "-t", "commit", "-w", "--literally", "--stdin")
	cmd.Stdin = bytes.NewReader(obj)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("hash-object: %v", err)
	}
	sha := strings.TrimSpace(string(out))
	git(t, root, "update-ref", "refs/heads/main", sha)
	return sha
}

// ebcdic037 codifica en IBM037 (EBCDIC) el texto ASCII que usan las pruebas.
func ebcdic037(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		var b byte
		switch {
		case r >= 'a' && r <= 'i':
			b = 0x81 + byte(r-'a')
		case r >= 'j' && r <= 'r':
			b = 0x91 + byte(r-'j')
		case r >= 's' && r <= 'z':
			b = 0xA2 + byte(r-'s')
		case r >= 'A' && r <= 'I':
			b = 0xC1 + byte(r-'A')
		case r >= 'J' && r <= 'R':
			b = 0xD1 + byte(r-'J')
		case r >= 'S' && r <= 'Z':
			b = 0xE2 + byte(r-'S')
		case r >= '0' && r <= '9':
			b = 0xF0 + byte(r-'0')
		default:
			b = map[rune]byte{' ': 0x40, '\n': 0x25, '-': 0x60, ':': 0x7A, '<': 0x4C, '>': 0x6E, '@': 0x7C, '.': 0x4B, '/': 0x61, '_': 0x6D}[r]
		}
		out = append(out, b)
	}
	return out
}
