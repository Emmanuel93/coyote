// Package fsx reúne salvaguardas de escritura: coyote nunca escribe a través de
// un symlink dentro del proyecto, así un repo ajeno no puede hacerle escribir
// fuera de su carpeta.
package fsx

import (
	"fmt"
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
