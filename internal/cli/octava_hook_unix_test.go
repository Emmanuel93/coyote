//go:build unix

package cli

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestFileContentsNoAbreFIFONiDispositivos: un FIFO sin quien lo escriba colgaba
// el open del hook (hasta que el IDE lo mataba y dejaba pasar); /dev/zero no se
// acaba. Solo se leen archivos regulares.
func TestFileContentsNoAbreFIFONiDispositivos(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "mensaje")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip("no hay mkfifo:", err)
	}
	for _, p := range []string{fifo, "/dev/zero"} {
		done := make(chan string, 1)
		go func() { done <- fileContents("git commit -F "+p, "", 1<<20) }()
		select {
		case got := <-done:
			if got != "" {
				t.Errorf("%s: no es un archivo regular y no se lee, pero devolvió %d bytes", p, len(got))
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("%s: fileContents se colgó", p)
		}
	}
}
