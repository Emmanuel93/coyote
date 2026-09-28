// Package product trata un producto que vive en varios repos (ADR-0011): saca
// de cada repo su identidad y las interfaces que expone y consume (rutas HTTP
// y tópicos de eventos) sin modelo y sin escribir en los repos, arma el mapa
// del producto y cruza un cambio contra ese mapa para ver su impacto en todos.
package product

import (
	"regexp"
	"strings"
)

// Param es el segmento normalizado de un parámetro de ruta.
const Param = "{}"

var (
	schemeHost = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*://[^/]*`)
	// placeholders de ruta: {id}, ${expr}, $id, :id, <id>
	dollarBrace = regexp.MustCompile(`\$\{[^}]*\}`)
	dollarVar   = regexp.MustCompile(`\$[A-Za-z_][A-Za-z0-9_.]*`)
	braceVar    = regexp.MustCompile(`\{[^}/]*\}`)
)

// NormPath deja una ruta HTTP en forma comparable: sin esquema ni host, sin
// query, con los parámetros como {} y sin barra final. Una ruta que empieza
// con una variable (la URL base: `${API}/x`, `$base/x`) pierde ese segmento.
// Devuelve "" si no parece una ruta.
func NormPath(raw string) string {
	p := strings.TrimSpace(raw)
	if p == "" {
		return ""
	}
	p = schemeHost.ReplaceAllString(p, "")
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	// base variable al inicio: ${API_URL}/x, $baseUrl/x, {base}/x
	if !strings.HasPrefix(p, "/") {
		first, rest, found := strings.Cut(p, "/")
		if !found || !(strings.HasPrefix(first, "$") || strings.HasPrefix(first, "{")) {
			return ""
		}
		p = "/" + rest
	}
	var segs []string
	for _, s := range strings.Split(p, "/") {
		switch {
		case s == "":
		case isParamSeg(s):
			segs = append(segs, Param)
		default:
			segs = append(segs, s)
		}
	}
	if len(segs) == 0 {
		return "/"
	}
	return "/" + strings.Join(segs, "/")
}

// isParamSeg reconoce un segmento con parámetro: {id}, ${expr}, $id, :id o <id>.
func isParamSeg(s string) bool {
	return dollarBrace.MatchString(s) || dollarVar.MatchString(s) || braceVar.MatchString(s) ||
		(strings.HasPrefix(s, ":") && len(s) > 1) || (strings.HasPrefix(s, "<") && strings.HasSuffix(s, ">"))
}

// Trivial son rutas que casi todo servicio expone y no dicen nada de un cambio.
func Trivial(p string) bool {
	switch {
	case p == "/" || p == "":
		return true
	case strings.HasPrefix(p, "/actuator"), strings.HasPrefix(p, "/health"), strings.HasPrefix(p, "/metrics"),
		strings.HasPrefix(p, "/swagger"), strings.HasPrefix(p, "/v3/api-docs"), p == "/ping", p == "/ready", p == "/live":
		return true
	}
	return false
}

// MatchScore compara la ruta que llama un cliente con la que expone un
// servicio. Devuelve -1 si no coinciden; más alto es mejor: un literal igual
// vale 2, un parámetro contra parámetro 1, un literal contra parámetro 1 y un
// parámetro del cliente contra un literal del servicio 0 (posible).
func MatchScore(consumer, provider string) int {
	c := strings.Split(strings.Trim(consumer, "/"), "/")
	p := strings.Split(strings.Trim(provider, "/"), "/")
	if len(c) != len(p) {
		return -1
	}
	score := 0
	for i := range c {
		switch {
		case c[i] == p[i] && c[i] != Param:
			score += 2
		case c[i] == Param && p[i] == Param:
			score++
		case p[i] == Param:
			score++
		case c[i] == Param:
			// el cliente manda un valor donde el servicio tiene un literal: posible
		default:
			return -1
		}
	}
	return score
}

// splitTop parte una expresión por un separador de primer nivel, respetando
// cadenas ('…', "…", `…`) y paréntesis, llaves y corchetes.
func splitTop(expr string, sep byte) []string {
	var parts []string
	depth, last := 0, 0
	for i := 0; i < len(expr); i++ {
		switch c := expr[i]; {
		case c == '\'' || c == '"' || c == '`':
			for i++; i < len(expr) && expr[i] != c; i++ {
				if expr[i] == '\\' {
					i++
				}
			}
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
		case c == sep && depth == 0:
			parts = append(parts, expr[last:i])
			last = i + 1
		}
	}
	return append(parts, expr[last:])
}

// concatPath arma la ruta de una expresión que concatena cadenas y
// variables: "/cuentas/" + id + "/libro" es /cuentas/{}/libro. Una constante
// que se puede resolver aporta su valor; lo que va antes de la primera cadena
// es la base (el host). uncertain indica una constante de ruta sin resolver:
// la ruta que queda puede no ser la real.
func concatPath(expr string, resolve func(string) (string, bool)) (path string, uncertain bool) {
	var b strings.Builder
	for n, part := range splitTop(strings.TrimSpace(expr), '+') {
		part = strings.TrimSpace(part)
		switch {
		case len(part) >= 2 && strings.ContainsRune(`'"`+"`", rune(part[0])) && part[len(part)-1] == part[0]:
			b.WriteString(part[1 : len(part)-1])
		case resolve != nil && constName.MatchString(part):
			if v, ok := resolve(part); ok {
				b.WriteString(v)
				continue
			}
			uncertain = true
			fallthrough
		default:
			if n == 0 {
				b.WriteString("{base}")
			} else {
				b.WriteString("{}")
			}
		}
	}
	return b.String(), uncertain
}

var constName = regexp.MustCompile(`^(?:[A-Z]\w*\.)?[A-Z_][A-Z0-9_]*$`)
