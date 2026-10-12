//go:build !linux && !darwin

package gate

import (
	"errors"
	"os"
)

// openDir abre una carpeta para leer sus nombres. Donde no hay O_DIRECTORY, se
// mira antes que sea una carpeta: una tubería con nombre bloquearía al abrirla.
func openDir(dir string) (*os.File, error) {
	fi, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, &os.PathError{Op: "open", Path: dir, Err: errors.New("no es una carpeta")}
	}
	return os.Open(dir)
}
