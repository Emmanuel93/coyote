// Package secrets reconoce secretos (ADR-0016): los archivos que los guardan,
// por su nombre, y los secretos escritos en un texto, por patrones de alta
// confianza. Nunca devuelve un valor: un hallazgo dice archivo, línea y tipo.
package secrets

import (
	"encoding/json"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Emmanuel93/coyote/internal/fsx"
	"github.com/Emmanuel93/coyote/internal/glob"
)

// AllowMarker, en una línea, la dispensa del escáner; la persona lo ve al revisar.
const AllowMarker = "coyote:allow-secret"

// Allow dispensa rutas del proyecto, con motivo.
type Allow struct {
	Path   string `yaml:"path" json:"path"`
	Reason string `yaml:"reason" json:"reason"`
}

// Rules son las reglas de un proyecto: sus archivos de secretos, además de
// los de siempre, y sus dispensas (coyote/project.yaml, que ningún agente edita).
type Rules struct {
	Files []string `yaml:"files,omitempty" json:"files,omitempty"`
	Allow []Allow  `yaml:"allow,omitempty" json:"allow,omitempty"`
}

// exact son archivos de secretos por su nombre completo, en minúsculas.
var exact = map[string]string{
	".env":                "variables de entorno",
	".envrc":              "variables de entorno (direnv)",
	"key.properties":      "firma de Android",
	"keystore.properties": "firma de Android",
	"kubeconfig":          "kubeconfig",
	".npmrc":              "credenciales de npm",
	".pypirc":             "credenciales de PyPI",
	".netrc":              "credenciales de red",
	".git-credentials":    "credenciales de git",
	".dockercfg":          "credenciales de Docker",
	".htpasswd":           "contraseñas",
	".vault-token":        "token de Vault",
	"credentials.json":    "credenciales",
	"id_rsa":              "llave privada",
	"id_dsa":              "llave privada",
	"id_ecdsa":            "llave privada",
	"id_ed25519":          "llave privada",
}

// pemKind es el tipo de un .pem: puede ser una llave privada o solo un
// certificado o una llave pública, así que se confirma por su contenido.
const pemKind = "llave o certificado"

// suffixes son archivos de secretos por su terminación.
var suffixes = []struct{ suffix, kind string }{
	{".tfstate", "estado de Terraform"},
	{".tfstate.backup", "estado de Terraform"},
	{".pem", pemKind},
	{".key", "llave privada"},
	{".p12", "almacén de llaves (PKCS#12)"},
	{".pfx", "almacén de llaves (PKCS#12)"},
	{".jks", "almacén de llaves"},
	{".keystore", "almacén de llaves"},
	{".p8", "llave privada"},
	{".ppk", "llave privada"},
	{".ovpn", "configuración de VPN con llaves"},
	{".kubeconfig", "kubeconfig"},
	{".env", "variables de entorno"}, // prod.env, docker.env
	{"-key.json", "llave de cuenta de servicio"},
	{"_key.json", "llave de cuenta de servicio"},
}

// examples son terminaciones de plantillas: no guardan secretos.
var examples = []string{".example", ".sample", ".template", ".dist", ".tmpl", ".tpl", ".defaults"}

var (
	envRe      = regexp.MustCompile(`^\.env\.[a-z0-9_.-]+$`)
	accountRe  = regexp.MustCompile(`^(service-?account|client_secret|client-secret)[a-z0-9_.-]*\.json$`)
	secretsRe  = regexp.MustCompile(`^secrets?\.(ya?ml|json|toml|env|ini|properties)$`)
	tfstateRe  = regexp.MustCompile(`\.tfstate\.[a-z0-9_.-]+$`)
	dockerConf = regexp.MustCompile(`(^|/)\.docker/config\.json$`)
)

// FileKind dice si una ruta es un archivo de secretos por su nombre y de qué
// tipo. Las plantillas (.env.example) no lo son.
func FileKind(p string) (string, bool) {
	p = strings.ReplaceAll(p, "\\", "/")
	base := strings.ToLower(path.Base(p))
	if base == "" || base == "." || base == "/" {
		return "", false
	}
	for _, e := range examples {
		if strings.HasSuffix(base, e) || strings.Contains(base, e+".") {
			return "", false
		}
	}
	if k, ok := exact[base]; ok {
		return k, true
	}
	for _, s := range suffixes {
		if strings.HasSuffix(base, s.suffix) {
			return s.kind, true
		}
	}
	switch {
	case envRe.MatchString(base):
		return "variables de entorno", true
	case accountRe.MatchString(base):
		return "cuenta de servicio u OAuth", true
	case secretsRe.MatchString(base):
		return "secretos", true
	case tfstateRe.MatchString(base):
		return "estado de Terraform", true
	case dockerConf.MatchString(strings.ToLower(p)):
		return "credenciales de Docker", true
	}
	return "", false
}

// Kind aplica las reglas del proyecto: sus archivos propios se suman y las
// dispensas los quitan. rel es relativa a la raíz, con barras.
func (r Rules) Kind(rel string) (string, bool) {
	rel = strings.TrimPrefix(strings.ReplaceAll(rel, "\\", "/"), "./")
	if r.Allowed(rel) {
		return "", false
	}
	if k, ok := FileKind(rel); ok {
		return k, true
	}
	if glob.Any(r.Files, rel) {
		return "secretos del proyecto", true
	}
	return "", false
}

// Allowed informa si una ruta está dispensada.
func (r Rules) Allowed(rel string) bool {
	for _, a := range r.Allow {
		if glob.Match(a.Path, rel) {
			return true
		}
	}
	return false
}

// samples son nombres típicos de archivos de secretos: un patrón de búsqueda
// que coincide con alguno puede leer secretos.
var samples = []string{".env", ".env.local", ".env.production", "prod.env", ".envrc", "terraform.tfstate", "terraform.tfstate.backup",
	"server.pem", "server.key", "cert.p12", "release.jks", "upload.keystore", "key.properties", "kubeconfig",
	"credentials.json", "service-account.json", "sa-key.json", "id_rsa", "id_ed25519", "secrets.yaml", ".npmrc", ".netrc"}

// GlobMayMatch informa si un patrón de nombres (*.pem, **/.env*) puede
// coincidir con un archivo de secretos, y con cuál.
func GlobMayMatch(pattern string) (string, bool) {
	pattern = strings.ReplaceAll(pattern, "\\", "/")
	if !glob.HasMeta(pattern) {
		return "", false
	}
	base := path.Base(pattern)
	for _, s := range samples {
		if glob.Match(base, s) || glob.Match(pattern, s) || glob.Match(pattern, "a/"+s) {
			return s, true
		}
	}
	return "", false
}

// Finding es un secreto encontrado en un texto.
type Finding struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Kind string `json:"kind"`
	Hint string `json:"hint,omitempty"` // qué se encontró, sin el valor
}

// detector reconoce un tipo de secreto. hint dice qué se encontró sin
// mostrar el valor; val es el grupo con el valor (0, todo lo que coincide),
// que no debe ser un valor de ejemplo; check filtra lo que coincide pero no
// es un secreto.
type detector struct {
	kind  string
	re    *regexp.Regexp
	hint  string
	val   int
	check func(m []string) bool
}

// pemHeader es el encabezado de una llave privada en PEM. Solo, sin cuerpo,
// no es un secreto: el código que lee llaves lo menciona.
const pemHeader = `-----BEGIN (?:RSA |EC |DSA |OPENSSH |ENCRYPTED |PGP )?PRIVATE KEY(?: BLOCK)?-----`

var (
	pemHeaderRe = regexp.MustCompile(pemHeader)
	b64RunRe    = regexp.MustCompile(`[A-Za-z0-9+/]{40,}`)
)

// pemWindow son las líneas que siguen a un encabezado PEM donde se busca el
// cuerpo: una llave cifrada lleva antes Proc-Type, DEK-Info y una línea vacía.
const pemWindow = 4

// keyBody dice si un texto trae el cuerpo de una llave: un tramo en base64
// de 40 caracteres o más con mayúsculas, minúsculas y dígitos (entre
// comillas, tras un \n escapado o concatenado), o el Proc-Type de una llave
// cifrada. Un nombre largo en camelCase o una ruta no llevan las tres cosas.
func keyBody(s string) bool {
	if strings.Contains(s, "Proc-Type: 4,ENCRYPTED") {
		return true
	}
	for _, m := range b64RunRe.FindAllString(s, -1) {
		if strings.ContainsAny(m, "0123456789") && strings.ContainsAny(m, "abcdefghijklmnopqrstuvwxyz") && strings.ContainsAny(m, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
			return true
		}
	}
	return false
}

// classes cuenta las clases de caracteres de un texto: minúsculas,
// mayúsculas, dígitos y otros.
func classes(s string) int {
	n := 0
	for _, set := range []string{"abcdefghijklmnopqrstuvwxyz", "ABCDEFGHIJKLMNOPQRSTUVWXYZ", "0123456789"} {
		if strings.ContainsAny(s, set) {
			n++
		}
	}
	if strings.IndexFunc(s, func(r rune) bool { return !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", r) }) >= 0 {
		n++
	}
	return n
}

var detectors = []detector{
	{kind: "llave de acceso de AWS", re: regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), hint: "AKIA…"},
	{kind: "llave secreta de AWS", re: regexp.MustCompile(`(?i)aws_?secret_?access_?key["']?\s*[:=]\s*["']?([A-Za-z0-9/+=]{40})(?:[^A-Za-z0-9/+=]|$)`), hint: "aws_secret_access_key = …", val: 1},
	{kind: "token de GitHub", re: regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{36,255}|github_pat_[A-Za-z0-9_]{22,255})\b`), hint: "ghp_…"},
	{kind: "token de GitLab", re: regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}`), hint: "glpat-…"},
	{kind: "token de Slack", re: regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,}`), hint: "xox…",
		check: func(m []string) bool { return strings.ContainsAny(m[0], "0123456789") }},
	{kind: "webhook de Slack", re: regexp.MustCompile(`https://hooks\.slack\.com/services/T[A-Za-z0-9]+/B[A-Za-z0-9]+/[A-Za-z0-9]+`), hint: "hooks.slack.com/services/…"},
	{kind: "llave de Stripe", re: regexp.MustCompile(`\b(?:sk|rk)_live_[A-Za-z0-9]{20,}`), hint: "sk_live_…"},
	{kind: "llave de SendGrid", re: regexp.MustCompile(`\bSG\.[A-Za-z0-9_-]{22}\.[A-Za-z0-9_-]{43}\b`), hint: "SG.…"},
	{kind: "token de npm", re: regexp.MustCompile(`\bnpm_[A-Za-z0-9]{36}\b`), hint: "npm_…"},
	{kind: "token de PyPI", re: regexp.MustCompile(`\bpypi-AgEIcHlwaS5vcmc[A-Za-z0-9_-]{50,}`), hint: "pypi-…"},
	{kind: "llave de API de un modelo", re: regexp.MustCompile(`\bsk-(?:ant-(?:api|admin)\d{2}|proj|svcacct|admin)-[A-Za-z0-9_-]{40,}`), hint: "sk-…"},
	{kind: "URL con contraseña", re: regexp.MustCompile(`\b[a-z][a-z0-9+.-]{1,20}://[^\s:/@'"<>]{1,64}:([^\s:/@'"<>]{8,128})@[A-Za-z0-9.-]+`), hint: "usuario:…@servidor", val: 1,
		check: func(m []string) bool { return randomLooking(m[1]) }},
}

// placeholderWords delatan un valor de ejemplo en la documentación o en las
// pruebas: la llave de ejemplo de AWS, xoxb-your-bot-token, ghp_xxxx….
var placeholderWords = []string{"example", "sample", "placeholder", "your", "dummy", "fake", "redacted", "changeme", "xxxx"}

// placeholder dice si un valor es de ejemplo: nombra un ejemplo o repite un
// mismo caracter seis veces seguidas.
func placeholder(s string) bool {
	lower := strings.ToLower(s)
	for _, w := range placeholderWords {
		if strings.Contains(lower, w) {
			return true
		}
	}
	run := 1
	for i := 1; i < len(s); i++ {
		if s[i] == s[i-1] {
			run++
			if run >= 6 {
				return true
			}
		} else {
			run = 1
		}
	}
	return false
}

// randomLooking descarta contraseñas de ejemplo o variables: un secreto de
// verdad es largo y mezcla clases de caracteres.
func randomLooking(s string) bool {
	if strings.ContainsAny(s, "${}%*<>") {
		return false
	}
	lower := strings.ToLower(s)
	for _, p := range []string{"password", "passwd", "changeme", "example", "secret", "contraseña", "placeholder"} {
		if strings.Contains(lower, p) {
			return false
		}
	}
	n := classes(s)
	return (len(s) >= 12 && n >= 3) || (len(s) >= 20 && n >= 2)
}

// MaxLine es el tramo de una línea que se revisa de una vez: una línea más
// larga (un archivo minificado) se revisa por tramos que se enciman.
const MaxLine = 64 << 10

// lineOverlap es lo que se enciman los tramos de una línea larga: más que el
// secreto más largo que se reconoce.
const lineOverlap = 1 << 10

// Scan busca secretos en un texto. Las líneas con coyote:allow-secret no cuentan.
func Scan(p, text string) []Finding {
	return ScanFrom(p, 1, text)
}

// ScanFrom es Scan para un tramo de texto que empieza en la línea first: las
// líneas que agrega un cambio, por ejemplo. Revisa todas las líneas, de
// cualquier largo.
func ScanFrom(p string, first int, text string) []Finding {
	var out []Finding
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for i, l := range lines {
		l = strings.TrimSuffix(l, "\r")
		n := first + i
		found := ScanLine(p, n, l)
		// Una llave en varias líneas: el encabezado y, en las siguientes, su cuerpo.
		if len(found) == 0 && !strings.Contains(l, AllowMarker) && pemHeaderRe.MatchString(l) {
			for j := i + 1; j < len(lines) && j <= i+pemWindow; j++ {
				if keyBody(lines[j]) {
					found = append(found, Finding{Path: p, Line: n, Kind: "llave privada", Hint: "BEGIN … PRIVATE KEY"})
					break
				}
			}
		}
		out = append(out, found...)
	}
	return out
}

// NeedsContent dice si un tipo de archivo de secretos se confirma por su
// contenido: un .pem puede ser un certificado o una llave pública.
func NeedsContent(kind string) bool { return kind == pemKind }

// Confirm dice si el contenido confirma un archivo de secretos: un .pem lo es
// solo si lleva el encabezado de una llave privada. Los demás tipos se
// confirman por su nombre.
func Confirm(kind string, data []byte) bool {
	if !NeedsContent(kind) {
		return true
	}
	return pemHeaderRe.Match(data)
}

// KindAt es Kind para un archivo en disco: confirma por su contenido los
// tipos que lo necesitan, salvo que el proyecto declare el archivo en
// secrets.files. Un archivo que no existe, no es regular o no se puede leer
// cuenta como secreto.
func (r Rules) KindAt(root, rel string) (string, bool) {
	kind, ok := r.Kind(rel)
	if !ok || !NeedsContent(kind) {
		return kind, ok
	}
	if glob.Any(r.Files, strings.TrimPrefix(strings.ReplaceAll(rel, "\\", "/"), "./")) {
		return kind, true
	}
	p := filepath.Join(root, filepath.FromSlash(rel))
	if filepath.IsAbs(filepath.FromSlash(rel)) {
		p = filepath.FromSlash(rel)
	}
	data, err := fsx.ReadCapped(p, 1<<20)
	if err != nil {
		return kind, true
	}
	return kind, Confirm(kind, data)
}

// ScanLine busca secretos en una línea; una línea larga se revisa por tramos.
func ScanLine(p string, n int, line string) []Finding {
	if strings.Contains(line, AllowMarker) {
		return nil
	}
	var out []Finding
	seen := map[string]bool{}
	for start := 0; ; start += MaxLine - lineOverlap {
		end := min(start+MaxLine, len(line))
		chunk := line[start:end]
		for _, d := range detectors {
			if seen[d.kind] {
				continue
			}
			for _, m := range d.re.FindAllStringSubmatch(chunk, -1) {
				if placeholder(m[d.val]) || (d.check != nil && !d.check(m)) {
					continue
				}
				out = append(out, Finding{Path: p, Line: n, Kind: d.kind, Hint: d.hint})
				seen[d.kind] = true
				break
			}
		}
		// Una llave en una sola línea: el encabezado seguido de su cuerpo, tras
		// un \n escapado (el JSON de una cuenta de servicio), en cadenas
		// concatenadas o con espacios.
		if !seen["llave privada"] {
			for _, loc := range pemHeaderRe.FindAllStringIndex(chunk, -1) {
				if keyBody(chunk[loc[1]:]) {
					out = append(out, Finding{Path: p, Line: n, Kind: "llave privada", Hint: "BEGIN … PRIVATE KEY"})
					seen["llave privada"] = true
					break
				}
			}
		}
		if end >= len(line) {
			break
		}
	}
	return out
}

// Binary informa si un contenido parece binario: no se escanea.
func Binary(data []byte) bool {
	n := len(data)
	if n > 8000 {
		n = 8000
	}
	for _, b := range data[:n] {
		if b == 0 {
			return true
		}
	}
	return false
}

var nameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,63}$`)

// valueLike descarta lo que parece un valor y no un nombre: largo, sin
// separadores y con mayúsculas, minúsculas y dígitos, como un tramo de base64.
func valueLike(k string) bool {
	if strings.ContainsAny(k, "_.-") {
		return false
	}
	return len(k) >= 24 || (len(k) >= 16 && classes(k) >= 3)
}

// Names lee los nombres de un archivo de secretos, sin sus valores: las
// variables de un .env o de un .properties y las claves de primer nivel de
// un JSON o un YAML. Un nombre que parece un secreto se omite.
func Names(p string, data []byte) []string {
	if Binary(data) {
		return nil
	}
	seen := map[string]bool{}
	add := func(k string) {
		k = strings.TrimSpace(k)
		if nameRe.MatchString(k) && !valueLike(k) && len(ScanLine("", 0, k)) == 0 {
			seen[k] = true
		}
	}
	base := strings.ToLower(path.Base(strings.ReplaceAll(p, "\\", "/")))
	text := string(data)
	switch {
	case strings.HasSuffix(base, ".json") || strings.Contains(base, ".tfstate"):
		var m map[string]any
		if json.Unmarshal(data, &m) == nil {
			for k := range m {
				add(k)
			}
			if outs, ok := m["outputs"].(map[string]any); ok && strings.Contains(base, ".tfstate") {
				for k := range outs {
					add("outputs." + k)
				}
			}
		}
	case strings.HasSuffix(base, ".yaml") || strings.HasSuffix(base, ".yml"):
		for _, l := range strings.Split(text, "\n") {
			if l == "" || l[0] == ' ' || l[0] == '\t' || l[0] == '#' || l[0] == '-' {
				continue
			}
			if i := strings.Index(l, ":"); i > 0 {
				add(l[:i])
			}
		}
	case strings.HasSuffix(base, ".pem") || strings.HasSuffix(base, ".key") || strings.HasSuffix(base, ".p12") ||
		strings.HasSuffix(base, ".pfx") || strings.HasSuffix(base, ".jks") || strings.HasSuffix(base, ".keystore") ||
		strings.HasPrefix(base, "id_"):
		return nil
	default:
		// Las líneas de una llave o de un valor entre comillas de varias líneas
		// son parte de un valor, no nombres: la última de un cuerpo en base64
		// termina en = y parecería una variable.
		inPEM, quote := false, byte(0)
		for _, l := range strings.Split(text, "\n") {
			l = strings.TrimSpace(l)
			if quote != 0 || inPEM {
				if quote != 0 && strings.IndexByte(l, quote) >= 0 {
					quote = 0
				}
				if strings.Contains(l, "-----END ") {
					inPEM = false
				}
				continue
			}
			if strings.Contains(l, "-----BEGIN ") && !strings.Contains(l, "-----END ") {
				inPEM = true
			}
			if l == "" || strings.HasPrefix(l, "#") || strings.HasPrefix(l, "!") || strings.HasPrefix(l, "-----") {
				continue
			}
			l = strings.TrimPrefix(l, "export ")
			i := strings.IndexAny(l, "=:")
			if i <= 0 {
				continue
			}
			add(l[:i])
			if v := strings.TrimSpace(l[i+1:]); v != "" && (v[0] == '"' || v[0] == '\'') && strings.IndexByte(v[1:], v[0]) < 0 {
				quote = v[0]
			}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
