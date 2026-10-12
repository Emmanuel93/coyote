package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGateAbiertoEnUnAnidadoProtegeAlDeAfuera: séptima revisión de v1.0. Con el
// IDE abierto en un proyecto anidado, el gobierno del proyecto de afuera (su
// coyote/project.yaml, que decide lo que se bloquea en todo el subárbol, sus
// aprobaciones, su .claude/settings.json) no se protegía: el Edit y el Write
// pedían la aprobación normal y rm -rf ../coyote también, y desde la raíz se
// bloquean siempre.
func TestGateAbiertoEnUnAnidadoProtegeAlDeAfuera(t *testing.T) {
	_, root := gateProject(t)
	must(t, run(t, root, "", "init", "svc", "--no-git", "--type", "backend", "--purpose", "servicio de pagos de la tienda"), 0, "init anidado")
	write(t, root, "svc/README.md", "# servicio\n")
	write(t, root, "otro/README.md", "# otro\n")
	write(t, root, ".claude/settings.json", "{}\n")
	svc := filepath.Join(root, "svc")
	check := func(tool string, input map[string]any) result {
		t.Helper()
		return run(t, root, hook(t, svc, tool, input, nil), "gate", "check")
	}
	siempre := map[string]result{
		"Edit del project.yaml de afuera":     check("Edit", map[string]any{"file_path": filepath.Join(root, "coyote", "project.yaml"), "old_string": "a", "new_string": "b"}),
		"Write de las aprobaciones de afuera": check("Write", map[string]any{"file_path": filepath.Join(root, "coyote", "approvals", "P-1.json"), "content": "x"}),
		"Write del settings de afuera":        check("Write", map[string]any{"file_path": filepath.Join(root, ".claude", "settings.json"), "content": "{}"}),
		"rm -rf ../coyote":                    check("Bash", map[string]any{"command": "rm -rf ../coyote", "description": "x"}),
		"mv ../coyote":                        check("Bash", map[string]any{"command": "mv ../coyote /tmp/c", "description": "x"}),
		"tar -x -C ../coyote":                 check("Bash", map[string]any{"command": "tar -xf /tmp/e.tar -C ../coyote", "description": "x"}),
		"rm -rf ../.git":                      check("Bash", map[string]any{"command": "rm -rf ../.git", "description": "x"}),
	}
	for name, r := range siempre {
		if r.code != 2 || !strings.Contains(r.stderr, "bloqueado siempre") {
			t.Errorf("%s debe bloquearse siempre (código %d):\n%s", name, r.code, r.stderr)
		}
	}
	// Lo demás del repo pide la aprobación normal, y leer es libre.
	if r := check("Write", map[string]any{"file_path": filepath.Join(root, "otro", "README.md"), "content": "x"}); strings.Contains(r.stderr, "bloqueado siempre") {
		t.Errorf("un archivo común del repo no es gobierno:\n%s", r.stderr)
	}
	if r := check("Read", map[string]any{"file_path": filepath.Join(root, "coyote", "project.yaml")}); r.code != 0 {
		t.Errorf("leer el project.yaml de afuera es libre (código %d):\n%s", r.code, r.stderr)
	}
}

func linkedWorktree(t *testing.T, repo, rel, branch string) string {
	t.Helper()
	git(t, repo, "worktree", "add", "-q", "-b", branch, rel)
	dir := filepath.Join(repo, filepath.FromSlash(rel))
	if _, err := os.Lstat(filepath.Join(dir, ".git")); err != nil {
		t.Fatalf("el worktree no tiene .git: %v", err)
	}
	return dir
}

// TestGateEnUnWorktreeEnlazadoVeLosSecretosDelRepo: séptima revisión de v1.0.
// El tope de un worktree enlazado (Claude Code los pone en .claude/worktrees/)
// es su propio .git, y los archivos de secretos que el proyecto declara y que
// solo existen en el checkout principal (los ignorados por git, justo los
// secretos) no se protegían al leerlos desde el worktree por su ruta absoluta
// o con ../.. : la regla vale para todos los checkouts del repo.
func TestGateEnUnWorktreeEnlazadoVeLosSecretosDelRepo(t *testing.T) {
	_, root := gateProject(t)
	cfg := readFile(t, filepath.Join(root, "coyote", "project.yaml"))
	write(t, root, "coyote/project.yaml", cfg+"secrets:\n  files: [\"docs/privado.txt\", \"svc/claves/*.txt\"]\n")
	write(t, root, ".gitignore", ".coyote/\ndocs/privado.txt\nsvc/claves/\n.claude/worktrees/\n")
	write(t, root, "README.md", "# tienda\n")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "chore: inicio", "--no-verify")
	// Los secretos solo existen en el checkout principal: git no los sigue.
	write(t, root, "docs/privado.txt", "contraseña\n")
	write(t, root, "svc/claves/a.txt", "uno\n")
	w2 := linkedWorktree(t, root, ".claude/worktrees/w2", "w2")
	w3 := linkedWorktree(t, root, ".claude/worktrees/w3", "w3")
	write(t, w3, "docs/privado.txt", "otra\n")
	// Un checkout hermano, fuera del árbol del principal.
	afuera := filepath.Join(filepath.Dir(root), "tienda-w4")
	git(t, root, "worktree", "add", "-q", "-b", "w4", afuera)
	write(t, afuera, "docs/privado.txt", "cuarta\n")

	worktreeChecks(t, root, w2, w3, afuera)

	// Con worktree.useRelativePaths, git escribe el gitdir de cada worktree
	// relativo a su carpeta: los mismos checkouts.
	entries, err := os.ReadDir(filepath.Join(root, ".git", "worktrees"))
	if err != nil || len(entries) != 3 {
		t.Fatalf("worktrees: %v %v", entries, err)
	}
	for _, e := range entries {
		f := filepath.Join(root, ".git", "worktrees", e.Name(), "gitdir")
		abs := strings.TrimSpace(readFile(t, f))
		rel, err := filepath.Rel(filepath.Dir(f), abs)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte(rel+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	worktreeChecks(t, root, w2, w3, afuera)
}

func worktreeChecks(t *testing.T, root, w2, w3, afuera string) {
	t.Helper()
	check := func(cwd, tool string, input map[string]any) result {
		t.Helper()
		return run(t, root, hook(t, cwd, tool, input, nil), "gate", "check")
	}
	for name, r := range map[string]result{
		"Read del principal, por su ruta absoluta":      check(w2, "Read", map[string]any{"file_path": filepath.Join(root, "docs", "privado.txt")}),
		"cat con ../ hasta el principal":                check(w2, "Bash", map[string]any{"command": "cat ../../../docs/privado.txt", "description": "x"}),
		"Read de un comodín declarado en el principal":  check(w2, "Read", map[string]any{"file_path": filepath.Join(root, "svc", "claves", "a.txt")}),
		"Read del hermano dentro del árbol":             check(w2, "Read", map[string]any{"file_path": filepath.Join(w3, "docs", "privado.txt")}),
		"Read del hermano fuera del árbol":              check(w2, "Read", map[string]any{"file_path": filepath.Join(afuera, "docs", "privado.txt")}),
		"Read de un worktree desde el principal":        check(root, "Read", map[string]any{"file_path": filepath.Join(w3, "docs", "privado.txt")}),
		"Read de adentro, como siempre":                 check(w3, "Read", map[string]any{"file_path": filepath.Join(w3, "docs", "privado.txt")}),
		"Read del principal desde el hermano de afuera": check(afuera, "Read", map[string]any{"file_path": filepath.Join(root, "docs", "privado.txt")}),
	} {
		if r.code != 2 || !strings.Contains(r.stderr, "bloqueado siempre") {
			t.Errorf("%s debe bloquearse siempre (código %d):\n%s", name, r.code, r.stderr)
		}
	}
	for name, r := range map[string]result{
		"el README del principal, desde el worktree": check(w2, "Read", map[string]any{"file_path": filepath.Join(root, "README.md")}),
		"cat del README con ../":                     check(w2, "Bash", map[string]any{"command": "cat ../../../README.md", "description": "x"}),
		"el README del worktree":                     check(w2, "Read", map[string]any{"file_path": filepath.Join(w2, "README.md")}),
		"el README del hermano":                      check(w2, "Read", map[string]any{"file_path": filepath.Join(afuera, "README.md")}),
	} {
		if r.code != 0 {
			t.Errorf("%s debe pasar (código %d):\n%s", name, r.code, r.stderr)
		}
	}
	// El gobierno de los otros checkouts tampoco lo escribe un agente de este.
	for name, r := range map[string]result{
		"Write del settings del principal":   check(w2, "Write", map[string]any{"file_path": filepath.Join(root, ".claude", "settings.json"), "content": "{}"}),
		"Write del project.yaml del hermano": check(w2, "Write", map[string]any{"file_path": filepath.Join(afuera, "coyote", "project.yaml"), "content": "x"}),
		"rm -rf del coyote/ del principal":   check(w2, "Bash", map[string]any{"command": "rm -rf " + filepath.Join(root, "coyote"), "description": "x"}),
	} {
		if r.code != 2 || !strings.Contains(r.stderr, "bloqueado siempre") {
			t.Errorf("%s debe bloquearse siempre (código %d):\n%s", name, r.code, r.stderr)
		}
	}
}

// TestRepoCheckoutsAcotaLosWorktrees: un repo con miles de worktrees no vuelve
// lento cada llamada al gate; los checkouts que no existen se descartan.
func TestRepoCheckoutsAcotaLosWorktrees(t *testing.T) {
	top := t.TempDir()
	if err := os.MkdirAll(filepath.Join(top, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxCheckouts+20; i++ {
		name := "w" + strings.Repeat("x", i%5) + string(rune('a'+i%26)) + string(rune('a'+i/26))
		wt := filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(wt, 0o755); err != nil {
			t.Fatal(err)
		}
		write(t, top, ".git/worktrees/n"+name+"/gitdir", filepath.Join(wt, ".git")+"\n")
	}
	write(t, top, ".git/worktrees/fantasma/gitdir", filepath.Join(top, "no-existe", ".git")+"\n")
	got := repoCheckouts(top)
	if len(got) != maxCheckouts {
		t.Errorf("se esperaban %d checkouts, hay %d", maxCheckouts, len(got))
	}
	for _, g := range got {
		if g == top || strings.Contains(g, "no-existe") {
			t.Errorf("%s no es otro checkout", g)
		}
	}
}

// Un gitdir vacío no apunta a ningún checkout: no se lee como la carpeta de los worktrees.
func TestRepoCheckoutsIgnoraUnGitdirVacio(t *testing.T) {
	top := t.TempDir()
	write(t, top, ".git/worktrees/vacio/gitdir", "\n")
	if got := repoCheckouts(top); len(got) != 0 {
		t.Errorf("no hay otros checkouts: %v", got)
	}
}

func TestOuterDirs(t *testing.T) {
	for _, c := range []struct {
		phys, top string
		want      []string
	}{
		{"/a/b/c", "/a", []string{"/a/b", "/a"}},
		{"/a/b/c", "/a/b", []string{"/a/b"}},
		{"/a/b", "/a/b", nil},
		{"/a/b", "/a/b/c", nil},
		{"/a", "/", []string{"/"}},
		{"/", "/", nil},
	} {
		got := outerDirs(c.phys, c.top)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("outerDirs(%q, %q) = %v, quiero %v", c.phys, c.top, got, c.want)
		}
	}
}

// TestGateRutaAbsolutaDeUnHermanoConReglasIlegibles: séptima revisión de v1.0.
// Un archivo del mismo repo, fuera del proyecto del IDE, que un proyecto
// hermano con el project.yaml roto contiene, no se puede juzgar: el gate
// bloquea aunque la ruta sea absoluta, y lo dice. Antes ninguna prueba cubría
// esa rama: ignorar el error no hacía fallar nada.
func TestGateRutaAbsolutaDeUnHermanoConReglasIlegibles(t *testing.T) {
	_, root := gateProject(t)
	must(t, run(t, root, "", "init", "svc", "--no-git", "--type", "backend", "--purpose", "servicio de pagos de la tienda"), 0, "init anidado")
	must(t, run(t, root, "", "init", "malo", "--no-git", "--type", "backend", "--purpose", "servicio hermano de la tienda"), 0, "init hermano")
	write(t, root, "malo/coyote/project.yaml", "name: [\n  roto\n")
	write(t, root, "malo/datos.txt", "x\n")
	write(t, root, "svc/datos.txt", "x\n")
	svc := filepath.Join(root, "svc")
	for name, r := range map[string]result{
		"Read por la ruta absoluta": run(t, root, hook(t, svc, "Read", map[string]any{"file_path": filepath.Join(root, "malo", "datos.txt")}, nil), "gate", "check"),
		"cat con ../":               run(t, root, hook(t, svc, "Bash", map[string]any{"command": "cat ../malo/datos.txt", "description": "x"}, nil), "gate", "check"),
	} {
		if r.code != 2 || !strings.Contains(r.stderr, "no se pueden leer") || !strings.Contains(r.stderr, "malo/coyote/project.yaml") {
			t.Errorf("%s (código %d):\n%s", name, r.code, r.stderr)
		}
	}
	// Lo del propio proyecto, con reglas legibles, sigue pasando.
	if r := run(t, root, hook(t, svc, "Read", map[string]any{"file_path": filepath.Join(svc, "datos.txt")}, nil), "gate", "check"); r.code != 0 {
		t.Errorf("leer lo común del propio proyecto (código %d):\n%s", r.code, r.stderr)
	}
}
