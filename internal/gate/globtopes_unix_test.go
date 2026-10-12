//go:build linux || darwin

package gate

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestComodinSoloAbreCarpetas: séptima revisión de v1.0 (fuzzing del gate). Un
// comodín alcanza archivos de todo tipo y el gate solo debe leer carpetas.
// Abrir una tubería con nombre bloquea hasta que alguien escribe al otro lado,
// y un gate colgado deja pasar la acción cuando el IDE corta el hook por
// tiempo; abrir un terminal o un dispositivo puede tener efectos. El fuzzing
// encontró el cuelgue con `/*/*/…`, que pasa por /dev y /proc.
func TestComodinSoloAbreCarpetas(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "a", "tuberia"), 0o600); err != nil {
		t.Skipf("no se puede crear una tubería con nombre: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a", "archivo"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	done := make(chan []string, 1)
	go func() {
		m, _ := globAll(dir + "/*/*/x*")
		done <- m
	}()
	select {
	case got := <-done:
		if len(got) != 0 {
			t.Errorf("no hay nada que alcanzar bajo un archivo: %v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("el gate se colgó al abrir una tubería con nombre que el comodín alcanzó")
	}
}
