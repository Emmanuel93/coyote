// Package glob compara rutas relativas con patrones tipo gitignore:
// ** (cualquier número de directorios), * y ? (dentro de un segmento) y {a,b}.
package glob

import (
	"path"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"
)

var cache sync.Map // patrón -> *regexp.Regexp

// Match informa si rel (ruta relativa con '/') coincide con pattern. Un patrón
// sin '/' también se compara contra el nombre base, como en .gitignore.
func Match(pattern, rel string) bool {
	rel = strings.TrimPrefix(path.Clean("/"+rel), "/")
	if dir, ok := strings.CutSuffix(pattern, "/"); ok && dir != "" {
		// "respaldos/" es un directorio: cualquier archivo debajo, en cualquier nivel
		// si el patrón no tiene otra barra (como en .gitignore).
		if strings.Contains(strings.TrimPrefix(dir, "/"), "/") {
			pattern = dir + "/**"
		} else {
			pattern = "**/" + strings.TrimPrefix(dir, "/") + "/**"
		}
	}
	re := compile(pattern)
	if re.MatchString(rel) {
		return true
	}
	if !strings.Contains(strings.TrimSuffix(pattern, "/"), "/") {
		return re.MatchString(path.Base(rel))
	}
	return false
}

// Any informa si rel coincide con alguno de los patrones.
func Any(patterns []string, rel string) bool {
	for _, p := range patterns {
		if Match(p, rel) {
			return true
		}
	}
	return false
}

// HasMeta informa si el patrón usa comodines.
func HasMeta(pattern string) bool {
	return strings.ContainsAny(pattern, "*?{")
}

func compile(pattern string) *regexp.Regexp {
	if v, ok := cache.Load(pattern); ok {
		return v.(*regexp.Regexp)
	}
	re, err := regexp.Compile(toRegexp(pattern))
	if err != nil {
		re = never // un patrón imposible no coincide con nada; nunca hace fallar al lint
	}
	cache.Store(pattern, re)
	return re
}

var never = regexp.MustCompile(`[^\s\S]`)

func toRegexp(p string) string {
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimPrefix(p, "/")
	p = strings.TrimSuffix(p, "/")
	var b strings.Builder
	b.WriteString("^")
	depth := 0 // llaves {a,b} abiertas; se permiten anidadas
	for i := 0; i < len(p); {
		switch {
		case strings.HasPrefix(p[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 3
		case strings.HasPrefix(p[i:], "/**") && i+3 == len(p):
			b.WriteString("(?:/.*)?")
			i += 3
		case strings.HasPrefix(p[i:], "**"):
			b.WriteString(".*")
			i += 2
		case p[i] == '*':
			b.WriteString("[^/]*")
			i++
		case p[i] == '?':
			b.WriteString("[^/]")
			i++
		case p[i] == '{':
			b.WriteString("(?:")
			depth++
			i++
		case p[i] == '}' && depth > 0:
			b.WriteString(")")
			depth--
			i++
		case p[i] == ',' && depth > 0:
			b.WriteString("|")
			i++
		default:
			r, size := utf8.DecodeRuneInString(p[i:])
			b.WriteString(regexp.QuoteMeta(string(r)))
			i += size
		}
	}
	b.WriteString(strings.Repeat(")", depth))
	b.WriteString("$")
	return b.String()
}

// BracketsAsAny sustituye cada expresión entre corchetes ([abc], [!x],
// [[:alpha:]], []x]) por un ?: este paquete no entiende los corchetes, y para
// saber si un patrón de búsqueda (rg -g '.e[n]v') puede alcanzar un nombre
// basta leerlos de más. Una barra invertida cita el caracter que sigue, y un
// [ que no cierra queda como está.
//
// Lo que el shell lee de una manera propia no se imita: un tramo (lo que hay
// entre dos barras) con una equivalencia ([[=x=]]), un símbolo de colación
// ([[.x.]]) o una clase sin cierre ([[:alpha) se lee entero como *. Lo mismo uno
// de más de maxBracketSeg bytes. Cada tramo se lee por separado, en tiempo y
// memoria proporcionales a su largo: el análisis de un patrón largo con
// corchetes sin cerrar costaba el cubo de su largo.
func BracketsAsAny(p string) string {
	if !strings.Contains(p, "[") {
		return p
	}
	var b strings.Builder
	var x bracketScan
	for start := 0; ; {
		end := len(p)
		if i := strings.IndexByte(p[start:], '/'); i >= 0 {
			end = start + i
		}
		b.WriteString(x.segment(p[start:end]))
		if end == len(p) {
			break
		}
		b.WriteByte('/')
		start = end + 1
	}
	return b.String()
}

// maxBracketSeg acota el largo, en bytes, de un tramo cuyos corchetes se leen:
// el análisis guarda unos enteros por cada byte, y ningún patrón de búsqueda real
// pasa de unos cientos.
const maxBracketSeg = 64 << 10

// bracketScan guarda los arreglos del análisis de un tramo, para reusarlos.
type bracketScan struct {
	// res[k] dice cómo termina una expresión entre corchetes que se lee desde k: el
	// índice (exclusivo) donde cierra, 0 si el tramo se acaba sin cerrar, o -1 si
	// antes trae algo que no se imita ([=, [. o [: sin cierre).
	res []int32
	// colon[k] es el primer índice desde k donde hay :] o -1.
	colon []int32
}

func (x *bracketScan) segment(s string) string {
	if !strings.Contains(s, "[") {
		return s
	}
	n := len(s)
	if n > maxBracketSeg {
		return "*"
	}
	if cap(x.res) < n+2 {
		x.res, x.colon = make([]int32, n+2), make([]int32, n+2)
	}
	x.res, x.colon = x.res[:n+2], x.colon[:n+2]
	x.colon[n], x.colon[n+1] = -1, -1
	for i := n - 1; i >= 0; i-- {
		if s[i] == ':' && i+1 < n && s[i+1] == ']' {
			x.colon[i] = int32(i)
		} else {
			x.colon[i] = x.colon[i+1]
		}
	}
	x.res[n], x.res[n+1] = 0, 0
	for k := n - 1; k >= 0; k-- {
		var r int32
		switch c := s[k]; {
		case c == ']':
			r = int32(k + 1)
		case c == '[' && k+1 < n && (s[k+1] == '=' || s[k+1] == '.'):
			r = -1
		case c == '-' && k+2 < n && s[k+1] == '[' && (s[k+2] == '=' || s[k+2] == '.' || s[k+2] == ':'):
			// El final de un rango que empieza como una clase ([a-[:alpha:]]).
			r = -1
		case c == '[' && k+1 < n && s[k+1] == ':':
			if j := x.colon[k+2]; j >= 0 {
				r = x.res[j+2]
			} else {
				r = -1
			}
		case c == '\\' && k+1 < n:
			r = x.res[k+2]
		default:
			r = x.res[k+1]
		}
		x.res[k] = r
	}
	var b strings.Builder
	for i := 0; i < n; {
		switch c := s[i]; {
		case c == '\\' && i+1 < n:
			b.WriteString(s[i : i+2])
			i += 2
		case c == '[':
			// El primer ] tras el [ (o el [!) es un miembro más.
			p0 := i + 1
			if p0 < n && (s[p0] == '!' || s[p0] == '^') {
				p0++
			}
			var r int32
			switch {
			case p0 >= n:
			case s[p0] == ']':
				r = x.res[p0+1]
			default:
				r = x.res[p0]
			}
			switch {
			case r > 0:
				b.WriteByte('?')
				i = int(r)
			case r < 0:
				return "*"
			default:
				b.WriteByte('[')
				i++
			}
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}
