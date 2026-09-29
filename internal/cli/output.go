package cli

import (
	"io"
	"os"

	"github.com/Emmanuel93/coyote/internal/safetext"
	"github.com/Emmanuel93/coyote/internal/tty"
)

// La salida de coyote pasa por safetext: coyote nunca escribe secuencias de
// escape propias, y lo que llega así viene de un archivo, de un repo o de un
// agente. Un nombre con escapes no borra la pantalla ni tapa un error.
func newSafeWriter(w io.Writer) *safetext.Writer { return safetext.NewWriter(w) }

// contentOut es la salida de un contenido pensado para un archivo (coyote slo
// rules --stdout > reglas.yaml): va tal cual cuando la salida no es una
// terminal, para que los bytes sean los mismos que escribe coyote, y
// escapado cuando alguien lo mira en la terminal.
func (a *app) contentOut() io.Writer {
	if f, ok := a.rawOut.(*os.File); ok && tty.IsTerminal(f.Fd()) {
		return a.stdout
	}
	if a.rawOut == nil {
		return a.stdout
	}
	return a.rawOut
}
