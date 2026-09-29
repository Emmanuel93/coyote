package ci

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Emmanuel93/coyote/internal/fsx"
	"github.com/Emmanuel93/coyote/internal/glob"
)

// CodeownersFiles son las ubicaciones que GitHub revisa, en su orden.
var CodeownersFiles = []string{".github/CODEOWNERS", "CODEOWNERS", "docs/CODEOWNERS"}

type ownerRule struct {
	patterns []string
	owners   []string
}

// Owners son las reglas de un CODEOWNERS: la última que coincide manda.
type Owners struct {
	File  string
	rules []ownerRule
}

var (
	ownerRe = regexp.MustCompile(`^@[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:/[A-Za-z0-9._-]+)?$`)
	emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
)

// ReadCodeowners busca el CODEOWNERS de un repo. Sin archivo devuelve nil.
func ReadCodeowners(dir string) *Owners {
	for _, f := range CodeownersFiles {
		p := filepath.Join(dir, filepath.FromSlash(f))
		info, err := os.Lstat(p)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 3<<20 {
			continue
		}
		data, err := fsx.ReadCapped(p, fsx.MaxText)
		if err != nil {
			continue
		}
		o := ParseCodeowners(string(data))
		o.File = f
		return o
	}
	return nil
}

// ParseCodeowners lee las reglas. Un dueño es @persona, @org/equipo o un
// correo; un correo queda como dueño aunque no se pueda comparar con quien
// aprueba en GitHub: esa regla no la aprueba cualquiera.
func ParseCodeowners(text string) *Owners {
	o := &Owners{}
	for _, raw := range strings.Split(text, "\n") {
		l := strings.TrimSpace(raw)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if i := strings.Index(l, " #"); i >= 0 {
			l = l[:i]
		}
		fields := strings.Fields(strings.ReplaceAll(l, `\ `, "\x00"))
		if len(fields) == 0 || strings.HasPrefix(fields[0], "!") {
			continue // GitHub no admite negaciones
		}
		pat := strings.ReplaceAll(fields[0], "\x00", " ")
		var owners []string
		for _, f := range fields[1:] {
			if ownerRe.MatchString(f) || emailRe.MatchString(f) {
				owners = append(owners, strings.ToLower(f))
			}
		}
		o.rules = append(o.rules, ownerRule{patterns: ownerPatterns(pat), owners: owners})
	}
	return o
}

// ownerPatterns traduce un patrón de CODEOWNERS a los de internal/glob: con
// barra al inicio o en medio se ancla a la raíz; sin barra vale en cualquier
// nivel; con barra al final es un directorio. Un nombre sin comodín al final
// también cubre lo que haya debajo, si es un directorio.
func ownerPatterns(p string) []string {
	anchored := strings.HasPrefix(p, "/")
	p = strings.TrimPrefix(p, "/")
	dir := strings.HasSuffix(p, "/")
	p = strings.TrimSuffix(p, "/")
	if p == "" || p == "*" || p == "**" {
		return []string{"**"}
	}
	if !anchored && !strings.Contains(p, "/") {
		p = "**/" + p
	}
	if dir {
		return []string{p + "/**"}
	}
	last := p[strings.LastIndex(p, "/")+1:]
	if strings.ContainsAny(last, "*?") {
		return []string{p}
	}
	return []string{p, p + "/**"}
}

// For devuelve los dueños de una ruta: los de la última regla que coincide.
// Una regla sin dueños deja la ruta sin dueño.
func (o *Owners) For(path string) []string {
	if o == nil {
		return nil
	}
	var owners []string
	for _, r := range o.rules {
		if glob.Any(r.patterns, path) {
			owners = r.owners
		}
	}
	return owners
}
