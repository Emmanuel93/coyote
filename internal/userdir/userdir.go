// Package userdir ubica el estado de coyote de esta persona en esta máquina,
// fuera de cualquier repo: ritmo por cuenta y claves locales.
package userdir

import (
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
)

// StateDir es el directorio de estado (COYOTE_STATE_DIR o el de configuración del usuario).
func StateDir() (string, error) {
	if d := os.Getenv("COYOTE_STATE_DIR"); d != "" {
		return d, nil
	}
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "coyote"), nil
}

// Key devuelve una clave local de 32 bytes, creada la primera vez con permisos
// 0600. Sirve para firmar cachés: una caché copiada de otra máquina o
// fabricada dentro de un repo no valida.
func Key(name string) ([]byte, error) {
	dir, err := StateDir()
	if err != nil {
		return nil, err
	}
	p := filepath.Join(dir, name+".key")
	if data, err := os.ReadFile(p); err == nil && len(data) == 32 {
		return data, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) { // otro proceso la creó primero
			if data, err := os.ReadFile(p); err == nil && len(data) == 32 {
				return data, nil
			}
		}
		return nil, err
	}
	defer f.Close()
	if _, err := f.Write(key); err != nil {
		return nil, err
	}
	return key, nil
}
