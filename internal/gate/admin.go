package gate

import (
	"path"
	"regexp"
	"strings"
)

// Un agente nunca corre coyote approve, reject, revoke, auth, hooks,
// install, run, ws run ni ws continue: eso lo hace la persona en su terminal.
// La revisión no enumera envoltorios (watch, parallel, find -exec…): busca la
// palabra coyote seguida del subcomando en cualquier lugar de cada segmento,
// sigue las variables que guardan coyote y lee como comando el texto entre
// comillas que el shell podría ejecutar (bash -c, eval, script -c…). El texto
// que es dato (un mensaje de commit, un patrón de grep) no cuenta.

// adminVerbs son los subcomandos que solo corre la persona.
var adminVerbs = map[string]bool{"approve": true, "reject": true, "revoke": true, "auth": true, "hooks": true, "install": true, "run": true}

// dataCommands son programas cuyos argumentos entre comillas son datos, no
// comandos: sus patrones o mensajes pueden mencionar coyote run sin correrlo.
var dataCommands = map[string]bool{"grep": true, "egrep": true, "fgrep": true, "rg": true, "ag": true, "ack": true,
	"sed": true, "awk": true, "gawk": true, "echo": true, "printf": true, "jq": true, "yq": true, "tr": true, "cut": true,
	"sort": true, "uniq": true, "head": true, "tail": true, "wc": true, "diff": true, "cat": true, "less": true,
	"git": true, "gh": true, "glab": true, "tee": true, "column": true, "comm": true, "nl": true}

// shells leen un programa por la entrada estándar si no traen -c.
var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "fish": true, "csh": true, "tcsh": true}

var (
	// messageArg son los textos de mensajes y títulos: datos, salvo que
	// traigan una sustitución, que el shell sí ejecuta.
	messageArg = regexp.MustCompile(`(?i)(^|\s)(-m|--message|-b|--body|-t|--title|--reason|--notes?|--description)(=|\s+)('[^']*'|"[^"$` + "`" + `]*")`)
	// noteText es el texto de coyote note y coyote record.
	noteText   = regexp.MustCompile(`(?i)(\bcoyote\s+(?:-C\s+\S+\s+)*(?:note|record\s+\S+)\s+)('[^']*'|"[^"$` + "`" + `]*")`)
	assignRe   = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)=(.*)$`)
	mentionsRe = regexp.MustCompile(`(?i)\bcoyote\b.*\b(approve|reject|revoke|auth|hooks|install|run|ws\s+(run|continue))\b`)
)

// lword es una palabra de shell sin comillas; quoted dice si traía comillas.
type lword struct {
	text   string
	quoted bool
	subst  bool // traía $( o ` dentro de comillas dobles
}

// isCoyoteAdmin dice si cmd correría uno de los subcomandos de la persona.
func isCoyoteAdmin(cmd string) bool {
	cmd = strings.NewReplacer("${IFS}", " ", "$IFS", " ", "$'", "'", `$"`, `"`, "\\\r\n", "", "\\\n", "").Replace(cmd)
	return adminIn(cmd, 0)
}

func adminIn(cmd string, depth int) bool {
	if depth > 4 {
		return mentionsRe.MatchString(cmd)
	}
	cmd = messageArg.ReplaceAllString(cmd, "${1}${2}${3}_")
	cmd = noteText.ReplaceAllString(cmd, "${1}_")
	segs := lenientSplit(cmd)
	vars := map[string]bool{}
	for _, seg := range segs {
		for _, w := range seg {
			if m := assignRe.FindStringSubmatch(w.text); m != nil && isCoyoteWord(firstField(m[2]), nil) {
				vars[m[1]] = true
			}
		}
	}
	for _, seg := range segs {
		if adminWords(seg, vars) {
			return true
		}
		prog := program(seg)
		// Un shell sin -c corre un script: sus argumentos entre comillas son datos.
		scriptArgs := shells[prog] && !hasShortFlag(seg, 'c')
		for _, w := range seg {
			text := w.text
			if m := assignRe.FindStringSubmatch(text); m != nil {
				text = m[2]
			}
			// Lo que va entre comillas se lee como comando si lo recibe un
			// programa que ejecuta (o trae una sustitución, que se ejecuta siempre).
			if w.quoted && (w.subst || (!dataCommands[prog] && !scriptArgs)) && adminIn(text, depth+1) {
				return true
			}
		}
		// echo "coyote ws run W" | bash: un shell sin script ni -c lee su
		// programa de la entrada.
		if scriptArgs && len(positionalWords(seg)) == 0 && mentionsRe.MatchString(cmd) {
			return true
		}
	}
	return false
}

// adminWords busca coyote (o una variable que lo guarda) seguido de un
// subcomando de la persona, en cualquier posición del segmento.
func adminWords(seg []lword, vars map[string]bool) bool {
	for i := range seg {
		text := seg[i].text
		if m := assignRe.FindStringSubmatch(text); m != nil {
			text = m[2]
		}
		// Un texto entre comillas con espacios es una sola palabra: si es un
		// comando, lo lee adminIn cuando lo recibe un programa que ejecuta.
		if seg[i].quoted && len(strings.Fields(text)) > 1 {
			continue
		}
		if !isCoyoteWord(strings.TrimSpace(text), vars) {
			continue
		}
		rest := texts(seg[i+1:])
		j := 0
		for j+1 < len(rest) && rest[j] == "-C" {
			j += 2
		}
		if j >= len(rest) {
			continue
		}
		verb := strings.ToLower(rest[j])
		if adminVerbs[verb] {
			return true
		}
		if verb == "ws" && j+1 < len(rest) {
			if next := strings.ToLower(rest[j+1]); next == "run" || next == "continue" {
				return true
			}
		}
	}
	return false
}

// isCoyoteWord reconoce coyote, ./bin/coyote, go run ./cmd/coyote o una
// variable que lo guarda ($c, ${c}).
func isCoyoteWord(w string, vars map[string]bool) bool {
	if strings.EqualFold(path.Base(strings.TrimSuffix(w, "/")), "coyote") {
		return true
	}
	name := strings.TrimPrefix(w, "$")
	if name == w {
		return false
	}
	name = strings.TrimSuffix(strings.TrimPrefix(name, "{"), "}")
	return vars[name]
}

func firstField(s string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return f[0]
	}
	return ""
}

func texts(ws []lword) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.text
	}
	return out
}

// program es el programa del segmento: la primera palabra que no es una
// asignación ni un prefijo del shell.
func program(seg []lword) string {
	for _, w := range seg {
		if assignRe.MatchString(w.text) {
			continue
		}
		switch w.text {
		case "!", "then", "do", "else", "elif", "if", "while", "until", "time", "exec", "command", "builtin", "export":
			continue
		}
		return strings.ToLower(path.Base(w.text))
	}
	return ""
}

// positionalWords son las palabras después del programa que no son opciones.
func positionalWords(seg []lword) []string {
	var out []string
	seen := false
	for _, w := range seg {
		if !seen {
			if assignRe.MatchString(w.text) {
				continue
			}
			seen = true
			continue
		}
		if !strings.HasPrefix(w.text, "-") {
			out = append(out, w.text)
		}
	}
	return out
}

func hasShortFlag(seg []lword, f rune) bool {
	for _, w := range seg[1:] {
		if strings.HasPrefix(w.text, "-") && !strings.HasPrefix(w.text, "--") && strings.ContainsRune(w.text[1:], f) {
			return true
		}
	}
	return false
}

// lenientSplit parte un comando en segmentos de palabras sin rechazar nada:
// separa en ; & | saltos, paréntesis, llaves, acentos graves y $(.
func lenientSplit(cmd string) [][]lword {
	var segs [][]lword
	var cur []lword
	var b strings.Builder
	in, quoted, subst := false, false, false
	flush := func() {
		if in {
			cur = append(cur, lword{b.String(), quoted, subst})
		}
		b.Reset()
		in, quoted, subst = false, false, false
	}
	cut := func() {
		flush()
		if len(cur) > 0 {
			segs = append(segs, cur)
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
			cut()
			i++
		case c == '$' && i+1 < len(r) && r[i+1] == '{':
			// ${VAR} es parte de la palabra, no una llave del shell.
			j := i + 2
			for j < len(r) && r[j] != '}' {
				j++
			}
			b.WriteString(string(r[i:min(j+1, len(r))]))
			in, i = true, j
		case strings.ContainsRune(";&|\n(){}`", c):
			cut()
		default:
			b.WriteRune(c)
			in = true
		}
	}
	cut()
	return segs
}
