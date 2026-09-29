package secrets

import (
	"strings"
	"testing"
)

// Los secretos de prueba se arman por partes: el código de coyote no lleva
// ninguno escrito (R18) y un push no los confunde con reales.
var (
	fakeAWS     = "AKIA" + "IOSFODNN7EXAMPLE"
	fakeGitHub  = "ghp_" + strings.Repeat("a1B2", 9)
	fakePAT     = "github_" + "pat_" + strings.Repeat("Zx9", 10)
	fakeKey     = "-----BEGIN " + "RSA PRIVATE KEY-----"
	fakeOpenSSH = "-----BEGIN " + "OPENSSH PRIVATE KEY-----"
	fakeSlack   = "xoxb-" + "123456789012-abcdefghijkl"
	fakeStripe  = "sk_" + "live_" + strings.Repeat("q7", 12)
	fakeURL     = "postgres://app:" + "Zk9pLq2vX7mN" + "@db.internal:5432/ventas"
	fakeModel   = "sk-" + "ant-api03-" + strings.Repeat("AbC1", 21)
)

func TestFileKind(t *testing.T) {
	secret := []string{".env", "config/.env.local", ".env.production", ".envrc", "infra/terraform.tfstate",
		"stacks/demo/terraform.tfstate.backup", "terraform.tfstate.1695123.backup", "certs/server.pem", "tls.key",
		"android/app/upload.jks", "android/key.properties", "release.keystore", "kubeconfig", "ops/prod.kubeconfig",
		"gcp/sa-key.json", "service-account.json", "client_secret_123.apps.json", "credentials.json", "k8s/secrets.yaml",
		"k8s/secret.yml", ".npmrc", ".netrc", "id_rsa", "id_ed25519", "AuthKey_ABC.p8", ".docker/config.json", "vpn/office.ovpn",
		"deploy/prod.env", "docker.env"}
	for _, p := range secret {
		if _, ok := FileKind(p); !ok {
			t.Errorf("%s es un archivo de secretos", p)
		}
	}
	plain := []string{".env.example", ".env.sample", "config/.env.template", "secrets.yaml.example", "id_rsa.pub", "README.md",
		"main.go", "environment.ts", "env.go", "src/key.ts", "keys.go", "terraform.tfvars", "google-services.json",
		"monkey.json", "package.json", "docker-compose.yml", "service.yaml", "application-prod.yml", ".envoy.yaml"}
	for _, p := range plain {
		if k, ok := FileKind(p); ok {
			t.Errorf("%s no es un archivo de secretos (%s)", p, k)
		}
	}
	r := Rules{Files: []string{"config/prod/*.yaml"}, Allow: []Allow{{Path: "testdata/**", Reason: "llaves de prueba generadas"}}}
	if _, ok := r.Kind("config/prod/app.yaml"); !ok {
		t.Error("un archivo de secretos del proyecto no se reconoce")
	}
	if _, ok := r.Kind("testdata/server.key"); ok {
		t.Error("una dispensa no se respeta")
	}
	if _, ok := r.Kind("certs/server.key"); !ok {
		t.Error("una dispensa no debe alcanzar otras rutas")
	}
}

func TestGlobMayMatch(t *testing.T) {
	for _, p := range []string{"*.pem", "**/.env*", ".env*", "**/*.tfstate", "*.key", "**/*", "*", "secrets.*", "id_*"} {
		if _, ok := GlobMayMatch(p); !ok {
			t.Errorf("%q alcanza archivos de secretos", p)
		}
	}
	for _, p := range []string{"*.go", "src/**/*.ts", "**/*.md", ".env", "docs/*.txt", "*.example"} {
		if s, ok := GlobMayMatch(p); ok {
			t.Errorf("%q no alcanza archivos de secretos (%s)", p, s)
		}
	}
}

func TestScan(t *testing.T) {
	text := strings.Join([]string{
		"aws_access_key_id = " + fakeAWS,
		"token: " + fakeGitHub,
		"otro: " + fakePAT,
		fakeKey,
		fakeOpenSSH,
		"SLACK=" + fakeSlack,
		"STRIPE=" + fakeStripe,
		"DATABASE_URL=" + fakeURL,
		"ANTHROPIC=" + fakeModel,
		"nada que ver aquí",
	}, "\n")
	found := Scan("x.env", text)
	kinds := map[string]int{}
	for _, f := range found {
		kinds[f.Kind] = f.Line
		if strings.Contains(f.Hint, "EXAMPLE") || strings.Contains(f.Hint, "a1B2") || strings.Contains(f.Hint, "Zk9") {
			t.Errorf("la pista muestra el valor: %+v", f)
		}
	}
	for _, want := range []string{"llave de acceso de AWS", "token de GitHub", "llave privada", "token de Slack", "llave de Stripe",
		"URL con contraseña", "llave de API de un modelo"} {
		if _, ok := kinds[want]; !ok {
			t.Errorf("no encontró %s en %v", want, kinds)
		}
	}
	if kinds["URL con contraseña"] != 8 {
		t.Errorf("la línea de la URL: %d", kinds["URL con contraseña"])
	}
	clean := []string{
		"postgres://user:password@localhost:5432/db",
		"postgres://postgres:postgres@db:5432/app",
		"https://user:${TOKEN}@github.com/x/y",
		"jdbc:postgresql://localhost:5432/app?user=app",
		"ghp_corto",
		"AKIA123",
		"-----BEGIN PUBLIC KEY-----",
		"-----BEGIN CERTIFICATE-----",
		"const pattern = `-----BEGIN (RSA )?PRIVATE KEY-----`",
		"AIzaSyDaGmWKa4JsXZ-HjGw7ISLn_3namBGewQe",
		"https://git:" + "Zk9pLq2vX7mN" + "@x.com # " + AllowMarker,
	}
	for _, l := range clean {
		if f := Scan("", l); len(f) > 0 {
			t.Errorf("%q no es un secreto: %+v", l, f)
		}
	}
}

func TestNames(t *testing.T) {
	env := "# comentario\nDB_HOST=db\nexport DB_PASSWORD=s3cr3t\nFARO_API_KEY=\"x\"\n" + fakeGitHub + "\n"
	got := strings.Join(Names(".env", []byte(env)), ",")
	if got != "DB_HOST,DB_PASSWORD,FARO_API_KEY" {
		t.Errorf("nombres de .env: %s", got)
	}
	for _, leaked := range []string{"s3cr3t", "a1B2"} {
		if strings.Contains(got, leaked) {
			t.Errorf("se filtró un valor: %s", got)
		}
	}
	sa := `{"type":"service_account","project_id":"p","private_key_id":"k","private_key":"` + fakeKey + `"}`
	if got := strings.Join(Names("sa-key.json", []byte(sa)), ","); got != "private_key,private_key_id,project_id,type" {
		t.Errorf("nombres de la cuenta de servicio: %s", got)
	}
	state := `{"version":4,"outputs":{"db_ip":{"value":"10.0.0.1"}},"resources":[]}`
	if got := strings.Join(Names("terraform.tfstate", []byte(state)), ","); got != "outputs,outputs.db_ip,resources,version" {
		t.Errorf("nombres del estado: %s", got)
	}
	yaml := "apiVersion: v1\nkind: Secret\ndata:\n  password: eHl6\n"
	if got := strings.Join(Names("secret.yaml", []byte(yaml)), ","); got != "apiVersion,data,kind" {
		t.Errorf("nombres del YAML: %s", got)
	}
	props := "storePassword=abc\nkeyAlias=upload\n"
	if got := strings.Join(Names("key.properties", []byte(props)), ","); got != "keyAlias,storePassword" {
		t.Errorf("nombres de key.properties: %s", got)
	}
	if got := Names("server.pem", []byte(fakeKey+"\nMIIE\n")); len(got) != 0 {
		t.Errorf("una llave no tiene nombres: %v", got)
	}
	if !Binary([]byte{1, 0, 2}) || Binary([]byte("texto")) {
		t.Error("Binary")
	}
}
