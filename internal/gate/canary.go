package gate

import (
	"fmt"
	"regexp"
)

// El canario mide el nivel de un IDE (ADR-0015). coyote doctor --ide pide que
// el agente corra "coyote doctor canary <código>": el gate lo niega siempre.
// Si el IDE llamó al gate y el comando no corrió, el IDE respeta el gate; si
// el comando corrió de todos modos, el IDE ignora la negación.

// canaryRe reconoce el canario en un comando o en cualquier texto.
var canaryRe = regexp.MustCompile(`(?:^|[^A-Za-z0-9_.-])coyote["']?\s+doctor\s+canary\s+["']?([a-z0-9]{6,32})\b`)

// CanaryCode valida el código de un canario.
var CanaryCode = regexp.MustCompile(`^[a-z0-9]{6,32}$`)

// Canary devuelve el código del canario si el texto lo corre.
func Canary(text string) (string, bool) {
	m := canaryRe.FindStringSubmatch(text)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// CanaryRun devuelve el código si el comando de una shell corre el canario
// como programa (coyote doctor canary <código>, también dentro de bash -c). Un
// echo, un patrón de búsqueda o un texto que solo lo nombran no cuentan: el
// nivel se mide con lo que el IDE habría ejecutado.
func CanaryRun(cmd string) (string, bool) {
	for _, seg := range view(cmd, false) {
		prog, at := mainProg(seg)
		if prog != "coyote" {
			continue
		}
		args := seg[at+1:]
		for len(args) >= 2 && args[0] == "-C" {
			args = args[2:]
		}
		if len(args) >= 3 && args[0] == "doctor" && args[1] == "canary" && CanaryCode.MatchString(args[2]) {
			return args[2], true
		}
	}
	return "", false
}

func canaryReason(code string) string {
	return fmt.Sprintf("es el canario de coyote doctor (%s) y el gate lo niega a propósito; avísale a la persona que el gate lo negó y no lo repitas", code)
}
