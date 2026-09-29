package cli

import (
	"io"

	"github.com/Emmanuel93/coyote/internal/safetext"
)

// La salida de coyote pasa por safetext: coyote nunca escribe secuencias de
// escape propias, y lo que llega así viene de un archivo, de un repo o de un
// agente. Un nombre con escapes no borra la pantalla ni tapa un error.
func newSafeWriter(w io.Writer) *safetext.Writer { return safetext.NewWriter(w) }
