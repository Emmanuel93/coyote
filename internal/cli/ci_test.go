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
	must(t, run(t, root, "", "install", "--ci", "github", "--coyote-ref", "v0.5.0", "--coyote-repo", "acme/coyote", "--check"), 0, "check vigente")
	must(t, run(t, root, "", "install", "--ci", "github", "--coyote-ref", "v0.5.1", "--coyote-repo", "acme/coyote", "--check"), 1, "check con otra versión")
	must(t, run(t, root, "", "install", "--ci", "gitlab"), 2, "otro proveedor")
	must(t, run(t, root, "", "install", "--ci", "github", "--ide", "cursor"), 2, "ide y ci a la vez")
}
