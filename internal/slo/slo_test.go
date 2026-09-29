package slo

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const pagos = `version: 1
service: pagos-api
labels: { team: pagos }
slos:
  - name: disponibilidad
    objective: 99.9
    description: Respuestas sin error 5xx
    sli:
      errors: sum(rate(http_server_request_duration_seconds_count{job="pagos-api",http_response_status_code=~"5.."}[{{.window}}]))
      total: sum(rate(http_server_request_duration_seconds_count{job="pagos-api"}[{{.window}}]))
    alerts:
      runbook: coyote/runbooks/pagos-api-disponibilidad.md
      page: { labels: { severity: page } }
      ticket: { labels: { severity: ticket } }
`

func mustParse(t *testing.T, s string) *Spec {
	t.Helper()
	spec, err := Parse([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestFactoresDelLibroDeSRE(t *testing.T) {
	want30 := []float64{14.4, 6, 3, 1}
	want28 := []float64{13.44, 5.6, 2.8, 0.933333}
	for i, b := range append(append([]burn{}, pageBurns...), ticketBurns...) {
		if got := b.Factor(30); got != want30[i] {
			t.Errorf("30 días, %s/%s: %v, se esperaba %v", b.Long, b.Short, got, want30[i])
		}
		if got := b.Factor(28); got != want28[i] {
			t.Errorf("28 días, %s/%s: %v, se esperaba %v", b.Long, b.Short, got, want28[i])
		}
	}
	if got := num(ErrorBudget(SLO{Objective: 99.9})); got != "0.001" {
		t.Fatalf("presupuesto de 99.9: %s", got)
	}
	if got := num(ErrorBudget(SLO{Objective: 99.95})); got != "0.0005" {
		t.Fatalf("presupuesto de 99.95: %s", got)
	}
}

func TestReglasDeterministas(t *testing.T) {
	s := mustParse(t, pagos)
	a, err := Rules(s)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Rules(mustParse(t, pagos))
	if !bytes.Equal(a, b) {
		t.Fatal("el mismo archivo da los mismos bytes")
	}
	out := string(a)
	for _, want := range []string{
		"record: slo:sli_error:ratio_rate5m", "record: slo:sli_error:ratio_rate3d", "record: slo:sli_error:ratio_rate30d",
		`[1h])))`, "sum_over_time(slo:sli_error:ratio_rate5m", "vector(0.999)", "vector(0.001)", "vector(30)",
		"alert: PagosApiDisponibilidad", "(14.4 * 0.001)", "(6 * 0.001)", "(3 * 0.001)", "(1 * 0.001)",
		"slo_severity: page", "slo_severity: ticket", "severity: page", "runbook_url: coyote/runbooks/pagos-api-disponibilidad.md", "team: pagos",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("falta %q en las reglas", want)
		}
	}
	if strings.Contains(out, "{{.window}}") {
		t.Fatal("la ventana se reemplaza en cada regla")
	}
	// Con la page apagada no hay alerta page ni hace falta runbook.
	off := strings.Replace(strings.Replace(pagos, "page: { labels: { severity: page } }", "page: { disable: true }", 1), "      runbook: coyote/runbooks/pagos-api-disponibilidad.md\n", "", 1)
	out2, err := Rules(mustParse(t, off))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out2), "slo_severity: page") || !strings.Contains(string(out2), "slo_severity: ticket") {
		t.Fatalf("sin page:\n%s", out2)
	}
}

func TestArchivosInvalidos(t *testing.T) {
	cases := map[string]string{
		"sin runbook con page":   strings.Replace(pagos, "      runbook: coyote/runbooks/pagos-api-disponibilidad.md\n", "", 1),
		"sin ventana":            strings.Replace(pagos, `[{{.window}}]))`+"\n      total", `[5m]))`+"\n      total", 1),
		"ventana fija":           strings.Replace(pagos, `"5.."}[{{.window}}]`, `"5.."}[{{.window}}] + x[10m]`, 1),
		"otro marcador":          strings.Replace(pagos, `[{{.window}}]))`+"\n      total", `[{{.window}}] {{.x}}))`+"\n      total", 1),
		"paréntesis":             strings.Replace(pagos, `sum(rate(http_server_request_duration_seconds_count{job="pagos-api"}[{{.window}}]))`, `sum(rate(http_server_request_duration_seconds_count{job="pagos-api"}[{{.window}}])`, 1),
		"comillas":               strings.Replace(pagos, `{job="pagos-api"}`, `{job="pagos-api}`, 1),
		"objetivo 100":           strings.Replace(pagos, "objective: 99.9", "objective: 100", 1),
		"objetivo 49":            strings.Replace(pagos, "objective: 99.9", "objective: 49", 1),
		"clave desconocida":      strings.Replace(pagos, "objective: 99.9", "objetivo: 99.9", 1),
		"etiqueta reservada":     strings.Replace(pagos, "team: pagos", "slo_id: otro", 1),
		"etiqueta con __":        strings.Replace(pagos, "team: pagos", "__name__: x", 1),
		"servicio inválido":      strings.Replace(pagos, "service: pagos-api", "service: Pagos API", 1),
		"periodo":                strings.Replace(pagos, "labels: { team: pagos }", "labels: { team: pagos }\nperiod: 7d", 1),
		"errors y error_ratio":   strings.Replace(pagos, "      total:", "      error_ratio: x[{{.window}}]\n      total:", 1),
		"runbook fuera del repo": strings.Replace(pagos, "coyote/runbooks/pagos-api-disponibilidad.md", "../../etc/passwd", 1),
		"nombre de alerta":       strings.Replace(pagos, "    alerts:\n", "    alerts:\n      name: \"Pagos API\"\n", 1),
		"sin slos":               "version: 1\nservice: pagos-api\nslos: []\n",
		"varias métricas":        strings.Replace(pagos, `http_server_request_duration_seconds_count{job="pagos-api",http_response_status_code=~"5.."}`, `{__name__=~"x_(a|b)_total", job="pagos-api"}`, 1),
	}
	for name, text := range cases {
		if _, err := Parse([]byte(text)); err == nil {
			t.Errorf("%s: debió fallar", name)
		}
	}
	if _, err := Parse([]byte(strings.Replace(pagos, "coyote/runbooks/pagos-api-disponibilidad.md", "https://wiki.example.com/runbooks/pagos", 1))); err != nil {
		t.Fatalf("un runbook puede ser una URL https: %v", err)
	}
}

func TestSLOsRelajados(t *testing.T) {
	risk := func(changes []Change) string {
		r := ""
		for _, c := range changes {
			if c.Risk > r {
				r = c.Risk
			}
		}
		return r
	}
	cases := []struct {
		name, text string
		want       string
	}{
		{"baja el objetivo", strings.Replace(pagos, "objective: 99.9", "objective: 99", 1), "R3"},
		{"sube el objetivo", strings.Replace(pagos, "objective: 99.9", "objective: 99.95", 1), "R2"},
		{"apaga la page", strings.Replace(pagos, "page: { labels: { severity: page } }", "page: { disable: true }", 1), "R3"},
		{"cambia la severidad de la page", strings.Replace(pagos, "severity: page }", "severity: info }", 1), "R3"},
		{"cambia qué es error", strings.Replace(pagos, `=~"5.."`, `="503"`, 1), "R3"},
		{"cambia las etiquetas del servicio", strings.Replace(pagos, "team: pagos", "team: nadie", 1), "R3"},
		{"cambia el nombre de la alerta", strings.Replace(pagos, "    alerts:\n", "    alerts:\n      name: PagosOtra\n", 1), "R3"},
		{"apaga el ticket", strings.Replace(pagos, "ticket: { labels: { severity: ticket } }", "ticket: { disable: true }", 1), "R3"},
		{"cambia el runbook", strings.Replace(pagos, "pagos-api-disponibilidad.md", "pagos.md", 1), "R2"},
		{"solo formato", "# SLOs de pagos\n" + strings.Replace(pagos, "labels: { team: pagos }", "labels:\n  team: pagos   # dueño", 1), ""},
		{"espacios en la consulta", strings.Replace(pagos, "sum(rate(", "sum( rate(", 1), "R3"},
		{"no valida", "version: 1\nservice: x\n", "R3"},
	}
	for _, c := range cases {
		if got := risk(Compare(pagos, true, c.text, true)); got != c.want {
			t.Errorf("%s: %q, se esperaba %q (%v)", c.name, got, c.want, Compare(pagos, true, c.text, true))
		}
	}
	if risk(Compare(pagos, true, "", false)) != "R3" || risk(Compare("", false, pagos, true)) != "R2" {
		t.Fatal("quitar el archivo es R3; agregarlo, R2")
	}
	two := pagos + `  - name: latencia
    objective: 95
    sli:
      error_ratio: 1 - (sum(rate(x_bucket{le="0.5"}[{{.window}}])) / sum(rate(x_count[{{.window}}])))
    alerts:
      runbook: coyote/runbooks/pagos-api-latencia.md
`
	if risk(Compare(two, true, pagos, true)) != "R3" || risk(Compare(pagos, true, two, true)) != "R2" {
		t.Fatal("quitar un SLO es R3; agregarlo, R2")
	}
	for _, p := range []string{"coyote/slo/pagos.yaml", "svc/coyote/slo/pagos.yml"} {
		if !IsSpecPath(p) {
			t.Errorf("%s es un archivo de SLOs", p)
		}
	}
	for _, p := range []string{"coyote/slo/prometheus/pagos.yaml", "slo/pagos.yaml", "coyote/slo/pagos.md", "xcoyote/slo/a.yaml"} {
		if IsSpecPath(p) {
			t.Errorf("%s no es un archivo de SLOs", p)
		}
	}
}

// TestPromtool corre las reglas generadas en promtool con series sintéticas:
// la alerta page dispara con un consumo de ~20 veces sostenido (y no con un
// pico de 5 minutos), ticket con ~5 y ninguna con ~0.5. Solo corre con
// PROMTOOL apuntando al binario.
func TestPromtool(t *testing.T) {
	bin := os.Getenv("PROMTOOL")
	if bin == "" {
		t.Skip("PROMTOOL no está definido")
	}
	dir := t.TempDir()
	out, err := Rules(mustParse(t, pagos))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pagos-api.yaml"), out, 0o644); err != nil {
		t.Fatal(err)
	}
	labels := `slo_id: pagos-api-disponibilidad, slo_service: pagos-api, slo_name: disponibilidad, team: pagos`
	tests := `rule_files: [pagos-api.yaml]
evaluation_interval: 1m
tests:
  # Una hora sin errores y después 20 malas por cada 1000 buenas: 1.96 % de
  # error, un consumo de 19.6. A los 5 minutos del pico la ventana de 1h
  # todavía frena la page; el ticket lento (6h y 3d, que aquí solo ven 66
  # minutos de historia) ya dispara. Una hora después dispara la page.
  - interval: 1m
    input_series:
      - series: 'http_server_request_duration_seconds_count{job="pagos-api",http_response_status_code="200"}'
        values: '0+1000x300'
      - series: 'http_server_request_duration_seconds_count{job="pagos-api",http_response_status_code="500"}'
        values: '0x60 20+20x240'
    alert_rule_test:
      - eval_time: 66m
        alertname: PagosApiDisponibilidad
        exp_alerts:
          - exp_labels: {severity: ticket, slo_severity: ticket, ` + labels + `}
            exp_annotations: {summary: "pagos-api disponibilidad: el presupuesto de error se consume demasiado rápido", description: "Objetivo 99.9 % en 30 días. Respuestas sin error 5xx", runbook_url: coyote/runbooks/pagos-api-disponibilidad.md}
      - eval_time: 130m
        alertname: PagosApiDisponibilidad
        exp_alerts:
          - exp_labels: {severity: page, slo_severity: page, ` + labels + `}
            exp_annotations: {summary: "pagos-api disponibilidad: el presupuesto de error se consume demasiado rápido", description: "Objetivo 99.9 % en 30 días. Respuestas sin error 5xx", runbook_url: coyote/runbooks/pagos-api-disponibilidad.md}
          - exp_labels: {severity: ticket, slo_severity: ticket, ` + labels + `}
            exp_annotations: {summary: "pagos-api disponibilidad: el presupuesto de error se consume demasiado rápido", description: "Objetivo 99.9 % en 30 días. Respuestas sin error 5xx", runbook_url: coyote/runbooks/pagos-api-disponibilidad.md}
  # 1000 buenas y 5 malas por minuto: 0.5 % de error, un consumo de 5: ticket sí, page no.
  - interval: 1m
    input_series:
      - series: 'http_server_request_duration_seconds_count{job="pagos-api",http_response_status_code="200"}'
        values: '0+1000x300'
      - series: 'http_server_request_duration_seconds_count{job="pagos-api",http_response_status_code="500"}'
        values: '0+5x300'
    alert_rule_test:
      - eval_time: 4h
        alertname: PagosApiDisponibilidad
        exp_alerts:
          - exp_labels: {severity: ticket, slo_severity: ticket, ` + labels + `}
            exp_annotations: {summary: "pagos-api disponibilidad: el presupuesto de error se consume demasiado rápido", description: "Objetivo 99.9 % en 30 días. Respuestas sin error 5xx", runbook_url: coyote/runbooks/pagos-api-disponibilidad.md}
  # 2000 buenas y 1 mala por minuto: 0.05 % de error, un consumo de 0.5: nada.
  - interval: 1m
    input_series:
      - series: 'http_server_request_duration_seconds_count{job="pagos-api",http_response_status_code="200"}'
        values: '0+2000x300'
      - series: 'http_server_request_duration_seconds_count{job="pagos-api",http_response_status_code="500"}'
        values: '0+1x300'
    alert_rule_test:
      - eval_time: 4h
        alertname: PagosApiDisponibilidad
        exp_alerts: []
    promql_expr_test:
      - expr: slo:error_budget:ratio
        eval_time: 1m
        exp_samples:
          - labels: 'slo:error_budget:ratio{` + strings.ReplaceAll(strings.ReplaceAll(labels, ": ", `="`), ", ", `", `) + `"}'
            value: 0.001
`
	if err := os.WriteFile(filepath.Join(dir, "test.yaml"), []byte(tests), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"check", "rules", "pagos-api.yaml"}, {"test", "rules", "test.yaml"}} {
		cmd := exec.Command(bin, args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("promtool %v: %v\n%s", args, err, out)
		}
	}
}

func TestConsultasQueNoMidenLoQueDicen(t *testing.T) {
	base := `sum(rate(http_server_request_duration_seconds_count{job="pagos-api",http_response_status_code=~"5.."}[{{.window}}]))`
	bad := map[string]string{
		"comentario":              "'" + base + " # errores'",
		"arroba":                  `sum(rate(m{job="a"}[{{.window}}] @ 0))`,
		"offset":                  `sum(rate(m{job="a"}[{{.window}}] offset 1h))`,
		"rango compuesto":         `sum(rate(m{job="a"}[1h30m])) + sum(rate(m{job="a"}[{{.window}}]))`,
		"rango en segundos":       `sum(rate(m{job="a"}[300])) + sum(rate(m{job="a"}[{{.window}}]))`,
		"ventana en un literal":   `sum(rate(m{job="{{.window}}"}[5m]))`,
		"sin nombre":              `sum(rate({job="a"}[{{.window}}]))`,
		"__name__ entre comillas": `sum(rate({"__name__"=~"a|b", job="a"}[{{.window}}]))`,
		"__name__ distinto":       `sum(rate(m{__name__!~"b", job="a"}[{{.window}}]))`,
		"otro marcador":           `sum(rate(m{job="a"}[{{.window}}])) * {{.x}}`,
		// Segunda revisión: mayúsculas, selectores después de un operador,
		// la ventana en un literal, subconsultas con paso largo y un literal
		// crudo que termina en barra invertida.
		"OFFSET":                 `sum(rate(m{job="a"}[{{.window}}] OFFSET 1w))`,
		"Offset":                 `sum(rate(m{job="a"}[{{.window}}] Offset 1w))`,
		"or sin nombre":          `sum(rate(m{job="a"}[{{.window}}])) or {job="b"}`,
		"and sin nombre":         `sum(rate(m{job="a"}[{{.window}}])) and {job="b"}`,
		"bool sin nombre":        `sum(rate(m{job="a"}[{{.window}}])) > bool {job="b"}`,
		"ventana en un string":   `sum(rate(m{job="a"}[{{.window}}])) + 0 * sum(rate(n{job="{{.window}}"}[{{.window}}]))`,
		"subconsulta de un día":  `sum(rate(m{job="a"}[{{.window}}:1d]))`,
		"subconsulta de ventana": `sum(rate(m{job="a"}[{{.window}}:{{.window}}]))`,
		"crudo con barra":        "sum(rate(m{job=\"a\"}[{{.window}}])) + 0 * sum(rate(m{job=`a\\`}[5m] @ 100 offset 1w))",
	}
	for name, q := range bad {
		text := strings.Replace(pagos, base, q, 1)
		if _, err := Parse([]byte(text)); err == nil {
			t.Errorf("%s: %q debió fallar", name, q)
		}
	}
	ok := []string{
		`sum(rate(m{job="a", path=~"/api/(pagos|cobros)"}[{{.window}}]))`,
		`sum by (route) (rate(m{job="a"}[{{.window}}]))`,
		`sum(rate(m_bucket{job="a",le="0.5"}[{{.window}}:1m]))`,
		`sum(rate(m_bucket{job="a",le="0.5"}[{{.window}}:]))`,
		`sum(rate(orders_total{job="a"}[{{.window}}])) or sum(rate(and_total{job="a"}[{{.window}}]))`,
		`(sum(rate(a_total{job="x"}[{{.window}}])) or vector(0)) + (sum(rate(b_total{job="x"}[{{.window}}])) or vector(0))`,
	}
	for _, q := range ok {
		if _, err := Parse([]byte(strings.Replace(pagos, base, q, 1))); err != nil {
			t.Errorf("%q es válida: %v", q, err)
		}
	}
}

func TestTextoQueNoEsPlantilla(t *testing.T) {
	for name, text := range map[string]string{
		"description": strings.Replace(pagos, "description: Respuestas sin error 5xx", "description: \"{{ range 1000000000000 }}{{ end }}\"", 1),
		"anotación":   strings.Replace(pagos, "    alerts:\n", "    alerts:\n      annotations: { dashboard: \"x {{ $labels.job }}\" }\n", 1),
		"etiqueta":    strings.Replace(pagos, "team: pagos", "team: \"{{ .x }}\"", 1),
		"runbook":     strings.Replace(pagos, "coyote/runbooks/pagos-api-disponibilidad.md", "coyote/runbooks/{{x}}.md", 1),
		"page con 80": strings.Replace(pagos, "objective: 99.9", "objective: 80", 1),
		"dos docs":    pagos + "---\nversion: 1\n",
		"alias":       strings.Replace(pagos, "labels: { team: pagos }", "labels: &l { team: pagos }", 1),
	} {
		if _, err := Parse([]byte(text)); err == nil {
			t.Errorf("%s: debió fallar", name)
		}
	}
	// Con la page apagada, un objetivo bajo vale.
	low := strings.Replace(strings.Replace(pagos, "objective: 99.9", "objective: 80", 1), "page: { labels: { severity: page } }", "page: { disable: true }", 1)
	if _, err := Parse([]byte(low)); err != nil {
		t.Fatalf("objetivo bajo sin page: %v", err)
	}
}

func TestComparacionesQueNoSeEsconden(t *testing.T) {
	risk := func(changes []Change) string {
		r := ""
		for _, c := range changes {
			if c.Risk > r {
				r = c.Risk
			}
		}
		return r
	}
	// Una base que ya no valida (con __name__=~) se compara igual.
	oldBase := strings.Replace(pagos, `http_server_request_duration_seconds_count{job="pagos-api",http_response_status_code=~"5.."}`, `{__name__=~"a|b", job="pagos-api"}`, 1)
	if _, err := Parse([]byte(oldBase)); err == nil {
		t.Fatal("la base de la prueba no debe validar")
	}
	relaxed := strings.Replace(pagos, "objective: 99.9", "objective: 50", 1)
	if got := risk(CompareFile("coyote/slo/pagos-api.yaml", oldBase, true, relaxed, true)); got != "R3" {
		t.Fatalf("relajar sobre una base inválida es R3: %s", got)
	}
	// El servicio tiene que llamarse como el archivo.
	if got := risk(CompareFile("coyote/slo/pagos-extra.yaml", "", false, pagos, true)); got != "R3" {
		t.Fatalf("servicio distinto del archivo: %s", got)
	}
	// Cambiar el periodo cambia los umbrales: R3.
	if got := risk(Compare(pagos, true, strings.Replace(pagos, "labels: { team: pagos }", "labels: { team: pagos }\nperiod: 28d", 1), true)); got != "R3" {
		t.Fatalf("periodo: %s", got)
	}
	// Unas reglas que no se pueden comprobar porque el SLO no valida: R3.
	if c, bad := CheckGenerated(oldBase, true, []byte("groups: []\n"), true); !bad || c.Risk != "R3" {
		t.Fatalf("reglas con SLO inválido: %+v %v", c, bad)
	}
	spec, rules := Pair("svc/coyote/slo/prometheus/pagos.yaml")
	if len(spec) != 2 || spec[1] != "svc/coyote/slo/pagos.yml" || rules != "svc/coyote/slo/prometheus/pagos.yaml" {
		t.Fatalf("pareja: %v %s", spec, rules)
	}
	if !InRulesDir("coyote/slo/prometheus/extra/x.yml") || IsRulesPath("coyote/slo/prometheus/extra/x.yaml") || IsRulesPath("coyote/slo/prometheus/x.yml") {
		t.Fatal("solo <servicio>.yaml directo es un archivo de reglas")
	}
}
