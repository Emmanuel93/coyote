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
	want := "ok\tá ñ 🙂\n\\u001b[2J\\u000d\\u009b\\u202eevil\\u2028\\U000e0041\\xff fin\n"
	if got != want {
		t.Fatalf("salida:\n%q\nesperaba\n%q", got, want)
	}
	// Dentro de una cadena JSON, el escape sigue siendo JSON válido.
	b.Reset()
	w = newSafeWriter(&b)
	data, _ := json.Marshal(map[string]string{"x": "a\u202eb\u0085"})
	w.Write(data)
	var back map[string]string
	if err := json.Unmarshal(b.Bytes(), &back); err != nil || back["x"] != "a\u202eb\u0085" {
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
