//go:build !unix

package workstream

import (
	"errors"
	"os"
	"sync"
)

// Sin flock, el lock vale dentro de este proceso.
var (
	mu     sync.Mutex
	locked = map[string]bool{}
)

func lockFile(f *os.File) error {
	mu.Lock()
	defer mu.Unlock()
	if locked[f.Name()] {
		return errors.New("ocupado")
	}
	locked[f.Name()] = true
	return nil
}

func unlockFile(f *os.File) error {
	mu.Lock()
	defer mu.Unlock()
	delete(locked, f.Name())
	return nil
}
