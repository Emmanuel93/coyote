package gate

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/Emmanuel93/coyote/internal/infra"
)

// Verdict es lo que decide la política antes de buscar aprobaciones.
type Verdict int

const (
	Allow         Verdict = iota // pasa sin aprobación: solo lee
	NeedsApproval                // necesita una aprobación de la acción exacta
	Block                        // bloqueado siempre, aun con aprobación
)

func (v Verdict) String() string {
	return [...]string{"permitido", "necesita aprobación", "bloqueado"}[v]
}

// Decision es el resultado de evaluar una acción.
type Decision struct {
	Verdict Verdict
	Kind    Kind
	Reason  string // por qué necesita aprobación o por qué se bloquea
	Normalized
}

// Evaluate aplica la política: primero lo que se bloquea siempre, después lo
// que solo lee y, para el resto, el hash de la acción exacta.
func (ps Paths) Evaluate(a Action) Decision {
	a = ps.absCwd(a)
	d := Decision{Kind: Classify(a)}
	var h *Hard
	switch d.Kind {
	case KindRead:
		h = ps.readTool(a)
		if h == nil {
			if why := execFlags(a); why != "" {
				// Una búsqueda que corre programas deja de ser lectura.
				d.Kind, d.Reason = KindOther, why
				break
			}
			return d // Allow
		}
	case KindShell:
		if h = ps.hardShell(a); h == nil {
			ok, why, cred := ps.readShell(a)
			switch {
			case ok:
				return d
			case cred:
				h = hard("%s", why)
			default:
				d.Reason = why
			}
		}
	case KindFile:
		h = ps.hardFile(a)
	default:
		h = ps.hardOther(a)
	}
	d.Normalized = ps.Normalize(a)
	if h != nil {
		d.Verdict, d.Reason = Block, h.Reason
		return d
	}
	d.Verdict = NeedsApproval
	if d.Kind == KindShell && infra.Effect(neutralize(a.Command)) {
		d.Reason = "cambia infraestructura: se aprueba de a un uso"
	}
	if d.Reason == "" {
		d.Reason = fmt.Sprintf("%s con efectos", d.Kind)
	}
	return d
}

// InfraEffect dice si un comando cambia infraestructura: su aprobación vale
// para un solo uso (ADR-0017).
func InfraEffect(cmd string) bool { return infra.Effect(neutralize(cmd)) }

// Respond escribe la respuesta en el formato del IDE y devuelve el código de
// salida del hook. En todos, 2 bloquea. Cursor además espera JSON; Copilot lee
// permissionDecision y trata cualquier salida distinta de 0 como negación.
// Dejar pasar nunca aprueba en nombre del IDE: sale 0 sin decisión y el IDE
// sigue con sus propios permisos.
func Respond(ide string, allow bool, message string, stdout, stderr io.Writer) int {
	switch ide {
	case IDECursor:
		out := map[string]string{"permission": "allow"}
		if !allow {
			out = map[string]string{"permission": "deny", "user_message": message, "agent_message": message}
		}
		b, _ := json.Marshal(out)
		fmt.Fprintln(stdout, string(b))
	case IDECopilot:
		if !allow {
			b, _ := json.Marshal(map[string]string{"permissionDecision": "deny", "permissionDecisionReason": message})
			fmt.Fprintln(stdout, string(b))
		}
	}
	if allow {
		return 0
	}
	fmt.Fprintln(stderr, message)
	return 2
}
