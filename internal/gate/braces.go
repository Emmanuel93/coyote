package gate

import (
	"strconv"
	"strings"
)

// Las llaves se expanden antes que cualquier otra cosa: `cat .{env,x}` lee
// .env, y `rm -rf {a,coyote}` borra el gobierno del proyecto. El gate expande
// las de una palabra como el shell (el producto de los grupos, los anidados y
// las secuencias {1..3}, {a..c}), con topes, y cuando un tope recorta no deja
// fuera nada sin avisar: agrega la palabra con cada grupo cambiado por *, que
// alcanza todo lo que se recortó.

const (
	// maxBraceWords acota cuántas combinaciones de una palabra se siguen.
	maxBraceWords = 64
	// maxBraceDepth acota cuántos grupos caben uno dentro de otro.
	maxBraceDepth = 16
)

// braceItem es un trozo de una palabra con llaves: texto, un grupo con comas
// ({a,b}) o una secuencia ({1..3}).
type braceItem struct {
	lit  string        // texto
	alts [][]braceItem // un grupo con comas: cada alternativa
	seq  []string      // una secuencia, ya expandida (hasta maxBraceWords)
	kind byte          // 'l' texto, 'g' grupo, 's' secuencia
}

// braceParser parte una palabra en trozos. match[i] es la posición del }
// que cierra el { de i, o -1 si no cierra.
type braceParser struct {
	w     string
	match []int
	over  bool // un grupo no se pudo leer entero (demasiado hondo, secuencia enorme)
}

func newBraceParser(w string) *braceParser {
	p := &braceParser{w: w, match: make([]int, len(w))}
	for i := range p.match {
		p.match[i] = -1
	}
	var stack []int
	for i := 0; i < len(w); i++ {
		switch w[i] {
		case '{':
			stack = append(stack, i)
		case '}':
			if n := len(stack); n > 0 {
				p.match[stack[n-1]] = i
				stack = stack[:n-1]
			}
		}
	}
	return p
}

// parse lee w[from:to]. Un { que no cierra, un } suelto y un grupo sin comas ni
// secuencia son texto; lo que hay dentro de un grupo sin comas se lee igual.
func (p *braceParser) parse(from, to, depth int) []braceItem {
	var items []braceItem
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			items = append(items, braceItem{kind: 'l', lit: lit.String()})
			lit.Reset()
		}
	}
	for i := from; i < to; {
		c := p.w[i]
		j := -1
		if c == '{' {
			j = p.match[i]
		}
		if j < 0 || j >= to {
			lit.WriteByte(c)
			i++
			continue
		}
		// Los cortes de nivel superior del grupo.
		var cuts []int
		for k := i + 1; k < j; k++ {
			switch p.w[k] {
			case '{':
				if m := p.match[k]; m > k && m < j {
					k = m
				}
			case ',':
				cuts = append(cuts, k)
			}
		}
		switch {
		case depth >= maxBraceDepth:
			// Demasiado hondo para seguirlo: el grupo queda como texto y se avisa.
			p.over = true
			flush()
			items = append(items, braceItem{kind: 's', seq: []string{p.w[i : j+1]}})
		case len(cuts) > 0:
			flush()
			g := braceItem{kind: 'g'}
			start := i + 1
			for _, k := range append(cuts, j) {
				g.alts = append(g.alts, p.parse(start, k, depth+1))
				start = k + 1
			}
			items = append(items, g)
		default:
			if seq, over, ok := braceSequence(p.w[i+1 : j]); ok {
				flush()
				p.over = p.over || over
				items = append(items, braceItem{kind: 's', seq: seq})
			} else {
				lit.WriteByte('{')
				flush()
				items = append(items, p.parse(i+1, j, depth+1)...)
				lit.WriteByte('}')
			}
		}
		i = j + 1
	}
	flush()
	return items
}

// braceSequence lee el interior de {x..y} y {x..y..paso}: enteros o un solo
// carácter. Devuelve los elementos (a lo sumo maxBraceWords) y si hubo más.
func braceSequence(in string) (out []string, over, ok bool) {
	parts := strings.Split(in, "..")
	if len(parts) != 2 && len(parts) != 3 {
		return nil, false, false
	}
	step := int64(1)
	if len(parts) == 3 {
		s, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			return nil, false, false
		}
		if s < 0 {
			s = -s
		}
		if s == 0 {
			s = 1
		}
		step = s
	}
	lo, err1 := strconv.ParseInt(parts[0], 10, 64)
	hi, err2 := strconv.ParseInt(parts[1], 10, 64)
	if err1 == nil && err2 == nil {
		width := 0
		for _, s := range parts[:2] {
			if t := strings.TrimLeft(s, "+-"); len(t) > 1 && t[0] == '0' && len(s) > width {
				width = len(s)
			}
		}
		dir := int64(1)
		if hi < lo {
			dir = -1
		}
		for v, n := lo, 0; ; v += dir * step {
			if n == maxBraceWords {
				return out, true, true
			}
			s := strconv.FormatInt(v, 10)
			for len(s) < width {
				s = "0" + s
			}
			out = append(out, s)
			n++
			if (dir > 0 && v+step > hi) || (dir < 0 && v-step < hi) {
				break
			}
		}
		return out, false, true
	}
	if len(parts[0]) == 1 && len(parts[1]) == 1 && parts[0][0] < 0x80 && parts[1][0] < 0x80 {
		a, b := int64(parts[0][0]), int64(parts[1][0])
		dir := int64(1)
		if b < a {
			dir = -1
		}
		for v, n := a, 0; ; v += dir * step {
			if n == maxBraceWords {
				return out, true, true
			}
			out = append(out, string(rune(v)))
			n++
			if (dir > 0 && v+step > b) || (dir < 0 && v-step < b) {
				break
			}
		}
		return out, false, true
	}
	return nil, false, false
}

// options son las cadenas que un trozo puede ser, a lo sumo limit.
func (it braceItem) options(limit int) (out []string, over bool) {
	switch it.kind {
	case 'g':
		for _, alt := range it.alts {
			sub, ov := expandItems(alt, limit)
			over = over || ov
			out = append(out, sub...)
			if len(out) > limit {
				return out[:limit], true
			}
		}
		return out, over
	case 's':
		return it.seq, false
	}
	return []string{it.lit}, false
}

// expandItems da las combinaciones de los trozos en el orden del shell (el
// primer grupo cambia más despacio), a lo sumo limit; una combinación puede ser
// la cadena vacía. El producto se cuenta sin armarlo: el tiempo es el de las
// combinaciones que se devuelven, no el de todas.
func expandItems(items []braceItem, limit int) (out []string, over bool) {
	opts := make([][]string, len(items))
	total := 1
	for i, it := range items {
		o, ov := it.options(limit)
		over = over || ov
		opts[i] = o
		if len(o) == 0 {
			o = []string{""}
			opts[i] = o
		}
		if total <= limit {
			total *= len(o)
		}
	}
	if total > limit {
		total, over = limit, true
	}
	idx := make([]int, len(items))
	for k := 0; k < total; k++ {
		var b strings.Builder
		for i := range items {
			b.WriteString(opts[i][idx[i]])
		}
		out = append(out, b.String())
		for i := len(items) - 1; i >= 0; i-- {
			if idx[i]++; idx[i] < len(opts[i]) {
				break
			}
			idx[i] = 0
		}
	}
	return out, over
}

func expandable(items []braceItem) bool {
	for _, it := range items {
		if it.kind == 'g' || it.kind == 's' {
			return true
		}
	}
	return false
}

// wildItems arma la palabra con cada grupo y cada secuencia cambiados por *.
func wildItems(items []braceItem) string {
	var b strings.Builder
	for _, it := range items {
		if it.kind == 'l' {
			b.WriteString(it.lit)
		} else {
			b.WriteByte('*')
		}
	}
	return b.String()
}

// expandBraces expande las llaves de una palabra como el shell ({apply,destroy},
// a{b,c}d{e,f}, {a,{b,c}}, {1..3}). Un {x} sin comas ni secuencia es texto. A
// lo sumo maxBraceWords combinaciones; si hay más, devuelve las primeras y, al
// final, la palabra con cada grupo cambiado por *.
func expandBraces(w string) []string {
	if !strings.Contains(w, "{") {
		return []string{w}
	}
	p := newBraceParser(w)
	items := p.parse(0, len(w), 0)
	if !expandable(items) {
		return []string{w}
	}
	all, over := expandItems(items, maxBraceWords)
	var out []string
	for _, s := range all {
		// Las palabras vacías ({,} o {a,}) desaparecen, como en el shell.
		if s != "" {
			out = append(out, s)
		}
	}
	if over || p.over {
		out = append(out, wildItems(items))
	}
	return out
}

// wildBraces es la palabra con cada grupo de llaves cambiado por *.
func wildBraces(w string) string {
	if !strings.Contains(w, "{") {
		return w
	}
	return wildItems(newBraceParser(w).parse(0, len(w), 0))
}
