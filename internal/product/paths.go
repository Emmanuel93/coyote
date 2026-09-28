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
