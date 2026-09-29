package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestSalidaSinEscapesDeTerminal(t *testing.T) {
	var b bytes.Buffer
	w := newSafeWriter(&b)
	in := "ok\tá ñ 🙂\n\x1b[2J\r\u009b\u202eevil\u2028\U000e0041\xff fin\n"
	// La runa de 4 bytes del emoji se parte entre dos escrituras.
	cut := strings.Index(in, "🙂") + 2
	w.Write([]byte(in[:cut]))
	w.Write([]byte(in[cut:]))
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	want := "ok\tá ñ 🙂\n\\u001b[2J\\u000d\\u009b\\u202eevil\\u2028\\udb40\\udc41\\xff fin\n"
	if got != want {
		t.Fatalf("salida:\n%q\nesperaba\n%q", got, want)
	}
	// Los que unen caracteres (emojis, persa) pasan tal cual.
	b.Reset()
	w = newSafeWriter(&b)
	w.Write([]byte("👩\u200d💻 می\u200cخواهم"))
	if b.String() != "👩\u200d💻 می\u200cخواهم" {
		t.Fatalf("ZWJ y ZWNJ pasan: %q", b.String())
	}
	// Dentro de una cadena JSON, el escape sigue siendo JSON válido.
	b.Reset()
	w = newSafeWriter(&b)
	data, _ := json.Marshal(map[string]string{"x": "a\u202eb\u0085\U000e0041"})
	w.Write(data)
	var back map[string]string
	if err := json.Unmarshal(b.Bytes(), &back); err != nil || back["x"] != "a\u202eb\u0085\U000e0041" {
		t.Fatalf("JSON: %v %q %q", err, b.String(), back["x"])
	}
}

func TestSLOCheckNoEscribeEscapes(t *testing.T) {
	base := setup(t)
	root := base + "/tienda"
	must(t, run(t, base, "", "init", "tienda", "--type", "backend", "--purpose", "API de la tienda demo"), 0, "init")
	write(t, root, "coyote/slo/pedidos-service.yaml", sloPedidos+"\"\\r\\e[2K\\e[32mSLOs válidos\\u202e\": 1\n")
	r := run(t, root, "", "slo", "check")
	if r.code == 0 || strings.ContainsAny(r.stdout+r.stderr, "\x1b\r\u202e") || !strings.Contains(r.stdout+r.stderr, "\\u001b") {
		t.Fatalf("slo check escribe los escapes como texto:\n%q", r.stdout+r.stderr)
	}
}

func TestMarkdownDelPRSinEnlacesNiInvisibles(t *testing.T) {
	got := mdText("ver https://evil.example y www.evil.example, #12, @org/sre, &commat;org y a\u202eb")
	for _, bad := range []string{"https://", "www.", "#12", "@org", "&commat;", "\u202e", "\u200b"} {
		if strings.Contains(got, bad) {
			t.Errorf("mdText deja %q: %s", bad, got)
		}
	}
	if !strings.Contains(got, "&amp;commat;") || !strings.Contains(got, `\u202e`) || !strings.Contains(got, "@&#8203;org") {
		t.Errorf("mdText: %s", got)
	}
	if c := codeCell("x\u202e.yaml`|"); strings.ContainsAny(c, "\u202e`|") {
		t.Errorf("codeCell: %q", c)
	}
}
