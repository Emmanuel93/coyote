package ci

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Emmanuel93/coyote/internal/glob"
)

// Riesgos de un cambio: R1 bajo, R2 medio, R3 alto (como en los pasos de un plan).
const (
	R1 = "R1"
	R2 = "R2"
	R3 = "R3"
)

// Rule da un riesgo a las rutas que coinciden con sus patrones.
type Rule struct {
	Risk  string
	Paths []string
	Why   string
}

// DefaultRules son las reglas genéricas de riesgo por rutas. Cada proyecto
// agrega las suyas (--risk R3=patrón); nunca se relajan desde el PR.
var DefaultRules = []Rule{
	{R3, []string{".github/workflows/**", ".github/actions/**", ".gitlab-ci.yml", "CODEOWNERS", ".github/CODEOWNERS", "docs/CODEOWNERS"}, "el pipeline o quién revisa"},
	{R3, []string{".gitattributes", "**/.gitattributes", ".gitmodules"}, "cómo git lee los archivos"},
	{R3, []string{".gitleaks.toml", "**/.gitleaks.toml", ".gitleaksignore", "**/.gitleaksignore"}, "la configuración de gitleaks"},
	{R3, []string{"**/migrations/**", "**/migration/**", "**/db/changelog/**", "**/flyway/**", "**/*.sql"}, "el esquema de datos"},
	{R3, []string{"**/auth/**", "**/security/**", "**/crypto/**"}, "autenticación o seguridad"},
	{R3, []string{"**/*.tf", "**/*.tf.json", "**/*.tfvars", "**/*.tfvars.json", "**/terragrunt.hcl", "**/.terraform.lock.hcl",
		"**/terraform/**", "**/helm/**", "**/k8s/**", "**/kubernetes/**", "**/Dockerfile", "**/docker-compose*.{yml,yaml}"}, "infraestructura"},
	{R3, []string{".claude/**", ".cursor/**", ".codex/**", ".gemini/**", ".github/hooks/**", ".windsurf/**", ".devin/**",
		"coyote/project.yaml", "coyote/infra.yaml", "coyote/hub.yaml", "coyote/standards/**", "coyote/approvals/**"}, "el gate o el estándar"},
	{R2, []string{"**/openapi*.{yaml,yml,json}", "**/swagger*.{yaml,yml,json}", "**/asyncapi*.{yaml,yml}", "**/*.proto", "**/*.avsc", "**/*.graphql"}, "un contrato de API"},
	{R2, []string{"go.mod", "**/pom.xml", "**/build.gradle", "**/build.gradle.kts", "**/package.json", "**/pubspec.yaml", "**/requirements*.txt", "**/pyproject.toml", "**/Cargo.toml"}, "las dependencias"},
	{R2, []string{"**/application*.{yml,yaml,properties}", "**/bootstrap*.{yml,yaml,properties}"}, "la configuración del servicio"},
	{R2, []string{"coyote/slo/**", "**/coyote/slo/**"}, "los SLOs y sus alertas"},
}

// riskFlagRe acepta solo caracteres de patrón: la regla viaja por el shell del workflow.
var riskFlagRe = regexp.MustCompile(`^(R[23])=([A-Za-z0-9._/*?{},@+-]+)$`)

// ParseRiskFlag lee --risk R3=patrón.
func ParseRiskFlag(s string) (Rule, error) {
	m := riskFlagRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil || strings.HasPrefix(m[2], "!") || strings.Contains(m[2], "..") {
		return Rule{}, fmt.Errorf("--risk %q: usa R2=patrón o R3=patrón (services/*/pagos/**)", s)
	}
	return Rule{Risk: m[1], Paths: []string{m[2]}, Why: "regla del proyecto (" + m[2] + ")"}, nil
}

// Rank ordena los riesgos; un valor desconocido cuenta como R1.
func Rank(r string) int {
	switch r {
	case R3:
		return 3
	case R2:
		return 2
	}
	return 1
}

// Max devuelve el riesgo más alto.
func Max(a, b string) string {
	if Rank(b) > Rank(a) {
		return b
	}
	if a == "" {
		return R1
	}
	return a
}

// FileRisk es el riesgo de un archivo cambiado y la regla que lo da.
type FileRisk struct {
	Path string `json:"path"`
	Risk string `json:"risk"`
	Why  string `json:"why"`
}

// PathRisks da a cada archivo el riesgo más alto de las reglas que coinciden.
// Devuelve solo los de R2 o más, ordenados por riesgo y ruta.
func PathRisks(files []string, rules []Rule) []FileRisk {
	var out []FileRisk
	for _, f := range files {
		best := FileRisk{Path: f, Risk: R1}
		for _, r := range rules {
			if Rank(r.Risk) > Rank(best.Risk) && glob.Any(r.Paths, f) {
				best.Risk, best.Why = r.Risk, r.Why
			}
		}
		if Rank(best.Risk) >= 2 {
			out = append(out, best)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if Rank(out[i].Risk) != Rank(out[j].Risk) {
			return Rank(out[i].Risk) > Rank(out[j].Risk)
		}
		return out[i].Path < out[j].Path
	})
	return out
}
