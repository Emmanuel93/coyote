package infra

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// demoRepo arma un repo de Terraform como los de la vida real: ambientes en
// var-files, un stack activo con backend remoto y otros de andamiaje.
func demoRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, root, ".tool-versions", "terraform 1.9.8\nkubectl 1.31.0\n")
	write(t, root, "environments/demo.tfvars", "env = \"demo\"\n")
	write(t, root, "environments/prod.tfvars", "env = \"prod\"\n")
	write(t, root, "stacks/gcp/demo/main.tf", "terraform {\n  backend \"gcs\" {\n    bucket = \"estado\"\n  }\n}\n")
	write(t, root, "stacks/gcp/demo/.terraform.lock.hcl", "# lock\n")
	write(t, root, "stacks/gcp/prod/README.md", "andamiaje\n")
	write(t, root, "stacks/aws/demo/.gitkeep", "")
	write(t, root, "Makefile", "ENV ?= demo\n\nplan:\n\tterraform -chdir=$(STACK) plan\n\napply:\n\tterraform -chdir=$(STACK) apply\n\ndestroy:\n\tterraform -chdir=$(STACK) destroy\n\nup:\n\t./scripts/toggle.sh up\n\nfmt:\n\tterraform fmt -recursive\n")
	return root
}

const validInventory = `version: 1
tool: terraform
versions: .tool-versions
owners: ["@ana"]
environments:
  demo:
    var_file: environments/demo.tfvars
    budget: { target_usd: 100, cap_usd: 150 }
    apply: local
    match: ["ENV=demo"]
  prod:
    var_file: environments/prod.tfvars
    budget: { target_usd: 1200, cap_usd: 1800 }
    match: ["ENV=prod", "gke_tienda-prod"]
stacks:
  - { path: stacks/gcp/demo, cloud: gcp, env: demo, status: active }
  - { path: stacks/gcp/prod, cloud: gcp, env: prod, status: scaffold }
  - { path: stacks/aws/demo, cloud: aws, env: demo, status: scaffold }
commands:
  apply: ["make apply", "make destroy", "make up"]
`

func TestParseAndValidate(t *testing.T) {
	inv, err := Parse([]byte(validInventory))
	if err != nil {
		t.Fatal(err)
	}
	if inv.Environments["prod"].Apply != Reviewed {
		t.Error("un ambiente sin apply se aplica con revisor")
	}
	bad := map[string]string{
		"versión":         strings.Replace(validInventory, "version: 1", "version: 2", 1),
		"herramienta":     strings.Replace(validInventory, "tool: terraform", "tool: ansible", 1),
		"apply":           strings.Replace(validInventory, "apply: local", "apply: yolo", 1),
		"presupuesto":     strings.Replace(validInventory, "target_usd: 100, cap_usd: 150", "target_usd: 200, cap_usd: 150", 1),
		"ambiente":        strings.Replace(validInventory, "env: prod, status: scaffold", "env: qa, status: scaffold", 1),
		"estado":          strings.Replace(validInventory, "status: active", "status: vivo", 1),
		"ruta":            strings.Replace(validInventory, "path: stacks/gcp/demo", "path: ../fuera", 1),
		"dueño":           strings.Replace(validInventory, `owners: ["@ana"]`, `owners: ["ana"]`, 1),
		"campo":           validInventory + "sorpresa: 1\n",
		"marca corta":     strings.Replace(validInventory, `match: ["ENV=demo"]`, `match: ["d"]`, 1),
		"sin ambientes":   "version: 1\ntool: terraform\nenvironments: {}\n",
		"var_file afuera": strings.Replace(validInventory, "var_file: environments/demo.tfvars", "var_file: /etc/passwd", 1),
	}
	for name, doc := range bad {
		if _, err := Parse([]byte(doc)); err == nil {
			t.Errorf("%s: se aceptó un inventario inválido", name)
		}
	}
}

func TestCheck(t *testing.T) {
	root := demoRepo(t)
	inv, err := Parse([]byte(validInventory))
	if err != nil {
		t.Fatal(err)
	}
	if found := Check(root, inv, nil); len(found) != 0 {
		t.Errorf("el inventario coincide con el repo: %+v", found)
	}
	// Lo que se rompe: un stack activo sin backend, un var-file que falta,
	// presupuesto pendiente, tfstate en git y un stack sin declarar.
	write(t, root, "stacks/gcp/demo/main.tf", "resource \"x\" \"y\" {}\n")
	write(t, root, "modules/red/backend.tf", "terraform {\n  backend \"s3\" {}\n}\n")
	os.Remove(filepath.Join(root, "environments/prod.tfvars"))
	inv.Environments["demo"].Budget = &Budget{}
	inv.Environments["prod"].Budget = nil
	found := Check(root, inv, []string{"stacks/gcp/demo/terraform.tfstate", "stacks/gcp/demo/main.tf"})
	text := ""
	for _, f := range found {
		text += f.Level + " " + f.Where + ": " + f.Msg + "\n"
	}
	for _, want := range []string{"error stacks stacks/gcp/demo: sin backend", "error environments.prod: var_file environments/prod.tfvars no existe",
		"error environments.demo: presupuesto pendiente", "error environments.prod: sin presupuesto",
		"error stacks/gcp/demo/terraform.tfstate: estado de Terraform en git", "aviso stacks: modules/red tiene un backend y no está en el inventario",
		"aviso stacks stacks/gcp/demo: .terraform.lock.hcl no está en git"} {
		if !strings.Contains(text, want) {
			t.Errorf("falta %q en:\n%s", want, text)
		}
	}
	write(t, root, "stacks/gcp/demo/main.tf", "terraform {\n  backend \"local\" {}\n}\n")
	if found := Check(root, inv, nil); !strings.Contains(findingsText(found), "backend \"local\": el estado debe ser remoto") {
		t.Errorf("un backend local no es remoto:\n%s", findingsText(found))
	}
}

func findingsText(fs []Finding) string {
	var b strings.Builder
	for _, f := range fs {
		b.WriteString(f.Level + " " + f.Where + ": " + f.Msg + "\n")
	}
	return b.String()
}

func TestProposeAndDetect(t *testing.T) {
	root := demoRepo(t)
	if tool, ok := Detect(root); !ok || tool != "terraform" {
		t.Fatalf("Detect: %s %v", tool, ok)
	}
	if _, ok := Detect(t.TempDir()); ok {
		t.Error("una carpeta vacía no es de infraestructura")
	}
	inv, err := Propose(root)
	if err != nil {
		t.Fatal(err)
	}
	doc := inv.YAML("infra@abc1234")
	for _, want := range []string{"tool: terraform", "versions: .tool-versions", "demo:", "prod:", "var_file: environments/prod.tfvars",
		"apply: reviewed", `match: ["ENV=prod"]`, "{ path: stacks/gcp/demo, cloud: gcp, env: demo, status: active }",
		"{ path: stacks/gcp/prod, cloud: gcp, env: prod, status: scaffold }", "{ path: stacks/aws/demo, cloud: aws, env: demo, status: scaffold }",
		`apply: ["make apply", "make destroy", "make up"]`, "pendiente: meta y tope", "Propuesto por coyote extract desde infra@abc1234"} {
		if !strings.Contains(doc, want) {
			t.Errorf("la propuesta no tiene %q:\n%s", want, doc)
		}
	}
	// La propuesta es válida y la revisión pide lo que falta: dueños y presupuestos.
	back, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("la propuesta no es válida: %v\n%s", err, doc)
	}
	text := findingsText(Check(root, back, nil))
	if !strings.Contains(text, "presupuesto pendiente") || !strings.Contains(text, "sin dueños") {
		t.Errorf("la revisión de la propuesta:\n%s", text)
	}
}

func TestPlan(t *testing.T) {
	plan := `{"format_version":"1.2","terraform_version":"1.9.8","resource_changes":[
 {"address":"google_container_node_pool.spot","mode":"managed","type":"google_container_node_pool","change":{"actions":["create"],"after":{"node_count":1}}},
 {"address":"google_compute_network.vpc","mode":"managed","type":"google_compute_network","change":{"actions":["update"],"after":{"name":"vpc"}}},
 {"address":"google_sql_database_instance.db","mode":"managed","type":"google_sql_database_instance","change":{"actions":["update"],"after":{"deletion_protection":false,"settings":[{"tier":"db-g1-small"}]}}},
 {"address":"google_project_iam_member.tf","mode":"managed","type":"google_project_iam_member","change":{"actions":["create"],"after":{"role":"roles/editor"}}},
 {"address":"google_compute_firewall.ssh","mode":"managed","type":"google_compute_firewall","change":{"actions":["create"],"after":{"source_ranges":["0.0.0.0/0"]}}},
 {"address":"google_kms_crypto_key.k","mode":"managed","type":"google_kms_crypto_key","change":{"actions":["create"],"after":{}}},
 {"address":"google_storage_bucket_iam_binding.pub","mode":"managed","type":"google_storage_bucket_iam_binding","change":{"actions":["create"],"after":{"members":["allUsers"]}}},
 {"address":"google_compute_instance.vm","mode":"managed","type":"google_compute_instance","change":{"actions":["delete","create"],"after":{"password":"no-se-muestra"}}},
 {"address":"google_redis_instance.old","mode":"managed","type":"google_redis_instance","change":{"actions":["delete"],"after":null}},
 {"address":"google_compute_address.ip","mode":"managed","type":"google_compute_address","change":{"actions":["no-op"],"after":{}}},
 {"address":"data.google_project.p","mode":"data","type":"google_project","change":{"actions":["read"],"after":{}}}
]}`
	s, err := ReadPlan(strings.NewReader(plan))
	if err != nil {
		t.Fatal(err)
	}
	if s.Risk != "R3" || s.Headline() != "crear 5, cambiar 2, reemplazar 1, destruir 1" {
		t.Errorf("resumen: %s %s", s.Risk, s.Headline())
	}
	why := map[string]string{}
	risk := map[string]string{}
	for _, c := range s.Changes {
		why[c.Address], risk[c.Address] = c.Why, c.Risk
	}
	checks := map[string]string{
		"google_project_iam_member.tf":          "permisos (IAM)",
		"google_compute_firewall.ssh":           "abre la red a internet (0.0.0.0/0)",
		"google_kms_crypto_key.k":               "llaves o secretos",
		"google_storage_bucket_iam_binding.pub": "permisos (IAM)",
		"google_compute_instance.vm":            "se reemplaza",
		"google_redis_instance.old":             "se destruye",
		"google_sql_database_instance.db":       "datos sin protección contra borrado",
	}
	for addr, w := range checks {
		if why[addr] != w || risk[addr] != "R3" {
			t.Errorf("%s: %s %q, se esperaba R3 %q", addr, risk[addr], why[addr], w)
		}
	}
	if risk["google_container_node_pool.spot"] != "R2" || risk["google_compute_network.vpc"] != "R2" {
		t.Errorf("crear o cambiar sin más es R2: %v", risk)
	}
	if _, ok := risk["google_compute_address.ip"]; ok {
		t.Error("un recurso sin cambios no cuenta")
	}
	if _, ok := risk["data.google_project.p"]; ok {
		t.Error("un data source no cuenta")
	}
	if strings.Join(s.Costly, ",") != "google_compute_instance,google_container_node_pool,google_sql_database_instance" {
		t.Errorf("costo: %v", s.Costly)
	}
	if strings.Contains(strings.Join(s.Reasons(), ","), "no-se-muestra") {
		t.Error("un valor del plan salió en el resumen")
	}
	for _, bad := range []string{"", "{}", "no json", `{"resource_changes":[]}`} {
		if _, err := ReadPlan(strings.NewReader(bad)); err == nil {
			t.Errorf("%q no es un plan", bad)
		}
	}
	empty, err := ReadPlan(strings.NewReader(`{"format_version":"1.2","resource_changes":[]}`))
	if err != nil || empty.Risk != "R1" || empty.Headline() != "el plan no cambia recursos" {
		t.Errorf("plan vacío: %+v %v", empty, err)
	}
}

func TestCommands(t *testing.T) {
	apply := []string{"terraform apply", "terraform -chdir=stacks/gcp/demo apply -var-file=x", "tofu destroy -auto-approve",
		"terraform import google_x.y id", "terraform state rm google_x.y", "terraform force-unlock 123", "terragrunt run-all apply",
		"pulumi up --yes", "cdk deploy", "terraform workspace delete prod"}
	for _, c := range apply {
		if _, ok := ApplyCommand(c); !ok {
			t.Errorf("%q aplica infraestructura", c)
		}
	}
	notApply := []string{"terraform plan -out tfplan", "terraform init", "terraform fmt -check", "terraform validate", "tofu providers",
		"terraform state list", "git commit -m 'arregla terraform'", "echo terraform"}
	for _, c := range notApply {
		if _, ok := ApplyCommand(c); ok {
			t.Errorf("%q no aplica infraestructura", c)
		}
	}
	effects := []string{"kubectl apply -f k8s/", "kubectl --context gke_tienda-prod delete pod x", "helm upgrade --install x ./chart",
		"gcloud compute instances create vm", "gcloud container clusters resize demo --num-nodes 0", "aws ec2 terminate-instances --instance-ids i-1",
		"aws s3 rm s3://b/k", "az vm deallocate -g g -n n", "gsutil rm gs://b/k"}
	for _, c := range effects {
		if !Effect(c) {
			t.Errorf("%q cambia infraestructura", c)
		}
	}
	for _, c := range []string{"kubectl get pods", "helm list", "gcloud compute instances list", "aws s3 ls", "az vm list", "gsutil ls gs://b"} {
		if Effect(c) {
			t.Errorf("%q solo lee", c)
		}
	}
	inv, err := Parse([]byte(validInventory))
	if err != nil {
		t.Fatal(err)
	}
	if c, ok := inv.DeclaredApply("make apply ENV=prod"); !ok || c != "make apply" {
		t.Errorf("make apply es un comando de apply declarado: %q %v", c, ok)
	}
	if _, ok := inv.DeclaredApply("make plan ENV=prod"); ok {
		t.Error("make plan no aplica")
	}
	cases := map[string]string{
		"make apply ENV=prod":                              "prod",
		"terraform -chdir=stacks/gcp/prod plan":            "prod",
		"terraform plan -var-file=environments/demo.tfvars": "demo",
		"kubectl --context gke_tienda-prod apply -f x.yaml": "prod",
		"make apply ENV=production":                         "",
		"kubectl apply -f x.yaml":                           "",
	}
	for c, want := range cases {
		if got, _ := inv.EnvFor(c); got != want {
			t.Errorf("EnvFor(%q) = %q, se esperaba %q", c, got, want)
		}
	}
}
