package gate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Emmanuel93/coyote/internal/secrets"
)

var (
	testAWS = "AKIA" + "Q3VZ7T2M9KX4B8JN"
	testKey = "-----BEGIN " + "RSA PRIVATE KEY-----\n" + strings.Repeat("MIIEvQIBADANBgkqhkiG9w0B", 3)
)

func secretPaths(t *testing.T) (Paths, string) {
	t.Helper()
	ps, root, _ := testPaths(t)
	for rel, content := range map[string]string{".env": "DB_PASSWORD=x\n", ".env.example": "DB_PASSWORD=\n",
		"src/app.go": "package app\n", "config/prod/app.yaml": "db: x\n", "testdata/server.key": "x\n", "config/local.env": "A=1\n"} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ps.Secrets = secrets.Rules{Files: []string{"config/prod/*.yaml"},
		Allow: []secrets.Allow{{Path: "testdata/**", Reason: "llaves de prueba generadas"}}}
	return ps, root
}

func TestSecretFilesAreBlocked(t *testing.T) {
	ps, root := secretPaths(t)
	bash := func(cmd string) Action { return claude(t, "Bash", map[string]any{"command": cmd}, root) }
	blocked := []Action{
		claude(t, "Read", map[string]any{"file_path": filepath.Join(root, ".env")}, root),
		claude(t, "Read", map[string]any{"file_path": filepath.Join(root, "config", "prod", "app.yaml")}, root),
		claude(t, "Grep", map[string]any{"pattern": "PASSWORD", "path": ".env"}, root),
		claude(t, "Grep", map[string]any{"pattern": "BEGIN", "glob": "*.pem"}, root),
		hook(t, map[string]any{"hook_event_name": "BeforeTool", "cwd": root, "tool_name": "read_many_files",
			"tool_input": map[string]any{"paths": []any{"**/.env*"}}}, "gemini"),
		hook(t, map[string]any{"agent_action_name": "pre_read_code", "tool_info": map[string]any{"file_path": filepath.Join(root, ".env")}}, "windsurf"),
		bash("cat .env"),
		bash("head -5 config/prod/app.yaml"),
		bash("cat config/*.env*"),
		bash("cat .e*"),
		bash("cat config/prod/*"),
		bash("find . -name .env -exec cat {} +"),
		bash("cp .env /tmp/x"),
		bash("cp .env.example .env"),
		bash("source .env && npm start"),
		bash("docker run --env-file=.env imagen"),
		bash("python3 -c 'print(open(\".env\").read())'"),
		claude(t, "Write", map[string]any{"file_path": filepath.Join(root, ".env"), "content": "A=1"}, root),
		claude(t, "Edit", map[string]any{"file_path": filepath.Join(root, "terraform.tfstate"), "old_string": "a", "new_string": "b"}, root),
	}
	for _, a := range blocked {
		if d := ps.Evaluate(a); d.Verdict != Block {
			t.Errorf("%s %v debe bloquearse siempre: %s (%s)", a.Tool, a.Input, d.Verdict, d.Reason)
		}
	}
	allowed := []Action{
		claude(t, "Read", map[string]any{"file_path": filepath.Join(root, ".env.example")}, root),
		claude(t, "Read", map[string]any{"file_path": filepath.Join(root, "testdata", "server.key")}, root),
		claude(t, "Grep", map[string]any{"pattern": ".env", "path": "src"}, root),
		claude(t, "Grep", map[string]any{"pattern": "API_KEY", "glob": "*.go"}, root),
		claude(t, "Glob", map[string]any{"pattern": "**/*"}, root),
		bash("grep -rn 'process.env' src"),
		bash("git grep -n DB_PASSWORD"),
		bash("rg TODO"),
		bash("cat .env.example"),
		bash("ls -la src"),
		bash("ls -la .env"),
		bash("ls *"),
		bash("cat src/*.go"),
		bash("wc -l .env"),
		bash("find . -name '*.pem'"),
	}
	for _, a := range allowed {
		if d := ps.Evaluate(a); d.Verdict != Allow {
			t.Errorf("%s %v debe pasar: %s (%s)", a.Tool, a.Input, d.Verdict, d.Reason)
		}
	}
	// Buscar en todos los archivos de una carpeta con secretos pide aprobación.
	for _, c := range []string{"grep -rn PASSWORD .", "grep -R token", "rg --hidden PASSWORD", "rg -uu x .", "diff -r . /tmp/otro"} {
		if d := ps.Evaluate(bash(c)); d.Verdict != NeedsApproval || !strings.Contains(d.Reason, "secretos") {
			t.Errorf("%q debe pedir aprobación por los secretos: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
	if d := ps.Evaluate(bash("grep -rn PASSWORD src")); d.Verdict != Allow {
		t.Errorf("una carpeta sin secretos se busca libre: %s %s", d.Verdict, d.Reason)
	}
}

func TestSecretContentInWrites(t *testing.T) {
	ps, root := secretPaths(t)
	f := filepath.Join(root, "src", "config.go")
	blocked := []Action{
		claude(t, "Write", map[string]any{"file_path": f, "content": "const k = \"" + testAWS + "\"\n"}, root),
		claude(t, "Edit", map[string]any{"file_path": f, "old_string": "x", "new_string": testKey}, root),
		hook(t, map[string]any{"hook_event_name": "PreToolUse", "turn_id": "t", "cwd": root, "tool_name": "apply_patch",
			"tool_input": map[string]any{"command": "*** Begin Patch\n*** Add File: k.txt\n+" + strings.ReplaceAll(testKey, "\n", "\n+") + "\n*** End Patch\n"}}, "codex"),
		hook(t, map[string]any{"agent_action_name": "pre_write_code", "tool_info": map[string]any{"file_path": f,
			"edits": []any{map[string]any{"old_string": "", "new_string": "k := \"" + testAWS + "\""}}}}, "windsurf"),
	}
	for _, a := range blocked {
		d := ps.Evaluate(a)
		if d.Verdict != Block || !strings.Contains(d.Reason, "secreto literal") || strings.Contains(d.Reason, "Q3VZ") {
			t.Errorf("%s: un secreto escrito se bloquea sin mostrarlo: %s (%s)", a.Tool, d.Verdict, d.Reason)
		}
	}
	allowed := []Action{
		// Quitar un secreto no se bloquea: lo viejo no se revisa.
		claude(t, "Edit", map[string]any{"file_path": f, "old_string": testAWS, "new_string": "os.Getenv(\"AWS_KEY\")"}, root),
		// Un dato de prueba marcado pide la aprobación normal.
		claude(t, "Write", map[string]any{"file_path": f, "content": "const k = \"" + testAWS + "\" // " + secrets.AllowMarker + "\n"}, root),
	}
	for _, a := range allowed {
		if d := ps.Evaluate(a); d.Verdict != NeedsApproval {
			t.Errorf("%s: %s (%s), se esperaba aprobación", a.Tool, d.Verdict, d.Reason)
		}
	}
}

func TestCredentialCommands(t *testing.T) {
	ps, root := secretPaths(t)
	blocked := []string{
		"gcloud auth print-access-token", "gcloud --project p auth application-default print-access-token",
		"gcloud secrets versions access latest --secret=db", "gcloud iam service-accounts keys create k.json --iam-account x",
		"gcloud container clusters get-credentials demo --zone us-central1-a", "gcloud auth login",
		"aws sts get-session-token", "aws --profile prod configure get aws_secret_access_key", "aws secretsmanager get-secret-value --secret-id db",
		"aws ssm get-parameter --name /db/pass --with-decryption", "aws ecr get-login-password | docker login --password-stdin x",
		"az account get-access-token", "az keyvault secret show --name db --vault-name v",
		"kubectl get secret db -o yaml", "kubectl -n prod get secrets", "kubectl get configmaps,secrets", "oc get secret/db",
		"kubectl create token default", "kubectl config view --raw",
		"terraform output -json", "terraform -chdir=stacks/gcp/demo output", "tofu state pull", "terraform show -json plan.tfplan", "terraform console",
		"helm get values mi-release", "vault kv get secret/db", "docker inspect db", "docker compose config", "docker login ghcr.io",
		"gh auth status --show-token", "op read op://vault/db/password", "sops -d secrets.enc.yaml", "gpg --decrypt clave.gpg",
		"cat /proc/self/environ", "env", "env | grep TOKEN", "printenv", "printenv GITHUB_TOKEN", "export -p", "declare -x", "set",
		"echo $GITHUB_TOKEN", "printf '%s' \"$API_KEY\"", "echo ${apiKey}", "printenv DB_PASSWORD", "bash -c 'gcloud auth print-access-token'",
		// Las anotaciones de un secreto aplicado con kubectl apply llevan sus datos.
		"kubectl describe secret db",
	}
	for _, c := range blocked {
		d := ps.Evaluate(claude(t, "Bash", map[string]any{"command": c}, root))
		if d.Verdict != Block {
			t.Errorf("%q imprime credenciales y debe bloquearse: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
	notBlocked := []string{
		"kubectl get pods", "kubectl get pods -l app=secret-rotator", "kubectl describe pod db", "terraform plan -out tfplan",
		"terraform fmt -check", "helm template ./chart", "printenv PATH", "env FOO=1 make build", "set -euo pipefail",
		"export FOO=1", "echo $HOME", "echo $AUTHOR_NAME", "echo $PWD", "git commit -m 'docs: explica por qué no se corre terraform output'",
		"coyote note \"nunca corras gcloud auth print-access-token\" --type inv", "curl -H \"Authorization: Bearer $GITHUB_TOKEN\" https://api.github.com/user",
	}
	for _, c := range notBlocked {
		if d := ps.Evaluate(claude(t, "Bash", map[string]any{"command": c}, root)); d.Verdict == Block {
			t.Errorf("%q no imprime credenciales: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
	// Una herramienta MCP que corre un comando también.
	mcp := claude(t, "mcp__shell__run", map[string]any{"command": "kubectl get secret db -o json"}, root)
	if d := ps.Evaluate(mcp); d.Verdict != Block {
		t.Errorf("MCP con credenciales: %s %s", d.Verdict, d.Reason)
	}
}

// TestLecturaConCorchetesYComodinesLiterales: sexta revisión de v1.0. Una ruta
// con [, ? o * se tomaba por un patrón y la herramienta de lectura no la
// miraba: Read de "[x].env", de un archivo declarado en secrets.files con un
// corchete en el nombre, o de cualquier archivo de un proyecto cuya carpeta
// (o cuyo home) lleva corchetes ("Mi app [v2]") pasaba sin pedir nada.
func TestLecturaConCorchetesYComodinesLiterales(t *testing.T) {
	home := filepath.Join(t.TempDir(), "Juan [Trabajo]")
	root := filepath.Join(home, "Documents", "Mi app [v2]")
	files := map[string]string{
		filepath.Join(root, ".env"):                   "A=1\n",
		filepath.Join(root, "[x].env"):                "A=1\n",
		filepath.Join(root, "src", "app.go"):          "package app\n",
		filepath.Join(root, "src", "[a].go"):          "package a\n",
		filepath.Join(root, "claves", "[z].txt"):      "k\n",
		filepath.Join(root, "claves", "ok?.txt"):      "k\n",
		filepath.Join(root, ".git", "HEAD"):           "ref: refs/heads/main\n",
		filepath.Join(home, ".ssh", "id_rsa"):         "x\n",
		filepath.Join(home, ".config", "coyote", "k"): "x\n",
	}
	for p, content := range files {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ps := NewPaths(root, home, filepath.Join(home, ".config", "coyote"))
	ps.Secrets = secrets.Rules{Files: []string{"claves/*.txt"}}
	read := func(tool, key, p string) Action {
		return claude(t, tool, map[string]any{key: p}, root)
	}
	for _, a := range []Action{
		read("Read", "file_path", filepath.Join(root, ".env")),
		read("Read", "file_path", filepath.Join(root, "[x].env")),
		read("Read", "file_path", "[x].env"),
		read("Read", "file_path", filepath.Join(root, ".en?")),
		read("Read", "file_path", ".e*"),
		read("Read", "file_path", filepath.Join(root, "claves", "[z].txt")),
		read("Read", "file_path", filepath.Join(root, "claves", "ok?.txt")),
		read("Read", "file_path", filepath.Join(home, ".ssh", "id_rsa")),
		read("Read", "file_path", filepath.Join(home, ".config", "coyote", "k")),
	} {
		if d := ps.Evaluate(a); d.Verdict != Block {
			t.Errorf("%s %v debe bloquearse siempre: %s (%s)", a.Tool, a.Input, d.Verdict, d.Reason)
		}
	}
	// Lo mismo en el shell: la carpeta con corchetes no es una clase de
	// caracteres, y un comodín que no alcanza nada deja el nombre literal.
	bash := func(cmd string) Action { return claude(t, "Bash", map[string]any{"command": cmd}, root) }
	for _, c := range []string{"cat .e*", "cat ./.e?v", "cat .e[n]v", "cat [x].env", "cat claves/ok?.txt", "cat claves/*.txt",
		"cat " + filepath.Join(home, ".ssh", "id_*")} {
		if d := ps.Evaluate(bash(c)); d.Verdict != Block {
			t.Errorf("%q debe bloquearse siempre: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
	// Y lo que protege el gate de cualquier escritura.
	for _, c := range []string{"rm -r .g*", "rm -r ./.gi?"} {
		if d := ps.Evaluate(bash(c)); d.Verdict != Block {
			t.Errorf("%q debe bloquearse siempre: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
	for _, c := range []string{"cat src/*.go", "cat src/a*", "ls *", "wc -l src/[a].go"} {
		if d := ps.Evaluate(bash(c)); d.Verdict != Allow {
			t.Errorf("%q debe pasar: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
	for _, a := range []Action{
		read("Read", "file_path", filepath.Join(root, "src", "app.go")),
		read("Read", "file_path", filepath.Join(root, "src", "[a].go")),
		claude(t, "LS", map[string]any{"path": root, "ignore": []any{"*.env", "[x].env"}}, root),
		claude(t, "Grep", map[string]any{"pattern": "TODO", "path": filepath.Join(root, "src"), "exclude": "*.env"}, root),
	} {
		if d := ps.Evaluate(a); d.Verdict != Allow {
			t.Errorf("%s %v debe pasar: %s (%s)", a.Tool, a.Input, d.Verdict, d.Reason)
		}
	}
}

// TestGobiernoDeProyectosAnidadosProtegido: sexta revisión de v1.0. Las
// herramientas de archivos protegían el coyote/project.yaml, las aprobaciones
// y el ledger de la raíz y no los de un proyecto anidado: un agente podía
// escribir una dispensa en el project.yaml de svc/ y dejar de proteger sus
// secretos.
func TestGobiernoDeProyectosAnidadosProtegido(t *testing.T) {
	ps, root, _ := testPaths(t)
	// Son proyectos: tienen su project.yaml. Donde no lo hay, un ledger o una
	// carpeta approvals es código de cualquiera (TestCarpetasConNombreDeGobiernoSinProyecto).
	mkTree(t, root, map[string]string{"svc/coyote/project.yaml": "x\n", "a/b/coyote/project.yaml": "x\n"})
	w := func(tool, rel string) Action {
		in := map[string]any{"file_path": filepath.Join(root, filepath.FromSlash(rel)), "content": "x"}
		if tool == "Edit" {
			in = map[string]any{"file_path": filepath.Join(root, filepath.FromSlash(rel)), "old_string": "a", "new_string": "b"}
		}
		return claude(t, tool, in, root)
	}
	for _, a := range []Action{
		w("Write", "svc/coyote/project.yaml"),
		w("Edit", "svc/coyote/project.yaml"),
		w("Write", "svc/Coyote/Project.YAML"),
		w("Write", "a/b/coyote/infra.yaml"),
		w("Write", "a/b/coyote/hub.yaml"),
		w("Write", "svc/coyote/approvals/P-0001.json"),
		w("Write", "svc/coyote/ledger/2026/10/11-ana.ccf"),
	} {
		if d := ps.Evaluate(a); d.Verdict != Block {
			t.Errorf("%s %v debe bloquearse siempre: %s (%s)", a.Tool, a.Input, d.Verdict, d.Reason)
		}
	}
	for _, a := range []Action{
		w("Write", "svc/src/main.go"),
		w("Write", "svc/coyote/standards/propio.md"),
		w("Write", "svc/docs/coyote/notas.md"),
		w("Write", "svc/coyote"),
	} {
		if d := ps.Evaluate(a); d.Verdict == Block {
			t.Errorf("%s %v no toca el gobierno de un proyecto: %s (%s)", a.Tool, a.Input, d.Verdict, d.Reason)
		}
	}
}

// TestCarpetaDeGobiernoDeUnProyectoAnidado: sexta revisión de v1.0. Mover,
// borrar o reescribir la carpeta coyote/ de un proyecto anidado (mv svc/coyote
// svc/x, rm -rf svc/coyote) sacaba al proyecto del escaneo de secretos y solo
// pedía la aprobación normal; la de la raíz se bloquea siempre. Una carpeta
// que se llama coyote y no tiene el gobierno de un proyecto (cmd/coyote)
// sigue siendo una carpeta cualquiera.
func TestCarpetaDeGobiernoDeUnProyectoAnidado(t *testing.T) {
	ps, root, _ := testPaths(t)
	for _, d := range []string{"coyote/ledger", "svc/coyote/ledger", "svc/coyote/approvals", "svc/docs/coyote", "cmd/coyote", "otro/coyote"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"coyote/project.yaml", "svc/coyote/project.yaml", "svc/docs/coyote/notas.md", "cmd/coyote/main.go"} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(f)), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Un proyecto hermano, fuera de este: su gobierno no es de este gate.
	hermano := filepath.Join(filepath.Dir(root), "hermano", "coyote")
	if err := os.MkdirAll(hermano, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hermano, "project.yaml"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	blocked := []string{
		"mv svc/coyote svc/coyote.bak", "rm -rf svc/coyote", "git mv svc/coyote svc/old", "cd svc && mv coyote c",
		"cd svc && rm -rf coyote", "mv ./svc/coyote/ /tmp/c", "tar -xf /tmp/e.tar -C svc/coyote", "cd svc/coyote && tar -xf /tmp/e.tar",
		"find svc/coyote -name project.yaml -exec sed -i s/a/b/ {} +", "cp -r /tmp/x svc/coyote", "chmod -R a+w svc/coyote",
		"rm -rf " + filepath.Join(root, "svc", "coyote"), "mv svc/coyote svc/c && cp /tmp/x svc/c/project.yaml",
		// git mv y git rm son mv y rm: también en la raíz. Y extraer en la carpeta donde se está.
		"git rm -r svc/coyote", "git -C svc mv coyote c", "git mv coyote c", "git rm -rf coyote", "cd coyote && tar -xf /tmp/e.tar",
		"cd svc && cd coyote && unzip /tmp/e.zip", "tar --directory=svc/coyote -xf /tmp/e.tar", "cd svc && tar -x --directory=coyote -f /tmp/e.tar",
	}
	for _, c := range blocked {
		if d := ps.Evaluate(claude(t, "Bash", map[string]any{"command": c}, root)); d.Verdict != Block {
			t.Errorf("%q debería bloquearse siempre: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
	for _, c := range []string{"cp -r svc/coyote /tmp/respaldo", "tar -czf /tmp/r.tgz svc/coyote", "ls svc/coyote", "cat svc/coyote/project.yaml",
		"rm -rf cmd/coyote", "mv svc/docs/coyote svc/docs/c", "mv otro/coyote otro/c", "rm -rf ../hermano/coyote", "git rm -r --cached cmd/coyote",
		"git mv svc/docs/coyote svc/docs/c", "cd cmd/coyote && tar -xf /tmp/e.tar", "git status", "git rm README.md",
		// Una ruta absoluta de afuera no es la del proyecto aunque el mismo nombre exista adentro.
		"rm -rf /svc/coyote"} {
		if d := ps.Evaluate(claude(t, "Bash", map[string]any{"command": c}, root)); d.Verdict == Block {
			t.Errorf("%q no toca el gobierno de un proyecto: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
}
