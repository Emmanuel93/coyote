package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const cfgText = `# Proyecto Coyote.
version: 1
name: hub
type: hub

language: { docs: es, code: en }
autonomy: manual                 # manual | supervised | autonomous (A1, A4)
`

func writeCfg(t *testing.T, content string) string {
	t.Helper()
	root := t.TempDir()
	p := filepath.Join(root, "coyote", "project.yaml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func read(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "coyote", "project.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestAddRepoKeepsFormatting(t *testing.T) {
	root := writeCfg(t, cfgText)
	if st, err := AddRepo(root, RepoRef{Name: "pagos", URL: "git@github.com:acme/pagos.git"}); err != nil || st != "agregado" {
		t.Fatalf("%s %v", st, err)
	}
	if st, err := AddRepo(root, RepoRef{Name: "envios", URL: "https://github.com/acme/envios.git", Branch: "main"}); err != nil || st != "agregado" {
		t.Fatalf("%s %v", st, err)
	}
	if st, err := AddRepo(root, RepoRef{Name: "pagos", URL: "git@github.com:acme/pagos-v2.git"}); err != nil || st != "actualizado" {
		t.Fatalf("%s %v", st, err)
	}
	got := read(t, root)
	want := cfgText + "repos:\n  - { name: pagos, url: \"git@github.com:acme/pagos-v2.git\" }\n  - { name: envios, url: \"https://github.com/acme/envios.git\", branch: \"main\" }\n"
	if got != want {
		t.Fatalf("formato perdido o entrada mal escrita:\n%s\n--- se esperaba ---\n%s", got, want)
	}
	cfg, err := Load(root)
	if err != nil || len(cfg.Repos) != 2 || cfg.Repos[0].URL != "git@github.com:acme/pagos-v2.git" || cfg.Repos[1].Branch != "main" {
		t.Fatalf("Load: %+v %v", cfg, err)
	}
}

func TestAddRepoEmptyAndInline(t *testing.T) {
	for _, repos := range []string{"repos:\n", "repos: []\n", "repos:   # sin repos todavía\n", "repos: [{name: viejo, url: \"https://example.com/v.git\"}]\n"} {
		root := writeCfg(t, cfgText+repos+"budgets:\n  monthly_usd: 0\n")
		if _, err := AddRepo(root, RepoRef{Name: "pagos", URL: "https://example.com/p.git"}); err != nil {
			t.Fatalf("%q: %v", repos, err)
		}
		cfg, err := Load(root)
		if err != nil || cfg.Repos[len(cfg.Repos)-1].Name != "pagos" || cfg.Budgets.MonthlyUSD != 0 {
			t.Fatalf("%q: %+v %v\n%s", repos, cfg, err, read(t, root))
		}
		if !strings.Contains(read(t, root), "# manual | supervised | autonomous") {
			t.Fatalf("%q: se perdieron comentarios", repos)
		}
	}
}
