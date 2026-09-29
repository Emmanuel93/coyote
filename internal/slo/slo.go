// Package slo lee los SLOs de un servicio (coyote/slo/<servicio>.yaml) y
// genera sus reglas de Prometheus con el método de varias ventanas y varias
// tasas de consumo del presupuesto de error (ADR-0020). La especificación
// está en docs/specs/slo-v1.md.
package slo

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Rutas del proyecto.
const (
	Dir      = "coyote/slo"
	RulesDir = "coyote/slo/prometheus"
	// MaxFile es el tope de un archivo de SLOs.
	MaxFile = 1 << 20
	// Window es el marcador de la ventana en las consultas.
	Window = "{{.window}}"
)

// Spec es coyote/slo/<servicio>.yaml v1.
type Spec struct {
	Version int               `yaml:"version"`
	Service string            `yaml:"service"`
	Labels  map[string]string `yaml:"labels,omitempty"`
	Period  string            `yaml:"period,omitempty"` // 30d (defecto) o 28d
	SLOs    []SLO             `yaml:"slos"`
}

// SLO es un objetivo de proporción: eventos malos sobre totales.
type SLO struct {
	Name        string  `yaml:"name"`
	Objective   float64 `yaml:"objective"` // porcentaje, p. ej. 99.9
	Description string  `yaml:"description,omitempty"`
	SLI         SLI     `yaml:"sli"`
	Alerts      Alerts  `yaml:"alerts"`
}

// SLI son las consultas, con {{.window}} donde va la ventana.
type SLI struct {
	Errors     string `yaml:"errors,omitempty"`
	Total      string `yaml:"total,omitempty"`
	ErrorRatio string `yaml:"error_ratio,omitempty"`
}

// Alerts configura las alertas de un SLO.
type Alerts struct {
	Name        string            `yaml:"name,omitempty"`
	Runbook     string            `yaml:"runbook,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty"`
	Page        Alert             `yaml:"page,omitempty"`
	Ticket      Alert             `yaml:"ticket,omitempty"`
}

// Alert es la alerta page o ticket.
type Alert struct {
	Disable     bool              `yaml:"disable,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty"`
}

var (
	nameRe      = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	labelRe     = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,99}$`)
	alertRe     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,99}$`)
	hardRangeRe = regexp.MustCompile(`\[\s*\d+(ms|s|m|h|d|w|y)\s*[\]:]`)
)

// reserved son las etiquetas que pone coyote.
var reserved = map[string]bool{"slo_id": true, "slo_service": true, "slo_name": true, "slo_window": true, "slo_severity": true}

// Parse lee y valida un archivo de SLOs. Una clave desconocida es un error:
// un "objetivo:" mal escrito dejaría un SLO sin objetivo.
func Parse(data []byte) (*Spec, error) {
	var s Spec
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("archivo vacío")
		}
		return nil, err
	}
	if s.Period == "" {
		s.Period = "30d"
	}
	return &s, s.Validate()
}

// Days devuelve el periodo en días.
func (s *Spec) Days() int {
	switch s.Period {
	case "28d":
		return 28
	}
	return 30
}

// Validate revisa el archivo sin tocar el disco.
func (s *Spec) Validate() error {
	var errs []string
	add := func(format string, a ...any) { errs = append(errs, fmt.Sprintf(format, a...)) }
	if s.Version != 1 {
		add("version %d no soportada; usa version: 1", s.Version)
	}
	if !nameRe.MatchString(s.Service) {
		add("service %q inválido: minúsculas, números y guiones", s.Service)
	}
	if s.Period != "30d" && s.Period != "28d" {
		add("period %q: usa 30d o 28d", s.Period)
	}
	checkLabels("labels", s.Labels, add)
	if len(s.SLOs) == 0 {
		add("slos: declara al menos un SLO")
	}
	names := map[string]bool{}
	for i, o := range s.SLOs {
		where := fmt.Sprintf("slos[%d]", i)
		if o.Name != "" {
			where = "slo " + o.Name
		}
		switch {
		case !nameRe.MatchString(o.Name):
			add("%s: name %q inválido: minúsculas, números y guiones", where, o.Name)
		case names[o.Name]:
			add("%s: repetido", where)
		}
		names[o.Name] = true
		if math.IsNaN(o.Objective) || o.Objective < 50 || o.Objective >= 100 {
			add("%s: objective %v fuera de rango: un porcentaje de 50 a menos de 100", where, o.Objective)
		} else if d := decimals(o.Objective); d > 4 {
			add("%s: objective %v con más de cuatro decimales", where, o.Objective)
		}
		checkSLI(where, o.SLI, add)
		a := o.Alerts
		if a.Name != "" && !alertRe.MatchString(a.Name) {
			add("%s: alerts.name %q inválido: letras, números y guion bajo", where, a.Name)
		}
		if !a.Page.Disable && strings.TrimSpace(a.Runbook) == "" {
			add("%s: la alerta page necesita alerts.runbook: quien la recibe de madrugada sigue un runbook", where)
		}
		if r := strings.TrimSpace(a.Runbook); r != "" && (strings.HasPrefix(r, "/") || strings.Contains(r, "..") || strings.ContainsAny(r, "\x00\n")) {
			if !strings.HasPrefix(r, "https://") {
				add("%s: alerts.runbook %q: una ruta del repo sin .. o una URL https", where, r)
			}
		}
		checkLabels(where+": alerts.labels", a.Labels, add)
		checkLabels(where+": alerts.page.labels", a.Page.Labels, add)
		checkLabels(where+": alerts.ticket.labels", a.Ticket.Labels, add)
		for _, m := range []map[string]string{a.Annotations, a.Page.Annotations, a.Ticket.Annotations} {
			for k := range m {
				if !labelRe.MatchString(k) {
					add("%s: anotación %q inválida", where, k)
				}
			}
		}
	}
	if len(errs) > 0 {
		sort.Strings(errs)
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func decimals(v float64) int {
	s := strconv.FormatFloat(v, 'f', -1, 64)
	if i := strings.IndexByte(s, '.'); i >= 0 {
		return len(s) - i - 1
	}
	return 0
}

func checkLabels(where string, m map[string]string, add func(string, ...any)) {
	for k, v := range m {
		switch {
		case !labelRe.MatchString(k) || strings.HasPrefix(k, "__"):
			add("%s: etiqueta %q inválida", where, k)
		case reserved[k]:
			add("%s: la etiqueta %s la pone coyote", where, k)
		case strings.ContainsAny(v, "\x00\n"):
			add("%s: el valor de %s lleva un salto de línea", where, k)
		}
	}
}

// checkSLI revisa la forma de las consultas: coyote no valida PromQL
// completo, pero sí que la ventana sea la suya y que la consulta cierre.
func checkSLI(where string, s SLI, add func(string, ...any)) {
	var qs map[string]string
	switch {
	case s.ErrorRatio != "" && (s.Errors != "" || s.Total != ""):
		add("%s: sli lleva errors y total, o error_ratio; no los dos", where)
		return
	case s.ErrorRatio != "":
		qs = map[string]string{"error_ratio": s.ErrorRatio}
	case s.Errors != "" && s.Total != "":
		qs = map[string]string{"errors": s.Errors, "total": s.Total}
	default:
		add("%s: sli necesita errors y total, o error_ratio", where)
		return
	}
	keys := make([]string, 0, len(qs))
	for k := range qs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		q := qs[k]
		if !strings.Contains(q, Window) {
			add("%s: sli.%s no usa %s: la ventana la pone coyote en cada regla", where, k, Window)
		}
		if hardRangeRe.MatchString(q) {
			add("%s: sli.%s tiene una ventana fija; usa %s", where, k, Window)
		}
		if strings.Contains(strings.ReplaceAll(q, Window, ""), "{{") {
			add("%s: sli.%s: el único marcador es %s", where, k, Window)
		}
		if strings.ContainsAny(q, "\x00") || len(q) > 4000 {
			add("%s: sli.%s es demasiado larga o tiene bytes nulos", where, k)
		}
		if err := balanced(q); err != nil {
			add("%s: sli.%s: %v", where, k, err)
		}
	}
}

// balanced revisa paréntesis, corchetes, llaves y comillas, sin contar lo
// que está entre comillas.
func balanced(q string) error {
	var stack []rune
	pairs := map[rune]rune{')': '(', ']': '[', '}': '{'}
	var quote rune
	escaped := false
	for _, r := range q {
		if quote != 0 {
			switch {
			case escaped:
				escaped = false
			case r == '\\' && quote != '`':
				escaped = true
			case r == quote:
				quote = 0
			}
			continue
		}
		switch r {
		case '"', '\'', '`':
			quote = r
		case '(', '[', '{':
			stack = append(stack, r)
		case ')', ']', '}':
			if len(stack) == 0 || stack[len(stack)-1] != pairs[r] {
				return fmt.Errorf("%q sin abrir", r)
			}
			stack = stack[:len(stack)-1]
		}
	}
	if quote != 0 {
		return errors.New("comillas sin cerrar")
	}
	if len(stack) > 0 {
		return fmt.Errorf("%q sin cerrar", stack[len(stack)-1])
	}
	return nil
}
