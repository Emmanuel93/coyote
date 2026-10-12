package gate

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Emmanuel93/coyote/internal/secrets"
)

// topesDeGlob baja los topes de trabajo de la expansión de comodines mientras
// dura la prueba.
func topesDeGlob(t *testing.T, paths, dir, reads, names int) {
	t.Helper()
	oP, oD, oR, oN := maxGlobPaths, maxGlobDir, maxGlobReads, maxGlobNames
	maxGlobPaths, maxGlobDir, maxGlobReads, maxGlobNames = paths, dir, reads, names
	t.Cleanup(func() { maxGlobPaths, maxGlobDir, maxGlobReads, maxGlobNames = oP, oD, oR, oN })
}

// TestComodinDeclaraCuandoSeCorta: séptima revisión de v1.0 (fuzzing del gate).
// El shell expande un comodín sin tope y el gate no puede: un patrón que arranca
// en la raíz del disco (`/*/*/x`) tardaba segundos, y un hook que tarda más que
// el tiempo del IDE deja pasar la acción. La expansión tiene topes, y cuando se
// corta lo dice: quien la usa no puede tomar un resultado recortado por completo.
func TestComodinDeclaraCuandoSeCorta(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{}
	for i := 0; i < 10; i++ {
		files[fmt.Sprintf("d%d/f", i)] = "x"
	}
	mkTree(t, dir, files)

	got, complete := globAll(dir + "/*/f")
	if !complete || len(got) != 10 {
		t.Fatalf("con los topes de siempre: %d coincidencias, completa=%v", len(got), complete)
	}
	if got, complete = globAll(dir + "/*/*"); !complete || len(got) != 10 {
		t.Fatalf("*/*: %d coincidencias, completa=%v", len(got), complete)
	}
	if got, _ = globAll(dir + "/*"); !sort.StringsAreSorted(got) {
		t.Errorf("las coincidencias salen ordenadas, para que la decisión no dependa del orden de la carpeta: %v", got)
	}

	// Carpetas leídas: dir/*/* lee dir y después cada d<N>.
	topesDeGlob(t, 5000, 50000, 5, 400000)
	got, complete = globAll(dir + "/*/*")
	if complete {
		t.Errorf("con 5 carpetas de tope, una expansión que lee 11 se declaró completa")
	}
	if len(got) != 4 {
		t.Errorf("con 5 lecturas (la raíz y cuatro carpetas) quedan 4 coincidencias: %d", len(got))
	}

	// Rutas que se siguen en un tramo.
	topesDeGlob(t, 3, 50000, 4000, 400000)
	got, complete = globAll(dir + "/*")
	if complete || len(got) != 3 {
		t.Errorf("tope de 3 rutas: %d coincidencias, completa=%v", len(got), complete)
	}

	// Nombres que se leen de una carpeta.
	topesDeGlob(t, 5000, 3, 4000, 400000)
	if _, complete = globAll(dir + "/*"); complete {
		t.Errorf("una carpeta con 10 nombres y tope de 3 se declaró completa")
	}

	// Nombres que se leen en toda la expansión.
	topesDeGlob(t, 5000, 50000, 4000, 15)
	if _, complete = globAll(dir + "/*/*"); complete {
		t.Errorf("11 carpetas y 20 nombres con tope de 15 se declararon completas")
	}
	// Y una sola carpeta que se pasa del tope lo declara aunque sea la última.
	topesDeGlob(t, 5000, 50000, 4000, 5)
	if _, complete = globAll(dir + "/*"); complete {
		t.Errorf("una carpeta con 10 nombres y 5 de tope en total se declaró completa")
	}

	// La cuenta es exacta: ni una carpeta ni un nombre de más.
	topesDeGlob(t, 5000, 50000, 5, 400000)
	w := &globWork{}
	w.expand(dir + "/*/*")
	if w.reads != 5 {
		t.Errorf("con 5 carpetas de tope se leyeron %d", w.reads)
	}
	topesDeGlob(t, 5000, 50000, 4000, 5)
	w = &globWork{}
	w.expand(dir + "/*")
	if w.names != 5 {
		t.Errorf("con 5 nombres de tope se leyeron %d", w.names)
	}
	// Lo que se corta en una expansión no cuenta como cortado en la siguiente
	// que sí llega al final, y el aviso sigue.
	topesDeGlob(t, 3, 50000, 4000, 400000)
	w = &globWork{}
	if _, complete = w.expand(dir + "/*"); complete {
		t.Errorf("10 nombres con 3 rutas de tope se declararon completos")
	}
	if _, complete = w.expand(dir + "/d3/f"); !complete {
		t.Errorf("una ruta sin comodines se declaró cortada por lo que cortó a otra")
	}
	if !w.cut || !strings.Contains(w.cutAt, "/*") {
		t.Errorf("la cuenta de la evaluación debe seguir diciendo que algo se cortó, y dónde: cut=%v en %q", w.cut, w.cutAt)
	}

	// Una expansión sin comodines, o con uno solo en el último tramo, lee una
	// carpeta como mucho: no gasta el tope.
	topesDeGlob(t, 5000, 50000, 1, 400000)
	if got, complete = globAll(dir + "/d3/f"); !complete || len(got) != 1 {
		t.Errorf("ruta sin comodines: %d coincidencias, completa=%v", len(got), complete)
	}
	if got, complete = globAll(dir + "/d3/*"); !complete || len(got) != 1 {
		t.Errorf("un comodín en el último tramo: %d coincidencias, completa=%v", len(got), complete)
	}
}

// Un comodín que el gate no alcanza a expandir no es un bloqueo ni un permiso:
// el comando pide la aprobación de una persona, que lo ve entero. Si antes del
// tope apareció un archivo de secretos, es el bloqueo de siempre.
func TestUnComodinQueNoSePuedeExpandirPideAprobacion(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "tienda")
	files := map[string]string{"README.md": "x\n", ".git/HEAD": "ref: refs/heads/main\n", "coyote/project.yaml": "version: 1\n"}
	for i := 0; i < 12; i++ {
		files[fmt.Sprintf("mod/m%02d/src/a.go", i)] = "package a\n"
	}
	files["mod/m00/src/claves.txt"] = "k\n"
	mkTree(t, root, files)
	ps := NewPaths(root, home, filepath.Join(home, ".config", "coyote"))
	ps.Secrets = secrets.Rules{Files: []string{"mod/m00/src/claves.txt"}}
	bash := func(cmd string) Action { return claude(t, "Bash", map[string]any{"command": cmd}, root) }

	lecturas := []string{"cat mod/*/src/*.go", "ls mod/*/src/*", "head -n 3 mod/*/*/a.go", "wc -l mod/m0?/src/*.go",
		"grep -n package mod/*/src/*.go", "cat mod/*/src/*.go README.md"}
	for _, c := range lecturas {
		if d := ps.Evaluate(bash(c)); d.Verdict != Allow {
			t.Errorf("%q con los topes de siempre: %v (%s)", c, d.Verdict, d.Reason)
		}
	}

	topesDeGlob(t, 5000, 50000, 3, 400000)
	for _, c := range lecturas {
		d := ps.Evaluate(bash(c))
		if d.Verdict != NeedsApproval || !strings.Contains(d.Reason, "demasiadas carpetas") || !strings.Contains(d.Reason, "mod/") {
			t.Errorf("%q con 3 carpetas de tope: %v (%s); quiero que pida aprobación y diga por qué y qué comodín", c, d.Verdict, d.Reason)
		}
	}
	// Con el archivo de secretos entre lo que sí alcanzó, se bloquea.
	if d := ps.Evaluate(bash("cat mod/*/src/cl*.txt")); d.Verdict != Block {
		t.Errorf("un comodín cuyo primer resultado es un archivo de secretos: %v (%s)", d.Verdict, d.Reason)
	}
	// Un archivo de secretos nombrado aparte sigue bloqueándose, aunque otro
	// argumento no se haya podido expandir.
	if d := ps.Evaluate(bash("cat mod/*/src/*.go mod/m00/src/claves.txt")); d.Verdict != Block {
		t.Errorf("un comodín que se corta junto a un archivo de secretos: %v (%s)", d.Verdict, d.Reason)
	}
	// Lo que no es lectura sigue pidiendo aprobación como siempre.
	if d := ps.Evaluate(bash("rm mod/*/src/*.go")); d.Verdict != NeedsApproval {
		t.Errorf("rm con un comodín amplio: %v (%s)", d.Verdict, d.Reason)
	}
	// Que un comodín posterior sí se expanda entero no borra el corte de otro:
	// ni cuando el corte fue de carpetas ni cuando fue de rutas.
	topesDeGlob(t, 3, 50000, 4000, 400000)
	for _, c := range []string{"cat mod/* mod/m00/src/a.g?", "cat mod/m00/src/a.g? mod/*"} {
		if d := ps.Evaluate(bash(c)); d.Verdict != NeedsApproval || !strings.Contains(d.Reason, "demasiadas carpetas") {
			t.Errorf("%q con 3 rutas de tope por tramo: %v (%s)", c, d.Verdict, d.Reason)
		}
	}
	topesDeGlob(t, 5000, 50000, 3, 400000)
	// Una herramienta de lectura que recibe una ruta con un comodín que no se
	// alcanza a expandir también lo pide: otra herramienta podría expandirla.
	read := claude(t, "Read", map[string]any{"file_path": filepath.Join(root, "mod", "m*", "*", "a.go")}, root)
	if d := ps.Evaluate(read); d.Verdict != NeedsApproval || !strings.Contains(d.Reason, "demasiadas carpetas") {
		t.Errorf("Read con un comodín que se corta: %v (%s)", d.Verdict, d.Reason)
	}
}

// Los topes valen para toda la evaluación y no para cada comodín: un comando con
// muchos comodines no multiplica el trabajo. Y un comodín repetido se expande
// una vez.
func TestLosTopesDeComodinesValenParaTodoElComando(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "tienda")
	files := map[string]string{"README.md": "x\n", ".git/HEAD": "ref: refs/heads/main\n", "coyote/project.yaml": "version: 1\n"}
	for i := 0; i < 12; i++ {
		files[fmt.Sprintf("mod/m%02d/src/a.go", i)] = "package a\n"
		files[fmt.Sprintf("mod/m%02d/src/b.go", i)] = "package b\n"
	}
	mkTree(t, root, files)
	ps := NewPaths(root, home, filepath.Join(home, ".config", "coyote"))
	bash := func(cmd string) Action { return claude(t, "Bash", map[string]any{"command": cmd}, root) }

	// Lo que cuesta un comodín de 13 carpetas, medido con la cuenta de una
	// evaluación.
	medir := func(cmd string) int {
		w := &globWork{}
		ps2 := ps
		ps2.glob = w
		a := ps2.absCwd(bash(cmd))
		ps2.hardShell(a)
		ps2.readShell(a)
		return w.reads
	}
	uno := medir("cat mod/*/*/a.go")
	if uno < 13 {
		t.Fatalf("un comodín de 13 carpetas gastó %d lecturas", uno)
	}
	if dos := medir("cat mod/*/*/a.go mod/*/*/a.go"); dos != uno {
		t.Errorf("el mismo comodín dos veces gastó %d lecturas; una sola vez, %d", dos, uno)
	}
	if dos := medir("cat mod/*/*/a.go mod/*/*/b.go"); dos < 2*uno-1 {
		t.Errorf("dos comodines distintos gastaron %d lecturas; uno, %d", dos, uno)
	}

	topesDeGlob(t, 5000, 50000, uno+uno/2, 400000)
	for _, c := range []string{"cat mod/*/*/a.go", "cat mod/*/*/a.go mod/*/*/a.go"} {
		if d := ps.Evaluate(bash(c)); d.Verdict != Allow {
			t.Errorf("%q con topes para uno y medio: %v (%s)", c, d.Verdict, d.Reason)
		}
	}
	if d := ps.Evaluate(bash("cat mod/*/*/a.go mod/*/*/b.go")); d.Verdict != NeedsApproval || !strings.Contains(d.Reason, "demasiadas carpetas") {
		t.Errorf("dos comodines distintos que juntos pasan el tope: %v (%s)", d.Verdict, d.Reason)
	}
}

// Los archivos que un comodín alcanza se revisan todos, no los primeros: un
// archivo de secretos al final de una carpeta grande no se escapa.
func TestUnComodinRevisaTodoLoQueAlcanza(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "tienda")
	files := map[string]string{"README.md": "x\n", ".git/HEAD": "ref: refs/heads/main\n", "coyote/project.yaml": "version: 1\n"}
	for i := 0; i < 1100; i++ {
		files[fmt.Sprintf("datos/f%04d.csv", i)] = "1\n"
	}
	files["datos/zz-claves.txt"] = "k\n"
	mkTree(t, root, files)
	ps := NewPaths(root, home, filepath.Join(home, ".config", "coyote"))
	ps.Secrets = secrets.Rules{Files: []string{"datos/zz-claves.txt"}}
	bash := func(cmd string) Action { return claude(t, "Bash", map[string]any{"command": cmd}, root) }
	if d := ps.Evaluate(bash("cat datos/*")); d.Verdict != Block {
		t.Errorf("cat sobre una carpeta de 1101 archivos, el de secretos al final: %v (%s)", d.Verdict, d.Reason)
	}
	if d := ps.Evaluate(bash("cat datos/*.csv")); d.Verdict != Allow {
		t.Errorf("cat sobre 1100 archivos que no son de secretos: %v (%s)", d.Verdict, d.Reason)
	}
}
