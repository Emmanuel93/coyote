package gate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// carpetasProtegidas crea las carpetas que contienen lo protegido en la raíz de
// un proyecto de prueba, y un proyecto anidado con su gobierno.
func carpetasProtegidas(t *testing.T, root string) {
	t.Helper()
	mkTree(t, root, map[string]string{
		"coyote/project.yaml":         "x\n",
		"coyote/ledger/2026/10/x.ccf": "x\n",
		"coyote/approvals/.gitkeep":   "",
		".claude/settings.json":       "{}\n",
		".coyote/estado":              "x\n",
		".git/HEAD":                   "ref: refs/heads/main\n",
		"svc/coyote/project.yaml":     "x\n",
		"svc/coyote/ledger/x.ccf":     "x\n",
		"otro/README.md":              "x\n",
	})
}

func evalBash(t *testing.T, ps Paths, cwd, cmd string) Decision {
	t.Helper()
	return ps.Evaluate(claude(t, "Bash", map[string]any{"command": cmd}, cwd))
}

// TestCarpetasProtegidasPorRutaAbsoluta: séptima revisión de v1.0. Mover o
// borrar coyote/, .claude/, .git/ o .coyote/ con la ruta absoluta pedía la
// aprobación normal, y con la relativa se bloquea siempre: la ruta que un
// agente escribe no puede cambiar la decisión. Un nombre en otras mayúsculas
// es la misma carpeta en el disco de macOS.
func TestCarpetasProtegidasPorRutaAbsoluta(t *testing.T) {
	ps, root, _ := testPaths(t)
	carpetasProtegidas(t, root)
	abs := func(rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }
	for _, dir := range []string{"coyote", ".claude", ".git", ".coyote"} {
		a := abs(dir)
		for _, c := range []string{
			"rm -rf " + a, "rm -rf " + a + "/", "mv " + a + " /tmp/c", "cp -r /tmp/y " + a, "ln -sfn /tmp/y " + a,
			"tar -xf /tmp/e.tar -C " + a, "unzip /tmp/e.zip -d " + a, "find " + a + " -delete",
			"rsync -a --delete /tmp/y/ " + a + "/", "git mv " + a + " /tmp/c", "chmod -R a+w " + a,
			"tar --directory=" + a + " -xf /tmp/e.tar", "cd " + a + " && tar -xf /tmp/e.tar",
			"rm -rf " + filepath.Join(root, "otro", "..", dir), "rm -rf " + abs("./"+dir),
		} {
			if d := evalBash(t, ps, root, c); d.Verdict != Block {
				t.Errorf("%q debería bloquearse siempre: %s (%s)", c, d.Verdict, d.Reason)
			}
		}
	}
	// Desde otra carpeta: la ruta absoluta vale lo mismo que la relativa.
	for _, c := range []string{"rm -rf " + abs("coyote"), "mv " + abs(".git") + " /tmp/c"} {
		if d := evalBash(t, ps, abs("otro"), c); d.Verdict != Block {
			t.Errorf("%q desde otra carpeta debería bloquearse siempre: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
	// El nombre en otras mayúsculas, con rutas relativas y absolutas.
	for _, c := range []string{"rm -rf COYOTE", "rm -rf Coyote/", "mv .GIT x", "mv .Claude x", "rm -rf .CLAUDE", "tar -xf /tmp/e.tar -C .Coyote",
		"rm -rf " + abs("COYOTE"), "mv " + abs(".GIT") + " /tmp/c"} {
		if d := evalBash(t, ps, root, c); d.Verdict != Block {
			t.Errorf("%q debería bloquearse siempre: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
	// Lo que no es una de esas carpetas, o queda fuera del proyecto, no se bloquea.
	hermano := filepath.Join(filepath.Dir(root), "hermano")
	for _, c := range []string{
		"rm -rf " + abs("otro"), "cp -r " + abs("coyote") + " /tmp/respaldo",
		"ls " + abs("coyote"), "rm -rf " + filepath.Join(hermano, "coyote"), "rm -rf /tmp/algo/.git", "mv /tmp/coyote /tmp/c",
		"tar -xf /tmp/e.tar -C " + abs("otro"), "tar -czf /tmp/r.tgz " + abs(".claude"),
	} {
		if d := evalBash(t, ps, root, c); d.Verdict == Block {
			t.Errorf("%q no toca lo protegido de este proyecto: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
}

// TestCopiaALaCarpetaPorTargetDirectory: séptima revisión de v1.0. mv -t
// coyote se bloquea siempre, pero cp -t coyote, install -t y ln -t, que
// también ponen archivos dentro, pedían solo la aprobación normal: el destino
// va en la opción y no es el último argumento.
func TestCopiaALaCarpetaPorTargetDirectory(t *testing.T) {
	ps, root, _ := testPaths(t)
	carpetasProtegidas(t, root)
	abs := func(rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }
	for _, c := range []string{
		"cp -t coyote /tmp/project.yaml", "cp --target-directory=coyote /tmp/project.yaml", "cp --target-directory coyote /tmp/project.yaml",
		"install -t coyote /tmp/x", "install -Dm644 -t coyote/ /tmp/x", "ln -sft coyote /tmp/x", "ln -s -t coyote /tmp/x", "ln --target-directory=coyote -s /tmp/x",
		"cp -rt coyote /tmp/x", "cp -at coyote/ /tmp/x", "cp -tcoyote /tmp/x", "cp -rtcoyote /tmp/x", "cd coyote && cp -t . /tmp/x",
		"cp -t svc/coyote /tmp/x", "cp -t .claude /tmp/s.json", "cp -t " + abs("coyote") + " /tmp/x", "cp -t .git /tmp/x", "cp -t .coyote /tmp/x",
		"mv --target-directory=coyote /tmp/x", "mv -t coyote /tmp/x", "cp -v -t coyote -- /tmp/x", "cp /tmp/x -t coyote", "cp -t ./coyote/ /tmp/x",
		"cp -t svc/../coyote /tmp/x", "install -m 0644 -o root -t coyote /tmp/x",
		// El destino pegado a la opción, contra la carpeta donde corre el comando.
		"cd coyote && cp -t. /tmp/x", "cd coyote && cp --target-directory=. /tmp/x", "env -C coyote cp --target-directory=. /tmp/x",
		"cd svc && cp --target-directory=../coyote /tmp/x", "cd svc && cp -t../coyote /tmp/x",
	} {
		if d := evalBash(t, ps, root, c); d.Verdict != Block {
			t.Errorf("%q debería bloquearse siempre: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
	// Copiar desde ahí, o a una carpeta cualquiera, no. En rsync -t es --times,
	// no una carpeta de destino.
	for _, c := range []string{
		"cp -t /tmp/out otro/README.md", "cp -t otro /tmp/x", "cp -r coyote /tmp/respaldo", "cp --target-directory=otro /tmp/x",
		"rsync -t coyote /tmp/x", "rsync -at coyote/ /tmp/x/", "cp -T /tmp/x otro", "install -t otro /tmp/x", "ln -sft otro /tmp/x",
		"cp -t svc/docs /tmp/x", "cp -t /tmp/coyote /tmp/x", "cp -- -tcoyote /tmp/x", "cp -v -- -t coyote2 /tmp/x",
	} {
		if d := evalBash(t, ps, root, c); d.Verdict == Block {
			t.Errorf("%q no copia a lo protegido: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
}

// TestCarpetasConNombreDeGobiernoSinProyecto: séptima revisión de v1.0. El
// gobierno de un proyecto anidado se bloqueaba por el nombre de la carpeta, sin
// mirar si hay un proyecto: pkg/coyote/ledger/l.go o src/Coyote/Approvals/x.cs,
// código de cualquiera, no se podían escribir ni con aprobación. Donde hay un
// proyecto (su coyote/project.yaml), sigue igual; y el project.yaml mismo se
// protege siempre, para que un agente no cree un proyecto con sus dispensas.
func TestCarpetasConNombreDeGobiernoSinProyecto(t *testing.T) {
	ps, root, _ := testPaths(t)
	carpetasProtegidas(t, root)
	mkTree(t, root, map[string]string{
		"pkg/coyote/ledger/l.go":    "package ledger\n",
		"src/Coyote/Ledger/x.cs":    "class X {}\n",
		"pkg/coyote/approvals/a.go": "package approvals\n",
		"cmd/coyote/main.go":        "package main\n",
	})
	abs := func(rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }
	w := func(tool, rel string) Action {
		in := map[string]any{"file_path": abs(rel), "content": "x"}
		if tool == "Edit" {
			in = map[string]any{"file_path": abs(rel), "old_string": "a", "new_string": "b"}
		}
		return claude(t, tool, in, root)
	}
	for _, a := range []Action{
		w("Write", "pkg/coyote/ledger/l.go"), w("Edit", "pkg/coyote/ledger/l.go"), w("Write", "src/Coyote/Ledger/x.cs"),
		w("Write", "pkg/coyote/approvals/a.go"), w("Write", "pkg/coyote/ledger/nuevo.go"), w("Write", "internal/coyote/hub.yaml"),
		w("Write", "docs/coyote/infra.yaml"), w("Write", "lib/Coyote/Approvals/x.rb"),
	} {
		if d := ps.Evaluate(a); d.Verdict == Block {
			t.Errorf("%s %v es código común, sin un proyecto ahí: %s (%s)", a.Tool, a.Input, d.Verdict, d.Reason)
		}
	}
	for _, a := range []Action{
		w("Write", "svc/coyote/project.yaml"), w("Write", "nuevo/coyote/project.yaml"), w("Write", "nuevo/Coyote/Project.YAML"),
		w("Write", "svc/coyote/ledger/2026/10/x.ccf"), w("Write", "svc/coyote/approvals/P-1.json"), w("Edit", "svc/coyote/Ledger/x.ccf"),
	} {
		if d := ps.Evaluate(a); d.Verdict != Block {
			t.Errorf("%s %v es el gobierno de un proyecto: %s (%s)", a.Tool, a.Input, d.Verdict, d.Reason)
		}
	}
	// La carpeta coyote/ de un proyecto no se mueve; la de un paquete de código, sí (con aprobación).
	for _, c := range []string{"rm -rf svc/coyote", "mv svc/coyote svc/c", "rm -rf " + abs("svc/coyote")} {
		if d := evalBash(t, ps, root, c); d.Verdict != Block {
			t.Errorf("%q debería bloquearse siempre: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
	for _, c := range []string{"rm -rf pkg/coyote", "mv pkg/coyote pkg/c", "rm -rf src/Coyote", "git mv pkg/coyote pkg/c",
		"rm -rf " + abs("pkg/coyote"), "cp -t pkg/coyote /tmp/x", "tar -xf /tmp/e.tar -C pkg/coyote"} {
		d := evalBash(t, ps, root, c)
		if d.Verdict == Block {
			t.Errorf("%q mueve un paquete de código: %s (%s)", c, d.Verdict, d.Reason)
		}
		if d.Verdict == Allow {
			t.Errorf("%q tiene efectos y pide aprobación: %s", c, d.Verdict)
		}
	}
}

// TestGobiernoDeLosProyectosDeAfuera: séptima revisión de v1.0. Con el IDE
// abierto en un proyecto anidado, el gobierno del proyecto de afuera (su
// coyote/project.yaml, sus aprobaciones, su .claude/settings.json) quedaba
// fuera de lo protegido, y ese project.yaml decide lo que se bloquea en todo el
// subárbol. Los desde la raíz se bloqueaban.
func TestGobiernoDeLosProyectosDeAfuera(t *testing.T) {
	_, top, home := testPaths(t)
	carpetasProtegidas(t, top)
	svc := filepath.Join(top, "svc")
	mkTree(t, top, map[string]string{"svc/src/main.go": "package main\n", "svc/.claude/settings.json": "{}\n",
		"lib/coyote/project.yaml": "x\n", "lib/coyote/ledger/x.ccf": "x\n", "pkg/coyote/ledger/l.go": "package ledger\n"})
	ps := NewPaths(svc, home, filepath.Join(home, ".config", "coyote"))
	ps.Outer = []string{top}
	abs := func(rel string) string { return filepath.Join(top, filepath.FromSlash(rel)) }
	// El motivo dice dónde queda lo protegido respecto del proyecto.
	d := ps.Evaluate(claude(t, "Edit", map[string]any{"file_path": abs("coyote/project.yaml"), "old_string": "a", "new_string": "b"}, svc))
	if d.Verdict != Block || !strings.Contains(d.Reason, "../coyote/project.yaml") {
		t.Errorf("el motivo debe decir ../coyote/project.yaml: %s (%s)", d.Verdict, d.Reason)
	}
	for _, a := range []Action{
		claude(t, "Edit", map[string]any{"file_path": abs("coyote/project.yaml"), "old_string": "a", "new_string": "b"}, svc),
		claude(t, "Write", map[string]any{"file_path": abs("coyote/approvals/x.json"), "content": "x"}, svc),
		claude(t, "Write", map[string]any{"file_path": abs("coyote/ledger/2026/10/x.ccf"), "content": "x"}, svc),
		claude(t, "Write", map[string]any{"file_path": abs(".claude/settings.json"), "content": "{}"}, svc),
		claude(t, "Write", map[string]any{"file_path": abs(".coyote/estado"), "content": "x"}, svc),
		claude(t, "Write", map[string]any{"file_path": abs(".git/hooks/pre-commit"), "content": "x"}, svc),
		claude(t, "Write", map[string]any{"file_path": abs("coyote/hub.yaml"), "content": "x"}, svc),
		claude(t, "Write", map[string]any{"file_path": abs("otro/.codex/hooks.json"), "content": "{}"}, svc),
		// Y el de otro proyecto del mismo repo, fuera del IDE.
		claude(t, "Write", map[string]any{"file_path": abs("lib/coyote/ledger/y.ccf"), "content": "x"}, svc),
		claude(t, "Write", map[string]any{"file_path": abs("lib/coyote/project.yaml"), "content": "x"}, svc),
		// Lo de adentro, como siempre.
		claude(t, "Write", map[string]any{"file_path": filepath.Join(svc, "coyote", "project.yaml"), "content": "x"}, svc),
		claude(t, "Write", map[string]any{"file_path": filepath.Join(svc, ".claude", "settings.json"), "content": "{}"}, svc),
	} {
		if d := ps.Evaluate(a); d.Verdict != Block {
			t.Errorf("%s %v debe bloquearse siempre: %s (%s)", a.Tool, a.Input, d.Verdict, d.Reason)
		}
	}
	for _, c := range []string{
		"rm -rf ../coyote", "mv ../coyote /tmp/c", "tar -xf /tmp/e.tar -C ../coyote", "cp -r /tmp/y ../coyote", "cp -t ../coyote /tmp/x",
		"rm -rf ../.git", "mv ../.claude /tmp/c", "rm -rf " + abs("coyote"), "rm -rf " + abs(".git"), "cd .. && rm -rf coyote",
		"find ../coyote -delete", "rm -rf ../COYOTE", "git rm -r ../coyote", "unzip /tmp/e.zip -d " + abs("coyote"),
		"rm -rf coyote", "rm -rf .claude", "rm -rf ../lib/coyote", "mv " + abs("lib/coyote") + " /tmp/c",
	} {
		if d := evalBash(t, ps, svc, c); d.Verdict != Block {
			t.Errorf("%q desde el proyecto anidado debería bloquearse siempre: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
	for _, c := range []string{"rm -rf ../otro", "rm -rf src", "rm -rf ../pkg/coyote", "cat ../coyote/project.yaml", "ls ../coyote", "cp -r ../coyote /tmp/respaldo",
		"rm -rf ../../fuera/coyote", "tar -xf /tmp/e.tar -C ../otro"} {
		if d := evalBash(t, ps, svc, c); d.Verdict == Block {
			t.Errorf("%q no toca el gobierno de los proyectos de afuera: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
	for _, a := range []Action{
		claude(t, "Write", map[string]any{"file_path": abs("otro/README.md"), "content": "x"}, svc),
		claude(t, "Write", map[string]any{"file_path": abs("pkg/coyote/ledger/l.go"), "content": "x"}, svc),
		claude(t, "Write", map[string]any{"file_path": filepath.Join(svc, "src", "main.go"), "content": "x"}, svc),
		claude(t, "Read", map[string]any{"file_path": abs("coyote/project.yaml")}, svc),
	} {
		if d := ps.Evaluate(a); d.Verdict == Block {
			t.Errorf("%s %v no toca el gobierno: %s (%s)", a.Tool, a.Input, d.Verdict, d.Reason)
		}
	}
}

// TestCarpetasProtegidasConLaRaizPorUnEnlace: la ruta absoluta de una carpeta
// protegida vale lo mismo escrita con el enlace por el que se abrió el IDE
// (/var en macOS) que con la ruta real (/private/var).
func TestCarpetasProtegidasConLaRaizPorUnEnlace(t *testing.T) {
	_, _, home := testPaths(t)
	real := filepath.Join(home, "real", "proyecto")
	carpetasProtegidas(t, real)
	link := filepath.Join(home, "enlace")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("sin enlaces simbólicos")
	}
	ps := NewPaths(link, home, filepath.Join(home, ".config", "coyote"))
	for _, c := range []string{
		"rm -rf " + filepath.Join(real, "coyote"), "rm -rf " + filepath.Join(link, "coyote"), "mv " + filepath.Join(real, ".git") + " /tmp/c",
		"rm -rf " + filepath.Join(real, "svc", "coyote"), "rm -rf " + filepath.Join(link, "svc", "coyote"), "cp -t " + filepath.Join(real, ".claude") + " /tmp/x",
		"cd " + filepath.Join(real, "coyote") + " && tar -xf /tmp/e.tar",
	} {
		if d := evalBash(t, ps, link, c); d.Verdict != Block {
			t.Errorf("%q debería bloquearse siempre: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
}
