package product

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/Emmanuel93/coyote/internal/fsx"
)

// AddedLine es una línea que agrega un cambio, con su número en el lado nuevo.
type AddedLine struct {
	Line int
	Text string
}

// AddedFile son las líneas que un cambio agrega a un archivo. Whole marca un
// archivo que git lista sin hunks (binario, vacío o con -diff en
// .gitattributes): hay que leerlo completo en la revisión nueva.
type AddedFile struct {
	Path   string
	Status byte // A, M, T…
	Lines  []AddedLine
	Whole  bool
}

// ParseAdded lee un parche con --unified=0 y devuelve las líneas agregadas por
// archivo, en el orden en que aparecen. Cuenta las líneas de cada hunk: una
// línea agregada que empieza con "++ " se ve en el parche como "+++ " y no es
// el encabezado de otro archivo. Lee líneas de cualquier largo.
func ParseAdded(r io.Reader) (map[string][]AddedLine, error) {
	out := map[string][]AddedLine{}
	br := bufio.NewReaderSize(r, 1<<16)
	name, next := "", 0
	oldLeft, newLeft := 0, 0
	for {
		l, err := br.ReadString('\n')
		if l != "" {
			l = strings.TrimSuffix(l, "\n")
			inHunk := oldLeft > 0 || newLeft > 0
			switch {
			case strings.HasPrefix(l, "\\"):
				// "\ No newline at end of file": no es una línea del archivo.
			case inHunk && strings.HasPrefix(l, "+"):
				if name != "" {
					out[name] = append(out[name], AddedLine{Line: next, Text: l[1:]})
				}
				next++
				newLeft--
			case inHunk && strings.HasPrefix(l, "-"):
				oldLeft--
			case inHunk && strings.HasPrefix(l, " "):
				oldLeft--
				newLeft--
				next++
			case strings.HasPrefix(l, "diff --git "):
				name, next, oldLeft, newLeft = "", 0, 0, 0
			case strings.HasPrefix(l, "+++ "):
				name = diffName(l[4:])
			case strings.HasPrefix(l, "@@"):
				m := hunkRe.FindStringSubmatch(l)
				if m == nil {
					break
				}
				next, _ = strconv.Atoi(m[3])
				oldLeft, newLeft = 1, 1
				if m[2] != "" {
					oldLeft, _ = strconv.Atoi(m[2])
				}
				if m[4] != "" {
					newLeft, _ = strconv.Atoi(m[4])
				}
			}
		}
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return out, err
		}
	}
}

// safeGitPath rechaza rutas que git leería como opción o fuera de la raíz del
// repo: las que empiezan con - o /, y las que tienen un componente vacío, .
// o .. (dos..puntos.txt sí es un nombre válido).
func safeGitPath(p string) bool {
	if p == "" || strings.HasPrefix(p, "-") || strings.HasPrefix(p, "/") {
		return false
	}
	for _, c := range strings.Split(p, "/") {
		if c == "" || c == "." || c == ".." {
			return false
		}
	}
	return true
}

// GitPrefix devuelve la raíz del repo git que contiene dir y la carpeta dir
// relativa a esa raíz, con barra al final ("" si dir es la raíz). Las rutas
// de git son relativas a la raíz; las reglas de un proyecto, a su carpeta.
func GitPrefix(dir string) (top, prefix string, err error) {
	var stdout, stderr bytes.Buffer
	cmd := gitRead(dir, "rev-parse", "--show-toplevel", "--show-prefix")
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", "", fmt.Errorf("git rev-parse: %v %s", err, strings.TrimSpace(stderr.String()))
	}
	lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return "", "", fmt.Errorf("git rev-parse no dio la raíz del repo")
	}
	if len(lines) > 1 {
		prefix = lines[1]
	}
	return lines[0], prefix, nil
}

// AddedText devuelve lo que agrega un rango de git (base...head o base..head)
// de un repo del producto, leído con plomería: nunca corre filtros, hooks ni
// diff externos y nunca escribe en el repo.
func AddedText(dir, diff string) ([]AddedFile, string, error) {
	names, err := diffNames(dir, diff)
	if err != nil {
		return nil, "", err
	}
	left, right, worktree := diffSides(dir, diff)
	if worktree {
		return nil, "", fmt.Errorf("el rango %q compara contra el árbol de trabajo; usa base...head", diff)
	}
	cmd := gitRead(dir, "-c", "core.quotePath=false", "diff-tree", "-r", "-p", "--no-color", "--no-ext-diff", "--no-textconv",
		"--no-renames", "--unified=0", "--ignore-submodules=all", left, right, "--")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, "", fmt.Errorf("git diff %s: %v %s", diff, err, strings.TrimSpace(stderr.String()))
	}
	lines, err := ParseAdded(&stdout)
	if err != nil {
		return nil, "", err
	}
	var out []AddedFile
	for _, n := range names {
		if n.status == 'D' {
			continue
		}
		f := AddedFile{Path: n.path, Status: n.status, Lines: lines[n.path]}
		// Lo que git lista sin hunks se lee completo en la revisión nueva.
		f.Whole = len(f.Lines) == 0
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, right, nil
}

// TrackedFiles lista los archivos versionados de un repo, con plomería.
func TrackedFiles(dir string) ([]string, error) {
	var stdout, stderr bytes.Buffer
	cmd := gitRead(dir, "ls-files", "-z", "--cached")
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git ls-files: %v %s", err, strings.TrimSpace(stderr.String()))
	}
	var out []string
	for _, f := range strings.Split(stdout.String(), "\x00") {
		if f != "" {
			out = append(out, f)
		}
	}
	return out, nil
}

// emptyTree es el árbol vacío de git: la base de un repo sin commits.
const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// StagedAdded devuelve lo que agrega el índice contra HEAD (lo preparado para
// el commit). Con worktree, compara el árbol de trabajo de los archivos
// versionados, como git commit -a. Lee con plomería.
func StagedAdded(dir string, worktree bool) ([]AddedFile, error) {
	base := "HEAD"
	if err := gitRead(dir, "rev-parse", "--verify", "-q", "HEAD").Run(); err != nil {
		base = emptyTree
	}
	mode := []string{"--cached"}
	if worktree {
		mode = nil
	}
	namesArgs := append(append([]string{"diff-index", "-z", "--name-status", "--no-renames", "--ignore-submodules=all"}, mode...), base, "--")
	var nameOut, stderr bytes.Buffer
	cmd := gitRead(dir, namesArgs...)
	if worktree {
		c, err := gitWorktree(dir, namesArgs...)
		if err != nil {
			return nil, err
		}
		cmd = c
	}
	cmd.Stdout, cmd.Stderr = &nameOut, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git diff-index: %v %s", err, strings.TrimSpace(stderr.String()))
	}
	patchArgs := append(append([]string{"-c", "core.quotePath=false", "diff-index", "-p", "--no-color", "--no-ext-diff", "--no-textconv",
		"--no-renames", "--unified=0", "--ignore-submodules=all"}, mode...), base, "--")
	var patch bytes.Buffer
	stderr.Reset()
	cmd = gitRead(dir, patchArgs...)
	if worktree {
		c, err := gitWorktree(dir, patchArgs...)
		if err != nil {
			return nil, err
		}
		cmd = c
	}
	cmd.Stdout, cmd.Stderr = &patch, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git diff-index: %v %s", err, strings.TrimSpace(stderr.String()))
	}
	lines, err := ParseAdded(&patch)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(nameOut.String(), "\x00")
	var out []AddedFile
	for i := 0; i+1 < len(parts); i += 2 {
		if parts[i] == "" || parts[i+1] == "" || parts[i][0] == 'D' {
			continue
		}
		f := AddedFile{Path: parts[i+1], Status: parts[i][0], Lines: lines[parts[i+1]]}
		f.Whole = len(f.Lines) == 0
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// IndexText lee un archivo de texto tal como está en el índice (":ruta",
// relativa a la raíz del repo).
func IndexText(dir, path string) (string, bool) {
	if !safeGitPath(path) {
		return "", false
	}
	cmd := gitRead(dir, "cat-file", "blob", ":"+path)
	var out bytes.Buffer
	cmd.Stdout = &out
	if cmd.Run() != nil || out.Len() > maxFile {
		return "", false
	}
	return fsx.Text(out.Bytes())
}

// WorktreeText lee un archivo de texto del árbol de trabajo, sin seguir symlinks.
func WorktreeText(dir, path string) (string, bool) {
	if !safeGitPath(path) {
		return "", false
	}
	return fileText(dir, "", path)
}
