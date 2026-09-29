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

// CanaryOf busca el canario en una acción: el comando de una shell o
// cualquier texto de su entrada.
func CanaryOf(a Action) (string, bool) {
	if code, ok := Canary(a.Command); ok {
		return code, true
	}
	var texts []string
	allStrings(a.Input, &texts)
	for _, t := range texts {
		if code, ok := Canary(t); ok {
			return code, true
		}
	}
	return "", false
}

func canaryReason(code string) string {
	return fmt.Sprintf("es el canario de coyote doctor (%s) y el gate lo niega a propósito; avísale a la persona que el gate lo negó y no lo repitas", code)
}
