package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

const sloPedidos = `version: 1
service: pedidos-service
labels: { team: pedidos }
slos:
  - name: disponibilidad
    objective: 99.9
    description: Pedidos sin error 5xx
    sli:
      errors: sum(rate(http_server_request_duration_seconds_count{job="pedidos-service",http_response_status_code=~"5.."}[{{.window}}]))
      total: sum(rate(http_server_request_duration_seconds_count{job="pedidos-service"}[{{.window}}]))
    alerts:
      runbook: coyote/runbooks/pedidos-disponibilidad.md
      page: { labels: { severity: page } }
      ticket: { labels: { severity: ticket } }
`

func TestSLOsComoCodigo(t *testing.T) {
	base := setup(t)
	root := filepath.Join(base, "tienda")
	must(t, run(t, base, "", "init", "tienda", "--type", "backend", "--purpose", "API de la tienda demo"), 0, "init")
	r := run(t, root, "", "slo", "check")
	must(t, r, 0, "sin SLOs")
	if !strings.Contains(r.stdout, "Sin SLOs") {
		t.Fatalf("sin SLOs:\n%s", r.stdout)
	}
	write(t, root, "coyote/slo/pedidos-service.yaml", sloPedidos)
	r = run(t, root, "", "slo", "check")
	must(t, r, 1, "sin runbook ni reglas")
	for _, want := range []string{"el runbook coyote/runbooks/pedidos-disponibilidad.md no existe", "no están al día"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("slo check sin %q:\n%s", want, r.stdout)
		}
	}
	lint := run(t, root, "", "standards", "lint")
	if lint.code == 0 || !strings.Contains(lint.stdout, "R19") {
		t.Fatalf("R19 es MUST en el lint:\n%s", lint.stdout)
	}
	write(t, root, "coyote/runbooks/pedidos-disponibilidad.md", "# Pedidos: disponibilidad\n\n## Síntomas\n## Diagnóstico\n## Mitigación\n## Escalamiento\n")
	must(t, run(t, root, "", "slo", "rules", "--check"), 1, "reglas sin generar")
	r = run(t, root, "", "slo", "rules")
	must(t, r, 0, "slo rules")
	if !strings.Contains(r.stdout, "creado") || !strings.Contains(r.stdout, "coyote/slo/prometheus/pedidos-service.yaml") {
		t.Fatalf("slo rules:\n%s", r.stdout)
	}
	rules := readFile(t, filepath.Join(root, "coyote/slo/prometheus/pedidos-service.yaml"))
	if !strings.Contains(rules, "alert: PedidosServiceDisponibilidad") || !strings.Contains(rules, "(14.4 * 0.001)") {
		t.Fatalf("reglas:\n%s", rules)
	}
	must(t, run(t, root, "", "slo", "rules", "--check"), 0, "reglas al día")
	r = run(t, root, "", "slo", "check")
	must(t, r, 0, "slo check")
	if !strings.Contains(r.stdout, "presupuesto 43 min") || !strings.Contains(r.stdout, "page y ticket") {
		t.Fatalf("slo check:\n%s", r.stdout)
	}
	must(t, run(t, root, "", "slo", "rules", "pedidos-service", "--stdout"), 0, "stdout")

	// Una regla editada a mano deja de estar vigente.
	write(t, root, "coyote/slo/prometheus/pedidos-service.yaml", strings.Replace(rules, "14.4", "144", 1))
	must(t, run(t, root, "", "slo", "rules", "--check"), 1, "regla editada a mano")
	must(t, run(t, root, "", "slo", "rules"), 0, "regenerar")
	// Reglas sin su archivo de SLOs.
	write(t, root, "coyote/slo/prometheus/huerfano.yaml", "groups: []\n")
	if r = run(t, root, "", "slo", "check"); r.code == 0 || !strings.Contains(r.stdout, "sin su archivo de SLOs") {
		t.Fatalf("reglas huérfanas:\n%s", r.stdout)
	}

	// La web muestra el SLO.
	pages := webPages(t, root, "/slo")
	if !strings.Contains(pages["/slo"], "pedidos-service · disponibilidad") || !strings.Contains(pages["/slo"], "43 min") {
		t.Fatalf("/slo:\n%s", pages["/slo"])
	}
}

func TestGatePRConSLORelajado(t *testing.T) {
	base := setup(t)
	productoDemo(t, base)
	svc := filepath.Join(base, "servicios")
	write(t, svc, "coyote/slo/pedidos-service.yaml", sloPedidos)
	git(t, svc, "add", "-A")
	git(t, svc, "commit", "-qm", "feat(slo): SLOs de pedidos")
	baseSHA := strings.TrimSpace(git(t, svc, "rev-parse", "HEAD"))
	write(t, svc, "coyote/slo/pedidos-service.yaml", strings.Replace(strings.Replace(sloPedidos, "objective: 99.9", "objective: 99", 1), "page: { labels: { severity: page } }", "page: { disable: true }", 1))
	git(t, svc, "commit", "-qam", "fix(slo): menos ruido")
	headSHA := strings.TrimSpace(git(t, svc, "rev-parse", "HEAD"))
	r := run(t, base, "", "gate", "pr", "--repo", "servicios="+svc, "--self", "servicios", "--base", baseSHA, "--head", headSHA, "--event", "")
	must(t, r, 0, "gate pr en warn")
	for _, want := range []string{"R3", "baja el objetivo de disponibilidad de 99.9 % a 99 %", "apaga la alerta page de disponibilidad"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("reporte sin %q:\n%s", want, r.stdout)
		}
	}
	// Subir el objetivo es R2.
	write(t, svc, "coyote/slo/pedidos-service.yaml", strings.Replace(sloPedidos, "objective: 99.9", "objective: 99.95", 1))
	git(t, svc, "commit", "-qam", "feat(slo): más exigente")
	up := strings.TrimSpace(git(t, svc, "rev-parse", "HEAD"))
	r = run(t, base, "", "gate", "pr", "--repo", "servicios="+svc, "--self", "servicios", "--base", headSHA, "--head", up, "--event", "")
	if !strings.Contains(r.stdout, "R2") || strings.Contains(r.stdout, "### coyote: R3") {
		t.Fatalf("subir el objetivo es R2:\n%s", r.stdout)
	}
}
