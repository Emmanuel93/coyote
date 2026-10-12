//go:build linux || darwin

package gate

import (
	"os"
	"syscall"
)

// openDir abre una carpeta para leer sus nombres, y solo una carpeta: con
// O_DIRECTORY el sistema rechaza de inmediato cualquier otra cosa, sin llegar a
// abrirla. Abrir una tubería con nombre bloquea hasta que alguien escribe al
// otro lado, y abrir un terminal o un dispositivo puede tener efectos.
func openDir(dir string) (*os.File, error) {
	return os.OpenFile(dir, os.O_RDONLY|syscall.O_DIRECTORY, 0)
}
