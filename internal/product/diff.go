package product

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ---- cambios por archivos ----

type lineRange struct{ from, to int }

// fileChanges son los rangos cambiados por archivo a cada lado de un diff:
// old son líneas del commit base y new, del commit final.
type fileChanges struct {
	old, new map[string][]lineRange
}

// diffSide dice con qué lado del diff se corresponden las líneas del mapa.
type diffSide int

const (
	sideNew diffSide = iota
	sideOld
	sideBoth
)

func (c *fileChanges) pick(s diffSide) map[string][]lineRange {
	switch s {
	case sideOld:
		return c.old
	case sideNew:
		return c.new
	}
	out := map[string][]lineRange{}
	for _, m := range []map[string][]lineRange{c.old, c.new} {
		for f, r := range m {
			out[f] = append(out[f], r...)
		}
	}
	return out
}

var hunkRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// whole marca un archivo completo.
var whole = lineRange{1, 1 << 30}

// changedLines devuelve los rangos cambiados por archivo. Sin rango de git,
// cada archivo cuenta completo. Usa los comandos de plomería de git
// (diff-tree y diff-index), que nunca escriben el índice: git diff lo
// refresca y lo reescribe aunque solo se le pida leer.
//
// La lista de archivos sale de --name-status -z, que no depende de los
// atributos del árbol ni de comillas: un archivo sin hunks (binario, vacío,
// marcado con -diff en .gitattributes o con solo un cambio de modo) cuenta
// completo.
func changedLines(dir, diff string, files []string) (*fileChanges, error) {
	ch := &fileChanges{old: map[string][]lineRange{}, new: map[string][]lineRange{}}
	if diff == "" {
		for _, f := range files {
			f = filepath.ToSlash(f)
			ch.old[f] = []lineRange{whole}
			ch.new[f] = []lineRange{whole}
		}
		return ch, nil
	}
	names, err := diffNames(dir, diff)
	if err != nil {
		return nil, err
	}
	left, right, worktree := diffSides(dir, diff)
	opts := []string{"-c", "core.quotePath=false"}
	flags := []string{"-p", "--no-color", "--no-ext-diff", "--no-textconv", "--no-renames", "--unified=0"}
	var cmd *exec.Cmd
	if worktree {
		c, err := gitWorktree(dir, append(append(append(opts, "diff-index", "--ignore-submodules=all"), flags...), left, "--")...)
		if err != nil {
			return nil, err
		}
		cmd = c
	} else {
		cmd = gitRead(dir, append(append(append(opts, "diff-tree", "-r", "--ignore-submodules=all"), flags...), left, right, "--")...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git diff %s: %v %s", diff, err, strings.TrimSpace(stderr.String()))
	}
	oldName, newName := "", ""
	sc := bufio.NewScanner(&stdout)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		l := sc.Text()
		switch {
		case strings.HasPrefix(l, "diff --git "):
			oldName, newName = "", ""
		case strings.HasPrefix(l, "--- "):
			oldName = diffName(l[4:])
		case strings.HasPrefix(l, "+++ "):
			newName = diffName(l[4:])
		case strings.HasPrefix(l, "@@"):
			m := hunkRe.FindStringSubmatch(l)
			if m == nil {
				continue
			}
			if oldName != "" {
				ch.old[oldName] = append(ch.old[oldName], hunkRange(m[1], m[2]))
			}
			if newName != "" {
				ch.new[newName] = append(ch.new[newName], hunkRange(m[3], m[4]))
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	// Lo que git lista sin hunks cuenta completo en el lado que existe.
	for _, n := range names {
		if n.status != 'A' && len(ch.old[n.path]) == 0 {
			ch.old[n.path] = []lineRange{whole}
		}
		if n.status != 'D' && len(ch.new[n.path]) == 0 {
			ch.new[n.path] = []lineRange{whole}
		}
	}
	return ch, nil
}

// diffName lee el nombre de una línea --- o +++: sin el prefijo a/ o b/, y
// sin las comillas con las que git escribe los nombres raros.
func diffName(s string) string {
	s = strings.TrimSuffix(s, "\t")
	if strings.HasPrefix(s, "\"") {
		if u, err := strconv.Unquote(s); err == nil {
			s = u
		}
	}
	if s == "/dev/null" {
		return ""
	}
	return strings.TrimPrefix(strings.TrimPrefix(s, "a/"), "b/")
}

type nameStatus struct {
	status byte // A, D, M, T…
	path   string
}

// diffNames lista los archivos de un rango con su estado. Contra el árbol de
// trabajo, lo nuevo que git aún no sigue también entra, como agregado.
func diffNames(dir, diff string) ([]nameStatus, error) {
	if strings.HasPrefix(diff, "-") {
		return nil, fmt.Errorf("rango de git inválido %q", diff)
	}
	left, right, worktree := diffSides(dir, diff)
	if left == "" || (!worktree && right == "") {
		return nil, fmt.Errorf("git: no reconozco el rango %q", diff)
	}
	flags := []string{"-z", "--name-status", "--no-renames", "--ignore-submodules=all"}
	var cmd *exec.Cmd
	if worktree {
		c, err := gitWorktree(dir, append(append([]string{"diff-index"}, flags...), left, "--")...)
		if err != nil {
			return nil, err
		}
		cmd = c
	} else {
		cmd = gitRead(dir, append(append([]string{"diff-tree", "-r"}, flags...), left, right, "--")...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git diff %s: %v %s", diff, err, strings.TrimSpace(stderr.String()))
	}
	parts := strings.Split(stdout.String(), "\x00")
	var out []nameStatus
	for i := 0; i+1 < len(parts); i += 2 {
		if parts[i] == "" || parts[i+1] == "" {
			continue
		}
		out = append(out, nameStatus{status: parts[i][0], path: parts[i+1]})
	}
	if worktree {
		o, err := gitRead(dir, "ls-files", "-z", "--others", "--exclude-standard").Output()
		if err == nil {
			for _, f := range strings.Split(string(o), "\x00") {
				if f != "" {
					out = append(out, nameStatus{status: 'A', path: f})
				}
			}
		}
	}
	return out, nil
}

// hunkRange convierte "inicio,cuenta" en un rango; una cuenta de cero es un
// punto (lo que se insertó o borró junto a esa línea).
func hunkRange(start, count string) lineRange {
	s, _ := strconv.Atoi(start)
	n := 1
	if count != "" {
		n, _ = strconv.Atoi(count)
	}
	if n == 0 {
		return lineRange{s, s}
	}
	return lineRange{s, s + n - 1}
}

// revSHA resuelve una revisión a su commit completo, o "".
func revSHA(dir, rev string) string {
	if rev == "" || strings.HasPrefix(rev, "-") {
		return ""
	}
	out, err := gitRead(dir, "rev-parse", "--verify", "--quiet", rev+"^{commit}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// diffSides resuelve los commits a cada lado de un rango de git. right vacío
// es el árbol de trabajo (un diff contra una sola revisión).
func diffSides(dir, diff string) (left, right string, worktree bool) {
	orHead := func(s string) string {
		if s == "" {
			return "HEAD"
		}
		return s
	}
	if a, b, ok := strings.Cut(diff, "..."); ok {
		out, err := gitRead(dir, "merge-base", "--", orHead(a), orHead(b)).Output()
		if err == nil {
			left = strings.TrimSpace(string(out))
		}
		return left, revSHA(dir, orHead(b)), false
	}
	if a, b, ok := strings.Cut(diff, ".."); ok {
		return revSHA(dir, orHead(a)), revSHA(dir, orHead(b)), false
	}
	return revSHA(dir, diff), "", true
}

// sideFor decide con qué lado del diff se comparan las líneas del mapa: el
// commit del mapa dice de qué versión son sus números de línea. Devuelve
// también la revisión de la que se lee el texto de los archivos ("" es el
// árbol de trabajo).
func (m *Map) sideFor(dir string, c Change) (diffSide, string, string) {
	if c.Diff == "" {
		return sideNew, "", ""
	}
	sha := m.SHAs[c.Repo]
	left, right, worktree := diffSides(dir, c.Diff)
	has := func(full string) bool { return sha != "" && full != "" && strings.HasPrefix(full, sha) }
	switch {
	case worktree && has(revSHA(dir, "HEAD")):
		return sideNew, "", ""
	case has(right):
		return sideNew, right, ""
	case has(left):
		return sideOld, left, ""
	}
	rev := right
	return sideBoth, rev, fmt.Sprintf("el mapa de %s (%s) no es de ningún lado del diff %s: las líneas pueden no coincidir; usa --fresh o arma el mapa en el commit base",
		c.Repo, dashIf(sha), c.Diff)
}

// fileText lee un archivo del repo en una revisión, o del árbol de trabajo.
func fileText(dir, rev, p string) (string, bool) {
	if rev == "" {
		data, ok := readRegular(dir, p)
		return string(data), ok
	}
	cmd := gitRead(dir, "cat-file", "blob", rev+":"+p)
	var out bytes.Buffer
	cmd.Stdout = &out
	if cmd.Run() != nil || out.Len() > maxFile || bytes.IndexByte(out.Bytes(), 0) >= 0 {
		return "", false
	}
	return out.String(), true
}

// methodAfter devuelve el método que declara una anotación en la línea dada
// (desde 1): la primera declaración en las líneas siguientes.
func methodAfter(lines []string, line int) (string, int) {
	for i := line - 1; i >= 0 && i < len(lines) && i < line+9; i++ {
		if name, ok := isDecl(lines[i]); ok {
			return name, i + 1
		}
	}
	return "", 0
}

// blockOf devuelve el rango de líneas (desde 1) del método que empieza en la
// declaración start: hasta antes de la siguiente declaración.
func blockOf(lines []string, start int) lineRange {
	for i := start; i < len(lines); i++ {
		if _, ok := isDecl(lines[i]); ok {
			return lineRange{start, i}
		}
	}
	return lineRange{start, len(lines)}
}

// entryBlock es el método de una interfaz: el que sigue a la anotación de un
// endpoint o listener, o el que contiene una llamada o publicación.
func entryBlock(lines []string, e Entry) (string, lineRange, bool) {
	if e.Role == Exposes || e.Role == Listens {
		name, at := methodAfter(lines, e.Line)
		if at == 0 {
			return "", lineRange{}, false
		}
		return name, blockOf(lines, at), true
	}
	for i := e.Line - 1; i >= 0 && i < len(lines); i-- {
		if name, ok := isDecl(lines[i]); ok {
			return name, blockOf(lines, i+1), true
		}
	}
	return "", lineRange{}, false
}

// near dice si una interfaz está en las líneas cambiadas; las anotaciones de
// endpoints y listeners cubren también las dos líneas que siguen.
func near(e Entry, r lineRange) bool {
	if e.Role == Exposes || e.Role == Listens {
		return e.Line >= r.from-2 && e.Line <= r.to+1
	}
	return e.Line >= r.from-1 && e.Line <= r.to+1
}

var typeDeclRe = regexp.MustCompile(`\b(?:class|record|interface|enum|type)\s+([A-Z][A-Za-z0-9_]*)`)

// touchByFiles marca lo que tocan los archivos cambiados: interfaces en las
// líneas cambiadas, las interfaces cuyo método contiene el cambio y las que
// usan un tipo que cambió.
func (m *Map) touchByFiles(rd *reader, repo, dir, rev string, changes map[string][]lineRange, touched map[int]string, im *Impact) {
	byFile := map[string][]int{}
	for i, e := range m.Entries {
		if e.Repo == repo {
			byFile[e.File] = append(byFile[e.File], i)
		}
	}
	// Todo lo que se va a leer en esa revisión, en un solo lote.
	var want []string
	for f := range changes {
		want = append(want, f)
	}
	for f := range byFile {
		want = append(want, f)
	}
	sort.Strings(want)
	rd.prefetch(dir, rev, want)
	text := func(f string) (string, bool) { return rd.text(dir, rev, f) }
	lines := map[string][]string{}
	linesOf := func(f string) ([]string, bool) {
		if l, ok := lines[f]; ok {
			return l, l != nil
		}
		t, ok := text(f)
		if !ok {
			lines[f] = nil
			return nil, false
		}
		lines[f] = strings.Split(t, "\n")
		return lines[f], true
	}
	files := make([]string, 0, len(changes))
	for f := range changes {
		files = append(files, f)
	}
	sort.Strings(files)
	var changedTypes []string
	for _, f := range files {
		idx := byFile[f]
		ls, haveText := linesOf(f)
		for _, r := range changes[f] {
			if r == whole {
				for _, i := range idx {
					touched[i] = "archivo cambiado: " + f
				}
				continue
			}
			hit := false
			for _, i := range idx {
				if near(m.Entries[i], r) {
					touched[i] = "cambia en " + f
					hit = true
				}
			}
			if hit {
				continue
			}
			if !haveText {
				// Sin el texto: el último endpoint antes del cambio.
				last := -1
				for _, i := range idx {
					if m.Entries[i].Role == Exposes && m.Entries[i].Line < r.from {
						last = i
					}
				}
				if last >= 0 {
					touched[last] = "cambia su implementación en " + f + " (posible)"
				}
				continue
			}
			// Las interfaces cuyo método contiene el cambio.
			for _, i := range idx {
				_, b, ok := entryBlock(ls, m.Entries[i])
				if !ok || r.to < b.from || r.from > b.to {
					continue
				}
				if _, done := touched[i]; !done {
					touched[i] = "cambia su implementación en " + f
				}
			}
		}
		if len(idx) == 0 && haveText && codeExt[strings.ToLower(filepath.Ext(f))] && !isTestPath(f) {
			for _, mt := range typeDeclRe.FindAllStringSubmatch(strings.Join(ls, "\n"), -1) {
				// Un tipo es un nombre con minúsculas: URL o T son constantes o genéricos.
				if len(mt[1]) >= 3 && strings.ToUpper(mt[1]) != mt[1] {
					changedTypes = append(changedTypes, mt[1])
				}
			}
		}
	}
	if len(changedTypes) == 0 {
		return
	}
	// Interfaces cuyo método usa un tipo que cambió (un DTO, un evento).
	users := map[string]bool{}
	for f, idx := range byFile {
		ls, ok := linesOf(f)
		if !ok {
			continue
		}
		for _, i := range idx {
			e := m.Entries[i]
			if _, done := touched[i]; done {
				continue
			}
			_, b, ok := entryBlock(ls, e)
			if !ok {
				continue
			}
			ids := identifiers(strings.Join(ls[b.from-1:minInt(b.to, len(ls))], "\n"))
			for _, t := range changedTypes {
				if ids[t] {
					touched[i] = "usa " + t + ", que cambió"
					users[f] = true
					break
				}
			}
		}
	}
	if len(users) > 20 {
		shown := changedTypes
		more := ""
		if len(shown) > 8 {
			shown, more = shown[:8], fmt.Sprintf(" y %d más", len(changedTypes)-8)
		}
		im.Notes = append(im.Notes, fmt.Sprintf("los tipos que cambiaron (%s%s) se usan en %d archivos con interfaces: revisa si el cambio es de contrato",
			strings.Join(shown, ", "), more, len(users)))
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ---- cambios de contrato dentro de un diff ----

// contractID identifica una interfaz por su contrato: repo, archivo, rol,
// verbo y ruta, sin la línea ni el texto original.
func contractID(e Entry) string {
	u := ""
	if e.Unresolved {
		u = "?" + e.Raw
	}
	return strings.Join([]string{e.Repo, e.File, e.Role + u, e.Method, e.Path}, "|")
}

// moduleFor deduce el módulo de un archivo con los módulos que el mapa conoce.
func (m *Map) moduleFor(repo, file string) string {
	best := "."
	for _, e := range m.Entries {
		if e.Repo == repo && e.Module != "." && strings.HasPrefix(file, e.Module+"/") && len(e.Module) > len(best) {
			best = e.Module
		}
	}
	return best
}

// contractChanges compara las interfaces de cada archivo de código cambiado
// antes y después del diff: lo que se elimina y lo que se agrega.
func (m *Map) contractChanges(rd *reader, dir, repo string, ch *fileChanges, left, right string, worktree bool) (added, removed []Entry) {
	names := map[string]bool{}
	for f := range ch.old {
		names[f] = true
	}
	for f := range ch.new {
		names[f] = true
	}
	files := make([]string, 0, len(names))
	for f := range names {
		files = append(files, f)
	}
	sort.Strings(files)
	var olds, news []string
	for _, f := range files {
		if codeExt[strings.ToLower(filepath.Ext(f))] && !isTestPath(f) {
			if _, ok := ch.old[f]; ok {
				olds = append(olds, f)
			}
			if _, ok := ch.new[f]; ok {
				news = append(news, f)
			}
		}
	}
	rd.prefetch(dir, left, olds)
	if !worktree {
		rd.prefetch(dir, right, news)
	}
	ctxs := map[string]*moduleCtx{}
	for _, f := range files {
		if !codeExt[strings.ToLower(filepath.Ext(f))] || isTestPath(f) {
			continue
		}
		mod := m.moduleFor(repo, f)
		var lt, rt string
		if _, ok := ch.old[f]; ok && left != "" {
			lt, _ = rd.text(dir, left, f)
		}
		if _, ok := ch.new[f]; ok {
			switch {
			case worktree:
				rt, _ = rd.text(dir, "", f)
			case right != "":
				rt, _ = rd.text(dir, right, f)
			}
		}
		mc := ctxs[mod]
		if mc == nil && (strings.HasSuffix(f, ".java") || strings.HasSuffix(f, ".kt")) {
			mc = loadModuleCtx(rd, dir, mod)
			ctxs[mod] = mc
		}
		before, after := extractText(repo, mod, f, lt, mc), extractText(repo, mod, f, rt, mc)
		count := map[string]int{}
		for _, e := range before {
			count[contractID(e)]++
		}
		for _, e := range after {
			if k := contractID(e); count[k] > 0 {
				count[k]--
				continue
			}
			added = append(added, e)
		}
		remaining := map[string]int{}
		for _, e := range after {
			remaining[contractID(e)]++
		}
		for _, e := range before {
			if k := contractID(e); remaining[k] > 0 {
				remaining[k]--
				continue
			}
			removed = append(removed, e)
		}
	}
	return added, removed
}

// ChangedFiles lista los archivos que cambia un rango de git (base...head,
// base..head o una revisión contra el árbol de trabajo): los nuevos, los
// modificados y los borrados, también los binarios y los vacíos. Lee con
// plomería, como el resto del paquete.
func ChangedFiles(dir, diff string) ([]string, error) {
	if diff == "" {
		return nil, fmt.Errorf("falta el rango de git")
	}
	names, err := diffNames(dir, diff)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, n := range names {
		if !seen[n.path] {
			seen[n.path] = true
			out = append(out, n.path)
		}
	}
	sort.Strings(out)
	return out, nil
}

// FileAt lee un archivo de texto tal como está en una revisión, sin tocar el
// árbol de trabajo: el CODEOWNERS que manda es el de la rama base.
func FileAt(dir, rev, path string) (string, bool) {
	if rev == "" || strings.HasPrefix(rev, "-") || strings.HasPrefix(path, "-") || strings.Contains(path, "..") {
		return "", false
	}
	return fileText(dir, rev, path)
}
