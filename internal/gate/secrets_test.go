package gate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Emmanuel93/coyote/internal/secrets"
)

var (
	testAWS = "AKIA" + "IOSFODNN7EXAMPLE"
	testKey = "-----BEGIN " + "RSA PRIVATE KEY-----"
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
			"tool_input": map[string]any{"command": "*** Begin Patch\n*** Add File: k.pem\n+" + testKey + "\n*** End Patch\n"}}, "codex"),
		hook(t, map[string]any{"agent_action_name": "pre_write_code", "tool_info": map[string]any{"file_path": f,
			"edits": []any{map[string]any{"old_string": "", "new_string": "k := \"" + testAWS + "\""}}}}, "windsurf"),
	}
	for _, a := range blocked {
		d := ps.Evaluate(a)
		if d.Verdict != Block || !strings.Contains(d.Reason, "secreto literal") || strings.Contains(d.Reason, "EXAMPLE") {
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
	}
	for _, c := range blocked {
		d := ps.Evaluate(claude(t, "Bash", map[string]any{"command": c}, root))
		if d.Verdict != Block {
			t.Errorf("%q imprime credenciales y debe bloquearse: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
	notBlocked := []string{
		"kubectl get pods", "kubectl get pods -l app=secret-rotator", "kubectl describe secret db", "terraform plan -out tfplan",
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
