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
