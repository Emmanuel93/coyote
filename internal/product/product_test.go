package product

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Emmanuel93/coyote/internal/ccfdoc"
)

func put(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitRepo(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"},
		{"-c", "user.name=Ana", "-c", "user.email=ana@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "inicio"}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// producto arma tres repos sintéticos: servicios Spring con dos BFF, una app
// Flutter y un backoffice Nx.
func producto(t *testing.T) (string, string, string) {
	t.Helper()
	base := t.TempDir()
	svc := filepath.Join(base, "servicios")
	put(t, svc, "README.md", "# Servicios\n\n![insignia](x.svg)\n\nServicios de la tienda demo: pedidos, envíos y dos BFF.\n")
	put(t, svc, "settings.gradle.kts", `include("services:pedidos-service")`+"\n")
	put(t, svc, "build.gradle.kts", "plugins { id(\"org.springframework.boot\") }\n")
	put(t, svc, "gradlew", "#!/bin/sh\n")
	put(t, svc, "docs/dominio.md", "# Dominio de pedidos\n\ntexto\n")
	put(t, svc, "services/pedidos-service/build.gradle.kts", "dependencies {}\n")
	put(t, svc, "services/pedidos-service/src/main/java/demo/pedidos/PedidosController.java", `package demo.pedidos;

@RestController
@RequestMapping("/api/v1/pedidos")
public class PedidosController {
    private final PedidosService service;

    @GetMapping("/{id}")
    public PedidoDto get(@PathVariable String id) {
        return service.get(id);
    }

    @PostMapping
    public PedidoDto create(@RequestBody NuevoPedido body) {
        return service.create(body);
    }

    @RequestMapping(value = "/{id}/cancel", method = RequestMethod.POST, produces = "application/json")
    public void cancel(@PathVariable String id) {
        service.cancel(id);
    }
}
`)
	put(t, svc, "services/pedidos-service/src/main/java/demo/pedidos/PedidoDto.java", "package demo.pedidos;\n\npublic record PedidoDto(String id, String estado) {}\n")
	put(t, svc, "services/pedidos-service/src/main/java/demo/pedidos/PedidosEventos.java", `package demo.pedidos;

class PedidosEventos {
    static final String TOPIC_CREADO = "pedidos.pedido-creado";
    void creado(Object p) {
        kafkaTemplate.send(TOPIC_CREADO, p);
    }
}
`)
	put(t, svc, "services/pedidos-service/src/test/java/demo/pedidos/PedidosControllerTest.java", `@GetMapping("/no-cuenta")`)
	put(t, svc, "services/envios-service/build.gradle.kts", "dependencies {}\n")
	put(t, svc, "services/envios-service/src/main/java/demo/envios/Escucha.java", `package demo.envios;

class Escucha {
    @KafkaListener(topics = "pedidos.pedido-creado", groupId = "envios")
    void alCrear(String msg) {}

    @KafkaListener(topics = "${app.topics.devoluciones}")
    void alDevolver(String msg) {}
}
`)
	put(t, svc, "services/envios-service/src/main/java/demo/envios/EnviosClient.java", `package demo.envios;

@FeignClient(name = "tarifas", path = "/api/v1/tarifas")
interface TarifasClient {
    @GetMapping("/{zona}")
    Tarifa tarifa(@PathVariable String zona);
}
`)
	put(t, svc, "services/bff-movil/build.gradle.kts", "dependencies {}\n")
	put(t, svc, "services/bff-movil/src/main/java/demo/bff/AppController.java", `package demo.bff;

@RestController
class AppController {
    private final PedidosClient pedidos;

    @PostMapping("/pedidos")
    Object crear(@RequestBody Object body) { return pedidos.crear(body); }

    @GetMapping("/pedidos/{id}")
    Object ver(@PathVariable String id) { return pedidos.ver(id); }

    @GetMapping("/perfil")
    Object perfil() { return null; }
}
`)
	put(t, svc, "services/bff-movil/src/main/java/demo/bff/PedidosClient.java", `package demo.bff;

class PedidosClient {
    private final WebClient web;
    Object ver(String id) {
        return web.get().uri("/api/v1/pedidos/{id}", id).retrieve().bodyToMono(Object.class).block();
    }
    Object crear(Object body) {
        return web.post()
            .uri("/api/v1/pedidos")
            .bodyValue(body).retrieve().bodyToMono(Object.class).block();
    }
}
`)
	put(t, svc, "services/bff-backoffice/build.gradle.kts", "dependencies {}\n")
	put(t, svc, "services/bff-backoffice/src/main/java/demo/admin/AdminController.java", `package demo.admin;

@RestController
@RequestMapping("/admin")
class AdminController {
    private final AdminPedidos pedidos;

    @GetMapping({"/pedidos/{id}", "/ordenes/{id}"})
    Object ver(@PathVariable String id) { return pedidos.ver(id); }
}
`)
	put(t, svc, "services/bff-backoffice/src/main/java/demo/admin/AdminPedidos.java", `package demo.admin;

class AdminPedidos {
    Object ver(String id) {
        return restTemplate.getForObject(baseUrl + "/api/v1/pedidos/{id}", Object.class, id);
    }
}
`)
	gitRepo(t, svc)

	app := filepath.Join(base, "app")
	put(t, app, "README.md", "# App\n\nApp móvil de la tienda demo.\n")
	put(t, app, "pubspec.yaml", "name: tienda_app\n")
	put(t, app, "melos.yaml", "name: tienda\nscripts:\n  test:all: melos exec -- flutter test\n  analyze: melos exec -- dart analyze\n")
	put(t, app, "packages/tienda_pedidos/pubspec.yaml", "name: tienda_pedidos\ndescription: Pedidos desde la app\n")
	put(t, app, "packages/tienda_pedidos/lib/src/pedidos_api.dart", `class PedidosApi {
  final Dio _dio;
  Future<void> crear(Map body) => _dio.post('/pedidos', data: body);
  Future<Pedido> ver(String id) async {
    final r = await _dio.get<Map<String, dynamic>>('/pedidos/$id');
    return Pedido.fromJson(r.data!);
  }
  void irAInicio(BuildContext context) => context.go('/home');
  Future<void> reembolsos() => _dio.get('/reembolsos/${widget.id}/detalle');
}
`)
	put(t, app, "packages/tienda_pedidos/test/pedidos_api_test.dart", `_dio.get('/no-cuenta')`)
	gitRepo(t, app)

	bo := filepath.Join(base, "backoffice")
	put(t, bo, "README.md", "# Backoffice\n\nBackoffice web de la tienda demo.\n")
	put(t, bo, "package.json", `{"name":"backoffice","scripts":{"dev":"nx serve","test":"nx run-many -t test","build":"nx run-many -t build"}}`)
	put(t, bo, "nx.json", "{}")
	put(t, bo, "apps/mfe-pedidos/project.json", `{"name":"mfe-pedidos"}`)
	put(t, bo, "apps/mfe-pedidos/src/api.ts", "export const ver = (id: string) => request(`/admin/pedidos/${id}`);\n"+
		"export const verCon = (id: string) => fetch(`${API_URL}/admin/ordenes/${id}`, { method: 'GET' });\n"+
		"export const reembolsar = (body: unknown) => api.post('/admin/reembolsos', body);\n")
	put(t, bo, "apps/mfe-pedidos/src/api.spec.ts", "request('/no-cuenta')\n")
	gitRepo(t, bo)
	return svc, app, bo
}

func find(entries []Entry, role, method, p string) *Entry {
	for i := range entries {
		e := &entries[i]
		if e.Role == role && e.Method == method && e.Path == p {
			return e
		}
	}
	return nil
}

func TestNormPathAndMatch(t *testing.T) {
	cases := map[string]string{
		"/api/v1/pedidos/{id}":             "/api/v1/pedidos/{}",
		"/pedidos/$id":                     "/pedidos/{}",
		"/reembolsos/${widget.id}/detalle": "/reembolsos/{}/detalle",
		"${API_URL}/admin/ordenes/${id}":   "/admin/ordenes/{}",
		"$baseUrl/x":                       "/x",
		"http://localhost:8080/a/b?x=1":    "/a/b",
		"/users/:id/":                      "/users/{}",
		"pedidos":                          "",
		"/":                                "/",
	}
	for in, want := range cases {
		if got := NormPath(in); got != want {
			t.Errorf("NormPath(%q) = %q; quiero %q", in, got, want)
		}
	}
	if MatchScore("/pedidos/{}", "/pedidos/{}") <= MatchScore("/pedidos/{}", "/pedidos/activos") {
		t.Error("parámetro contra parámetro debe ganarle a parámetro contra literal")
	}
	if MatchScore("/pedidos/activos", "/pedidos/activos") <= MatchScore("/pedidos/activos", "/pedidos/{}") {
		t.Error("el literal exacto debe ganar")
	}
	if MatchScore("/pedidos", "/pedidos/{}") != -1 || MatchScore("/a/b", "/a/c") != -1 {
		t.Error("rutas distintas no coinciden")
	}
}

func TestExtractThreeStacks(t *testing.T) {
	svc, app, bo := producto(t)
	s, err := Extract(Source{Name: "servicios", Dir: svc})
	if err != nil {
		t.Fatal(err)
	}
	if s.SHA == "" || !strings.Contains(strings.Join(s.Identity.Stacks, ","), "spring") || s.Identity.Test != "./gradlew test" ||
		s.Identity.Purpose != "Servicios de la tienda demo: pedidos, envíos y dos BFF." || len(s.Identity.Docs) != 1 {
		t.Errorf("identidad de servicios inesperada: %+v (sha %q)", s.Identity, s.SHA)
	}
	want := []struct{ role, method, path string }{
		{Exposes, "GET", "/api/v1/pedidos/{}"}, {Exposes, "POST", "/api/v1/pedidos"}, {Exposes, "POST", "/api/v1/pedidos/{}/cancel"},
		{Exposes, "POST", "/pedidos"}, {Exposes, "GET", "/pedidos/{}"}, {Exposes, "GET", "/admin/pedidos/{}"}, {Exposes, "GET", "/admin/ordenes/{}"},
		{Calls, "GET", "/api/v1/pedidos/{}"}, {Calls, "POST", "/api/v1/pedidos"}, {Calls, "GET", "/api/v1/tarifas/{}"},
		{Publishes, "", "pedidos.pedido-creado"}, {Listens, "", "pedidos.pedido-creado"},
	}
	for _, w := range want {
		if find(s.Entries, w.role, w.method, w.path) == nil {
			t.Errorf("falta %s %s %s en servicios", w.role, w.method, w.path)
		}
	}
	if e := find(s.Entries, Exposes, "GET", "/api/v1/pedidos/{}"); e == nil || e.Line != 8 || e.Module != "services/pedidos-service" {
		t.Errorf("ubicación inesperada: %+v", e)
	}
	unresolved := 0
	for _, e := range s.Entries {
		if e.Unresolved {
			unresolved++
		}
		if strings.Contains(e.Path, "no-cuenta") {
			t.Error("las pruebas no son interfaces")
		}
	}
	if unresolved != 1 {
		t.Errorf("el tópico por propiedad debe quedar sin resolver: %d", unresolved)
	}
	a, err := Extract(Source{Name: "app", Dir: app})
	if err != nil {
		t.Fatal(err)
	}
	if a.Identity.Type != "mobile" || a.Identity.Test != "melos run test:all" || len(a.Identity.Modules) != 1 || a.Identity.Modules[0].Description != "Pedidos desde la app" {
		t.Errorf("identidad de la app inesperada: %+v", a.Identity)
	}
	for _, w := range [][2]string{{"POST", "/pedidos"}, {"GET", "/pedidos/{}"}, {"GET", "/reembolsos/{}/detalle"}} {
		if find(a.Entries, Calls, w[0], w[1]) == nil {
			t.Errorf("falta la llamada %s %s en la app: %+v", w[0], w[1], a.Entries)
		}
	}
	if len(a.Entries) != 3 {
		t.Errorf("rutas de pantalla o de pruebas no son llamadas: %+v", a.Entries)
	}
	b, err := Extract(Source{Name: "backoffice", Dir: bo})
	if err != nil {
		t.Fatal(err)
	}
	if b.Identity.Run != "npm run dev" || !strings.Contains(strings.Join(b.Identity.Stacks, ","), "nx") {
		t.Errorf("identidad del backoffice inesperada: %+v", b.Identity)
	}
	for _, w := range [][2]string{{"GET", "/admin/pedidos/{}"}, {"GET", "/admin/ordenes/{}"}, {"POST", "/admin/reembolsos"}} {
		if find(b.Entries, Calls, w[0], w[1]) == nil {
			t.Errorf("falta la llamada %s %s en el backoffice: %+v", w[0], w[1], b.Entries)
		}
	}
}

func buildMap(t *testing.T) (*Map, Sources) {
	t.Helper()
	_, m, src := buildScans(t)
	return m, src
}

func buildScans(t *testing.T) ([]*Scan, *Map, Sources) {
	t.Helper()
	svc, app, bo := producto(t)
	var scans []*Scan
	src := Sources{"servicios": svc, "app": app, "backoffice": bo}
	for _, name := range []string{"servicios", "app", "backoffice"} {
		sc, err := Extract(Source{Name: name, Dir: src[name]})
		if err != nil {
			t.Fatal(err)
		}
		scans = append(scans, sc)
	}
	return scans, Build(scans), src
}

func TestMapLinksAcrossRepos(t *testing.T) {
	m, _ := buildMap(t)
	has := func(fromRepo, fromPath, toRepo, toMod string) bool {
		for _, l := range m.Links {
			f, to := m.Entries[l.From], m.Entries[l.To]
			if f.Repo == fromRepo && f.Path == fromPath && to.Repo == toRepo && ModuleName(to.Module) == toMod {
				return true
			}
		}
		return false
	}
	for _, c := range [][4]string{
		{"app", "/pedidos", "servicios", "bff-movil"}, {"app", "/pedidos/{}", "servicios", "bff-movil"},
		{"backoffice", "/admin/pedidos/{}", "servicios", "bff-backoffice"}, {"backoffice", "/admin/ordenes/{}", "servicios", "bff-backoffice"},
		{"servicios", "/api/v1/pedidos/{}", "servicios", "pedidos-service"}, {"servicios", "pedidos.pedido-creado", "servicios", "pedidos-service"},
	} {
		if !has(c[0], c[1], c[2], c[3]) {
			t.Errorf("falta el enlace %s %s → %s/%s", c[0], c[1], c[2], c[3])
		}
	}
	sum := m.Summary()
	if len(sum) != 3 {
		t.Fatalf("resumen por repo: %+v", sum)
	}
	for _, st := range sum {
		if st.Repo == "backoffice" && (st.Linked != 2 || st.Unlinked != 1) {
			t.Errorf("el backoffice tiene 2 llamadas enlazadas y 1 sin servicio: %+v", st)
		}
	}
	// Ida y vuelta del archivo de mapa.
	for repo, sha := range m.SHAs {
		text := Encode(repo, sha, m.Entries)
		r, s, entries, err := Decode(text)
		if err != nil || r != repo || s != sha || Encode(r, s, entries) != text {
			t.Errorf("el mapa de %s no se lee igual: %v", repo, err)
		}
	}
}

func hitIn(hits []Hit, repo, p string) bool {
	for _, h := range hits {
		if h.Entry.Repo == repo && h.Entry.Path == p {
			return true
		}
	}
	return false
}

func TestImpactHolistic(t *testing.T) {
	m, src := buildMap(t)
	im, err := m.Impact(Query{Endpoint: "GET /api/v1/pedidos/{pedidoId}"}, src)
	if err != nil {
		t.Fatal(err)
	}
	if len(im.Touched) != 1 || im.Touched[0].Entry.Module != "services/pedidos-service" {
		t.Fatalf("tocado inesperado: %+v", im.Touched)
	}
	if !hitIn(im.Direct, "servicios", "/api/v1/pedidos/{}") {
		t.Errorf("los BFF que llaman el endpoint son impacto directo: %+v", im.Direct)
	}
	if !hitIn(im.Indirect, "app", "/pedidos/{}") || !hitIn(im.Indirect, "backoffice", "/admin/pedidos/{}") || !hitIn(im.Indirect, "backoffice", "/admin/ordenes/{}") {
		t.Errorf("la app y el backoffice son impacto indirecto a través de sus BFF: %+v", im.Indirect)
	}
	if hitIn(im.Indirect, "app", "/pedidos") {
		t.Errorf("POST /pedidos usa el mismo cliente pero no el GET que cambió: %+v", im.Indirect)
	}
	if strings.Join(im.Repos, ",") != "app,backoffice,servicios" {
		t.Errorf("repos afectados: %v", im.Repos)
	}
	// Tópico: quien publica y quien escucha, y lo sin resolver del módulo.
	im, _ = m.Impact(Query{Topic: "pedidos.pedido-creado"}, src)
	if len(im.Touched) != 2 || len(im.Unresolved) != 1 {
		t.Errorf("tópico: %+v sin resolver %+v", im.Touched, im.Unresolved)
	}
	// Texto libre.
	im, _ = m.Impact(Query{Text: "cancelar pedido"}, src)
	if len(im.Touched) == 0 {
		t.Error("el texto debe encontrar el endpoint de cancelación")
	}
}

func TestImpactFromDiff(t *testing.T) {
	m, src := buildMap(t)
	svc := src["servicios"]
	// Cambio de contrato: un campo nuevo en el DTO.
	put(t, svc, "services/pedidos-service/src/main/java/demo/pedidos/PedidoDto.java", "package demo.pedidos;\n\npublic record PedidoDto(String id, String estado, String zona) {}\n")
	// Cambio dentro del método de cancelación.
	ctrl := filepath.Join(svc, "services/pedidos-service/src/main/java/demo/pedidos/PedidosController.java")
	data, _ := os.ReadFile(ctrl)
	if err := os.WriteFile(ctrl, []byte(strings.Replace(string(data), "service.cancel(id);", "service.cancel(id, true);", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"-c", "user.name=Ana", "-c", "user.email=ana@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "zona"}} {
		if out, err := exec.Command("git", append([]string{"-C", svc}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
	}
	im, err := m.Impact(Query{Repo: "servicios", Diff: "HEAD~1..HEAD"}, src)
	if err != nil {
		t.Fatal(err)
	}
	why := map[string]string{}
	for _, h := range im.Touched {
		why[h.Entry.Describe()] = h.Why
	}
	if !strings.Contains(why["POST /api/v1/pedidos/{}/cancel"], "cambia") {
		t.Errorf("el cambio dentro del método toca su endpoint: %v", why)
	}
	if !strings.Contains(why["GET /api/v1/pedidos/{}"], "PedidoDto") {
		t.Errorf("un DTO que cambió toca los endpoints que lo usan: %v", why)
	}
	if !hitIn(im.Indirect, "app", "/pedidos/{}") {
		t.Errorf("el cambio de contrato llega a la app a través del BFF: %+v", im.Indirect)
	}
	if _, err := m.Impact(Query{Repo: "servicios", Diff: "--output=/tmp/x"}, src); err == nil {
		t.Error("un rango que parece bandera no se pasa a git")
	}
}

func validDoc(t *testing.T, name, content string) {
	t.Helper()
	d, issues := ccfdoc.Parse(name, []byte(content))
	issues = append(issues, d.Validate()...)
	if ccfdoc.HasErrors(issues) {
		t.Errorf("%s no es válido: %v\n%s", name, issues, content)
	}
}

func TestProposals(t *testing.T) {
	scans, m, _ := buildScans(t)
	props := map[string]Proposal{}
	for _, sc := range scans {
		p := Propose(sc, m, "2026-09-28")
		props[sc.Repo] = p
		for name, content := range map[string]string{ccfdoc.ReadmeFile: p.Readme, ccfdoc.ContextFile: p.Context} {
			validDoc(t, name, content)
			if Edited(content) {
				t.Errorf("%s/%s recién propuesto no cuenta como editado", sc.Repo, name)
			}
			if !Edited(content + "how|x|un cambio de la persona|-\n") {
				t.Errorf("%s/%s: un cambio de la persona debe detectarse", sc.Repo, name)
			}
			if doc, h := StripMarker(content); h == "" || strings.Contains(doc, MarkerPrefix) {
				t.Errorf("la huella se quita completa: %q", h)
			}
		}
	}
	svc, app, bo := props["servicios"], props["app"], props["backoffice"]
	for _, want := range []string{"purpose|Servicios de la tienda demo", "test|./gradlew test", "mod|bff-movil|", "docs|docs/dominio.md|Dominio de pedidos"} {
		if !strings.Contains(svc.Readme, want) {
			t.Errorf("README de servicios sin %q:\n%s", want, svc.Readme)
		}
	}
	for _, want := range []string{"inv|bff-movil|lo usan app: tienda_pedidos", "inv|pedidos-service|lo usan bff-movil, bff-backoffice y envios-service",
		"gap|envios-service|1 ruta o tópico sin resolver", "gap|contratos|"} {
		if !strings.Contains(svc.Context, want) {
			t.Errorf("CONTEXT de servicios sin %q:\n%s", want, svc.Context)
		}
	}
	if !strings.Contains(app.Readme, "dep|servicios|llama 2 endpoints de bff-movil") {
		t.Errorf("la app declara su dependencia de los servicios:\n%s", app.Readme)
	}
	if !strings.Contains(bo.Readme, "dep|servicios|llama 2 endpoints de bff-backoffice") {
		t.Errorf("el backoffice declara su dependencia de los servicios:\n%s", bo.Readme)
	}
	if strings.Contains(app.Readme, "mod|raíz") {
		t.Error("el repo entero no se lista como módulo")
	}
}

func TestProposalFitsBudget(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "README.md", "# Grande\n\nUn monorepo con muchos servicios.\n")
	put(t, dir, "build.gradle.kts", "plugins { id(\"org.springframework.boot\") }\n")
	for i := 0; i < 60; i++ {
		mod := fmt.Sprintf("services/servicio-%02d", i)
		put(t, dir, mod+"/build.gradle.kts", "dependencies {}\n")
		put(t, dir, mod+"/src/main/java/demo/C.java", fmt.Sprintf("@RestController\nclass C {\n  @GetMapping(\"/api/v1/recurso-%02d/{id}\")\n  Object get() { return null; }\n}\n", i))
		put(t, dir, fmt.Sprintf("docs/tema-%02d.md", i), fmt.Sprintf("# Tema %02d\n", i))
	}
	sc, err := Extract(Source{Name: "grande", Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	p := Propose(sc, Build([]*Scan{sc}), "2026-09-28")
	validDoc(t, ccfdoc.ReadmeFile, p.Readme)
	validDoc(t, ccfdoc.ContextFile, p.Context)
	if p.Omitted == 0 {
		t.Error("lo que no cabe se cuenta como omitido")
	}
}

func commitAll(t *testing.T, dir, msg string) {
	t.Helper()
	for _, args := range [][]string{{"add", "-A"}, {"-c", "user.name=Ana", "-c", "user.email=ana@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "-m", msg}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
	}
}

func hit(hits []Hit, repo, p string) *Hit {
	for i := range hits {
		if hits[i].Entry.Repo == repo && hits[i].Entry.Path == p {
			return &hits[i]
		}
	}
	return nil
}

func TestImpactContractChanges(t *testing.T) {
	scans, m, src := buildScans(t)
	svc := src["servicios"]
	ctrl := filepath.Join(svc, "services/pedidos-service/src/main/java/demo/pedidos/PedidosController.java")
	data, _ := os.ReadFile(ctrl)
	// Se quita GET /{id} (lo usan los dos BFF) y se agrega GET /{id}/historial.
	changed := strings.Replace(string(data), `    @GetMapping("/{id}")
    public PedidoDto get(@PathVariable String id) {
        return service.get(id);
    }`, `    @GetMapping("/{id}/historial")
    public Object historial(@PathVariable String id) {
        return service.historial(id);
    }`, 1)
	if changed == string(data) {
		t.Fatal("el reemplazo no aplicó")
	}
	if err := os.WriteFile(ctrl, []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	// Y se borra un archivo sin interfaces, junto con el cambio.
	if err := os.Remove(filepath.Join(svc, "services/pedidos-service/src/main/java/demo/pedidos/PedidoDto.java")); err != nil {
		t.Fatal(err)
	}
	commitAll(t, svc, "historial")

	check := func(name string, m *Map) {
		t.Helper()
		im, err := m.Impact(Query{Repo: "servicios", Diff: "HEAD~1..HEAD"}, src)
		if err != nil {
			t.Fatal(err)
		}
		why := map[string]string{}
		for _, h := range im.Touched {
			why[h.Entry.Describe()] = h.Why
		}
		if !strings.HasPrefix(why["GET /api/v1/pedidos/{}"], "se elimina") || !strings.HasPrefix(why["GET /api/v1/pedidos/{}/historial"], "se agrega") {
			t.Errorf("%s: el diff muestra lo que se elimina y lo que se agrega: %v", name, why)
		}
		h := hit(im.Direct, "servicios", "/api/v1/pedidos/{}")
		if h == nil || !h.Breaking || !strings.Contains(h.Why, "elimina") {
			t.Errorf("%s: el BFF que llama al endpoint eliminado se rompe: %+v", name, im.Direct)
		}
		if !hitIn(im.Indirect, "app", "/pedidos/{}") {
			t.Errorf("%s: la app se entera a través del BFF: %+v", name, im.Indirect)
		}
	}
	// Mapa del commit base (el caso normal: el mapa se arma en main).
	check("mapa base", m)
	// Mapa del commit nuevo: lo eliminado ya no está en el mapa y aun así se ve.
	var fresh []*Scan
	for _, sc := range scans {
		if sc.Repo == "servicios" {
			sc2, err := Extract(Source{Name: "servicios", Dir: svc})
			if err != nil {
				t.Fatal(err)
			}
			sc = sc2
		}
		fresh = append(fresh, sc)
	}
	check("mapa nuevo", Build(fresh))
}

func TestChangedLinesSides(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "a.txt", "1\n2\n3\n")
	put(t, dir, "b.txt", "x\n")
	put(t, dir, "c.txt", "uno\ndos\n")
	gitRepo(t, dir)
	put(t, dir, "a.txt", "1\nnuevo\n2\n3\n")
	if err := os.Remove(filepath.Join(dir, "b.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "c.txt")); err != nil {
		t.Fatal(err)
	}
	commitAll(t, dir, "cambios")
	ch, err := changedLines(dir, "HEAD~1..HEAD", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := ch.new["a.txt"]; len(got) != 1 || got[0] != (lineRange{2, 2}) {
		t.Errorf("línea agregada en el lado nuevo: %v", got)
	}
	if got := ch.old["a.txt"]; len(got) != 1 || got[0] != (lineRange{1, 1}) {
		t.Errorf("punto de inserción en el lado base: %v", got)
	}
	for _, f := range []string{"b.txt", "c.txt"} {
		if len(ch.old[f]) == 0 || len(ch.new[f]) != 0 {
			t.Errorf("%s borrado: base %v nuevo %v", f, ch.old[f], ch.new[f])
		}
	}
	left, right, wt := diffSides(dir, "HEAD~1..HEAD")
	if left == "" || right == "" || wt || left == right {
		t.Errorf("lados del diff: %q %q %v", left, right, wt)
	}
	if _, _, wt := diffSides(dir, "HEAD~1"); !wt {
		t.Error("una sola revisión se compara con el árbol de trabajo")
	}
}

func TestConstantesPorArchivoYClase(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "build.gradle.kts", "plugins { id(\"org.springframework.boot\") }\n")
	put(t, dir, "libs/eventos/build.gradle.kts", "dependencies {}\n")
	put(t, dir, "libs/eventos/src/main/java/demo/Topics.java", `package demo;
public final class Topics {
    public static final String PAGO_APLICADO = "pagos.pago-aplicado";
    public static final String TOPIC = "no.este";
}
`)
	put(t, dir, "services/alta/build.gradle.kts", "dependencies {}\n")
	put(t, dir, "services/alta/src/main/java/demo/ProspectoPublisher.java", `package demo;
class ProspectoPublisher {
    static final String TOPIC = "alta.prospecto-creado";
    void publicar(Object e) { kafkaTemplate.send(TOPIC, e); }
}
`)
	put(t, dir, "services/alta/src/main/java/demo/ScorePublisher.java", `package demo;
class ScorePublisher {
    static final String TOPIC = "alta.score-solicitado";
    void publicar(Object e) { kafkaTemplate.send(TOPIC, e); }
    void pago(Object e) { kafkaTemplate.send(Topics.PAGO_APLICADO, e); }
}
`)
	put(t, dir, "services/cobro/build.gradle.kts", "dependencies {}\n")
	put(t, dir, "services/cobro/src/main/java/demo/Escucha.java", `package demo;
import static demo.Topics.PAGO_APLICADO;
class Escucha {
    @KafkaListener(topics = PAGO_APLICADO)
    void alPagar(String m) {}
    @KafkaListener(topics = TOPIC)
    void ambiguo(String m) {}
}
`)
	sc, err := Extract(Source{Name: "svc", Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range sc.Entries {
		key := e.Role + " " + path.Base(e.File)
		if e.Unresolved {
			key += " ?"
		}
		got[key] += e.Path + ";"
	}
	want := map[string]string{
		"publica ProspectoPublisher.java": "alta.prospecto-creado;",
		"publica ScorePublisher.java":     "alta.score-solicitado;pagos.pago-aplicado;",
		"escucha Escucha.java":            "pagos.pago-aplicado;",
		"escucha Escucha.java ?":          "TOPIC;",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %q, se esperaba %q (todo: %v)", k, got[k], v, got)
		}
	}
}
