package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// Secretos de prueba armados por partes (R18 del propio repo).
var (
	cliAWS    = "AKIA" + "IOSFODNN7EXAMPLE"
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

	// Sin secretos versionados, scan pasa.
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "inicio", "--no-verify")
	must(t, run(t, root, "", "secrets", "scan"), 0, "scan limpio")

	// Un secreto escrito en un archivo versionado.
	write(t, root, "src/config.go", "package src\n\nconst clave = \""+cliAWS+"\"\n")
	git(t, root, "add", "src/config.go")
	s := run(t, root, "", "secrets", "scan", "--staged")
	must(t, s, 1, "scan --staged con secreto")
	if !strings.Contains(s.stdout, "src/config.go:3") || !strings.Contains(s.stdout, "llave de acceso de AWS") || strings.Contains(s.stdout, "EXAMPLE") {
		t.Errorf("scan --staged:\n%s", s.stdout)
	}
	// coyote commit no deja pasar el secreto, ni con --no-verify.
	c := run(t, root, "", "commit", "-m", "feat(src): configuración")
	must(t, c, 1, "commit con secreto")
	if !strings.Contains(c.stderr, "R18") || strings.Contains(c.stderr+c.stdout, "EXAMPLE") {
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
