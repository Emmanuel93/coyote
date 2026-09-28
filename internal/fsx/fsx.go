// Package fsx reúne salvaguardas de escritura: coyote nunca escribe a través de
// un symlink dentro del proyecto, así un repo ajeno no puede hacerle escribir
// fuera de su carpeta.
package fsx

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// NoSymlinks verifica que rel quede dentro de root y que ninguno de sus
// componentes existentes sea un symlink. Lo que todavía no existe se creará
// como archivo o carpeta normal.
func NoSymlinks(root, rel string) error {
	rel = filepath.Clean(filepath.FromSlash(rel))
	if filepath.IsAbs(rel) {
		r, err := filepath.Rel(root, rel)
		if err != nil {
			return err
		}
		rel = r
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s queda fuera del proyecto", rel)
	}
	cur := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			r, _ := filepath.Rel(root, cur)
			return fmt.Errorf("%s es un symlink; coyote no escribe a través de symlinks", filepath.ToSlash(r))
		}
	}
	return nil
}

// ReadFile lee rel dentro de root solo si es un archivo regular, sin symlinks
// en el camino y de hasta max bytes: un FIFO, /dev/zero o un enlace a otro
// lugar nunca se leen.
func ReadFile(root, rel string, max int64) ([]byte, error) {
	if err := NoSymlinks(root, rel); err != nil {
		return nil, err
	}
	p := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s no es un archivo regular", rel)
	}
	if info.Size() > max {
		return nil, fmt.Errorf("%s pesa %d bytes; máximo %d", rel, info.Size(), max)
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, max+1))
}

// Regular informa si rel es un archivo regular dentro de root, sin symlinks.
func Regular(root, rel string) bool {
	if NoSymlinks(root, rel) != nil {
		return false
	}
	info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil && info.Mode().IsRegular()
}
