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
func BracketsAsAny(p string) string {
	if !strings.Contains(p, "[") {
		return p
	}
	var b strings.Builder
	for i := 0; i < len(p); {
		switch {
		case p[i] == '\\' && i+1 < len(p):
			b.WriteString(p[i : i+2])
			i += 2
		case p[i] == '[':
			if n := bracketLen(p[i:]); n > 0 {
				b.WriteByte('?')
				i += n
				continue
			}
			b.WriteByte('[')
			i++
		default:
			b.WriteByte(p[i])
			i++
		}
	}
	return b.String()
}

// bracketLen es el largo de la expresión entre corchetes que empieza en s[0],
// o 0 si no cierra. El primer ] (tras el [ o el [!) es un miembro más, y
// [:clase:], [=c=] y [.c.] llevan su propio cierre.
func bracketLen(s string) int {
	i := 1
	if i < len(s) && (s[i] == '!' || s[i] == '^') {
		i++
	}
	for first := true; i < len(s); first = false {
		switch c := s[i]; {
		case c == ']' && !first:
			return i + 1
		case c == '[' && i+1 < len(s) && (s[i+1] == ':' || s[i+1] == '=' || s[i+1] == '.'):
			if j := strings.Index(s[i+2:], string(s[i+1])+"]"); j >= 0 {
				i += 2 + j + 2
			} else {
				i++
			}
		case c == '\\' && i+1 < len(s):
			i += 2
		default:
			i++
		}
	}
	return 0
}
