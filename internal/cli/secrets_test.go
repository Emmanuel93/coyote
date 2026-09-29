package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Emmanuel93/coyote/internal/install"
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

func TestTerceraRevision(t *testing.T) {
	// UTF-16 marcado con diff en .gitattributes: git da hunks de texto con
	// bytes 0, y el archivo se relee completo y decodificado.
	_, root := gateProject(t)
	write(t, root, ".gitignore", ".coyote/\n")
	write(t, root, ".gitattributes", "*.ps1 diff\n")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "inicio", "--no-verify")
	utf16 := []byte{0xFF, 0xFE}
	for _, r := range "$t = '" + cliGitHub + "'\r\n" {
		utf16 = append(utf16, byte(r), 0)
	}
	if err := os.WriteFile(filepath.Join(root, "cfg.ps1"), utf16, 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "cfg.ps1")
	if s := run(t, root, "", "secrets", "scan", "--staged"); s.code != 1 || !strings.Contains(s.stdout, "token de GitHub") {
		t.Errorf("UTF-16 con diff en .gitattributes: %d\n%s", s.code, s.stdout)
	}
	git(t, root, "reset", "-q")
	// doctor avisa si disableAllHooks apaga los hooks de Claude Code.
	write(t, root, ".claude/settings.local.json", `{"disableAllHooks": true}`)
	if d := run(t, root, "", "doctor"); !strings.Contains(d.stdout, "disableAllHooks") {
		t.Errorf("doctor no avisa de disableAllHooks:\n%s", d.stdout)
	}
}

func TestGatePRReglasDeUnProyectoEnSubcarpeta(t *testing.T) {
	base := setup(t)
	productoDemo(t, base)
	svc := filepath.Join(base, "servicios")
	write(t, svc, "api/coyote/project.yaml", "name: api\nsecrets:\n  files: [\"config/prod/*.yaml\"]\n")
	git(t, svc, "add", "-A")
	git(t, svc, "commit", "-qm", "proyecto coyote en una subcarpeta")
	baseSHA := strings.TrimSpace(git(t, svc, "rev-parse", "HEAD"))
	write(t, svc, "api/config/prod/db.yaml", "password: corta\n")
	git(t, svc, "add", "-A")
	git(t, svc, "commit", "-qm", "configuración")
	headSHA := strings.TrimSpace(git(t, svc, "rev-parse", "HEAD"))
	r := run(t, base, "", "gate", "pr", "--repo", "servicios="+svc, "--self", "servicios", "--base", baseSHA, "--head", headSHA, "--event", "", "--policy", "fail")
	if r.code != 1 || !strings.Contains(r.stdout, "api/config/prod/db.yaml") {
		t.Errorf("gate pr con secrets.files de un proyecto en subcarpeta: %d\n%s", r.code, r.stdout)
	}
}

func TestSecretsListVacio(t *testing.T) {
	_, root := gateProject(t)
	if r := run(t, root, "", "secrets", "list", "--json"); r.code != 0 || strings.TrimSpace(r.stdout) != "[]" {
		t.Errorf("sin archivos de secretos, --json da []: %d %q", r.code, r.stdout)
	}
}

func TestGatePRConGitleaks(t *testing.T) {
	base := setup(t)
	productoDemo(t, base)
	svc := filepath.Join(base, "servicios")
	sha := strings.TrimSpace(git(t, svc, "rev-parse", "HEAD"))
	report := filepath.Join(base, "gitleaks.json")
	write(t, base, "gitleaks.json", `[{"RuleID":"generic-api-key","StartLine":12,"Match":"REDACTED","Secret":"clave-que-no-debe-salir","File":"services/pedidos-service/src/main/resources/application.yml","Commit":"`+sha+`"}]`)
	r := run(t, base, "", "gate", "pr", "--repo", "servicios="+svc, "--self", "servicios", "--base", sha, "--head", sha, "--event", "", "--gitleaks", report)
	must(t, r, 0, "gate pr con gitleaks en warn")
	for _, want := range []string{"### coyote: R3", "1 hallazgo de gitleaks", "**gitleaks.**", "application.yml", "| 12 |", "generic-api-key", "gitleaks encontró un posible secreto"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("reporte sin %q:\n%s", want, r.stdout)
		}
	}
	if strings.Contains(r.stdout, "clave-que-no-debe-salir") {
		t.Fatalf("el reporte no muestra el valor:\n%s", r.stdout)
	}
	must(t, run(t, base, "", "gate", "pr", "--repo", "servicios="+svc, "--self", "servicios", "--base", sha, "--head", sha, "--event", "", "--gitleaks", report, "--policy", "fail"), 1, "fail espera a un dueño")
	// El diff neto solo suma lo que el escaneo de commits no vio.
	netReport := filepath.Join(base, "gitleaks-net.json")
	write(t, base, "gitleaks-net.json", `[{"RuleID":"generic-api-key","StartLine":12,"File":"services/pedidos-service/src/main/resources/application.yml","Commit":"abc"},`+
		`{"RuleID":"github-pat","StartLine":3,"File":"conf/lavado.txt","Commit":"abc"}]`)
	r = run(t, base, "", "gate", "pr", "--repo", "servicios="+svc, "--self", "servicios", "--base", sha, "--head", sha, "--event", "", "--gitleaks", report, "--gitleaks-net", netReport)
	must(t, r, 0, "gate pr con los dos reportes")
	if !strings.Contains(r.stdout, "2 hallazgos de gitleaks") || !strings.Contains(r.stdout, "conf/lavado.txt") || !strings.Contains(r.stdout, "diff del PR") {
		t.Errorf("el diff neto suma lo nuevo una sola vez:\n%s", r.stdout)
	}
	write(t, base, "gitleaks-net.json", "[] []")
	if r = run(t, base, "", "gate", "pr", "--repo", "servicios="+svc, "--self", "servicios", "--base", sha, "--head", sha, "--event", "", "--gitleaks", report, "--gitleaks-net", netReport); r.code == 0 {
		t.Fatalf("un reporte neto inválido falla cerrado:\n%s", r.stdout)
	}
	// Sin reporte no hay revisión: falla cerrado.
	r = run(t, base, "", "gate", "pr", "--repo", "servicios="+svc, "--self", "servicios", "--base", sha, "--head", sha, "--event", "", "--gitleaks", filepath.Join(base, "no-existe.json"))
	if r.code == 0 || !strings.Contains(r.stderr, "no puedo leer el reporte de gitleaks") {
		t.Fatalf("sin reporte: %d\n%s", r.code, r.stderr)
	}
	write(t, base, "gitleaks.json", "[]")
	r = run(t, base, "", "gate", "pr", "--repo", "servicios="+svc, "--self", "servicios", "--base", sha, "--head", sha, "--event", "", "--gitleaks", report)
	if r.code != 0 || strings.Contains(r.stdout, "gitleaks") {
		t.Fatalf("sin hallazgos no hay sección:\n%s", r.stdout)
	}
}

// TestGitleaksDeVerdad corre el paso del workflow tal cual, con gitleaks
// descargado de GitHub y verificado por hash, sobre un PR que intenta
// exentarse. Necesita red: corre con COYOTE_GITLEAKS_E2E=1.
func TestGitleaksDeVerdad(t *testing.T) {
	if os.Getenv("COYOTE_GITLEAKS_E2E") != "1" {
		t.Skip("COYOTE_GITLEAKS_E2E no está en 1")
	}
	base := setup(t)
	productoDemo(t, base)
	ws := filepath.Join(base, "ws")
	tmp := filepath.Join(base, "runner")
	if err := os.MkdirAll(filepath.Join(ws, "repos"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	svc := filepath.Join(ws, "repos", "servicios")
	git(t, base, "clone", "-q", filepath.Join(base, "servicios"), svc)
	// La base permite los fixtures de pruebas; el PR agrega un token, otro con
	// gitleaks:allow, uno en fixtures y su propia configuración para apagarlo todo.
	write(t, svc, ".gitleaks.toml", "[extend]\nuseDefault = true\n[allowlist]\npaths = [\"fixtures/\"]\n")
	git(t, svc, "add", "-A")
	git(t, svc, "commit", "-qm", "chore: configuración de gitleaks")
	baseSHA := strings.TrimSpace(git(t, svc, "rev-parse", "HEAD"))
	tok := func(s string) string { return "ghp_" + strings.Repeat(s, 36/len(s)) }
	write(t, svc, "conf/app.txt", "github_token = \""+tok("aB3dE5gH7jK9")+"\"\n")
	write(t, svc, "conf/otro.txt", "token = \""+tok("Zx8Cv6Bn4Mq2")+"\" # gitleaks:allow\n")
	write(t, svc, "fixtures/f.txt", "token = \""+tok("Pw2Lk4Jh6Gf8")+"\"\n")
	git(t, svc, "add", "-A")
	git(t, svc, "commit", "-qm", "feat: configuración")
	write(t, svc, ".gitleaks.toml", "[extend]\nuseDefault = true\n[allowlist]\npaths = [\".*\"]\n")
	write(t, svc, ".gitleaksignore", "x:conf/app.txt:github-pat:1\n")
	git(t, svc, "add", "-A")
	git(t, svc, "commit", "-qm", "chore: se exenta")
	// Lavado por rename: se agrega en una ruta que gitleaks ignora y se mueve.
	write(t, svc, "x/node_modules/p.txt", "token = \""+tok("Rt5Yu7Io9Pa1")+"\"\n")
	git(t, svc, "add", "-A")
	git(t, svc, "commit", "-qm", "chore: dependencia")
	git(t, svc, "mv", "x/node_modules/p.txt", "conf/lavado.txt")
	git(t, svc, "commit", "-qm", "chore: mueve")
	// Un .gitattributes del PR que marca el archivo como binario no lo esconde.
	write(t, svc, ".gitattributes", "*.dat binary\n")
	write(t, svc, "conf/claves.dat", "token = \""+tok("Qa2Ws4Ed6Rf8")+"\"\n")
	git(t, svc, "add", "-A")
	git(t, svc, "commit", "-qm", "chore: datos")
	// Un secreto que entra al resolver un merge.
	git(t, svc, "checkout", "-q", "-b", "lado", baseSHA)
	write(t, svc, "conf/lado.txt", "lado\n")
	git(t, svc, "add", "-A")
	git(t, svc, "commit", "-qm", "chore: lado")
	git(t, svc, "checkout", "-q", "-")
	git(t, svc, "merge", "-q", "--no-ff", "--no-commit", "lado")
	write(t, svc, "conf/merge.txt", "token = \""+tok("Mn3Bv5Cx7Zl9")+"\"\n")
	git(t, svc, "add", "-A")
	git(t, svc, "commit", "-qm", "merge lado")
	headSHA := strings.TrimSpace(git(t, svc, "rev-parse", "HEAD"))

	script := filepath.Join(base, "gitleaks.sh")
	write(t, base, "gitleaks.sh", install.GitleaksScript("servicios"))
	cmd := exec.Command("bash", script)
	cmd.Env = append(os.Environ(), "RUNNER_TEMP="+tmp, "GITHUB_WORKSPACE="+ws, "BASE="+baseSHA, "HEAD="+headSHA, "GIT_ATTR_SOURCE=4b825dc642cb6eb9a060e54bf8d69288fbee4904")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("el paso de gitleaks falló: %v\n%s", err, out)
	}
	r := run(t, base, "", "gate", "pr", "--repo", "servicios="+svc, "--self", "servicios", "--base", baseSHA, "--head", headSHA, "--event", "",
		"--gitleaks", filepath.Join(tmp, "gitleaks.json"), "--gitleaks-net", filepath.Join(tmp, "gitleaks-net.json"))
	must(t, r, 0, "gate pr")
	section := r.stdout[strings.Index(r.stdout, "**gitleaks.**"):]
	section = section[:strings.Index(section, "\n\n**")]
	for _, want := range []string{"conf/app.txt", "conf/otro.txt", "conf/lavado.txt", "conf/merge.txt", "conf/claves.dat"} {
		if !strings.Contains(section, want) {
			t.Errorf("gitleaks no vio %s (config del PR, gitleaks:allow, rename, merge o atributos):\n%s", want, section)
		}
	}
	if strings.Contains(section, "fixtures/f.txt") {
		t.Errorf("la configuración de la base cuenta:\n%s", section)
	}
	// Cambiar la configuración de gitleaks es R3 por su ruta.
	if !strings.Contains(r.stdout, "la configuración de gitleaks") {
		t.Errorf("la configuración de gitleaks es R3:\n%s", r.stdout)
	}
	if strings.Contains(r.stdout, "aB3dE5") || strings.Contains(readFile(t, filepath.Join(tmp, "gitleaks.json")), "aB3dE5") {
		t.Fatal("ningún valor en el reporte")
	}
	// Una base que no está en el repo hace fallar el paso: gitleaks saldría con 0.
	cmd = exec.Command("bash", script)
	cmd.Env = append(os.Environ(), "RUNNER_TEMP="+tmp, "GITHUB_WORKSPACE="+ws, "BASE=0123456789abcdef0123456789abcdef01234567", "HEAD="+headSHA)
	if err := cmd.Run(); err == nil {
		t.Fatal("una base desconocida hace fallar el paso")
	}
}
