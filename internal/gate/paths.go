package gate

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// segPaths es un segmento de un comando con las rutas que nombra cada una de
// sus palabras, tal como las vería el sistema de archivos.
type segPaths struct {
	line  string     // las palabras y sus rutas, para buscar textos protegidos
	cands [][]string // por palabra: sus rutas limpias y resueltas
}

// resolveSegs resuelve las rutas de cada segmento (v1.0, revisiones
// adversariales). Una palabra se reconoce también:
//   - limpia: coyote//approvals, .claude/./settings.json, coyote/x/../ledger;
//   - contra la carpeta actual: la del IDE (cwd) y la de cada cd, pushd o
//     popd anterior del mismo comando, y la de -C, --chdir, --work-tree o
//     --directory en su segmento;
//   - con el valor de una opción: --target-directory=x, -tx, of=x;
//   - con los comodines que el shell expandiría en ese momento;
//   - a través de un enlace que ya existe o que el mismo comando crea con ln -s.
//
// Una carpeta que llega al correr ($DIR, ~, -, CDPATH, pushd +1) deja de
// resolverse: lo que sigue pide aprobación como siempre.
func (ps Paths) resolveSegs(segs [][]string, cwd string) []segPaths {
	out := make([]segPaths, len(segs))
	dir, known := ".", true
	if cwd != "" && ps.Root != "" {
		rel, err := filepath.Rel(ps.Root, cwd)
		rel = filepath.ToSlash(rel)
		switch {
		case err == nil && rel != ".." && !strings.HasPrefix(rel, "../"):
			dir = path.Clean(rel)
		case filepath.IsAbs(cwd):
			dir = filepath.ToSlash(filepath.Clean(cwd))
		}
	}
	alias := map[string]string{}
	cdpath := false
	for i, seg := range segs {
		prog, at := mainProg(seg)
		for _, w := range seg {
			if strings.HasPrefix(w, "CDPATH=") {
				cdpath = true
			}
		}
		words := append([]string(nil), seg...)
		cands := make([][]string, len(seg))
		if prog == "cd" || prog == "pushd" || prog == "popd" {
			arg := firstOperand(seg, at)
			switch {
			case prog == "popd", arg == "", arg == "-", cdpath, strings.ContainsAny(arg, "$~`*?["),
				prog == "pushd" && (strings.HasPrefix(arg, "+") || strings.HasPrefix(arg, "-")):
				known = false
			case path.IsAbs(arg):
				dir, known = path.Clean(arg), true
			default:
				dir = path.Join(dir, arg)
			}
			out[i] = segPaths{line: segText(words), cands: cands}
			continue
		}
		var bases []string
		if known {
			bases = append(bases, dir)
			if d := segDir(seg); d != "" && !strings.ContainsAny(d, "$~`") {
				bases = append(bases, joinPath(dir, d))
			}
		}
		skip := subcommandAt(prog, seg, at)
		for j, w := range seg {
			target := redirPrefix.ReplaceAllString(w, "")
			redirect := target != w
			var cs []string
			add := func(c string) {
				if c != "" && c != w {
					cs = append(cs, c)
				}
			}
			add(cleanPath(target))
			if (j > at || redirect) && j != skip && target != "" {
				for _, v := range wordValues(target) {
					for _, b := range bases {
						p := cleanPath(joinPath(b, v))
						add(p)
						if strings.ContainsAny(v, "*?[") {
							for _, m := range ps.globRel(p) {
								add(m)
							}
						}
						if r := ps.throughLinks(p); r != "" {
							add(r)
						}
						for k, t := range alias {
							if p == k || strings.HasPrefix(p, k+"/") {
								add(t + strings.TrimPrefix(p, k))
							}
						}
					}
				}
			}
			cands[j] = cs
			words = append(words, cs...)
		}
		if prog == "ln" && known && symbolic(seg, at) {
			if ops := operands(seg, at); len(ops) >= 2 {
				link := cleanPath(joinPath(dir, ops[len(ops)-1]))
				tgt := ops[len(ops)-2]
				if !path.IsAbs(tgt) {
					tgt = path.Join(path.Dir(link), tgt)
				}
				alias[link] = path.Clean(tgt)
			}
		}
		out[i] = segPaths{line: segText(words), cands: cands}
	}
	return out
}

// redirPrefix es una redirección pegada a su destino (>x, 2>>x, &>x, >&x,
// >|x, 3<>x).
var redirPrefix = regexp.MustCompile(`^(?:[0-9]*|&)(?:>>|>\||>&|<>|>|<)`)

// redirOp es una redirección escrita como palabra aparte (>, 3>>, &>, <>).
var redirOp = regexp.MustCompile(`^(?:[0-9]*|&)(?:>>|>\||>&|<>|>|<)$`)

// cleanPath limpia una palabra con forma de ruta, también el valor de
// --opción=ruta.
func cleanPath(w string) string {
	if !strings.Contains(w, "/") {
		return w
	}
	if k, v, ok := strings.Cut(w, "="); ok && !strings.Contains(k, "/") {
		return k + "=" + path.Clean(v)
	}
	return path.Clean(w)
}

// joinPath resuelve una palabra relativa contra dir.
func joinPath(dir, w string) string {
	if path.IsAbs(w) {
		return w
	}
	return path.Join(dir, w)
}

// wordValues son las rutas que puede nombrar una palabra: ella misma, el
// valor de --opción=valor o de clave=valor (dd of=x) y el de una opción
// corta con el valor pegado (cp -tx).
func wordValues(w string) []string {
	switch {
	case strings.HasPrefix(w, "--"):
		if _, v, ok := strings.Cut(w, "="); ok && v != "" {
			return []string{v}
		}
		return nil
	case strings.HasPrefix(w, "-"):
		if len(w) > 2 {
			return []string{w[2:]}
		}
		return nil
	}
	if k, v, ok := strings.Cut(w, "="); ok && v != "" && !strings.Contains(k, "/") {
		return []string{w, v}
	}
	return []string{w}
}

// segDir es la carpeta en la que corre un segmento por su opción: -C (git,
// make, tar, env), --chdir, --work-tree o --directory.
func segDir(seg []string) string {
	d := ""
	for j, w := range seg {
		switch {
		case (w == "-C" || w == "--chdir" || w == "--work-tree" || w == "--directory") && j+1 < len(seg):
			d = seg[j+1]
		case strings.HasPrefix(w, "--chdir="), strings.HasPrefix(w, "--work-tree="), strings.HasPrefix(w, "--directory="):
			_, d, _ = strings.Cut(w, "=")
		}
	}
	return d
}

// subPrograms tienen subcomandos: su subcomando no es una ruta (git config
// después de cd .git no lee .git/config).
var subPrograms = map[string]bool{"git": true, "go": true, "npm": true, "pnpm": true, "yarn": true, "docker": true,
	"kubectl": true, "gh": true, "coyote": true, "cargo": true, "terraform": true, "tofu": true, "helm": true,
	"aws": true, "gcloud": true, "az": true}

// subcommandAt devuelve la posición del subcomando, o -1.
func subcommandAt(prog string, seg []string, at int) int {
	if !subPrograms[prog] || at < 0 {
		return -1
	}
	if prog == "git" {
		if _, off := gitSub(seg[at+1:]); off >= 0 {
			return at + 1 + off
		}
		return -1
	}
	for j := at + 1; j < len(seg); j++ {
		if !strings.HasPrefix(seg[j], "-") {
			return j
		}
	}
	return -1
}

// firstOperand es el primer argumento que no es una opción ("-" sí cuenta).
func firstOperand(seg []string, at int) string {
	if at < 0 {
		return ""
	}
	for _, w := range seg[at+1:] {
		if w == "-" || !strings.HasPrefix(w, "-") {
			return w
		}
	}
	return ""
}

// operands son los argumentos que no son opciones.
func operands(seg []string, at int) []string {
	var out []string
	if at < 0 {
		return out
	}
	for _, w := range seg[at+1:] {
		if !strings.HasPrefix(w, "-") {
			out = append(out, w)
		}
	}
	return out
}

// symbolic dice si un ln crea un enlace simbólico.
func symbolic(seg []string, at int) bool {
	for _, w := range seg[at+1:] {
		if w == "--symbolic" || strings.HasPrefix(w, "-") && !strings.HasPrefix(w, "--") && strings.ContainsRune(w, 's') {
			return true
		}
	}
	return false
}

// glob expande un patrón como lo haría el shell en ese momento. Lo que del
// camino ya existe con ese nombre se toma literal: la carpeta de un proyecto
// en "Mi app [v2]" no es una clase de caracteres, y leerla así hacía que
// `cat .e*` no alcanzara nada. Devuelve las coincidencias de las dos lecturas.
func glob(pattern string) []string {
	var out []string
	seen := map[string]bool{}
	pats := []string{pattern}
	if lit := literalPrefix(pattern); lit != pattern {
		pats = append(pats, lit)
	}
	for _, pat := range pats {
		matches, _ := filepath.Glob(pat)
		for _, m := range matches {
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	return out
}

// literalPrefix escapa los comodines del tramo inicial de pattern que existe en
// el disco con ese mismo nombre. En Windows no hay escape: queda igual.
func literalPrefix(pattern string) string {
	if runtime.GOOS == "windows" || !strings.ContainsAny(pattern, "*?[") {
		return pattern
	}
	sep := string(filepath.Separator)
	parts := strings.Split(pattern, sep)
	cur := ""
	if strings.HasPrefix(pattern, sep) {
		cur = sep
	}
	n := 0
	for i, part := range parts {
		if part == "" {
			n = i + 1
			continue
		}
		next := filepath.Join(cur, part)
		if strings.ContainsAny(part, "*?[") {
			if _, err := os.Lstat(next); err != nil {
				break
			}
		}
		cur = next
		n = i + 1
	}
	if n >= len(parts) {
		return escapeMeta(cur)
	}
	return filepath.Join(escapeMeta(cur), strings.Join(parts[n:], sep))
}

// escapeMeta antepone \ a los caracteres de comodín de s.
func escapeMeta(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '*' || r == '?' || r == '[' || r == '\\' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// globRel expande un comodín como lo haría el shell en ese momento y devuelve
// las rutas relativas al proyecto. Un comodín que no alcanza nada queda tal
// cual, como en el shell.
func (ps Paths) globRel(p string) []string {
	if ps.Root == "" {
		return nil
	}
	abs := p
	if !path.IsAbs(p) {
		abs = filepath.Join(ps.Root, filepath.FromSlash(p))
	}
	matches := glob(abs)
	var out []string
	for i, m := range matches {
		if i >= 100 {
			break
		}
		out = append(out, ps.relOrAbs(m))
	}
	return out
}

// throughLinks resuelve los enlaces que ya existen en el camino de p, aunque
// el final todavía no exista: c/approvals con c -> coyote es coyote/approvals.
func (ps Paths) throughLinks(p string) string {
	if ps.Root == "" {
		return ""
	}
	abs := p
	if !path.IsAbs(p) {
		abs = filepath.Join(ps.Root, filepath.FromSlash(p))
	}
	rest := ""
	cur := filepath.Clean(abs)
	for i := 0; i < 64; i++ {
		if _, err := os.Lstat(cur); err == nil {
			real, err := filepath.EvalSymlinks(cur)
			if err != nil {
				return ""
			}
			full := filepath.Join(real, rest)
			if full == filepath.Clean(abs) {
				return ""
			}
			return ps.relOrAbs(full)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
	return ""
}

// relOrAbs da la ruta relativa al proyecto si cae dentro, o la absoluta.
func (ps Paths) relOrAbs(abs string) string {
	root := ps.Root
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	if rel, err := filepath.Rel(root, abs); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(abs)
}

// ledgerRe nombra el ledger de un proyecto.
var ledgerRe = regexp.MustCompile(`(?i)(^|[^a-z0-9_.-])coyote/ledger(/|$)`)

// ledgerReaders leen lo que nombran sin escribirlo: con el ledger como
// argumento, piden la aprobación normal.
var ledgerReaders = map[string]bool{"cat": true, "head": true, "tail": true, "wc": true, "grep": true, "egrep": true,
	"fgrep": true, "rg": true, "diff": true, "cmp": true, "sha256sum": true, "sha1sum": true, "md5sum": true, "shasum": true,
	"stat": true, "ls": true, "file": true, "less": true, "more": true, "jq": true, "du": true, "cut": true, "nl": true,
	"od": true, "strings": true, "column": true}

// ledgerGitReaders son los subcomandos de git que no reescriben el ledger:
// leerlo o prepararlo para un commit.
var ledgerGitReaders = map[string]bool{"add": true, "diff": true, "log": true, "show": true, "blame": true, "status": true,
	"ls-files": true, "grep": true, "rev-parse": true, "cat-file": true, "commit": true, "shortlog": true, "ls-tree": true}

// ledgerTouch dice si un comando con efectos puede escribir en el ledger. La
// regla va al revés que una lista de escritores, que siempre queda corta
// (gzip, ex, sponge, python…): todo lo que lo nombra se bloquea, salvo leerlo
// con un programa que solo lee, prepararlo o verlo con git, o copiarlo hacia
// otro lugar. Una redirección hacia él, un programa que ejecuta lo que recibe
// (xargs, sh -c, find -exec) o una opción que lo apunta (--output=) se
// bloquean siempre.
func ledgerTouch(segs [][]string, sps []segPaths) bool {
	exec := false
	for _, seg := range segs {
		prog, at := mainProg(seg)
		if executors[prog] || prog == "find" && at >= 0 && findExecs(seg[at+1:]) {
			exec = true
		}
	}
	for i, seg := range segs {
		hits := func(j int) bool {
			if j < 0 || j >= len(seg) {
				return false
			}
			if ledgerRe.MatchString(seg[j]) {
				return true
			}
			for _, c := range sps[i].cands[j] {
				if ledgerRe.MatchString(c) {
					return true
				}
			}
			return false
		}
		named := false
		for j := range seg {
			if hits(j) {
				named = true
			}
		}
		if !named {
			continue
		}
		for j, w := range seg {
			if redirOp.MatchString(w) && hits(j+1) || redirPrefix.MatchString(w) && !redirOp.MatchString(w) && hits(j) {
				return true
			}
			if strings.HasPrefix(w, "-") && hits(j) {
				return true // una opción que apunta al ledger: --output=, -o, -t
			}
		}
		if exec {
			return true
		}
		prog, at := mainProg(seg)
		if at < 0 {
			return true
		}
		switch {
		case ledgerReaders[prog]:
			continue
		case prog == "git":
			if sub, _ := gitSub(seg[at+1:]); ledgerGitReaders[sub] {
				continue
			}
			return true
		case prog == "cp" || prog == "rsync" || prog == "scp" || prog == "install":
			// Copiar desde el ledger hacia otro lugar no lo escribe; el destino
			// es el último argumento.
			last := -1
			for j := at + 1; j < len(seg); j++ {
				if !strings.HasPrefix(seg[j], "-") && !redirOp.MatchString(seg[j]) && !redirPrefix.MatchString(seg[j]) {
					last = j
				}
			}
			if hits(last) {
				return true
			}
			continue
		}
		return true
	}
	return false
}

// findExecs dice si find corre algo o borra lo que encuentra.
func findExecs(args []string) bool {
	for _, w := range args {
		switch w {
		case "-exec", "-execdir", "-ok", "-okdir", "-delete", "-fprint", "-fprint0", "-fprintf", "-fls":
			return true
		}
	}
	return false
}

// protectedParents son carpetas que contienen lo que protege el gate: mover,
// borrar o reescribir una de ellas lo toca aunque no lo nombre.
var protectedParents = map[string]string{"coyote": "coyote/, con el ledger, las aprobaciones y la configuración del proyecto",
	".claude": ".claude/, con la configuración de Claude Code", ".git": ".git/, la carpeta de git", ".coyote": ".coyote/, el estado del gate"}

// fragmentRe son pedazos de rutas protegidas que un programa que ejecuta lo
// que recibe (xargs, find -exec) puede completar al correr: {}/approvals/.
var fragmentRe = regexp.MustCompile(`(?i)/(approvals|ledger)(/|$)|^(approvals|ledger)/|/settings(\.local)?\.json$|/project\.yaml$`)

// parentTouch dice si un comando mueve, borra o reescribe una carpeta que
// contiene lo protegido (mv coyote c, rm -rf .claude, tar -x -C coyote, find
// coyote -exec …), o si un programa que ejecuta lo que recibe completa una
// ruta protegida al correr.
func parentTouch(segs [][]string, sps []segPaths) (string, bool) {
	exec := false
	for _, seg := range segs {
		prog, at := mainProg(seg)
		if prog == "xargs" || prog == "parallel" || prog == "find" && at >= 0 && findExecs(seg[at+1:]) {
			exec = true
		}
	}
	if exec {
		for _, seg := range segs {
			for _, w := range seg {
				if fragmentRe.MatchString(w) {
					return "una ruta protegida que se completa al correr", true
				}
			}
		}
	}
	for i, seg := range segs {
		prog, at := mainProg(seg)
		if at < 0 {
			continue
		}
		parent := func(j int) string {
			for _, c := range append([]string{seg[j]}, sps[i].cands[j]...) {
				c = strings.TrimSuffix(strings.TrimPrefix(c, "./"), "/")
				if why, ok := protectedParents[c]; ok {
					return why
				}
			}
			return ""
		}
		operandsAt := func() []int {
			var out []int
			for j := at + 1; j < len(seg); j++ {
				if !strings.HasPrefix(seg[j], "-") {
					out = append(out, j)
				}
			}
			return out
		}
		ops := operandsAt()
		switch prog {
		case "mv", "rm", "rmdir", "shred", "chmod", "chown":
			for _, j := range ops {
				if why := parent(j); why != "" {
					return why, true
				}
			}
		case "cp", "rsync", "install", "ln":
			if len(ops) > 0 {
				if why := parent(ops[len(ops)-1]); why != "" {
					return why, true
				}
			}
		case "tar", "unzip", "bsdtar":
			extract := prog == "unzip"
			for j := at + 1; j < len(seg); j++ {
				w := seg[j]
				if w == "--extract" || w == "--get" || strings.HasPrefix(w, "-") && !strings.HasPrefix(w, "--") && strings.ContainsRune(w, 'x') ||
					j == at+1 && !strings.HasPrefix(w, "-") && strings.ContainsRune(w, 'x') {
					extract = true
				}
			}
			if extract {
				if d := segDir(seg); d != "" {
					if _, ok := protectedParents[strings.TrimSuffix(strings.TrimPrefix(path.Clean(d), "./"), "/")]; ok {
						return protectedParents[strings.TrimSuffix(strings.TrimPrefix(path.Clean(d), "./"), "/")], true
					}
				}
				for j := at + 1; j < len(seg); j++ {
					if (seg[j] == "-d" || seg[j] == "-C") && j+1 < len(seg) {
						if why := parent(j + 1); why != "" {
							return why, true
						}
					}
				}
			}
		case "find":
			if findExecs(seg[at+1:]) {
				for _, j := range ops {
					if why := parent(j); why != "" {
						return why, true
					}
				}
			}
		}
	}
	return "", false
}
