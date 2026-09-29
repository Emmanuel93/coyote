package gate

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// word es una palabra de shell ya sin comillas. glob indica que trae comodines
// fuera de comillas, que el shell expandiría.
type word struct {
	s    string
	glob bool
}

// splitShell parte un comando en segmentos (separados por ;, &&, || o |) de
// palabras. Es deliberadamente estrecho: rechaza toda construcción que pueda
// ejecutar otra cosa o escribir (sustituciones, variables, redirecciones a
// archivos, subshells, segundo plano, heredocs). Solo acepta redirigir a
// /dev/null y duplicar descriptores (2>&1).
func splitShell(cmd string) ([][]word, error) {
	var segs [][]word
	var cur []word
	var b strings.Builder
	inWord, glob := false, false
	flushWord := func() {
		if inWord {
			cur = append(cur, word{b.String(), glob})
		}
		b.Reset()
		inWord, glob = false, false
	}
	flushSeg := func(strict bool) error {
		flushWord()
		if len(cur) == 0 {
			if strict {
				return errors.New("operador sin comando")
			}
			return nil
		}
		segs = append(segs, cur)
		cur = nil
		return nil
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
			if j >= len(r) {
				return nil, errors.New("comilla sin cerrar")
			}
			b.WriteString(string(r[i+1 : j]))
			inWord, i = true, j
		case c == '"':
			j := i + 1
			for ; j < len(r) && r[j] != '"'; j++ {
				switch r[j] {
				case '$', '`':
					return nil, errors.New("expansión dentro de comillas dobles")
				case '\\':
					if j+1 < len(r) && strings.ContainsRune("\"\\$`", r[j+1]) {
						j++
					}
				}
				if r[j] < 0x20 && r[j] != '\t' && r[j] != '\n' {
					return nil, errors.New("caracter de control")
				}
				b.WriteRune(r[j])
			}
			if j >= len(r) {
				return nil, errors.New("comilla sin cerrar")
			}
			inWord, i = true, j
		case c == '\\':
			if i+1 >= len(r) {
				return nil, errors.New("barra invertida al final")
			}
			i++
			if r[i] == '\n' {
				continue
			}
			b.WriteRune(r[i])
			inWord = true
		case c == ' ' || c == '\t':
			flushWord()
		case c == '\n' || c == ';':
			if err := flushSeg(false); err != nil {
				return nil, err
			}
		case c == '|':
			if i+1 < len(r) && r[i+1] == '&' {
				return nil, errors.New("|& redirige la salida de error")
			}
			if i+1 < len(r) && r[i+1] == '|' {
				i++
			}
			if err := flushSeg(true); err != nil {
				return nil, err
			}
		case c == '&':
			if i+1 < len(r) && r[i+1] == '&' {
				i++
				if err := flushSeg(true); err != nil {
					return nil, err
				}
				continue
			}
			return nil, errors.New("& (segundo plano o redirección)")
		case c == '>':
			if inWord {
				if s := b.String(); s == "1" || s == "2" {
					b.Reset() // descriptor de la redirección, no un argumento
					inWord, glob = false, false
				} else {
					flushWord()
				}
			}
			if i+1 < len(r) && (r[i+1] == '>' || r[i+1] == '|') {
				return nil, errors.New("redirección que escribe")
			}
			if i+1 < len(r) && r[i+1] == '&' {
				j := i + 2
				for j < len(r) && r[j] >= '0' && r[j] <= '9' {
					j++
				}
				if j == i+2 || (j < len(r) && !strings.ContainsRune(" \t\n;|&", r[j])) {
					return nil, errors.New("redirección no permitida")
				}
				i = j - 1
				continue
			}
			j := i + 1
			for j < len(r) && (r[j] == ' ' || r[j] == '\t') {
				j++
			}
			k := j
			for k < len(r) && !strings.ContainsRune(" \t\n;|&<>", r[k]) {
				k++
			}
			if string(r[j:k]) != "/dev/null" {
				return nil, errors.New("redirección a un archivo")
			}
			i = k - 1
		case strings.ContainsRune("<`$(){}!", c):
			return nil, fmt.Errorf("construcción de shell no permitida sin aprobación (%c)", c)
		case c == '#' && !inWord:
			return nil, errors.New("comentario de shell")
		case c == '*' || c == '?' || c == '[':
			glob = true
			b.WriteRune(c)
			inWord = true
		case c < 0x20 || c == 0x7f:
			return nil, errors.New("caracter de control")
		default:
			b.WriteRune(c)
			inWord = true
		}
	}
	if err := flushSeg(false); err != nil {
		return nil, err
	}
	return segs, nil
}

// segment es un comando simple ya validado.
type segment struct {
	prog string
	args []word
}

// analyzeShell decide si cmd solo lee. Devuelve los segmentos para revisar
// sus rutas y, si no es de solo lectura, el motivo.
func analyzeShell(cmd string) ([]segment, error) {
	parts, err := splitShell(cmd)
	if err != nil {
		return nil, err
	}
	if len(parts) == 0 {
		return nil, errors.New("comando vacío")
	}
	var out []segment
	for _, words := range parts {
		first := words[0]
		if first.glob || strings.Contains(first.s, "/") {
			return nil, fmt.Errorf("%q: solo se aceptan programas por nombre", first.s)
		}
		if i := strings.IndexByte(first.s, '='); i > 0 {
			return nil, errors.New("asignar variables de entorno cambia lo que hace un comando")
		}
		chk, ok := readOnlyPrograms[first.s]
		if !ok {
			return nil, fmt.Errorf("%s no está en la lista de comandos de solo lectura", first.s)
		}
		args := words[1:]
		if !globSafe[first.s] {
			for _, a := range args {
				if a.glob {
					return nil, fmt.Errorf("comodines sin comillas con %s", first.s)
				}
			}
		}
		if chk != nil {
			if err := chk(args); err != nil {
				return nil, fmt.Errorf("%s: %v", first.s, err)
			}
		}
		out = append(out, segment{first.s, args})
	}
	return out, nil
}

type argCheck func(args []word) error

// globSafe son programas cuyas banderas no ejecutan ni escriben, así que un
// comodín que se expande a un nombre raro no cambia lo que hacen.
var globSafe = map[string]bool{
	"ls": true, "cat": true, "head": true, "tail": true, "wc": true, "stat": true, "du": true,
	"grep": true, "egrep": true, "fgrep": true, "diff": true, "cmp": true, "nl": true,
	"sha256sum": true, "sha1sum": true, "md5sum": true, "shasum": true, "cksum": true,
}

// readOnlyPrograms es la lista cerrada de comandos que se ejecutan sin
// aprobación. nil significa que cualquier argumento es de lectura.
var readOnlyPrograms = map[string]argCheck{
	"ls": nil, "pwd": nil, "cat": nil, "head": nil, "tail": nil, "wc": nil, "stat": nil,
	"du": nil, "df": nil, "which": nil, "whoami": nil, "id": nil, "uname": nil,
	"basename": nil, "dirname": nil, "realpath": nil, "readlink": nil, "echo": nil,
	"printf": nil, "true": nil, "false": nil, "nl": nil, "cut": nil, "tr": nil,
	"column": nil, "comm": nil, "cmp": nil, "diff": nil, "grep": nil, "egrep": nil,
	"fgrep": nil, "jq": jqCheck, "strings": nil, "od": nil, "sha256sum": nil, "sha1sum": nil,
	"md5sum": nil, "shasum": nil, "cksum": nil, "cd": nil, "sleep": nil,
	"hostname": maxPositional(0),
	"file":     denyFlags("-C", "--compile"),
	"tree":     denyFlags("-o", "--output", "-R"),
	"sort":     denyFlags("-o", "--output", "--compress-program", "-T", "--temporary-directory"),
	"uniq":     maxPositional(1),
	"date":     denyFlags("-s", "--set"),
	"rg":       denyFlags("--pre", "--pre-glob", "-z", "--search-zip", "--hostname-bin"),
	"xxd":      both(denyFlags("-r", "-revert"), maxPositional(1)),
	"find":     denyFlags("-exec", "-execdir", "-ok", "-okdir", "-delete", "-fprint", "-fprint0", "-fprintf", "-fls"),
	"git":      gitCheck,
	"go":       goCheck,
	"coyote":   coyoteCheck,
}

// jqCheck impide que jq imprima variables de entorno, donde suele haber tokens.
func jqCheck(args []word) error {
	for _, a := range args {
		if jqEnv.MatchString(a.s) {
			return fmt.Errorf("lee variables de entorno")
		}
	}
	return nil
}

var jqEnv = regexp.MustCompile(`\$ENV|\benv\b|\$__prog_args|input_filename`)

func both(a, b argCheck) argCheck {
	return func(args []word) error {
		if err := a(args); err != nil {
			return err
		}
		return b(args)
	}
}

// hasFlag reconoce una bandera exacta, en forma --larga=valor o, para banderas
// cortas de una letra, dentro de un grupo (-ro incluye -o).
func hasFlag(args []word, flags ...string) (string, bool) {
	for _, a := range args {
		s := a.s
		if s == "--" {
			return "", false
		}
		for _, f := range flags {
			switch {
			case s == f:
				return f, true
			case strings.HasPrefix(f, "--") && strings.HasPrefix(s, f+"="):
				return f, true
			case len(f) == 2 && f[0] == '-' && f[1] != '-' && len(s) > 2 && s[0] == '-' && s[1] != '-':
				if strings.ContainsRune(s[1:], rune(f[1])) {
					return f, true
				}
			case len(f) > 2 && f[0] == '-' && f[1] != '-' && strings.HasPrefix(s, f):
				// banderas de una raya y varias letras (find -exec, xxd -revert)
				return f, true
			}
		}
	}
	return "", false
}

// hasExact reconoce una bandera booleana escrita tal cual: --check=false no
// cuenta como --check.
// flagOn dice si una bandera booleana de Go queda prendida: la última
// aparición manda (--check --check=false la apaga). Con "--" en los
// argumentos no se decide: coyote vuelve a leer banderas después de un
// argumento suelto.
func flagOn(args []word, flags ...string) bool {
	on := false
	for _, a := range args {
		if a.s == "--" {
			return false
		}
		for _, f := range flags {
			switch {
			case a.s == f:
				on = true
			case strings.HasPrefix(a.s, f+"="):
				v, err := strconv.ParseBool(strings.TrimPrefix(a.s, f+"="))
				on = err == nil && v
			}
		}
	}
	return on
}

func hasExact(args []word, flags ...string) bool {
	for _, a := range args {
		if a.s == "--" {
			return false
		}
		for _, f := range flags {
			if a.s == f || a.s == f+"=true" {
				return true
			}
		}
	}
	return false
}

func denyFlags(flags ...string) argCheck {
	return func(args []word) error {
		if f, ok := hasFlag(args, flags...); ok {
			return fmt.Errorf("%s escribe o ejecuta", f)
		}
		return nil
	}
}

func positional(args []word) []word {
	var out []word
	rest := false
	for _, a := range args {
		if rest || !strings.HasPrefix(a.s, "-") || a.s == "-" {
			out = append(out, a)
			continue
		}
		if a.s == "--" {
			rest = true
		}
	}
	return out
}

func maxPositional(n int) argCheck {
	return func(args []word) error {
		if len(positional(args)) > n {
			return fmt.Errorf("con más de %d argumentos escribe", n)
		}
		return nil
	}
}

// gitCheck admite subcomandos de git que solo leen, sin opciones globales que
// cambien la configuración ni banderas que escriban o ejecuten programas.
func gitCheck(args []word) error {
	i := 0
	for i < len(args) && strings.HasPrefix(args[i].s, "-") {
		switch args[i].s {
		case "-C":
			i += 2
		case "--no-pager", "-P", "--no-optional-locks", "--literal-pathspecs", "--no-replace-objects":
			i++
		default:
			return fmt.Errorf("la opción global %s no se permite sin aprobación", args[i].s)
		}
	}
	if i >= len(args) {
		return nil
	}
	sub, rest := args[i].s, args[i+1:]
	readFlags := denyFlags("--output", "--ext-diff", "--textconv", "-O", "--open-files-in-pager", "--filters")
	switch sub {
	case "status", "log", "show", "diff", "whatchanged", "shortlog", "blame", "annotate", "grep",
		"rev-parse", "rev-list", "ls-files", "ls-tree", "cat-file", "describe", "name-rev", "merge-base",
		"for-each-ref", "show-ref", "count-objects", "var", "check-ignore", "check-attr", "version":
		return readFlags(rest)
	case "branch":
		return listOnly(rest, map[string]bool{"-a": true, "--all": true, "-r": true, "--remotes": true, "-v": true,
			"-vv": true, "--verbose": true, "--show-current": true, "--contains": true, "--no-contains": true,
			"--merged": true, "--no-merged": true, "--points-at": true, "--color": true, "--no-color": true,
			"--column": true, "--no-column": true, "-i": true, "--ignore-case": true, "--abbrev": true, "--no-abbrev": true})
	case "tag":
		return listOnly(rest, map[string]bool{"-n": true, "--contains": true, "--no-contains": true, "--merged": true,
			"--no-merged": true, "--points-at": true, "--column": true, "--no-column": true, "-i": true, "--ignore-case": true})
	case "remote":
		// Solo los nombres: las URLs pueden llevar tokens (https://token@host).
		if len(rest) == 0 {
			return nil
		}
	case "config":
		// Solo --get de claves sin secretos: --list, --get-regexp o --get-urlmatch
		// vuelcan la configuración, donde puede haber tokens (extraheader, insteadOf).
		if len(rest) == 2 && (rest[0].s == "--get" || rest[0].s == "--get-all") && safeGitKey.MatchString(rest[1].s) {
			return nil
		}
	case "stash":
		if len(rest) > 0 && (rest[0].s == "list" || rest[0].s == "show") {
			return readFlags(rest)
		}
	case "reflog":
		if len(rest) == 0 || rest[0].s == "show" || strings.HasPrefix(rest[0].s, "-") {
			return readFlags(rest)
		}
	case "worktree":
		if len(rest) > 0 && rest[0].s == "list" {
			return nil
		}
	case "submodule":
		if len(rest) > 0 && rest[0].s == "status" {
			return nil
		}
	}
	return fmt.Errorf("git %s necesita aprobación", sub)
}

// safeGitKey son claves de configuración de git que no guardan secretos.
var safeGitKey = regexp.MustCompile(`(?i)^(user\.(name|email)|init\.defaultbranch|core\.(autocrlf|filemode|ignorecase|bare|editor)|pull\.rebase|push\.default|commit\.gpgsign|coyote\.user)$`)

// listOnly acepta git branch o git tag solo para listar: sin nombres nuevos y
// con las banderas de listado; con -l o --list los argumentos son patrones.
func listOnly(rest []word, ok map[string]bool) error {
	list := false
	for _, a := range rest {
		if a.s == "-l" || a.s == "--list" {
			list = true
		}
	}
	for _, a := range rest {
		s := a.s
		switch {
		case s == "-l" || s == "--list":
		case strings.HasPrefix(s, "--sort=") || strings.HasPrefix(s, "--format=") || strings.HasPrefix(s, "--color=") ||
			strings.HasPrefix(s, "--column=") || strings.HasPrefix(s, "--abbrev=") || strings.HasPrefix(s, "--contains=") ||
			strings.HasPrefix(s, "--merged=") || strings.HasPrefix(s, "--no-merged=") || strings.HasPrefix(s, "--points-at="):
		case strings.HasPrefix(s, "-"):
			if !ok[s] && !(strings.HasPrefix(s, "-n") && len(s) > 2 && strings.Trim(s[2:], "0123456789") == "") {
				return fmt.Errorf("%s modifica o no es de listado", s)
			}
		default:
			if !list {
				return fmt.Errorf("%q crearía una rama o etiqueta", s)
			}
		}
	}
	return nil
}

var safeGoEnv = map[string]bool{"GOPATH": true, "GOROOT": true, "GOOS": true, "GOARCH": true, "GOVERSION": true,
	"GOMOD": true, "GOCACHE": true, "GOMODCACHE": true, "GOWORK": true, "GOBIN": true, "CGO_ENABLED": true, "GOEXE": true}

func goCheck(args []word) error {
	if len(args) == 0 {
		return nil
	}
	switch args[0].s {
	case "version":
		return nil
	case "env":
		// Solo variables nombradas y sin secretos: go env a secas incluye GOPROXY
		// y GOAUTH, que pueden llevar credenciales.
		if len(args) == 1 {
			return fmt.Errorf("go env sin variables muestra todo el entorno de Go")
		}
		for _, a := range args[1:] {
			if !safeGoEnv[a.s] {
				return fmt.Errorf("go env %s necesita aprobación", a.s)
			}
		}
		return nil
	}
	// vet, build y test compilan (cgo corre el compilador de C) o ejecutan código.
	return fmt.Errorf("go %s compila o ejecuta código", args[0].s)
}

// coyoteCheck admite los subcomandos de coyote que solo leen, más propose,
// que solo encola una propuesta para la persona.
func coyoteCheck(args []word) error {
	i := 0
	for i < len(args) && args[i].s == "-C" {
		i += 2
	}
	if i >= len(args) {
		return nil
	}
	sub, rest := args[i].s, args[i+1:]
	first := ""
	if len(rest) > 0 {
		first = rest[0].s
	}
	switch sub {
	case "status", "log", "get", "version", "help", "approvals", "review", "index", "propose", "-h", "--help", "-v", "--version":
		return nil
	case "doctor":
		// doctor lee; pedir un canario o correrlo escriben en .coyote/gate/.
		if first == "canary" || hasExact(rest, "--canary", "-canary") {
			return fmt.Errorf("coyote doctor con el canario escribe el estado del gate")
		}
		return nil
	case "ask":
		return denyFlags("--record", "-record")(rest)
	case "standards":
		switch first {
		case "lint":
			return denyFlags("--scripts", "-scripts")(rest)
		case "show", "explain", "diff", "":
			return nil
		}
	case "attribution":
		if first == "check" {
			return nil
		}
	case "generate":
		if flagOn(rest, "--check", "-check") {
			return nil
		}
	case "install":
		if flagOn(rest, "--check", "-check") || flagOn(rest, "--dry-run", "-dry-run") {
			return nil
		}
	case "repo":
		if first == "list" {
			return nil
		}
	case "ws":
		if first == "status" || first == "check" {
			return nil
		}
	case "secrets":
		// Solo nombres y ubicaciones, nunca valores (ADR-0016).
		if first == "list" || first == "scan" {
			return nil
		}
	case "hub":
		if first == "status" {
			return nil
		}
	case "slo":
		if first == "check" || first == "rules" && (flagOn(rest, "--check", "-check") || flagOn(rest, "--stdout", "-stdout")) {
			return nil
		}
	}
	return fmt.Errorf("coyote %s tiene efectos", strings.TrimSpace(sub+" "+first))
}

// cleanJoin resuelve p contra dir como lo haría el shell, sin tocar el disco.
func cleanJoin(dir, p string) string {
	if path.IsAbs(p) {
		return path.Clean(p)
	}
	return path.Join(dir, p)
}
