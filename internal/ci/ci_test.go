package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadPR(t *testing.T) {
	dir := t.TempDir()
	write := func(s string) string {
		p := filepath.Join(dir, "ev.json")
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	sha := strings.Repeat("a", 40)
	pr, err := ReadPR(write(`{"number":3,"pull_request":{"number":3,"draft":true,"base":{"sha":"` + sha + `","ref":"main","repo":{"full_name":"o/r"}},"head":{"sha":"` + strings.Repeat("b", 40) + `","ref":"f","repo":{"full_name":"x/r"}}}}`))
	if err != nil || pr.Number != 3 || !pr.Draft || !pr.FromFork() || pr.Repo != "o/r" {
		t.Errorf("evento: %+v %v", pr, err)
	}
	if _, err := ReadPR(write(`{"push":{}}`)); err == nil {
		t.Error("un evento sin pull_request es un error")
	}
	if _, err := ReadPR(write(`{"pull_request":{"base":{"sha":"--output=x"},"head":{"sha":"` + sha + `"}}}`)); err == nil {
		t.Error("un commit que no es un SHA completo se rechaza")
	}
}

func TestClip(t *testing.T) {
	long := strings.Repeat("| fila |\n", 20000)
	out := Clip(long, "https://ci/run/1")
	if len(out) > 65536 || !strings.Contains(out, "resumen del job") {
		t.Errorf("recorte: %d", len(out))
	}
	if Clip("corto", "") != "corto" {
		t.Error("lo corto no se toca")
	}
}
