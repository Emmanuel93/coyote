package fsx

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNoSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "coyote", "ledger"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".claude")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "x.md"), filepath.Join(root, "README.md")); err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{
		"coyote/ledger/2026/09/28-ana.ccf": true,
		"CONTEXT.coyote.md":                true,
		".claude/settings.json":            false,
		"README.md":                        false,
		"../fuera.md":                      false,
	}
	for rel, ok := range cases {
		if err := NoSymlinks(root, rel); (err == nil) != ok {
			t.Errorf("NoSymlinks(%q) = %v, se esperaba ok=%v", rel, err, ok)
		}
	}
}

func TestWriteAtomicNoSigueUnTemporalPlantado(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victima.txt")
	if err := os.WriteFile(victim, []byte("intacto"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "svc.map")
	// El temporal de nombre fijo de antes, plantado como symlink.
	if err := os.Symlink(victim, target+".tmp"); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(target, []byte("mapa"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(victim); string(got) != "intacto" {
		t.Errorf("la escritura salió por el symlink: %q", got)
	}
	info, err := os.Lstat(target)
	if err != nil || !info.Mode().IsRegular() {
		t.Errorf("el destino es un archivo regular: %v %v", info, err)
	}
	if got, _ := os.ReadFile(target); string(got) != "mapa" {
		t.Errorf("contenido: %q", got)
	}
	left, _ := filepath.Glob(filepath.Join(dir, ".svc.map.*.tmp"))
	if len(left) != 0 {
		t.Errorf("quedaron temporales: %v", left)
	}
}
