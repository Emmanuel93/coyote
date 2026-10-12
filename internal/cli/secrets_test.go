package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Emmanuel93/coyote/internal/install"
	"github.com/Emmanuel93/coyote/internal/secrets"
	"github.com/Emmanuel93/coyote/internal/version"
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

// TestGitleaksDeVerdadConfigPropia: con una configuración propia en la base
// (sin las reglas por defecto), un archivo del PR en la misma ruta relativa
// que la configuración del paso se revisa igual; y el paso no depende de la
// identidad de git del runner ni de que firme commits.
func TestGitleaksDeVerdadConfigPropia(t *testing.T) {
	if os.Getenv("COYOTE_GITLEAKS_E2E") != "1" {
		t.Skip("COYOTE_GITLEAKS_E2E no está en 1")
	}
	base := setup(t)
	productoDemo(t, base)
	ws := filepath.Join(base, "ws")
	tmp := filepath.Join(base, "runner")
	for _, d := range []string{filepath.Join(ws, "repos"), tmp} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	svc := filepath.Join(ws, "repos", "servicios")
	git(t, base, "clone", "-q", filepath.Join(base, "servicios"), svc)
	write(t, svc, ".gitleaks.toml", "[[rules]]\nid = \"github-pat\"\nregex = '''ghp_[0-9a-zA-Z]{36}'''\n")
	git(t, svc, "add", "-A")
	git(t, svc, "commit", "-qm", "chore: configuración propia de gitleaks")
	baseSHA := strings.TrimSpace(git(t, svc, "rev-parse", "HEAD"))
	tok := "ghp_" + strings.Repeat("Kd8Lm2Np4Qr6", 3)
	write(t, svc, "conf/control.txt", "token = \""+tok+"\"\n")
	write(t, svc, "gitleaks-base/gitleaks.toml", "token = \""+tok+"\"\n")
	git(t, svc, "add", "-A")
	git(t, svc, "commit", "-qm", "feat: tokens")
	headSHA := strings.TrimSpace(git(t, svc, "rev-parse", "HEAD"))
	// Un runner sin identidad de git y que firma commits.
	runnerCfg := filepath.Join(base, "runner.gitconfig")
	write(t, base, "runner.gitconfig", "[commit]\n\tgpgsign = true\n[user]\n\tuseConfigOnly = true\n")
	write(t, base, "gitleaks.sh", install.GitleaksScript("servicios"))
	cmd := exec.Command("bash", filepath.Join(base, "gitleaks.sh"))
	cmd.Env = append(os.Environ(), "RUNNER_TEMP="+tmp, "GITHUB_WORKSPACE="+ws, "BASE="+baseSHA, "HEAD="+headSHA,
		"GIT_ATTR_SOURCE=4b825dc642cb6eb9a060e54bf8d69288fbee4904", "GIT_CONFIG_GLOBAL="+runnerCfg)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("el paso de gitleaks falló: %v\n%s", err, out)
	}
	for _, report := range []string{"gitleaks.json", "gitleaks-net.json"} {
		got := readFile(t, filepath.Join(tmp, report))
		for _, want := range []string{"conf/control.txt", "gitleaks-base/gitleaks.toml"} {
			if !strings.Contains(got, want) {
				t.Errorf("%s no tiene %s:\n%s", report, want, got)
			}
		}
	}
}

// TestGitleaksDeVerdadSinConfig: sin .gitleaks.toml en la base, el paso usa
// las reglas por defecto fijadas por hash y sin la lista de rutas que saltan:
// un token en node_modules/ o en un lockfile se reporta.
func TestGitleaksDeVerdadSinConfig(t *testing.T) {
	if os.Getenv("COYOTE_GITLEAKS_E2E") != "1" {
		t.Skip("COYOTE_GITLEAKS_E2E no está en 1")
	}
	base := setup(t)
	productoDemo(t, base)
	ws := filepath.Join(base, "ws")
	tmp := filepath.Join(base, "runner")
	for _, d := range []string{filepath.Join(ws, "repos"), tmp} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	svc := filepath.Join(ws, "repos", "servicios")
	git(t, base, "clone", "-q", filepath.Join(base, "servicios"), svc)
	baseSHA := strings.TrimSpace(git(t, svc, "rev-parse", "HEAD"))
	tok := "ghp_" + strings.Repeat("Vb3Nm5Qw7Er9", 3)
	for _, f := range []string{"node_modules/x/a.txt", "yarn.lock", "cfg/verification-metadata.xml.d/settings.txt", "src/gitleaks.toml/creds.go"} {
		write(t, svc, f, "token = \""+tok+"\"\n")
	}
	git(t, svc, "add", "-A")
	git(t, svc, "commit", "-qm", "feat: tokens en rutas que gitleaks salta")
	headSHA := strings.TrimSpace(git(t, svc, "rev-parse", "HEAD"))
	write(t, base, "gitleaks.sh", install.GitleaksScript("servicios"))
	cmd := exec.Command("bash", filepath.Join(base, "gitleaks.sh"))
	cmd.Env = append(os.Environ(), "RUNNER_TEMP="+tmp, "GITHUB_WORKSPACE="+ws, "BASE="+baseSHA, "HEAD="+headSHA,
		"GIT_ATTR_SOURCE=4b825dc642cb6eb9a060e54bf8d69288fbee4904")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("el paso de gitleaks falló: %v\n%s", err, out)
	}
	got := readFile(t, filepath.Join(tmp, "gitleaks.json"))
	for _, want := range []string{"node_modules/x/a.txt", "yarn.lock", "cfg/verification-metadata.xml.d/settings.txt", "src/gitleaks.toml/creds.go"} {
		if !strings.Contains(got, want) {
			t.Errorf("gitleaks no vio %s:\n%s", want, got)
		}
	}
}

// TestRequiresNoApagaLasReglasDeSecretos: revisión adversarial de v1.0. Si
// el project.yaml pide un coyote más nuevo, el escáner y gate pr fallan en
// vez de seguir sin secrets.files.
func TestRequiresNoApagaLasReglasDeSecretos(t *testing.T) {
	base := setup(t)
	oldV, oldC := version.Version, version.Commit
	version.Version, version.Commit = "v1.9.0", "abc1234"
	t.Cleanup(func() { version.Version, version.Commit = oldV, oldC })
	must(t, run(t, base, "", "init", "sec", "--type", "backend", "--purpose", "API de pedidos de la tienda demo"), 0, "init")
	root := filepath.Join(base, "sec")
	cfg := readFile(t, filepath.Join(root, "coyote", "project.yaml"))
	cfg = strings.Replace(cfg, `requires: ">=1.9"`, `requires: ">=1.10"`, 1)
	if !strings.Contains(cfg, `">=1.10"`) {
		t.Fatalf("init no escribió requires:\n%s", cfg)
	}
	write(t, root, "coyote/project.yaml", cfg+"secrets:\n  files: [\"config/prod/*.yaml\"]\n")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "chore: proyecto de una versión más nueva")
	baseSHA := strings.TrimSpace(git(t, root, "rev-parse", "HEAD"))
	write(t, root, "config/prod/otro.yaml", "db_password: hunter2-libre\n")
	git(t, root, "add", "-A")
	for _, args := range [][]string{{"secrets", "scan", "--staged", "--hook"}, {"secrets", "scan", "--staged"}, {"secrets", "scan"}} {
		r := run(t, root, "", args...)
		if r.code != 1 || !strings.Contains(r.stderr, ">=1.10") {
			t.Errorf("%v: se esperaba código 1 con el requires (el hook solo se detiene con 1): %d\n%s%s", args, r.code, r.stdout, r.stderr)
		}
	}
	git(t, root, "commit", "-qm", "feat: configuración de producción", "--no-verify")
	headSHA := strings.TrimSpace(git(t, root, "rev-parse", "HEAD"))
	r := run(t, root, "", "secrets", "scan", "--range", baseSHA+"..."+headSHA)
	if r.code != 1 || !strings.Contains(r.stderr, ">=1.10") {
		t.Errorf("scan --range: %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	r = run(t, base, "", "gate", "pr", "--repo", "sec="+root, "--self", "sec", "--base", baseSHA, "--head", headSHA, "--event", "", "--policy", "warn")
	if r.code != 2 || !strings.Contains(r.stderr, "rama base") {
		t.Errorf("gate pr con la base de una versión más nueva: %d\n%s%s", r.code, r.stdout, r.stderr)
	}
}

// TestSecretosDesdeLaRaizDelMonorepo: segunda revisión adversarial de v1.0.
// Desde la raíz de un repo, secrets scan y secrets list aplican las reglas
// del proyecto de cada subcarpeta, y fallan si su project.yaml no se lee.
func TestSecretosDesdeLaRaizDelMonorepo(t *testing.T) {
	base := setup(t)
	mono := filepath.Join(base, "mono")
	if err := os.MkdirAll(mono, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, mono, "init", "-q")
	must(t, run(t, mono, "", "init", "svc", "--no-git", "--type", "backend", "--purpose", "API de pedidos de la tienda demo"), 0, "init")
	root := filepath.Join(mono, "svc")
	cfgPath := filepath.Join(root, "coyote", "project.yaml")
	cfg := readFile(t, cfgPath)
	write(t, root, "coyote/project.yaml", cfg+"secrets:\n  files: [\"config/prod/*.yaml\"]\n")
	write(t, root, "config/prod/db.yaml", "password: corta\n")
	git(t, mono, "add", "-A")
	git(t, mono, "commit", "-qm", "chore: inicio", "--no-verify")
	if r := run(t, mono, "", "secrets", "scan"); r.code != 1 || !strings.Contains(r.stdout, "svc/config/prod/db.yaml") {
		t.Errorf("scan desde la raíz: %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	if r := run(t, mono, "", "secrets", "list"); r.code != 0 || !strings.Contains(r.stdout, "svc/config/prod/db.yaml") {
		t.Errorf("list desde la raíz: %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	oldV, oldC := version.Version, version.Commit
	version.Version, version.Commit = "v1.9.0", "abc1234"
	t.Cleanup(func() { version.Version, version.Commit = oldV, oldC })
	write(t, root, "coyote/project.yaml", strings.Replace(readFile(t, cfgPath), "version: 1\n", "version: 1\nrequires: \">=1.10\"\n", 1))
	for _, args := range [][]string{{"secrets", "scan"}, {"secrets", "list"}} {
		if r := run(t, mono, "", args...); r.code != 1 || !strings.Contains(r.stderr, ">=1.10") {
			t.Errorf("%v con el proyecto de una versión más nueva: %d\n%s%s", args, r.code, r.stdout, r.stderr)
		}
	}
}

// TestSecretosConProyectosAnidados: tercera revisión de v1.0. Un proyecto
// anidado no tapa los secretos que declara el de afuera; un proyecto dentro
// de vendor no cuenta; un project.yaml roto se nombra y lo demás se reporta.
func TestSecretosConProyectosAnidados(t *testing.T) {
	base := setup(t)
	must(t, run(t, base, "", "init", "mono", "--type", "backend", "--purpose", "API de pedidos de la tienda demo"), 0, "init")
	root := filepath.Join(base, "mono")
	cfg := readFile(t, filepath.Join(root, "coyote", "project.yaml"))
	write(t, root, "coyote/project.yaml", cfg+"secrets:\n  files: [\"**/config/prod/*.yaml\"]\n")
	must(t, run(t, root, "", "init", "svc", "--no-git", "--type", "backend", "--purpose", "servicio de pagos de la tienda"), 0, "init anidado")
	write(t, root, "svc/config/prod/db.yaml", "password: corta\n")
	write(t, root, "config/prod/db.yaml", "password: corta\n")
	write(t, root, "vendor/github.com/acme/lib/coyote/project.yaml", "autonomy: yolo\n")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "chore: inicio", "--no-verify")
	r := run(t, root, "", "secrets", "scan")
	if r.code != 1 || !strings.Contains(r.stdout, "svc/config/prod/db.yaml") || !strings.Contains(r.stdout, "  config/prod/db.yaml") {
		t.Errorf("scan con un proyecto anidado: %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	if strings.Contains(r.stderr, "yolo") {
		t.Errorf("un proyecto en vendor no cuenta:\n%s", r.stderr)
	}
	// Un project.yaml roto fuera de las dependencias se nombra, y lo demás se reporta.
	write(t, root, "otro/coyote/project.yaml", "autonomy: yolo\n")
	write(t, root, "otro/.env", "TOKEN=x\n")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "chore: otro", "--no-verify")
	r = run(t, root, "", "secrets", "scan")
	if r.code != 1 || !strings.Contains(r.stderr, "otro/coyote/project.yaml") || !strings.Contains(r.stdout, "svc/config/prod/db.yaml") {
		t.Errorf("project.yaml roto: %d\n%s%s", r.code, r.stdout, r.stderr)
	}
}

// TestSecretosCuartaRevision: cuarta pasada de v1.0. Los proyectos salen de
// lo versionado, así que borrar su project.yaml del árbol de trabajo no
// apaga sus secrets.files; y uno dentro de dependencias suma sus archivos de
// secretos sin poder dispensar nada.
func TestSecretosCuartaRevision(t *testing.T) {
	base := setup(t)
	must(t, run(t, base, "", "init", "mono", "--type", "backend", "--purpose", "API de pedidos de la tienda demo"), 0, "init")
	root := filepath.Join(base, "mono")
	must(t, run(t, root, "", "init", "svc", "--no-git", "--type", "backend", "--purpose", "servicio de pagos de la tienda"), 0, "init anidado")
	cfg := readFile(t, filepath.Join(root, "svc", "coyote", "project.yaml"))
	write(t, root, "svc/coyote/project.yaml", cfg+"secrets:\n  files: [\"secretos/*.txt\"]\n")
	write(t, root, "svc/secretos/a.txt", "algo\n")
	write(t, root, "third_party/lib/coyote/project.yaml", cfg+"secrets:\n  files: [\"claves/*.txt\"]\n  allow:\n    - path: \"**\"\n      reason: \"la dependencia dispensa todo lo suyo\"\n")
	write(t, root, "third_party/lib/claves/k.txt", "uno\n")
	write(t, root, "third_party/lib/.env", "TOKEN=x\n")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "chore: inicio", "--no-verify")

	// Un proyecto dentro de dependencias suma sus archivos y no dispensa nada.
	r := run(t, root, "", "secrets", "scan")
	for _, want := range []string{"svc/secretos/a.txt", "third_party/lib/claves/k.txt", "third_party/lib/.env"} {
		if r.code != 1 || !strings.Contains(r.stdout, want) {
			t.Errorf("falta %q (código %d):\n%s%s", want, r.code, r.stdout, r.stderr)
		}
	}

	// Sin el project.yaml versionado en el árbol de trabajo, el escaneo no da
	// el visto bueno: no se pierde el proyecto con sus archivos declarados.
	if err := os.Remove(filepath.Join(root, "svc", "coyote", "project.yaml")); err != nil {
		t.Fatal(err)
	}
	r = run(t, root, "", "secrets", "scan")
	if r.code != 1 || !strings.Contains(r.stderr, "svc/coyote/project.yaml") || !strings.Contains(r.stderr, "versionado") {
		t.Errorf("project.yaml versionado que falta: %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	git(t, root, "checkout", "--", "svc/coyote/project.yaml")

	// Lo mismo con un enlace en lugar del archivo.
	if err := os.Remove(filepath.Join(root, "svc", "coyote", "project.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "coyote", "project.yaml"), filepath.Join(root, "svc", "coyote", "project.yaml")); err != nil {
		t.Skip("sin enlaces simbólicos")
	}
	r = run(t, root, "", "secrets", "scan")
	if r.code != 1 || !strings.Contains(r.stderr, "svc/coyote/project.yaml") {
		t.Errorf("project.yaml que es un enlace: %d\n%s%s", r.code, r.stdout, r.stderr)
	}
}

// TestMergeRulesDependencias: las dependencias suman archivos de secretos y
// nunca dispensas; una dispensa de adentro la veta el proyecto de afuera.
func TestMergeRulesDependencias(t *testing.T) {
	allow := []secrets.Allow{{Path: "**", Reason: "motivo suficiente para dispensar"}}
	rules, near := mergeRules("lib/x/claves/k.txt", []layer{
		{rules: secrets.Rules{Files: []string{"claves/*.txt"}, Allow: allow}, rel: "claves/k.txt", dep: true},
		{rules: secrets.Rules{Files: []string{"lib/x/claves/*.txt"}}, rel: "lib/x/claves/k.txt"},
	})
	if near != "lib/x/claves/k.txt" || rules.Allowed(near) || len(rules.Allow) != 0 {
		t.Errorf("la dependencia no dispensa: %+v %q", rules, near)
	}
	if _, ok := rules.Kind(near); !ok {
		t.Errorf("el archivo que declara la dependencia es de secretos: %+v", rules)
	}
	// Sin otro proyecto, la ruta queda como llegó.
	rules, near = mergeRules("lib/claves/k.txt", []layer{{rules: secrets.Rules{Files: []string{"claves/*.txt"}, Allow: allow}, rel: "claves/k.txt", dep: true}})
	if _, ok := rules.Kind(near); !ok || near != "lib/claves/k.txt" || rules.Allowed(near) {
		t.Errorf("solo una dependencia: %+v %q", rules, near)
	}
}

// TestGateSecretosDeProyectosAnidados: quinta revisión de v1.0. Un agente
// abierto en la raíz de un monorepo no lee los archivos que declaran como
// secretos los proyectos anidados, también los que viven en dependencias.
func TestGateSecretosDeProyectosAnidados(t *testing.T) {
	_, root := gateProject(t)
	must(t, run(t, root, "", "init", "svc", "--no-git", "--type", "backend", "--purpose", "servicio de pagos de la tienda"), 0, "init anidado")
	cfg := readFile(t, filepath.Join(root, "svc", "coyote", "project.yaml"))
	write(t, root, "svc/coyote/project.yaml", cfg+"secrets:\n  files: [\"secretos/*.txt\"]\n")
	write(t, root, "svc/secretos/a.txt", "algo\n")
	write(t, root, "svc/README.md", "# servicio\n")
	write(t, root, "apps/vendor/coyote/project.yaml", cfg+"secrets:\n  files: [\"config/prod.yaml\"]\n")
	write(t, root, "apps/vendor/config/prod.yaml", "password: corta\n")
	write(t, root, "third_party/lib/coyote/project.yaml", cfg+"secrets:\n  files: [\"claves/*.txt\"]\n")
	write(t, root, "third_party/lib/claves/k.txt", "uno\n")

	read := func(rel string) result {
		return run(t, root, hook(t, root, "Read", map[string]any{"file_path": filepath.Join(root, rel)}, nil), "gate", "check")
	}
	for _, rel := range []string{"svc/secretos/a.txt", "apps/vendor/config/prod.yaml", "third_party/lib/claves/k.txt"} {
		if r := read(rel); r.code != 2 || !strings.Contains(r.stderr, "bloqueado siempre") {
			t.Errorf("leer %s: el proyecto anidado lo declara como secreto (código %d):\n%s", rel, r.code, r.stderr)
		}
		cat := run(t, root, hook(t, root, "Bash", map[string]any{"command": "cat " + rel, "description": "x"}, nil), "gate", "check")
		if cat.code != 2 || !strings.Contains(cat.stderr, "bloqueado siempre") {
			t.Errorf("cat %s (código %d):\n%s", rel, cat.code, cat.stderr)
		}
	}
	// Un comodín que alcanza el archivo.
	cmd := run(t, root, hook(t, root, "Bash", map[string]any{"command": "cat svc/secretos/*.txt", "description": "x"}, nil), "gate", "check")
	if cmd.code != 2 || !strings.Contains(cmd.stderr, "bloqueado siempre") {
		t.Errorf("cat con comodín (código %d):\n%s", cmd.code, cmd.stderr)
	}
	// Una búsqueda recursiva que recorre una carpeta con esos archivos pide la
	// aprobación de una persona, también si el proyecto se llama vendor.
	write(t, root, "svc/src/main.go", "package main\n")
	for _, c := range []string{"grep -r algo svc", "grep -r uno third_party", "grep -r password apps"} {
		if r := run(t, root, hook(t, root, "Bash", map[string]any{"command": c, "description": "x"}, nil), "gate", "check"); r.code != 2 {
			t.Errorf("%q (código %d):\n%s", c, r.code, r.stderr)
		}
	}
	if r := run(t, root, hook(t, root, "Bash", map[string]any{"command": "grep -r package svc/src", "description": "x"}, nil), "gate", "check"); r.code != 0 {
		t.Errorf("una carpeta sin secretos se busca con normalidad (código %d):\n%s", r.code, r.stderr)
	}
	// Lo demás del proyecto anidado se lee con normalidad.
	if r := read("svc/README.md"); r.code != 0 {
		t.Errorf("leer un archivo común del proyecto anidado (código %d):\n%s", r.code, r.stderr)
	}
}

// TestSecretsListProyectosAnidados: quinta revisión de v1.0. list ve los
// mismos proyectos que scan: uno que se llama vendor no se salta y un
// project.yaml versionado que no se puede leer se nombra.
func TestSecretsListProyectosAnidados(t *testing.T) {
	base := setup(t)
	must(t, run(t, base, "", "init", "mono", "--type", "backend", "--purpose", "API de pedidos de la tienda demo"), 0, "init")
	root := filepath.Join(base, "mono")
	must(t, run(t, root, "", "init", "svc", "--no-git", "--type", "backend", "--purpose", "servicio de pagos de la tienda"), 0, "init anidado")
	cfg := readFile(t, filepath.Join(root, "svc", "coyote", "project.yaml"))
	write(t, root, "svc/coyote/project.yaml", cfg+"secrets:\n  files: [\"secretos/*.txt\"]\n")
	write(t, root, "svc/secretos/a.txt", "algo\n")
	write(t, root, "apps/vendor/coyote/project.yaml", cfg+"secrets:\n  files: [\"config/prod.yaml\"]\n")
	write(t, root, "apps/vendor/config/prod.yaml", "password: corta\n")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "chore: inicio", "--no-verify")

	r := run(t, root, "", "secrets", "list", "--no-names")
	for _, want := range []string{"svc/secretos/a.txt", "apps/vendor/config/prod.yaml"} {
		if r.code != 0 || !strings.Contains(r.stdout, want) {
			t.Errorf("list no muestra %q (código %d):\n%s%s", want, r.code, r.stdout, r.stderr)
		}
	}

	// Un project.yaml versionado que es un enlace roto: la lista sale
	// incompleta y lo dice, como scan.
	if err := os.Remove(filepath.Join(root, "svc", "coyote", "project.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "no-existe.yaml"), filepath.Join(root, "svc", "coyote", "project.yaml")); err != nil {
		t.Skip("sin enlaces simbólicos")
	}
	r = run(t, root, "", "secrets", "list", "--no-names")
	if r.code != 1 || !strings.Contains(r.stderr, "svc/coyote/project.yaml") {
		t.Errorf("list con un project.yaml roto (código %d):\n%s%s", r.code, r.stdout, r.stderr)
	}
}

// TestSecretosProyectoSinVersionar: quinta revisión de v1.0. Un proyecto que
// está en el disco y todavía no se versiona protege lo que se prepara para el
// commit: sus secrets.files cuentan junto con los de los proyectos que git
// sigue.
func TestSecretosProyectoSinVersionar(t *testing.T) {
	base := setup(t)
	t.Setenv("COYOTE_STATE_DIR", filepath.Join(base, "state"))
	old := isTerminal
	isTerminal = func() bool { return true }
	t.Cleanup(func() { isTerminal = old })
	must(t, run(t, base, "", "init", "mono", "--type", "backend", "--purpose", "API de pedidos de la tienda demo"), 0, "init")
	root := filepath.Join(base, "mono")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "chore: inicio", "--no-verify")
	must(t, run(t, root, "", "init", "nuevo", "--no-git", "--type", "backend", "--purpose", "servicio nuevo de la tienda"), 0, "init anidado")
	cfg := readFile(t, filepath.Join(root, "nuevo", "coyote", "project.yaml"))
	write(t, root, "nuevo/coyote/project.yaml", cfg+"secrets:\n  files: [\"secretos/*.txt\"]\n")
	write(t, root, "nuevo/secretos/b.txt", "algo\n")
	git(t, root, "add", "nuevo/secretos/b.txt") // el project.yaml queda sin versionar

	s := run(t, root, "", "secrets", "scan", "--staged")
	if s.code != 1 || !strings.Contains(s.stdout, "nuevo/secretos/b.txt") {
		t.Errorf("scan --staged con un proyecto sin versionar (código %d):\n%s%s", s.code, s.stdout, s.stderr)
	}
	c := run(t, root, "", "commit", "-m", "feat(nuevo): configuración")
	if c.code != 1 || !strings.Contains(c.stderr, "nuevo/secretos/b.txt") {
		t.Errorf("commit con un proyecto sin versionar (código %d):\n%s%s", c.code, c.stdout, c.stderr)
	}
}

// TestSecretosProyectoVersionadoQueFalta: quinta revisión de v1.0. Lo que se
// prepara para el commit, un rango de git y el commit mismo también salen de
// los proyectos que git sigue: uno cuyo project.yaml falta en el disco no da el
// visto bueno, no se vuelve invisible.
func TestSecretosProyectoVersionadoQueFalta(t *testing.T) {
	base := setup(t)
	t.Setenv("COYOTE_STATE_DIR", filepath.Join(base, "state"))
	old := isTerminal
	isTerminal = func() bool { return true }
	t.Cleanup(func() { isTerminal = old })
	must(t, run(t, base, "", "init", "mono", "--type", "backend", "--purpose", "API de pedidos de la tienda demo"), 0, "init")
	root := filepath.Join(base, "mono")
	must(t, run(t, root, "", "init", "svc", "--no-git", "--type", "backend", "--purpose", "servicio de pagos de la tienda"), 0, "init anidado")
	cfg := readFile(t, filepath.Join(root, "svc", "coyote", "project.yaml"))
	write(t, root, "svc/coyote/project.yaml", cfg+"secrets:\n  files: [\"secretos/*.txt\"]\n")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "chore: inicio", "--no-verify")
	baseSHA := strings.TrimSpace(git(t, root, "rev-parse", "HEAD"))
	write(t, root, "svc/secretos/c.txt", "algo\n")
	git(t, root, "add", "svc/secretos/c.txt")
	git(t, root, "commit", "-qm", "chore: configuración", "--no-verify")
	headSHA := strings.TrimSpace(git(t, root, "rev-parse", "HEAD"))
	proj := filepath.Join(root, "svc", "coyote", "project.yaml")
	if err := os.Remove(proj); err != nil {
		t.Fatal(err)
	}
	faltaProyecto := func(what string, r result) {
		t.Helper()
		if r.code != 1 || !strings.Contains(r.stderr, "svc/coyote/project.yaml") || !strings.Contains(r.stderr, "versionado") {
			t.Errorf("%s (código %d):\n%s%s", what, r.code, r.stdout, r.stderr)
		}
	}
	faltaProyecto("scan --range", run(t, root, "", "secrets", "scan", "--range", baseSHA+"..."+headSHA))

	write(t, root, "svc/secretos/b.txt", "otro\n")
	git(t, root, "add", "svc/secretos/b.txt")
	faltaProyecto("scan --staged", run(t, root, "", "secrets", "scan", "--staged"))
	c := run(t, root, "", "commit", "-m", "feat(svc): configuración")
	if c.code != 1 || !strings.Contains(c.stderr, "svc/coyote/project.yaml") {
		t.Errorf("commit con un project.yaml versionado que falta (código %d):\n%s%s", c.code, c.stdout, c.stderr)
	}
}

// TestGatePRDependenciasEnLaBase: quinta revisión de v1.0. gate pr toma de la
// base los archivos que declara un proyecto dentro de dependencias, y sus
// dispensas no valen.
func TestGatePRDependenciasEnLaBase(t *testing.T) {
	base := setup(t)
	productoDemo(t, base)
	svc := filepath.Join(base, "servicios")
	write(t, svc, "third_party/lib/coyote/project.yaml", "name: lib\nsecrets:\n  files: [\"claves/*.txt\"]\n  allow:\n    - path: \"**\"\n      reason: \"la dependencia dispensa todo lo suyo\"\n")
	git(t, svc, "add", "-A")
	git(t, svc, "commit", "-qm", "una dependencia con un proyecto coyote")
	baseSHA := strings.TrimSpace(git(t, svc, "rev-parse", "HEAD"))
	write(t, svc, "third_party/lib/claves/k.txt", "uno\n")
	git(t, svc, "add", "-A")
	git(t, svc, "commit", "-qm", "claves")
	headSHA := strings.TrimSpace(git(t, svc, "rev-parse", "HEAD"))
	r := run(t, base, "", "gate", "pr", "--repo", "servicios="+svc, "--self", "servicios", "--base", baseSHA, "--head", headSHA, "--event", "", "--policy", "fail")
	if r.code != 1 || !strings.Contains(r.stdout, "third_party/lib/claves/k.txt") {
		t.Errorf("gate pr con secrets.files de una dependencia: %d\n%s%s", r.code, r.stdout, r.stderr)
	}
}

// TestMergeRulesNombresConComodines: quinta revisión de v1.0. La ruta de un
// archivo que declara un proyecto de afuera pasa a ser un patrón del proyecto
// más cercano: un nombre con llaves, comas o asteriscos sigue siendo ese
// archivo, no un patrón que ya no lo alcanza.
func TestMergeRulesNombresConComodines(t *testing.T) {
	name := "claves/k{1},x*.txt"
	rules, near := mergeRules("svc/"+name, []layer{
		{rules: secrets.Rules{}, rel: name},
		{rules: secrets.Rules{Files: []string{"svc/claves/*.txt"}}, rel: "svc/" + name},
	})
	if _, ok := rules.Kind(near); !ok {
		t.Errorf("el archivo que declara el proyecto de afuera es de secretos: %+v %q", rules, near)
	}
	rules, near = mergeRules("lib/"+name, []layer{
		{rules: secrets.Rules{Files: []string{"claves/*.txt"}}, rel: name, dep: true},
	})
	if _, ok := rules.Kind(near); !ok {
		t.Errorf("el archivo que declara una dependencia es de secretos: %+v %q", rules, near)
	}
	// Y no alcanza a otro archivo con otra forma.
	if _, ok := rules.Kind("lib/claves/otro.txt"); ok {
		t.Errorf("no declara nada más: %+v", rules)
	}
}

// TestSecretosUTF16ConSecuenciasAnsi: sexta revisión de v1.0. Un log de
// PowerShell en UTF-16 trae secuencias ANSI (ESC): contarlas como basura lo
// dejaba por binario, y el token que traía pasaba sin escanear en secrets
// scan y en el commit. Un binario con BOM de mentira sigue sin ser texto.
func TestSecretosUTF16ConSecuenciasAnsi(t *testing.T) {
	_, root := gateProject(t)
	write(t, root, ".gitignore", ".coyote/\n")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "inicio", "--no-verify")
	esc := string(rune(0x1b))
	log := ""
	for i := 0; i < 30; i++ {
		log += esc + "[32mok" + esc + "[0m paso\r\n"
	}
	log += "GITHUB_TOKEN=" + cliGitHub + "\r\n"
	if err := os.WriteFile(filepath.Join(root, "salida.txt"), le16(log), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "salida.txt")
	if s := run(t, root, "", "secrets", "scan", "--staged"); s.code != 1 || !strings.Contains(s.stdout, "token de GitHub") {
		t.Errorf("un log en UTF-16 con ANSI y un token: %d\n%s", s.code, s.stdout)
	}
	if c := run(t, root, "", "commit", "-m", "chore(build): salida del pipeline"); c.code != 1 || !strings.Contains(c.stderr, "R18") {
		t.Errorf("el commit de ese log: %d\n%s", c.code, c.stderr)
	}
}

// TestGateAbiertoEnUnProyectoAnidado: sexta revisión de v1.0. Un IDE abierto en
// un proyecto dentro de un monorepo tomaba las reglas del proyecto más cercano
// y olvidaba los secrets.files del de afuera, que el commit sí aplica: el
// agente leía lo que el commit habría bloqueado. Las rutas hermanas, dentro
// del mismo repo, también.
func TestGateAbiertoEnUnProyectoAnidado(t *testing.T) {
	_, root := gateProject(t)
	cfg := readFile(t, filepath.Join(root, "coyote", "project.yaml"))
	write(t, root, "coyote/project.yaml", cfg+"secrets:\n  files: [\"svc/config/prod/*.yaml\", \"otro/claves/*.txt\"]\n")
	must(t, run(t, root, "", "init", "svc", "--no-git", "--type", "backend", "--purpose", "servicio de pagos de la tienda"), 0, "init anidado")
	write(t, root, "svc/config/prod/db.yaml", "password: corta\n")
	write(t, root, "svc/README.md", "# servicio\n")
	write(t, root, "otro/claves/k.txt", "uno\n")
	write(t, root, "otro/README.md", "# otro\n")
	svc := filepath.Join(root, "svc")
	fuera := filepath.Join(filepath.Dir(root), "fuera")
	write(t, fuera, ".env", "A=1\n")
	write(t, fuera, "notas.txt", "hola\n")
	check := func(tool string, input map[string]any) result {
		t.Helper()
		return run(t, root, hook(t, svc, tool, input, nil), "gate", "check")
	}
	for name, r := range map[string]result{
		"Read de lo que declara el proyecto de afuera": check("Read", map[string]any{"file_path": filepath.Join(svc, "config", "prod", "db.yaml")}),
		"cat de lo mismo": check("Bash", map[string]any{"command": "cat config/prod/db.yaml", "description": "x"}),
		"Read de una ruta hermana que declara el de afuera": check("Read", map[string]any{"file_path": filepath.Join(root, "otro", "claves", "k.txt")}),
		"cat de la ruta hermana":                            check("Bash", map[string]any{"command": "cat ../otro/claves/k.txt", "description": "x"}),
		"un .env fuera del repo, por su nombre":             check("Read", map[string]any{"file_path": filepath.Join(fuera, ".env")}),
	} {
		if r.code != 2 || !strings.Contains(r.stderr, "bloqueado siempre") {
			t.Errorf("%s (código %d):\n%s", name, r.code, r.stderr)
		}
	}
	for name, r := range map[string]result{
		"un archivo común del proyecto":   check("Read", map[string]any{"file_path": filepath.Join(svc, "README.md")}),
		"un archivo común del hermano":    check("Read", map[string]any{"file_path": filepath.Join(root, "otro", "README.md")}),
		"un archivo común fuera del repo": check("Read", map[string]any{"file_path": filepath.Join(fuera, "notas.txt")}),
	} {
		if r.code != 0 {
			t.Errorf("%s (código %d):\n%s", name, r.code, r.stderr)
		}
	}
}

// TestSecretsListIncluyeLoVersionadoEnDependencias: sexta revisión de v1.0.
// list no recorre vendor, node_modules ni build, pero scan sí reporta lo que
// git sigue ahí: un archivo de secretos versionado en una dependencia se
// lista, y uno sin versionar en esas carpetas, no.
func TestSecretsListIncluyeLoVersionadoEnDependencias(t *testing.T) {
	_, root := gateProject(t)
	write(t, root, ".gitignore", ".coyote/\nnode_modules/sin-git/\n")
	write(t, root, "vendor/lib/.env", "A=1\n")
	write(t, root, "node_modules/pkg/server.key", "-----BEGIN "+"RSA PRIVATE KEY-----\nabc\n")
	write(t, root, "node_modules/sin-git/.env", "B=2\n")
	write(t, root, "build/.env", "C=3\n")
	write(t, root, ".env", "D=4\n")
	if err := os.Symlink(filepath.Join(root, ".env"), filepath.Join(root, "vendor", "lib", "enlace.env")); err != nil {
		t.Skip("sin enlaces simbólicos")
	}
	git(t, root, "add", "-f", "vendor", "node_modules/pkg", ".gitignore", "coyote", ".env")
	git(t, root, "commit", "-qm", "chore: inicio", "--no-verify")

	r := run(t, root, "", "secrets", "list", "--no-names")
	must(t, r, 0, "secrets list")
	for _, want := range []string{"vendor/lib/.env", "node_modules/pkg/server.key", ".env"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("list no muestra %q:\n%s", want, r.stdout)
		}
	}
	for _, not := range []string{"node_modules/sin-git/.env", "build/.env", "enlace.env"} {
		if strings.Contains(r.stdout, not) {
			t.Errorf("list no recorre las dependencias sin versionar (%q):\n%s", not, r.stdout)
		}
	}
	// Cada archivo sale una sola vez.
	j := run(t, root, "", "secrets", "list", "--no-names", "--json")
	must(t, j, 0, "secrets list --json")
	var entries []secretEntry
	if err := json.Unmarshal([]byte(j.stdout), &entries); err != nil {
		t.Fatalf("json: %v\n%s", err, j.stdout)
	}
	count := map[string]int{}
	for _, e := range entries {
		count[e.Path]++
	}
	for p, c := range count {
		if c != 1 {
			t.Errorf("%s sale %d veces", p, c)
		}
	}
	if len(count) != 3 {
		t.Errorf("deben ser 3 archivos: %v", count)
	}
}

// TestProposeYApproveVenLosSecretosAnidados: sexta revisión de v1.0. propose y
// approve --bash juzgan el comando con las mismas reglas que el gate: un
// comando que lee los secretos de un proyecto anidado no es "de solo lectura".
func TestProposeYApproveVenLosSecretosAnidados(t *testing.T) {
	_, root := gateProject(t)
	must(t, run(t, root, "", "init", "svc", "--no-git", "--type", "backend", "--purpose", "servicio de pagos de la tienda"), 0, "init anidado")
	cfg := readFile(t, filepath.Join(root, "svc", "coyote", "project.yaml"))
	write(t, root, "svc/coyote/project.yaml", cfg+"secrets:\n  files: [\"secretos/*.txt\"]\n")
	write(t, root, "svc/secretos/a.txt", "algo\n")
	for _, args := range [][]string{
		{"propose", "--bash", "cat svc/secretos/a.txt"},
		{"approve", "--bash", "cat svc/secretos/a.txt"},
	} {
		r := run(t, root, "", args...)
		if r.code != 1 || !strings.Contains(r.stderr, "se bloquea siempre") {
			t.Errorf("%v (código %d):\n%s%s", args, r.code, r.stdout, r.stderr)
		}
	}
	if r := run(t, root, "", "propose", "--bash", "cat README.md"); r.code != 0 || !strings.Contains(r.stdout, "solo lectura") {
		t.Errorf("leer lo común sigue siendo de solo lectura (código %d):\n%s%s", r.code, r.stdout, r.stderr)
	}
}

// TestMergeRulesDispensasDeProyectosAnidados: sexta revisión de v1.0. La
// dispensa del proyecto más cercano vale para lo suyo, como dice la
// especificación; solo no tapa lo que el proyecto de afuera declara como
// secreto y no dispensa.
func TestMergeRulesDispensasDeProyectosAnidados(t *testing.T) {
	allow := func(p string) []secrets.Allow {
		return []secrets.Allow{{Path: p, Reason: "llaves de prueba generadas por el pipeline"}}
	}
	// El de afuera no dice nada de esa ruta: la dispensa de adentro vale.
	rules, near := mergeRules("svc/testdata/server.key", []layer{
		{rules: secrets.Rules{Allow: allow("testdata/**")}, rel: "testdata/server.key"},
		{rules: secrets.Rules{Files: []string{"otro/*.yaml"}}, rel: "svc/testdata/server.key"},
	})
	if near != "testdata/server.key" || !rules.Allowed(near) {
		t.Errorf("la dispensa del proyecto más cercano vale: %+v %q", rules, near)
	}
	// El de afuera declara el archivo y no lo dispensa: no se tapa.
	rules, near = mergeRules("svc/config/prod/db.yaml", []layer{
		{rules: secrets.Rules{Allow: allow("config/prod/*.yaml")}, rel: "config/prod/db.yaml"},
		{rules: secrets.Rules{Files: []string{"svc/config/prod/*.yaml"}}, rel: "svc/config/prod/db.yaml"},
	})
	if rules.Allowed(near) {
		t.Errorf("el proyecto de adentro no dispensa lo que el de afuera declara: %+v %q", rules, near)
	}
	if _, ok := rules.Kind(near); !ok {
		t.Errorf("sigue siendo un archivo de secretos: %+v %q", rules, near)
	}
	// Si el de afuera también lo dispensa, vale.
	rules, near = mergeRules("svc/config/prod/db.yaml", []layer{
		{rules: secrets.Rules{Allow: allow("config/prod/*.yaml")}, rel: "config/prod/db.yaml"},
		{rules: secrets.Rules{Files: []string{"svc/config/prod/*.yaml"}, Allow: allow("svc/config/prod/*.yaml")}, rel: "svc/config/prod/db.yaml"},
	})
	if !rules.Allowed(near) {
		t.Errorf("los dos dispensan: %+v %q", rules, near)
	}
}

// TestGateProyectoAnidadoConReglasIlegibles: sexta revisión de v1.0. Con el
// project.yaml de un proyecto anidado roto, el gate no puede saber qué es
// secreto ahí y bloquea; el motivo lo dice, en vez de afirmar que el
// archivo es de secretos y mandar a secrets list, que tampoco puede.
func TestGateProyectoAnidadoConReglasIlegibles(t *testing.T) {
	_, root := gateProject(t)
	must(t, run(t, root, "", "init", "svc", "--no-git", "--type", "backend", "--purpose", "servicio de pagos de la tienda"), 0, "init anidado")
	write(t, root, "svc/README.md", "# servicio\n")
	write(t, root, "svc/coyote/project.yaml", "name: [\n  roto\n")
	for name, r := range map[string]result{
		"Read":  run(t, root, hook(t, root, "Read", map[string]any{"file_path": filepath.Join(root, "svc", "README.md")}, nil), "gate", "check"),
		"Write": run(t, root, hook(t, root, "Write", map[string]any{"file_path": filepath.Join(root, "svc", "notas.md"), "content": "x"}, nil), "gate", "check"),
		"cat":   run(t, root, hook(t, root, "Bash", map[string]any{"command": "cat svc/README.md", "description": "x"}, nil), "gate", "check"),
	} {
		if r.code != 2 || !strings.Contains(r.stderr, "no se pueden leer") || !strings.Contains(r.stderr, "svc/coyote/project.yaml") || !strings.Contains(r.stderr, "la persona corrige") {
			t.Errorf("%s (código %d):\n%s", name, r.code, r.stderr)
		}
		if strings.Contains(r.stderr, "archivo de secretos: un proyecto") || strings.Contains(r.stderr, "pide los nombres con coyote secrets list") {
			t.Errorf("%s: el motivo no es que el archivo sea de secretos ni hay lista que pedir:\n%s", name, r.stderr)
		}
	}
}

// TestSecretosCambioDeModoNoCargaLoQueYaEstaba: sexta revisión de v1.0. Lo que
// git lista sin hunks se compara con HEAD en lo preparado para el commit,
// como en un PR: hacer ejecutable un script no acusa el token que ya tenía,
// y una línea nueva con un token en un archivo con -diff, sí.
func TestSecretosCambioDeModoNoCargaLoQueYaEstaba(t *testing.T) {
	_, root := gateProject(t)
	write(t, root, ".gitignore", ".coyote/\n")
	write(t, root, ".gitattributes", "*.min.js -diff\n")
	write(t, root, "scripts/deploy.sh", "#!/bin/sh\nTOKEN="+cliGitHub+"\n")
	write(t, root, "web/app.min.js", "var a=1;\n")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "inicio", "--no-verify")
	if err := os.Chmod(filepath.Join(root, "scripts", "deploy.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "scripts/deploy.sh")
	must(t, run(t, root, "", "secrets", "scan", "--staged"), 0, "solo cambia el modo")
	must(t, run(t, root, "", "commit", "-m", "chore(scripts): hace ejecutable el script"), 0, "commit de un cambio de modo")

	// Una línea nueva con un token en un archivo con -diff sí se acusa.
	write(t, root, "web/app.min.js", "var a=1;\nvar t='"+cliGitHub+"';\n")
	git(t, root, "add", "web/app.min.js")
	if s := run(t, root, "", "secrets", "scan", "--staged"); s.code != 1 || !strings.Contains(s.stdout, "web/app.min.js:2") {
		t.Errorf("la línea nueva de un archivo con -diff: %d\n%s", s.code, s.stdout)
	}
}
