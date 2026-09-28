package product

import (
	"regexp"
	"strings"
)

var (
	// Dart: dio.get('/x'), _dio.post<T>("/x/$id"), http.get(Uri.parse('$base/x'))
	dartCallRe = regexp.MustCompile(`\.(get|post|put|patch|delete)(?:<(?:[^<>()]|<[^<>()]*>)*>)?\(\s*(?:Uri\.parse\(\s*)?(['"])((?:[^'"\\]|\\.)*)['"]`)
	// TypeScript/JavaScript: request('/x'), fetch(`${API}/x/${id}`), axios.get('/x'), api.post("/x")
	tsCallRe   = regexp.MustCompile(`\b(fetch|request|axios|[A-Za-z_$][A-Za-z0-9_$]*\.(?:get|post|put|patch|delete|request))\s*(?:<(?:[^<>()]|<[^<>()]*>)*>)?\(\s*(['"` + "`" + `])((?:[^'"` + "`" + `\\]|\\.)*)['"` + "`" + `]`)
	tsMethodRe = regexp.MustCompile(`method\s*:\s*['"](GET|POST|PUT|PATCH|DELETE|get|post|put|patch|delete)['"]`)
)

// looksLikeAPIPath descarta textos que no son rutas de API (claves, rutas de
// pantallas con un solo segmento de navegación, archivos).
func looksLikeAPIPath(raw string) bool {
	if raw == "" {
		return false
	}
	return strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") ||
		strings.HasPrefix(raw, "$") || strings.HasPrefix(raw, "{")
}

// dartCalls saca las llamadas HTTP de un archivo Dart (Dio o http).
func dartCalls(text, repo, mod, file string) []Entry {
	var out []Entry
	for _, loc := range dartCallRe.FindAllStringSubmatchIndex(text, -1) {
		raw := text[loc[6]:loc[7]]
		if !looksLikeAPIPath(raw) {
			continue
		}
		norm := NormPath(raw)
		if norm == "" || Trivial(norm) {
			continue
		}
		out = append(out, Entry{Repo: repo, Module: mod, Role: Calls, Method: strings.ToUpper(text[loc[2]:loc[3]]),
			Path: norm, Raw: raw, File: file, Line: lineOf(text, loc[0])})
	}
	return out
}

// tsCalls saca las llamadas HTTP de un archivo TypeScript o JavaScript.
func tsCalls(text, repo, mod, file string) []Entry {
	var out []Entry
	for _, loc := range tsCallRe.FindAllStringSubmatchIndex(text, -1) {
		fn := text[loc[2]:loc[3]]
		raw := text[loc[6]:loc[7]]
		if !looksLikeAPIPath(raw) {
			continue
		}
		norm := NormPath(raw)
		if norm == "" || Trivial(norm) {
			continue
		}
		method := ""
		if i := strings.LastIndex(fn, "."); i >= 0 {
			method = strings.ToUpper(fn[i+1:])
			if method == "REQUEST" {
				method = ""
			}
		}
		if method == "" {
			// El método va en las opciones de esta misma llamada, nunca en la siguiente.
			args := ""
			if open := strings.LastIndex(text[:loc[4]], "("); open >= loc[0] {
				args = jsArgs(text, open)
			}
			if m := tsMethodRe.FindStringSubmatch(args); m != nil {
				method = strings.ToUpper(m[1])
			} else if fn == "fetch" || fn == "request" {
				method = "GET" // sin method: fetch hace GET
			}
		}
		out = append(out, Entry{Repo: repo, Module: mod, Role: Calls, Method: method, Path: norm, Raw: raw, File: file, Line: lineOf(text, loc[0])})
	}
	return out
}

// jsArgs devuelve el texto entre el paréntesis que abre en open y el que lo
// cierra, saltando cadenas ('…', "…" y `…`) y anidamientos; acota la
// búsqueda a 4 KB.
func jsArgs(text string, open int) string {
	depth := 0
	limit := open + 4096
	if limit > len(text) {
		limit = len(text)
	}
	for i := open; i < limit; i++ {
		switch c := text[i]; c {
		case '\'', '"', '`':
			for i++; i < limit && text[i] != c; i++ {
				if text[i] == '\\' {
					i++
				}
			}
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
			if depth == 0 {
				return text[open+1 : i]
			}
		}
	}
	return text[open+1 : limit]
}
