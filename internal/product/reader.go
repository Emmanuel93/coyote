package product

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// reader lee los repos durante un análisis de impacto: guarda la lista de
// archivos de cada repo y trae las versiones de git en lote, con un solo
// proceso de git por revisión, en lugar de uno por archivo.
type reader struct {
	lists map[string][]string          // carpeta → archivos
	blobs map[string]map[string]string // carpeta|revisión → ruta → texto
}

func newReader() *reader {
	return &reader{lists: map[string][]string{}, blobs: map[string]map[string]string{}}
}

// files lista los archivos de un repo una sola vez por análisis.
func (r *reader) files(dir string) []string {
	if r == nil {
		list, _, _ := files(dir)
		return list
	}
	if l, ok := r.lists[dir]; ok {
		return l
	}
	list, _, err := files(dir)
	if err != nil {
		list = nil
	}
	r.lists[dir] = list
	return list
}

// moduleFiles lista los archivos de un módulo con la misma lista que el mapa
// (git, con lo nuevo sin commit), para que el análisis vea lo mismo que el mapa.
func (r *reader) moduleFiles(dir, mod string) []string {
	list := r.files(dir)
	if mod == "." || mod == "" {
		return list
	}
	var out []string
	for _, f := range list {
		if strings.HasPrefix(f, mod+"/") {
			out = append(out, f)
		}
	}
	return out
}

// prefetch trae de git, en un solo proceso, las versiones de varios archivos
// en una revisión. Lo que no existe en esa revisión queda como ausente.
func (r *reader) prefetch(dir, rev string, paths []string) {
	if r == nil || rev == "" || len(paths) == 0 {
		return
	}
	key := dir + "|" + rev
	cache := r.blobs[key]
	if cache == nil {
		cache = map[string]string{}
		r.blobs[key] = cache
	}
	var want []string
	for _, p := range paths {
		if _, ok := cache[p]; !ok && !strings.ContainsAny(p, "\n\r") {
			want = append(want, p)
		}
	}
	if len(want) == 0 {
		return
	}
	var in bytes.Buffer
	for _, p := range want {
		fmt.Fprintf(&in, "%s:%s\n", rev, p)
	}
	cmd := gitRead(dir, "cat-file", "--batch")
	cmd.Stdin = &in
	out, err := cmd.StdoutPipe()
	if err != nil {
		return
	}
	if err := cmd.Start(); err != nil {
		return
	}
	br := bufio.NewReaderSize(out, 1<<16)
	for _, p := range want {
		header, err := br.ReadString('\n')
		if err != nil {
			break
		}
		f := strings.Fields(header)
		if len(f) != 3 || f[1] != "blob" {
			cache[p] = "\x00" // ausente o no es un archivo
			continue
		}
		size, err := strconv.Atoi(f[2])
		if err != nil || size < 0 {
			break
		}
		data := make([]byte, size)
		if _, err := io.ReadFull(br, data); err != nil {
			break
		}
		_, _ = br.ReadByte() // el salto de línea que sigue al contenido
		if size > maxFile || bytes.IndexByte(data, 0) >= 0 {
			cache[p] = "\x00"
			continue
		}
		cache[p] = string(data)
	}
	_, _ = io.Copy(io.Discard, br)
	_ = cmd.Wait()
}

// text lee un archivo en una revisión (del lote, si se trajo) o del árbol de
// trabajo si rev está vacía.
func (r *reader) text(dir, rev, p string) (string, bool) {
	if rev == "" {
		data, ok := readRegular(dir, p)
		return string(data), ok
	}
	if r != nil {
		if t, ok := r.blobs[dir+"|"+rev][p]; ok {
			return t, t != "\x00"
		}
	}
	return fileText(dir, rev, p)
}
