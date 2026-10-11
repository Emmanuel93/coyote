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

// TestEscapaMarcasInvisibles: las marcas que esconden texto salen con su
// código; el selector de un emoji se queda.
func TestEscapaMarcasInvisibles(t *testing.T) {
	cases := map[string]string{
		"a\u034fb":              `a\u034fb`,
		"x\ufe0fy":              `x\ufe0fy`,
		"x\U000e0101y":          `x\udb40\udd01y`,
		"\u26a0\ufe0f atención": "\u26a0\ufe0f atención",
		"1\ufe0f\u20e3":         "1\ufe0f\u20e3",
		"m\u180bn":              `m\u180bn`,
	}
	for in, want := range cases {
		if got := String(in); got != want {
			t.Errorf("String(%+q) = %+q, quería %+q", in, got, want)
		}
	}
}

// TestEmojisConSelector: el selector de un emoji cuya base es un signo o una
// letra (ℹ, ‼, 〽) se queda; el blanco del braille sale con su código.
func TestEmojisConSelector(t *testing.T) {
	for _, ok := range []string{"\u2139\ufe0f info", "\u203c\ufe0f", "\u2049\ufe0f", "\u3030\ufe0f", "\u303d\ufe0f"} {
		if got := String(ok); got != ok {
			t.Errorf("String(%+q) = %+q", ok, got)
		}
	}
	if got := String("a\u2800b"); got != `a\u2800b` {
		t.Errorf("el blanco del braille: %+q", got)
	}
}
