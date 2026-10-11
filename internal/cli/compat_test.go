package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Emmanuel93/coyote/internal/version"
)

// TestRequiresDeProyectoYHub cubre ADR-0026: un archivo creado por coyote 1.x
// pide su línea, y un coyote más viejo lo dice claro.
func TestRequiresDeProyectoYHub(t *testing.T) {
	oldV, oldC := version.Version, version.Commit
	t.Cleanup(func() { version.Version, version.Commit = oldV, oldC })
	version.Version, version.Commit = "v1.2.0", "abc1234"
	base := setup(t)
	must(t, run(t, base, "", "init", "tienda", "--purpose", "API de pedidos de la tienda demo"), 0, "init")
	must(t, run(t, base, "", "init", "acme-hub", "--type", "hub", "--purpose", "hub de la organización demo"), 0, "init hub")
	root := filepath.Join(base, "tienda")
	if cfg := readFile(t, filepath.Join(root, "coyote", "project.yaml")); !strings.Contains(cfg, "requires: \">=1.2\"") {
		t.Fatalf("project.yaml debe pedir la línea que lo creó:\n%s", cfg)
	}
	if h := readFile(t, filepath.Join(base, "acme-hub", "coyote", "hub.yaml")); !strings.Contains(h, "requires: \">=1.2\"") {
		t.Fatalf("hub.yaml debe pedir la línea que lo creó:\n%s", h)
	}
	must(t, run(t, root, "", "status"), 0, "la misma línea lo lee")
	version.Version = "v1.1.4"
	r := run(t, root, "", "status")
	must(t, r, 1, "un coyote más viejo")
	if !strings.Contains(r.stderr, "coyote/project.yaml pide coyote >=1.2 y tienes v1.1.4: actualiza coyote") {
		t.Errorf("el mensaje debe decir qué hacer:\n%s", r.stderr)
	}
	r = run(t, filepath.Join(base, "acme-hub"), "", "hub", "status")
	if r.code == 0 || !strings.Contains(r.stdout+r.stderr, "pide coyote >=1.2 y tienes v1.1.4") {
		t.Errorf("hub status con un coyote más viejo:\n%s%s", r.stdout, r.stderr)
	}
}
