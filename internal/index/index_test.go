package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Emmanuel93/coyote/internal/tokens"
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

func project(t *testing.T) string {
	t.Helper()
	t.Setenv("COYOTE_STATE_DIR", t.TempDir())
	root := t.TempDir()
	write(t, root, "README.coyote.md", "---\ncoyote: 1\nrepo: tienda\ntype: backend\nowners: [\"@ana\"]\n---\n"+
		"purpose|API de pedidos y pagos de la tienda\nrun|make run\ntest|make test\n"+
		"mod|pagos|captura y reembolsos|src/pagos|Gateway\nmod|envios|cotiza y rastrea envíos|src/envios\n"+
		"docs|docs/dominio.md|modelo de dominio\ndocs|docs/privado.md|notas internas\n")
	write(t, root, "CONTEXT.coyote.md", "---\ncoyote: 1\nrepo: tienda\n---\n"+
		"inv|pagos|un pedido se confirma solo con un pago capturado|docs/dominio.md\n"+
		"inv|envios|un envío no sale sin dirección validada|-\n"+
		"dec|pagos/reembolsos|los reembolsos pasan por el contexto de pagos|ADR-0002\n"+
		"gap|pagos|el sandbox responde 200 aun con tarjeta rechazada|-\n"+
		"term|general|SKU es la unidad vendible|-\n")
	write(t, root, "coyote/decisions/ADR-0002-reembolsos.md", "---\nstatus: accepted\n---\n# ADR-0002: Reembolsos centralizados\n\n## Contexto y problema\nLos reembolsos se hacían desde varios servicios.\n\n## Decisión\nTodo reembolso pasa por el servicio de pagos con idempotencia.\n")
	write(t, root, "docs/dominio.md", "# Dominio\n\n## Pedido\nUn pedido agrupa líneas de SKU y se confirma con el pago.\n\n## Envío\nEl envío se cotiza con la dirección validada.\n")
	write(t, root, "docs/privado.md", "# Privado\n\nnotas que no se indexan: contraseña de pruebas\n")
	write(t, root, ".coyoteignore", "# exclusiones\ndocs/privado.md\n")
	write(t, root, "coyote/ledger/2026/09/28-ana.ccf", "# ts|actor|project|repo|type|scope|what|refs|tokens in/cache/out|cost in+out|status\n"+
		"2026-09-28T01:00Z|@ana|-|tienda|feat|pagos|captura con reintentos|-|-|-|ok\n"+
		"2026-09-28T02:00Z|@ana/coyote-dev|W-0001|tienda|fix|envios|dirección obligatoria|-|1k/0/100|$0.001+$0.001|ok\n"+
		"línea inválida\n")
	return root
}

func TestTokens(t *testing.T) {
	got := strings.Join(Tokens("Las Decisiones de los pedidos y los envíos, luces y papeles"), " ")
	if got != "decision pedido envio luz papel" {
		t.Fatalf("Tokens = %q", got)
	}
}

func TestBuildSearchAndCache(t *testing.T) {
	root := project(t)
	ix, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, c := range ix.Chunks {
		kinds[c.Kind]++
		if strings.Contains(c.Text, "contraseña") {
			t.Fatal(".coyoteignore no se respetó")
		}
	}
	if kinds[KindContext] != 5 || kinds[KindReadme] != 7 || kinds[KindADR] != 2 || kinds[KindDoc] != 2 || kinds[KindEvent] != 2 {
		t.Fatalf("fragmentos por clase inesperados: %v", kinds)
	}
	hits := ix.Search("pago capturado para confirmar pedidos", SearchOptions{Limit: 3})
	if len(hits) == 0 || hits[0].Kind != KindContext || hits[0].Type != "inv" {
		t.Fatalf("la invariante debe ser el primer resultado: %+v", hits)
	}
	if hits[0].Ref() != "CONTEXT.coyote.md#L5" {
		t.Fatalf("referencia inesperada %s", hits[0].Ref())
	}
	if h := ix.Search("reembolsos", SearchOptions{Kinds: []string{KindADR}}); len(h) == 0 || !strings.Contains(h[0].Title, "ADR-0002") {
		t.Fatalf("no encontró el ADR: %+v", h)
	}
	if h := ix.Search("dirección", SearchOptions{Scope: "pagos"}); len(h) != 0 {
		for _, x := range h {
			if !InScope(x.Scope, "pagos") {
				t.Fatalf("el filtro de ámbito dejó pasar %+v", x)
			}
		}
	}
	// Segunda corrida: todo sale de la caché.
	ix2, err := Build(root)
	if err != nil || ix2.Parsed != 0 || ix2.Reused == 0 || len(ix2.Chunks) != len(ix.Chunks) {
		t.Fatalf("caché no reutilizada: parsed=%d reused=%d err=%v", ix2.Parsed, ix2.Reused, err)
	}
	// Un archivo cambiado se vuelve a leer, el resto no.
	time.Sleep(10 * time.Millisecond)
	write(t, root, "CONTEXT.coyote.md", "---\ncoyote: 1\nrepo: tienda\n---\ninv|pagos|un pedido se confirma solo con un pago capturado|-\n")
	ix3, err := Build(root)
	if err != nil || ix3.Parsed != 1 {
		t.Fatalf("se esperaba releer un archivo: parsed=%d err=%v", ix3.Parsed, err)
	}
}

func TestPack(t *testing.T) {
	root := project(t)
	ix, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	p := ix.Pack("tienda", PackOptions{Scope: "pagos", Budget: 2000, Now: now})
	md := p.Markdown()
	for _, want := range []string{"Propósito: API de pedidos", "un pedido se confirma solo con un pago capturado", "los reembolsos pasan por el contexto de pagos",
		"sandbox", "SKU es la unidad vendible", "ADR-0002", "captura con reintentos"} {
		if !strings.Contains(md, want) {
			t.Errorf("falta %q en el paquete:\n%s", want, md)
		}
	}
	for _, not := range []string{"dirección validada", "dirección obligatoria", "cotiza y rastrea"} {
		if strings.Contains(md, not) {
			t.Errorf("el paquete de pagos no debe incluir %q:\n%s", not, md)
		}
	}
	if p.Used > p.Budget {
		t.Fatalf("el paquete excede el presupuesto: %d > %d", p.Used, p.Budget)
	}
	small := ix.Pack("tienda", PackOptions{Budget: 120, Now: now})
	md = small.Markdown()
	if small.Used > 120 || small.Omitted == 0 {
		t.Fatalf("con presupuesto chico debe omitir: used=%d omitted=%d", small.Used, small.Omitted)
	}
	if !strings.Contains(md, "Propósito") {
		t.Fatalf("la identidad entra primero:\n%s", md)
	}
	for _, budget := range []int{100, 300, 1000} {
		for _, f := range []func(*Pack) string{(*Pack).Markdown, (*Pack).CCF} {
			q := ix.Pack("tienda", PackOptions{Scope: "pagos", Query: strings.Repeat("pagos y envíos ", 150), Budget: budget, Now: now})
			if out := f(q); tokens.Estimate(out) > budget || q.Used > budget {
				t.Errorf("con presupuesto %d la salida estima %d tokens (Used=%d)", budget, tokens.Estimate(out), q.Used)
			}
		}
	}
	ccf := p.CCF()
	if !strings.HasPrefix(ccf, "# contexto|tienda|pagos|") || !strings.Contains(ccf, "inv|pagos|") {
		t.Fatalf("formato CCF inesperado:\n%s", ccf)
	}
}

func TestInScope(t *testing.T) {
	cases := []struct {
		chunk, want string
		ok          bool
	}{
		{"pagos", "pagos", true}, {"pagos/reembolsos", "pagos", true}, {"pagos", "pagos/reembolsos", true},
		{"envios", "pagos", false}, {"general", "pagos", true}, {"", "pagos", true}, {"Envíos", "envios", true},
	}
	for _, c := range cases {
		if InScope(c.chunk, c.want) != c.ok {
			t.Errorf("InScope(%q, %q) != %v", c.chunk, c.want, c.ok)
		}
	}
}

func TestIgnoreDirectoriesAndSpecialFiles(t *testing.T) {
	root := project(t)
	write(t, root, "README.coyote.md", "---\ncoyote: 1\nrepo: tienda\ntype: backend\nowners: [\"@ana\"]\n---\npurpose|tienda\nrun|x\ntest|x\ndocs|docs|documentos\n")
	write(t, root, "docs/private/secret.md", "# Secreto\n\nclave de produccion xyzzy\n")
	for _, pattern := range []string{"docs/private", "/docs/private", "**/private", "private", "docs/private/"} {
		write(t, root, ".coyoteignore", pattern+"\n")
		ix, err := Build(root)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range ix.Chunks {
			if strings.Contains(c.Text, "xyzzy") {
				t.Fatalf(".coyoteignore %q no excluyó la carpeta", pattern)
			}
		}
	}
	// Una caché que es un symlink a /dev/zero o un archivo especial no se lee.
	if err := os.MkdirAll(filepath.Join(root, ".coyote"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(root, ".coyote", "index.json"))
	if err := os.Symlink("/dev/zero", filepath.Join(root, ".coyote", "index.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(root); err != nil {
		t.Fatal(err)
	}
	// Una caché fabricada con el mismo tamaño y fecha no contradice a git.
	_ = os.Remove(filepath.Join(root, ".coyote", "index.json"))
	if _, err := Build(root); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(root, ".coyote", "index.json"))
	forged := strings.Replace(string(data), "tienda", "TEXTO QUE NO ESTA EN GIT", 1)
	if err := os.WriteFile(filepath.Join(root, ".coyote", "index.json"), []byte(forged), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range ix.Chunks {
		if strings.Contains(c.Text, "TEXTO QUE NO ESTA EN GIT") {
			t.Fatal("la caché fabricada se usó en lugar del contenido real")
		}
	}
}
