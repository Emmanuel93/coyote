package cli

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestSecretsListYScanDesdeUnAnidadoVenLoQueDeclaraElDeAfuera: séptima revisión
// de v1.0. secrets list y secrets scan, corridos desde un proyecto anidado,
// tomaban ese proyecto como tope y no veían los archivos que declara el de
// afuera, que el gate y el commit sí bloquean: un agente que pide los nombres
// no los veía, y el escaneo daba el visto bueno a lo que el commit rechaza.
func TestSecretsListYScanDesdeUnAnidadoVenLoQueDeclaraElDeAfuera(t *testing.T) {
	_, root := gateProject(t)
	cfg := readFile(t, filepath.Join(root, "coyote", "project.yaml"))
	write(t, root, "coyote/project.yaml", cfg+"secrets:\n  files: [\"svc/declarado-raiz.txt\", \"svc/a/**/declarado-raiz.txt\"]\n")
	must(t, run(t, root, "", "init", "svc", "--no-git", "--type", "backend", "--purpose", "servicio de pagos de la tienda"), 0, "init anidado")
	svcCfg := readFile(t, filepath.Join(root, "svc", "coyote", "project.yaml"))
	write(t, root, "svc/coyote/project.yaml", svcCfg+"secrets:\n  files: [\"propio/*.txt\"]\n")
	write(t, root, "svc/declarado-raiz.txt", "uno\n")
	write(t, root, "svc/a/b/c/d/declarado-raiz.txt", "dos\n")
	write(t, root, "svc/propio/k.txt", "tres\n")
	write(t, root, "svc/README.md", "# servicio\n")
	write(t, root, "otro/declarado-raiz.txt", "cuatro\n")
	// Lo que git sigue dentro de dependencias no se recorre, pero se lista.
	write(t, root, "svc/vendor/lib/.env", "A=1\n")
	write(t, root, ".gitignore", ".coyote/\n")
	git(t, root, "add", "-A")
	git(t, root, "add", "-f", "svc/vendor")
	git(t, root, "commit", "-qm", "chore: inicio", "--no-verify")
	svc := filepath.Join(root, "svc")

	paths := func(dir string) []string {
		t.Helper()
		r := run(t, dir, "", "secrets", "list", "--no-names", "--json")
		must(t, r, 0, "secrets list")
		var entries []secretEntry
		if err := json.Unmarshal([]byte(r.stdout), &entries); err != nil {
			t.Fatalf("json: %v\n%s", err, r.stdout)
		}
		var out []string
		for _, e := range entries {
			out = append(out, e.Path)
		}
		sort.Strings(out)
		return out
	}
	// Desde el anidado, con las rutas de ese proyecto; desde la raíz, con las del repo.
	if got, want := strings.Join(paths(svc), " "), "a/b/c/d/declarado-raiz.txt declarado-raiz.txt propio/k.txt vendor/lib/.env"; got != want {
		t.Errorf("secrets list desde el anidado:\n  %s\nquiero\n  %s", got, want)
	}
	if got, want := strings.Join(paths(root), " "), "svc/a/b/c/d/declarado-raiz.txt svc/declarado-raiz.txt svc/propio/k.txt svc/vendor/lib/.env"; got != want {
		t.Errorf("secrets list desde la raíz:\n  %s\nquiero\n  %s", got, want)
	}
	// El escaneo del árbol, desde el anidado, acusa lo mismo y solo lo suyo.
	s := run(t, svc, "", "secrets", "scan")
	if s.code != 1 {
		t.Fatalf("secrets scan desde el anidado (código %d):\n%s%s", s.code, s.stdout, s.stderr)
	}
	for _, want := range []string{"declarado-raiz.txt", "a/b/c/d/declarado-raiz.txt", "propio/k.txt"} {
		if !strings.Contains(s.stdout, want) {
			t.Errorf("secrets scan no acusa %s:\n%s", want, s.stdout)
		}
	}
	if strings.Contains(s.stdout, "otro/") || strings.Contains(s.stdout, "README") {
		t.Errorf("secrets scan desde el anidado no revisa lo de afuera:\n%s", s.stdout)
	}
}
