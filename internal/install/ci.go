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

// Secretos del workflow. Un token fino de GitHub cubre los repos de un solo
// dueño: si coyote vive en otra cuenta que el producto, su checkout usa
// COYOTE_TOOL_TOKEN. COYOTE_TEAMS_TOKEN, opcional, deja verificar los
// equipos de CODEOWNERS (Members: read de la organización).
const (
	SecretName      = "COYOTE_PRODUCT_TOKEN"
	ToolSecretName  = "COYOTE_TOOL_TOKEN"
	TeamsSecretName = "COYOTE_TEAMS_TOKEN"
)

// CIRepo es un repo del producto en GitHub.
type CIRepo struct {
	Name     string // nombre en el producto
	FullName string // owner/nombre en GitHub
}

// CIOptions define el workflow de impacto de un repo.
type CIOptions struct {
	Self       CIRepo
	Others     []CIRepo
	CoyoteRepo string   // owner/nombre del repo de coyote
	CoyoteRef  string   // etiqueta de la versión, p. ej. v0.5.0
	Policy     string   // warn o fail
	Risk       []string // reglas de riesgo del proyecto: R2=patrón o R3=patrón
	Gitleaks   bool     // escanea los commits del PR con gitleaks (features.gitleaks, ADR-0021)
}

// gitleaks fijado por versión y hash: subirlo es un cambio de coyote con su
// prueba (ADR-0021). El hash es el del tarball de Linux x64 del release.
const (
	emptyTree       = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"
	GitleaksVersion = "8.30.1"
	gitleaksURL     = "https://github.com/gitleaks/gitleaks/releases/download/v" + GitleaksVersion + "/gitleaks_" + GitleaksVersion + "_linux_x64.tar.gz"
	gitleaksSHA256  = "551f6fc83ea457d62a0d98237cbad105af8d557003051f41f3e7ca7b3f2470eb"
	// Las reglas por defecto de esa versión, para una base sin .gitleaks.toml:
	// se usan sin la lista global de rutas que saltan (lockfiles,
	// node_modules, imágenes, cualquier ruta con gitleaks.toml…), que un PR
	// podría usar para esconder un archivo. El resultado también va fijado.
	gitleaksDefaultURL    = "https://raw.githubusercontent.com/gitleaks/gitleaks/v" + GitleaksVersion + "/config/gitleaks.toml"
	gitleaksDefaultSHA256 = "e163e53b9e7e8a8511e77271e2b323ed057759542a6d988258afe3a1fa329caf"
	gitleaksStrictSHA256  = "9e66540bf992931a74bf926200494b6b9ac333b6b7d4e1abe71f2e529422d97a"
)

// GitleaksScript es el paso que corre gitleaks sobre el PR (ADR-0021):
//   - dos escaneos: los commits del PR (con --remerge-diff, para ver lo que
//     entra al resolver un merge) y el diff neto del PR como un commit aparte,
//     escrito fuera del clon, que ve lo que se integra aunque llegue por un
//     rename o por un archivo que fue binario en un commit intermedio;
//   - el repo se lee por su carpeta .git: gitleaks busca .gitleaksignore en
//     la carpeta que escanea, y en el árbol de trabajo sería el del PR;
//   - la configuración y las excepciones salen de la rama base, y los
//     gitleaks:allow del PR no cuentan;
//   - gitleaks sale con 0 aunque git falle: un envoltorio de git deja el error
//     en su log y el paso falla ante cualquier error;
//   - la configuración va por ruta absoluta: gitleaks salta el archivo del
//     repo cuya ruta es igual a la de --config, y una ruta del repo nunca es
//     absoluta;
//   - el commit del diff neto lleva autor y fecha fijos y sin firma: no
//     depende de la identidad de git del runner.
func GitleaksScript(self string) string {
	return `set -eu
cd "$RUNNER_TEMP"
curl -sSfL --retry 3 -o gitleaks.tgz ` + gitleaksURL + `
echo "` + gitleaksSHA256 + `  gitleaks.tgz" | sha256sum -c -
tar -xzf gitleaks.tgz gitleaks
repo="$GITHUB_WORKSPACE/repos/` + self + `"
git -C "$repo" cat-file -e "$BASE^{commit}"
git -C "$repo" cat-file -e "$HEAD^{commit}"
mkdir -p gitleaks-base gitshim net-objects
if git -C "$repo" cat-file -e "$BASE:.gitleaks.toml" 2>/dev/null; then
  git -C "$repo" show "$BASE:.gitleaks.toml" > gitleaks-base/gitleaks.toml
else
  curl -sSfL --retry 3 -o gitleaks-default.toml ` + gitleaksDefaultURL + `
  echo "` + gitleaksDefaultSHA256 + `  gitleaks-default.toml" | sha256sum -c -
  awk 'BEGIN{skip=0; done=0} /^\[\[rules\]\]/{done=1} !done && /^paths = \[/{skip=1; next} skip && /^\]/{skip=0; next} !skip{print}' \
    gitleaks-default.toml > gitleaks-base/gitleaks.toml
  echo "` + gitleaksStrictSHA256 + `  gitleaks-base/gitleaks.toml" | sha256sum -c -
fi
if git -C "$repo" cat-file -e "$BASE:.gitleaksignore" 2>/dev/null; then
  git -C "$repo" show "$BASE:.gitleaksignore" > gitleaks-base/.gitleaksignore
fi
printf '#!/bin/sh\n"%s" "$@"; rc=$?\n[ "$rc" -eq 0 ] || echo "git terminó con estado $rc" >&2\nexit "$rc"\n' "$(command -v git)" > gitshim/git
chmod +x gitshim/git
mb=$(git -C "$repo" merge-base "$BASE" "$HEAD")
net=$(GIT_OBJECT_DIRECTORY="$RUNNER_TEMP/net-objects" GIT_ALTERNATE_OBJECT_DIRECTORIES="$repo/.git/objects" \
  GIT_AUTHOR_NAME=coyote GIT_AUTHOR_EMAIL=coyote@localhost GIT_AUTHOR_DATE="@0 +0000" \
  GIT_COMMITTER_NAME=coyote GIT_COMMITTER_EMAIL=coyote@localhost GIT_COMMITTER_DATE="@0 +0000" \
  git -C "$repo" -c commit.gpgSign=false commit-tree "$HEAD^{tree}" -p "$mb" -m "diff del PR" </dev/null)
flags=(--config "$RUNNER_TEMP/gitleaks-base/gitleaks.toml" --gitleaks-ignore-path "$RUNNER_TEMP/gitleaks-base" --ignore-gitleaks-allow
  --redact --no-banner --no-color --report-format json --exit-code 0)
status=0
PATH="$RUNNER_TEMP/gitshim:$PATH" ./gitleaks git "$repo/.git" --log-opts="--remerge-diff --no-renames $BASE..$HEAD" "${flags[@]}" \
  --report-path gitleaks.json 2> gitleaks.log || status=$?
PATH="$RUNNER_TEMP/gitshim:$PATH" GIT_OBJECT_DIRECTORY="$RUNNER_TEMP/net-objects" GIT_ALTERNATE_OBJECT_DIRECTORIES="$repo/.git/objects" \
  ./gitleaks git "$repo/.git" --log-opts="--no-renames $mb..$net" "${flags[@]}" --report-path gitleaks-net.json 2>> gitleaks.log || status=$?
sed 's/\x1b\[[0-9;]*m//g' gitleaks.log > gitleaks.txt
cat gitleaks.txt >&2
if [ "$status" -ne 0 ] || grep -Eq '^[^ ]+ (ERR|FTL) ' gitleaks.txt; then
  echo "gitleaks no pudo revisar el PR" >&2
  exit 1
fi
`
}

var (
	fullNameRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})/[A-Za-z0-9._-]{1,100}$`)
	refRe      = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)
	nameRe     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	// riskRe solo deja caracteres de patrón: la regla va entre comillas simples en el shell.
	riskRe = regexp.MustCompile(`^R[23]=[A-Za-z0-9._/*?{},@+-]+$`)
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
	for _, r := range o.Risk {
		if !riskRe.MatchString(r) || strings.Contains(r, "..") {
			return fmt.Errorf("regla de riesgo inválida %q: R2=patrón o R3=patrón, con letras, números y . _ / * ? { } , @ + -", r)
		}
	}
	return nil
}

// GitHubWorkflow arma .github/workflows/coyote.yml para un repo del producto
// (ADR-0013). Corre en pull_request_target: el workflow, la política y las
// reglas de riesgo son los de la rama base, así un PR no cambia el chequeo
// que lo evalúa. El código del PR se lee como dato con git de plomería y
// nunca se compila ni se ejecuta; por eso los forks se evalúan igual.
func GitHubWorkflow(o CIOptions) (string, error) {
	if err := o.Validate(); err != nil {
		return "", err
	}
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	w("# Generado por coyote install --ci github (ADR-0013). No lo edites aquí: cámbialo en el proyecto del producto.")
	w("# Secretos: %s, un token de solo lectura (Contents: read) de los repos del producto;", SecretName)
	w("# %s, si coyote vive en otra cuenta; %s (opcional), Members: read para verificar los equipos de CODEOWNERS.", ToolSecretName, TeamsSecretName)
	w("# pull_request_target: este archivo y sus reglas salen de la rama base. El código del PR se lee como dato:")
	w("# nunca se compila ni se ejecuta. Después de una aprobación, vuelve a correr el chequeo (Re-run) o empuja un commit.")
	w("name: coyote gate pr")
	w("on:")
	w("  pull_request_target:")
	w("    # edited: cambiar la rama base de un PR vuelve a correr el chequeo.")
	w("    types: [opened, synchronize, reopened, ready_for_review, edited]")
	w("permissions:")
	w("  contents: read")
	w("  pull-requests: write")
	w("concurrency:")
	w("  group: coyote-gate-${{ github.event.pull_request.number }}")
	w("  cancel-in-progress: true")
	w("jobs:")
	w("  gate:")
	w("    name: riesgo e impacto")
	w("    runs-on: ubuntu-latest")
	w("    timeout-minutes: 15")
	w("    steps:")
	w("      - name: %s en el commit del PR, como dato", o.Self.Name)
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
	w("          token: ${{ secrets.%s || secrets.%s }}", ToolSecretName, SecretName)
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
	if o.Gitleaks {
		w("      - name: gitleaks %s en el PR, con la configuración de la rama base", GitleaksVersion)
		w("        env:")
		w("          BASE: ${{ github.event.pull_request.base.sha }}")
		w("          HEAD: ${{ github.event.pull_request.head.sha }}")
		w("          # Sin atributos de git: un .gitattributes no esconde un archivo como binario.")
		w("          GIT_ATTR_SOURCE: %s", emptyTree)
		w("        shell: bash")
		w("        run: |")
		for _, line := range strings.Split(strings.TrimSuffix(GitleaksScript(o.Self.Name), "\n"), "\n") {
			if line == "" {
				w("")
				continue
			}
			w("          %s", line)
		}
	}
	w("      - name: Riesgo, impacto y revisión")
	w("        env:")
	w("          GITHUB_TOKEN: ${{ github.token }}")
	w("          %s: ${{ secrets.%s }}", TeamsSecretName, TeamsSecretName)
	w("        run: >-")
	w("          \"$RUNNER_TEMP/coyote\" gate pr")
	w("          --repo %s=repos/%s", o.Self.Name, o.Self.Name)
	for _, r := range o.Others {
		w("          --repo %s=repos/%s", r.Name, r.Name)
	}
	for _, r := range o.Risk {
		w("          --risk '%s'", r)
	}
	if o.Gitleaks {
		w("          --gitleaks \"$RUNNER_TEMP/gitleaks.json\" --gitleaks-net \"$RUNNER_TEMP/gitleaks-net.json\"")
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
