package product

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
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
// archivo, en el orden en que aparecen.
func ParseAdded(r io.Reader) (map[string][]AddedLine, error) {
	out := map[string][]AddedLine{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	name, next := "", 0
	for sc.Scan() {
		l := sc.Text()
		switch {
		case strings.HasPrefix(l, "diff --git "):
			name, next = "", 0
		case strings.HasPrefix(l, "+++ "):
			name = diffName(l[4:])
		case strings.HasPrefix(l, "--- "):
		case strings.HasPrefix(l, "@@"):
			m := hunkRe.FindStringSubmatch(l)
			if m == nil {
				continue
			}
			next, _ = strconv.Atoi(m[3])
		case strings.HasPrefix(l, "+") && name != "":
			out[name] = append(out[name], AddedLine{Line: next, Text: l[1:]})
			next++
		}
	}
	return out, sc.Err()
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

// IndexText lee un archivo de texto tal como está en el índice (":ruta").
func IndexText(dir, path string) (string, bool) {
	if strings.HasPrefix(path, "-") || strings.Contains(path, "..") {
		return "", false
	}
	cmd := gitRead(dir, "cat-file", "blob", ":"+path)
	var out bytes.Buffer
	cmd.Stdout = &out
	if cmd.Run() != nil || out.Len() > maxFile || bytes.IndexByte(out.Bytes(), 0) >= 0 {
		return "", false
	}
	return out.String(), true
}

// WorktreeText lee un archivo de texto del árbol de trabajo, sin seguir symlinks.
func WorktreeText(dir, path string) (string, bool) {
	if strings.HasPrefix(path, "-") || strings.Contains(path, "..") {
		return "", false
	}
	return fileText(dir, "", path)
}
