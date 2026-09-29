// Package safetext deja un texto sin caracteres que un terminal o un
// navegador interpretan en vez de mostrar: controles, separadores de línea
// Unicode y caracteres de formato invisibles, como los que cambian la
// dirección del texto o los de etiqueta, que esconden texto. Se escriben
// como su código (\u202e), que además es un escape válido dentro de una
// cadena JSON: fuera del plano básico, como par sustituto (\udb40\udc41).
package safetext

import (
	"fmt"
	"io"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// Unsafe dice si una runa no se escribe tal cual. El salto de línea y el
// tabulador sí, y también los que unen caracteres (U+200C y U+200D), que
// arman emojis y palabras en persa o en lenguas índicas.
func Unsafe(r rune) bool {
	switch {
	case r == '\n' || r == '\t':
		return false
	case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
		return true
	case r == 0x2028 || r == 0x2029:
		return true
	case r == 0x200c || r == 0x200d:
		return false
	}
	return unicode.Is(unicode.Cf, r)
}

// Escape devuelve los bytes con cada runa insegura escrita como su código y
// cada byte que no es UTF-8 válido como \xNN.
func Escape(b []byte) []byte {
	clean := true
	for i := 0; i < len(b); {
		r, n := utf8.DecodeRune(b[i:])
		if (r == utf8.RuneError && n == 1) || Unsafe(r) {
			clean = false
			break
		}
		i += n
	}
	if clean {
		return b
	}
	out := make([]byte, 0, len(b)+16)
	for i := 0; i < len(b); {
		r, n := utf8.DecodeRune(b[i:])
		switch {
		case r == utf8.RuneError && n == 1:
			out = fmt.Appendf(out, "\\x%02x", b[i])
		case Unsafe(r) && r > 0xffff:
			hi, lo := utf16.EncodeRune(r)
			out = fmt.Appendf(out, "\\u%04x\\u%04x", hi, lo)
		case Unsafe(r):
			out = fmt.Appendf(out, "\\u%04x", r)
		default:
			out = append(out, b[i:i+n]...)
		}
		i += n
	}
	return out
}

// String es Escape para un texto.
func String(s string) string { return string(Escape([]byte(s))) }

// Writer escapa lo que escribe. Una runa partida entre dos escrituras espera
// a la siguiente; Flush escribe lo que quedó.
type Writer struct {
	w       io.Writer
	pending []byte
}

// NewWriter envuelve w.
func NewWriter(w io.Writer) *Writer { return &Writer{w: w} }

func (s *Writer) Write(p []byte) (int, error) {
	data := append(s.pending, p...)
	s.pending = nil
	cut := len(data)
	for i := 1; i < utf8.UTFMax && i <= len(data); i++ {
		if utf8.RuneStart(data[len(data)-i]) {
			if !utf8.FullRune(data[len(data)-i:]) {
				cut = len(data) - i
			}
			break
		}
	}
	s.pending = append([]byte(nil), data[cut:]...)
	if _, err := s.w.Write(Escape(data[:cut])); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Flush escribe lo pendiente, escapado como bytes sueltos.
func (s *Writer) Flush() error {
	if len(s.pending) == 0 {
		return nil
	}
	data := s.pending
	s.pending = nil
	_, err := s.w.Write(Escape(data))
	return err
}
