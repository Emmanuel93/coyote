package golden

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Emmanuel93/coyote/internal/index"
)

func write(t *testing.T, text string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "golden.jsonl")
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoad(t *testing.T) {
	qs, err := Load(write(t, "# comentario\n\n{\"id\":\"g1\",\"q\":\"¿cómo se aprueba?\",\"expect\":[\"doc:docs/gate.md\",\"adr:ADR-0009\"]}\n{\"id\":\"g2\",\"q\":\"x\",\"expect\":[\"ctx:pagos\"],\"k\":3}\n"))
	if err != nil || len(qs) != 2 || qs[0].K != 5 || qs[1].K != 3 {
		t.Fatalf("Load: %v %+v", err, qs)
	}
	for text, want := range map[string]string{
		`{"id":"g1","q":"x","expect":["doc:a"],"extra":1}`:                                         "unknown field",
		`{"id":"","q":"x","expect":["doc:a"]}`:                                                     "necesita id",
		`{"id":"g1","q":"x","expect":[]}`:                                                          "necesita id",
		`{"id":"g1","q":"x","expect":["web:a"]}`:                                                   "referencia",
		`{"id":"g1","q":"x","expect":["doc:a"]}` + "\n" + `{"id":"g1","q":"y","expect":["doc:b"]}`: "repetido",
	} {
		if _, err := Load(write(t, text)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: se esperaba %q, hubo %v", text, want, err)
		}
	}
}

func TestMatches(t *testing.T) {
	adr := index.Chunk{Kind: index.KindADR, Path: "coyote/decisions/ADR-0009-gate-por-hash.md"}
	doc := index.Chunk{Kind: index.KindDoc, Path: "docs/specs/gate-v1.md"}
	ctx := index.Chunk{Kind: index.KindContext, Scope: "pagos/cobro", Path: "CONTEXT.coyote.md"}
	for _, c := range []struct {
		c    index.Chunk
		ref  string
		want bool
	}{
		{adr, "adr:ADR-0009", true}, {adr, "adr:ADR-0010", false}, {doc, "adr:ADR-0009", false},
		{doc, "doc:docs/specs/gate-v1.md", true}, {doc, "doc:docs/specs/gate", false},
		{ctx, "ctx:pagos", true}, {ctx, "ctx:pedidos", false}, {doc, "ctx:pagos", false},
	} {
		if Matches(c.c, c.ref) != c.want {
			t.Errorf("Matches(%s, %s) = %v", c.c.Path, c.ref, !c.want)
		}
	}
}

func TestRegression(t *testing.T) {
	base := Summary{Recall: 80, MRR: 70, Coverage: 90}
	if r := Regression(base, Summary{Recall: 78.1, MRR: 68, Coverage: 95}, 2); len(r) != 0 {
		t.Errorf("dentro de la tolerancia: %v", r)
	}
	if r := Regression(base, Summary{Recall: 77.9, MRR: 70, Coverage: 80}, 2); len(r) != 2 {
		t.Errorf("dos métricas bajaron más de 2 puntos: %v", r)
	}
}
