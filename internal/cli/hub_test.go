package cli

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestHubDeLaOrganizacion(t *testing.T) {
	base := setup(t)
	hubDir := filepath.Join(base, "acme-hub")
	r := run(t, base, "", "hub", "init", "acme-hub", "--org", "acme")
	must(t, r, 0, "hub init")
	for _, f := range []string{"coyote/hub.yaml", "domains/README.md", "coyote/standards/rules.yaml", "AGENTS.md"} {
		if _, err := os.Stat(filepath.Join(hubDir, f)); err != nil {
			t.Fatalf("hub init no creó %s", f)
		}
	}
	conf := readFile(t, filepath.Join(hubDir, "coyote/hub.yaml"))
	if !strings.Contains(conf, "org: acme") || !strings.Contains(conf, `admins: ["@ana"]`) {
		t.Fatalf("hub.yaml:\n%s", conf)
	}
	if !strings.Contains(readFile(t, filepath.Join(hubDir, "coyote/project.yaml")), "type: hub") {
		t.Fatal("el hub es un proyecto coyote de tipo hub")
	}
	// Sin commit, el hub no rige.
	must(t, run(t, hubDir, "", "hub", "status"), 1, "hub sin commits")

	// La organización agrega una regla, un proyecto y un tope, y hace commit.
	if err := os.WriteFile(filepath.Join(hubDir, "coyote/hub.yaml"), []byte(strings.Replace(conf, "projects: []", "projects:\n  - { name: shop, path: ../shop }\n  - { name: infra, path: ../infra }", 1)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rules := "version: 1\nextends: coyote:default\nrules:\n  - id: ORG1\n    title: Todo servicio declara sus SLOs\n    level: SHOULD\n"
	if err := os.WriteFile(filepath.Join(hubDir, "coyote/standards/rules.yaml"), []byte(rules), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, hubDir, "add", "-A")
	git(t, hubDir, "commit", "-q", "-m", "chore(hub): estándar y proyectos de la organización")

	shop := filepath.Join(base, "shop")
	must(t, run(t, base, "", "init", "shop", "--type", "backend", "--purpose", "API de la tienda demo", "--hub", "../acme-hub"), 0, "init con hub")
	if err := os.WriteFile(filepath.Join(shop, "coyote/standards/rules.yaml"), []byte("version: 1\nextends: hub\nrules: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r = run(t, shop, "", "standards", "show")
	must(t, r, 0, "standards show")
	if !strings.Contains(r.stdout, "hub acme (rama main@") || !strings.Contains(r.stdout, "ORG1") {
		t.Fatalf("la capa del hub se ve con su commit:\n%s", r.stdout)
	}
	r = run(t, shop, "", "hub", "status", "--json")
	must(t, r, 0, "hub status")
	var st struct {
		Org      string
		Admins   []string
		Projects []hubProject
	}
	if err := json.Unmarshal([]byte(r.stdout), &st); err != nil {
		t.Fatal(err)
	}
	if st.Org != "acme" || len(st.Projects) != 2 || st.Projects[0].State != "sigue el hub" || st.Projects[1].State != "sin clon" {
		t.Fatalf("hub status: %+v", st)
	}
	r = run(t, shop, "", "doctor")
	if !strings.Contains(r.stdout, "hub") || !strings.Contains(r.stdout, "acme (rama main@") {
		t.Fatalf("doctor muestra el hub:\n%s", r.stdout)
	}

	// Un cambio sin commit en el hub no rige; el clon que falta es un error.
	if err := os.WriteFile(filepath.Join(hubDir, "coyote/standards/rules.yaml"), []byte("version: 1\nrules: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r = run(t, shop, "", "standards", "show"); !strings.Contains(r.stdout, "ORG1") {
		t.Fatalf("lo que no tiene commit en el hub no rige:\n%s", r.stdout)
	}
	if err := os.Rename(hubDir, hubDir+".bak"); err != nil {
		t.Fatal(err)
	}
	r = run(t, shop, "", "standards", "lint")
	if r.code == 0 || !strings.Contains(r.stderr+r.stdout, "no encuentro el clon") {
		t.Fatalf("un hub declarado que no se puede leer es un error:\n%s%s", r.stdout, r.stderr)
	}

	// init valida lo que escribe; la organización solo cuenta en un hub.
	must(t, run(t, base, "", "init", "otro", "--hub", "https://github.com/acme/hub"), 1, "hub como URL")
	must(t, run(t, base, "", "hub", "init", "h2", "--org", "acme: x"), 1, "organización inválida")
	must(t, run(t, base, "", "init", "café", "--type", "backend", "--purpose", "API con nombre acentuado"), 0, "un proyecto con acento no es un hub")
}

func TestGateProtegeElClonDelHub(t *testing.T) {
	base := setup(t)
	must(t, run(t, base, "", "hub", "init", "acme-hub", "--org", "acme"), 0, "hub init")
	must(t, run(t, base, "", "init", "shop", "--type", "backend", "--purpose", "API de la tienda demo", "--hub", "../acme-hub"), 0, "init")
	root := filepath.Join(base, "shop")
	for _, target := range []string{filepath.Join(base, "acme-hub", "coyote", "hub.yaml"), filepath.Join(base, "acme-hub", "coyote", "standards", "rules.yaml")} {
		in := fmt.Sprintf(`{"hook_event_name":"PreToolUse","tool_name":"Write","tool_input":{"file_path":%q,"content":"x"},"cwd":%q}`, target, root)
		r := run(t, root, in, "gate", "check", "--ide", "claude-code")
		if r.code != 2 || !strings.Contains(r.stderr, "bloqueado") {
			t.Fatalf("escribir %s desde el proyecto: %d\n%s%s", target, r.code, r.stdout, r.stderr)
		}
	}
}

// TestGateProtegeLasCarpetasDeGit cubre la segunda revisión: el clon del hub
// entero y todas las carpetas de git (la del hub, la del proyecto y la común
// de un worktree enlazado) no se escriben, su configuración no se lee y un
// comando con efectos sobre el clon se bloquea.
func TestGateProtegeLasCarpetasDeGit(t *testing.T) {
	base := setup(t)
	must(t, run(t, base, "", "hub", "init", "acme-hub", "--org", "acme"), 0, "hub init")
	git(t, filepath.Join(base, "acme-hub"), "add", "-A")
	git(t, filepath.Join(base, "acme-hub"), "commit", "-qm", "chore(hub): inicio")
	must(t, run(t, base, "", "init", "shop", "--type", "backend", "--purpose", "API de la tienda demo", "--hub", "../acme-hub"), 0, "init")
	root := filepath.Join(base, "shop")
	hubDir := filepath.Join(base, "acme-hub")
	decide := func(cwd, tool, input string) string {
		t.Helper()
		in := fmt.Sprintf(`{"hook_event_name":"PreToolUse","tool_name":%q,"tool_input":%s,"cwd":%q}`, tool, input, cwd)
		r := run(t, cwd, in, "gate", "check", "--ide", "claude-code")
		switch {
		case r.code == 0:
			return "libre"
		case strings.Contains(r.stderr, "bloqueado siempre"):
			return "bloqueado"
		case strings.Contains(r.stderr, "no aprobada"):
			return "aprobación"
		}
		t.Fatalf("respuesta inesperada %d: %s%s", r.code, r.stdout, r.stderr)
		return ""
	}
	q := func(s string) string { return fmt.Sprintf("%q", s) }
	cases := []struct{ tool, input, want string }{
		{"Write", `{"file_path":` + q(filepath.Join(hubDir, ".git", "refs", "heads", "main")) + `,"content":"x"}`, "bloqueado"},
		{"Write", `{"file_path":` + q(filepath.Join(hubDir, ".git", "config")) + `,"content":"x"}`, "bloqueado"},
		{"Write", `{"file_path":` + q(filepath.Join(hubDir, "README.md")) + `,"content":"x"}`, "bloqueado"},
		{"Read", `{"file_path":` + q(filepath.Join(hubDir, ".git", "config")) + `}`, "bloqueado"},
		{"Read", `{"file_path":` + q(filepath.Join(hubDir, "coyote", "hub.yaml")) + `}`, "libre"},
		{"Bash", `{"command":"git -C ../acme-hub update-ref refs/heads/main HEAD"}`, "bloqueado"},
		{"Bash", `{"command":"cd ../acme-hub/coyote && sed -i s/ana/mallory/ hub.yaml"}`, "bloqueado"},
		{"Bash", `{"command":"git -C ../acme-hub log --oneline -3"}`, "libre"},
		{"Grep", `{"pattern":"url","path":".git"}`, "bloqueado"},
		{"Grep", `{"pattern":"url","glob":".git/**"}`, "bloqueado"},
		{"Grep", `{"pattern":"url"}`, "libre"},
		{"Bash", `{"command":"grep -r url .git"}`, "bloqueado"},
		{"Bash", `{"command":"cat .git/HEAD"}`, "bloqueado"},
		{"Bash", `{"command":"grep -r url ."}`, "aprobación"},
		{"Bash", `{"command":"grep -r --exclude-dir=.git url ."}`, "libre"},
		{"Bash", `{"command":"ls .git"}`, "libre"},
	}
	for _, c := range cases {
		if got := decide(root, c.tool, c.input); got != c.want {
			t.Errorf("%s %s: %s, se esperaba %s", c.tool, c.input, got, c.want)
		}
	}
	// En un worktree enlazado, la carpeta de git real está en el checkout principal.
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "chore: inicio")
	wt := filepath.Join(base, "shop-wt")
	git(t, root, "worktree", "add", "-q", wt)
	for _, c := range []struct{ tool, input, want string }{
		{"Write", `{"file_path":` + q(filepath.Join(root, ".git", "config")) + `,"content":"x"}`, "bloqueado"},
		{"Write", `{"file_path":` + q(filepath.Join(root, ".git", "hooks", "pre-commit")) + `,"content":"x"}`, "bloqueado"},
		{"Read", `{"file_path":` + q(filepath.Join(root, ".git", "config")) + `}`, "bloqueado"},
	} {
		if got := decide(wt, c.tool, c.input); got != c.want {
			t.Errorf("desde el worktree, %s %s: %s, se esperaba %s", c.tool, c.input, got, c.want)
		}
	}
}

// webPages arranca coyote web y pide varias rutas.
func webPages(t *testing.T, root string, paths ...string) map[string]string {
	t.Helper()
	old := webServe
	defer func() { webServe = old }()
	pages := map[string]string{}
	webServe = func(srv *http.Server, ln net.Listener) error {
		defer ln.Close()
		for _, p := range paths {
			req := httptest.NewRequest(http.MethodGet, p, nil)
			req.Host = "127.0.0.1"
			rec := httptest.NewRecorder()
			srv.Handler.ServeHTTP(rec, req)
			pages[p] = fmt.Sprintf("%d\n%s", rec.Code, rec.Body.String())
		}
		return nil
	}
	must(t, run(t, root, "", "web", "--addr", "127.0.0.1:0"), 0, "web")
	return pages
}

func TestWebV1ConHubYVisibilidad(t *testing.T) {
	base := setup(t)
	hubDir := filepath.Join(base, "acme-hub")
	must(t, run(t, base, "", "hub", "init", "acme-hub", "--org", "acme"), 0, "hub init")
	conf := readFile(t, filepath.Join(hubDir, "coyote/hub.yaml"))
	conf = strings.Replace(conf, "monthly_usd: 0", "monthly_usd: 50", 1)
	conf = strings.Replace(conf, "projects: []", "projects:\n  - { name: shop, path: ../shop }\n  - { name: lejos, path: ../no-esta }", 1)
	if err := os.WriteFile(filepath.Join(hubDir, "coyote/hub.yaml"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, hubDir, "add", "-A")
	git(t, hubDir, "commit", "-q", "-m", "chore(hub): organización")

	root := filepath.Join(base, "shop")
	must(t, run(t, base, "", "init", "shop", "--type", "backend", "--purpose", "API de la tienda demo", "--hub", "../acme-hub"), 0, "init")
	must(t, run(t, root, "", "record", "run", "implementa pedidos", "--agent", "coyote-dev", "--tokens", "12k/8k/1k", "--cost", "0.01+0.02", "--refs", "model:sonnet-5"), 0, "record ana")
	t.Setenv("COYOTE_USER", "luis")
	must(t, run(t, root, "", "record", "run", "revisa pagos", "--agent", "coyote-reviewer", "--tokens", "2k/0/1k", "--cost", "0.2+0.3", "--refs", "model:opus-5.5"), 0, "record luis")
	must(t, run(t, root, "", "propose", "--bash", "make deploy TOKEN=ghp_"+strings.Repeat("a", 36)), 0, "propose")
	must(t, run(t, root, "", "propose", "--bash", "make test <b>x</b>"), 0, "propose 2")
	if err := os.WriteFile(filepath.Join(root, "coyote/approvals/P-0001.json"), []byte(`{"id":"P-0001","gate":"G1","release":"v0.1.0","decision":"approved","approver":"@ana","date":"2026-09-01","authorizes":["etiquetar v0.1.0"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(root, "coyote/workstreams/W-0001-pedidos")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	plan := "id: W-0001\ntitle: pedidos\nowner: \"@ana\"\nautonomy: manual\nrisk: R2\nbudget_usd: 5\nsteps:\n  - { id: S1, does: diseña pedidos, agent: coyote-architect, max_usd: 1 }\n"
	if err := os.WriteFile(filepath.Join(ws, "plan.yaml"), []byte(plan), 0o644); err != nil {
		t.Fatal(err)
	}

	// Luis no es admin: ve lo suyo y los totales; no ve la organización.
	pages := webPages(t, root, "/?since=all", "/org", "/gate", "/workstreams", "/presupuesto")
	if p := pages["/?since=all"]; strings.Contains(p, "<td>@ana") || !strings.Contains(p, "<td>@luis") || !strings.Contains(p, "total del proyecto") {
		t.Fatalf("luis no ve el desglose de ana:\n%s", p)
	}
	if !strings.HasPrefix(pages["/org"], "403") {
		t.Fatalf("/org para luis: %s", pages["/org"][:3])
	}
	gatePage := pages["/gate"]
	if strings.Contains(gatePage, "ghp_") || !strings.Contains(gatePage, "TOKEN=***") || strings.Contains(gatePage, "<b>x</b>") || !strings.Contains(gatePage, "G1") {
		t.Fatalf("la cola se muestra sin secretos ni HTML, con los gates de release:\n%s", gatePage)
	}
	if !strings.Contains(pages["/workstreams"], "W-0001") || !strings.Contains(pages["/presupuesto"], "$50.00") {
		t.Fatalf("workstreams y presupuesto:\n%s\n%s", pages["/workstreams"], pages["/presupuesto"])
	}

	// Ana es admin del hub: ve a luis y la organización, con el proyecto sin clon.
	t.Setenv("COYOTE_USER", "ana")
	pages = webPages(t, root, "/?view=person&since=all", "/org")
	if !strings.Contains(pages["/?view=person&since=all"], "@luis") {
		t.Fatalf("ana, admin, ve el desglose:\n%s", pages["/?view=person&since=all"])
	}
	org := pages["/org"]
	if !strings.HasPrefix(org, "200") || !strings.Contains(org, "Organización acme") || !strings.Contains(org, "sin clon en esta máquina") || !strings.Contains(org, "@luis") {
		t.Fatalf("vista de la organización:\n%s", org)
	}
}

func TestWebNoMuestraSecretos(t *testing.T) {
	if got := webAction("P-1", "Bash: export K=AKIA"+"Q3VZ7T2M9KX4B8JN"); strings.Contains(got, "Q3VZ") || !strings.Contains(got, "coyote review P-1") {
		t.Fatalf("una acción con un secreto no se muestra: %s", got)
	}
	if got := webAction("P-2", "Bash: make test\n\x1b[31mrojo"); strings.ContainsAny(got, "\n\x1b") {
		t.Fatalf("una línea visible, sin controles: %q", got)
	}
	// Quitar el marcador una vez lo rearmaría: se quita hasta que no quede.
	nested := "Bash: make deploy KEY=ASIA" + "Q3VZ7T2M9KX4B8JM # coyote:allow-coyote:allow-secretsecret"
	if got := webAction("P-3", nested); strings.Contains(got, "Q3VZ") {
		t.Fatalf("un marcador anidado no deja ver el secreto: %s", got)
	}
}

// TestWebEscapaTodaLaPagina: un título de plan con controles de dirección o
// escapes se ve como su código en toda la página, y un repo registrado que es
// la misma carpeta del proyecto no cuenta dos veces.
func TestWebEscapaTodaLaPagina(t *testing.T) {
	base := setup(t)
	root := filepath.Join(base, "shop")
	must(t, run(t, base, "", "init", "shop", "--type", "backend", "--purpose", "API de la tienda demo"), 0, "init")
	must(t, run(t, root, "", "note", "una nota para el ledger", "--type", "how", "--scope", "web"), 0, "note")
	one := webPages(t, root, "/api/usage")["/api/usage"]
	cfg := readFile(t, filepath.Join(root, "coyote/project.yaml"))
	write(t, root, "coyote/project.yaml", cfg+"repos:\n  - { name: mismo, path: . }\n")
	two := webPages(t, root, "/api/usage")["/api/usage"]
	events := regexp.MustCompile(`"events": [0-9]+`)
	if a, b := events.FindString(one), events.FindString(two); a == "" || a != b {
		t.Fatalf("un repo en la carpeta del proyecto no cuenta dos veces: %q vs %q\n%s", a, b, two)
	}
	// Un nombre de repo con escapes no pasa la validación del proyecto.
	write(t, root, "coyote/project.yaml", cfg+"repos:\n  - { name: \"x\\e]0;PWNED\\a\\e[2J\", path: . }\n")
	if r := run(t, root, "", "web", "--addr", "127.0.0.1:0"); r.code == 0 || !strings.Contains(r.stderr, "repos: nombre inválido") || strings.ContainsRune(r.stdout+r.stderr, 0x1b) {
		t.Fatalf("repo con escapes:\n%q\n%q", r.stdout, r.stderr)
	}
	write(t, root, "coyote/project.yaml", cfg)
	// Un plan con un título que se reordena en pantalla.
	write(t, root, "coyote/workstreams/W-0001-demo/plan.yaml", "id: W-0001\ntitle: \"pagar \\u202efactura\"\nowner: \"@ana\"\nautonomy: manual\nbudget_usd: 1\nsteps:\n  - { id: s1, does: \"ver \\u2066x\", agent: coyote-dev }\n")
	page := webPages(t, root, "/workstreams")["/workstreams"]
	if strings.ContainsAny(page, "\u202e\u2066") {
		t.Fatalf("la página no lleva controles de dirección:\n%s", page)
	}
}

func TestWebSoloLeeYAdminsDelProyecto(t *testing.T) {
	base := setup(t)
	state := filepath.Join(base, "estado")
	t.Setenv("COYOTE_STATE_DIR", state)
	root := filepath.Join(base, "shop")
	must(t, run(t, base, "", "init", "shop", "--type", "backend", "--purpose", "API de la tienda demo"), 0, "init")
	// Una propuesta vencida en la cola: mirar la web no la borra.
	old := `{"id":"P-oldold","hash":"sha256:` + strings.Repeat("a", 64) + `","object":"Bash: make x","tool":"Bash","kind":"bash","requested_by":"@ana","first_seen":"2026-01-01T00:00:00Z","last_seen":"2026-01-01T00:00:00Z","attempts":1,"status":"pending"}`
	write(t, root, ".coyote/proposals/P-oldold.json", old)
	pages := webPages(t, root, "/gate")
	if !strings.HasPrefix(pages["/gate"], "200") {
		t.Fatalf("/gate: %s", pages["/gate"][:200])
	}
	if _, err := os.Stat(filepath.Join(root, ".coyote/proposals/P-oldold.json")); err != nil {
		t.Fatal("la web no borra propuestas vencidas: solo mira")
	}
	if _, err := os.Stat(filepath.Join(state, "approvals.key")); err == nil {
		t.Fatal("la web no crea la clave de firmas")
	}
	// Un admin del proyecto ve a todas sus personas, no la organización.
	cfg := readFile(t, filepath.Join(root, "coyote/project.yaml"))
	write(t, root, "coyote/project.yaml", cfg+"admins: [\"@ana\"]\n")
	pages = webPages(t, root, "/org", "/")
	if !strings.Contains(pages["/"], "eres admin") && !strings.Contains(pages["/"], "admin") {
		t.Fatalf("admin del proyecto:\n%s", pages["/"])
	}
	if !strings.HasPrefix(pages["/org"], "404") {
		t.Fatalf("sin hub, /org no existe: %s", pages["/org"][:3])
	}
	if got := webText("a\u2028b\u200bc"); strings.ContainsAny(got, "\u2028\u200b") || !strings.Contains(got, `\u2028`) || !strings.Contains(got, `\u200`) {
		t.Fatalf("invisibles a la vista: %q", got)
	}
}

// TestElHubNoSeEsquiva cubre la segunda revisión: el proyecto no puede dejar
// fuera la capa del hub, ni leerla del árbol de trabajo, ni apagar sus reglas
// cambiando el perfil; un segundo documento YAML no esconde reglas y una ref
// completa no se completa con una etiqueta.
func TestElHubNoSeEsquiva(t *testing.T) {
	base := setup(t)
	hubDir := filepath.Join(base, "acme-hub")
	must(t, run(t, base, "", "hub", "init", "acme-hub", "--org", "acme"), 0, "hub init")
	org := "version: 1\nextends: coyote:default\nrules:\n  - id: ORG1\n    title: Todo backend declara sus SLOs\n    level: MUST\n    profiles: [backend]\n    check: { type: file_exists, files: [SLO.md] }\n"
	write(t, hubDir, "coyote/standards/rules.yaml", org)
	git(t, hubDir, "add", "-A")
	git(t, hubDir, "commit", "-qm", "chore(hub): estándar")
	shop := filepath.Join(base, "shop")
	must(t, run(t, base, "", "init", "shop", "--type", "backend", "--purpose", "API de la tienda demo", "--hub", "../acme-hub"), 0, "init con hub")
	write(t, shop, "coyote/standards/rules.yaml", "version: 1\nextends: hub\nrules: []\n")
	lintHas := func(want string) {
		t.Helper()
		r := run(t, shop, "", "standards", "lint")
		if r.code == 0 || !strings.Contains(r.stdout+r.stderr, want) {
			t.Fatalf("lint sin %q:\n%s%s", want, r.stdout, r.stderr)
		}
	}
	lintHas("ORG1")

	// El perfil de README.coyote.md suma reglas; no apaga las del tipo.
	readme := readFile(t, filepath.Join(shop, "README.coyote.md"))
	write(t, shop, "README.coyote.md", strings.Replace(readme, "---\n", "---\nstandards: { profile: nadie }\n", 1))
	lintHas("ORG1")
	write(t, shop, "README.coyote.md", readme)

	// Sin extends: hub, las reglas de la organización no rigen: S2 y doctor en rojo.
	write(t, shop, "coyote/standards/rules.yaml", "version: 1\nextends: coyote:default\nrules: []\n")
	lintHas("S2")
	if r := run(t, shop, "", "doctor"); !strings.Contains(r.stdout, "no pasa por extends: hub") {
		t.Fatalf("doctor avisa que el hub no rige:\n%s", r.stdout)
	}
	// Con motivo, queda a la vista pero no falla por S2.
	write(t, shop, "coyote/standards/rules.yaml", "version: 1\nextends: coyote:default\nreason: piloto aislado del estándar de la organización hasta Q4\nrules: []\n")
	if r := run(t, shop, "", "standards", "lint"); strings.Contains(r.stdout, "S2") {
		t.Fatalf("con motivo no hay S2:\n%s", r.stdout)
	}

	// Leer el hub del disco tomaría su árbol de trabajo.
	write(t, shop, "coyote/standards/rules.yaml", "version: 1\nextends: ../../../acme-hub/coyote/standards/rules.yaml\nrules: []\n")
	lintHas("lee el clon del hub del disco")
	write(t, shop, "coyote/standards/rules.yaml", "version: 1\nextends: hub\nrules: []\n")

	// Un segundo documento en el estándar del hub no se ignora en silencio.
	write(t, hubDir, "coyote/standards/rules.yaml", org+"---\nrules:\n  - { id: ORG2, title: Oculta, level: MUST }\n")
	git(t, hubDir, "commit", "-qam", "chore(hub): dos documentos")
	lintHas("documento")
	git(t, hubDir, "reset", "-q", "--hard", "HEAD~1")

	// refs/heads/release no existe: una etiqueta llamada así no la reemplaza.
	sha := strings.TrimSpace(git(t, hubDir, "rev-parse", "HEAD"))
	git(t, hubDir, "update-ref", "refs/tags/refs/heads/release", sha)
	cfg := readFile(t, filepath.Join(shop, "coyote/project.yaml"))
	pinned := strings.Replace(cfg, `hub: "../acme-hub"`, "hub: { path: ../acme-hub, ref: refs/heads/release }", 1)
	if pinned == cfg {
		t.Fatalf("project.yaml sin la línea del hub:\n%s", cfg)
	}
	write(t, shop, "coyote/project.yaml", pinned)
	if r := run(t, shop, "", "hub", "status"); r.code == 0 || !strings.Contains(r.stdout+r.stderr, "no es una rama") {
		t.Fatalf("una etiqueta no completa una ref:\n%s%s", r.stdout, r.stderr)
	}
}
