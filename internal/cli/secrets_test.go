package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Secretos de prueba armados por partes (R18 del propio repo).
var (
	cliAWS    = "AKIA" + "Q3VZ7T2M9KX4B8JN"
	cliGitHub = "ghp_" + strings.Repeat("a1B2", 9)
)

func TestSecretsListYScan(t *testing.T) {
	_, root := gateProject(t)
	write(t, root, ".gitignore", ".env\n*.tfstate\n.coyote/\n")
	write(t, root, ".env", "DB_HOST=db\nDB_PASSWORD=Sup3rS3creta\nTOKEN="+cliGitHub+"\n")
	write(t, root, "infra/terraform.tfstate", `{"version":4,"outputs":{"db_ip":{"value":"10.1.1.1"}}}`)

	l := run(t, root, "", "secrets", "list")
	must(t, l, 0, "secrets list")
	for _, want := range []string{".env", "variables de entorno", "DB_HOST, DB_PASSWORD, TOKEN", "infra/terraform.tfstate", "outputs.db_ip"} {
		if !strings.Contains(l.stdout, want) {
			t.Errorf("secrets list sin %q:\n%s", want, l.stdout)
		}
	}
	for _, leaked := range []string{"Sup3rS3creta", "a1B2", "10.1.1.1"} {
		if strings.Contains(l.stdout, leaked) {
			t.Fatalf("secrets list mostró un valor (%s):\n%s", leaked, l.stdout)
		}
	}
	must(t, run(t, root, "", "secrets", "list", "--json"), 0, "secrets list --json")
	if nn := run(t, root, "", "secrets", "list", "--no-names"); !strings.Contains(nn.stdout, ".env") || strings.Contains(nn.stdout, "DB_HOST") {
		t.Errorf("--no-names no abre los archivos:\n%s", nn.stdout)
	}

	// Sin secretos versionados, scan pasa.
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "inicio", "--no-verify")
	must(t, run(t, root, "", "secrets", "scan"), 0, "scan limpio")

	// Un secreto escrito en un archivo versionado.
	write(t, root, "src/config.go", "package src\n\nconst clave = \""+cliAWS+"\"\n")
	git(t, root, "add", "src/config.go")
	s := run(t, root, "", "secrets", "scan", "--staged")
	must(t, s, 1, "scan --staged con secreto")
	if !strings.Contains(s.stdout, "src/config.go:3") || !strings.Contains(s.stdout, "llave de acceso de AWS") || strings.Contains(s.stdout, "Q3VZ") {
		t.Errorf("scan --staged:\n%s", s.stdout)
	}
	// coyote commit no deja pasar el secreto, ni con --no-verify.
	c := run(t, root, "", "commit", "-m", "feat(src): configuración")
	must(t, c, 1, "commit con secreto")
	if !strings.Contains(c.stderr, "R18") || strings.Contains(c.stderr+c.stdout, "Q3VZ") {
		t.Errorf("commit con secreto:\n%s\n%s", c.stdout, c.stderr)
	}
	must(t, run(t, root, "", "commit", "--no-verify", "-m", "feat(src): configuración"), 1, "commit --no-verify con secreto")
	// Marcado como dato de prueba, pasa.
	write(t, root, "src/config.go", "package src\n\nconst clave = \""+cliAWS+"\" // coyote:allow-secret\n")
	git(t, root, "add", "src/config.go")
	must(t, run(t, root, "", "secrets", "scan", "--staged"), 0, "secreto marcado")
	must(t, run(t, root, "", "commit", "-m", "feat(src): configuración de prueba"), 0, "commit con secreto marcado")

	// Un archivo de secretos versionado lo detecta scan y el lint (R18, MUST).
	git(t, root, "add", "-f", ".env")
	git(t, root, "commit", "-qm", "oops", "--no-verify")
	s = run(t, root, "", "secrets", "scan")
	must(t, s, 1, "scan con .env versionado")
	if !strings.Contains(s.stdout, ".env") || !strings.Contains(s.stdout, "archivo de secretos") || strings.Contains(s.stdout, "Sup3rS3creta") {
		t.Errorf("scan:\n%s", s.stdout)
	}
	lint := run(t, root, "", "standards", "lint")
	if !strings.Contains(lint.stdout, "R18") || !strings.Contains(lint.stdout, "archivo de secretos en el repo") {
		t.Errorf("lint sin R18:\n%s", lint.stdout)
	}
	// Una dispensa con motivo en project.yaml lo quita.
	cfg := readFile(t, filepath.Join(root, "coyote", "project.yaml"))
	write(t, root, "coyote/project.yaml", cfg+"secrets:\n  allow:\n    - { path: .env, reason: variables de ejemplo sin valores reales }\n")
	must(t, run(t, root, "", "secrets", "scan"), 0, "scan con dispensa")
	write(t, root, "coyote/project.yaml", cfg+"secrets:\n  allow:\n    - { path: .env, reason: todo }\n")
	must(t, run(t, root, "", "secrets", "scan"), 1, "una dispensa sin motivo no vale")
}

func TestGatePRSecretos(t *testing.T) {
	base := setup(t)
	productoDemo(t, base)
	svc := filepath.Join(base, "servicios")
	baseSHA := strings.TrimSpace(git(t, svc, "rev-parse", "HEAD"))
	write(t, svc, "services/pedidos-service/src/main/resources/claves.properties", "github.token="+cliGitHub+"\n")
	write(t, svc, "deploy/prod.env", "DB_PASSWORD=x\n")
	git(t, svc, "add", "-A")
	git(t, svc, "commit", "-qm", "configuración")
	headSHA := strings.TrimSpace(git(t, svc, "rev-parse", "HEAD"))
	r := run(t, base, "", "gate", "pr", "--repo", "servicios="+svc, "--self", "servicios", "--base", baseSHA, "--head", headSHA, "--event", "", "--policy", "fail")
	must(t, r, 1, "gate pr con secretos")
	for _, want := range []string{"### coyote: R3, el cambio agrega secretos", "**Secretos (R18).**", "claves.properties", "token de GitHub",
		"deploy/prod.env", "archivo de secretos", "ninguna aprobación lo deja pasar"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("reporte sin %q:\n%s", want, r.stdout)
		}
	}
	if strings.Contains(r.stdout, "a1B2") || !strings.Contains(r.stderr, "R18") {
		t.Errorf("el reporte no debe mostrar el valor:\n%s\n%s", r.stdout, r.stderr)
	}
	must(t, run(t, base, "", "gate", "pr", "--repo", "servicios="+svc, "--self", "servicios", "--base", baseSHA, "--head", headSHA, "--event", "", "--policy", "warn"), 0, "warn")
	if st := git(t, svc, "status", "--porcelain", "--ignored"); st != "" {
		t.Errorf("el repo cambió: %s", st)
	}
}

func TestSecretosEnProyectoDeSubcarpeta(t *testing.T) {
	base := setup(t)
	t.Setenv("COYOTE_STATE_DIR", filepath.Join(base, "state"))
	old := isTerminal
	isTerminal = func() bool { return true }
	t.Cleanup(func() { isTerminal = old })
	mono := filepath.Join(base, "mono")
	if err := os.MkdirAll(mono, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, mono, "init", "-q")
	must(t, run(t, mono, "", "init", "demo", "--type", "backend", "--purpose", "API de pedidos de la tienda demo"), 0, "init")
	root := filepath.Join(mono, "demo")
	cfg := readFile(t, filepath.Join(root, "coyote", "project.yaml"))
	write(t, root, "coyote/project.yaml", cfg+"secrets:\n  files: [\"conf/*.conf\"]\n")
	write(t, root, "conf/prod.conf", "db.password=corta\n")
	git(t, mono, "add", "-A")
	s := run(t, root, "", "secrets", "scan", "--staged")
	must(t, s, 1, "scan --staged en un proyecto de subcarpeta")
	if !strings.Contains(s.stdout, "demo/conf/prod.conf") || !strings.Contains(s.stdout, "secretos del proyecto") {
		t.Errorf("scan --staged:\n%s", s.stdout)
	}
	// El hook commit-msg corre en la raíz del repo: las reglas del proyecto de
	// la subcarpeta aplican igual.
	if s := run(t, mono, "", "secrets", "scan", "--staged"); s.code != 1 || !strings.Contains(s.stdout, "demo/conf/prod.conf") {
		t.Errorf("scan --staged desde la raíz del repo:\n%s", s.stdout)
	}
	for _, args := range [][]string{{"commit", "-m", "feat(conf): configuración de producción"}, {"commit", "-a", "-m", "feat(conf): configuración de producción"}} {
		c := run(t, root, "", args...)
		if c.code != 1 || !strings.Contains(c.stderr, "R18") || !strings.Contains(c.stderr, "demo/conf/prod.conf") {
			t.Errorf("%v con un secreto del proyecto: %d\n%s\n%s", args, c.code, c.stdout, c.stderr)
		}
	}
}

func TestEscanerNoSeEvade(t *testing.T) {
	_, root := gateProject(t)
	write(t, root, ".gitignore", ".coyote/\n")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "inicio", "--no-verify")
	// Una línea agregada que empieza con "++ " no esconde lo que sigue.
	write(t, root, "README.md", readFile(t, filepath.Join(root, "README.md"))+"x\n++ fin\ntoken = "+cliGitHub+"\n")
	git(t, root, "add", "README.md")
	must(t, run(t, root, "", "secrets", "scan", "--staged"), 1, "scan --staged con ++")
	if c := run(t, root, "", "commit", "-m", "docs(readme): notas"); c.code != 1 || !strings.Contains(c.stderr, "R18") {
		t.Errorf("commit con ++ y un token: %d\n%s", c.code, c.stderr)
	}
	git(t, root, "reset", "-q", "--hard")
	// Una línea larga antes del secreto tampoco.
	write(t, root, "app.min.js", strings.Repeat("a", 100<<10)+"\nconst t = '"+cliGitHub+"';\n")
	git(t, root, "add", "app.min.js")
	if c := run(t, root, "", "commit", "-m", "feat(web): bundle"); c.code != 1 || !strings.Contains(c.stderr, "R18") {
		t.Errorf("commit con línea larga y un token: %d\n%s", c.code, c.stderr)
	}
	git(t, root, "reset", "-q")
	// Un nombre con dos puntos seguidos se revisa.
	write(t, root, "dos..puntos.txt", "clave "+cliAWS+"\n")
	git(t, root, "add", "-f", "dos..puntos.txt")
	git(t, root, "commit", "-qm", "oops", "--no-verify")
	if s := run(t, root, "", "secrets", "scan"); s.code != 1 || !strings.Contains(s.stdout, "dos..puntos.txt:1") {
		t.Errorf("scan con dos..puntos.txt: %d\n%s", s.code, s.stdout)
	}
}

func TestEscanerSegundaRevision(t *testing.T) {
	_, root := gateProject(t)
	write(t, root, ".gitignore", ".coyote/\n")
	header := "-----BEGIN " + "PRIVATE KEY-----"
	body := strings.Repeat("MIIEvQIBADANBgkqhkiG9w0B", 3)
	write(t, root, "deploy/tls.yaml", "key: |\n  "+header+"\n  PEGA_AQUI\n  -----END PRIVATE KEY-----\n")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "inicio", "--no-verify")
	// El cuerpo pegado bajo un encabezado que ya estaba versionado.
	write(t, root, "deploy/tls.yaml", "key: |\n  "+header+"\n  "+body+"\n  -----END PRIVATE KEY-----\n")
	git(t, root, "add", "deploy/tls.yaml")
	if s := run(t, root, "", "secrets", "scan", "--staged"); s.code != 1 || !strings.Contains(s.stdout, "deploy/tls.yaml:3") {
		t.Errorf("el cuerpo bajo un encabezado ya versionado: %d\n%s", s.code, s.stdout)
	}
	if c := run(t, root, "", "commit", "-m", "feat(deploy): llave"); c.code != 1 || !strings.Contains(c.stderr, "R18") {
		t.Errorf("commit con el cuerpo de una llave: %d\n%s", c.code, c.stderr)
	}
	git(t, root, "reset", "-q", "--hard")
	// Un archivo en UTF-16 con BOM (lo que escribe PowerShell 5).
	utf16 := []byte{0xFF, 0xFE}
	for _, r := range "token = " + cliGitHub + "\r\n" {
		utf16 = append(utf16, byte(r), 0)
	}
	if err := os.WriteFile(filepath.Join(root, "config.ps1.txt"), utf16, 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "config.ps1.txt")
	if s := run(t, root, "", "secrets", "scan", "--staged"); s.code != 1 || !strings.Contains(s.stdout, "token de GitHub") {
		t.Errorf("un archivo UTF-16 con un token: %d\n%s", s.code, s.stdout)
	}
	git(t, root, "reset", "-q")
	// La plantilla de un chart de Helm no es un archivo de secretos.
	write(t, root, "chart/templates/secret.yaml", "apiVersion: v1\nkind: Secret\ndata:\n  password: {{ .Values.password | b64enc }}\n")
	write(t, root, "README.md", readFile(t, filepath.Join(root, "README.md"))+"\nSLACK_BOT_TOKEN=xoxb-your-bot-token\nAWS_ACCESS_KEY_ID=AKIA"+"IOSFODNN7EXAMPLE\n")
	git(t, root, "add", "chart", "README.md")
	if s := run(t, root, "", "secrets", "scan", "--staged"); s.code != 0 {
		t.Errorf("una plantilla de Helm y valores de ejemplo no son secretos: %d\n%s", s.code, s.stdout)
	}
}
