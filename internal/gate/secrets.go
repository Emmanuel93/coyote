package gate

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Emmanuel93/coyote/internal/secrets"
)

// Secretos del proyecto y comandos que imprimen credenciales (ADR-0016). Un
// agente no los lee ni los escribe, ni con aprobación: si lee un secreto, lo
// tiene en su contexto y lo puede escribir en un archivo, un comando o un PR.

type credCmd struct {
	re   *regexp.Regexp
	what string
}

func cred(expr, what string) credCmd { return credCmd{regexp.MustCompile(`(?i)` + expr), what} }

// sub es un programa seguido, después de sus opciones, de un subcomando.
const sub = `\b(?:\s+\S+)*?\s+`

// credCommands imprimen, crean o guardan credenciales.
var credCommands = []credCmd{
	cred(`\bgcloud`+sub+`auth\s+(?:application-default\s+)?(?:print-(?:access|identity)-token|login|activate-service-account)\b`, "credenciales de gcloud"),
	cred(`\bgcloud`+sub+`(?:config\s+config-helper|sql\s+generate-login-token)\b`, "un token de gcloud"),
	cred(`\bgcloud`+sub+`secrets\s+versions\s+access\b`, "un secreto de Secret Manager"),
	cred(`\bgcloud`+sub+`iam\s+service-accounts\s+keys\s+create\b`, "una llave de cuenta de servicio"),
	cred(`\bgcloud`+sub+`container\s+clusters\s+get-credentials\b`, "credenciales de GKE"),
	cred(`\baws`+sub+`(?:sts\s+(?:get-session-token|assume-role\S*|get-federation-token)|configure\s+(?:get|export-credentials)|secretsmanager\s+get-secret-value|ecr\s+get-login-password|eks\s+get-token|iam\s+create-access-key|kms\s+decrypt)\b`, "credenciales de AWS"),
	cred(`\baws`+sub+`ssm\s+get-parameters?(?:-by-path)?\b.*--with-decryption`, "un parámetro cifrado de AWS"),
	cred(`\baz`+sub+`(?:account\s+get-access-token|keyvault\s+secret\s+(?:show|download)|ad\s+sp\s+create-for-rbac|aks\s+get-credentials|storage\s+account\s+keys\s+list)\b`, "credenciales de Azure"),
	cred(`\baz`+sub+`acr\s+login\b.*--expose-token`, "un token de Azure"),
	cred(`\b(?:kubectl|oc)`+sub+`(?:get|describe)\s+(?:\S+\s+)*?[a-z,]*\bsecrets?\b`, "un secreto de Kubernetes"),
	cred(`\b(?:kubectl|oc)`+sub+`(?:create\s+token\b|config\s+view\b.*--(?:raw|flatten)\b)`, "credenciales de Kubernetes"),
	cred(`\b(?:terraform|tofu)`+sub+`(?:output|console|show)\b`, "valores del estado o del plan de Terraform"),
	cred(`\b(?:terraform|tofu)`+sub+`state\s+(?:pull|show)\b`, "el estado de Terraform"),
	cred(`\bhelm`+sub+`get\s+(?:values|all|manifest)\b`, "los valores de un release de Helm"),
	cred(`\bvault`+sub+`(?:read|kv\s+get|token\s+(?:create|lookup)|print\s+token|login)\b`, "un secreto de Vault"),
	cred(`\bdocker`+sub+`(?:login|inspect)\b`, "credenciales o variables de un contenedor"),
	cred(`\bdocker(?:-compose|\s+compose)`+sub+`config\b`, "la configuración de compose con sus variables"),
	cred(`\bgh`+sub+`auth\s+status\b.*(?:--show-token|\s-t\b)`, "el token de gh"),
	cred(`\b(?:op\s+(?:read|inject|item\s+get)|bw\s+(?:get|unlock|export)|lpass\s+show|pass\s+show|doppler\s+secrets|infisical\s+secrets)\b`, "un gestor de secretos"),
	cred(`\bkubeseal\b.*--recovery-unseal`, "un secreto sellado"),
	cred(`\b(?:sops|age|gpg)`+sub+`(?:-d|--decrypt)\b`, "un archivo cifrado"),
	cred(`\b(?:firebase\s+login:ci|heroku\s+auth:token|npm\s+token\s+(?:create|list))\b`, "un token"),
	cred(`/proc/(?:self|\d+|\*)/environ`, "las variables de entorno de un proceso"),
}

// varRef reconoce una referencia a una variable ($NOMBRE o ${NOMBRE}).
var varRef = regexp.MustCompile(`\$\{?([A-Za-z_][A-Za-z0-9_]*)`)

// secretParts son las partes de un nombre de variable que delatan un secreto.
var secretParts = map[string]bool{"TOKEN": true, "SECRET": true, "SECRETS": true, "PASSWORD": true, "PASSWD": true,
	"PASS": true, "KEY": true, "APIKEY": true, "CREDENTIAL": true, "CREDENTIALS": true, "AUTH": true, "PAT": true}

// camelRe separa las palabras de un nombre en camelCase.
var camelRe = regexp.MustCompile(`([a-z0-9])([A-Z])`)

// secretName dice si un nombre de variable parece de un secreto:
// GITHUB_TOKEN, DB_PASSWORD o apiKey sí; AUTHOR o PWD no.
func secretName(name string) bool {
	name = camelRe.ReplaceAllString(name, "${1}_${2}")
	for _, part := range strings.FieldsFunc(strings.ToUpper(name), func(r rune) bool { return r == '_' || r == '-' }) {
		if secretParts[part] {
			return true
		}
	}
	return false
}

// secretVar reconoce, en un texto, una variable con nombre de secreto.
func secretVar(text string) bool {
	for _, m := range varRef.FindAllStringSubmatch(text, -1) {
		if secretName(m[1]) {
			return true
		}
	}
	return false
}

// credCommand dice si un comando imprime o crea credenciales. Se revisa como
// lo correría el shell: las comillas no esconden un subcomando (gcloud auth
// "print-access-token"). Los mensajes, las notas y los datos (lo que imprime
// echo, el patrón de grep) no son comandos.
func credCommand(cmd string) (string, bool) {
	for _, text := range cmdTexts(cmd) {
		for _, c := range credCommands {
			if c.re.MatchString(text) {
				return c.what, true
			}
		}
	}
	for _, seg := range view(cmd, false) {
		if what, ok := envDump(seg); ok {
			return what, true
		}
	}
	return "", false
}

// cmdTexts son los textos de un comando que revisan los detectores por
// patrón: cada segmento como lo correría el shell y sin sus datos. Si el
// comando arma palabras al correr (sustituciones), también el texto entero.
func cmdTexts(cmd string) []string {
	var out []string
	for _, seg := range view(cmd, true) {
		out = append(out, segText(seg))
	}
	if strings.Contains(cmd, "$(") || strings.Contains(cmd, "`") || strings.Contains(cmd, "<(") || strings.Contains(cmd, ">(") {
		out = append(out, neutralize(cmd))
	}
	return out
}

// neutralize quita del comando los textos que son datos: mensajes, títulos y
// notas de coyote.
func neutralize(cmd string) string {
	cmd = strings.NewReplacer("${IFS}", " ", "$IFS", " ", "\\\r\n", "", "\\\n", "").Replace(cmd)
	cmd = messageArg.ReplaceAllString(cmd, "${1}${2}${3}_")
	return noteText.ReplaceAllString(cmd, "${1}_")
}

// envDump reconoce un segmento que imprime las variables de entorno, donde
// suelen vivir tokens: env o printenv a secas, export -p, declare -p (a secas
// o de una variable con nombre de secreto), set a secas, o echo de una
// variable con nombre de secreto.
func envDump(seg []string) (string, bool) {
	prog, at := mainProg(seg)
	if at < 0 {
		return "", false
	}
	var pos []string
	flags := 0
	for _, w := range seg[at+1:] {
		if strings.HasPrefix(w, "-") {
			flags++
		} else {
			pos = append(pos, w)
		}
	}
	switch prog {
	case "env":
		// env con un comando lo corre con otras variables (mainProg lo salta);
		// sin comando, las imprime.
		return "las variables de entorno", true
	case "printenv":
		if len(pos) == 0 {
			return "las variables de entorno", true
		}
		for _, p := range pos {
			if secretName(p) {
				return "una variable con nombre de secreto", true
			}
		}
	case "export", "declare", "typeset", "readonly":
		if len(pos) == 0 {
			return "las variables de entorno", true
		}
		if flags > 0 {
			// declare -p GITHUB_TOKEN imprime el valor.
			for _, p := range pos {
				if !assignRe.MatchString(p) && secretName(p) {
					return "una variable con nombre de secreto", true
				}
			}
		}
	case "set":
		if len(pos) == 0 && flags == 0 {
			return "las variables del shell", true
		}
	case "echo", "printf", "print":
		for _, w := range seg[at+1:] {
			if secretVar(w) {
				return "una variable con nombre de secreto", true
			}
		}
	}
	return "", false
}

// pathLike son los tramos de un texto que pueden ser una ruta o un comodín.
var pathLike = regexp.MustCompile(`[A-Za-z0-9_./~@%+*?\[\]-]+`)

// grepPrograms reciben un patrón como primer argumento: ese texto es dato.
var grepPrograms = map[string]bool{"grep": true, "egrep": true, "fgrep": true, "rg": true, "ag": true, "ack": true}

// nameOnly son programas que solo ven nombres o metadatos, no contenidos:
// nombrar un archivo de secretos con ellos no lo lee.
var nameOnly = map[string]bool{"ls": true, "find": true, "tree": true, "du": true, "stat": true, "file": true, "wc": true,
	"test": true, "[": true, "realpath": true, "readlink": true, "basename": true, "dirname": true}

// findRuns reconoce un find que corre programas sobre lo que encuentra.
func findRuns(seg []lword) bool {
	for _, w := range seg {
		switch w.text {
		case "-exec", "-execdir", "-ok", "-okdir", "-delete", "-fprint", "-fprintf", "-fls":
			return true
		}
	}
	return false
}

// secretWord busca en un comando una palabra que nombre un archivo de
// secretos (cat .env, cp .env x, --env-file=.env, grep -f.env) o un patrón de
// búsqueda que los alcance (rg -g '*.env').
func (ps Paths) secretWord(cmd, cwd string) (string, bool) {
	for _, seg := range lenientSplit(neutralize(cmd)) {
		words := texts(seg)
		prog, at := mainProg(words)
		if nameOnly[prog] && !(prog == "find" && findRuns(seg)) {
			continue
		}
		skip := map[int]bool{}
		var globs []string
		search := func(kind string, from int) {
			sp := parseSearch(kind, words[from:])
			for i := range sp.pattern {
				skip[from+i] = true // el patrón de la búsqueda es dato
			}
			for i := range sp.globWords {
				skip[from+i] = true // los patrones de nombres se revisan abajo
			}
			globs = sp.globs
		}
		switch {
		case at < 0:
		case grepPrograms[prog]:
			search(prog, at+1)
		case prog == "git":
			if sub, off := gitSub(words[at+1:]); sub == "grep" {
				search("git-grep", at+2+off)
			}
		}
		// Un patrón que incluye nombres (rg -g, grep --include) manda sobre
		// .gitignore: con él, la búsqueda lee los archivos que alcanza.
		for _, g := range globs {
			if s, ok := secrets.GlobMayMatch(g); ok {
				return "un patrón de búsqueda que alcanza archivos de secretos (" + s + ")", true
			}
			if what, ok := ps.secretToken(g, cwd); ok {
				return what, true
			}
		}
		for i, w := range seg {
			if skip[i] {
				continue
			}
			// Una ruta puede venir dentro de un script (python -c 'open(".env")'):
			// se revisa cada tramo con forma de ruta.
			for _, f := range pathLike.FindAllString(w.text, -1) {
				if what, ok := ps.secretToken(f, cwd); ok {
					return what, true
				}
			}
		}
	}
	return "", false
}

// secretToken revisa una palabra: una ruta, una opción con ruta
// (--env-file=.env) o una opción corta con el valor pegado (-f.env).
func (ps Paths) secretToken(tok, cwd string) (string, bool) {
	cands := []string{tok}
	if i := strings.IndexByte(tok, '='); i >= 0 {
		cands = append(cands, tok[i+1:])
	}
	if len(tok) > 2 && tok[0] == '-' && tok[1] != '-' {
		// -f.env, -xf.env: el valor empieza después de una o más letras.
		for k := 2; k < len(tok) && k <= 6; k++ {
			cands = append(cands, tok[k:])
		}
	}
	for _, c := range cands {
		c = strings.Trim(c, `"'(),;`)
		if c == "" || strings.HasPrefix(c, "-") || strings.Contains(c, "://") {
			continue
		}
		if strings.ContainsAny(c, "*?[") {
			if what, ok := ps.secretGlobWord(c, cwd); ok {
				return what, true
			}
			continue
		}
		if kind, rel, ok := ps.secretFile(resolve(c, cwd, ps.Home)); ok {
			return kind + " (" + rel + ")", true
		}
	}
	return "", false
}

// secretGlobWord revisa un comodín de un comando: con ** se juzga por el
// patrón; si no, por los archivos que el shell le daría ahora.
func (ps Paths) secretGlobWord(pattern, cwd string) (string, bool) {
	if strings.Contains(pattern, "**") {
		if s, ok := secrets.GlobMayMatch(pattern); ok {
			return "un patrón que alcanza archivos de secretos (" + s + ")", true
		}
		return "", false
	}
	matches, _ := filepath.Glob(resolve(pattern, cwd, ps.Home))
	for i, m := range matches {
		if i == 1000 {
			break
		}
		if kind, rel, ok := ps.secretFile(m); ok {
			return "un comodín que alcanza " + kind + " (" + rel + ")", true
		}
	}
	return "", false
}

// secretFile dice si abs es un archivo de secretos: por su nombre, por las
// reglas del proyecto (secrets.files) y salvo sus dispensas.
func (ps Paths) secretFile(abs string) (kind, rel string, ok bool) {
	rel = ps.Rel(abs)
	if filepath.IsAbs(filepath.FromSlash(rel)) {
		// Fuera del proyecto cuentan el nombre y, si hace falta, el contenido.
		kind, ok = secrets.Rules{}.KindAt("", rel)
		return kind, rel, ok
	}
	kind, ok = ps.Secrets.KindAt(ps.Root, rel)
	return kind, rel, ok
}

// contentTools devuelven el contenido de lo que buscan, no solo nombres.
var contentTools = map[string]bool{"grep": true, "grep_search": true, "search_file_content": true, "rg": true,
	"codebase_search": true, "semanticsearch": true, "semantic_search": true, "read_many_files": true}

// globFields son los campos con patrones de nombres de archivo.
var globFields = []string{"glob", "include", "include_pattern", "paths", "files", "path", "file_pattern"}

// secretGlob revisa los patrones de una herramienta de búsqueda de contenido:
// uno que alcanza archivos de secretos (*.pem, **/.env*) los leería.
func secretGlob(a Action) (string, bool) {
	if !contentTools[strings.ToLower(a.Tool)] {
		return "", false
	}
	for _, k := range globFields {
		var texts []string
		allStrings(a.Input[k], &texts)
		for _, t := range texts {
			if s, ok := secrets.GlobMayMatch(t); ok {
				return s, true
			}
		}
	}
	return "", false
}

// secretsWalkLimit acota lo que se recorre para saber si una carpeta tiene
// archivos de secretos: pasado el tope, se asume que sí.
const secretsWalkLimit = 20000

// skipDirs no se recorren: dependencias y salidas de compilación.
var skipDirs = map[string]bool{".git": true, "node_modules": true, "vendor": true, "build": true, "dist": true,
	"target": true, ".gradle": true, ".dart_tool": true, "Pods": true, ".venv": true, "venv": true,
	"__pycache__": true, ".idea": true, ".next": true, ".coyote": true}

// dirSecret busca un archivo de secretos dentro de dir. Lo usa una búsqueda
// recursiva que lee contenidos sin respetar .gitignore (grep -r).
func (ps Paths) dirSecret(dir string) (string, bool) {
	info, err := os.Stat(dir)
	if err != nil {
		return "", false
	}
	if !info.IsDir() {
		if kind, rel, ok := ps.secretFile(dir); ok {
			return kind + " (" + rel + ")", true
		}
		return "", false
	}
	n := 0
	found := ""
	stop := filepath.SkipAll
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		n++
		if n > secretsWalkLimit {
			found = "demasiados archivos para revisar"
			return stop
		}
		if d.IsDir() {
			if p != dir && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if kind, rel, ok := ps.secretFile(p); ok {
			found = kind + " (" + rel + ")"
			return stop
		}
		return nil
	})
	return found, found != ""
}
