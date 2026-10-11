package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReferenciaDeComandos: docs/reference/cli.md sale de la CLI y no puede
// quedar vieja (T58).
func TestReferenciaDeComandos(t *testing.T) {
	p := filepath.Join("..", "..", "docs", "reference", "cli.md")
	want := Reference()
	if os.Getenv("COYOTE_UPDATE_DOCS") != "" {
		if err := os.WriteFile(p, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(p)
	if err != nil || string(got) != want {
		t.Fatalf("docs/reference/cli.md está vieja; regenérala con COYOTE_UPDATE_DOCS=1 go test ./internal/cli -run TestReferenciaDeComandos")
	}
}
