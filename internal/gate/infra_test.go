package gate

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Emmanuel93/coyote/internal/infra"
)

const gateInventory = `version: 1
tool: terraform
environments:
  demo:
    budget: { target_usd: 100, cap_usd: 150 }
    apply: local
    match: ["ENV=demo", "minikube"]
  prod:
    budget: { target_usd: 1200, cap_usd: 1800 }
    match: ["ENV=prod", "gke_tienda-prod"]
stacks:
  - { path: stacks/gcp/prod, cloud: gcp, env: prod, status: scaffold }
commands:
  apply: ["make apply", "make down"]
`

func TestInfraGate(t *testing.T) {
	ps, root, _ := testPaths(t)
	bash := func(cmd string) Decision {
		return ps.Evaluate(claude(t, "Bash", map[string]any{"command": cmd}, root))
	}
	// Sin inventario: un agente nunca aplica Terraform u OpenTofu.
	for _, c := range []string{"terraform apply", "terraform -chdir=stacks/gcp/demo apply -auto-approve", "tofu destroy", "terraform state rm x.y", "pulumi up"} {
		if d := bash(c); d.Verdict != Block || !strings.Contains(d.Reason, "nunca aplica infraestructura") {
			t.Errorf("%q: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
	if d := bash("terraform plan -out tfplan"); d.Verdict != NeedsApproval {
		t.Errorf("terraform plan pide aprobación: %s", d.Verdict)
	}
	if d := bash("git commit -m 'docs: por qué no se corre terraform apply desde un agente'"); d.Verdict == Block {
		t.Errorf("un mensaje de commit que menciona apply no se bloquea: %s", d.Reason)
	}
	if d := bash("kubectl apply -f k8s/app.yaml"); d.Verdict != NeedsApproval || !strings.Contains(d.Reason, "un uso") {
		t.Errorf("un cambio de infraestructura sin ambiente pide aprobación de un uso: %s (%s)", d.Verdict, d.Reason)
	}
	// Con inventario: los comandos de apply declarados y los ambientes con revisor.
	inv, err := infra.Parse([]byte(gateInventory))
	if err != nil {
		t.Fatal(err)
	}
	ps.Infra = inv
	blocked := []string{"make apply ENV=demo", "make down", "kubectl --context gke_tienda-prod apply -f k8s/", "helm upgrade x ./chart --kube-context gke_tienda-prod",
		"gcloud container clusters resize demo --num-nodes 0 --project x ENV=prod"}
	for _, c := range blocked {
		if d := bash(c); d.Verdict != Block {
			t.Errorf("%q: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
	for _, c := range []string{"kubectl --context minikube apply -f k8s/", "make plan ENV=prod", "kubectl get pods --context gke_tienda-prod"} {
		if d := bash(c); d.Verdict == Block {
			t.Errorf("%q no se bloquea: %s", c, d.Reason)
		}
	}
	// Un inventario ilegible bloquea los cambios de infraestructura hasta corregirlo.
	ps.Infra, ps.InfraErr = nil, errors.New("yaml roto")
	if d := bash("kubectl apply -f k8s/"); d.Verdict != Block || !strings.Contains(d.Reason, "no se puede leer") {
		t.Errorf("inventario ilegible: %s (%s)", d.Verdict, d.Reason)
	}
	// El inventario no lo escribe un agente.
	ps.InfraErr = nil
	w := claude(t, "Write", map[string]any{"file_path": filepath.Join(root, "coyote", "infra.yaml"), "content": "x"}, root)
	if d := ps.Evaluate(w); d.Verdict != Block {
		t.Errorf("escribir coyote/infra.yaml: %s", d.Verdict)
	}
	// Tampoco los admins y el presupuesto de la organización (ADR-0018).
	w = claude(t, "Edit", map[string]any{"file_path": filepath.Join(root, "coyote", "hub.yaml"), "old_string": "admins: []", "new_string": "admins: [\"@ana\"]"}, root)
	if d := ps.Evaluate(w); d.Verdict != Block {
		t.Errorf("editar coyote/hub.yaml: %s", d.Verdict)
	}
	if d := bash("sed -i 's/monthly_usd: 100/monthly_usd: 0/' coyote/hub.yaml"); d.Verdict != Block {
		t.Errorf("sed sobre coyote/hub.yaml: %s", d.Verdict)
	}
	if !InfraEffect("helm install x ./chart") || InfraEffect("helm list") {
		t.Error("InfraEffect")
	}
	// Cargar o borrar reglas de alertas, o silenciarlas, tiene efectos (ADR-0020); revisarlas no.
	for _, c := range []string{"mimirtool rules sync coyote/slo/prometheus/pagos.yaml", "mimirtool --address=http://localhost:9009 --id=anonymous rules load r.yaml",
		"cortextool rules delete ns grupo", "mimirtool alertmanager load am.yaml", "amtool silence add alertname=PagosApi", "amtool --alertmanager.url=http://am silence expire 1234", "amtool alert add x",
		"mimirtool rules --user=1 sync --address=https://mimir.prod.example r.yaml", "mimirtool alertmanager --address=https://am load am.yaml",
		"amtool silence --alertmanager.url=https://am.prod.example add x", "amtool silence update --duration=720h 1234", `sh -c "mimirtool rules --user=1 sync r.yaml"`, "mimirtool @args.txt"} {
		if !InfraEffect(c) {
			t.Errorf("%q tiene efectos", c)
		}
	}
	for _, c := range []string{"mimirtool rules lint r.yaml", "mimirtool rules check r.yaml", "mimirtool rules list", "amtool silence query", "amtool alert query", "promtool check rules r.yaml"} {
		if InfraEffect(c) {
			t.Errorf("%q solo lee", c)
		}
	}
}

// TestInfraGateSegundaRevision: binarios con sufijo, imágenes con etiqueta,
// escrituras por HTTP a las APIs de alertas y subcomandos que llegan al
// correr.
func TestInfraGateSegundaRevision(t *testing.T) {
	ps, root, _ := testPaths(t)
	bash := func(cmd string) Decision {
		return ps.Evaluate(claude(t, "Bash", map[string]any{"command": cmd}, root))
	}
	// Sin saber el subcomando, una herramienta de apply se bloquea.
	for _, c := range []string{
		"echo apply -auto-approve | xargs terraform",
		`f() { terraform "$@"; }; f apply -auto-approve`,
		`set -- apply -auto-approve; terraform "$@"`,
		"shopt -s expand_aliases\nalias t=terraform\nt apply -auto-approve",
		"terraform ${x:-apply} -auto-approve",
		"terraform $(printf 'ap%s' ply) -auto-approve",
		"tofu $VERB",
	} {
		if d := bash(c); d.Verdict != Block {
			t.Errorf("%q: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
	// Lo que nombra terraform sin correrlo no se bloquea.
	for _, c := range []string{"cat terraform.tfvars", `git commit -m "chore: xargs terraform plan"`, "terraform plan -var \"region=$REGION\"", `echo "terraform $1"`} {
		if d := bash(c); d.Verdict == Block {
			t.Errorf("%q no se bloquea: %s", c, d.Reason)
		}
	}
	// Contra el ambiente con revisor, cargar reglas o silenciar alertas se bloquea
	// también con el binario de un release, una imagen con etiqueta, por HTTP o
	// con el subcomando en una variable.
	inv, err := infra.Parse([]byte(gateInventory + "  alertas:\n    match: [\"mimir.prod.example\", \"am.prod.example\"]\n"))
	if err != nil {
		inv, err = infra.Parse([]byte(strings.Replace(gateInventory, `match: ["ENV=prod", "gke_tienda-prod"]`, `match: ["ENV=prod", "gke_tienda-prod", "mimir.prod.example", "am.prod.example"]`, 1)))
		if err != nil {
			t.Fatal(err)
		}
	}
	ps.Infra = inv
	for _, c := range []string{
		"./mimirtool-linux-amd64 rules sync --address=https://mimir.prod.example --id=t r.yaml",
		"docker run grafana/mimirtool:2.14.0 rules sync --address=https://mimir.prod.example --id=t r.yaml",
		"docker run grafana/mimirtool@sha256:abc rules load --address=https://mimir.prod.example r.yaml",
		"curl -X POST --data-binary @r.yaml https://mimir.prod.example/prometheus/config/v1/rules/pagos",
		"curl -XDELETE https://mimir.prod.example/prometheus/config/v1/rules/pagos",
		`curl -d '{"matchers":[]}' https://am.prod.example/api/v2/silences`,
		"wget --method=DELETE https://am.prod.example/api/v2/silence/abc",
		"http POST https://am.prod.example/api/v2/silences matchers:='[]'",
		"promtool push metrics https://mimir.prod.example/api/v1/push m.txt",
		"amtool silence ${x:-add} --alertmanager.url=https://am.prod.example alertname=x",
	} {
		if d := bash(c); d.Verdict != Block {
			t.Errorf("%q: %s (%s)", c, d.Verdict, d.Reason)
		}
	}
	// Leer la API de alertas no es un efecto.
	if d := bash("curl -s https://am.prod.example/api/v2/silences"); d.Verdict == Block {
		t.Errorf("leer silencios no se bloquea: %s", d.Reason)
	}
}
