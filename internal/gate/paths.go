package gate

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
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

// redirPrefix es una redirección pegada a su destino (>x, 2>>x, &>x, >&x, >|x).
var redirPrefix = regexp.MustCompile(`^(?:[0-9]*|&)(?:>>|>\||>&|>|<)`)

// redirOps son las redirecciones escritas como palabra aparte.
var redirOps = map[string]bool{">": true, ">>": true, ">|": true, ">&": true, "&>": true, "&>>": true,
	"1>": true, "2>": true, "1>>": true, "2>>": true}

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
	matches, err := filepath.Glob(abs)
	if err != nil {
		return nil
	}
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
var ledgerRe = regexp.MustCompile(`(?i)(^|/)coyote/ledger(/|$)`)

// ledgerWriters escriben o borran lo que nombran.
var ledgerWriters = map[string]bool{"rm": true, "rmdir": true, "mv": true, "tee": true, "truncate": true, "touch": true,
	"chmod": true, "chown": true, "shred": true, "unlink": true, "install": true, "ln": true, "dd": true}

// ledgerGitWriters son los subcomandos de git que reescriben el árbol de trabajo.
var ledgerGitWriters = map[string]bool{"rm": true, "mv": true, "checkout": true, "restore": true, "reset": true,
	"apply": true, "am": true, "stash": true, "clean": true, "read-tree": true, "update-index": true, "switch": true}

// ledgerWrite dice si un comando escribe en el ledger: una redirección hacia
// él, un programa que escribe o borra lo que nombra, sed o perl en el lugar,
// una copia que lo tiene de destino o git que reescribe sus archivos. Leerlo
// o prepararlo para un commit (git add) no lo escribe y pide la aprobación
// normal.
func ledgerWrite(segs [][]string, sps []segPaths) bool {
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
		for j, w := range seg {
			if redirOps[w] && hits(j+1) || redirPrefix.MatchString(w) && redirPrefix.FindString(w) != w && hits(j) {
				return true
			}
		}
		prog, at := mainProg(seg)
		if at < 0 {
			continue
		}
		any := func(from int) bool {
			for j := from; j < len(seg); j++ {
				if hits(j) {
					return true
				}
			}
			return false
		}
		args := seg[at+1:]
		switch {
		case ledgerWriters[prog]:
			if any(at + 1) {
				return true
			}
		case prog == "cp" || prog == "rsync" || prog == "scp":
			last := -1
			for j := at + 1; j < len(seg); j++ {
				if strings.HasPrefix(seg[j], "-t") || strings.HasPrefix(seg[j], "--target-directory") {
					if hits(j) || seg[j] == "-t" && hits(j+1) {
						return true
					}
				}
				if !strings.HasPrefix(seg[j], "-") {
					last = j
				}
			}
			if hits(last) {
				return true
			}
		case prog == "sed" || prog == "perl" || prog == "ruby" || prog == "gawk" || prog == "awk":
			if inPlace(prog, args) && any(at+1) {
				return true
			}
		case prog == "git":
			if sub, off := gitSub(args); ledgerGitWriters[sub] && any(at+1+off) {
				return true
			}
		}
	}
	return false
}

// inPlace dice si sed, perl, ruby o awk editan el archivo en su lugar.
func inPlace(prog string, args []string) bool {
	for _, w := range args {
		switch {
		case prog == "sed" && (w == "-i" || strings.HasPrefix(w, "--in-place") || strings.HasPrefix(w, "-i") || len(w) > 1 && w[0] == '-' && w[1] != '-' && strings.ContainsRune(w, 'i')),
			(prog == "perl" || prog == "ruby") && strings.HasPrefix(w, "-") && !strings.HasPrefix(w, "--") && strings.ContainsRune(w, 'i'),
			(prog == "awk" || prog == "gawk") && (w == "inplace" || strings.Contains(w, "inplace")):
			return true
		}
	}
	return false
}
