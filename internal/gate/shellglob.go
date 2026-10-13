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
// ([[:alpha:]]), un ] como primer miembro ([]n]) ni la negación con ! ([!x]). Con
// `cat .e[[:alpha:]]v` el gate no veía que se leía .env.
//
// El criterio es leer de más: un nombre que el shell no alcanzaría puede
// coincidir, nunca al revés. Un * alcanza también los nombres que empiezan con
// punto, las mayúsculas valen como minúsculas (shopt -s nocaseglob) y un ?
// casa con un carácter o con un byte (en un locale C el ? lee un byte). Lo que
// bash lee de una manera propia, como una equivalencia [[=x=]] o un símbolo de
// colación [[.x.]] (que seguidos de otro corchete consumen un solo carácter), el
// gate no lo imita: ese tramo alcanza cualquier nombre.

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
	// maxGlobPart acota el largo, en bytes, de un tramo del patrón que se lee. Un
	// nombre mide a lo sumo 255 bytes: un tramo de más de mil no lo escribe
	// nadie, y leerlo cuesta lo que crece con su largo.
	maxGlobPart = 1024
	// maxGlobWork acota el trabajo de comparar nombres con patrones en toda la
	// evaluación: cada comparación gasta el producto de los dos largos, que es lo
	// más que tarda.
	maxGlobWork = 100_000_000
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
	any     bool // una clase que no se conoce: alcanza cualquiera
	runes   []rune
	ranges  [][2]rune
	classes []func(rune) bool
}

// in dice si r es miembro del conjunto, sin la negación.
func (s *globSet) in(r rune) bool {
	for _, x := range s.runes {
		if x == r {
			return true
		}
	}
	for _, rg := range s.ranges {
		if rg[0] <= r && r <= rg[1] {
			return true
		}
	}
	for _, f := range s.classes {
		if f(r) {
			return true
		}
	}
	return false
}

// has dice si el conjunto alcanza a r. Con fold lee también como lo haría un shell
// con nocaseglob (la letra vale en cualquiera de sus escrituras) y alcanza lo que
// cualquiera de las dos lecturas alcance: [!a] alcanza A, que sin nocaseglob no
// es la a, y no alcanza a, que no la alcanza de ninguna manera.
func (s *globSet) has(r rune, fold bool) bool {
	if s.any {
		return true
	}
	in := s.in(r)
	if in != s.neg {
		return true
	}
	if !fold {
		return false
	}
	for f := unicode.SimpleFold(r); f != r && !in; f = unicode.SimpleFold(f) {
		in = s.in(f)
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

// setIndex dice dónde cierra cada [: [= [. de un patrón: la primera posición
// desde un punto donde hay kind seguido de ]. Se calcula una vez por patrón, de
// atrás para adelante: buscar el cierre desde cada [ hacía el análisis cúbico (el
// de un conjunto sin cerrar sigue siendo cuadrático; lo acota compileGlob).
type setIndex struct {
	rs   []rune
	next map[rune][]int
}

func (x *setIndex) closer(kind rune, from int) int {
	if from >= len(x.rs) {
		return -1
	}
	nx, ok := x.next[kind]
	if !ok {
		nx = make([]int, len(x.rs)+1)
		nx[len(x.rs)] = -1
		for i := len(x.rs) - 1; i >= 0; i-- {
			if x.rs[i] == kind && i+1 < len(x.rs) && x.rs[i+1] == ']' {
				nx[i] = i
			} else {
				nx[i] = nx[i+1]
			}
		}
		if x.next == nil {
			x.next = map[rune][]int{}
		}
		x.next[kind] = nx
	}
	return nx[from]
}

// compileGlob parte un patrón en sus elementos. Una barra invertida cita el
// caracter que sigue; un [ que no cierra es un caracter más. Devuelve wild si el
// patrón trae algo que el gate no imita (una equivalencia, un símbolo de
// colación, una clase sin cierre): ese tramo alcanza cualquier nombre. Con
// byteMode lee el patrón como bytes, como un shell de locale C: cada byte es un
// carácter y las clases solo conocen el ASCII.
//
// Un patrón de más de maxGlobPart bytes no se compila: es wild. Quien expande
// comodines lo declara cortado antes de llegar aquí (globAll), y el cierre de
// cada [ se busca hacia adelante, así que muchos [ sin cerrar cuestan el
// cuadrado del largo (32 KB, 2,7 s). Con el tope, ninguna entrada se cuelga el
// compilador, la use quien la use.
func compileGlob(p string, byteMode bool) (elems []globElem, wild bool) {
	if len(p) > maxGlobPart {
		return nil, true
	}
	var rs []rune
	if byteMode {
		rs = make([]rune, len(p))
		for i := 0; i < len(p); i++ {
			rs[i] = rune(p[i])
		}
	} else {
		rs = []rune(p)
	}
	ix := &setIndex{rs: rs}
	for i := 0; i < len(rs); {
		switch c := rs[i]; c {
		case '*':
			for i < len(rs) && rs[i] == '*' {
				i++
			}
			elems = append(elems, globElem{kind: '*'})
		case '?':
			elems = append(elems, globElem{kind: '?'})
			i++
		case '[':
			set, next, w := parseGlobSet(rs, i, ix, byteMode)
			if w {
				return nil, true
			}
			if set != nil {
				elems = append(elems, globElem{kind: '[', set: set})
				i = next
			} else {
				elems = append(elems, globElem{kind: 'l', r: '['})
				i++
			}
		case '\\':
			if i+1 < len(rs) {
				elems = append(elems, globElem{kind: 'l', r: rs[i+1]})
				i += 2
			} else {
				elems = append(elems, globElem{kind: 'l', r: '\\'})
				i++
			}
		default:
			elems = append(elems, globElem{kind: 'l', r: c})
			i++
		}
	}
	return elems, false
}

// setMember lee un miembro simple de una expresión entre corchetes: un
// caracter o uno citado con \.
func setMember(rs []rune) (rune, int) {
	if rs[0] == '\\' && len(rs) > 1 {
		return rs[1], 2
	}
	return rs[0], 1
}

// parseGlobSet lee la expresión entre corchetes que empieza en rs[start] ==
// '['. Devuelve nil si no cierra. El primer ] después del [ (o del [! ) es un
// miembro más. Devuelve wild si trae una equivalencia ([=x=]), un símbolo de
// colación ([.x.]) o una clase sin cierre ([:alpha): bash las lee de una manera
// propia y el gate no la imita (ver el comienzo del archivo).
func parseGlobSet(rs []rune, start int, ix *setIndex, byteMode bool) (set *globSet, next int, wild bool) {
	s := &globSet{}
	i := start + 1
	if i < len(rs) && (rs[i] == '!' || rs[i] == '^') {
		s.neg = true
		i++
	}
	first := true
	for i < len(rs) {
		c := rs[i]
		if c == ']' && !first {
			return s, i + 1, false
		}
		first = false
		if c == '[' && i+1 < len(rs) {
			switch rs[i+1] {
			case '=', '.':
				return nil, 0, true
			case ':':
				end := ix.closer(':', i+2)
				if end < 0 {
					return nil, 0, true
				}
				if f := posixClass[string(rs[i+2:end])]; f != nil {
					if byteMode {
						g := f
						f = func(r rune) bool { return r < 0x80 && g(r) }
					}
					s.classes = append(s.classes, f)
				} else {
					s.any = true
				}
				i = end + 2
				continue
			}
		}
		lo, n := setMember(rs[i:])
		i += n
		if i+1 < len(rs) && rs[i] == '-' && rs[i+1] != ']' {
			if rs[i+1] == '[' && i+2 < len(rs) && (rs[i+2] == '=' || rs[i+2] == '.' || rs[i+2] == ':') {
				return nil, 0, true
			}
			hi, m := setMember(rs[i+1:])
			i += 1 + m
			s.ranges = append(s.ranges, [2]rune{lo, hi})
			continue
		}
		s.runes = append(s.runes, lo)
	}
	return nil, 0, false
}

// foldEq dice si dos caracteres son la misma letra, sin distinguir mayúsculas.
func foldEq(a, b rune) bool {
	if a == b {
		return true
	}
	for f := unicode.SimpleFold(a); f != a; f = unicode.SimpleFold(f) {
		if f == b {
			return true
		}
	}
	return false
}

// matchGlob dice si name coincide con el patrón compilado. El * retrocede lo
// mínimo: el tiempo es proporcional al producto de los dos largos. Con fold no
// distingue mayúsculas de minúsculas.
func matchGlob(p []globElem, name []rune, fold bool) bool {
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
				if e.set.has(name[ni], fold) {
					pi++
					ni++
					continue
				}
			default:
				if e.r == name[ni] || (fold && foldEq(e.r, name[ni])) {
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

// globPat es un tramo de un patrón, compilado: leído por caracteres y, cuando un
// nombre trae bytes que no son ASCII, también por bytes.
type globPat struct {
	src       string
	runes     []globElem
	wild      bool
	bytes     []globElem
	bytesDone bool
}

func newGlobPat(src string) *globPat {
	p := &globPat{src: src}
	p.runes, p.wild = compileGlob(src, false)
	return p
}

func asciiOnly(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// match dice si el patrón alcanza a name, de más y no de menos: sin distinguir
// mayúsculas y, si el nombre no es ASCII, por caracteres o por bytes.
func (p *globPat) match(name string) bool {
	if p.wild {
		return true
	}
	if matchGlob(p.runes, []rune(name), true) {
		return true
	}
	if asciiOnly(name) {
		return false
	}
	if !p.bytesDone {
		p.bytesDone = true
		p.bytes, _ = compileGlob(p.src, true)
	}
	b := make([]rune, len(name))
	for i := 0; i < len(name); i++ {
		b[i] = rune(name[i])
	}
	return matchGlob(p.bytes, b, true)
}

// shellMatch dice si name coincide con un patrón de un solo tramo, como lo
// leería un shell de locale UTF-8 con las opciones de siempre: por caracteres y
// distinguiendo mayúsculas. La expansión (globPat.match) lee de más.
func shellMatch(pattern, name string) bool {
	elems, wild := compileGlob(pattern, false)
	return wild || matchGlob(elems, []rune(name), false)
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

// globWork lleva el trabajo de una evaluación: las carpetas y los nombres que
// se leyeron, lo que se gastó comparando y las rutas distintas que se resolvieron
// contra el disco (los topes valen para el conjunto, no para cada comodín),
// lo que ya se expandió y si algún tope cortó una expansión.
type globWork struct {
	reads    int  // carpetas abiertas
	names    int  // nombres leídos
	work     int  // unidades de comparación gastadas
	resolves int  // rutas distintas resueltas contra el disco
	cut      bool // un tope dejó trabajo sin hacer: el shell habría alcanzado más
	// workOut dice que ya no queda trabajo de comparación: nada más se compara.
	workOut bool
	// cutAt es el primer patrón que un tope cortó y cutWhy, por qué (vacío si lo
	// que se cortó no fue un comodín sino las rutas que se resuelven).
	cutAt, cutWhy string
	memo          map[string]globOut
	res           map[[2]string]string // rutas ya resueltas: (ruta, carpeta) → ruta real
}

// globOut es una expansión hecha.
type globOut struct {
	matches  []string
	complete bool
}

const (
	whyTooMany = "alcanza demasiadas carpetas o archivos para que el gate lo expanda entero"
	whyTooLong = "es demasiado largo para que el gate lo lea"
	whyTooHard = "pide más trabajo del que el gate se permite para expandirlo"
)

// stop anota que un tope cortó el trabajo, y por qué si es el primero.
func (w *globWork) stop(why string) {
	w.cut = true
	if w.cutWhy == "" {
		w.cutWhy = why
	}
}

// spend gasta n unidades de comparación. Falla, y lo anota, si pasan del tope.
func (w *globWork) spend(n int) bool {
	if w.workOut || w.work > maxGlobWork-n {
		w.workOut = true
		w.stop(whyTooHard)
		return false
	}
	w.work += n
	return true
}

// open reserva la lectura de una carpeta. Falla, y lo anota, si ya se leyeron
// todas las que el tope permite. Los nombres los acota dirNames.
func (w *globWork) open() bool {
	if w.reads >= maxGlobReads {
		w.stop(whyTooMany)
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
		w.stop(whyTooMany)
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
		if !o.complete {
			w.cut = true
		}
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
				w.stop(whyTooMany)
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
			if len(part) > maxGlobPart {
				w.stop(whyTooLong)
				return nil
			}
			pat := newGlobPat(part)
			for _, b := range bases {
				if w.workOut || !w.open() {
					break
				}
				for _, name := range w.dirNames(b) {
					cost := (len(pat.runes) + 1) * (len(name) + 1)
					if !asciiOnly(name) {
						cost *= 2
					}
					if !w.spend(cost) {
						break
					}
					if pat.match(name) {
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
