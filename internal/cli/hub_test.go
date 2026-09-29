package cli

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
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

// webPages arranca coyote web y pide varias rutas.
func webPages(t *testing.T, root string, paths ...string) map[string]string {
	t.Helper()
	old := webServe
	defer func() { webServe = old }()
	pages := map[string]string{}
	webServe = func(srv *http.Server, ln net.Listener) error {
		defer ln.Close()
		for _, p := range paths {
			req := httptest.NewRequest(http.MethodGet, p, nil)
			req.Host = "127.0.0.1"
			rec := httptest.NewRecorder()
			srv.Handler.ServeHTTP(rec, req)
			pages[p] = fmt.Sprintf("%d\n%s", rec.Code, rec.Body.String())
		}
		return nil
	}
	must(t, run(t, root, "", "web", "--addr", "127.0.0.1:0"), 0, "web")
	return pages
}

func TestWebV1ConHubYVisibilidad(t *testing.T) {
	base := setup(t)
	hubDir := filepath.Join(base, "acme-hub")
	must(t, run(t, base, "", "hub", "init", "acme-hub", "--org", "acme"), 0, "hub init")
	conf := readFile(t, filepath.Join(hubDir, "coyote/hub.yaml"))
	conf = strings.Replace(conf, "monthly_usd: 0", "monthly_usd: 50", 1)
	conf = strings.Replace(conf, "projects: []", "projects:\n  - { name: shop, path: ../shop }\n  - { name: lejos, path: ../no-esta }", 1)
	if err := os.WriteFile(filepath.Join(hubDir, "coyote/hub.yaml"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, hubDir, "add", "-A")
	git(t, hubDir, "commit", "-q", "-m", "chore(hub): organización")

	root := filepath.Join(base, "shop")
	must(t, run(t, base, "", "init", "shop", "--type", "backend", "--purpose", "API de la tienda demo", "--hub", "../acme-hub"), 0, "init")
	must(t, run(t, root, "", "record", "run", "implementa pedidos", "--agent", "coyote-dev", "--tokens", "12k/8k/1k", "--cost", "0.01+0.02", "--refs", "model:sonnet-5"), 0, "record ana")
	t.Setenv("COYOTE_USER", "luis")
	must(t, run(t, root, "", "record", "run", "revisa pagos", "--agent", "coyote-reviewer", "--tokens", "2k/0/1k", "--cost", "0.2+0.3", "--refs", "model:opus-5.5"), 0, "record luis")
	must(t, run(t, root, "", "propose", "--bash", "make deploy TOKEN=ghp_"+strings.Repeat("a", 36)), 0, "propose")
	must(t, run(t, root, "", "propose", "--bash", "make test <b>x</b>"), 0, "propose 2")
	if err := os.WriteFile(filepath.Join(root, "coyote/approvals/P-0001.json"), []byte(`{"id":"P-0001","gate":"G1","release":"v0.1.0","decision":"approved","approver":"@ana","date":"2026-09-01","authorizes":["etiquetar v0.1.0"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(root, "coyote/workstreams/W-0001-pedidos")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	plan := "id: W-0001\ntitle: pedidos\nowner: \"@ana\"\nautonomy: manual\nrisk: R2\nbudget_usd: 5\nsteps:\n  - { id: S1, does: diseña pedidos, agent: coyote-architect, max_usd: 1 }\n"
	if err := os.WriteFile(filepath.Join(ws, "plan.yaml"), []byte(plan), 0o644); err != nil {
		t.Fatal(err)
	}

	// Luis no es admin: ve lo suyo y los totales; no ve la organización.
	pages := webPages(t, root, "/?since=all", "/org", "/gate", "/workstreams", "/presupuesto")
	if p := pages["/?since=all"]; strings.Contains(p, "<td>@ana") || !strings.Contains(p, "<td>@luis") || !strings.Contains(p, "total del proyecto") {
		t.Fatalf("luis no ve el desglose de ana:\n%s", p)
	}
	if !strings.HasPrefix(pages["/org"], "403") {
		t.Fatalf("/org para luis: %s", pages["/org"][:3])
	}
	gatePage := pages["/gate"]
	if strings.Contains(gatePage, "ghp_") || !strings.Contains(gatePage, "TOKEN=***") || strings.Contains(gatePage, "<b>x</b>") || !strings.Contains(gatePage, "G1") {
		t.Fatalf("la cola se muestra sin secretos ni HTML, con los gates de release:\n%s", gatePage)
	}
	if !strings.Contains(pages["/workstreams"], "W-0001") || !strings.Contains(pages["/presupuesto"], "$50.00") {
		t.Fatalf("workstreams y presupuesto:\n%s\n%s", pages["/workstreams"], pages["/presupuesto"])
	}

	// Ana es admin del hub: ve a luis y la organización, con el proyecto sin clon.
	t.Setenv("COYOTE_USER", "ana")
	pages = webPages(t, root, "/?view=person&since=all", "/org")
	if !strings.Contains(pages["/?view=person&since=all"], "@luis") {
		t.Fatalf("ana, admin, ve el desglose:\n%s", pages["/?view=person&since=all"])
	}
	org := pages["/org"]
	if !strings.HasPrefix(org, "200") || !strings.Contains(org, "Organización acme") || !strings.Contains(org, "sin clon en esta máquina") || !strings.Contains(org, "@luis") {
		t.Fatalf("vista de la organización:\n%s", org)
	}
}

func TestWebNoMuestraSecretos(t *testing.T) {
	if got := webAction("P-1", "Bash: export K=AKIA"+"Q3VZ7T2M9KX4B8JN"); strings.Contains(got, "Q3VZ") || !strings.Contains(got, "coyote review P-1") {
		t.Fatalf("una acción con un secreto no se muestra: %s", got)
	}
	if got := webAction("P-2", "Bash: make test\n\x1b[31mrojo"); strings.ContainsAny(got, "\n\x1b") {
		t.Fatalf("una línea visible, sin controles: %q", got)
	}
}
