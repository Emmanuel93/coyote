package hub

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestRefDesdeProjectYAML(t *testing.T) {
	cases := []struct {
		in   string
		want Ref
		err  bool
	}{
		{`hub: ""`, Ref{}, false},
		{`hub:`, Ref{}, false},
		{`hub: ../acme-hub`, Ref{Path: "../acme-hub"}, false},
		{`hub: { path: ~/Documents/acme-hub, ref: v3 }`, Ref{Path: "~/Documents/acme-hub", Ref: "v3"}, false},
		{`hub: { path: ../h, rama: main }`, Ref{}, true},
		{`hub: [a, b]`, Ref{}, true},
		{`hub: { path: [a] }`, Ref{}, true},
	}
	for _, c := range cases {
		var p struct {
			Hub Ref `yaml:"hub"`
		}
		err := yaml.Unmarshal([]byte(c.in), &p)
		if (err != nil) != c.err || (!c.err && p.Hub != c.want) {
			t.Errorf("%s: %+v, %v", c.in, p.Hub, err)
		}
	}
	for _, bad := range []Ref{{Path: "https://github.com/acme/hub"}, {Path: "git@github.com:acme/hub.git"}, {Ref: "main"},
		{Path: "../h", Ref: "-c"}, {Path: "../h", Ref: "main..dev"}, {Path: "../h", Ref: "main^{tree}"}, {Path: "../h", Ref: "main:x"}} {
		if bad.Validate() == nil {
			t.Errorf("%+v no es válido", bad)
		}
	}
	out, _ := yaml.Marshal(struct {
		Hub Ref `yaml:"hub"`
	}{Ref{Path: "../h", Ref: "v1"}})
	if !strings.Contains(string(out), "ref: v1") {
		t.Fatalf("se escribe como mapa con ref: %s", out)
	}
}

func TestHubYAML(t *testing.T) {
	c, err := Parse([]byte("version: 1\norg: acme\nadmins: [ana, \"@Ana\", \"@luis\"]\nbudgets: { monthly_usd: 120 }\nprojects:\n  - { name: shop, path: ../shop, repo: acme/shop }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(c.Admins, ",") != "@ana,@luis" || !c.IsAdmin("ana") || !c.IsAdmin("@LUIS") || c.IsAdmin("@beto") {
		t.Fatalf("admins normalizados: %v", c.Admins)
	}
	for _, bad := range []string{
		"version: 2\norg: acme\n",
		"version: 1\norg: \"\"\n",
		"version: 1\norg: acme\nadmin: [ana]\n",
		"version: 1\norg: acme\nadmins: [\"ana smith\"]\n",
		"version: 1\norg: acme\nbudgets: { monthly_usd: -1 }\n",
		"version: 1\norg: acme\nprojects: [{ name: shop }, { name: Shop }]\n",
		"version: 1\norg: acme\nprojects: [{ name: shop, repo: \"acme/shop; rm\" }]\n",
		"version: 1\norg: acme\nprojects: [{ name: shop, path: \"https://x/y\" }]\n",
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("debió fallar: %q", bad)
		}
	}
}

func TestCleanRel(t *testing.T) {
	for _, ok := range []string{"coyote/hub.yaml", "coyote/standards/../standards/rules.yaml", "domains/pagos.md"} {
		if _, err := CleanRel(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", ".", "..", "../x", "/etc/passwd", "coyote/../../x", "a:b", "a\nb"} {
		if _, err := CleanRel(bad); err == nil {
			t.Errorf("%q sale del hub", bad)
		}
	}
}

// repo crea un repo git con un commit y devuelve una función para correr git.
func repo(t *testing.T, dir string) func(args ...string) string {
	t.Helper()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=Ana", "-c", "user.email=ana@example.com", "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if err := os.MkdirAll(filepath.Join(dir, "coyote"), 0o755); err != nil {
		t.Fatal(err)
	}
	run("init", "-q", "-b", "main")
	return run
}

func write(t *testing.T, dir, rel, text string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRefsQueNoCambianLoQueRige(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "hub")
	git := repo(t, dir)
	write(t, dir, ConfFile, "version: 1\norg: acme\nadmins: [\"@ana\"]\n")
	git("add", "-A")
	git("commit", "-qm", "hub")
	mainSHA := git("rev-parse", "HEAD")
	// Una etiqueta llamada main en otro commit no suplanta a la rama.
	git("switch", "-q", "-c", "feat")
	write(t, dir, ConfFile, "version: 1\norg: acme\nadmins: [\"@ana\", \"@mallory\"]\n")
	git("commit", "-qam", "admins")
	git("tag", "main")
	git("switch", "-q", "main")
	if _, err := Open(base, Ref{Path: "hub"}); err == nil || !strings.Contains(err.Error(), "ambigua") {
		t.Fatalf("una rama y una etiqueta con el mismo nombre son un error: %v", err)
	}
	h, err := Open(base, Ref{Path: "hub", Ref: "refs/heads/main"})
	if err != nil || h.Commit != mainSHA || h.Conf.IsAdmin("@mallory") || !strings.Contains(h.String(), "rama main@") {
		t.Fatalf("la ref completa elige la rama: %v %+v", err, h)
	}
	git("tag", "-d", "main")
	if h, err = Open(base, Ref{Path: "hub"}); err != nil || h.Commit != mainSHA {
		t.Fatalf("sin la etiqueta, main es la rama: %v", err)
	}
	for _, bad := range []string{"HEAD", "ORIG_HEAD", mainSHA[:12], "feat-1-g" + mainSHA[:7]} {
		if _, err := Open(base, Ref{Path: "hub", Ref: bad}); err == nil {
			t.Errorf("ref %q no debe valer", bad)
		}
	}
	if h, err := Open(base, Ref{Path: "hub", Ref: mainSHA}); err != nil || h.Kind() != "commit" {
		t.Fatalf("un commit completo vale: %v", err)
	}
	// GIT_DIR heredado (un hook de git, un worktree) no cambia el repo que se lee.
	other := filepath.Join(base, "otro")
	gitOther := repo(t, other)
	write(t, other, ConfFile, "version: 1\norg: otro\nadmins: [\"@mallory\"]\n")
	gitOther("add", "-A")
	gitOther("commit", "-qm", "otro")
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	if h, err := Open(base, Ref{Path: "hub"}); err != nil || h.Conf.Org != "acme" {
		t.Fatalf("GIT_DIR del entorno no debe cambiar el hub: %v %+v", err, h)
	}
	if err := os.MkdirAll(filepath.Join(base, "no-es-repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(base, Ref{Path: "no-es-repo"}); err == nil {
		t.Fatal("una carpeta sin repo no es un hub, aunque GIT_DIR apunte a otro")
	}
}

func TestInsideConSymlinks(t *testing.T) {
	base := t.TempDir()
	proj := filepath.Join(base, "shop")
	if err := os.MkdirAll(filepath.Join(proj, "vendor", "hub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(proj, "vendor", "hub"), filepath.Join(base, "hubvendor")); err != nil {
		t.Fatal(err)
	}
	if !Inside(proj, filepath.Join(base, "hubvendor")) {
		t.Fatal("un symlink de afuera que apunta al proyecto sigue adentro")
	}
	if Inside(proj, filepath.Join(base, "otro")) {
		t.Fatal("una carpeta hermana está afuera")
	}
}

func TestHubYAMLHostil(t *testing.T) {
	bomb := "version: 1\norg: acme\nadmins: [&s \"" + strings.Repeat("A", 1000) + "\"" + strings.Repeat(", *s", 2000) + "]\n"
	if _, err := Parse([]byte(bomb)); err == nil || len(err.Error()) > 500 {
		t.Fatalf("alias: %d bytes de error", len(fmt.Sprint(err)))
	}
	if _, err := Parse([]byte("version: 1\norg: acme\nadmins: [\"@ana\"]\n---\nadmins: [\"@luis\"]\n")); err == nil {
		t.Fatal("un segundo documento no se ignora")
	}
	if _, err := Parse([]byte("version: 1\norg: acme\nprojects: [{ name: x, path: \"../x\\e[2K\\rtodo bien\" }]\n")); err == nil {
		t.Fatal("una ruta con controles no vale")
	}
	many := "version: 1\norg: acme\nadmins: [" + strings.TrimSuffix(strings.Repeat("\"x\", ", 300), ", ") + "]\n"
	if _, err := Parse([]byte(many)); err == nil || len(err.Error()) > 300 {
		t.Fatalf("demasiados admins: %v", err)
	}
}

func TestArbolConEntradasDuplicadas(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "hub")
	git := repo(t, dir)
	write(t, dir, ConfFile, "version: 1\norg: acme\n")
	git("add", "-A")
	git("commit", "-qm", "hub")
	blob := git("rev-parse", "HEAD:"+ConfFile)
	// Un árbol hecho a mano con dos entradas hub.yaml.
	cmd := exec.Command("git", "-C", dir, "mktree")
	cmd.Stdin = strings.NewReader("100644 blob " + blob + "\thub.yaml\n100644 blob " + blob + "\thub.yaml\n")
	out, err := cmd.Output()
	if err != nil {
		t.Skip("git mktree no acepta entradas duplicadas en esta versión")
	}
	inner := strings.TrimSpace(string(out))
	cmd = exec.Command("git", "-C", dir, "mktree")
	cmd.Stdin = strings.NewReader("040000 tree " + inner + "\tcoyote\n")
	out, err = cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	commit := git("commit-tree", strings.TrimSpace(string(out)), "-m", "duplicado")
	git("update-ref", "refs/heads/main", commit)
	if _, err := Open(base, Ref{Path: "hub"}); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("entradas duplicadas son un error, no un archivo que falta: %v", err)
	}
}
