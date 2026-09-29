package install

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestGitHubWorkflow(t *testing.T) {
	o := CIOptions{Self: CIRepo{"servicios", "acme/servicios"}, Others: []CIRepo{{"app", "acme/app"}, {"backoffice", "acme/backoffice-web"}},
		CoyoteRepo: "acme/coyote", CoyoteRef: "v0.5.0", Policy: "warn", Risk: []string{"R3=services/*/src/**/pagos/**", "R2=**/*.graphqls"}}
	wf, err := GitHubWorkflow(o)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(wf), &doc); err != nil {
		t.Fatalf("YAML inválido: %v\n%s", err, wf)
	}
	for _, want := range []string{
		"pull_request_target:", "contents: read", "pull-requests: write", "persist-credentials: false",
		"ref: ${{ github.event.pull_request.head.sha }}", "fetch-depth: 0",
		"repository: acme/app", "repository: acme/backoffice-web", "token: ${{ secrets.COYOTE_PRODUCT_TOKEN }}",
		"repository: acme/coyote", "ref: v0.5.0", "actions/checkout@11d5960a326750d5838078e36cf38b85af677262",
		"--repo servicios=repos/servicios", "--repo backoffice=repos/backoffice", "--self servicios --policy warn --comment",
		"gate pr", "--risk 'R3=services/*/src/**/pagos/**'", "--risk 'R2=**/*.graphqls'",
		"token: ${{ secrets.COYOTE_TOOL_TOKEN || secrets.COYOTE_PRODUCT_TOKEN }}", "COYOTE_TEAMS_TOKEN: ${{ secrets.COYOTE_TEAMS_TOKEN }}",
	} {
		if !strings.Contains(wf, want) {
			t.Errorf("falta %q:\n%s", want, wf)
		}
	}
	if strings.Contains(wf, "pull_request_review") || strings.Contains(wf, "  pull_request:") || strings.Count(wf, "secrets.") != 5 {
		t.Errorf("solo pull_request_target, y los secretos solo en los checkouts y en la verificación de equipos:\n%s", wf)
	}
	if !strings.Contains(wf, "types: [opened, synchronize, reopened, ready_for_review, edited]") || strings.Contains(wf, "gitleaks") {
		t.Errorf("cambiar la base vuelve a correr el chequeo; sin la bandera no hay paso de gitleaks:\n%s", wf)
	}
	// Con gitleaks: YAML válido, sin atributos del repo y los dos reportes.
	g := o
	g.Gitleaks = true
	wf, err = GitHubWorkflow(g)
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal([]byte(wf), &doc); err != nil {
		t.Fatalf("YAML inválido con gitleaks: %v\n%s", err, wf)
	}
	for _, want := range []string{
		"GIT_ATTR_SOURCE: " + emptyTree, "--remerge-diff --no-renames $BASE..$HEAD", "commit-tree \"$HEAD^{tree}\" -p \"$mb\"",
		"--ignore-gitleaks-allow", "--redact", gitleaksSHA256,
		`--gitleaks "$RUNNER_TEMP/gitleaks.json" --gitleaks-net "$RUNNER_TEMP/gitleaks-net.json"`,
	} {
		if !strings.Contains(wf, want) {
			t.Errorf("falta %q:\n%s", want, wf)
		}
	}
	// Nada sin validar entra al YAML ni al shell.
	bad := []CIOptions{o, o, o, o, o, o, o}
	bad[0].Self.Name = "x${{ secrets.X }}"
	bad[1].Others = []CIRepo{{"app", "acme/app\n  run: rm -rf /"}}
	bad[2].CoyoteRef = "main"
	bad[3].Policy = "maybe"
	bad[4].Others = []CIRepo{{"servicios", "acme/otra"}}
	bad[5].Risk = []string{"R3=x'; curl evil #"}
	bad[6].Risk = []string{"R1=**"}
	for i, b := range bad {
		if _, err := GitHubWorkflow(b); err == nil {
			t.Errorf("caso %d: debía rechazarse", i)
		}
	}
}

func TestGitHubFullName(t *testing.T) {
	for in, want := range map[string]string{
		"git@github.com:acme/servicios.git":     "acme/servicios",
		"https://github.com/acme/app":           "acme/app",
		"https://github.com/acme/app.git/":      "acme/app",
		"ssh://git@github.com/acme/web-app.git": "acme/web-app",
		"https://gitlab.com/acme/app.git":       "",
		"git@github.com:acme/app;rm -rf /.git":  "",
	} {
		got, ok := GitHubFullName(in)
		if got != want || ok != (want != "") {
			t.Errorf("%q → %q %v, se esperaba %q", in, got, ok, want)
		}
	}
}
