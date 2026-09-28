package workstream

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Emmanuel93/coyote/internal/fsx"
)

// Lock evita dos motores sobre el mismo workstream en esta copia del
// proyecto. En macOS y Linux es un flock: el sistema lo suelta si el proceso
// muere, así que nunca queda uno abandonado ni hay carrera al recuperarlo.
func Lock(root, id string) (func(), error) {
	if !IDRe.MatchString(id) {
		return nil, fmt.Errorf("workstream %q inválido", id)
	}
	rel := ".coyote/ws/" + id + ".lock"
	if err := fsx.NoSymlinks(root, rel); err != nil {
		return nil, err
	}
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return nil, fmt.Errorf("el motor ya corre %s en otro proceso; espera a que termine", id)
	}
	_ = f.Truncate(0)
	fmt.Fprintf(f, "%d\n", os.Getpid())
	return func() {
		_ = unlockFile(f)
		f.Close()
	}, nil
}
