package gate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Emmanuel93/coyote/internal/infra"
)

// Casos de las revisiones adversariales de v0.6: cada uno se reprodujo antes
// de corregirlo.

func TestComillasNoBajanUnBloqueo(t *testing.T) {
	ps, root := secretPaths(t)
	blocked := []string{
		`terraform "apply"`, `terraform "destroy"`, `terraform state "rm" x`, `t"erraform" apply`, `terraform ap''ply`,
		`terraform $'\x61pply'`, `terraform $'\141pply'`, `a=apply; terraform $a`, `export A=apply && terraform ${A}`,
		`terraform {apply,}`, `bash -c 'terraform "apply"'`, `sh -c "gcloud auth \"print-access-token\""`,
		`gcloud auth "print-access-token"`, `kubectl get "secret" x`, `vault "read" secret/x`, `sudo -u ops terraform apply`,
		`docker run --rm -v "$PWD:/w" img sh -c 'terraform apply'`, `echo "terraform apply" | sh`,
	}
	for _, c := range blocked {
		if d := ps.Evaluate(claude(t, "Bash", map[string]any{"command": c}, root)); d.Verdict != Block {
			t.Errorf("%q se bloquea siempre: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
}

func TestGitGrepSinIndice(t *testing.T) {
	ps, root := secretPaths(t)
	bash := func(cmd string) Decision { return ps.Evaluate(claude(t, "Bash", map[string]any{"command": cmd}, root)) }
	for _, c := range []string{"git grep --no-index SUPERSECRET", "git grep --untracked --no-exclude-standard SUPERSECRET",
		"git -C . grep --no-index -e SUPERSECRET"} {
		if d := bash(c); d.Verdict == Allow || !strings.Contains(d.Reason, "secretos") {
			t.Errorf("%q lee archivos ignorados: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
	for _, c := range []string{"git grep SUPERSECRET", "git grep --untracked SUPERSECRET", "git grep -n x -- src"} {
		if d := bash(c); d.Verdict != Allow {
			t.Errorf("%q respeta .gitignore: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
}

func TestBusquedasQueAlcanzanSecretos(t *testing.T) {
	ps, root := secretPaths(t)
	bash := func(cmd string) Decision { return ps.Evaluate(claude(t, "Bash", map[string]any{"command": cmd}, root)) }
	for _, c := range []string{"grep -f.env x", "grep -xf.env x", "rg -g '*.env' KEY", "rg -g .env KEY", "rg --glob=.env* KEY",
		"rg -uu -g .env* KEY", "grep -r --include=*.env KEY .", "declare -p GITHUB_TOKEN", "typeset -p DB_PASSWORD",
		"sudo env", "env -u PATH"} {
		if d := bash(c); d.Verdict != Block {
			t.Errorf("%q: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
	for _, c := range []string{"rg -g '!*.env' KEY", "grep -e .env Makefile", "grep '.env' Makefile", "rg -g '*.go' KEY", "declare -p PATH",
		"git grep -e .env", "grep -A 2 x src/app.go"} {
		if d := bash(c); d.Verdict == Block {
			t.Errorf("%q no lee secretos: %s", c, d.Reason)
		}
	}
}

func TestMencionarNoEsTocar(t *testing.T) {
	ps, root, _ := testPaths(t)
	bash := func(cmd string) Decision { return ps.Evaluate(claude(t, "Bash", map[string]any{"command": cmd}, root)) }
	// Solo leen o solo mencionan: pasan o piden aprobación, nunca bloqueo.
	allowed := []string{
		`grep -rn "managed-settings" src`, `echo "edit coyote/project.yaml to set autonomy"`, `git log --grep=".claude/settings" --oneline`,
		`cat .github/hooks/coyote.json`, `git diff -- coyote/project.yaml`, `ls .codex/hooks`, `rg "disableAllHooks" docs`,
	}
	for _, c := range allowed {
		if d := bash(c); d.Verdict != Allow {
			t.Errorf("%q solo lee: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
	approval := []string{
		`git commit -m "docs: document .claude/settings.json and .ssh/ setup"`, `echo "configura .claude/settings.json" >> NOTAS.md`,
		`git add docs/gate.md && git commit -m "docs(gate): coyote/project.yaml y .coyote/"`,
	}
	for _, c := range approval {
		if d := bash(c); d.Verdict != NeedsApproval {
			t.Errorf("%q solo menciona rutas: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
	// Tocarlas sí se bloquea, también con comillas y leyendo credenciales.
	blocked := []string{
		`sed -i 's/reviewed/local/' coyote/infra.yaml`, `echo x > coyote/infra.yaml`, `echo '{}' > ".claude/settings.json"`,
		`cat .claude/settings.json`, `cat ~/.ssh/id_rsa`, `grep x ~/.ssh/config`, `echo '{"disableAllHooks": true}' > x.json`,
		`printf x | tee .github/hooks/coyote.json`, `echo .claude/settings.json | xargs rm`, `cp x "$(echo .claude/settings.json)"`,
	}
	for _, c := range blocked {
		if d := bash(c); d.Verdict != Block {
			t.Errorf("%q toca el gate o credenciales: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
}

func TestMakeApplyNoSeEsquiva(t *testing.T) {
	ps, root, _ := testPaths(t)
	inv, err := infra.Parse([]byte(gateInventory))
	if err != nil {
		t.Fatal(err)
	}
	ps.Infra = inv
	bash := func(cmd string) Decision { return ps.Evaluate(claude(t, "Bash", map[string]any{"command": cmd}, root)) }
	for _, c := range []string{"make -C . apply", "make ENV=prod apply", "/usr/bin/make apply", "make -f Makefile apply", "make -j4 down",
		`bash -c "make apply"`, "sudo make apply", `make "apply"`} {
		if d := bash(c); d.Verdict != Block {
			t.Errorf("%q corre un comando de apply declarado: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
	for _, c := range []string{"make plan", `grep "make apply" Makefile`, "echo make apply", "make help"} {
		if d := bash(c); d.Verdict == Block {
			t.Errorf("%q no aplica: %s", c, d.Reason)
		}
	}
}

func TestVistaDelComando(t *testing.T) {
	got := map[string]string{}
	for _, c := range []string{`terraform $'\x61pply'`, `a=apply; terraform $a`, `terraform {apply,destroy}`, `bash -c 'terraform "apply"'`,
		`git commit -m "terraform apply"`, `echo "gcloud auth print-access-token"`} {
		var lines []string
		for _, seg := range view(c, true) {
			lines = append(lines, segText(seg))
		}
		got[c] = strings.Join(lines, " ; ")
	}
	want := map[string]string{
		`terraform $'\x61pply'`:                 "terraform apply",
		`a=apply; terraform $a`:                 "a=apply ; terraform apply",
		`terraform {apply,destroy}`:             "terraform apply destroy",
		`bash -c 'terraform "apply"'`:           `bash -c terraform "apply" ; terraform apply`,
		`git commit -m "terraform apply"`:       "git commit -m _",
		`echo "gcloud auth print-access-token"`: "echo _",
	}
	for c, w := range want {
		if got[c] != w {
			t.Errorf("view(%q) = %q, se esperaba %q", c, got[c], w)
		}
	}
	// Los envoltorios y sus opciones no son el programa.
	for c, want := range map[string]string{"sudo -u ops make apply": "make", "timeout 30 terraform plan": "terraform",
		"env -u X FOO=1 go test": "go", "env": "env", "nice -n 5 make": "make"} {
		if p, _ := mainProg(strings.Fields(c)); p != want {
			t.Errorf("mainProg(%q) = %q, se esperaba %q", c, p, want)
		}
	}
}

func TestParseSearch(t *testing.T) {
	cases := []struct {
		prog, cmd string
		pattern   []string
		files     []string
		globs     []string
	}{
		{"grep", "-rn PASSWORD .", []string{"PASSWORD"}, []string{"."}, nil},
		{"grep", "-e x -e y a b", []string{"x", "y"}, []string{"a", "b"}, nil},
		{"grep", "-A 2 x f", []string{"x"}, []string{"f"}, nil},
		{"grep", "-r --include=*.env KEY .", []string{"KEY"}, []string{"."}, []string{"*.env"}},
		{"rg", "-g *.env KEY", []string{"KEY"}, nil, []string{"*.env"}},
		{"rg", "-g !*.env -t go KEY src", []string{"KEY"}, []string{"src"}, nil},
		{"grep", "x -- -f", []string{"x"}, []string{"-f"}, nil},
	}
	for _, c := range cases {
		args := strings.Fields(c.cmd)
		sp := parseSearch(c.prog, args)
		var pat, files []string
		for i := range args {
			if sp.pattern[i] {
				pat = append(pat, args[i])
			}
		}
		for _, i := range sp.files {
			files = append(files, args[i])
		}
		if strings.Join(pat, ",") != strings.Join(c.pattern, ",") || strings.Join(files, ",") != strings.Join(c.files, ",") ||
			strings.Join(sp.globs, ",") != strings.Join(c.globs, ",") {
			t.Errorf("%s %s: patrón %v, archivos %v, globs %v", c.prog, c.cmd, pat, files, sp.globs)
		}
	}
}

func TestSecretoEnDosEdiciones(t *testing.T) {
	// El encabezado solo, sin cuerpo, no es un secreto literal: pide la
	// aprobación normal, donde la persona lo ve. La llave completa en un
	// archivo la detiene el escáner al hacer commit.
	ps, root := secretPaths(t)
	f := filepath.Join(root, "src", "k.go")
	if err := os.WriteFile(f, []byte("package src\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	head := claude(t, "Edit", map[string]any{"file_path": f, "old_string": "package src", "new_string": "package src\nconst k = `-----BEGIN " + "RSA PRIVATE KEY-----"}, root)
	if d := ps.Evaluate(head); d.Verdict != NeedsApproval {
		t.Errorf("el encabezado solo pide aprobación: %s (%s)", d.Verdict, d.Reason)
	}
}

func TestParchesYLlavesPartidas(t *testing.T) {
	ps, root := secretPaths(t)
	body := strings.Repeat("MIIEvQIBADANBgkqhkiG9w0B", 3)
	header := "-----BEGIN " + "RSA PRIVATE KEY-----"
	token := "ghp_" + strings.Repeat("a1B2", 9)
	f := filepath.Join(root, "deploy", "tls.yaml")
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f, []byte("key: |\n  "+header+"\n  PEGA_AQUI\n  -----END RSA PRIVATE KEY-----\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	codex := func(patch string) Action {
		return hook(t, map[string]any{"hook_event_name": "PreToolUse", "turn_id": "t", "cwd": root, "tool_name": "apply_patch",
			"tool_input": map[string]any{"command": patch}}, "codex")
	}
	blocked := map[string]Action{
		"cuerpo bajo el encabezado del archivo": claude(t, "Edit", map[string]any{"file_path": f, "old_string": "PEGA_AQUI", "new_string": body}, root),
		"parche de Codex con ++":                codex("*** Begin Patch\n*** Add File: notas.md\n+x\n+++ token " + token + "\n*** End Patch\n"),
		"diff unificado con +++ agregada": claude(t, "mcp__fs__apply_diff", map[string]any{"path": "notas.md",
			"diff": "--- a/notas.md\n+++ b/notas.md\n@@ -1,0 +1,2 @@\n+x\n+++ token " + token + "\n"}, root),
		"encabezado de contexto y cuerpo agregado": codex("*** Begin Patch\n*** Update File: deploy/tls.yaml\n@@\n   " + header + "\n-  PEGA_AQUI\n+  " + body + "\n*** End Patch\n"),
	}
	for name, a := range blocked {
		if d := ps.Evaluate(a); d.Verdict != Block {
			t.Errorf("%s: %s (%s)", name, d.Verdict, d.Reason)
		}
	}
	// Un cuerpo lejos de cualquier encabezado es un dato cualquiera.
	g := filepath.Join(root, "src", "datos.go")
	if err := os.WriteFile(g, []byte("package src\n\nvar x = \"PEGA\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if d := ps.Evaluate(claude(t, "Edit", map[string]any{"file_path": g, "old_string": "PEGA", "new_string": body}, root)); d.Verdict != NeedsApproval {
		t.Errorf("un tramo base64 sin encabezado pide aprobación: %s (%s)", d.Verdict, d.Reason)
	}
}

func TestListadosQueAlimentanOtroPrograma(t *testing.T) {
	ps, root := secretPaths(t)
	inv, err := infra.Parse([]byte(gateInventory))
	if err != nil {
		t.Fatal(err)
	}
	ps.Infra = inv
	for _, c := range []string{"cat $(ls .env)", "ls .env | xargs cat", "find . -name '*.env' | xargs cat", "cat `find . -name .env`", "make $(echo apply)"} {
		if d := ps.Evaluate(claude(t, "Bash", map[string]any{"command": c}, root)); d.Verdict != Block {
			t.Errorf("%q: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
	for _, c := range []string{"ls -la .env", "find . -name .env", "stat .env"} {
		if d := ps.Evaluate(claude(t, "Bash", map[string]any{"command": c}, root)); d.Verdict == Block {
			t.Errorf("%q solo ve nombres: %s", c, d.Reason)
		}
	}
}
