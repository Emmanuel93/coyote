package gate

import (
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Un comando se revisa como lo correría el shell, no como texto: las
// comillas no esconden una palabra (terraform "apply"), el texto entre
// comillas que un programa ejecuta (bash -c, ssh, sudo…) se lee como otro
// comando, y una variable asignada antes se reemplaza por su valor. Los
// datos (el patrón de grep, lo que imprime echo, el mensaje de un commit) no
// son comandos: mencionarlos no toca nada.

// lseg es un segmento de lenientSegs. subst indica que está dentro de una
// sustitución ($( ), acentos graves, <( )): su salida forma parte de otro
// comando, así que sus datos cuentan.
type lseg struct {
	words []lword
	subst bool
}

// lenientSegs parte un comando en segmentos de palabras sin rechazar nada:
// separa en ; & | saltos, paréntesis, llaves y sustituciones. Quita las
// comillas, incluidas las de ANSI C ($'\x61'), y deja juntas las llaves de
// una expansión ({apply,destroy}).
func lenientSegs(cmd string) []lseg {
	var segs []lseg
	var cur []lword
	var b strings.Builder
	in, quoted, subst := false, false, false
	depth, tick, curSubst := 0, false, false
	flush := func() {
		if in {
			cur = append(cur, lword{b.String(), quoted, subst})
		}
		b.Reset()
		in, quoted, subst = false, false, false
	}
	push := func() {
		flush()
		if len(cur) > 0 {
			segs = append(segs, lseg{cur, curSubst})
		}
		cur = nil
	}
	r := []rune(cmd)
	for i := 0; i < len(r); i++ {
		c := r[i]
		switch {
		case c == '\'':
			j := i + 1
			for j < len(r) && r[j] != '\'' {
				j++
			}
			b.WriteString(string(r[i+1 : min(j, len(r))]))
			in, quoted, i = true, true, j
		case c == '$' && i+1 < len(r) && r[i+1] == '\'':
			// $'…': comillas de ANSI C, con escapes que el shell traduce.
			j, text := ansiC(r, i+2)
			b.WriteString(text)
			in, quoted, i = true, true, j
		case c == '$' && i+1 < len(r) && r[i+1] == '"':
			// $"…" es "…" traducido: se lee igual.
		case c == '"':
			j := i + 1
			for ; j < len(r) && r[j] != '"'; j++ {
				if r[j] == '\\' && j+1 < len(r) {
					j++
				}
				if r[j] == '`' || (r[j] == '$' && j+1 < len(r) && r[j+1] == '(') {
					subst = true
				}
				b.WriteRune(r[j])
			}
			in, quoted, i = true, true, j
		case c == '\\':
			if i+1 < len(r) {
				i++
				b.WriteRune(r[i])
				in = true
			}
		case c == ' ' || c == '\t':
			flush()
		case c == '$' && i+1 < len(r) && r[i+1] == '(':
			push()
			depth++
			curSubst = true
			i++
		case c == '(' && i > 0 && (r[i-1] == '<' || r[i-1] == '>'):
			// <( … ) y >( … ): sustitución de procesos.
			push()
			depth++
			curSubst = true
		case c == '$' && i+1 < len(r) && r[i+1] == '{':
			// ${VAR} es parte de la palabra, no una llave del shell.
			j := i + 2
			for j < len(r) && r[j] != '}' {
				j++
			}
			b.WriteString(string(r[i:min(j+1, len(r))]))
			in, i = true, j
		case c == ')':
			push()
			if depth > 0 {
				depth--
			}
			curSubst = depth > 0 || tick
		case c == '`':
			push()
			tick = !tick
			curSubst = depth > 0 || tick
		case c == '{' && braceWord(r, i):
			j := i + 1
			for r[j] != '}' {
				j++
			}
			b.WriteString(string(r[i : j+1]))
			in, i = true, j
		case strings.ContainsRune(";&|\n(){}", c):
			push()
		default:
			b.WriteRune(c)
			in = true
		}
	}
	push()
	return segs
}

// braceWord dice si la llave en i abre una expansión ({a,b} o {1..3}) y no
// un grupo de comandos ({ cmd; }).
func braceWord(r []rune, i int) bool {
	if i+1 >= len(r) || r[i+1] == ' ' || r[i+1] == '\t' || r[i+1] == '\n' {
		return false
	}
	comma := false
	for j := i + 1; j < len(r); j++ {
		switch r[j] {
		case '}':
			return comma
		case ',':
			comma = true
		case '.':
			if j+1 < len(r) && r[j+1] == '.' {
				comma = true
			}
		case ' ', '\t', '\n', ';', '|', '&', '(', ')', '{', '\'', '"', '`', '$':
			return false
		}
	}
	return false
}

// ansiC lee el texto de $'…' desde start y devuelve dónde cierra y el texto
// con sus escapes traducidos.
func ansiC(r []rune, start int) (int, string) {
	var b strings.Builder
	i := start
	for ; i < len(r) && r[i] != '\''; i++ {
		if r[i] != '\\' || i+1 >= len(r) {
			b.WriteRune(r[i])
			continue
		}
		i++
		switch c := r[i]; c {
		case 'a':
			b.WriteByte('\a')
		case 'b':
			b.WriteByte('\b')
		case 'e', 'E':
			b.WriteByte(0x1b)
		case 'f':
			b.WriteByte('\f')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'v':
			b.WriteByte('\v')
		case 'c':
			if i+1 < len(r) {
				i++
				b.WriteRune(r[i] & 0x1f)
			}
		case 'x', 'u', 'U':
			max := map[rune]int{'x': 2, 'u': 4, 'U': 8}[c]
			j := i + 1
			for j < len(r) && j-i-1 < max && strings.ContainsRune("0123456789abcdefABCDEF", r[j]) {
				j++
			}
			if j == i+1 {
				b.WriteRune('\\')
				b.WriteRune(c)
				continue
			}
			v, _ := strconv.ParseUint(string(r[i+1:j]), 16, 32)
			if c == 'x' {
				b.WriteByte(byte(v))
			} else if utf8.ValidRune(rune(v)) {
				b.WriteRune(rune(v))
			}
			i = j - 1
		case '0', '1', '2', '3', '4', '5', '6', '7':
			j := i
			for j < len(r) && j-i < 3 && r[j] >= '0' && r[j] <= '7' {
				j++
			}
			v, _ := strconv.ParseUint(string(r[i:j]), 8, 16)
			b.WriteByte(byte(v))
			i = j - 1
		default:
			// \\ \' \" \? y cualquier otro: el caracter mismo.
			b.WriteRune(c)
		}
	}
	return i, b.String()
}

// expandBraces expande la primera llave de una palabra ({apply,destroy}); los
// rangos ({1..3}) y lo demás quedan como están. A lo sumo 64 resultados.
func expandBraces(w string) []string {
	i := strings.IndexByte(w, '{')
	if i < 0 {
		return []string{w}
	}
	j := strings.IndexByte(w[i:], '}')
	if j < 0 || !strings.Contains(w[i:i+j], ",") {
		return []string{w}
	}
	j += i
	var out []string
	for _, alt := range strings.Split(w[i+1:j], ",") {
		for _, rest := range expandBraces(w[j+1:]) {
			if len(out) == 64 {
				return out
			}
			if s := w[:i] + alt + rest; s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

// executors ejecutan lo que reciben por la entrada, en un script o en sus
// argumentos: con uno en el comando, lo que parece dato puede ser código.
var executors = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "fish": true, "csh": true,
	"tcsh": true, "eval": true, "source": true, ".": true, "xargs": true, "parallel": true, "python": true, "python3": true,
	"python2": true, "node": true, "deno": true, "bun": true, "perl": true, "ruby": true, "php": true, "lua": true,
	"osascript": true, "pwsh": true, "powershell": true, "awk": true, "gawk": true, "sed": true}

// wrappers corren el comando que sigue: el programa de verdad es el siguiente.
var wrappers = map[string]bool{"sudo": true, "doas": true, "env": true, "nice": true, "nohup": true, "time": true,
	"timeout": true, "command": true, "builtin": true, "exec": true, "stdbuf": true, "ionice": true, "caffeinate": true,
	"chronic": true, "unbuffer": true, "eatmydata": true, "watch": true, "!": true, "then": true, "do": true, "else": true,
	"elif": true, "if": true, "while": true, "until": true}

// wrapperOpts son las opciones de un envoltorio que llevan un valor aparte.
var wrapperOpts = map[string]map[string]bool{
	"sudo":    set("-u", "-g", "-h", "-p", "-C", "-D", "-r", "-t", "-U", "-T", "--user", "--group", "--host", "--prompt", "--chdir"),
	"doas":    set("-u", "-C"),
	"env":     set("-u", "--unset", "-C", "--chdir", "-S", "--split-string"),
	"nice":    set("-n", "--adjustment"),
	"ionice":  set("-c", "-n", "-p", "--class", "--classdata", "--pid"),
	"timeout": set("-s", "-k", "--signal", "--kill-after"),
	"watch":   set("-n", "--interval", "-d"),
	"stdbuf":  set("-i", "-o", "-e"),
}

// mainProg devuelve el programa que corre un segmento y dónde está: salta
// asignaciones, envoltorios (sudo, env, timeout 30…), sus opciones y sus
// valores.
func mainProg(words []string) (string, int) {
	for i := 0; i < len(words); i++ {
		w := words[i]
		if assignRe.MatchString(w) {
			continue
		}
		base := strings.ToLower(path.Base(w))
		if !wrappers[base] {
			return base, i
		}
		at := i
		for i+1 < len(words) {
			next := words[i+1]
			switch {
			case wrapperOpts[base][next]:
				i += 2
				continue
			case strings.HasPrefix(next, "-") || assignRe.MatchString(next),
				base == "timeout" && strings.Trim(next, "0123456789.smhd") == "":
				i++
				continue
			}
			break
		}
		if i+1 >= len(words) {
			// Sin comando después, el envoltorio es el programa: env a secas
			// imprime las variables.
			return base, at
		}
	}
	return "", -1
}

// dataPrograms reciben datos entre comillas: patrones, textos o mensajes. Lo
// que va entre comillas en ellos no se lee como comando.
var dataPrograms = map[string]bool{"grep": true, "egrep": true, "fgrep": true, "rg": true, "ag": true, "ack": true,
	"echo": true, "printf": true, "print": true, "git": true, "gh": true, "glab": true, "jq": true, "yq": true,
	"tr": true, "cut": true, "sort": true, "uniq": true, "head": true, "tail": true, "wc": true, "diff": true, "cat": true,
	"less": true, "tee": true, "column": true, "comm": true, "nl": true, "coyote": true, "curl": true, "wget": true}

// view son los segmentos de un comando, palabra por palabra, como los
// correría el shell. Con dropData, los datos se reemplazan por _ (salvo que
// el comando tenga un programa que ejecuta lo que recibe).
func view(cmd string, dropData bool) [][]string {
	return viewOf(neutralize(cmd), dropData, 0)
}

func viewOf(text string, dropData bool, depth int) [][]string {
	segs := lenientSegs(text)
	exec := false
	for _, s := range segs {
		prog, _ := mainProg(texts(s.words))
		if executors[prog] {
			exec = true
		}
	}
	vars := map[string]string{}
	var out [][]string
	for _, s := range segs {
		words := texts(s.words)
		// Una asignación suelta (a=apply o export a=apply) se recuerda para
		// los segmentos que siguen.
		if assigns(words) {
			for _, w := range words {
				if m := assignRe.FindStringSubmatch(w); m != nil {
					vars[m[1]] = expandVars(m[2], vars)
				}
			}
		}
		for i := range words {
			words[i] = expandVars(words[i], vars)
		}
		prog, at := mainProg(words)
		if prog == "eval" && depth < 4 {
			// eval corre sus argumentos como un comando.
			out = append(out, viewOf(strings.Join(words[at+1:], " "), dropData, depth+1)...)
		}
		drop := dropData && !exec && !s.subst
		data := map[int]bool{}
		if drop {
			data = dataWords(prog, words, at)
		}
		var line []string
		var nested [][]string
		for i, w := range s.words {
			text := words[i]
			if data[i] && !w.subst {
				line = append(line, "_")
				continue
			}
			// El texto entre comillas es un comando si lo recibe un programa que
			// ejecuta, si trae una sustitución o si hay un ejecutor en el comando.
			if depth < 4 && (w.subst || (w.quoted && strings.ContainsAny(text, " \t\n;|&") && (exec || !dataPrograms[prog]))) {
				nested = append(nested, viewOf(text, dropData, depth+1)...)
			}
			line = append(line, expandBraces(text)...)
		}
		if len(line) > 0 {
			out = append(out, line)
		}
		out = append(out, nested...)
	}
	return out
}

// assigns dice si un segmento solo asigna variables (a=1, export a=1).
func assigns(words []string) bool {
	if len(words) == 0 {
		return false
	}
	i := 0
	switch words[0] {
	case "export", "declare", "typeset", "local", "readonly":
		i = 1
	}
	if i >= len(words) {
		return false
	}
	for _, w := range words[i:] {
		if !assignRe.MatchString(w) {
			return false
		}
	}
	return true
}

// varUse reconoce $a y ${a}.
var varUse = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

// expandVars reemplaza $a y ${a} por los valores conocidos.
func expandVars(s string, vars map[string]string) string {
	if len(vars) == 0 || !strings.Contains(s, "$") {
		return s
	}
	return varUse.ReplaceAllStringFunc(s, func(m string) string {
		sm := varUse.FindStringSubmatch(m)
		name := sm[1] + sm[2]
		if v, ok := vars[name]; ok {
			return v
		}
		return m
	})
}

// dataWords marca las palabras de un segmento que son datos: lo que imprime
// echo (salvo las redirecciones), el patrón de una búsqueda y los valores de
// git log --grep, --author o -S.
func dataWords(prog string, words []string, at int) map[int]bool {
	data := map[int]bool{}
	if at < 0 {
		return data
	}
	args := words[at+1:]
	switch prog {
	case "echo", "printf", "print":
		redirect := false
		for i, w := range args {
			switch {
			case redirect:
				redirect = false
			case w == ">" || w == ">>" || w == "1>" || w == "2>" || w == "&>" || w == ">|" || w == "<" || w == "1>>" || w == "2>>":
				redirect = true
			case strings.HasPrefix(w, ">") || strings.HasPrefix(w, "<") || strings.HasPrefix(w, "1>") || strings.HasPrefix(w, "2>") || strings.HasPrefix(w, "&>"):
			default:
				data[at+1+i] = true
			}
		}
	case "grep", "egrep", "fgrep", "rg":
		for i := range parseSearch(prog, args).pattern {
			data[at+1+i] = true
		}
	case "git":
		sub, off := gitSub(args)
		if sub == "grep" {
			for i := range parseSearch("git-grep", args[off+1:]).pattern {
				data[at+2+off+i] = true
			}
			break
		}
		for i := 0; i < len(args); i++ {
			w := args[i]
			for _, f := range []string{"--grep", "--author", "--committer", "--format", "--pretty", "-S", "-G"} {
				switch {
				case w == f && i+1 < len(args):
					data[at+2+i] = true
				case strings.HasPrefix(w, f+"=") || (len(f) == 2 && strings.HasPrefix(w, f) && len(w) > 2):
					data[at+1+i] = true
				}
			}
		}
	}
	return data
}

// gitSub devuelve el subcomando de git y su posición en args, saltando las
// opciones globales (-C dir, -c clave=valor, --no-pager…).
func gitSub(args []string) (string, int) {
	for i := 0; i < len(args); i++ {
		switch w := args[i]; {
		case w == "-C" || w == "-c" || w == "--git-dir" || w == "--work-tree" || w == "--namespace":
			i++
		case strings.HasPrefix(w, "-"):
		default:
			return w, i
		}
	}
	return "", -1
}

// searchSpec es lo que se sabe de los argumentos de grep, rg o git grep.
type searchSpec struct {
	pattern   map[int]bool // el patrón de la búsqueda: dato
	files     []int        // archivos o carpetas donde busca
	globs     []string     // patrones de nombres que incluye (-g, --glob, --include)
	globWords map[int]bool // palabras con patrones de nombres, que incluyen o excluyen
	reads     []string     // archivos que lee además de los operandos (-f patrones)
}

// searchOpts son las opciones que llevan un valor aparte, por programa.
var searchOpts = map[string]map[string]bool{
	"grep": set("-e", "-f", "-m", "-A", "-B", "-C", "-d", "-D", "--regexp", "--file", "--max-count", "--after-context",
		"--before-context", "--context", "--directories", "--devices", "--include", "--exclude", "--exclude-dir",
		"--exclude-from", "--label", "--binary-files", "--group-separator"),
	"rg": set("-e", "-f", "-g", "-t", "-T", "-m", "-A", "-B", "-C", "-M", "-j", "-r", "-E", "-d", "--regexp", "--file",
		"--glob", "--iglob", "--type", "--type-not", "--type-add", "--type-clear", "--max-count", "--after-context",
		"--before-context", "--context", "--max-columns", "--threads", "--replace", "--encoding", "--max-depth", "--maxdepth",
		"--color", "--colors", "--sort", "--sortr", "--path-separator", "--pre", "--pre-glob", "--max-filesize",
		"--dfa-size-limit", "--regex-size-limit", "--engine", "--context-separator", "--field-context-separator",
		"--field-match-separator", "--ignore-file", "--hostname-bin", "--hyperlink-format", "--generate"),
	"git-grep": set("-e", "-f", "-A", "-B", "-C", "-m", "--max-count", "--max-depth", "--threads", "--after-context",
		"--before-context", "--context", "--and", "--or", "--not"),
}

func set(xs ...string) map[string]bool {
	m := map[string]bool{}
	for _, x := range xs {
		m[x] = true
	}
	return m
}

// parseSearch lee los argumentos de una búsqueda (sin el programa) como lo
// hacen grep, rg y git grep: las opciones pueden ir después de los
// operandos, y si hay -e o -f, todos los operandos son archivos.
func parseSearch(prog string, args []string) searchSpec {
	switch prog {
	case "egrep", "fgrep":
		prog = "grep"
	}
	opts := searchOpts[prog]
	if opts == nil {
		opts = searchOpts["grep"]
	}
	sp := searchSpec{pattern: map[int]bool{}, globWords: map[int]bool{}}
	explicit := false
	var operands []int
	// value anota el valor de una opción; at es la palabra que lo lleva.
	value := func(opt, v string, at int) {
		switch opt {
		case "-e", "--regexp":
			explicit = true
			sp.pattern[at] = true
		case "-f", "--file":
			explicit = true
			sp.reads = append(sp.reads, v)
		case "-g", "--glob", "--iglob", "--include", "--exclude", "--exclude-dir":
			sp.globWords[at] = true
			if opt != "--exclude" && opt != "--exclude-dir" && !strings.HasPrefix(v, "!") {
				sp.globs = append(sp.globs, v)
			}
		}
	}
	rest := false
	for i := 0; i < len(args); i++ {
		w := args[i]
		switch {
		case rest || w == "-" || !strings.HasPrefix(w, "-"):
			operands = append(operands, i)
		case w == "--":
			rest = true
		case strings.HasPrefix(w, "--"):
			name, v, glued := strings.Cut(w, "=")
			if !opts[name] {
				continue
			}
			if glued {
				value(name, v, i)
			} else if i+1 < len(args) {
				i++
				value(name, args[i], i)
			}
		default:
			// Un grupo de opciones cortas: la primera que lleva valor toma el
			// resto del grupo o la palabra siguiente.
			for k := 1; k < len(w); k++ {
				opt := "-" + string(w[k])
				if !opts[opt] {
					continue
				}
				if k+1 < len(w) {
					value(opt, w[k+1:], i)
				} else if i+1 < len(args) {
					i++
					value(opt, args[i], i)
				}
				break
			}
		}
	}
	for n, i := range operands {
		if n == 0 && !explicit {
			sp.pattern[i] = true
			continue
		}
		sp.files = append(sp.files, i)
	}
	return sp
}

// segText une las palabras de un segmento.
func segText(seg []string) string { return strings.Join(seg, " ") }
