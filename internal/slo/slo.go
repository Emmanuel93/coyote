// Package slo lee los SLOs de un servicio (coyote/slo/<servicio>.yaml) y
// genera sus reglas de Prometheus con el método de varias ventanas y varias
// tasas de consumo del presupuesto de error (ADR-0020). La especificación
// está en docs/specs/slo-v1.md.
package slo

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Emmanuel93/coyote/internal/yamlx"
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
	nameRe  = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	labelRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,99}$`)
	alertRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,99}$`)
)

// Topes de un archivo de SLOs: más que cualquier servicio real, y lo bastante
// chicos para que las reglas generadas pesen menos de 2 MiB.
const (
	maxSLOs  = 40
	maxQuery = 2000
	maxText  = 500
)

// reserved son las etiquetas que pone coyote.
var reserved = map[string]bool{"slo_id": true, "slo_service": true, "slo_name": true, "slo_window": true, "slo_severity": true}

// Parse lee y valida un archivo de SLOs. Una clave desconocida es un error:
// un "objetivo:" mal escrito dejaría un SLO sin objetivo.
func Parse(data []byte) (*Spec, error) {
	s, err := Decode(data)
	if err != nil {
		return nil, err
	}
	return s, s.Validate()
}

// Decode lee el archivo sin validar su contenido: un solo documento, sin
// anclas ni alias y sin claves desconocidas. gate pr lo usa para comparar con
// una base que ya no valida.
func Decode(data []byte) (*Spec, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("archivo vacío")
	}
	var s Spec
	if err := yamlx.Strict(data, &s); err != nil {
		return nil, err
	}
	if s.Period == "" {
		s.Period = "30d"
	}
	return &s, nil
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
	switch {
	case len(s.SLOs) == 0:
		add("slos: declara al menos un SLO")
	case len(s.SLOs) > maxSLOs:
		add("slos: hasta %d por archivo; divide el servicio", maxSLOs)
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
		checkText(where+": description", o.Description, add)
		a := o.Alerts
		if a.Name != "" && !alertRe.MatchString(a.Name) {
			add("%s: alerts.name %q inválido: letras, números y guion bajo", where, a.Name)
		}
		if !a.Page.Disable && strings.TrimSpace(a.Runbook) == "" {
			add("%s: la alerta page necesita alerts.runbook: quien la recibe de madrugada sigue un runbook", where)
		}
		checkText(where+": alerts.runbook", a.Runbook, add)
		if !a.Page.Disable && o.Objective >= 50 && o.Objective < 100 {
			// Con un objetivo bajo, el umbral de consumo de la page pasa de
			// 100 % de errores: la alerta no puede disparar.
			possible := false
			for _, b := range pageBurns {
				possible = possible || b.Factor(s.Days())*ErrorBudget(o) < 1
			}
			if !possible {
				add("%s: con objetivo %v la alerta page no puede disparar (su umbral pasa del 100 %% de errores); apágala o sube el objetivo", where, o.Objective)
			}
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
			for k, v := range m {
				if !labelRe.MatchString(k) {
					add("%s: anotación %q inválida", where, k)
				}
				checkText(where+": anotación "+k, v, add)
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
		checkText(where+": "+k, v, add)
	}
}

// checkText revisa el texto que va a una etiqueta o anotación de una alerta.
// Prometheus lo ejecuta como plantilla: un {{ en el archivo podría romper la
// carga de las reglas o colgar la alerta justo cuando dispara.
func checkText(where, v string, add func(string, ...any)) {
	switch {
	case strings.Contains(v, "{{") || strings.Contains(v, "}}"):
		add("%s: sin {{ ni }}: Prometheus ejecuta ese texto como plantilla", where)
	case !utf8.ValidString(v):
		add("%s: el texto no es UTF-8 válido", where)
	case len(v) > maxText:
		add("%s: más de %d caracteres", where, maxText)
	}
}

// checkSLI revisa la forma de las consultas: coyote no valida PromQL
// completo, pero sí que la ventana sea la suya, que cada selector nombre una
// métrica y que la consulta cierre.
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
		switch {
		case !utf8.ValidString(q) || strings.ContainsRune(q, 0) || len(q) > maxQuery:
			add("%s: sli.%s es demasiado larga, tiene bytes nulos o no es UTF-8 válido", where, k)
			continue
		case strings.Contains(q, sentinel):
			add("%s: sli.%s no puede contener %s", where, k, sentinel)
			continue
		}
		if err := balanced(q); err != nil {
			add("%s: sli.%s: %v", where, k, err)
			continue
		}
		for _, msg := range scanQuery(q) {
			add("%s: sli.%s %s", where, k, msg)
		}
	}
}

// sentinel reemplaza a {{.window}} mientras se revisa una consulta.
const sentinel = "__COYOTE_WINDOW__"

// scanQuery recorre la consulta fuera de las comillas y devuelve lo que la
// haría fallar o medir otra cosa: ventanas que no son la de coyote,
// comentarios que se tragan el paréntesis que agrega coyote, selectores sin
// nombre de métrica o con __name__ (rate() quita el nombre y, si dos
// métricas comparten etiquetas, la regla falla en cada evaluación) y
// modificadores offset o @ que corren la ventana.
func scanQuery(q string) []string {
	var out []string
	add := func(msg string) {
		for _, m := range out {
			if m == msg {
				return
			}
		}
		out = append(out, msg)
	}
	q = strings.ReplaceAll(q, Window, sentinel)
	if !strings.Contains(q, sentinel) {
		add("no usa " + Window + ": la ventana la pone coyote en cada regla")
	}
	if strings.Contains(q, "{{") || strings.Contains(q, "}}") {
		add("tiene otro marcador; el único es " + Window)
	}
	rs := []rune(q)
	var quote rune
	escaped := false
	windows := 0
	for i := 0; i < len(rs); i++ {
		r := rs[i]
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
		switch {
		case r == '"' || r == '\'' || r == '`':
			quote = r
		case r == '#':
			add("tiene un comentario (#): se tragaría el paréntesis que agrega coyote")
		case r == '@':
			add("usa el modificador @: la ventana es la de coyote")
		case r == '[':
			end := i + 1
			for end < len(rs) && rs[end] != ']' {
				end++
			}
			inner := strings.TrimSpace(string(rs[i+1 : min(end, len(rs))]))
			rng, step, sub := strings.Cut(inner, ":")
			switch {
			case strings.TrimSpace(rng) != sentinel:
				add("tiene una ventana fija [" + inner + "]; usa " + Window)
			case sub && !shortStep(strings.TrimSpace(step)):
				add("usa una subconsulta con paso " + strings.TrimSpace(step) + ": el paso va de 1s a 5m, o vacío")
				windows++
			default:
				windows++
			}
			i = end
		case r == '{':
			j := i - 1
			for j >= 0 && unicode.IsSpace(rs[j]) {
				j--
			}
			k := j
			for k >= 0 && (unicode.IsLetter(rs[k]) || unicode.IsDigit(rs[k]) || rs[k] == '_' || rs[k] == ':') {
				k--
			}
			if j < 0 || k == j || operatorWord[strings.ToLower(string(rs[k+1:j+1]))] {
				add("tiene un selector sin nombre de métrica: {…} elige varias métricas y rate() falla si comparten etiquetas; suma cada métrica con (sum(rate(m[" + Window + "])) or vector(0))")
			}
			end := i + 1
			var inQuote rune
			for end < len(rs) {
				c := rs[end]
				if inQuote != 0 {
					if c == '\\' && inQuote != '`' {
						end += 2
						continue
					}
					if c == inQuote {
						inQuote = 0
					}
				} else if c == '"' || c == '\'' || c == '`' {
					inQuote = c
				} else if c == '}' {
					break
				}
				end++
			}
			if strings.Contains(string(rs[i:min(end+1, len(rs))]), "__name__") {
				add("elige métricas con __name__: rate() falla cuando comparten etiquetas; suma cada métrica con (sum(rate(m[" + Window + "])) or vector(0))")
			}
			i = end
		case unicode.IsLetter(r) || r == '_':
			j := i
			for j < len(rs) && (unicode.IsLetter(rs[j]) || unicode.IsDigit(rs[j]) || rs[j] == '_' || rs[j] == ':') {
				j++
			}
			word := string(rs[i:j])
			if strings.EqualFold(word, "offset") {
				add("usa offset: la ventana es la de coyote")
			}
			if strings.EqualFold(word, "scalar") {
				add("usa scalar(): el SLI es un vector, y coyote lo combina con or y con comparaciones")
			}
			i = j - 1
		}
	}
	if strings.Count(q, sentinel) > windows {
		add("usa " + Window + " fuera de una ventana [..]")
	}
	return out
}

// shortStep dice si el paso de una subconsulta es vacío o de 1s a 5m: un
// paso más largo que la ventana más corta deja a las alertas sin muestras.
func shortStep(step string) bool {
	if step == "" {
		return true
	}
	m := stepRe.FindStringSubmatch(step)
	if m == nil {
		return false
	}
	mins, _ := strconv.Atoi(m[1])
	secs, _ := strconv.Atoi(m[2])
	n := mins*60 + secs
	return n >= 1 && n <= 300
}

var stepRe = regexp.MustCompile(`^(?:([0-9]{1,3})m)?(?:([0-9]{1,3})s)?$`)

// operatorWord son las palabras de PromQL que pueden ir antes de un selector
// sin ser el nombre de una métrica.
var operatorWord = map[string]bool{"and": true, "or": true, "unless": true, "bool": true, "atan2": true,
	"group_left": true, "group_right": true}

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
