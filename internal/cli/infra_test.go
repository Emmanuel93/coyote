package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func iacRepo(t *testing.T, dir string) {
	t.Helper()
	write(t, dir, ".tool-versions", "terraform 1.9.8\n")
	write(t, dir, "environments/demo.tfvars", "env = \"demo\"\n")
	write(t, dir, "environments/prod.tfvars", "env = \"prod\"\n")
	write(t, dir, "stacks/gcp/demo/main.tf", "terraform {\n  backend \"gcs\" {}\n}\n")
	write(t, dir, "stacks/gcp/prod/README.md", "andamiaje\n")
	write(t, dir, "Makefile", "apply:\n\tterraform -chdir=$(STACK) apply\n\nplan:\n\tterraform plan\n")
}

const cliPlan = `{"format_version":"1.2","resource_changes":[
 {"address":"google_compute_firewall.abierto","mode":"managed","type":"google_compute_firewall","change":{"actions":["create"],"after":{"source_ranges":["0.0.0.0/0"]}}},
 {"address":"google_container_node_pool.spot","mode":"managed","type":"google_container_node_pool","change":{"actions":["create"],"after":{"node_config":[{"password":"valor-secreto"}]}}}
]}`

func TestInfraCLI(t *testing.T) {
	base := setup(t)
	repo := filepath.Join(base, "infra")
	iacRepo(t, repo)
	p := run(t, repo, "", "infra", "propose")
	must(t, p, 0, "infra propose")
	if !strings.Contains(p.stdout, "{ path: stacks/gcp/demo, cloud: gcp, env: demo, status: active }") || !strings.Contains(p.stdout, `apply: ["make apply"]`) {
		t.Errorf("propuesta:\n%s", p.stdout)
	}
	inv := filepath.Join(base, "infra.yaml")
	if err := os.WriteFile(inv, []byte(p.stdout), 0o644); err != nil {
		t.Fatal(err)
	}
	c := run(t, repo, "", "infra", "check", "--file", inv)
	must(t, c, 1, "check con presupuestos pendientes")
	for _, want := range []string{"Inventario: terraform · 2 ambientes (demo, prod)", "presupuesto pendiente", "sin dueños", "✗", "errores"} {
		if !strings.Contains(c.stdout, want) {
			t.Errorf("infra check sin %q:\n%s", want, c.stdout)
		}
	}
	fixed := strings.ReplaceAll(p.stdout, "{ target_usd: 0, cap_usd: 0 }", "{ target_usd: 100, cap_usd: 150 }")
	fixed = strings.Replace(fixed, "owners: []", `owners: ["@ana"]`, 1)
	if err := os.WriteFile(inv, []byte(fixed), 0o644); err != nil {
		t.Fatal(err)
	}
	c = run(t, repo, "", "infra", "check", "--file", inv)
	must(t, c, 0, "check completo")
	must(t, run(t, repo, "", "infra", "check"), 1, "sin coyote/infra.yaml")

	planPath := filepath.Join(base, "plan.json")
	if err := os.WriteFile(planPath, []byte(cliPlan), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"text", "md", "json"} {
		r := run(t, base, "", "infra", "plan", planPath, "--format", format)
		must(t, r, 0, "infra plan "+format)
		if !strings.Contains(r.stdout, "google_compute_firewall.abierto") || strings.Contains(r.stdout, "valor-secreto") {
			t.Errorf("infra plan %s:\n%s", format, r.stdout)
		}
	}
	if r := run(t, base, cliPlan, "infra", "plan", "-"); !strings.Contains(r.stdout, "riesgo R3") {
		t.Errorf("infra plan desde stdin:\n%s", r.stdout)
	}
	must(t, run(t, base, "{}", "infra", "plan", "-"), 1, "no es un plan")
}

func TestInfraGateYAprobacion(t *testing.T) {
	_, root := gateProject(t)
	write(t, root, "coyote/infra.yaml", "version: 1\ntool: terraform\nenvironments:\n  prod:\n    budget: { target_usd: 1, cap_usd: 2 }\n    match: [\"gke_tienda-prod\"]\ncommands:\n  apply: [\"make apply\"]\n")
	bash := func(cmd string) string {
		return hook(t, root, "Bash", map[string]any{"command": cmd}, nil)
	}
	for _, c := range []string{"terraform apply", "make apply", "kubectl --context gke_tienda-prod apply -f x.yaml"} {
		r := run(t, root, bash(c), "gate", "check")
		must(t, r, 2, c)
		if !strings.Contains(r.stderr, "bloqueado siempre") {
			t.Errorf("%q debe bloquearse siempre: %s", c, r.stderr)
		}
	}
	r := run(t, root, bash("kubectl apply -f k8s/app.yaml"), "gate", "check")
	must(t, r, 2, "kubectl sin ambiente")
	id := propRe.FindString(r.stderr)
	a := run(t, root, "", "approve", id, "--uses", "5")
	must(t, a, 0, "approve")
	if !strings.Contains(a.stdout, "un solo uso") || !strings.Contains(a.stdout, "1 uso(s)") {
		t.Errorf("un cambio de infraestructura se aprueba de a un uso:\n%s", a.stdout)
	}
	must(t, run(t, root, "", "approve", "--bash", "terraform apply"), 1, "approve --bash de un apply")
}

func TestR13EnProyectoInfra(t *testing.T) {
	base := setup(t)
	must(t, run(t, base, "", "init", "red", "--type", "infra", "--purpose", "Infraestructura de la tienda demo en la nube"), 0, "init infra")
	root := filepath.Join(base, "red")
	l := run(t, root, "", "standards", "lint")
	if !strings.Contains(l.stdout, "R13") || !strings.Contains(l.stdout, "falta el inventario") {
		t.Errorf("R13 sin inventario:\n%s", l.stdout)
	}
	iacRepo(t, root)
	p := run(t, root, "", "infra", "propose")
	doc := strings.ReplaceAll(p.stdout, "{ target_usd: 0, cap_usd: 0 }", "{ target_usd: 10, cap_usd: 20 }")
	doc = strings.Replace(doc, "owners: []", `owners: ["@ana"]`, 1)
	write(t, root, "coyote/infra.yaml", doc)
	if l = run(t, root, "", "standards", "lint"); !strings.Contains(l.stdout, "0 MUST") || !strings.Contains(l.stdout, "sin .terraform.lock.hcl") {
		t.Errorf("R13 con inventario completo: sin MUST, con el aviso del lock:\n%s", l.stdout)
	}
}

func TestGatePRConPlan(t *testing.T) {
	base := setup(t)
	productoDemo(t, base)
	svc := filepath.Join(base, "servicios")
	sha := strings.TrimSpace(git(t, svc, "rev-parse", "HEAD"))
	planPath := filepath.Join(base, "plan.json")
	if err := os.WriteFile(planPath, []byte(cliPlan), 0o644); err != nil {
		t.Fatal(err)
	}
	r := run(t, base, "", "gate", "pr", "--repo", "servicios="+svc, "--self", "servicios", "--base", sha, "--head", sha, "--event", "", "--plan", planPath)
	must(t, r, 0, "gate pr con plan (warn)")
	for _, want := range []string{"### coyote: R3", "R3 por el plan de Terraform: crear 2", "**Plan de Terraform (R3).**", "abre la red a internet"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("reporte sin %q:\n%s", want, r.stdout)
		}
	}
	if strings.Contains(r.stdout, "valor-secreto") {
		t.Error("el reporte mostró un valor del plan")
	}
}

func TestExtractInfra(t *testing.T) {
	base := setup(t)
	productoDemo(t, base)
	iac := filepath.Join(base, "infra")
	iacRepo(t, iac)
	git(t, base, "init", "-q", iac)
	root := filepath.Join(base, "producto")
	must(t, run(t, base, "", "init", "producto", "--type", "product", "--purpose", "tienda demo en tres repos"), 0, "init product")
	must(t, run(t, root, "", "repo", "add", "infra", "--path", "../infra"), 0, "repo add infra")
	r := run(t, root, "", "extract", "infra")
	must(t, r, 0, "extract infra")
	if !strings.Contains(r.stdout, "coyote/repos/infra/infra.yaml: creado") {
		t.Errorf("extract no propuso el inventario:\n%s", r.stdout)
	}
	doc := readFile(t, filepath.Join(root, "coyote", "repos", "infra", "infra.yaml"))
	if !strings.Contains(doc, "tool: terraform") || !strings.Contains(doc, "stacks/gcp/demo") {
		t.Errorf("inventario:\n%s", doc)
	}
	if r = run(t, root, "", "extract", "infra"); !strings.Contains(r.stdout, "infra.yaml: sin cambios") {
		t.Errorf("segunda vez:\n%s", r.stdout)
	}
	if st := git(t, iac, "status", "--porcelain"); !strings.Contains(st, "??") || strings.Contains(st, "coyote/") {
		t.Errorf("el repo de infraestructura no se escribe: %s", st)
	}
}
