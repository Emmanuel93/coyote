package cli

import (
	"bytes"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Emmanuel93/coyote/internal/auth"
)

// Las marcas de atribución se arman por partes para que este archivo no las contenga literales.
var (
	aiTrailer = "Co-Authored-By: " + "Cla" + "ude <noreply@" + "anthropic.com>"
	aiFooter  = "Gene" + "rated with [Cla" + "ude Code](https://claude.com/claude-code)"
)

type result struct {
	code           int
	stdout, stderr string
}

func run(t *testing.T, dir, stdin string, args ...string) result {
	t.Helper()
	var out, errb bytes.Buffer
	code := Main(append([]string{"-C", dir}, args...), strings.NewReader(stdin), &out, &errb)
	return result{code, out.String(), errb.String()}
}

func must(t *testing.T, r result, want int, what string) {
	t.Helper()
	if r.code != want {
		t.Fatalf("%s: código %d, se esperaba %d\nstdout:\n%s\nstderr:\n%s", what, r.code, want, r.stdout, r.stderr)
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func ledgerText(t *testing.T, root string) string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(root, "coyote", "ledger", "*", "*", "*.ccf"))
	var b strings.Builder
	for _, f := range files {
		b.WriteString(readFile(t, f))
	}
	return b.String()
}

// setup deja un entorno aislado: HOME y configuración global de git propios.
func setup(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git no está instalado")
	}
	base := t.TempDir()
	home := filepath.Join(base, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	gitconfig := filepath.Join(home, ".gitconfig")
	cfg := "[user]\n\tname = Ana Pérez\n\temail = ana@example.com\n[init]\n\tdefaultBranch = main\n[commit]\n\tgpgsign = false\n"
	if err := os.WriteFile(gitconfig, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", gitconfig)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("COYOTE_USER", "ana")
	return base
}

func TestFlujoCompleto(t *testing.T) {
	base := setup(t)
	root := filepath.Join(base, "acme")

	r := run(t, base, "", "init", "acme", "--type", "backend", "--purpose", "API de pedidos de la tienda demo")
	must(t, r, 0, "init")
	for _, f := range []string{"README.md", "README.coyote.md", "CONTEXT.coyote.md", "AGENTS.md", "CLAUDE.md",
		".coyoteignore", ".gitignore", ".claude/settings.json", "coyote/project.yaml", "coyote/standards/rules.yaml"} {
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			t.Errorf("init no creó %s", f)
		}
	}
	if !strings.Contains(ledgerText(t, root), "|@ana|-|acme|init|") {
		t.Errorf("falta el evento init en el ledger:\n%s", ledgerText(t, root))
	}

	// Una segunda corrida no sobrescribe nada.
	must(t, run(t, root, "", "init"), 0, "init repetido")

	must(t, run(t, root, "", "doctor"), 0, "doctor")
	must(t, run(t, root, "", "generate", "agents", "--check"), 0, "AGENTS.md recién generado")

	r = run(t, root, "", "note", "los pedidos se confirman solo con pago capturado", "--type", "inv", "--scope", "pedidos/diseño")
	must(t, r, 0, "note")
	if !strings.Contains(readFile(t, filepath.Join(root, "CONTEXT.coyote.md")), "inv|pedidos/diseño|los pedidos se confirman") {
		t.Error("la nota no llegó a CONTEXT.coyote.md")
	}
	must(t, run(t, root, "", "generate", "agents", "--check"), 0, "AGENTS.md se actualiza con la nota")
	if !strings.Contains(readFile(t, filepath.Join(root, "AGENTS.md")), "los pedidos se confirman") {
		t.Error("AGENTS.md no incluye la nota")
	}
	must(t, run(t, root, "", "note", "x", "--type", "nope"), 1, "note con tipo inválido")

	r = run(t, root, "", "record", "plan", "definir el dominio de pedidos", "--scope", "pedidos", "--ws", "W-0001",
		"--tokens", "12.4k/8.7k/1.1k", "--cost", "0.009+0.011", "--agent", "coyote-architect")
	must(t, r, 0, "record")
	must(t, run(t, root, "", "record", "foo", "x"), 1, "record con tipo inválido")

	r = run(t, root, "", "log", "--type", "plan")
	must(t, r, 0, "log")
	if !strings.Contains(r.stdout, "@ana/coyote-architect") || !strings.Contains(r.stdout, "costo $0.02") {
		t.Errorf("log sin el evento o sin totales:\n%s", r.stdout)
	}

	// R2: un mensaje fuera de formato no genera commit ni toca el ledger.
	before := ledgerText(t, root)
	must(t, run(t, root, "", "commit", "-m", "added stuff"), 1, "commit fuera de formato")
	if ledgerText(t, root) != before {
		t.Error("el ledger cambió con un commit rechazado")
	}

	git(t, root, "add", "-A")
	r = run(t, root, "", "commit", "-m", "feat(pedidos): alta del proyecto", "-m", aiTrailer, "--ws", "W-0001")
	must(t, r, 0, "commit con trailer de IA")
	msg := git(t, root, "log", "-1", "--format=%an <%ae>%n%B")
	if strings.Contains(msg, "Co-Authored-By") || strings.Contains(strings.ToLower(msg), "anthropic") {
		t.Errorf("el commit conserva la atribución:\n%s", msg)
	}
	if !strings.HasPrefix(msg, "Ana Pérez <ana@example.com>\nfeat(pedidos): alta del proyecto") {
		t.Errorf("autoría o asunto inesperados:\n%s", msg)
	}
	led := ledgerText(t, root)
	for _, want := range []string{"|@ana|W-0001|acme|attr|pedidos|1 marca de atribución", "|@ana|W-0001|acme|feat|pedidos|alta del proyecto|"} {
		if !strings.Contains(led, want) {
			t.Errorf("falta %q en el ledger:\n%s", want, led)
		}
	}
	if st := git(t, root, "status", "--porcelain"); st != "" {
		t.Errorf("el árbol quedó sucio después del commit:\n%s", st)
	}

	// Sin cambios preparados no hay commit (git tampoco lo haría); tampoco con rutas sueltas.
	must(t, run(t, root, "", "commit", "-m", "fix(pedidos): nada"), 1, "commit sin cambios")
	must(t, run(t, root, "", "commit", "-m", "fix(pedidos): algo", "README.md"), 2, "commit con rutas")

	// Una frase que atribuye el cambio a una herramienta detiene el commit; no se reescribe.
	if err := os.WriteFile(filepath.Join(root, "cambio.txt"), []byte("uno\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "cambio.txt")
	must(t, run(t, root, "", "commit", "-m", "feat(api): endpoint generado con "+"Cla"+"ude Code"), 1, "commit con frase de atribución")

	// Una identidad de IA como autor no pasa, aunque venga de variables de entorno.
	t.Setenv("GIT_AUTHOR_NAME", "Cursor Agent")
	t.Setenv("GIT_AUTHOR_EMAIL", "cursoragent@"+"cursor.com")
	must(t, run(t, root, "", "commit", "-m", "fix(pedidos): algo"), 1, "commit con autor de IA")
	os.Unsetenv("GIT_AUTHOR_NAME")
	os.Unsetenv("GIT_AUTHOR_EMAIL")

	// Si git commit falla, el ledger vuelve a su estado anterior y sale del índice.
	hook := filepath.Join(root, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	before = ledgerText(t, root)
	must(t, run(t, root, "", "commit", "-m", "fix(pedidos): algo"), 1, "commit con pre-commit fallido")
	if ledgerText(t, root) != before {
		t.Error("el ledger no se restauró tras el fallo de git commit")
	}
	if st := git(t, root, "status", "--porcelain", "--", "coyote/ledger"); st != "" {
		t.Errorf("el ledger quedó modificado o en el índice tras el rollback:\n%s", st)
	}
	os.Remove(hook)
	must(t, run(t, root, "", "commit", "-m", "fix(pedidos): algo"), 0, "commit después del rollback")

	r = run(t, root, "", "commit", "--dry-run", "-m", "docs: guía\n\n"+aiFooter)
	must(t, r, 0, "commit --dry-run")
	if strings.Contains(r.stdout, "Cla"+"ude Code") || !strings.Contains(r.stdout, "evento:") {
		t.Errorf("dry-run inesperado:\n%s", r.stdout)
	}

	must(t, run(t, root, "", "standards", "lint"), 0, "standards lint")
	must(t, run(t, root, "", "standards", "explain", "R15"), 0, "standards explain")
	must(t, run(t, root, "", "standards", "explain", "R999"), 1, "standards explain inexistente")
	must(t, run(t, root, "", "attribution", "check"), 0, "attribution check del proyecto")

	bad := filepath.Join(root, "notas.md")
	if err := os.WriteFile(bad, []byte("# Notas\n\n"+aiFooter+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	must(t, run(t, root, "", "attribution", "check", bad), 1, "attribution check con pie de IA")
	r = run(t, root, "", "attribution", "scrub", bad, "--in-place")
	must(t, r, 0, "attribution scrub")
	if strings.Contains(readFile(t, bad), "Cla"+"ude") {
		t.Error("scrub no quitó el pie")
	}
	must(t, run(t, root, "", "status"), 0, "status")
	must(t, run(t, root, "", "status", "--json"), 0, "status --json")
}

func TestGate(t *testing.T) {
	dir := t.TempDir()
	quoted := strings.ReplaceAll(aiTrailer, `"`, `\"`)
	cases := []struct {
		name, input string
		want        int
	}{
		{"bash con trailer", `{"tool_name":"Bash","tool_input":{"command":"git commit -m \"feat: x\n\n` + quoted + `\""}}`, 2},
		{"bash limpio", `{"tool_name":"Bash","tool_input":{"command":"git commit -m \"feat: x\""}}`, 0},
		{"bash que no escribe en git", `{"tool_name":"Bash","tool_input":{"command":"echo '` + quoted + `'"}}`, 0},
		{"shell de otro IDE", `{"command":"gh pr create --title x --body \"` + quoted + `\""}`, 2},
		{"herramienta MCP de PR", `{"tool_name":"mcp__github__create_pull_request","tool_input":{"title":"x","body":"` + aiFooter + `"}}`, 2},
		{"herramienta MCP ajena", `{"tool_name":"mcp__docs__search","tool_input":{"q":"` + aiFooter + `"}}`, 0},
		{"entrada no JSON con commit", "git commit -m 'feat: x' -m '" + aiTrailer + "'", 2},
		{"entrada vacía", "", 0},
		{"continuación de línea", `{"tool_name":"Bash","tool_input":{"command":"git -C r \\\ncommit -m \"feat: x\" -m \"` + quoted + `\""}}`, 2},
		{"MCP que escribe un archivo", `{"tool_name":"mcp__github__create_or_update_file","tool_input":{"path":"a.md","message":"docs: x\n\n` + quoted + `"}}`, 2},
		{"MCP que crea un issue", `{"tool_name":"mcp__github__create_issue","tool_input":{"title":"x","body":"` + aiFooter + `"}}`, 2},
		{"Copilot con toolArgs en texto", `{"toolName":"bash","toolArgs":"{\"command\":\"git commit -m \\\"feat: x\\\" -m \\\"` + strings.ReplaceAll(quoted, `\"`, `\\\"`) + `\\\"\"}"}`, 2},
		{"comando como lista", `{"tool_name":"shell","tool_input":{"command":["git","commit","-m","` + quoted + `"]}}`, 2},
		{"--author de una herramienta", `{"tool_name":"Bash","tool_input":{"command":"git commit --author=\"Cursor Agent <cursoragent@` + `cursor.com>\" -m \"feat: x\""}}`, 2},
		{"-c user.email de una herramienta", `{"tool_name":"Bash","tool_input":{"command":"git -c user.name=Copilot -c user.email=1+Copilot@users.noreply.github.com commit -m \"feat: x\""}}`, 2},
		{"variables de autor", `{"tool_name":"Bash","tool_input":{"command":"GIT_AUTHOR_NAME=x GIT_AUTHOR_EMAIL=cursoragent@` + `cursor.com git commit -m \"feat: x\""}}`, 2},
		{"gh api de lectura", `{"tool_name":"Bash","tool_input":{"command":"gh api repos/a/b/commits | grep -c noreply@` + `anthropic.com"}}`, 0},
		{"gh api que escribe", `{"tool_name":"Bash","tool_input":{"command":"gh api -X POST repos/a/b/issues -f body='` + aiFooter + `'"}}`, 2},
		{"MCP de lectura", `{"tool_name":"mcp__github__list_commits","tool_input":{"author":"noreply@` + `anthropic.com"}}`, 0},
		{"MCP de PR con command", `{"tool_name":"mcp__github__create_pull_request","tool_input":{"command":"npx server","body":"` + aiFooter + `"}}`, 2},
		{"continuación dentro de una palabra", `{"tool_name":"Bash","tool_input":{"command":"git com\\\nmit -m \"feat: x\" -m \"Co-Authored-By: Cla\\\nude <noreply@anthr\\\nopic.com>\""}}`, 2},
		{"push con descripción de MR", `{"tool_name":"Bash","tool_input":{"command":"git push -o merge_request.description='` + aiFooter + `' origin rama"}}`, 2},
		{"git log de lectura", `{"tool_name":"Bash","tool_input":{"command":"git log --grep=merge --author=noreply@` + `anthropic.com"}}`, 0},
		{"git notes show", `{"tool_name":"Bash","tool_input":{"command":"git notes show HEAD | grep noreply@` + `anthropic.com"}}`, 0},
		{"git con opciones globales", `{"tool_name":"Bash","tool_input":{"command":"git --no-pager -C repo -c core.editor=true commit -m \"feat: x\" -m \"` + quoted + `\""}}`, 2},
		{"persona llamada Claude Monet", `{"tool_name":"Bash","tool_input":{"command":"git commit -m \"docs: add painting made by Claude Monet\""}}`, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := run(t, dir, c.input, "gate", "attribution")
			if r.code != c.want {
				t.Fatalf("código %d, se esperaba %d\nstderr: %s", r.code, c.want, r.stderr)
			}
		})
	}
}

func TestComandoDesconocido(t *testing.T) {
	r := run(t, t.TempDir(), "", "nope")
	if r.code != 2 {
		t.Fatalf("código %d, se esperaba 2", r.code)
	}
	if r := run(t, t.TempDir(), "", "help"); r.code != 0 || !strings.Contains(r.stdout, "commit") {
		t.Fatalf("help: %d\n%s", r.code, r.stdout)
	}
	if r := run(t, t.TempDir(), "", "status"); r.code != 1 || !strings.Contains(r.stderr, "coyote init") {
		t.Fatalf("status fuera de un proyecto: %d\n%s", r.code, r.stderr)
	}
}

func TestParseConventional(t *testing.T) {
	cases := []struct{ in, typ, scope, desc string }{
		{"feat(pedidos): alta", "feat", "pedidos", "alta"},
		{"docs: guía", "doc", "-", "guía"},
		{"fix(api)!: rompe", "fix", "api", "rompe"},
		{"revert: algo", "fix", "-", "algo"},
		{"wip(x y): algo", "chore", "x-y", "algo"},
		{"sin formato", "chore", "-", "sin formato"},
	}
	for _, c := range cases {
		typ, scope, desc := parseConventional(c.in)
		if typ != c.typ || scope != c.scope || desc != c.desc {
			t.Errorf("%q: %s|%s|%s, se esperaba %s|%s|%s", c.in, typ, scope, desc, c.typ, c.scope, c.desc)
		}
	}
	if got := ledgerID("  ámbito con espacios|y pipes  "); got != "ámbito-con-espacios-y-pipes" {
		t.Errorf("ledgerID: %q", got)
	}
}

func TestGateArchivoDeMensaje(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "body.md"), []byte("Resumen.\n\n"+aiFooter+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	in := `{"cwd":"` + dir + `","tool_name":"Bash","tool_input":{"command":"gh pr create --title x --body-file body.md"}}`
	if r := run(t, dir, in, "gate", "attribution"); r.code != 2 {
		t.Fatalf("--body-file con pie de IA debe bloquearse: %d %s", r.code, r.stderr)
	}
	in = `{"cwd":"` + dir + `","tool_name":"Bash","tool_input":{"command":"git commit -F body.md"}}`
	if r := run(t, dir, in, "gate", "attribution"); r.code != 2 {
		t.Fatalf("git commit -F con pie de IA debe bloquearse: %d %s", r.code, r.stderr)
	}
	in = `{"cwd":"` + dir + `","tool_name":"Bash","tool_input":{"command":"git commit -Fbody.md"}}`
	if r := run(t, dir, in, "gate", "attribution"); r.code != 2 {
		t.Fatalf("git commit -Ffile con pie de IA debe bloquearse: %d %s", r.code, r.stderr)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "m.txt"), []byte("feat: x\n\n"+aiTrailer+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	in = `{"cwd":"` + dir + `","tool_name":"Bash","tool_input":{"command":"cd sub && git commit -F m.txt"}}`
	if r := run(t, dir, in, "gate", "attribution"); r.code != 2 {
		t.Fatalf("cd sub && git commit -F m.txt debe bloquearse: %d %s", r.code, r.stderr)
	}
}

func TestEstandarDesprendido(t *testing.T) {
	base := setup(t)
	root := filepath.Join(base, "suelto")
	must(t, run(t, base, "", "init", "suelto", "--type", "library", "--purpose", "biblioteca de prueba"), 0, "init")
	rules := filepath.Join(root, "coyote", "standards", "rules.yaml")
	if err := os.WriteFile(rules, []byte("version: 1\nextends: none\nrules: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := run(t, root, "", "standards", "lint")
	must(t, r, 1, "extends: none sin reason")
	if !strings.Contains(r.stdout, "S0") {
		t.Errorf("falta el hallazgo S0:\n%s", r.stdout)
	}
	if r := run(t, root, "", "standards", "diff"); !strings.Contains(r.stdout, "FUERA") {
		t.Errorf("diff debe listar las reglas que quedaron fuera:\n%s", r.stdout)
	}
	if err := os.WriteFile(rules, []byte("version: 1\nextends: none\nreason: prueba de un estándar propio completo\nrules: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	must(t, run(t, root, "", "standards", "lint"), 0, "extends: none con reason")
	if err := os.WriteFile(rules, []byte("version: 1\nrules:\n  - id: R10\n    Override: { level: MAY }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	must(t, run(t, root, "", "standards", "lint"), 1, "clave desconocida en rules.yaml")
}

func TestHistorialConMerges(t *testing.T) {
	base := setup(t)
	root := filepath.Join(base, "m")
	must(t, run(t, base, "", "init", "m", "--type", "library", "--purpose", "prueba de merges"), 0, "init")
	git(t, root, "add", "-A")
	must(t, run(t, root, "", "commit", "-m", "chore: adopta coyote"), 0, "primer commit")
	git(t, root, "checkout", "-q", "-b", "rama")
	if err := os.WriteFile(filepath.Join(root, "x.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "x.txt")
	git(t, root, "commit", "-q", "--no-verify", "-m", "feat: x")
	git(t, root, "checkout", "-q", "main")
	git(t, root, "merge", "-q", "--no-ff", "--no-verify", "rama", "-m", "Merge rama", "-m", aiFooter)
	r := run(t, root, "", "attribution", "check", "--commits", "10")
	must(t, r, 1, "merge con pie de IA")
}

func TestHookAjenoYCompartido(t *testing.T) {
	base := setup(t)
	root := filepath.Join(base, "h")
	must(t, run(t, base, "", "init", "h", "--type", "library", "--purpose", "prueba de hooks", "--no-hooks"), 0, "init")
	hook := filepath.Join(root, ".git", "hooks", "commit-msg")
	own := "#!/bin/sh\n# revisa el ticket\nexit 0\n"
	if err := os.WriteFile(hook, []byte(own), 0o755); err != nil {
		t.Fatal(err)
	}
	must(t, run(t, root, "", "hooks", "install"), 1, "hook ajeno")
	if readFile(t, hook) != own {
		t.Fatal("se pisó un hook ajeno")
	}
	shared := filepath.Join(base, "hooks-globales")
	git(t, root, "config", "core.hooksPath", shared)
	must(t, run(t, root, "", "hooks", "install"), 1, "core.hooksPath fuera del repo")
	if _, err := os.Stat(filepath.Join(shared, "commit-msg")); err == nil {
		t.Fatal("se instaló un hook en un directorio compartido")
	}
}

func TestInitNoSigueSymlinks(t *testing.T) {
	base := setup(t)
	root := filepath.Join(base, "s")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "fuera.md")
	if err := os.Symlink(outside, filepath.Join(root, "README.md")); err != nil {
		t.Fatal(err)
	}
	must(t, run(t, base, "", "init", "s", "--purpose", "prueba de symlinks"), 0, "init")
	if _, err := os.Stat(outside); err == nil {
		t.Fatal("init escribió a través de un symlink")
	}
}

func TestHistorialNoSeReinicia(t *testing.T) {
	base := setup(t)
	root := filepath.Join(base, "r")
	must(t, run(t, base, "", "init", "r", "--type", "library", "--purpose", "prueba de historial"), 0, "init")
	git(t, root, "add", "-A")
	must(t, run(t, root, "", "commit", "-m", "chore: adopta coyote"), 0, "primer commit")
	// Commits con atribución que saltan los hooks, uno con un separador en el asunto.
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "a.txt")
	git(t, root, "commit", "-q", "--no-verify", "-m", "feat: z\x1e", "-m", aiTrailer)
	must(t, run(t, root, "", "attribution", "check", "--commits", "20"), 1, "trailer con separador en el asunto")
	// Borrar y volver a agregar coyote/project.yaml no reinicia el historial revisado.
	cfgPath := filepath.Join(root, "coyote", "project.yaml")
	saved := readFile(t, cfgPath)
	git(t, root, "rm", "-q", "coyote/project.yaml")
	git(t, root, "commit", "-q", "--no-verify", "-m", "chore: quita project")
	if err := os.WriteFile(cfgPath, []byte(saved), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "coyote/project.yaml")
	git(t, root, "commit", "-q", "--no-verify", "-m", "chore: vuelve project")
	must(t, run(t, root, "", "attribution", "check", "--commits", "20"), 1, "historial tras borrar y agregar project.yaml")
}

func TestHookModoIdentidadYTijeras(t *testing.T) {
	base := setup(t)
	root := filepath.Join(base, "k")
	must(t, run(t, base, "", "init", "k", "--type", "library", "--purpose", "prueba del modo hook"), 0, "init")
	msg := filepath.Join(root, ".git", "COMMIT_EDITMSG")
	body := "feat: x\n\n# ------------------------ >8 ------------------------\n-Esta guía fue escrita con " + "Cla" + "ude Code.\n"
	if err := os.WriteFile(msg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	must(t, run(t, root, "", "attribution", "scrub", "--in-place", "--commit-msg", msg), 0, "el diff de commit -v no se revisa")
	t.Setenv("GIT_AUTHOR_NAME", "claude[bot]")
	t.Setenv("GIT_AUTHOR_EMAIL", "209825114+claude[bot]@users.noreply.github.com")
	must(t, run(t, root, "", "attribution", "scrub", "--in-place", "--commit-msg", msg), 1, "autor de IA en modo hook")
}

func TestHookEditadoNoSePisa(t *testing.T) {
	base := setup(t)
	root := filepath.Join(base, "e")
	must(t, run(t, base, "", "init", "e", "--type", "library", "--purpose", "prueba de hook editado"), 0, "init")
	hook := filepath.Join(root, ".git", "hooks", "commit-msg")
	edited := readFile(t, hook) + "# revisión del ticket\n"
	if err := os.WriteFile(hook, []byte(edited), 0o755); err != nil {
		t.Fatal(err)
	}
	must(t, run(t, root, "", "hooks", "install"), 1, "hook de coyote con cambios")
	if readFile(t, hook) != edited {
		t.Fatal("se pisó un hook con cambios de la persona")
	}
	must(t, run(t, root, "", "hooks", "install", "--force"), 0, "reemplazo explícito")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\n# TODO: coyote attribution scrub\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if r := run(t, root, "", "doctor"); !strings.Contains(r.stdout, "no instalado") {
		t.Errorf("un comentario no cuenta como hook instalado:\n%s", r.stdout)
	}
}

func TestNoEscribeFueraDelProyecto(t *testing.T) {
	base := setup(t)
	root := filepath.Join(base, "q")
	outside := filepath.Join(base, "fuera")
	for _, d := range []string{root, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(root, ".claude")); err != nil {
		t.Fatal(err)
	}
	must(t, run(t, base, "", "init", "q", "--purpose", "prueba de symlinks"), 0, "init")
	if _, err := os.Stat(filepath.Join(outside, "settings.json")); err == nil {
		t.Fatal("init escribió settings.json a través de un directorio symlink")
	}
	must(t, run(t, root, "", "note", "algo", "--type", "dec", "--file", "../fuera/x.md"), 1, "note fuera del proyecto")
	ledgerDir := filepath.Join(root, "coyote", "ledger")
	if err := os.RemoveAll(ledgerDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, ledgerDir); err != nil {
		t.Fatal(err)
	}
	must(t, run(t, root, "", "record", "note", "prueba"), 1, "ledger con symlink")
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("se escribió fuera del proyecto: %v", entries)
	}
}

func TestContextoYPreguntas(t *testing.T) {
	base := setup(t)
	root := filepath.Join(base, "tienda")
	must(t, run(t, base, "", "init", "tienda", "--type", "backend", "--purpose", "API de pedidos y pagos"), 0, "init")
	must(t, run(t, root, "", "note", "un pedido se confirma solo con un pago capturado", "--type", "inv", "--scope", "pagos"), 0, "note pagos")
	must(t, run(t, root, "", "note", "un envío no sale sin dirección validada", "--type", "inv", "--scope", "envios"), 0, "note envios")

	r := run(t, root, "", "get", "context", "--scope", "pagos", "--budget", "600")
	must(t, r, 0, "get context")
	if !strings.Contains(r.stdout, "pago capturado") || strings.Contains(r.stdout, "dirección validada") {
		t.Errorf("paquete de pagos inesperado:\n%s", r.stdout)
	}
	r = run(t, root, "", "get", "context", "--format", "ccf")
	must(t, r, 0, "get context ccf")
	if !strings.HasPrefix(r.stdout, "# contexto|tienda|") {
		t.Errorf("formato CCF inesperado:\n%s", r.stdout)
	}
	must(t, run(t, root, "", "get", "context", "--budget", "10"), 2, "presupuesto demasiado chico")
	must(t, run(t, root, "", "get", "context", "otro"), 1, "repo desconocido")

	r = run(t, root, "", "ask", "cuándo se confirma un pedido", "--record")
	must(t, r, 0, "ask")
	if !strings.Contains(r.stdout, "pago capturado") || !strings.Contains(r.stdout, "CONTEXT.coyote.md#L") {
		t.Errorf("ask sin la invariante o sin referencia:\n%s", r.stdout)
	}
	if !strings.Contains(ledgerText(t, root), "|ask|-|cuándo se confirma un pedido|doc:CONTEXT.coyote.md#L") {
		t.Errorf("ask --record no quedó en el ledger:\n%s", ledgerText(t, root))
	}
	r = run(t, root, "", "ask", "xyzzy inexistente")
	must(t, r, 0, "ask sin resultados")
	if !strings.Contains(r.stdout, "sin resultados") {
		t.Errorf("se esperaba 'sin resultados':\n%s", r.stdout)
	}
	must(t, run(t, root, "", "index", "--rebuild"), 0, "index")
	if _, err := os.Stat(filepath.Join(root, ".coyote", "index.json")); err != nil {
		t.Error("no se escribió la caché del índice")
	}
	if st := git(t, root, "status", "--porcelain", "--", ".coyote"); st != "" {
		t.Errorf(".coyote/ debe estar ignorado por git:\n%s", st)
	}
}

func TestContextoDeOtroRepo(t *testing.T) {
	base := setup(t)
	// Repo remoto (bare) con documentos coyote y código que no debe bajarse.
	src := filepath.Join(base, "pagos-src")
	must(t, run(t, base, "", "init", "pagos-src", "--type", "backend", "--purpose", "servicio de pagos"), 0, "init remoto")
	must(t, run(t, src, "", "note", "todo cobro es idempotente por clave", "--type", "inv", "--scope", "cobros"), 0, "note remoto")
	if err := os.MkdirAll(filepath.Join(src, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "src", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, src, "add", "-A")
	must(t, run(t, src, "", "commit", "-m", "feat: servicio de pagos"), 0, "commit remoto")
	bare := filepath.Join(base, "pagos.git")
	git(t, base, "clone", "-q", "--bare", src, bare)

	root := filepath.Join(base, "hub")
	must(t, run(t, base, "", "init", "hub", "--type", "hub", "--purpose", "proyecto multi-repo"), 0, "init hub")
	must(t, run(t, root, "", "repo", "add", "pagos", "file://"+bare), 0, "repo add")
	must(t, run(t, root, "", "repo", "add", "malo", "https://user:tok@example.com/x.git"), 1, "URL con credenciales")
	must(t, run(t, root, "", "repo", "add", "malo", "--upload-pack=touch x"), 2, "URL que parece opción")
	cfg := readFile(t, filepath.Join(root, "coyote", "project.yaml"))
	if !strings.Contains(cfg, "name: pagos") || !strings.Contains(cfg, "# manual | supervised | autonomous") {
		t.Errorf("project.yaml sin el repo o sin sus comentarios:\n%s", cfg)
	}
	r := run(t, root, "", "get", "context", "pagos")
	must(t, r, 0, "get context de otro repo")
	if !strings.Contains(r.stdout, "idempotente por clave") || !strings.Contains(r.stdout, "# Contexto: pagos") {
		t.Errorf("contexto remoto inesperado:\n%s", r.stdout)
	}
	docs := filepath.Join(root, ".coyote", "repos", "pagos")
	if _, err := os.Stat(filepath.Join(docs, "src", "main.go")); err == nil {
		t.Fatal("se bajó código del repo; solo deben venir sus documentos")
	}
	if _, err := os.Stat(filepath.Join(docs, "CONTEXT.coyote.md")); err != nil {
		t.Fatal("no se trajo CONTEXT.coyote.md")
	}
	r = run(t, root, "", "repo", "list")
	must(t, r, 0, "repo list")
	if !strings.Contains(r.stdout, "traídos") {
		t.Errorf("repo list sin estado:\n%s", r.stdout)
	}
	must(t, run(t, root, "", "ask", "cobro idempotente", "--repo", "pagos"), 0, "ask a otro repo")
}

func noPace(t *testing.T) {
	t.Helper()
	old := paceSleep
	paceSleep = func(time.Duration) {}
	t.Cleanup(func() { paceSleep = old })
	t.Setenv("COYOTE_STATE_DIR", t.TempDir())
}

func TestPushPull(t *testing.T) {
	base := setup(t)
	noPace(t)
	bare := filepath.Join(base, "equipo.git")
	git(t, base, "init", "-q", "--bare", "-b", "main", bare)
	ana := filepath.Join(base, "equipo")
	must(t, run(t, base, "", "init", "equipo", "--type", "backend", "--purpose", "servicio del equipo"), 0, "init")
	git(t, ana, "add", "-A")
	must(t, run(t, ana, "", "commit", "-m", "chore: adopta coyote"), 0, "primer commit")
	git(t, ana, "remote", "add", "origin", "file://"+bare)
	r := run(t, ana, "", "push", "--dry-run")
	must(t, r, 0, "push --dry-run")
	if _, err := exec.Command("git", "-C", bare, "rev-parse", "--verify", "-q", "refs/heads/main").Output(); err == nil {
		t.Fatal("--dry-run publicó")
	}
	must(t, run(t, ana, "", "push"), 0, "push")
	if out := git(t, bare, "log", "--format=%an", "-1", "main"); strings.TrimSpace(out) != "Ana Pérez" {
		t.Fatalf("el remoto no tiene el commit de Ana: %q", out)
	}
	if !strings.Contains(ledgerText(t, ana), "|sync|-|push de 1 commits a origin/main|sha:") {
		t.Errorf("push no quedó en el ledger:\n%s", ledgerText(t, ana))
	}
	must(t, run(t, ana, "", "push"), 0, "push sin nada nuevo")

	// Un agente no publica en main.
	must(t, run(t, ana, "", "note", "los cobros son idempotentes", "--type", "inv", "--scope", "cobros"), 0, "note")
	git(t, ana, "add", "-A")
	must(t, run(t, ana, "", "commit", "-m", "docs(cobros): invariante de idempotencia"), 0, "commit")
	must(t, run(t, ana, "", "push", "--agent", "coyote-dev"), 1, "agente en main")

	// Luis clona y trae lo nuevo de Ana.
	luis := filepath.Join(base, "luis")
	git(t, base, "clone", "-q", "file://"+bare, luis)
	git(t, luis, "config", "user.name", "Luis Gómez")
	git(t, luis, "config", "user.email", "luis@example.com")
	must(t, run(t, ana, "", "push"), 0, "push de Ana")
	t.Setenv("COYOTE_USER", "luis")
	r = run(t, luis, "", "pull")
	must(t, r, 0, "pull")
	if !strings.Contains(r.stdout, "commits nuevos de Ana Pérez") || !strings.Contains(r.stdout, "eventos nuevos en el ledger") ||
		!strings.Contains(r.stdout, "CONTEXT.coyote.md") {
		t.Errorf("resumen de pull inesperado:\n%s", r.stdout)
	}
	if !strings.Contains(ledgerText(t, luis), "|@luis|-|equipo|sync|-|pull de") {
		t.Errorf("pull no quedó en el ledger de Luis")
	}
	must(t, run(t, luis, "", "pull"), 0, "pull sin cambios")

	// Commits hechos a mano que saltan los hooks no salen: atribución y formato.
	t.Setenv("COYOTE_USER", "ana")
	if err := os.WriteFile(filepath.Join(ana, "x.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, ana, "add", "x.txt")
	git(t, ana, "commit", "-q", "--no-verify", "-m", "feat: x", "-m", aiTrailer)
	must(t, run(t, ana, "", "push"), 1, "push con atribución")
	git(t, ana, "reset", "-q", "--soft", "HEAD~1")
	git(t, ana, "commit", "-q", "--no-verify", "-m", "cambios varios")
	must(t, run(t, ana, "", "push"), 1, "push fuera de formato")
}

func TestAuthStatusYLogin(t *testing.T) {
	setup(t)
	noPace(t)
	dir := t.TempDir()
	tok := "gho_0123456789abcdefghijklmnop"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+tok {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"message":"Bad credentials"}`))
			return
		}
		w.Header().Set("X-OAuth-Scopes", "repo")
		w.Write([]byte(`{"login":"ana"}`))
	}))
	defer srv.Close()
	t.Setenv("COYOTE_GITHUB_API", srv.URL)
	for _, e := range []string{"COYOTE_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"} {
		t.Setenv(e, "")
	}
	oldRun, oldLook := auth.Runner, auth.LookPath
	defer func() { auth.Runner, auth.LookPath = oldRun, oldLook }()
	var calls []string
	stored := ""
	auth.LookPath = func(name string) (string, error) {
		if name == "gh" {
			return "", errors.New("no")
		}
		return "/usr/bin/" + name, nil
	}
	auth.Runner = func(name string, args []string, stdin string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if len(args) > 0 && (args[0] == "store" || args[0] == "-i") {
			stored = stdin
			return "", nil
		}
		if stored != "" && (args[0] == "lookup" || args[0] == "find-generic-password") {
			return tok, nil
		}
		return "", errors.New("not found")
	}
	must(t, run(t, dir, "", "auth", "status"), 1, "sin token")
	must(t, run(t, dir, "no es token\n", "auth", "login", "--with-token"), 1, "token inválido")
	must(t, run(t, dir, tok+"\n", "auth", "login", "--with-token"), 0, "login")
	if !strings.Contains(stored, tok) || strings.Contains(strings.Join(calls, " "), tok) {
		t.Fatalf("el token debe ir por la entrada estándar del llavero, no en argumentos: %v", calls)
	}
	r := run(t, dir, "", "auth", "status", "--check")
	must(t, r, 0, "status --check")
	if !strings.Contains(r.stdout, "gho_…mnop") || !strings.Contains(r.stdout, "@ana") || strings.Contains(r.stdout, tok) {
		t.Fatalf("status inesperado o con el token completo:\n%s", r.stdout)
	}
	must(t, run(t, dir, "", "auth", "login", "--device"), 1, "device flow sin client ID")
}

func TestWeb(t *testing.T) {
	base := setup(t)
	root := filepath.Join(base, "w")
	must(t, run(t, base, "", "init", "w", "--purpose", "prueba web"), 0, "init")
	must(t, run(t, root, "", "record", "run", "implementa pedidos", "--agent", "coyote-dev", "--tokens", "12k/8k/1k", "--cost", "0.01+0.02", "--refs", "model:sonnet-5"), 0, "record")
	must(t, run(t, root, "", "web", "--addr", "0.0.0.0:7410"), 2, "web fuera de loopback")
	old := webServe
	defer func() { webServe = old }()
	var body string
	webServe = func(srv *http.Server, ln net.Listener) error {
		defer ln.Close()
		req := httptest.NewRequest(http.MethodGet, "/?since=all", nil)
		req.Host = "127.0.0.1"
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, req)
		body = rec.Body.String()
		return nil
	}
	must(t, run(t, root, "", "web", "--addr", "127.0.0.1:0"), 0, "web")
	for _, want := range []string{"$0.03", "sonnet-5", "@ana", "12k"} {
		if !strings.Contains(body, want) {
			t.Errorf("falta %q en la página:\n%s", want, body)
		}
	}
}

func TestPushRevisaTodoLoQueSale(t *testing.T) {
	base := setup(t)
	noPace(t)
	bare := filepath.Join(base, "r.git")
	git(t, base, "init", "-q", "--bare", "-b", "main", bare)
	root := filepath.Join(base, "r")
	must(t, run(t, base, "", "init", "r", "--type", "library", "--purpose", "prueba de push"), 0, "init")
	git(t, root, "add", "-A")
	must(t, run(t, root, "", "commit", "-m", "chore: adopta coyote"), 0, "commit")
	git(t, root, "remote", "add", "origin", "file://"+bare)
	must(t, run(t, root, "", "push"), 0, "push inicial")
	git(t, root, "checkout", "-q", "-b", "ws/tarea")
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "a.txt")
	git(t, root, "commit", "-q", "--no-verify", "-m", "feat: a", "-m", aiTrailer)
	// Un upstream local que ya contiene el commit no lo esconde: el remoto no lo tiene.
	git(t, root, "branch", "base2")
	git(t, root, "branch", "-u", "base2")
	must(t, run(t, root, "", "push", "--agent", "coyote-dev"), 1, "push con atribución escondida por el upstream")
	// Un agente no publica en Main aunque cambie mayúsculas.
	git(t, root, "reset", "-q", "--hard", "main")
	git(t, root, "checkout", "-q", "-b", "Main")
	must(t, run(t, root, "", "push", "--agent", "coyote-dev"), 1, "agente en Main")
}

func TestReferenciasConEspaciosRaros(t *testing.T) {
	base := setup(t)
	root := filepath.Join(base, "z")
	must(t, run(t, base, "", "init", "z", "--purpose", "prueba de referencias"), 0, "init")
	adr := filepath.Join(root, "coyote", "decisions", "ADR-1 model:gratis.md")
	if err := os.WriteFile(adr, []byte("# ADR-1: pagos idempotentes\n\n## Decisión\nlos pagos son idempotentes por clave\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	must(t, run(t, root, "", "ask", "pagos idempotentes", "--record"), 0, "ask --record")
	if strings.Contains(ledgerText(t, root), " model:gratis") {
		t.Fatalf("una referencia se partió en dos por un espacio raro:\n%s", ledgerText(t, root))
	}
	// index --rebuild no borra fuera del proyecto a través de un symlink.
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "index.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.RemoveAll(filepath.Join(root, ".coyote"))
	if err := os.Symlink(outside, filepath.Join(root, ".coyote")); err != nil {
		t.Fatal(err)
	}
	must(t, run(t, root, "", "index", "--rebuild"), 1, "index --rebuild con .coyote symlink")
	if _, err := os.Stat(filepath.Join(outside, "index.json")); err != nil {
		t.Fatal("se borró un archivo fuera del proyecto")
	}
}
