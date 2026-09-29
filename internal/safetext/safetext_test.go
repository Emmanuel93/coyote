package safetext

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestEscape(t *testing.T) {
	cases := map[string]string{
		"hola, ñandú 🙂\tfin\n":   "hola, ñandú 🙂\tfin\n",
		"\x1b[2Jborra":           "\\u001b[2Jborra",
		"a\u202eb\u2066c":        "a\\u202eb\\u2066c",
		"línea\u2028otra":        "línea\\u2028otra",
		"👩\u200d💻 می\u200cخواهم": "👩\u200d💻 می\u200cخواهم",
		"etiqueta\U000e0041":     "etiqueta\\udb40\\udc41",
		"suave\u00ad":            "suave\\u00ad",
		"byte \xff suelto":       "byte \\xff suelto",
	}
	for in, want := range cases {
		if got := String(in); got != want {
			t.Errorf("String(%q) = %q, se esperaba %q", in, got, want)
		}
	}
}

func TestWriterPartido(t *testing.T) {
	in := []byte("x\u202e🙂y\U000e0041z")
	for cut := 0; cut <= len(in); cut++ {
		var b bytes.Buffer
		w := NewWriter(&b)
		w.Write(in[:cut])
		w.Write(in[cut:])
		if err := w.Flush(); err != nil {
			t.Fatal(err)
		}
		if b.String() != String(string(in)) {
			t.Fatalf("corte %d: %q", cut, b.String())
		}
	}
	// El escape de un JSON sigue siendo JSON válido y dice lo mismo.
	data, _ := json.Marshal(map[string]string{"t": "a\u202eb\U000e0041"})
	var back map[string]string
	if err := json.Unmarshal(Escape(data), &back); err != nil || back["t"] != "a\u202eb\U000e0041" {
		t.Fatalf("JSON: %v %q", err, back["t"])
	}
}
