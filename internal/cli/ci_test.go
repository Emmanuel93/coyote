package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Emmanuel93/coyote/internal/product"
)

// fakeGitHub simula los comentarios de un PR.
type fakeGitHub struct {
	mu       sync.Mutex
	comments map[int64]string
	next     int64
	posts    int
	patches  int
	auth     string
}

func (f *fakeGitHub) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.auth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		var in struct {
			Body string `json:"body"`
		}
		_ = json.Unmarshal(body, &in)
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/repos/acme/servicios/issues/7/comments"):
			var list []map[string]any
			for id, b := range f.comments {
				list = append(list, map[string]any{"id": id, "body": b, "html_url": "https://x/c/1", "user": map[string]string{"type": "Bot"}})
			}
			_ = json.NewEncoder(w).Encode(list)
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/servicios/issues/7/comments":
			f.posts++
			f.next++
			f.comments[f.next] = in.Body
			_ = json.NewEncoder(w).Encode(map[string]any{"id": f.next, "html_url": "https://x/c/1"})
		case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/repos/acme/servicios/issues/comments/"):
			f.patches++
			for id := range f.comments {
				f.comments[id] = in.Body
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 1, "html_url": "https://x/c/1"})
		default:
			t.Errorf("llamada inesperada: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func TestCIImpact(t *testing.T) {
	base := setup(t)
	productoDemo(t, base)
	svc := filepath.Join(base, "servicios")
	baseSHA := strings.TrimSpace(git(t, svc, "rev-parse", "HEAD"))
	// El PR elimina el endpoint que usan el BFF y el backoffice.
	ctrl := filepath.Join(svc, "services/pedidos-service/src/main/java/demo/PedidosController.java")
	data := readFile(t, ctrl)
	changed := strings.Replace(data, `    @GetMapping("/{id}")
    PedidoDto ver(@PathVariable String id) { return null; }
`, "", 1)
	if changed == data {
		t.Fatal("el reemplazo no aplicó")
	}
	if err := os.WriteFile(ctrl, []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, svc, "commit", "-qam", "quita ver")
	headSHA := strings.TrimSpace(git(t, svc, "rev-parse", "HEAD"))

	event := filepath.Join(base, "event.json")
	ev := map[string]any{"number": 7, "pull_request": map[string]any{"number": 7,
		"base": map[string]any{"sha": baseSHA, "ref": "main", "repo": map[string]string{"full_name": "acme/servicios"}},
		"head": map[string]any{"sha": headSHA, "ref": "feat", "repo": map[string]string{"full_name": "acme/servicios"}}}}
	raw, _ := json.Marshal(ev)
	if err := os.WriteFile(event, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	gh := &fakeGitHub{comments: map[int64]string{}}
	srv := httptest.NewServer(gh.handler(t))
	defer srv.Close()
	summary := filepath.Join(base, "summary.md")
	t.Setenv("GITHUB_API_URL", srv.URL)
	t.Setenv("GITHUB_TOKEN", "tok-ci")
	t.Setenv("GITHUB_REPOSITORY", "acme/servicios")
	t.Setenv("GITHUB_EVENT_PATH", event)
	t.Setenv("GITHUB_STEP_SUMMARY", summary)
	args := []string{"ci", "impact", "--repo", "servicios=" + svc, "--repo", "app=" + filepath.Join(base, "app"),
		"--repo", "backoffice=" + filepath.Join(base, "backoffice"), "--comment"}

	r := run(t, base, "", args...)
	must(t, r, 0, "ci impact warn")
	for _, want := range []string{"coyote: el cambio rompe 2 interfaces", "se rompe", "| backoffice |", "a través de un servicio intermedio", "política `warn`"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("reporte sin %q:\n%s", want, r.stdout)
		}
	}
	if s := readFile(t, summary); !strings.Contains(s, "coyote: el cambio rompe") {
		t.Errorf("resumen del job:\n%s", s)
	}
	if gh.posts != 1 || gh.patches != 0 || gh.auth != "Bearer tok-ci" {
		t.Errorf("primer push: %d comentarios nuevos, %d actualizados, auth %q", gh.posts, gh.patches, gh.auth)
	}
	// Otro push: se actualiza el mismo comentario.
	must(t, run(t, base, "", args...), 0, "ci impact de nuevo")
	if gh.posts != 1 || gh.patches != 1 {
		t.Errorf("segundo push: %d nuevos, %d actualizados", gh.posts, gh.patches)
	}
	// Política fail: el chequeo falla porque algo se rompe.
	r = run(t, base, "", append(args, "--policy", "fail")...)
	must(t, r, 1, "ci impact fail")
	if !strings.Contains(r.stderr, "rompe 2 interfaces") {
		t.Errorf("fail:\n%s", r.stderr)
	}
	// Un PR de un fork no corre ni comenta.
	posts, patches := gh.posts, gh.patches
	ev["pull_request"].(map[string]any)["head"].(map[string]any)["repo"] = map[string]string{"full_name": "otra/servicios"}
	raw, _ = json.Marshal(ev)
	if err := os.WriteFile(event, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	r = run(t, base, "", args...)
	must(t, r, 0, "fork")
	if !strings.Contains(r.stdout, "fork") || gh.patches != patches || gh.posts != posts {
		t.Errorf("un fork no se analiza:\n%s", r.stdout)
	}
	// Con la política fail, un fork no queda verde por omisión.
	r = run(t, base, "", append(args, "--policy", "fail")...)
	must(t, r, 1, "fork con fail")
	if !strings.Contains(r.stderr, "viene de un fork") {
		t.Errorf("fork con fail:\n%s", r.stderr)
	}
	// Errores de uso.
	must(t, run(t, base, "", "ci", "impact"), 2, "sin repos")
	must(t, run(t, base, "", "ci", "impact", "--repo", "servicios="+svc, "--self", "nadie", "--base", baseSHA, "--head", headSHA, "--event", ""), 2, "self desconocido")
	must(t, run(t, base, "", "ci", "impact", "--repo", "servicios="+svc, "--base", "--output=x", "--head", headSHA, "--event", ""), 2, "commit que parece bandera")
	// Nada de esto escribió en los repos.
	for _, d := range []string{"servicios", "app", "backoffice"} {
		if st := git(t, filepath.Join(base, d), "status", "--porcelain", "--ignored"); st != "" {
			t.Errorf("%s cambió: %s", d, st)
		}
	}
}

func TestInstallCI(t *testing.T) {
	base := setup(t)
	productoDemo(t, base)
	root := filepath.Join(base, "producto")
	must(t, run(t, base, "", "init", "producto", "--type", "product", "--purpose", "tienda demo"), 0, "init")
	for _, r := range []string{"servicios", "app", "backoffice"} {
		must(t, run(t, root, "", "repo", "add", r, "--path", "../"+r), 0, "repo add "+r)
	}
	// Sin URL ni remoto no sabe a qué repo de GitHub apunta.
	must(t, run(t, root, "", "install", "--ci", "github", "--coyote-ref", "v0.5.0", "--coyote-repo", "acme/coyote"), 1, "sin remoto")
	for _, r := range []string{"servicios", "app", "backoffice"} {
		git(t, filepath.Join(base, r), "remote", "add", "origin", "git@github.com:acme/"+r+".git")
	}
	// Un binario sin versión etiquetada no genera el workflow.
	must(t, run(t, root, "", "install", "--ci", "github", "--coyote-repo", "acme/coyote"), 1, "versión sin etiqueta")
	r := run(t, root, "", "install", "--ci", "github", "--coyote-ref", "v0.5.0", "--coyote-repo", "acme/coyote")
	must(t, r, 0, "install --ci github")
	if !strings.Contains(r.stdout, "coyote/ci/app.yml") || !strings.Contains(r.stdout, "COYOTE_PRODUCT_TOKEN") {
		t.Errorf("salida:\n%s", r.stdout)
	}
	wf := readFile(t, filepath.Join(root, "coyote", "ci", "app.yml"))
	if !strings.Contains(wf, "--self app") || !strings.Contains(wf, "repository: acme/servicios") || strings.Contains(wf, "repository: acme/app\n") {
		t.Errorf("workflow de app:\n%s", wf)
	}
	// Las reglas de riesgo del proyecto viajan en el workflow de cada repo.
	cfgPath := filepath.Join(root, "coyote", "project.yaml")
	cfg := readFile(t, cfgPath)
	if err := os.WriteFile(cfgPath, []byte(cfg+"risk:\n  R3: [\"services/*/src/**/pagos/**\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	must(t, run(t, root, "", "install", "--ci", "github", "--coyote-ref", "v0.5.0", "--coyote-repo", "acme/coyote", "--check"), 1, "check con reglas nuevas")
	must(t, run(t, root, "", "install", "--ci", "github", "--coyote-ref", "v0.5.0", "--coyote-repo", "acme/coyote"), 0, "install con reglas")
	if wf := readFile(t, filepath.Join(root, "coyote", "ci", "servicios.yml")); !strings.Contains(wf, "--risk 'R3=services/*/src/**/pagos/**'") || !strings.Contains(wf, "gate pr") {
		t.Errorf("workflow con reglas:\n%s", wf)
	}
	if err := os.WriteFile(cfgPath, []byte(cfg+"risk:\n  R3: [\"x'; rm -rf ~\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r = run(t, root, "", "install", "--ci", "github", "--coyote-ref", "v0.5.0", "--coyote-repo", "acme/coyote")
	must(t, r, 1, "regla que rompe el shell")
	if !strings.Contains(r.stderr, "risk: patrón inválido") {
		t.Errorf("regla inválida:\n%s", r.stderr)
	}
	if err := os.WriteFile(cfgPath, []byte(cfg+"risk:\n  R3: [\"services/*/src/**/pagos/**\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	must(t, run(t, root, "", "install", "--ci", "github", "--coyote-ref", "v0.5.0", "--coyote-repo", "acme/coyote", "--check"), 0, "check vigente")
	must(t, run(t, root, "", "install", "--ci", "github", "--coyote-ref", "v0.5.1", "--coyote-repo", "acme/coyote", "--check"), 1, "check con otra versión")
	must(t, run(t, root, "", "install", "--ci", "gitlab"), 2, "otro proveedor")
	must(t, run(t, root, "", "install", "--ci", "github", "--ide", "cursor"), 2, "ide y ci a la vez")
}

func TestGatePR(t *testing.T) {
	base := setup(t)
	productoDemo(t, base)
	svc := filepath.Join(base, "servicios")
	// Dueños en la rama base: todo es de ana; pedidos, del equipo de pedidos.
	write(t, svc, ".github/CODEOWNERS", "* @ana\n/services/pedidos-service/ @acme/pedidos\n")
	git(t, svc, "add", "-A")
	git(t, svc, "commit", "-qm", "dueños")
	baseSHA := strings.TrimSpace(git(t, svc, "rev-parse", "HEAD"))
	// El PR de beto quita un endpoint que usan otros repos y se nombra dueño de todo.
	ctrl := filepath.Join(svc, "services/pedidos-service/src/main/java/demo/PedidosController.java")
	data := readFile(t, ctrl)
	changed := strings.Replace(data, "    @GetMapping(\"/{id}\")\n    PedidoDto ver(@PathVariable String id) { return null; }\n", "", 1)
	if changed == data {
		t.Fatal("el reemplazo no aplicó")
	}
	write(t, svc, "services/pedidos-service/src/main/java/demo/PedidosController.java", changed)
	write(t, svc, ".github/CODEOWNERS", "* @beto\n")
	git(t, svc, "commit", "-qam", "quita ver")
	headSHA := strings.TrimSpace(git(t, svc, "rev-parse", "HEAD"))

	event := filepath.Join(base, "event.json")
	ev := map[string]any{"number": 7, "pull_request": map[string]any{"number": 7, "user": map[string]string{"login": "beto"},
		"base": map[string]any{"sha": baseSHA, "ref": "main", "repo": map[string]string{"full_name": "acme/servicios"}},
		"head": map[string]any{"sha": headSHA, "ref": "feat", "repo": map[string]string{"full_name": "acme/servicios"}}}}
	raw, _ := json.Marshal(ev)
	if err := os.WriteFile(event, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	reviews := []map[string]any{}
	commits := []map[string]any{{"author": map[string]string{"login": "beto"}, "committer": map[string]string{"login": "web-flow"}}}
	comments := map[int64]string{}
	var teamAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		body, _ := io.ReadAll(r.Body)
		var in struct {
			Body string `json:"body"`
		}
		_ = json.Unmarshal(body, &in)
		switch {
		case r.URL.Path == "/repos/acme/servicios/pulls/7/reviews":
			if r.URL.Query().Get("page") == "1" {
				_ = json.NewEncoder(w).Encode(reviews)
			} else {
				_ = json.NewEncoder(w).Encode([]any{})
			}
		case r.URL.Path == "/repos/acme/servicios/pulls/7/commits":
			if r.URL.Query().Get("page") == "1" {
				_ = json.NewEncoder(w).Encode(commits)
			} else {
				_ = json.NewEncoder(w).Encode([]any{})
			}
		case r.URL.Path == "/orgs/acme/teams/pedidos/memberships/luis":
			teamAuth = r.Header.Get("Authorization")
			_ = json.NewEncoder(w).Encode(map[string]string{"state": "active"})
		case strings.HasPrefix(r.URL.Path, "/orgs/acme/teams/pedidos/memberships/"):
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "Not Found"})
		case r.URL.Path == "/orgs/acme/teams/pedidos":
			_ = json.NewEncoder(w).Encode(map[string]string{"slug": "pedidos"})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/servicios/issues/7/comments":
			var list []map[string]any
			for id, b := range comments {
				list = append(list, map[string]any{"id": id, "body": b, "html_url": "https://x/c/1", "user": map[string]string{"type": "Bot"}})
			}
			_ = json.NewEncoder(w).Encode(list)
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/servicios/issues/7/comments":
			comments[int64(len(comments)+1)] = in.Body
			_ = json.NewEncoder(w).Encode(map[string]any{"id": len(comments), "html_url": "https://x/c/1"})
		case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/repos/acme/servicios/issues/comments/"):
			for id := range comments {
				comments[id] = in.Body
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 1, "html_url": "https://x/c/1"})
		default:
			t.Errorf("llamada inesperada: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	t.Setenv("GITHUB_API_URL", srv.URL)
	t.Setenv("GITHUB_TOKEN", "tok-job")
	t.Setenv("COYOTE_TEAMS_TOKEN", "tok-equipos")
	t.Setenv("GITHUB_REPOSITORY", "acme/servicios")
	t.Setenv("GITHUB_EVENT_PATH", event)
	t.Setenv("GITHUB_STEP_SUMMARY", filepath.Join(base, "summary.md"))
	args := []string{"gate", "pr", "--repo", "servicios=" + svc, "--repo", "app=" + filepath.Join(base, "app"),
		"--repo", "backoffice=" + filepath.Join(base, "backoffice"), "--comment", "--policy", "fail"}

	// Sin revisiones: R3 por la ruta de CODEOWNERS y por lo que rompe; el merge espera.
	r := run(t, base, "", args...)
	must(t, r, 1, "gate pr sin aprobación")
	for _, want := range []string{"### coyote: R3, espera la aprobación de un dueño", "R3 por 1 archivo: el pipeline o quién revisa", "R3 por impacto: rompe 2 interfaces",
		"| `.github/CODEOWNERS` | R3 |", "- pendiente: @ana (`.github/CODEOWNERS`)", "- pendiente: @acme/pedidos (`services/pedidos-service/src/main/java/demo/PedidosController.java`)",
		"**Impacto.** El cambio rompe 2 interfaces.", "coyote gate pr ·"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("reporte sin %q:\n%s", want, r.stdout)
		}
	}
	if !strings.Contains(r.stderr, "R3 pide la aprobación de un dueño antes del merge (R17)") || len(comments) != 1 {
		t.Errorf("gate pr:\n%s\ncomentarios: %d", r.stderr, len(comments))
	}
	// beto aprueba lo suyo (no cuenta), ana aprueba un commit anterior (en R3 no cuenta).
	reviews = []map[string]any{
		{"user": map[string]string{"login": "beto"}, "state": "APPROVED", "commit_id": headSHA},
		{"user": map[string]string{"login": "ana"}, "state": "APPROVED", "commit_id": baseSHA},
	}
	r = run(t, base, "", args...)
	must(t, r, 1, "aprobaciones que no cuentan")
	if !strings.Contains(r.stdout, "la aprobación de @ana es de un commit anterior") {
		t.Errorf("aprobación vieja:\n%s", r.stdout)
	}
	// ana aprueba el último commit y luis, del equipo de pedidos, también: pasa.
	reviews = []map[string]any{
		{"user": map[string]string{"login": "ana"}, "state": "APPROVED", "commit_id": headSHA},
		{"user": map[string]string{"login": "luis"}, "state": "APPROVED", "commit_id": headSHA},
	}
	// Si luis subió un commit al PR, su aprobación no cuenta para su propio código.
	commits = append(commits, map[string]any{"author": map[string]string{"login": "Luis"}, "committer": map[string]string{"login": "luis"}})
	must(t, run(t, base, "", args...), 1, "aprueba quien escribió un commit")
	commits = commits[:1]
	r = run(t, base, "", args...)
	must(t, r, 0, "aprobado")
	if !strings.Contains(r.stdout, "### coyote: R3, aprobado por @ana, @luis") || teamAuth != "Bearer tok-equipos" || len(comments) != 1 {
		t.Errorf("aprobado:\n%s\nauth del equipo %q, comentarios %d", r.stdout, teamAuth, len(comments))
	}
	// Un PR desde un fork se evalúa igual (pull_request_target): no pasa por omisión.
	ev["pull_request"].(map[string]any)["head"].(map[string]any)["repo"] = map[string]string{"full_name": "otra/servicios"}
	raw, _ = json.Marshal(ev)
	if err := os.WriteFile(event, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	reviews = nil
	r = run(t, base, "", args...)
	must(t, r, 1, "fork evaluado")
	if !strings.Contains(r.stdout, "### coyote: R3, espera la aprobación de un dueño") {
		t.Errorf("fork:\n%s", r.stdout)
	}
	// Con warn nunca falla, y un cambio sin riesgo no pide revisión.
	must(t, run(t, base, "", append(args[:len(args)-2], "--policy", "warn")...), 0, "warn")
	r = run(t, base, "", "gate", "pr", "--repo", "servicios="+svc, "--base", baseSHA, "--head", baseSHA, "--event", "", "--policy", "fail")
	must(t, r, 0, "sin cambios")
	if !strings.Contains(r.stdout, "### coyote: R1, sin revisión extra") {
		t.Errorf("R1:\n%s", r.stdout)
	}
	must(t, run(t, base, "", "gate", "pr", "--repo", "servicios="+svc, "--risk", "R4=x", "--base", baseSHA, "--head", headSHA, "--event", ""), 2, "regla inválida")
	// Nada de esto escribió en los repos.
	for _, d := range []string{"servicios", "app", "backoffice"} {
		if st := git(t, filepath.Join(base, d), "status", "--porcelain", "--ignored"); st != "" {
			t.Errorf("%s cambió: %s", d, st)
		}
	}
}

// Lo que sale del código de un PR no arma enlaces ni HTML en el comentario.
func TestComentarioSinInyeccion(t *testing.T) {
	im := &product.Impact{Repos: []string{"svc"}, Touched: []product.Hit{{
		Entry: product.Entry{Repo: "svc", Module: "pagos", Role: "expone", Method: "GET", Path: "/x/[clic](https://malo.example)/<img src=x>", File: "src/a`b|c.java", Line: 3},
		Why:   "se elimina en [aquí](https://malo.example) <b>ya</b>"}}}
	m := &product.Map{SHAs: map[string]string{"svc": "abc1234"}}
	md := impactMarkdown(im, m, 1)
	for _, bad := range []string{"[clic](", "[aquí](", "<img", "<b>", "a`b"} {
		if strings.Contains(strings.ReplaceAll(md, "`GET /x/[clic](https://malo.example)/<img src=x>`", ""), bad) {
			t.Errorf("el reporte deja pasar %q:\n%s", bad, md)
		}
	}
	if !strings.Contains(md, "expone `GET /x/[clic](https://malo.example)/<img src=x>`") || !strings.Contains(md, `\[aquí\]`) || !strings.Contains(md, "&lt;b&gt;") {
		t.Errorf("la ruta va como código y el texto escapado:\n%s", md)
	}
}
