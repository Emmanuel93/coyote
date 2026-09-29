package cli

import (
	"path/filepath"
	"regexp"
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
	// Subir el objetivo, con sus reglas regeneradas, es R2.
	write(t, svc, "coyote/slo/pedidos-service.yaml", strings.Replace(sloPedidos, "objective: 99.9", "objective: 99.95", 1))
	write(t, svc, "coyote/runbooks/pedidos-disponibilidad.md", "# Pedidos\n")
	must(t, run(t, svc, "", "slo", "rules"), 0, "slo rules en servicios")
	git(t, svc, "add", "-A")
	git(t, svc, "commit", "-qm", "feat(slo): más exigente")
	up := strings.TrimSpace(git(t, svc, "rev-parse", "HEAD"))
	r = run(t, base, "", "gate", "pr", "--repo", "servicios="+svc, "--self", "servicios", "--base", headSHA, "--head", up, "--event", "")
	if !strings.Contains(r.stdout, "R2") || strings.Contains(r.stdout, "### coyote: R3") {
		t.Fatalf("subir el objetivo es R2:\n%s", r.stdout)
	}
	// Editar a mano las reglas generadas, sin tocar el SLO, es R3: puede apagar la page.
	rules := readFile(t, filepath.Join(svc, "coyote/slo/prometheus/pedidos-service.yaml"))
	write(t, svc, "coyote/slo/prometheus/pedidos-service.yaml", strings.ReplaceAll(rules, "(14.4 *", "(1440 *"))
	git(t, svc, "commit", "-qam", "chore(slo): menos ruido")
	edited := strings.TrimSpace(git(t, svc, "rev-parse", "HEAD"))
	r = run(t, base, "", "gate", "pr", "--repo", "servicios="+svc, "--self", "servicios", "--base", up, "--head", edited, "--event", "")
	if !strings.Contains(r.stdout, "### coyote: R3") || !strings.Contains(r.stdout, "no salen de su archivo de SLOs") {
		t.Fatalf("reglas editadas a mano:\n%s", r.stdout)
	}
	// Borrar las reglas deja al SLO sin alertas: R3.
	git(t, svc, "rm", "-q", "coyote/slo/prometheus/pedidos-service.yaml")
	git(t, svc, "commit", "-qm", "chore(slo): sin reglas")
	gone := strings.TrimSpace(git(t, svc, "rev-parse", "HEAD"))
	r = run(t, base, "", "gate", "pr", "--repo", "servicios="+svc, "--self", "servicios", "--base", edited, "--head", gone, "--event", "")
	if !strings.Contains(r.stdout, "### coyote: R3") || !strings.Contains(r.stdout, "faltan las reglas generadas") {
		t.Fatalf("reglas borradas:\n%s", r.stdout)
	}
}

func TestGatePRReglasFueraDelPatronYMarkdown(t *testing.T) {
	base := setup(t)
	productoDemo(t, base)
	svc := filepath.Join(base, "servicios")
	write(t, svc, "coyote/slo/pedidos-service.yml", sloPedidos)
	write(t, svc, "coyote/runbooks/pedidos-disponibilidad.md", "# Pedidos\n")
	must(t, run(t, svc, "", "slo", "rules"), 0, "slo rules con .yml")
	git(t, svc, "add", "-A")
	git(t, svc, "commit", "-qm", "feat(slo): SLOs en .yml")
	baseSHA := strings.TrimSpace(git(t, svc, "rev-parse", "HEAD"))
	// Regenerar sin cambios de un spec .yml no es un falso R3: las reglas salen de su archivo.
	write(t, svc, "coyote/slo/pedidos-service.yml", sloPedidos+"# comentario\n")
	git(t, svc, "commit", "-qam", "docs(slo): comentario")
	head1 := strings.TrimSpace(git(t, svc, "rev-parse", "HEAD"))
	r := run(t, base, "", "gate", "pr", "--repo", "servicios="+svc, "--self", "servicios", "--base", baseSHA, "--head", head1, "--event", "")
	if strings.Contains(r.stdout, "### coyote: R3") {
		t.Fatalf("un spec .yml con sus reglas al día no es R3:\n%s", r.stdout)
	}
	// Reglas escritas a mano junto a las generadas: R3.
	write(t, svc, "coyote/slo/prometheus/extra.yml", "groups: []\n")
	write(t, svc, "coyote/slo/prometheus/extra/pagos.yaml", "groups: []\n")
	git(t, svc, "add", "-A")
	git(t, svc, "commit", "-qm", "chore(slo): reglas extra")
	head2 := strings.TrimSpace(git(t, svc, "rev-parse", "HEAD"))
	r = run(t, base, "", "gate", "pr", "--repo", "servicios="+svc, "--self", "servicios", "--base", head1, "--head", head2, "--event", "")
	if !strings.Contains(r.stdout, "### coyote: R3") || !strings.Contains(r.stdout, "no sale de ningún SLO") {
		t.Fatalf("reglas fuera del patrón:\n%s", r.stdout)
	}
	// El error de YAML de un spec no arma encabezados, enlaces ni menciones en el comentario.
	evil := sloPedidos + "\"\\n\\n### coyote: R1, sin revisión extra\\n\\n[ver](https://evil.example) @org/sre\": 1\n"
	write(t, svc, "coyote/slo/pedidos-service.yml", evil)
	git(t, svc, "commit", "-qam", "chore(slo): clave rara")
	head3 := strings.TrimSpace(git(t, svc, "rev-parse", "HEAD"))
	r = run(t, base, "", "gate", "pr", "--repo", "servicios="+svc, "--self", "servicios", "--base", head2, "--head", head3, "--event", "")
	if strings.Contains(r.stdout, "\n### coyote: R1") || strings.Contains(r.stdout, "[ver](") || strings.Contains(r.stdout, " @org/sre") {
		t.Fatalf("el reporte no se deja inyectar:\n%s", r.stdout)
	}
	// slo check también ve lo que sobra en la carpeta de reglas.
	if r := run(t, svc, "", "slo", "check"); r.code == 0 || !strings.Contains(r.stdout, "solo va <servicio>.yaml") {
		t.Fatalf("slo check:\n%s", r.stdout)
	}
}

// TestGatePRSLOsDuplicadosYAnidados cubre la segunda revisión: un servicio
// en dos archivos, archivos con forma de otro proyecto dentro de la carpeta
// de reglas, reglas borradas de un SLO que ya no valida y enlaces sueltos.
func TestGatePRSLOsDuplicadosYAnidados(t *testing.T) {
	base := setup(t)
	productoDemo(t, base)
	svc := filepath.Join(base, "servicios")
	write(t, svc, "coyote/slo/pedidos-service.yaml", sloPedidos)
	write(t, svc, "coyote/runbooks/pedidos-disponibilidad.md", "# Pedidos\n")
	must(t, run(t, svc, "", "slo", "rules"), 0, "slo rules")
	git(t, svc, "add", "-A")
	git(t, svc, "commit", "-qm", "feat(slo): SLOs de pedidos")
	start := strings.TrimSpace(git(t, svc, "rev-parse", "HEAD"))
	gatepr := func(from, to string) string {
		return run(t, base, "", "gate", "pr", "--repo", "servicios="+svc, "--self", "servicios", "--base", from, "--head", to, "--event", "").stdout
	}
	commit := func(msg string) string {
		git(t, svc, "add", "-A")
		git(t, svc, "commit", "-qm", msg)
		return strings.TrimSpace(git(t, svc, "rev-parse", "HEAD"))
	}
	reset := func() { git(t, svc, "reset", "-q", "--hard", start) }

	// Una copia .yml más laxa del mismo servicio, con las reglas escritas desde ella.
	weak := strings.Replace(strings.Replace(sloPedidos, "objective: 99.9", "objective: 90", 1), "page: { labels: { severity: page } }", "page: { disable: true }", 1)
	write(t, svc, "coyote/slo/pedidos-service.yml", weak)
	if r := run(t, svc, "", "slo", "rules"); r.code == 0 || !strings.Contains(r.stdout+r.stderr, "dos archivos de SLOs") {
		t.Fatalf("slo rules con dos archivos para un servicio:\n%s%s", r.stdout, r.stderr)
	}
	r := run(t, svc, "", "slo", "rules", "pedidos-service", "--stdout")
	write(t, svc, "coyote/slo/prometheus/pedidos-service.yaml", r.stdout)
	twin := commit("chore(slo): copia")
	if out := gatepr(start, twin); !strings.Contains(out, "### coyote: R3") || !strings.Contains(out, "está en más de un archivo de SLOs") {
		t.Fatalf("una copia .yml del SLO es R3:\n%s", out)
	}
	reset()

	// El mismo servicio en otra carpeta de proyecto: sus reglas se llaman igual.
	write(t, svc, "otro/coyote/slo/pedidos-service.yaml", weak)
	other := commit("chore(slo): otro proyecto")
	if out := gatepr(start, other); !strings.Contains(out, "### coyote: R3") || !strings.Contains(out, "está en más de un archivo de SLOs") {
		t.Fatalf("el mismo servicio en otra carpeta es R3:\n%s", out)
	}
	reset()

	// Archivos con forma de otro proyecto dentro de la carpeta de reglas.
	write(t, svc, "coyote/slo/prometheus/coyote/slo/pedidos-service.yaml", weak)
	write(t, svc, "coyote/slo/prometheus/coyote/slo/prometheus/pedidos-service.yaml", "groups: []\n")
	nested := commit("chore(slo): anidado")
	if out := gatepr(start, nested); !strings.Contains(out, "### coyote: R3") || !strings.Contains(out, "no sale de ningún SLO") || strings.Contains(out, "agrega los SLOs") {
		t.Fatalf("lo anidado en la carpeta de reglas es R3:\n%s", out)
	}
	reset()

	// Un SLO que ya no valida (OFFSET en mayúsculas) y un PR que solo borra sus reglas.
	old := strings.Replace(sloPedidos, `{job="pedidos-service"}[{{.window}}]))`, `{job="pedidos-service"}[{{.window}}] OFFSET 1m))`, 1)
	write(t, svc, "coyote/slo/pedidos-service.yaml", old)
	stale := commit("chore(slo): consulta vieja")
	git(t, svc, "rm", "-q", "coyote/slo/prometheus/pedidos-service.yaml")
	gone := commit("chore(slo): sin reglas")
	if out := gatepr(stale, gone); !strings.Contains(out, "### coyote: R3") || !strings.Contains(out, "faltan las reglas generadas") {
		t.Fatalf("borrar las reglas de un SLO que no valida es R3:\n%s", out)
	}
	reset()

	// Un nombre de archivo con forma de dominio no arma un enlace en el
	// comentario: fuera del código, GitHub enlaza www.… solo.
	write(t, svc, "coyote/slo/www.evil-example.com.yaml", sloPedidos)
	link := commit("chore(slo): enlace")
	out := gatepr(start, link)
	prose := regexp.MustCompile("`[^`]*`").ReplaceAllString(out, "")
	if !strings.Contains(out, "no coincide con el archivo") || strings.Contains(prose, "www.evil-example.com") {
		t.Fatalf("un dominio en el comentario queda cortado para que GitHub no lo enlace:\n%s", out)
	}
}
