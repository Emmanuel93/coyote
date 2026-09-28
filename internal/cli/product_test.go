package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// productoDemo arma tres repos: servicios con un BFF, una app y un backoffice.
func productoDemo(t *testing.T, base string) {
	t.Helper()
	svc := filepath.Join(base, "servicios")
	write(t, svc, "README.md", "# Servicios\n\nServicios de la tienda demo.\n")
	write(t, svc, "build.gradle.kts", "plugins { id(\"org.springframework.boot\") }\n")
	write(t, svc, "services/pedidos-service/build.gradle.kts", "dependencies {}\n")
	write(t, svc, "services/pedidos-service/src/main/java/demo/PedidosController.java", `package demo;

@RestController
@RequestMapping("/api/v1/pedidos")
class PedidosController {
    @GetMapping("/{id}")
    PedidoDto ver(@PathVariable String id) { return null; }

    void creado(Object p) { kafkaTemplate.send("pedidos.creado", p); }
}
`)
	write(t, svc, "services/bff-movil/build.gradle.kts", "dependencies {}\n")
	write(t, svc, "services/bff-movil/src/main/java/demo/bff/AppController.java", `package demo.bff;

@RestController
class AppController {
    private final PedidosClient pedidos;

    @GetMapping("/pedidos/{id}")
    Object ver(@PathVariable String id) { return pedidos.ver(id); }
}
`)
	write(t, svc, "services/bff-movil/src/main/java/demo/bff/PedidosClient.java", `package demo.bff;

class PedidosClient {
    Object ver(String id) {
        return web.get().uri("/api/v1/pedidos/{id}", id).retrieve().bodyToMono(Object.class).block();
    }
}
`)
	app := filepath.Join(base, "app")
	write(t, app, "README.md", "# App\n\nApp móvil de la tienda demo.\n")
	write(t, app, "pubspec.yaml", "name: tienda\n")
	write(t, app, "lib/api.dart", "class Api {\n  Future ver(String id) => _dio.get('/pedidos/$id');\n}\n")
	bo := filepath.Join(base, "backoffice")
	write(t, bo, "README.md", "# Backoffice\n\nBackoffice web de la tienda demo.\n")
	write(t, bo, "package.json", `{"name":"backoffice","scripts":{"test":"vitest"}}`)
	write(t, bo, "src/api.ts", "export const ver = (id: string) => fetch(`${API}/api/v1/pedidos/${id}`);\n")
	for _, d := range []string{svc, app, bo} {
		git(t, d, "init", "-q")
		git(t, d, "add", "-A")
		git(t, d, "commit", "-q", "-m", "inicio")
	}
}

func TestProductoMultiRepo(t *testing.T) {
	base := setup(t)
	productoDemo(t, base)
	root := filepath.Join(base, "producto")
	must(t, run(t, base, "", "init", "producto", "--type", "product", "--purpose", "tienda demo en tres repos"), 0, "init product")
	if rc := readFile(t, filepath.Join(root, "README.coyote.md")); !strings.Contains(rc, "test|coyote map --check") {
		t.Errorf("un producto se prueba con coyote map --check:\n%s", rc)
	}
	for _, r := range []string{"servicios", "app", "backoffice"} {
		must(t, run(t, root, "", "repo", "add", r, "--path", "../"+r), 0, "repo add "+r)
	}
	// El mapa: quién expone y quién usa cada endpoint, entre los tres repos.
	r := run(t, root, "", "map")
	must(t, r, 0, "map")
	if !strings.Contains(r.stdout, "3 enlaces entre módulos, 2 entre repos") {
		t.Errorf("map sin enlaces entre repos:\n%s", r.stdout)
	}
	for _, f := range []string{"servicios", "app", "backoffice"} {
		if _, err := os.Stat(filepath.Join(root, "coyote", "map", f+".map")); err != nil {
			t.Errorf("falta coyote/map/%s.map", f)
		}
	}
	if !strings.Contains(ledgerText(t, root), "|idx|mapa|mapa del producto") {
		t.Error("el mapa nuevo queda en el ledger")
	}
	must(t, run(t, root, "", "map", "--check"), 0, "map --check al día")

	// Impacto de cambiar el endpoint del servicio: el BFF y el backoffice
	// directo, la app a través del BFF.
	r = run(t, root, "", "impact", "GET /api/v1/pedidos/{id}")
	must(t, r, 0, "impact endpoint")
	for _, want := range []string{"Afecta directo (2)", "backoffice", "bff-movil", "a través", "app  llama GET /pedidos/{}", "Repos afectados: app, backoffice, servicios (3 de 3)"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("impact sin %q:\n%s", want, r.stdout)
		}
	}
	r = run(t, root, "", "impact", "--topic", "pedidos.creado", "--json")
	must(t, r, 0, "impact tópico")
	if !strings.Contains(r.stdout, `"touched"`) || !strings.Contains(r.stdout, "pedidos.creado") {
		t.Errorf("impact --json:\n%s", r.stdout)
	}
	must(t, run(t, root, "", "impact", "--topic", "x", "--diff", "servicios=HEAD"), 2, "dos tipos de cambio a la vez")
	must(t, run(t, root, "", "impact", "--diff", "servicios=--output=/tmp/x"), 2, "rango que parece bandera")
	must(t, run(t, root, "", "impact", "--files", "servicios:../fuera.java"), 2, "ruta fuera del repo")

	// Un cambio en el código del servicio, visto desde el diff.
	svc := filepath.Join(base, "servicios")
	ctrl := filepath.Join(svc, "services/pedidos-service/src/main/java/demo/PedidosController.java")
	data := readFile(t, ctrl)
	if err := os.WriteFile(ctrl, []byte(strings.Replace(data, "PedidoDto ver(@PathVariable String id) { return null; }",
		"PedidoDto ver(@PathVariable String id) { return buscar(id); }\n\n    @DeleteMapping(\"/{id}\")\n    void borrar(@PathVariable String id) {}", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, svc, "commit", "-qam", "borrar pedidos")
	r = run(t, root, "", "impact", "--diff", "servicios=HEAD~1..HEAD", "--format", "md", "--record")
	must(t, r, 0, "impact diff")
	if !strings.Contains(r.stdout, "### Impacto en el producto") || !strings.Contains(r.stdout, "| app |") || !strings.Contains(r.stdout, "el mapa de servicios es de") {
		t.Errorf("impact --format md:\n%s", r.stdout)
	}
	if !strings.Contains(ledgerText(t, root), "|rev|impacto|impacto:") {
		t.Error("impact --record deja un evento rev")
	}
	r = run(t, root, "", "map", "--check")
	must(t, r, 1, "map --check con un endpoint nuevo")
	if !strings.Contains(r.stdout, "servicios: +1 −0") || !strings.Contains(r.stdout, "DELETE /api/v1/pedidos/{}") {
		t.Errorf("map --check debe mostrar lo nuevo:\n%s", r.stdout)
	}
	must(t, run(t, root, "", "map"), 0, "map de nuevo")

	// Propuestas de documentos por repo, sin pisar lo que edita una persona.
	r = run(t, root, "", "extract")
	must(t, r, 0, "extract")
	readme := filepath.Join(root, "coyote", "repos", "app", "README.coyote.md")
	if got := readFile(t, readme); !strings.Contains(got, "dep|servicios|llama 1 endpoint de bff-movil") {
		t.Errorf("la propuesta de la app declara su dependencia:\n%s", got)
	}
	must(t, run(t, root, "", "extract", "--check"), 0, "extract --check al día")
	edited := readFile(t, readme) + "docs|docs/guia.md|guía escrita por una persona\n"
	if err := os.WriteFile(readme, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	r = run(t, root, "", "extract", "app")
	must(t, r, 0, "extract con una propuesta editada")
	if !strings.Contains(r.stdout, "editado por una persona") || readFile(t, readme) != edited {
		t.Errorf("una propuesta editada no se reemplaza:\n%s", r.stdout)
	}
	must(t, run(t, root, "", "extract", "nadie"), 1, "extract de un repo desconocido")

	// El contexto de un repo del producto sale de su propuesta, y ask encuentra el mapa.
	r = run(t, root, "", "get", "context", "servicios")
	must(t, r, 0, "get context de un repo del producto")
	if !strings.Contains(r.stdout, "# Contexto: servicios") || !strings.Contains(r.stdout, "lo usan") {
		t.Errorf("contexto del repo desde la propuesta:\n%s", r.stdout)
	}
	r = run(t, root, "", "ask", "quién llama pedidos")
	must(t, r, 0, "ask sobre el mapa")
	if !strings.Contains(r.stdout, "llama") || !strings.Contains(r.stdout, "coyote/map/") {
		t.Errorf("ask debe encontrar el mapa:\n%s", r.stdout)
	}
	r = run(t, root, "", "get", "context", "--query", "pedidos")
	must(t, r, 0, "get context del producto")
	if !strings.Contains(r.stdout, "## Interfaces del producto") {
		t.Errorf("el paquete del producto trae sus interfaces:\n%s", r.stdout)
	}

	// Nada de esto escribió en los repos.
	for _, d := range []string{"servicios", "app", "backoffice"} {
		dir := filepath.Join(base, d)
		if st := git(t, dir, "status", "--porcelain", "--ignored"); st != "" {
			t.Errorf("%s cambió: %s", d, st)
		}
	}
}
