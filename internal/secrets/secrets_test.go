package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Los secretos de prueba se arman por partes: el código de coyote no lleva
// ninguno escrito (R18) y un push no los confunde con reales.
var (
	fakeAWS     = "AKIA" + "Q3VZ7T2M9KX4B8JN"
	fakeGitHub  = "ghp_" + strings.Repeat("a1B2", 9)
	fakePAT     = "github_" + "pat_" + strings.Repeat("Zx9", 10)
	fakeKey     = "-----BEGIN " + "RSA PRIVATE KEY-----"
	fakeOpenSSH = "-----BEGIN " + "OPENSSH PRIVATE KEY-----"
	fakeBody    = strings.Repeat("MIIEvQIBADANBgkqhkiG9w0B", 3)
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
		fakeBody,
		fakeOpenSSH,
		"",
		fakeBody,
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
		if strings.Contains(f.Hint, "Q3VZ") || strings.Contains(f.Hint, "a1B2") || strings.Contains(f.Hint, "Zk9") {
			t.Errorf("la pista muestra el valor: %+v", f)
		}
	}
	for _, want := range []string{"llave de acceso de AWS", "token de GitHub", "llave privada", "token de Slack", "llave de Stripe",
		"URL con contraseña", "llave de API de un modelo"} {
		if _, ok := kinds[want]; !ok {
			t.Errorf("no encontró %s en %v", want, kinds)
		}
	}
	if kinds["URL con contraseña"] != 11 {
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
	// El encabezado sin cuerpo es código que lee llaves, no una llave.
	code := "String pem = key.replace(\"" + fakeKey + "\", \"\")\n    .replace(\"\\n\", \"\");\n"
	if f := Scan("JwtAdapter.java", code); len(f) > 0 {
		t.Errorf("el encabezado en el código no es una llave: %+v", f)
	}
	near := "static final String H = \"" + fakeKey + "\";\nstatic String thisIsAVeryLongMethodNameThatExceedsFortyChars() { return com/SomeOrg/SomeRepo/blob/main/docs/Keys; }\n"
	if f := Scan("Pem.java", near); len(f) > 0 {
		t.Errorf("un nombre largo o una ruta junto al encabezado no son un cuerpo: %+v", f)
	}
	doc := "Genera la llave:\n\n```\n" + fakeKey + "\n...\n```\n"
	if f := Scan("README.md", doc); len(f) > 0 {
		t.Errorf("un ejemplo sin cuerpo no es una llave: %+v", f)
	}
	// En una línea, como en la cuenta de servicio: el encabezado, \n y el cuerpo.
	sa := `{"private_key":"` + fakeKey + `\n` + fakeBody + `\n"}`
	if f := Scan("sa.json", sa); len(f) != 1 || f[0].Kind != "llave privada" {
		t.Errorf("llave en una línea: %+v", f)
	}
	if f := ScanFrom("x.pem", 40, fakeKey+"\n"+fakeBody+"\n"); len(f) != 1 || f[0].Line != 40 {
		t.Errorf("ScanFrom numera desde la primera línea: %+v", f)
	}
}

func TestConfirmPEM(t *testing.T) {
	root := t.TempDir()
	must := func(rel, content string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must("keys/public.pem", "-----BEGIN PUBLIC KEY-----\n"+fakeBody+"\n")
	must("keys/private.pem", fakeKey+"\n"+fakeBody+"\n")
	r := Rules{}
	if _, ok := r.KindAt(root, "keys/public.pem"); ok {
		t.Error("una llave pública no es un secreto")
	}
	if _, ok := r.KindAt(root, "keys/private.pem"); !ok {
		t.Error("una llave privada sí")
	}
	if _, ok := r.KindAt(root, "keys/nueva.pem"); !ok {
		t.Error("un .pem que todavía no existe cuenta como secreto")
	}
	if _, ok := r.KindAt(root, ".env"); !ok {
		t.Error(".env no necesita contenido para ser secreto")
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

func TestPKCS12SinContenido(t *testing.T) {
	root := t.TempDir()
	// Un PKCS#12 es binario: no lleva encabezado PEM y sigue siendo secreto.
	for _, name := range []string{"cert.p12", "firma.pfx"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte{0x30, 0x82, 0x0a, 0x00, 0x02, 0x01, 0x03}, 0o644); err != nil {
			t.Fatal(err)
		}
		kind, ok := Rules{}.KindAt(root, name)
		if !ok {
			t.Errorf("%s es un archivo de secretos", name)
		}
		if NeedsContent(kind) || !Confirm(kind, []byte("binario")) {
			t.Errorf("%s no se confirma por contenido (%s)", name, kind)
		}
	}
	// Un .pem declarado en secrets.files no necesita llave privada.
	if err := os.WriteFile(filepath.Join(root, "ca.pem"), []byte("-----BEGIN CERTIFICATE-----\n"+fakeBody+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := (Rules{}).KindAt(root, "ca.pem"); ok {
		t.Error("un certificado no es secreto")
	}
	if _, ok := (Rules{Files: []string{"*.pem"}}).KindAt(root, "ca.pem"); !ok {
		t.Error("un .pem declarado por el proyecto es secreto sin mirar su contenido")
	}
	// Un .pem que apunta a un dispositivo no se lee y cuenta como secreto.
	if err := os.Symlink("/dev/zero", filepath.Join(root, "cero.pem")); err != nil {
		t.Fatal(err)
	}
	if _, ok := (Rules{}).KindAt(root, "cero.pem"); !ok {
		t.Error("un .pem que no se puede leer cuenta como secreto")
	}
}

func TestPEMEnVariasFormas(t *testing.T) {
	forms := map[string]string{
		"concatenada (JS)": "const key = '" + fakeKey + "\\n' +\n  '" + fakeBody + "\\n' +\n  '-----END RSA PRIVATE KEY-----';",
		"concatenada (Py)": "KEY = (\"" + fakeKey + "\\n\"\n       \"" + fakeBody + "\\n\")",
		"aplanada":         fakeKey + " " + fakeBody + " -----END RSA PRIVATE KEY-----",
		"JSON con \\r\\n":  `{"k":"` + fakeKey + `\r\n` + fakeBody + `\r\n"}`,
		"con CRLF":         fakeKey + "\r\n" + fakeBody + "\r\n",
		"cifrada":          fakeKey + "\nProc-Type: 4,ENCRYPTED\nDEK-Info: AES-128-CBC,00\n\n" + fakeBody + "\n",
		"OpenSSH":          fakeOpenSSH + "\n" + fakeBody + "\n",
	}
	for name, text := range forms {
		f := Scan("x", text)
		if len(f) == 0 || f[0].Kind != "llave privada" {
			t.Errorf("%s: no encontró la llave: %+v", name, f)
		}
	}
}

func TestLineasLargas(t *testing.T) {
	long := strings.Repeat("a", 200<<10)
	if f := Scan("app.min.js", long+"\ntoken = "+fakeGitHub+"\n"); len(f) != 1 || f[0].Line != 2 {
		t.Errorf("una línea larga no detiene el escáner: %+v", f)
	}
	inside := strings.Repeat("x=1;", 30<<10) + "var t='" + fakeGitHub + "';" + strings.Repeat("y=2;", 30<<10)
	if f := Scan("app.min.js", inside); len(f) != 1 {
		t.Errorf("un secreto dentro de una línea larga: %+v", f)
	}
}

func TestValoresDeEjemplo(t *testing.T) {
	examples := []string{
		"aws_access_key_id = AKIA" + "IOSFODNN7EXAMPLE",
		"aws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCY" + "EXAMPLEKEY",
		"SLACK_BOT_TOKEN=xoxb-" + "your-bot-token",
		"GITHUB_TOKEN=ghp_" + strings.Repeat("x", 36),
		"AWS_ACCESS_KEY_ID=AKIA" + strings.Repeat("X", 16),
		"sk-ant-api03-" + strings.Repeat("x", 48),
	}
	for _, l := range examples {
		if f := Scan("README.md", l); len(f) > 0 {
			t.Errorf("%q es un valor de ejemplo: %+v", l, f)
		}
	}
}

func TestNamesSinFragmentosDeLlaves(t *testing.T) {
	// La última línea del cuerpo termina en = y parecería una variable.
	body := fakeBody + "\n" + "dGhpcyBpcyBhIHRlc3Qga2V5IGJvZHk" + "=\n"
	env := "DB_HOST=db\nPRIVATE_KEY=\"" + fakeKey + "\n" + body + "-----END RSA PRIVATE KEY-----\"\nAPP_ENV=prod\n"
	got := strings.Join(Names(".env", []byte(env)), ",")
	if got != "APP_ENV,DB_HOST,PRIVATE_KEY" {
		t.Errorf("nombres: %s", got)
	}
	bare := "DB_HOST=db\n" + fakeKey + "\n" + body + "-----END RSA PRIVATE KEY-----\n"
	if got := strings.Join(Names(".env", []byte(bare)), ","); got != "DB_HOST" {
		t.Errorf("una llave suelta en un .env: %s", got)
	}
	if got := strings.Join(Names(".env", []byte("QWxhZGRpbjpvcGVuU2VzYW1lMTIz=x\n")), ","); got != "" {
		t.Errorf("un tramo de base64 no es un nombre: %s", got)
	}
}
