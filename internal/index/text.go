package index

import (
	"strings"
	"unicode"
)

// fold quita acentos y diacríticos comunes del español y otras lenguas latinas.
var fold = strings.NewReplacer(
	"á", "a", "à", "a", "ä", "a", "â", "a", "ã", "a",
	"é", "e", "è", "e", "ë", "e", "ê", "e",
	"í", "i", "ì", "i", "ï", "i", "î", "i",
	"ó", "o", "ò", "o", "ö", "o", "ô", "o", "õ", "o",
	"ú", "u", "ù", "u", "ü", "u", "û", "u",
	"ñ", "n", "ç", "c",
)

// stopwords son palabras demasiado comunes para distinguir un fragmento.
var stopwords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`
		a al algo como con contra cual cuando de del desde donde el ella ellas ellos en entre era es esa ese eso esta
		este esto estos estas fue hay la las le les lo los mas me mi mis muy no nos o para pero por que se sea ser si
		sin sobre solo son su sus tambien te tiene tu un una uno unos unas y ya yo
		an and are as at be been but by can do does for from has have how if in into is it its no not of on or so
		than that the their them then there these they this to was we were what when where which who why will with
		you your`) {
		stopwords[w] = true
	}
}

// Tokens normaliza un texto en términos: minúsculas, sin acentos, sin palabras
// vacías y con una reducción mínima de plurales en español e inglés.
func Tokens(s string) []string {
	s = fold.Replace(strings.ToLower(s))
	var out []string
	for _, w := range strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len(w) < 2 || stopwords[w] {
			continue
		}
		out = append(out, stem(w))
	}
	return out
}

// stem reduce plurales comunes: decisiones → decision, luces → luz,
// papeles → papel, pedidos → pedido, orders → order.
func stem(w string) string {
	switch {
	case len(w) > 5 && strings.HasSuffix(w, "iones"):
		return w[:len(w)-2]
	case len(w) > 4 && strings.HasSuffix(w, "ces"):
		return w[:len(w)-3] + "z"
	case len(w) > 4 && strings.HasSuffix(w, "es") && strings.ContainsRune("lrndj", rune(w[len(w)-3])):
		return w[:len(w)-2]
	case len(w) > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") && !strings.HasSuffix(w, "us") && !strings.HasSuffix(w, "is"):
		return w[:len(w)-1]
	}
	return w
}
