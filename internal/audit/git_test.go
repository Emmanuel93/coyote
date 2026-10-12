package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Emmanuel93/coyote/internal/attribution"
)

// gitRepo corre git en dir con identidad y fecha fijas y sin firmar.
func gitRepo(t *testing.T, dir string, date time.Time, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "commit.gpgsign=false", "-c", "user.name=Ana", "-c", "user.email=ana@example.com"}, args...)...)
	stamp := date.Format(time.RFC3339)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// priorRepo arma un repo con merges viejos y uno malo, que quita una línea del
// ledger frente a su primer padre, y devuelve lo que hace falta para leer su
// historia en octubre de 2026. config son pares clave=valor de la configuración
// del repo.
func priorRepo(t *testing.T, merges int, config ...string) (in Input, calls *int) {
	t.Helper()
	dir := t.TempDir()
	old := time.Date(2020, 1, 1, 12, 0, 0, 0, time.UTC)
	gitRepo(t, dir, old, "init", "-q", "-b", "main")
	for _, kv := range config {
		k, v, _ := strings.Cut(kv, "=")
		gitRepo(t, dir, old, "config", k, v)
	}
	ledger := filepath.Join(dir, "coyote", "ledger", "2020", "01")
	if err := os.MkdirAll(ledger, 0o755); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(ledger, "01-ana.ccf")
	if err := os.WriteFile(main, []byte("linea 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRepo(t, dir, old, "add", "-A")
	gitRepo(t, dir, old, "commit", "-qm", "base")
	for i := 1; i <= merges; i++ {
		day := old.AddDate(0, 0, i)
		gitRepo(t, dir, day, "checkout", "-q", "-b", fmt.Sprintf("lado%d", i))
		if err := os.WriteFile(filepath.Join(ledger, fmt.Sprintf("%02d-ben.ccf", i+1)), []byte("lado\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitRepo(t, dir, day, "add", "-A")
		gitRepo(t, dir, day, "commit", "-qm", "lado")
		gitRepo(t, dir, day, "checkout", "-q", "main")
		body, _ := os.ReadFile(main)
		if err := os.WriteFile(main, append(body, []byte(fmt.Sprintf("linea %d\n", i))...), 0o644); err != nil {
			t.Fatal(err)
		}
		gitRepo(t, dir, day, "commit", "-qam", "main")
		gitRepo(t, dir, day, "merge", "-q", "--no-ff", "-m", "merge", fmt.Sprintf("lado%d", i))
	}
	// Un merge malo: quita la última línea de main frente a su primer padre.
	gitRepo(t, dir, old.AddDate(0, 0, 400), "checkout", "-q", "-b", "malo")
	gitRepo(t, dir, old.AddDate(0, 0, 400), "commit", "-q", "--allow-empty", "-m", "malo")
	gitRepo(t, dir, old.AddDate(0, 0, 401), "checkout", "-q", "main")
	gitRepo(t, dir, old.AddDate(0, 0, 401), "merge", "-q", "--no-ff", "--no-commit", "malo")
	body, _ := os.ReadFile(main)
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	if err := os.WriteFile(main, []byte(strings.Join(lines[:len(lines)-1], "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRepo(t, dir, old.AddDate(0, 0, 402), "commit", "-qam", "merge malo")
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	gitRepo(t, dir, now.AddDate(0, 0, -6), "commit", "-q", "--allow-empty", "-m", "en el periodo")
	n := new(int)
	return Input{Root: dir, From: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), Now: now,
		Git: func(args ...string) (string, error) {
			*n++
			out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
			return string(out), err
		}}, n
}

// periodRepo arma un repo con merges dentro de octubre de 2026 y, al final, un
// commit firmado, uno en otra codificación y uno con atribución a una herramienta
// de IA (escritos con hash-object). Devuelve lo que hace falta para auditar octubre.
func periodRepo(t *testing.T, merges int) (in Input, calls *int) {
	t.Helper()
	dir := t.TempDir()
	t0 := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	gitRepo(t, dir, t0, "init", "-q", "-b", "main")
	ledger := filepath.Join(dir, "coyote", "ledger", "2026", "10")
	if err := os.MkdirAll(ledger, 0o755); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(ledger, "01-ana.ccf")
	if err := os.WriteFile(main, []byte("linea 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRepo(t, dir, t0, "add", "-A")
	gitRepo(t, dir, t0, "commit", "-qm", "base")
	for i := 1; i <= merges; i++ {
		day := t0.Add(time.Duration(i) * time.Hour)
		gitRepo(t, dir, day, "checkout", "-q", "-b", fmt.Sprintf("lado%d", i))
		if err := os.WriteFile(filepath.Join(ledger, fmt.Sprintf("%02d-ben.ccf", i+1)), []byte("lado\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitRepo(t, dir, day, "add", "-A")
		gitRepo(t, dir, day, "commit", "-qm", "lado")
		gitRepo(t, dir, day, "checkout", "-q", "main")
		body, _ := os.ReadFile(main)
		if err := os.WriteFile(main, append(body, []byte(fmt.Sprintf("linea %d\n", i))...), 0o644); err != nil {
			t.Fatal(err)
		}
		gitRepo(t, dir, day, "commit", "-qam", "main")
		gitRepo(t, dir, day, "merge", "-q", "--no-ff", "-m", "merge", fmt.Sprintf("lado%d", i))
	}
	crudo := func(when time.Time, extra, msg string) {
		tree := strings.TrimSpace(gitRepo(t, dir, when, "rev-parse", "HEAD^{tree}"))
		parent := strings.TrimSpace(gitRepo(t, dir, when, "rev-parse", "HEAD"))
		stamp := fmt.Sprintf("%d +0000", when.Unix())
		obj := "tree " + tree + "\nparent " + parent + "\nauthor Ana <ana@example.com> " + stamp + "\ncommitter Ana <ana@example.com> " + stamp + "\n" + extra + "\n" + msg
		cmd := exec.Command("git", "-C", dir, "hash-object", "-t", "commit", "-w", "--stdin")
		cmd.Stdin = strings.NewReader(obj)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("hash-object: %v", err)
		}
		gitRepo(t, dir, when, "update-ref", "refs/heads/main", strings.TrimSpace(string(out)))
	}
	end := t0.AddDate(0, 0, 10)
	crudo(end, "gpgsig -----BEGIN PGP SIGNATURE-----\n \n AAAA\n -----END PGP SIGNATURE-----\n", "chore: firmado\n")
	crudo(end.Add(time.Hour), "encoding IBM037\n", "chore: otra codificacion\n")
	crudo(end.Add(2*time.Hour), "", "feat: algo\n\nCo-Authored-By: "+"Cla"+"ude <noreply@"+"anthropic.com>\n")
	n := new(int)
	return Input{Root: dir, From: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		Now: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC),
		Git: func(args ...string) (string, error) {
			*n++
			out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
			return string(out), err
		}}, n
}

// TestPeriodoSinUnProcesoPorCommit: leer lo que llegó en el periodo cuesta lo mismo con
// tres merges que con doce. Un diff-tree por padre y un cat-file por commit hacían que
// un mes con miles de commits tardara medio minuto (sexta revisión, H7): ahora el diff
// del ledger sale de un git log y los commits, de un cat-file --batch. Y el reporte es
// el mismo que con un proceso por commit.
func TestPeriodoSinUnProcesoPorCommit(t *testing.T) {
	attr, err := attribution.Default()
	if err != nil {
		t.Fatal(err)
	}
	run := func(merges int, batch bool) (calls int, rep *Report) {
		in, n := periodRepo(t, merges)
		in.Attribution = attr
		if batch {
			in.GitBatch = func(stdin string, args ...string) (string, error) {
				*n++
				cmd := exec.Command("git", append([]string{"-C", in.Root}, args...)...)
				cmd.Stdin = strings.NewReader(stdin)
				out, err := cmd.Output()
				return string(out), err
			}
		}
		rep = Build(in)
		return *n, rep
	}
	few, repFew := run(3, true)
	many, repMany := run(12, true)
	if few != many {
		t.Errorf("los procesos de git dependen de los commits: %d con 3 merges, %d con 12", few, many)
	}
	for _, rep := range []*Report{repFew, repMany} {
		if c := check(rep, "commits del periodo sin atribución a IA (R15)"); c.Result != Fail {
			t.Errorf("el commit con atribución tiene que fallar R15: %+v", c)
		}
		if rep.Signed != 1 {
			t.Errorf("hay un commit firmado: %d", rep.Signed)
		}
	}
	if repMany.Commits != 1+12*3+3 {
		t.Errorf("commits del periodo: %d, se esperaban %d", repMany.Commits, 1+12*3+3)
	}
	// Sin GitBatch cada commit cuesta su proceso, pero el reporte es el mismo.
	slow, repSlow := run(3, false)
	if slow <= few {
		t.Errorf("sin GitBatch se esperaban más procesos que con él: %d frente a %d", slow, few)
	}
	a, _ := json.Marshal(repFew)
	b, _ := json.Marshal(repSlow)
	if string(a) != string(b) {
		t.Errorf("el reporte cambia según se lea por lotes o commit a commit:\n%s\n%s", a, b)
	}
}

// diffPorPadre es la lectura de antes de la sexta revisión: un git diff-tree por commit
// y por padre ("" es el árbol vacío, para el commit raíz). Queda aquí como referencia:
// el git log único tiene que dar los mismos diffs.
func diffPorPadre(t *testing.T, in Input, c gitCommit, parent string) ledgerDiff {
	t.Helper()
	args := []string{"-c", "core.quotePath=false", "diff-tree", "-p", "--text", "-U0", "--no-ext-diff", "--no-textconv", "--no-renames", "--relative"}
	if parent == "" {
		args = append(args, "--root", c.sha)
	} else {
		args = append(args, parent, c.sha)
	}
	out, err := in.Git(append(args, "--", ledgerDir)...)
	if err != nil {
		t.Fatal(err)
	}
	return parseLedgerDiff(out, c.sha, parent, len(c.parents) > 1)
}

// TestLosDiffsDeUnLogSonLosDeCadaPadre: el diff del ledger de lo que llegó sale de un
// solo git log -m, no de un diff-tree por padre. En una historia con de todo (raíz,
// merge limpio, octopus, un merge -s ours que tira las líneas de una rama, un borrado,
// un renombre, un cambio de modo, un commit que no toca el ledger, un merge sin
// cambios y otro que borra la línea de un solo padre) da, para cada commit y cada
// padre, lo mismo que el diff-tree.
func TestLosDiffsDeUnLogSonLosDeCadaPadre(t *testing.T) {
	dir := t.TempDir()
	// Los primeros comandos caen en septiembre: lo anterior al periodo no entra en los diffs.
	t0 := time.Date(2026, 9, 30, 20, 0, 0, 0, time.UTC)
	n := 0
	at := func() time.Time { n++; return t0.Add(time.Duration(n) * time.Hour) }
	g := func(args ...string) { t.Helper(); gitRepo(t, dir, at(), args...) }
	file := func(rel, body string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const led = "coyote/ledger/2026/10/"
	g("init", "-q", "-b", "main")
	file(led+"01-ana.ccf", "l1\nl2\n")
	file("README.md", "x\n")
	g("add", "-A")
	g("commit", "-qm", "raíz")
	file(led+"01-ana.ccf", "l1\nl2\nl3\n")
	g("commit", "-qam", "agrega l3")
	g("checkout", "-q", "-b", "lado1")
	file(led+"02-ben.ccf", "b1\n")
	file(led+"01-ana.ccf", "l1\nl2\nl3\nl4\n")
	g("add", "-A")
	g("commit", "-qm", "lado1")
	g("checkout", "-q", "main")
	file(led+"03-eva.ccf", "e1\ne2\n")
	g("add", "-A")
	g("commit", "-qm", "eva")
	g("merge", "-q", "--no-ff", "-m", "merge limpio", "lado1")
	for _, b := range []string{"lado2", "lado3", "lado4"} {
		g("checkout", "-q", "-b", b, "main")
		file(led+"0"+b[len(b)-1:]+"-"+b+".ccf", b+"\n")
		file("src/"+b+".txt", b+"\n")
		g("add", "-A")
		g("commit", "-qm", b)
	}
	g("checkout", "-q", "main")
	g("merge", "-q", "--no-ff", "-m", "octopus", "lado2", "lado3")
	g("merge", "-q", "--no-ff", "-s", "ours", "-m", "tira lado4", "lado4")
	g("rm", "-q", led+"03-eva.ccf")
	g("commit", "-qm", "borra el archivo de eva")
	g("mv", led+"02-ben.ccf", led+"02-ben-renombrado.ccf")
	g("commit", "-qm", "renombra")
	g("update-index", "--chmod=+x", led+"01-ana.ccf")
	g("commit", "-qm", "cambia el modo")
	file("src/otro.txt", "otro\n")
	g("add", "-A")
	g("commit", "-qm", "no toca el ledger")
	g("checkout", "-q", "-b", "lado5", "main")
	file("src/lado5.txt", "5\n")
	g("add", "-A")
	g("commit", "-qm", "lado5")
	g("checkout", "-q", "main")
	g("merge", "-q", "--no-ff", "-m", "merge sin cambios en el ledger", "lado5")
	file(led+"04-lado2.ccf", "")
	g("add", "-A")
	g("commit", "-qm", "vacía lado2")
	g("checkout", "-q", "-b", "lado6", "main~1")
	file(led+"06-lado6.ccf", "6\n")
	g("add", "-A")
	g("commit", "-qm", "lado6")
	g("checkout", "-q", "main")
	g("merge", "-q", "--no-ff", "-m", "borra la línea de un solo padre", "lado6")
	file(led+"06-lado6.ccf", "")
	g("commit", "-qam", "vacía lado6")

	git := func(args ...string) (string, error) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
		return string(out), err
	}
	in := Input{Root: dir, Git: git, Now: t0.AddDate(0, 0, 20), From: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}
	h, err := readHistory(in)
	if err != nil {
		t.Fatal(err)
	}
	total, _ := git("rev-list", "--count", "HEAD")
	if len(h.arrived) < 15 || fmt.Sprint(len(h.arrived)) == strings.TrimSpace(total) {
		t.Fatalf("la historia de prueba tiene que tener algo anterior al periodo y mucho dentro de él: %s commits, %d llegaron en octubre", strings.TrimSpace(total), len(h.arrived))
	}
	// Referencia: un diff-tree por commit y por padre, sin los vacíos.
	type clave struct{ commit, parent string }
	want := map[clave]ledgerDiff{}
	for _, c := range h.arrived {
		parents := c.parents
		if len(parents) == 0 {
			parents = []string{""}
		}
		for _, p := range parents {
			if d := diffPorPadre(t, in, c, p); len(d.removed) > 0 || len(d.added) > 0 {
				want[clave{c.sha, p}] = d
			}
		}
	}
	got := map[clave]ledgerDiff{}
	for _, d := range h.diffs {
		if len(d.removed) == 0 && len(d.added) == 0 {
			continue
		}
		if _, dup := got[clave{d.commit, d.parent}]; dup {
			t.Errorf("el log trae dos veces el diff de %s contra %s", short(d.commit), short(d.parent))
		}
		got[clave{d.commit, d.parent}] = d
	}
	if len(want) < 10 {
		t.Fatalf("la referencia tiene que ver cambios en el ledger de muchos commits: %d", len(want))
	}
	for k, w := range want {
		g, ok := got[k]
		if !ok {
			t.Errorf("falta el diff de %s contra %s: %+v", short(k.commit), short(k.parent), w)
			continue
		}
		if g.merge != w.merge || !reflect.DeepEqual(g.removed, w.removed) || !reflect.DeepEqual(g.added, w.added) {
			t.Errorf("el diff de %s contra %s cambia: log %+v, diff-tree %+v", short(k.commit), short(k.parent), g, w)
		}
	}
	for k, g := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("sobra el diff de %s contra %s: %+v", short(k.commit), short(k.parent), g)
		}
	}
	// Las dos formas de merge que más importan: el -s ours tira las líneas de su
	// segundo padre y se ve frente a él.
	var ours bool
	for k, w := range want {
		if w.merge && len(w.removed) > 0 && k.parent != h.byID[k.commit].parents[0] {
			ours = true
		}
	}
	if !ours {
		t.Error("la historia de prueba tiene que traer un merge que quita líneas frente a su segundo padre")
	}
}

// TestParseBatch: la salida de cat-file --batch se acepta solo si es exactamente la
// esperada, objeto por objeto y en orden. Cualquier otra cosa descarta el lote entero
// (y se lee commit a commit): nada de lo que diga un mensaje mueve los límites.
func TestParseBatch(t *testing.T) {
	a, b := strings.Repeat("a", 40), strings.Repeat("b", 40)
	cms := []gitCommit{{sha: a}, {sha: b}}
	un := func(sha, kind string, body string) string {
		return fmt.Sprintf("%s %s %d\n%s\n", sha, kind, len(body), body)
	}
	for _, c := range []struct {
		name string
		out  string
		want []string // nil: se descarta
	}{
		{"dos commits", un(a, "commit", "uno\n") + un(b, "commit", "dos"), []string{"uno\n", "dos"}},
		{"cuerpo vacío", un(a, "commit", "") + un(b, "commit", "x"), []string{"", "x"}},
		{"un cuerpo que imita una cabecera", un(a, "commit", "x\n"+b+" commit 3\nabc\ny") + un(b, "commit", "dos"), []string{"x\n" + b + " commit 3\nabc\ny", "dos"}},
		{"un cuerpo con bytes nulos y CR", un(a, "commit", "x\x00\r\ny") + un(b, "commit", "dos"), []string{"x\x00\r\ny", "dos"}},
		{"salida vacía", "", nil},
		{"falta el segundo", un(a, "commit", "uno"), nil},
		{"otro orden", un(b, "commit", "dos") + un(a, "commit", "uno"), nil},
		{"otro hash", un(a, "commit", "uno") + un(strings.Repeat("c", 40), "commit", "dos"), nil},
		{"un blob", un(a, "commit", "uno") + un(b, "blob", "dos"), nil},
		{"un tag", un(a, "tag", "uno") + un(b, "commit", "dos"), nil},
		{"missing", un(a, "commit", "uno") + b + " missing\n", nil},
		{"tamaño de más", fmt.Sprintf("%s commit 9\nuno\n", a) + un(b, "commit", "dos"), nil},
		{"tamaño de menos", fmt.Sprintf("%s commit 1\nuno\n", a) + un(b, "commit", "dos"), nil},
		{"tamaño negativo", fmt.Sprintf("%s commit -3\nuno\n", a) + un(b, "commit", "dos"), nil},
		{"tamaño -1, que apunta al salto de la cabecera", fmt.Sprintf("%s commit -1\nuno\n", a) + un(b, "commit", "dos"), nil},
		{"tamaño que no es número", fmt.Sprintf("%s commit tres\nuno\n", a) + un(b, "commit", "dos"), nil},
		{"sin el salto final", strings.TrimSuffix(un(a, "commit", "uno")+un(b, "commit", "dos"), "\n"), nil},
		{"bytes de sobra", un(a, "commit", "uno") + un(b, "commit", "dos") + "x", nil},
		{"el último no termina en salto de línea", un(a, "commit", "uno") + b + " commit 3\ndosX", nil},
		{"uno del medio no termina en salto de línea", a + " commit 3\nunoX" + un(b, "commit", "dos"), nil},
		{"un campo de más en la cabecera", a + " commit 3 x\nuno\n" + un(b, "commit", "dos"), nil},
	} {
		got, ok := parseBatch(c.out, cms)
		if (c.want != nil) != ok || (ok && !slices.Equal(got, c.want)) {
			t.Errorf("%s: parseBatch dio %q ok=%v, se esperaba %q", c.name, got, ok, c.want)
		}
	}
	if raws, ok := parseBatch("", nil); !ok || len(raws) != 0 {
		t.Errorf("sin commits y sin salida: %q %v", raws, ok)
	}
}

// TestParseBlobs: lo mismo para los archivos del ledger en HEAD.
func TestParseBlobs(t *testing.T) {
	h, h2 := strings.Repeat("1", 40), strings.Repeat("2", 64)
	paths := []string{"coyote/ledger/a.ccf", "coyote/ledger/b c.ccf"}
	un := func(id string, body string) string { return fmt.Sprintf("%s blob %d\n%s\n", id, len(body), body) }
	falta := func(p string) string { return "HEAD:./" + p + " missing\n" }
	got, ok := parseBlobs(un(h, "uno\n")+un(h2, ""), paths)
	if !ok || len(got) != 2 || got[0].data != "uno\n" || got[0].err != nil || got[1].data != "" || got[1].err != nil {
		t.Errorf("dos archivos: %+v %v", got, ok)
	}
	got, ok = parseBlobs(un(h, "uno\n")+falta(paths[1]), paths)
	if !ok || got[0].err != nil || got[1].err == nil {
		t.Errorf("uno falta en HEAD: %+v %v", got, ok)
	}
	for name, out := range map[string]string{
		"vacía":                 "",
		"falta uno":             un(h, "uno"),
		"missing de otra ruta":  un(h, "uno") + falta("coyote/ledger/otra.ccf"),
		"un commit":             un(h, "uno") + strings.Replace(un(h, "dos"), " blob ", " commit ", 1),
		"hash corto":            un("abc", "uno") + un(h, "dos"),
		"hash no hexadecimal":   un(strings.Repeat("g", 40), "uno") + un(h, "dos"),
		"tamaño que no cuadra":  h + " blob 9\nuno\n" + un(h, "dos"),
		"tamaño -1":             h + " blob -1\nuno\n" + un(h, "dos"),
		"bytes de sobra":        un(h, "uno") + un(h, "dos") + "x",
		"el último termina mal": un(h, "uno") + h + " blob 3\ndosX",
		"ambiguo":               "HEAD:./" + paths[0] + " ambiguous\n" + un(h, "dos"),
	} {
		if _, ok := parseBlobs(out, paths); ok {
			t.Errorf("%s: se aceptó una salida que no es la esperada: %q", name, out)
		}
	}
}

// TestLecturaPorLotesIgualQueCommitACommit: rawCommits y headBlobs dan los mismos bytes
// con cat-file --batch que con un proceso por objeto, también con mensajes que imitan
// el formato del lote, bytes nulos, rutas con espacios, comillas o saltos de línea, y
// archivos que no están en HEAD. Si el lote falla o devuelve basura, se lee uno a uno.
func TestLecturaPorLotesIgualQueCommitACommit(t *testing.T) {
	in, _ := periodRepo(t, 2)
	batch := func(stdin string, args ...string) (string, error) {
		cmd := exec.Command("git", append([]string{"-C", in.Root}, args...)...)
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.Output()
		return string(out), err
	}
	// Mensajes hostiles, escritos con hash-object.
	tree := strings.TrimSpace(gitRepo(t, in.Root, time.Now(), "rev-parse", "HEAD^{tree}"))
	parent := strings.TrimSpace(gitRepo(t, in.Root, time.Now(), "rev-parse", "HEAD"))
	for i, msg := range []string{
		"x\n" + strings.Repeat("a", 40) + " commit 3\nabc\n",
		"con nulo\x00\nCo-Authored-By: no\n",
		"fin sin salto de línea",
		"\r\n\r\n",
		"",
		strings.Repeat("larga ", 50000),
	} {
		obj := "tree " + tree + "\nparent " + parent + "\nauthor Ana <ana@example.com> 1790000000 +0000\ncommitter Ana <ana@example.com> 1790000000 +0000\n\n" + msg
		cmd := exec.Command("git", "-C", in.Root, "hash-object", "--literally", "-t", "commit", "-w", "--stdin")
		cmd.Stdin = strings.NewReader(obj)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("hash-object %d: %v", i, err)
		}
		parent = strings.TrimSpace(string(out))
	}
	gitRepo(t, in.Root, time.Now(), "update-ref", "refs/heads/main", parent)
	log, err := in.Git("rev-list", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	var cms []gitCommit
	for _, sha := range strings.Fields(log) {
		cms = append(cms, gitCommit{sha: sha})
	}
	slow := Input{Git: in.Git}
	want, err := rawCommits(slow, cms)
	if err != nil {
		t.Fatal(err)
	}
	for name, gb := range map[string]func(string, ...string) (string, error){
		"lote":       batch,
		"lote falla": func(string, ...string) (string, error) { return "", fmt.Errorf("sin lotes") },
		"basura":     func(string, ...string) (string, error) { return "esto no es un lote\n", nil },
		"lote corto": func(stdin string, args ...string) (string, error) {
			out, err := batch(stdin, args...)
			return out[:len(out)/2], err
		},
	} {
		got, err := rawCommits(Input{Git: in.Git, GitBatch: gb}, cms)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("%s: rawCommits cambia (%v)", name, err)
		}
	}
	// Los archivos del ledger de HEAD y rutas raras.
	names, err := in.Git("-c", "core.quotePath=false", "ls-tree", "-r", "-z", "--name-only", "HEAD", "--", ledgerDir)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, p := range strings.Split(names, "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	paths = append(paths, "coyote/ledger/no existe.ccf", "coyote/ledger/\"comillas\".ccf", "coyote/ledger/-guion.ccf", "coyote/ledger/ñandú.ccf")
	enHEAD := len(paths) - 4
	conSalto := append(slices.Clone(paths), "coyote/ledger/con\nsalto.ccf")
	for _, grupo := range []struct {
		name  string
		paths []string
		lotes bool // el lote se usa: una ruta con salto de línea no cabe en su entrada
	}{{"rutas normales", paths, true}, {"con una ruta con salto de línea", conSalto, false}} {
		wantB := headBlobs(Input{Git: in.Git}, grupo.paths)
		for name, gb := range map[string]func(string, ...string) (string, error){
			"lote":       batch,
			"lote falla": func(string, ...string) (string, error) { return "", fmt.Errorf("sin lotes") },
			"basura":     func(string, ...string) (string, error) { return "basura\n", nil },
		} {
			lotes := 0
			cuenta := func(stdin string, args ...string) (string, error) { lotes++; return gb(stdin, args...) }
			gotB := headBlobs(Input{Git: in.Git, GitBatch: cuenta}, grupo.paths)
			for i := range grupo.paths {
				if gotB[i].data != wantB[i].data || (gotB[i].err == nil) != (wantB[i].err == nil) {
					t.Errorf("%s, %s: headBlobs de %q cambia: %+v frente a %+v", grupo.name, name, grupo.paths[i], gotB[i], wantB[i])
				}
			}
			if (lotes > 0) != grupo.lotes {
				t.Errorf("%s, %s: el lote se pidió %d veces; se esperaba que se usara: %v", grupo.name, name, lotes, grupo.lotes)
			}
		}
		for i, p := range grupo.paths {
			if leido := wantB[i].err == nil; leido != (i < enHEAD) {
				t.Errorf("%s, %q: leído=%v, se esperaba %v", grupo.name, p, leido, i < enHEAD)
			}
		}
	}
	// El caso feliz del lote lee de verdad los bytes de HEAD.
	got := headBlobs(Input{Git: in.Git, GitBatch: batch}, paths)
	if got[0].err != nil || !strings.HasPrefix(got[0].data, "linea 0") {
		t.Errorf("el primer archivo del ledger tiene que empezar con la primera línea: %+v", got[0])
	}
}

// TestHistoriaAnteriorSinUnProcesoPorMerge: leer la historia anterior al
// periodo cuesta lo mismo con dos merges que con diez. Un proceso de git por
// merge hacía que auditar un repo grande tardara minutos.
func TestHistoriaAnteriorSinUnProcesoPorMerge(t *testing.T) {
	count := func(merges int) (calls int, prior []ledgerDiff) {
		in, n := priorRepo(t, merges)
		h, err := readHistory(in)
		if err != nil {
			t.Fatal(err)
		}
		return *n, h.prior
	}
	few, priorFew := count(2)
	many, priorMany := count(10)
	if few != many {
		t.Errorf("los procesos de git dependen de los merges: %d con 2, %d con 10", few, many)
	}
	// Solo el merge que quita una línea se avisa, contra cada uno de sus dos padres.
	for _, prior := range [][]ledgerDiff{priorFew, priorMany} {
		if len(prior) != 2 || prior[0].commit != prior[1].commit || !prior[0].merge || prior[0].parent == prior[1].parent {
			t.Errorf("se esperaba el merge que quita una línea frente a sus dos padres: %+v", prior)
		}
	}
}

// TestHistoriaAnteriorConLaConfiguracionDelDiffDeMerges: sexta revisión de v1.0
// (H1). Con git log -m, desde git 2.31, log.diffMerges decide el formato del
// diff de un merge: first-parent borra el diff contra el segundo padre y
// combined/dense-combined lo sacan en un formato que el lector no entiende, y un
// merge que tira las líneas del ledger de una rama pasaba por limpio. Es una
// preferencia legítima de la persona: la auditoría pide siempre un diff por padre.
func TestHistoriaAnteriorConLaConfiguracionDelDiffDeMerges(t *testing.T) {
	for _, mode := range []string{"separate", "first-parent", "combined", "dense-combined", "off", "on"} {
		in, _ := priorRepo(t, 2, "log.diffMerges="+mode)
		h, err := readHistory(in)
		if err != nil {
			t.Fatalf("log.diffMerges=%s: %v", mode, err)
		}
		if len(h.prior) != 2 || h.prior[0].commit != h.prior[1].commit || !h.prior[0].merge || h.prior[0].parent == h.prior[1].parent {
			t.Errorf("log.diffMerges=%s: se esperaba el merge que quita una línea frente a sus dos padres: %+v", mode, h.prior)
		}
	}
}

// TestHistoriaAnteriorConOtraCodificacionDeSalida: i18n.logOutputEncoding cambia la
// codificación con la que git escribe los mensajes en log, y con UTF-16 cada asunto
// trae bytes NUL y saltos de línea que no lo son. La historia anterior se pide en
// UTF-8 siempre (--encoding=UTF-8): la configuración de quien audita no cambia lo que
// se lee ni deja que un asunto parta una línea del diff.
func TestHistoriaAnteriorConOtraCodificacionDeSalida(t *testing.T) {
	for _, enc := range []string{"UTF-16LE", "UTF-16BE", "UTF-16", "ISO-8859-1", "IBM037", "UTF-8"} {
		in, _ := priorRepo(t, 2, "i18n.logOutputEncoding="+enc)
		git, logs := in.Git, 0
		in.Git = func(args ...string) (string, error) {
			for i, a := range args {
				if a == "log" {
					logs++
					// En readPrior la salida solo conserva el hash del encabezado, así que sin la
					// bandera nada cambia a la vista: se comprueba la llamada.
					if !slices.Contains(args[i:], "--encoding=UTF-8") {
						t.Errorf("i18n.logOutputEncoding=%s: git %v no pide --encoding=UTF-8", enc, args)
					}
					break
				}
			}
			return git(args...)
		}
		h, err := readHistory(in)
		if err != nil {
			t.Fatalf("i18n.logOutputEncoding=%s: %v", enc, err)
		}
		if logs < 2 {
			t.Errorf("i18n.logOutputEncoding=%s: se esperaban al menos dos git log (historia y anterior al periodo), hubo %d", enc, logs)
		}
		if len(h.prior) != 2 || h.prior[0].commit != h.prior[1].commit || !h.prior[0].merge || h.prior[0].parent == h.prior[1].parent {
			t.Errorf("i18n.logOutputEncoding=%s: se esperaba el merge que quita una línea frente a sus dos padres: %+v", enc, h.prior)
		}
	}
}

// TestFechaFuturaQuePasaSeCuentaEnDosReportes fija un límite conocido (sexta
// revisión, H2). Una fecha más de una hora adelantada a la de la auditoría es
// imposible: el commit llega con el que le sigue, y el reporte del mes cerrado lo
// cuenta ahí. Pero "imposible" se mide contra la hora de la auditoría: cuando la fecha
// pasa, el commit se cuenta por ella, arrastra a los que le siguen y el reporte del mes
// siguiente lo cuenta otra vez. Volver a correr el mes cerrado tampoco da el reporte
// que se publicó. Sellar el periodo (que el reporte escrito guarde hasta dónde contó)
// lo cerraría; queda para v1.1. Mientras tanto el reporte avisa de la fecha futura y
// de esto (docs/specs/audit-v1.md, "Fechas futuras"). Si el periodo se sella, esta
// prueba cambia: cada commit tiene que contarse en un solo reporte.
func TestFechaFuturaQuePasaSeCuentaEnDosReportes(t *testing.T) {
	at := func(s string) time.Time {
		w, err := time.Parse("2006-01-02T15:04", s)
		if err != nil {
			t.Fatal(err)
		}
		return w
	}
	dir := t.TempDir()
	gitRepo(t, dir, at("2026-09-20T12:00"), "init", "-q", "-b", "main")
	for _, c := range []struct{ msg, when string }{
		{"a", "2026-09-20T12:00"},
		{"x", "2026-10-03T00:00"}, // hecho el 24-09 con el reloj adelantado
		{"m", "2026-09-25T12:00"},
		{"n", "2026-09-28T12:00"},
	} {
		gitRepo(t, dir, at(c.when), "commit", "-q", "--allow-empty", "-m", c.msg)
	}
	git := func(args ...string) (string, error) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
		return string(out), err
	}
	llegados := func(now, from, to string) string {
		t.Helper()
		h, err := readHistory(Input{Root: dir, Git: git, Now: at(now), From: at(from), To: at(to)})
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, c := range h.arrived {
			out, err := git("show", "-s", "--format=%s", c.sha)
			if err != nil {
				t.Fatal(err)
			}
			names = append(names, strings.TrimSpace(out))
		}
		slices.Sort(names)
		return strings.Join(names, "")
	}
	const sep, oct, nov = "2026-09-01T00:00", "2026-10-01T00:00", "2026-11-01T00:00"
	// El 2 de octubre, cuando se escribe el reporte de septiembre, la fecha del 3 es
	// imposible: x llega con m y los cuatro cuentan en septiembre.
	if got := llegados("2026-10-02T12:00", sep, oct); got != "amnx" {
		t.Errorf("septiembre, auditado el 2-10: llegaron %q, se esperaba amnx", got)
	}
	if got := llegados("2026-10-02T12:00", oct, nov); got != "" {
		t.Errorf("octubre, auditado el 2-10: llegaron %q, se esperaba ninguno", got)
	}
	// Pasada esa fecha, x se cuenta por ella y arrastra a m y n: el reporte de octubre
	// los cuenta otra vez y el de septiembre ya no es el que se publicó.
	if got := llegados("2026-10-03T00:30", oct, nov); got != "mnx" {
		t.Errorf("octubre, auditado el 3-10: llegaron %q, se esperaba mnx", got)
	}
	if got := llegados("2026-10-03T00:30", sep, oct); got != "a" {
		t.Errorf("septiembre, vuelto a correr el 3-10: llegaron %q, se esperaba a", got)
	}
}

// TestSplitCommit: una codificación que no es UTF-8, en cualquiera de los
// encabezados encoding (git lee el primero y un objeto puede traer varios), hace
// que el mensaje se lea también como lo convierte git; UTF-8 en cualquiera de
// sus escrituras no cuesta otro proceso.
func TestSplitCommit(t *testing.T) {
	const base = "tree 1\nauthor Ana <ana@example.com> 1 +0000\ncommitter Ana <ana@example.com> 1 +0000\n"
	for _, c := range []struct {
		name, head string
		signed     bool
		enc        string
	}{
		{"sin encoding", "", false, ""},
		{"UTF-8", "encoding UTF-8\n", false, ""},
		{"utf8 en minúsculas", "encoding utf8\n", false, ""},
		{"UTF_8 con guion bajo", "encoding UTF_8\n", false, ""},
		{"dos UTF-8", "encoding UTF-8\nencoding utf-8\n", false, ""},
		{"IBM037", "encoding IBM037\n", false, "IBM037"},
		{"IBM037 y luego UTF-8", "encoding IBM037\nencoding UTF-8\n", false, "IBM037"},
		{"UTF-8 y luego IBM037", "encoding UTF-8\nencoding IBM037\n", false, "IBM037"},
		{"una etiqueta vacía", "encoding \n", false, "(vacía)"},
		{"firmado", "gpgsig -----BEGIN-----\n x\n -----END-----\n", true, ""},
		{"firmado sha256", "gpgsig-sha256 -----BEGIN-----\n x\n -----END-----\nencoding ISO-8859-1\n", true, "ISO-8859-1"},
		// Un encoding dentro de la continuación de una firma no es un encabezado.
		{"encoding dentro de la firma", "gpgsig -----BEGIN-----\n encoding IBM037\n -----END-----\n", true, ""},
	} {
		signed, enc, msg := splitCommit(base + c.head + "\nfeat: algo\n")
		if signed != c.signed || enc != c.enc || msg != "feat: algo\n" {
			t.Errorf("%s: firmado=%v codificación=%q mensaje=%q, se esperaba %v %q", c.name, signed, enc, msg, c.signed, c.enc)
		}
	}
}
