package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestFuentesSinElEntornoDeQuienCompila: cuarta pasada de v1.0. Los atributos
// globales, las plantillas con hooks y la configuración que llega por el
// entorno no cambian los bytes del release ni corren nada.
func TestFuentesSinElEntornoDeQuienCompila(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git no está instalado")
	}
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	for _, d := range []string{repo, filepath.Join(tmp, "xdg", "git"), filepath.Join(tmp, "plantilla", "hooks")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	marca := filepath.Join(tmp, "hook-corrio")
	files := map[string]string{
		filepath.Join(tmp, "xdg", "git", "attributes"):            "* text eol=crlf\n",
		filepath.Join(tmp, "plantilla", "hooks", "post-checkout"): "#!/bin/sh\ntouch '" + marca + "'\n",
		filepath.Join(repo, "a.txt"):                              "uno\ndos\n",
	}
	for p, text := range files {
		if err := os.WriteFile(p, []byte(text), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	gitc := func(env []string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=Ana", "-c", "user.email=ana@example.com", "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}, args...)...)
		cmd.Env = append(os.Environ(), env...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	gitc(nil, "init", "-q", "-b", "main")
	gitc(nil, "add", "a.txt")
	gitc(nil, "commit", "-qm", "inicio")
	gitc(nil, "tag", "-a", "-m", "v1", "v1.0.0")
	head := gitc(nil, "rev-parse", "HEAD")

	script, _ := filepath.Abs(filepath.Join("..", "distsrc.sh"))
	dest := filepath.Join(tmp, "dest")
	cmd := exec.Command("sh", script, repo, dest, head)
	cmd.Env = append(os.Environ(),
		"XDG_CONFIG_HOME="+filepath.Join(tmp, "xdg"),
		"GIT_TEMPLATE_DIR="+filepath.Join(tmp, "plantilla"),
		"GIT_CONFIG_COUNT=2", "GIT_CONFIG_KEY_0=core.autocrlf", "GIT_CONFIG_VALUE_0=true",
		"GIT_CONFIG_KEY_1=core.hooksPath", "GIT_CONFIG_VALUE_1="+filepath.Join(tmp, "plantilla", "hooks"),
		"GIT_CONFIG_PARAMETERS='core.eol=crlf'")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("distsrc.sh: %v\n%s", err, out)
	}
	got, err := os.ReadFile(filepath.Join(dest, "src", "a.txt"))
	if err != nil || string(got) != "uno\ndos\n" {
		t.Errorf("los bytes del release no son los del commit: %q (%v)", got, err)
	}
	if _, err := os.Stat(marca); err == nil {
		t.Error("el hook de la plantilla corrió")
	}
	meta := func(name string) string { return distMeta(t, dest, name) }
	if meta("ver") != "v1.0.0" || meta("tag") != "v1.0.0" || meta("full") != head || meta("commit") != head[:12] {
		t.Errorf("meta: ver=%q tag=%q full=%q commit=%q", meta("ver"), meta("tag"), meta("full"), meta("commit"))
	}
	if d := meta("date"); len(d) != len("2026-10-10T12:00Z") || !strings.HasSuffix(d, "Z") || !strings.HasSuffix(meta("rfc"), "Z") {
		t.Errorf("fechas: %q %q", d, meta("rfc"))
	}
	// Un commit sin etiqueta: la versión lleva 12 dígitos y no hay etiqueta exacta.
	if err := os.WriteFile(filepath.Join(repo, "b.txt"), []byte("tres\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitc(nil, "add", "b.txt")
	gitc(nil, "commit", "-qm", "otro")
	head = gitc(nil, "rev-parse", "HEAD")
	dest = filepath.Join(tmp, "dest2")
	cmd = exec.Command("sh", script, repo, dest, head)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("distsrc.sh: %v\n%s", err, out)
	}
	if v := distMeta(t, dest, "ver"); v != "v1.0.0-1-g"+head[:12] || distMeta(t, dest, "tag") != "" {
		t.Errorf("versión de un commit sin etiqueta: %q", v)
	}
}

// distMeta lee un dato de <dest>/meta.
func distMeta(t *testing.T, dest, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dest, "meta", name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}
