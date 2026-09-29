package safetext

import (
	"bytes"
	"testing"
	"unicode/utf8"
)

// FuzzEscape: la salida es UTF-8 válido, sin runas inseguras, y el Writer da
// lo mismo aunque el texto llegue partido en cualquier byte.
func FuzzEscape(f *testing.F) {
	for _, s := range []string{"hola", "a\u202eb", "\x1b[31m", "\xff\xfe", "👩\u200d💻", "\U000E0041", "a\u2028b", "\r\n\t"} {
		f.Add([]byte(s), 1)
	}
	f.Fuzz(func(t *testing.T, b []byte, cut int) {
		out := Escape(b)
		if !utf8.Valid(out) {
			t.Fatalf("salida inválida para %q", b)
		}
		for _, r := range string(out) {
			if Unsafe(r) {
				t.Fatalf("quedó %U en la salida de %q", r, b)
			}
		}
		if len(b) == 0 {
			return
		}
		if cut < 0 {
			cut = -cut
		}
		cut %= len(b) + 1
		var buf bytes.Buffer
		w := NewWriter(&buf)
		_, _ = w.Write(b[:cut])
		_, _ = w.Write(b[cut:])
		_ = w.Flush()
		if !bytes.Equal(buf.Bytes(), out) {
			t.Fatalf("partido en %d: %q, entero: %q", cut, buf.Bytes(), out)
		}
	})
}
