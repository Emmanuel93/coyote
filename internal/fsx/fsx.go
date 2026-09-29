// Package fsx reúne salvaguardas de escritura: coyote nunca escribe a través de
// un symlink dentro del proyecto, así un repo ajeno no puede hacerle escribir
// fuera de su carpeta.
package fsx

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
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

// ReadCapped lee un archivo que puede ser un symlink, pero solo si lo que
// alcanza es un archivo regular de hasta max bytes: un symlink a /dev/zero o
// a un FIFO no se lee. Sirve para los archivos de un repo ajeno, donde un
// symlink es legítimo; lo que coyote escribe se lee con ReadFile.
func ReadCapped(path string, max int64) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s no es un archivo regular", filepath.Base(path))
	}
	if info.Size() > max {
		return nil, fmt.Errorf("%s pesa %d bytes; máximo %d", filepath.Base(path), info.Size(), max)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%s creció a más de %d bytes al leerlo", filepath.Base(path), max)
	}
	return data, nil
}

// Regular informa si rel es un archivo regular dentro de root, sin symlinks.
func Regular(root, rel string) bool {
	if NoSymlinks(root, rel) != nil {
		return false
	}
	info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil && info.Mode().IsRegular()
}

// WriteAtomic escribe path completo o no lo toca: escribe un temporal con
// nombre al azar, creado en exclusiva en la misma carpeta, y lo renombra. Un
// temporal con nombre fijo se podría plantar antes como symlink y hacer que
// la escritura salga de la carpeta.
func WriteAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(perm); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	ok = true
	return nil
}

// Text devuelve un contenido como texto: decodifica UTF-16 con BOM (lo que
// escribe PowerShell 5) y quita el BOM de UTF-8. ok es false si parece
// binario (un byte 0 en los primeros 8000).
func Text(data []byte) (string, bool) {
	switch {
	case len(data) >= 2 && data[0] == 0xFF && data[1] == 0xFE:
		return utf16Text(data[2:], false), true
	case len(data) >= 2 && data[0] == 0xFE && data[1] == 0xFF:
		return utf16Text(data[2:], true), true
	case len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF:
		data = data[3:]
	}
	head := data
	if len(head) > 8000 {
		head = head[:8000]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return "", false
	}
	return string(data), true
}

func utf16Text(b []byte, big bool) string {
	u := make([]uint16, len(b)/2)
	for i := range u {
		if big {
			u[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
		} else {
			u[i] = uint16(b[2*i+1])<<8 | uint16(b[2*i])
		}
	}
	return string(utf16.Decode(u))
}
