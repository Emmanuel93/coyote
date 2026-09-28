package install

import (
	"fmt"
	"regexp"
	"strings"
)

// Acciones fijadas por commit: una etiqueta se puede mover, un commit no.
const (
	checkoutAction = "actions/checkout@11d5960a326750d5838078e36cf38b85af677262 # v4.4.0"
	setupGoAction  = "actions/setup-go@40f1582b2485089dde7abd97c1529aa768e1baff # v5.6.0"
)

// SecretName es el secreto con el token de solo lectura de los repos del producto.
const SecretName = "COYOTE_PRODUCT_TOKEN"

// CIRepo es un repo del producto en GitHub.
type CIRepo struct {
	Name     string // nombre en el producto
	FullName string // owner/nombre en GitHub
}

// CIOptions define el workflow de impacto de un repo.
type CIOptions struct {
	Self       CIRepo
	Others     []CIRepo
	CoyoteRepo string // owner/nombre del repo de coyote
	CoyoteRef  string // etiqueta de la versión, p. ej. v0.5.0
	Policy     string // warn o fail
}

var (
	fullNameRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})/[A-Za-z0-9._-]{1,100}$`)
	refRe      = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)
	nameRe     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// Validate revisa todo lo que entra al YAML: ningún valor se interpola sin
// validar, así el workflow no admite inyección.
func (o CIOptions) Validate() error {
	all := append([]CIRepo{o.Self}, o.Others...)
	seen := map[string]bool{}
	for _, r := range all {
		if !nameRe.MatchString(r.Name) || !fullNameRe.MatchString(r.FullName) {
			return fmt.Errorf("repo inválido para el workflow: %q (%q)", r.Name, r.FullName)
		}
		if seen[r.Name] {
			return fmt.Errorf("repo repetido: %s", r.Name)
		}
		seen[r.Name] = true
	}
	if !fullNameRe.MatchString(o.CoyoteRepo) {
		return fmt.Errorf("repo de coyote inválido: %q", o.CoyoteRepo)
	}
	if !refRe.MatchString(o.CoyoteRef) {
		return fmt.Errorf("versión de coyote inválida %q: el workflow usa una versión etiquetada (vX.Y.Z)", o.CoyoteRef)
	}
	if o.Policy != "warn" && o.Policy != "fail" {
		return fmt.Errorf("política inválida %q: warn o fail", o.Policy)
	}
	return nil
}

// GitHubWorkflow arma .github/workflows/coyote-impact.yml para un repo del
// producto (ADR-0013). En cada PR interno trae los otros repos con el token de
// solo lectura, compila coyote de una versión etiquetada y reporta el impacto.
func GitHubWorkflow(o CIOptions) (string, error) {
	if err := o.Validate(); err != nil {
		return "", err
	}
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	w("# Generado por coyote install --ci github (ADR-0013). No lo edites aquí: cámbialo en el proyecto del producto.")
	w("# Necesita el secreto %s: un token de solo lectura (Contents: read) de los repos del producto y del de coyote.", SecretName)
	w("name: coyote impact")
	w("on:")
	w("  pull_request:")
	w("    types: [opened, synchronize, reopened, ready_for_review]")
	w("permissions:")
	w("  contents: read")
	w("  pull-requests: write")
	w("concurrency:")
	w("  group: coyote-impact-${{ github.event.pull_request.number }}")
	w("  cancel-in-progress: true")
	w("jobs:")
	w("  impacto:")
	w("    # Solo PRs de ramas de este repo: un fork no recibe el secreto ni corre coyote.")
	w("    if: github.event.pull_request.head.repo.full_name == github.repository")
	w("    runs-on: ubuntu-latest")
	w("    timeout-minutes: 15")
	w("    steps:")
	w("      - name: %s en el commit del PR", o.Self.Name)
	w("        uses: %s", checkoutAction)
	w("        with:")
	w("          ref: ${{ github.event.pull_request.head.sha }}")
	w("          path: repos/%s", o.Self.Name)
	w("          fetch-depth: 0")
	w("          persist-credentials: false")
	for _, r := range o.Others {
		w("      - name: %s, rama principal", r.Name)
		w("        uses: %s", checkoutAction)
		w("        with:")
		w("          repository: %s", r.FullName)
		w("          path: repos/%s", r.Name)
		w("          token: ${{ secrets.%s }}", SecretName)
		w("          persist-credentials: false")
	}
	w("      - name: coyote %s", o.CoyoteRef)
	w("        uses: %s", checkoutAction)
	w("        with:")
	w("          repository: %s", o.CoyoteRepo)
	w("          ref: %s", o.CoyoteRef)
	w("          path: coyote-src")
	w("          token: ${{ secrets.%s }}", SecretName)
	w("          persist-credentials: false")
	w("      - uses: %s", setupGoAction)
	w("        with:")
	w("          go-version-file: coyote-src/go.mod")
	w("          cache: false")
	w("      - name: Compilar coyote")
	w("        working-directory: coyote-src")
	w("        env:")
	w("          GOFLAGS: -mod=mod")
	w("          GOPROXY: \"off\"")
	w("          GOSUMDB: \"off\"")
	w("          CGO_ENABLED: \"0\"")
	w("        run: go build -trimpath -o \"$RUNNER_TEMP/coyote\" ./cmd/coyote")
	w("      - name: Impacto en el producto")
	w("        env:")
	w("          GITHUB_TOKEN: ${{ github.token }}")
	w("        run: >-")
	w("          \"$RUNNER_TEMP/coyote\" ci impact")
	w("          --repo %s=repos/%s", o.Self.Name, o.Self.Name)
	for _, r := range o.Others {
		w("          --repo %s=repos/%s", r.Name, r.Name)
	}
	w("          --self %s --policy %s --comment", o.Self.Name, o.Policy)
	return b.String(), nil
}

// GitHubFullName saca owner/nombre de una URL de GitHub (https o ssh).
func GitHubFullName(url string) (string, bool) {
	u := strings.TrimSpace(url)
	for _, p := range []string{"https://github.com/", "http://github.com/", "ssh://git@github.com/", "git@github.com:"} {
		if strings.HasPrefix(u, p) {
			name := strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(u, p), "/"), ".git")
			if fullNameRe.MatchString(name) {
				return name, true
			}
		}
	}
	return "", false
}
