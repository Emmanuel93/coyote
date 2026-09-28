package product

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

var (
	mappingRe  = regexp.MustCompile(`@(Request|Get|Post|Put|Delete|Patch)Mapping\b`)
	stringLit  = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`)
	httpMethod = regexp.MustCompile(`RequestMethod\.(GET|POST|PUT|DELETE|PATCH)`)
	declRe     = regexp.MustCompile(`^\s*(?:(?:public|protected|private|abstract|final|static|sealed|open|internal|data|inner)\s+)*(class|interface|record|enum|object)\b`)
	feignRe    = regexp.MustCompile(`@FeignClient\b`)
	// WebClient y RestTemplate: .uri("/x"), .path("/x"), getForObject("/x"…
	uriCallRe = regexp.MustCompile(`\.(uri|path)\(\s*"([^"]*)"`)
	restTplRe = regexp.MustCompile(`\.(getForObject|getForEntity|postForObject|postForEntity|postForLocation|put|delete|patchForObject|exchange)\(\s*("([^"]*)"|[A-Za-z_][A-Za-z0-9_.]*\s*\+\s*"([^"]*)")`)
	verbRe    = regexp.MustCompile(`\.(get|post|put|delete|patch)\(\s*\)|HttpMethod\.(GET|POST|PUT|DELETE|PATCH)|\.method\(\s*HttpMethod\.(GET|POST|PUT|DELETE|PATCH)`)
	// Kafka
	listenerRe = regexp.MustCompile(`@KafkaListener\b`)
	sendRe     = regexp.MustCompile(`(?:kafkaTemplate|KafkaTemplate|template|producer)\s*\.\s*send\(\s*("([^"]+)"|([A-Za-z_][A-Za-z0-9_.]*))`)
	constRe    = regexp.MustCompile(`(?:static\s+final\s+String|const\s+val|final\s+static\s+String)\s+([A-Z_][A-Z0-9_]*)\s*(?::\s*String)?\s*=\s*"([^"]+)"`)
	topicLike  = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*(\.[a-z0-9][a-z0-9_-]*)+$`)
)

// constDef es una constante de texto declarada en un archivo.
type constDef struct{ file, class, value string }

// constTable guarda las constantes de texto por nombre: varias clases pueden
// declarar una constante con el mismo nombre (TOPIC) y valores distintos.
type constTable map[string][]constDef

// add registra las constantes de un archivo; la clase es el nombre del archivo.
func (t constTable) add(file, text string) {
	class := strings.TrimSuffix(path.Base(file), path.Ext(file))
	for _, m := range constRe.FindAllStringSubmatch(text, -1) {
		t[m[1]] = append(t[m[1]], constDef{file: file, class: class, value: m[2]})
	}
}

// unique devuelve el valor si todas las definiciones del nombre coinciden.
func (t constTable) unique(name string) (string, bool) {
	v := ""
	for _, d := range t[name] {
		if v != "" && d.value != v {
			return "", false
		}
		v = d.value
	}
	return v, v != ""
}

func (t constTable) inClass(class, name string) (string, bool) {
	for _, d := range t[name] {
		if d.class == class {
			return d.value, true
		}
	}
	return "", false
}

var staticImportRe = regexp.MustCompile(`(?m)^\s*import\s+static\s+(?:[\w]+\.)*(\w+)\.(\w+|\*)\s*;`)

// resolver resuelve una referencia a constante desde un archivo: primero la
// del mismo archivo; con clase (Topics.X) o import estático, la de esa clase
// en el módulo o en el repo; si no, solo un nombre con un único valor. Lo
// ambiguo queda sin resolver: mejor reportarlo que enlazar mal.
func resolver(file, text string, levels ...constTable) func(string) (string, bool) {
	imports := map[string]string{} // constante → clase
	var wildcard []string
	for _, m := range staticImportRe.FindAllStringSubmatch(text, -1) {
		if m[2] == "*" {
			wildcard = append(wildcard, m[1])
		} else {
			imports[m[2]] = m[1]
		}
	}
	return func(ref string) (string, bool) {
		name, class := ref, ""
		if i := strings.LastIndex(ref, "."); i >= 0 {
			name, class = ref[i+1:], ref[:i]
			if j := strings.LastIndex(class, "."); j >= 0 {
				class = class[j+1:]
			}
		}
		if class == "" {
			for _, t := range levels {
				for _, d := range t[name] {
					if d.file == file {
						return d.value, true
					}
				}
			}
		}
		classes := []string{class}
		if class == "" {
			classes = append([]string{imports[name]}, wildcard...)
		}
		for _, c := range classes {
			if c == "" {
				continue
			}
			for _, t := range levels {
				if v, ok := t.inClass(c, name); ok {
					return v, true
				}
			}
		}
		// Sin clase conocida: solo un nombre con un único valor; si en el
		// módulo es ambiguo, no se busca más lejos.
		for _, t := range levels {
			if v, ok := t.unique(name); ok {
				return v, true
			}
			if len(t[name]) > 0 {
				return "", false
			}
		}
		return "", false
	}
}

// balanced devuelve el texto entre el paréntesis que abre en i y el que lo
// cierra, respetando cadenas; "" si no hay paréntesis.
func balanced(text string, i int) (string, int) {
	for i < len(text) && (text[i] == ' ' || text[i] == '\t' || text[i] == '\n' || text[i] == '\r') {
		i++
	}
	if i >= len(text) || text[i] != '(' {
		return "", i
	}
	depth, inStr := 0, false
	for j := i; j < len(text); j++ {
		c := text[j]
		switch {
		case inStr:
			if c == '\\' {
				j++
			} else if c == '"' {
				inStr = false
			}
		case c == '"':
			inStr = true
		case c == '(':
			depth++
		case c == ')':
			depth--
			if depth == 0 {
				return text[i+1 : j], j + 1
			}
		}
	}
	return "", i
}

// topLevel parte los argumentos de una anotación por comas de primer nivel,
// respetando cadenas, llaves y paréntesis.
func topLevel(args string) []string {
	var parts []string
	depth, inStr, last := 0, false, 0
	for i := 0; i < len(args); i++ {
		c := args[i]
		switch {
		case inStr:
			if c == '\\' {
				i++
			} else if c == '"' {
				inStr = false
			}
		case c == '"':
			inStr = true
		case c == '{' || c == '(' || c == '[':
			depth++
		case c == '}' || c == ')' || c == ']':
			depth--
		case c == ',' && depth == 0:
			parts = append(parts, args[last:i])
			last = i + 1
		}
	}
	return append(parts, args[last:])
}

// mappingPaths saca las rutas de una anotación: el valor suelto, value= o
// path=, con una o varias cadenas. Sin ruta, la del método es la de la clase.
func mappingPaths(args string) []string {
	var out []string
	for _, part := range topLevel(args) {
		key, val, hasKey := strings.Cut(part, "=")
		if hasKey {
			k := strings.TrimSpace(key)
			if k != "value" && k != "path" {
				continue
			}
		} else {
			val = part
		}
		for _, m := range stringLit.FindAllStringSubmatch(val, -1) {
			out = append(out, m[1])
		}
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

// isTypeTarget informa si la anotación que termina en end cae sobre una clase.
func isTypeTarget(text string, end int) bool {
	rest := text[end:]
	for i := 0; i < 8; i++ {
		rest = strings.TrimLeft(rest, " \t\r\n")
		if strings.HasPrefix(rest, "@") {
			// otra anotación: se salta con sus argumentos
			j := 1
			for j < len(rest) && (rest[j] == '_' || rest[j] == '.' || (rest[j] >= 'a' && rest[j] <= 'z') || (rest[j] >= 'A' && rest[j] <= 'Z') || (rest[j] >= '0' && rest[j] <= '9')) {
				j++
			}
			if _, next := balanced(rest, j); next > j {
				rest = rest[next:]
			} else {
				rest = rest[j:]
			}
			continue
		}
		line := rest
		if k := strings.IndexAny(line, "{;("); k >= 0 {
			line = line[:k]
		}
		return declRe.MatchString(line)
	}
	return false
}

func joinPath(prefix, p string) string {
	switch {
	case prefix == "":
		return p
	case p == "":
		return prefix
	}
	return strings.TrimRight(prefix, "/") + "/" + strings.TrimLeft(p, "/")
}

// javaCtx es lo que un archivo Java o Kotlin necesita para resolver nombres:
// las constantes y las propiedades de Spring de su módulo.
type javaCtx struct {
	resolve func(string) (string, bool)
	props   map[string]string
	cfg     map[string]cfgClass // clases @ConfigurationProperties del repo, por nombre
}

// cfgClass es una clase @ConfigurationProperties: su prefijo y los valores
// por defecto de sus campos de texto.
type cfgClass struct {
	prefix   string
	defaults map[string]string
}

var (
	cfgPropsRe    = regexp.MustCompile(`@ConfigurationProperties\(\s*(?:(?:prefix|value)\s*=\s*)?"([^"]+)"`)
	cfgDefaultRe  = regexp.MustCompile(`\bString\s+(\w+)\s*=\s*"([^"]*)"`)
	getterCallRe  = regexp.MustCompile(`^(?:get)?([A-Za-z]\w*)$`)
	camelBoundary = regexp.MustCompile(`([a-z0-9])([A-Z])`)
)

// configClass lee una clase @ConfigurationProperties, si el archivo la declara.
func configClass(text string) (cfgClass, bool) {
	m := cfgPropsRe.FindStringSubmatch(text)
	if m == nil {
		return cfgClass{}, false
	}
	c := cfgClass{prefix: m[1], defaults: map[string]string{}}
	for _, d := range cfgDefaultRe.FindAllStringSubmatch(text, -1) {
		if _, ok := c.defaults[d[1]]; !ok {
			c.defaults[d[1]] = d[2]
		}
	}
	return c, true
}

func kebab(s string) string { return strings.ToLower(camelBoundary.ReplaceAllString(s, "$1-$2")) }

// configTopics busca, en un archivo que publica, las lecturas de una clase
// @ConfigurationProperties (topics.getCompleted()) y las resuelve con el
// application.yml del módulo o el valor por defecto del campo.
func configTopics(text string, cx javaCtx, publish func(line int, topic, raw string, unresolved bool)) {
	for class, c := range cx.cfg {
		if !strings.Contains(text, class) {
			continue
		}
		vars := regexp.MustCompile(`\b`+regexp.QuoteMeta(class)+`\s+(\w+)\s*[;,)=]`).FindAllStringSubmatch(text, -1)
		seen := map[string]bool{}
		for _, v := range vars {
			if seen[v[1]] {
				continue
			}
			seen[v[1]] = true
			calls := regexp.MustCompile(`\b`+regexp.QuoteMeta(v[1])+`\s*\.\s*(\w+)\(\s*\)`).FindAllStringSubmatchIndex(text, -1)
			for _, m := range calls {
				g := getterCallRe.FindStringSubmatch(text[m[2]:m[3]])
				if g == nil {
					continue
				}
				field := strings.ToLower(g[1][:1]) + g[1][1:]
				val, ok := "", false
				for _, key := range []string{c.prefix + "." + kebab(field), c.prefix + "." + field} {
					if raw, has := cx.props[key]; has {
						val, ok = cx.property(raw)
						break
					}
				}
				if !ok {
					val, ok = c.defaults[field]
				}
				if ok && topicLike.MatchString(val) {
					publish(lineOf(text, m[0]), val, v[1]+"."+text[m[2]:m[3]]+"()", false)
				}
			}
		}
	}
}

var placeholderRe = regexp.MustCompile(`^\$\{([^}:]+)(:([^}]*))?\}$`)

// property resuelve ${clave} o ${clave:valor} con application.yml o
// application.properties del módulo, o con el valor por defecto.
func (c javaCtx) property(raw string) (string, bool) {
	for depth := 0; depth < 3; depth++ {
		m := placeholderRe.FindStringSubmatch(strings.TrimSpace(raw))
		if m == nil {
			return raw, !strings.Contains(raw, "${")
		}
		if v, ok := c.props[m[1]]; ok {
			raw = v
			continue
		}
		if m[2] != "" && m[3] != "" {
			raw = m[3]
			continue
		}
		return "", false
	}
	return "", false
}

var (
	// @Value("${clave:valor}") String campo, en Java o Kotlin.
	valueFieldRe = regexp.MustCompile(`@Value\(\s*"\\?(\$\{[^"]+\})"\s*\)\s*(?:(?:private|protected|public|final|internal)\s+)*(?:String\s+(\w+)|(?:val|var)\s+(\w+))`)
	// productores: cualquier objeto.send( en un archivo que usa KafkaTemplate.
	anySendRe = regexp.MustCompile(`\b\w+\s*\.\s*send\(\s*("([^"]+)"|([A-Za-z_][A-Za-z0-9_.]*))`)
	// un método propio que publica (send, doSend, publish…) o un ProducerRecord con una constante.
	constCallRe = regexp.MustCompile(`(?:\b(?:do)?(?:[Ss]end|[Pp]ublish|[Ee]mit|[Pp]roduce|[Dd]ispatch)\w*|new\s+ProducerRecord(?:<[^>]*>)?)\(\s*((?:[A-Z]\w*\.)?[A-Z_][A-Z0-9_]{2,})\s*[,)]`)
)

// valueFields lee los campos y parámetros con @Value que resuelven a un texto.
func valueFields(text string, cx javaCtx) map[string]string {
	out := map[string]string{}
	for _, m := range valueFieldRe.FindAllStringSubmatch(text, -1) {
		name := m[2]
		if name == "" {
			name = m[3]
		}
		if v, ok := cx.property(m[1]); ok {
			out[name] = v
		}
	}
	return out
}

// springEntries saca de un archivo Java o Kotlin las rutas que expone (o que
// llama, si es un cliente Feign), las llamadas de WebClient y RestTemplate y
// los tópicos de Kafka que publica o escucha.
func springEntries(text, repo, mod, file string, cx javaCtx) []Entry {
	resolve := cx.resolve
	var out []Entry
	feign := feignRe.MatchString(text)
	role := Exposes
	if feign {
		role = Calls
	}
	base := "" // path de @FeignClient
	if feign {
		if i := strings.Index(text, "@FeignClient"); i >= 0 {
			if args, _ := balanced(text, i+len("@FeignClient")); args != "" {
				if m := feignPath.FindStringSubmatch(args); m != nil {
					base = m[1]
				}
			}
		}
	}
	prefix := base
	for _, loc := range mappingRe.FindAllStringSubmatchIndex(text, -1) {
		kind := text[loc[2]:loc[3]]
		args, end := balanced(text, loc[1])
		if end < loc[1] {
			end = loc[1]
		}
		paths := mappingPaths(args)
		if isTypeTarget(text, end) {
			if kind == "Request" {
				prefix = joinPath(base, paths[0])
			}
			continue
		}
		methods := []string{strings.ToUpper(kind)}
		if kind == "Request" {
			methods = nil
			for _, m := range httpMethod.FindAllStringSubmatch(args, -1) {
				methods = append(methods, m[1])
			}
			if len(methods) == 0 {
				methods = []string{""}
			}
		}
		for _, p := range paths {
			full := joinPath(prefix, p)
			norm := NormPath(full)
			if norm == "" {
				norm = "/"
			}
			for _, m := range methods {
				out = append(out, Entry{Repo: repo, Module: mod, Role: role, Method: m, Path: norm, Raw: full, File: file, Line: lineOf(text, loc[0])})
			}
		}
	}
	// Llamadas salientes: WebClient (.uri/.path) y RestTemplate.
	for _, loc := range uriCallRe.FindAllStringSubmatchIndex(text, -1) {
		raw := text[loc[4]:loc[5]]
		norm := NormPath(raw)
		if norm == "" || (text[loc[2]:loc[3]] == "path" && !strings.HasPrefix(raw, "/")) {
			continue
		}
		out = append(out, Entry{Repo: repo, Module: mod, Role: Calls, Method: verbBefore(text, loc[0]), Path: norm, Raw: raw, File: file, Line: lineOf(text, loc[0])})
	}
	for _, loc := range restTplRe.FindAllStringSubmatchIndex(text, -1) {
		raw := ""
		switch {
		case loc[6] >= 0:
			raw = text[loc[6]:loc[7]]
		case loc[8] >= 0:
			raw = text[loc[8]:loc[9]]
		}
		norm := NormPath(raw)
		if norm == "" {
			continue
		}
		method := map[string]string{"getForObject": "GET", "getForEntity": "GET", "postForObject": "POST", "postForEntity": "POST",
			"postForLocation": "POST", "put": "PUT", "delete": "DELETE", "patchForObject": "PATCH"}[text[loc[2]:loc[3]]]
		if method == "" {
			method = verbIn(text, loc[3]) // exchange: el HttpMethod de sus propios argumentos
		}
		out = append(out, Entry{Repo: repo, Module: mod, Role: Calls, Method: method, Path: norm, Raw: raw, File: file, Line: lineOf(text, loc[0])})
	}
	// Kafka: listeners y productores.
	for _, loc := range listenerRe.FindAllStringIndex(text, -1) {
		args, _ := balanced(text, loc[1])
		line := lineOf(text, loc[0])
		topics, unresolved := listenerTopics(args, cx)
		for _, t := range topics {
			out = append(out, Entry{Repo: repo, Module: mod, Role: Listens, Path: t, Raw: t, File: file, Line: line})
		}
		for _, u := range unresolved {
			out = append(out, Entry{Repo: repo, Module: mod, Role: Listens, Path: u, Raw: u, File: file, Line: line, Unresolved: true})
		}
	}
	// Productores. En un archivo que usa KafkaTemplate cuenta cualquier
	// objeto.send(…) y los métodos propios que reciben el tópico como constante.
	producer := strings.Contains(text, "KafkaTemplate") || strings.Contains(text, "kafkaTemplate")
	fields := valueFields(text, cx)
	seen := map[string]bool{}
	publish := func(line int, topic, raw string, unresolved bool) {
		k := fmt.Sprintf("%d|%s", line, topic)
		if seen[k] {
			return
		}
		seen[k] = true
		out = append(out, Entry{Repo: repo, Module: mod, Role: Publishes, Path: topic, Raw: raw, File: file, Line: line, Unresolved: unresolved})
	}
	re := sendRe
	if producer {
		re = anySendRe
	}
	for _, m := range re.FindAllStringSubmatchIndex(text, -1) {
		line := lineOf(text, m[0])
		switch {
		case m[4] >= 0:
			t := text[m[4]:m[5]]
			if strings.Contains(t, "${") {
				if v, ok := cx.property(t); ok {
					publish(line, v, t, !topicLike.MatchString(v))
					continue
				}
			}
			publish(line, t, t, !topicLike.MatchString(t))
		case m[6] >= 0:
			name := text[m[6]:m[7]]
			short := name[strings.LastIndex(name, ".")+1:]
			if v, ok := resolve(name); ok {
				publish(line, v, name, false)
			} else if v, ok := fields[short]; ok {
				publish(line, v, name, !topicLike.MatchString(v))
			} else if strings.ToUpper(short) == short {
				publish(line, name, name, true)
			}
		}
	}
	if producer {
		for _, m := range constCallRe.FindAllStringSubmatchIndex(text, -1) {
			name := text[m[2]:m[3]]
			if v, ok := resolve(name); ok && topicLike.MatchString(v) {
				publish(lineOf(text, m[0]), v, name, false)
			}
		}
		configTopics(text, cx, publish)
	}
	return out
}

// listenerTopics lee topics = "a" | {"a","b"} | CONSTANTE | "${propiedad}";
// lo que no se puede resolver queda sin resolver.
func listenerTopics(args string, cx javaCtx) (topics, unresolved []string) {
	resolve := cx.resolve
	i := strings.Index(args, "topics")
	if i < 0 {
		if strings.Contains(args, "topicPattern") {
			return nil, []string{"topicPattern"}
		}
		return nil, nil
	}
	rest := args[i+len("topics"):]
	rest = strings.TrimLeft(rest, " \t\n=")
	if strings.HasPrefix(rest, "{") {
		if j := strings.Index(rest, "}"); j >= 0 {
			rest = rest[:j+1]
		}
	} else if j := strings.IndexAny(rest, ",)"); j >= 0 {
		rest = rest[:j]
	}
	for _, m := range stringLit.FindAllStringSubmatch(rest, -1) {
		t := m[1]
		if strings.Contains(t, "${") {
			if v, ok := cx.property(t); ok {
				t = v
			}
		}
		if strings.Contains(t, "${") || !topicLike.MatchString(t) {
			unresolved = append(unresolved, m[1])
		} else {
			topics = append(topics, t)
		}
	}
	for _, id := range constRefRe.FindAllStringSubmatch(stringLit.ReplaceAllString(rest, `""`), -1) {
		if v, ok := resolve(id[1]); ok {
			topics = append(topics, v)
		} else {
			unresolved = append(unresolved, id[1])
		}
	}
	return topics, unresolved
}

// verbBefore busca el verbo HTTP de una llamada de WebClient antes de .uri,
// dentro de la misma sentencia.
func verbBefore(text string, i int) string {
	start := i - 400
	if start < 0 {
		start = 0
	}
	if j := strings.LastIndexAny(text[start:i], ";{}"); j >= 0 {
		start += j + 1
	}
	ms := verbRe.FindAllStringSubmatch(text[start:i], -1)
	if len(ms) == 0 {
		return ""
	}
	m := ms[len(ms)-1]
	for _, g := range m[1:] {
		if g != "" {
			return strings.ToUpper(g)
		}
	}
	return ""
}

// verbIn busca el HttpMethod en los argumentos de la llamada cuyo nombre
// termina en i (el paréntesis que sigue).
func verbIn(text string, i int) string {
	args, _ := balanced(text, i)
	if m := httpMethodAny.FindStringSubmatch(args); m != nil {
		return m[1]
	}
	return ""
}

var (
	// constRefRe reconoce CONSTANTE o Clase.CONSTANTE.
	constRefRe    = regexp.MustCompile(`\b((?:[A-Z][A-Za-z0-9_]*\.)?[A-Z_][A-Z0-9_]{2,})\b`)
	httpMethodAny = regexp.MustCompile(`HttpMethod\.(GET|POST|PUT|DELETE|PATCH)`)
	feignPath     = regexp.MustCompile(`path\s*=\s*"([^"]*)"`)
)
