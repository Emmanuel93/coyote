// Package secrets reconoce secretos (ADR-0016): los archivos que los guardan,
// por su nombre, y los secretos escritos en un texto, por patrones de alta
// confianza. Nunca devuelve un valor: un hallazgo dice archivo, línea y tipo.
package secrets

import (
	"bufio"
	"encoding/json"
	"path"
	"regexp"
	"sort"
	"strings"

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

// suffixes son archivos de secretos por su terminación.
var suffixes = []struct{ suffix, kind string }{
	{".tfstate", "estado de Terraform"},
	{".tfstate.backup", "estado de Terraform"},
	{".pem", "llave o certificado"},
	{".key", "llave privada"},
	{".p12", "llave o certificado"},
	{".pfx", "llave o certificado"},
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
var samples = []string{".env", ".env.local", ".env.production", ".envrc", "terraform.tfstate", "terraform.tfstate.backup",
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
// mostrar el valor; check filtra lo que coincide pero no es un secreto.
type detector struct {
	kind  string
	re    *regexp.Regexp
	hint  string
	check func(m []string) bool
}

var detectors = []detector{
	{kind: "llave privada", re: regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA |OPENSSH |ENCRYPTED |PGP )?PRIVATE KEY(?: BLOCK)?-----`), hint: "BEGIN … PRIVATE KEY"},
	{kind: "llave de acceso de AWS", re: regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), hint: "AKIA…"},
	{kind: "llave secreta de AWS", re: regexp.MustCompile(`(?i)aws_?secret_?access_?key["']?\s*[:=]\s*["']?[A-Za-z0-9/+=]{40}(?:[^A-Za-z0-9/+=]|$)`), hint: "aws_secret_access_key = …"},
	{kind: "token de GitHub", re: regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{36,255}|github_pat_[A-Za-z0-9_]{22,255})\b`), hint: "ghp_…"},
	{kind: "token de GitLab", re: regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}`), hint: "glpat-…"},
	{kind: "token de Slack", re: regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,}`), hint: "xox…"},
	{kind: "webhook de Slack", re: regexp.MustCompile(`https://hooks\.slack\.com/services/T[A-Za-z0-9]+/B[A-Za-z0-9]+/[A-Za-z0-9]+`), hint: "hooks.slack.com/services/…"},
	{kind: "llave de Stripe", re: regexp.MustCompile(`\b(?:sk|rk)_live_[A-Za-z0-9]{20,}`), hint: "sk_live_…"},
	{kind: "llave de SendGrid", re: regexp.MustCompile(`\bSG\.[A-Za-z0-9_-]{22}\.[A-Za-z0-9_-]{43}\b`), hint: "SG.…"},
	{kind: "token de npm", re: regexp.MustCompile(`\bnpm_[A-Za-z0-9]{36}\b`), hint: "npm_…"},
	{kind: "token de PyPI", re: regexp.MustCompile(`\bpypi-AgEIcHlwaS5vcmc[A-Za-z0-9_-]{50,}`), hint: "pypi-…"},
	{kind: "llave de API de un modelo", re: regexp.MustCompile(`\bsk-(?:ant-(?:api|admin)\d{2}|proj|svcacct|admin)-[A-Za-z0-9_-]{40,}`), hint: "sk-…"},
	{kind: "URL con contraseña", re: regexp.MustCompile(`\b[a-z][a-z0-9+.-]{1,20}://[^\s:/@'"<>]{1,64}:([^\s:/@'"<>]{8,128})@[A-Za-z0-9.-]+`), hint: "usuario:…@servidor",
		check: func(m []string) bool { return randomLooking(m[1]) }},
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
	classes := 0
	for _, set := range []string{"abcdefghijklmnopqrstuvwxyz", "ABCDEFGHIJKLMNOPQRSTUVWXYZ", "0123456789"} {
		if strings.ContainsAny(s, set) {
			classes++
		}
	}
	if strings.IndexFunc(s, func(r rune) bool { return !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", r) }) >= 0 {
		classes++
	}
	return (len(s) >= 12 && classes >= 3) || (len(s) >= 20 && classes >= 2)
}

// MaxLine es el largo máximo de línea que se revisa: un archivo minificado
// o un binario no se escanean línea por línea.
const MaxLine = 64 << 10

// Scan busca secretos en un texto. Las líneas con coyote:allow-secret no cuentan.
func Scan(p, text string) []Finding {
	var out []Finding
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 64<<10), MaxLine)
	n := 0
	for sc.Scan() {
		n++
		out = append(out, ScanLine(p, n, sc.Text())...)
	}
	return out
}

// ScanLine busca secretos en una línea.
func ScanLine(p string, n int, line string) []Finding {
	if len(line) > MaxLine || strings.Contains(line, AllowMarker) {
		return nil
	}
	var out []Finding
	for _, d := range detectors {
		for _, m := range d.re.FindAllStringSubmatch(line, -1) {
			if d.check != nil && !d.check(m) {
				continue
			}
			out = append(out, Finding{Path: p, Line: n, Kind: d.kind, Hint: d.hint})
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
		if nameRe.MatchString(k) && len(ScanLine("", 0, k)) == 0 {
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
		for _, l := range strings.Split(text, "\n") {
			l = strings.TrimSpace(l)
			if l == "" || strings.HasPrefix(l, "#") || strings.HasPrefix(l, "!") {
				continue
			}
			l = strings.TrimPrefix(l, "export ")
			if i := strings.IndexAny(l, "=:"); i > 0 {
				add(l[:i])
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
