package gate

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode"
)

// El gate lee un comodín como lo leería el shell. filepath.Glob de Go no
// entiende lo que bash y zsh sí: las clases de caracteres de POSIX
// ([[:alpha:]]), las equivalencias ([[=n=]]), los símbolos de colación
// ([[.n.]]), un ] como primer miembro ([]n]) ni la negación con ! ([!x]). Con
// `cat .e[[:alpha:]]v` el gate no veía que se leía .env.
//
// El criterio es leer de más: un nombre que el shell no alcanzaría puede
// coincidir, nunca al revés. Un * alcanza también los nombres que empiezan con
// punto.

// El shell expande un comodín sin tope y el gate no puede: corre dentro del hook
// de un IDE, que corta el hook por tiempo y deja pasar la acción. La expansión
// tiene topes de trabajo y, cuando uno la corta, lo dice (globAll devuelve
// completa=false): quien la usa no da por vista una lista recortada, pide la
// aprobación de una persona. Son variables para que las pruebas los bajen.
var (
	// maxGlobPaths acota cuántas rutas se siguen en cada tramo.
	maxGlobPaths = 5000
	// maxGlobDir acota cuántos nombres se leen de una carpeta.
	maxGlobDir = 50000
	// maxGlobReads acota cuántas carpetas se abren en toda la expansión.
	maxGlobReads = 4000
	// maxGlobNames acota cuántos nombres se leen en toda la expansión.
	maxGlobNames = 400000
)

// globElem es un elemento de un patrón de nombres: un caracter, ?, * o una
// expresión entre corchetes.
type globElem struct {
	kind byte // 'l' literal, '?' cualquier caracter, '*' cualquier tramo, '[' un conjunto
	r    rune
	set  *globSet
}

// globSet es una expresión entre corchetes.
type globSet struct {
	neg     bool
	any     bool // una clase o un símbolo de colación que no se conoce: alcanza cualquiera
	nonAscl bool // una equivalencia ([=n=]) alcanza también las letras con acento
	runes   []rune
	ranges  [][2]rune
	classes []func(rune) bool
}

func (s *globSet) has(r rune) bool {
	if s.any {
		return true
	}
	in := s.nonAscl && r > 0x7f
	for _, x := range s.runes {
		if x == r {
			in = true
		}
	}
	for _, rg := range s.ranges {
		if rg[0] <= r && r <= rg[1] {
			in = true
		}
	}
	for _, f := range s.classes {
		if f(r) {
			in = true
		}
	}
	return in != s.neg
}

// posixClass son las clases de caracteres de POSIX.
var posixClass = map[string]func(rune) bool{
	"alpha":  unicode.IsLetter,
	"digit":  unicode.IsDigit,
	"alnum":  func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) },
	"upper":  unicode.IsUpper,
	"lower":  unicode.IsLower,
	"punct":  func(r rune) bool { return unicode.IsPunct(r) || unicode.IsSymbol(r) },
	"space":  unicode.IsSpace,
	"blank":  func(r rune) bool { return r == ' ' || r == '\t' },
	"cntrl":  unicode.IsControl,
	"graph":  func(r rune) bool { return unicode.IsGraphic(r) && !unicode.IsSpace(r) },
	"print":  unicode.IsPrint,
	"xdigit": func(r rune) bool { return strings.ContainsRune("0123456789abcdefABCDEF", r) },
}

// compileGlob parte un patrón en sus elementos. Una barra invertida cita el
// caracter que sigue; un [ que no cierra es un caracter más.
func compileGlob(p string) []globElem {
	rs := []rune(p)
	var out []globElem
	for i := 0; i < len(rs); {
		switch c := rs[i]; c {
		case '*':
			for i < len(rs) && rs[i] == '*' {
				i++
			}
			out = append(out, globElem{kind: '*'})
		case '?':
			out = append(out, globElem{kind: '?'})
			i++
		case '[':
			if set, n := parseGlobSet(rs[i:]); set != nil {
				out = append(out, globElem{kind: '[', set: set})
				i += n
			} else {
				out = append(out, globElem{kind: 'l', r: '['})
				i++
			}
		case '\\':
			if i+1 < len(rs) {
				out = append(out, globElem{kind: 'l', r: rs[i+1]})
				i += 2
			} else {
				out = append(out, globElem{kind: 'l', r: '\\'})
				i++
			}
		default:
			out = append(out, globElem{kind: 'l', r: c})
			i++
		}
	}
	return out
}

// setMember lee un miembro simple de una expresión entre corchetes: un
// caracter, uno citado con \ o un símbolo de colación de un caracter ([.n.]).
func setMember(rs []rune) (rune, int) {
	if len(rs) == 0 {
		return 0, 0
	}
	if rs[0] == '\\' && len(rs) > 1 {
		return rs[1], 2
	}
	if rs[0] == '[' && len(rs) > 4 && rs[1] == '.' && rs[3] == '.' && rs[4] == ']' {
		return rs[2], 5
	}
	return rs[0], 1
}

// parseGlobSet lee la expresión entre corchetes que empieza en rs[0] == '['.
// Devuelve nil si no cierra. El primer ] después del [ (o del [! ) es un
// miembro más.
func parseGlobSet(rs []rune) (*globSet, int) {
	s := &globSet{}
	i := 1
	if i < len(rs) && (rs[i] == '!' || rs[i] == '^') {
		s.neg = true
		i++
	}
	first := true
	for i < len(rs) {
		c := rs[i]
		if c == ']' && !first {
			return s, i + 1
		}
		first = false
		if c == '[' && i+1 < len(rs) && (rs[i+1] == ':' || rs[i+1] == '=' || rs[i+1] == '.') {
			kind := rs[i+1]
			end := -1
			for j := i + 2; j+1 < len(rs); j++ {
				if rs[j] == kind && rs[j+1] == ']' {
					end = j
					break
				}
			}
			if end >= 0 && (kind != '.' || end-(i+2) != 1) {
				name := string(rs[i+2 : end])
				i = end + 2
				switch kind {
				case ':':
					if f := posixClass[name]; f != nil {
						s.classes = append(s.classes, f)
					} else {
						s.any = true
					}
				default:
					if rn := []rune(name); len(rn) == 1 {
						s.runes = append(s.runes, rn[0])
						s.nonAscl = s.nonAscl || kind == '='
					} else {
						s.any = true
					}
				}
				continue
			}
		}
		lo, n := setMember(rs[i:])
		i += n
		if i+1 < len(rs) && rs[i] == '-' && rs[i+1] != ']' {
			hi, m := setMember(rs[i+1:])
			i += 1 + m
			s.ranges = append(s.ranges, [2]rune{lo, hi})
			continue
		}
		s.runes = append(s.runes, lo)
	}
	return nil, 0
}

// matchGlob dice si name coincide con el patrón compilado. El * retrocede lo
// mínimo: el tiempo es proporcional al producto de los dos largos.
func matchGlob(p []globElem, name []rune) bool {
	pi, ni := 0, 0
	starP, starN := -1, 0
	for ni < len(name) {
		if pi < len(p) {
			switch e := p[pi]; e.kind {
			case '*':
				starP, starN = pi, ni
				pi++
				continue
			case '?':
				pi++
				ni++
				continue
			case '[':
				if e.set.has(name[ni]) {
					pi++
					ni++
					continue
				}
			default:
				if e.r == name[ni] {
					pi++
					ni++
					continue
				}
			}
		}
		if starP < 0 {
			return false
		}
		starN++
		pi, ni = starP+1, starN
	}
	for pi < len(p) && p[pi].kind == '*' {
		pi++
	}
	return pi == len(p)
}

// shellMatch dice si name coincide con un patrón de un solo tramo, como lo
// leería el shell.
func shellMatch(pattern, name string) bool {
	return matchGlob(compileGlob(pattern), []rune(name))
}

// globHasMeta dice si un tramo trae un comodín sin citar.
func globHasMeta(part string) bool {
	for i := 0; i < len(part); i++ {
		switch part[i] {
		case '\\':
			i++
		case '*', '?', '[':
			return true
		}
	}
	return false
}

// globUnquote quita las barras invertidas que citan: el nombre que queda si el
// patrón no alcanza nada, como lo deja el shell.
func globUnquote(part string) string {
	if !strings.Contains(part, `\`) {
		return part
	}
	var b strings.Builder
	for i := 0; i < len(part); i++ {
		if part[i] == '\\' && i+1 < len(part) {
			i++
		}
		b.WriteByte(part[i])
	}
	return b.String()
}

// globWork lleva el trabajo de las expansiones de una evaluación: las carpetas
// y los nombres que se leyeron (los topes valen para el conjunto, no para cada
// comodín), lo que ya se expandió y si algún tope cortó una expansión.
type globWork struct {
	reads int  // carpetas abiertas
	names int  // nombres leídos
	cut   bool // un tope dejó trabajo sin hacer: el shell habría alcanzado más
	// cutAt es el primer patrón que un tope cortó.
	cutAt string
	memo  map[string]globOut
}

// globOut es una expansión hecha.
type globOut struct {
	matches  []string
	complete bool
}

// open reserva la lectura de una carpeta. Falla, y lo anota, si ya se leyeron
// todas las que el tope permite. Los nombres los acota dirNames.
func (w *globWork) open() bool {
	if w.reads >= maxGlobReads {
		w.cut = true
		return false
	}
	w.reads++
	return true
}

// dirNames lista los nombres de una carpeta, ordenados y acotados. Solo abre
// carpetas (openDir): un comodín alcanza archivos de todo tipo, y abrir una
// tubería con nombre bloquea y abrir un terminal o un dispositivo puede tener
// efectos.
func (w *globWork) dirNames(dir string) []string {
	if dir == "" {
		dir = "."
	}
	f, err := openDir(dir)
	if err != nil {
		return nil
	}
	defer f.Close()
	limit := min(maxGlobDir, maxGlobNames-w.names)
	names, _ := f.Readdirnames(limit + 1)
	if len(names) > limit {
		names = names[:limit]
		w.cut = true
	}
	w.names += len(names)
	sort.Strings(names)
	return names
}

// globAll expande un patrón de rutas como lo haría el shell en ese momento,
// con topes propios (globWork.expand).
func globAll(pattern string) (matches []string, complete bool) {
	return (&globWork{}).expand(pattern)
}

// expand expande un patrón de rutas como lo haría el shell en ese momento. En
// cada tramo con comodines devuelve los nombres que alcanza y también el
// nombre tal cual si existe: la carpeta de un proyecto en "Mi app [v2]" no es
// una clase de caracteres, y cat [x].env lee ese archivo si no hay un x.env. Una
// barra invertida cita el caracter que sigue (así llegan los corchetes que el
// comando escribió entre comillas).
//
// Devuelve también si la expansión llegó hasta el final. Si un tope la cortó
// (rutas, nombres o carpetas de más), devuelve lo que alcanzó antes y false: el
// shell habría alcanzado más. Un patrón ya expandido no vuelve a gastar trabajo.
func (w *globWork) expand(pattern string) (matches []string, complete bool) {
	if runtime.GOOS == "windows" {
		m, _ := filepath.Glob(pattern)
		return m, true
	}
	if o, ok := w.memo[pattern]; ok {
		return o.matches, o.complete
	}
	was := w.cut
	w.cut = false
	matches = w.run(pattern)
	complete = !w.cut
	if w.cut && w.cutAt == "" {
		w.cutAt = pattern
	}
	w.cut = w.cut || was
	if w.memo == nil {
		w.memo = map[string]globOut{}
	}
	w.memo[pattern] = globOut{matches, complete}
	return matches, complete
}

// run hace la expansión de expand.
func (w *globWork) run(pattern string) []string {
	bases := []string{""}
	if strings.HasPrefix(pattern, "/") {
		bases = []string{"/"}
	}
	join := func(base, name string) string {
		switch base {
		case "":
			return name
		case "/":
			return "/" + name
		}
		return base + "/" + name
	}
	for _, part := range strings.Split(pattern, "/") {
		if part == "" {
			continue
		}
		var next []string
		seen := map[string]bool{}
		add := func(p string) {
			if seen[p] {
				return
			}
			if len(next) >= maxGlobPaths {
				w.cut = true
				return
			}
			seen[p] = true
			next = append(next, p)
		}
		lit := globUnquote(part)
		if !globHasMeta(part) {
			for _, b := range bases {
				add(join(b, lit))
			}
		} else {
			elems := compileGlob(part)
			for _, b := range bases {
				if !w.open() {
					break
				}
				for _, name := range w.dirNames(b) {
					if matchGlob(elems, []rune(name)) {
						add(join(b, name))
					}
				}
				if _, err := os.Lstat(join(b, lit)); err == nil {
					add(join(b, lit))
				}
			}
		}
		bases = next
	}
	var out []string
	for _, b := range bases {
		if b == "" || b == "/" {
			continue
		}
		if _, err := os.Lstat(b); err == nil {
			out = append(out, b)
		}
	}
	return out
}

// quoteGlob antepone \ a los caracteres que un patrón de nombres lee como
// comodín o como cita: así un corchete escrito entre comillas sigue siendo una
// letra cuando globAll lee la palabra. Trabaja por bytes: los que cita son
// ASCII y el resto pasa tal cual.
func quoteGlob(s string) string {
	if !strings.ContainsAny(s, `*?[]\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if strings.IndexByte(`*?[]\`, s[i]) >= 0 {
			b.WriteByte('\\')
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
