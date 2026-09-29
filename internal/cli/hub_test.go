package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHubDeLaOrganizacion(t *testing.T) {
	base := setup(t)
	hubDir := filepath.Join(base, "acme-hub")
	r := run(t, base, "", "hub", "init", "acme-hub", "--org", "acme")
	must(t, r, 0, "hub init")
	for _, f := range []string{"coyote/hub.yaml", "domains/README.md", "coyote/standards/rules.yaml", "AGENTS.md"} {
		if _, err := os.Stat(filepath.Join(hubDir, f)); err != nil {
			t.Fatalf("hub init no creó %s", f)
		}
	}
	conf := readFile(t, filepath.Join(hubDir, "coyote/hub.yaml"))
	if !strings.Contains(conf, "org: acme") || !strings.Contains(conf, `admins: ["@ana"]`) {
		t.Fatalf("hub.yaml:\n%s", conf)
	}
	if !strings.Contains(readFile(t, filepath.Join(hubDir, "coyote/project.yaml")), "type: hub") {
		t.Fatal("el hub es un proyecto coyote de tipo hub")
	}
	// Sin commit, el hub no rige.
	must(t, run(t, hubDir, "", "hub", "status"), 1, "hub sin commits")

	// La organización agrega una regla, un proyecto y un tope, y hace commit.
	if err := os.WriteFile(filepath.Join(hubDir, "coyote/hub.yaml"), []byte(strings.Replace(conf, "projects: []", "projects:\n  - { name: shop, path: ../shop }\n  - { name: infra, path: ../infra }", 1)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rules := "version: 1\nextends: coyote:default\nrules:\n  - id: ORG1\n    title: Todo servicio declara sus SLOs\n    level: SHOULD\n"
	if err := os.WriteFile(filepath.Join(hubDir, "coyote/standards/rules.yaml"), []byte(rules), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, hubDir, "add", "-A")
	git(t, hubDir, "commit", "-q", "-m", "chore(hub): estándar y proyectos de la organización")

	shop := filepath.Join(base, "shop")
	must(t, run(t, base, "", "init", "shop", "--type", "backend", "--purpose", "API de la tienda demo", "--hub", "../acme-hub"), 0, "init con hub")
	if err := os.WriteFile(filepath.Join(shop, "coyote/standards/rules.yaml"), []byte("version: 1\nextends: hub\nrules: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r = run(t, shop, "", "standards", "show")
	must(t, r, 0, "standards show")
	if !strings.Contains(r.stdout, "hub acme (main@") || !strings.Contains(r.stdout, "ORG1") {
		t.Fatalf("la capa del hub se ve con su commit:\n%s", r.stdout)
	}
	r = run(t, shop, "", "hub", "status", "--json")
	must(t, r, 0, "hub status")
	var st struct {
		Org      string
		Admins   []string
		Projects []hubProject
	}
	if err := json.Unmarshal([]byte(r.stdout), &st); err != nil {
		t.Fatal(err)
	}
	if st.Org != "acme" || len(st.Projects) != 2 || st.Projects[0].State != "sigue el hub" || st.Projects[1].State != "sin clon" {
		t.Fatalf("hub status: %+v", st)
	}
	r = run(t, shop, "", "doctor")
	if !strings.Contains(r.stdout, "hub") || !strings.Contains(r.stdout, "acme (main@") {
		t.Fatalf("doctor muestra el hub:\n%s", r.stdout)
	}

	// Un cambio sin commit en el hub no rige; el clon que falta es un error.
	if err := os.WriteFile(filepath.Join(hubDir, "coyote/standards/rules.yaml"), []byte("version: 1\nrules: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r = run(t, shop, "", "standards", "show"); !strings.Contains(r.stdout, "ORG1") {
		t.Fatalf("lo que no tiene commit en el hub no rige:\n%s", r.stdout)
	}
	if err := os.Rename(hubDir, hubDir+".bak"); err != nil {
		t.Fatal(err)
	}
	r = run(t, shop, "", "standards", "lint")
	if r.code == 0 || !strings.Contains(r.stderr+r.stdout, "no encuentro el clon") {
		t.Fatalf("un hub declarado que no se puede leer es un error:\n%s%s", r.stdout, r.stderr)
	}

	// init valida lo que escribe.
	must(t, run(t, base, "", "init", "otro", "--hub", "https://github.com/acme/hub"), 1, "hub como URL")
	must(t, run(t, base, "", "hub", "init", "h2", "--org", "acme: x"), 1, "organización inválida")
}
