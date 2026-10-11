// Package version expone la versión de la herramienta; make la fija con -ldflags.
package version

import (
	"fmt"
	"strconv"
	"strings"
)

// Valores inyectados en el build.
var (
	Version = "0.1.0-dev"
	Commit  = "none"
	Date    = "unknown"
)

// String devuelve la versión completa.
func String() string {
	return Version + " (" + Commit + ", " + Date + ")"
}

// Release informa si el binario salió de make con una versión: un build sin
// ldflags (las pruebas, go run) no se compara contra requires.
func Release() bool { return Commit != "none" && Commit != "" }

// MajorMinor devuelve mayor y menor de una versión ("v1.2.3-dev.1" da 1, 2).
// La parte de prerelease no cuenta: un 1.0.0-dev ya es de la línea 1.0.
func MajorMinor(v string) (int, int, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, 0, false
	}
	var n [3]int
	for i, p := range parts {
		x, err := strconv.Atoi(p)
		if err != nil || x < 0 || x > 9999 {
			return 0, 0, false
		}
		n[i] = x
	}
	return n[0], n[1], true
}

// CheckRequires revisa una restricción ">=X.Y" contra esta versión. Devuelve
// un error claro si no alcanza o si la restricción no se entiende. Un build
// sin versión no se compara (ADR-0026).
func CheckRequires(constraint string) error {
	c := strings.TrimSpace(constraint)
	if c == "" {
		return nil
	}
	want, ok := strings.CutPrefix(c, ">=")
	if !ok {
		return fmt.Errorf("requires %q: usa >=MAYOR.MENOR, por ejemplo \">=1.0\"", constraint)
	}
	wm, wn, ok := MajorMinor(want)
	if !ok {
		return fmt.Errorf("requires %q: usa >=MAYOR.MENOR, por ejemplo \">=1.0\"", constraint)
	}
	if !Release() {
		return nil
	}
	hm, hn, ok := MajorMinor(Version)
	if !ok {
		return nil
	}
	if hm < wm || hm == wm && hn < wn {
		return fmt.Errorf("pide coyote %s y tienes %s: actualiza coyote (docs/reference/compatibilidad.md)", c, Version)
	}
	return nil
}

// Requires es la restricción que escribe coyote en un archivo nuevo: la línea
// del binario que lo creó, o "" en un build sin versión.
func Requires() string {
	if !Release() {
		return ""
	}
	m, n, ok := MajorMinor(Version)
	if !ok {
		return ""
	}
	return fmt.Sprintf(">=%d.%d", m, n)
}
