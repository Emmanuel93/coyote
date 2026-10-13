package gate

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

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

// maxShellBytes es el largo máximo de un comando de shell que el gate lee. Pasado
// el tope no lo analiza: lo pide a la persona, que lo ve entero con coyote review.
// Por debajo se lee todo, con los bloqueos duros. Es una variable para que las
// pruebas la bajen.
var maxShellBytes = 128 << 10

// whyShellTooLong es por qué pide aprobación un comando que pasa de maxShellBytes.
const whyShellTooLong = "comando demasiado largo para que el gate lo lea entero; lo decide la persona, que lo ve con coyote review"

// EvalTimeout es el plazo que el hook se da para decidir una acción. Los IDE
// cortan un hook a los 30 s y, salvo Cursor, dejan pasar la acción: pasado este
// plazo el gate no espera más y la acción pide aprobación.
var EvalTimeout = 10 * time.Second

// HookDeadline es el plazo total del hook de un IDE, de que arranca a que
// responde: leer la entrada, revisar R15, decidir y anotar. Los IDE matan el
// hook a los 30 s y, salvo Cursor con failClosed, dejan pasar lo que no
// respondió; a los 20 s el hook niega por su cuenta (o mantiene el paso que ya
// había dado) y sale, sea lo que sea que lo tenga detenido.
var HookDeadline = 20 * time.Second

// minIdentifyWait es lo menos que se espera para identificar una acción que ya
// pasó el plazo (su hash), aunque el plazo sea más corto.
const minIdentifyWait = time.Second

// EvaluateWithin es Evaluate con un plazo. Si no termina a tiempo, la acción pide
// la aprobación de una persona (con su hash exacto, así puede encolarse): lo que
// el gate no alcanzó a leer no lo da por permitido. El trabajo ya está acotado
// por topes contados; el plazo es la última red, por si algo se escapa (un disco
// lento, un sistema de archivos remoto). Si ni describir la acción cabe en un
// segundo plazo, se bloquea sin hash: nada se encola y nada pasa. Un pánico del
// gate llega a quien lo llamó, que bloquea por seguridad.
func (ps Paths) EvaluateWithin(a Action, limit time.Duration) Decision {
	return withinDeadline(a, limit, ps.Evaluate, ps.Normalize)
}

// withinDeadline es EvaluateWithin con las dos tareas a la vista, para probarlo.
func withinDeadline(a Action, limit time.Duration, eval func(Action) Decision, norm func(Action) Normalized) Decision {
	type result struct {
		d        Decision
		n        Normalized
		panicked bool
		v        any
	}
	run := func(f func() result) <-chan result {
		ch := make(chan result, 1)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					ch <- result{panicked: true, v: r}
				}
			}()
			ch <- f()
		}()
		return ch
	}
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case r := <-run(func() result { return result{d: eval(a)} }):
		if r.panicked {
			panic(r.v)
		}
		return r.d
	case <-timer.C:
	}
	// Pasó el plazo. La propuesta lleva el hash de la acción exacta.
	timer.Reset(max(limit, minIdentifyWait))
	select {
	case r := <-run(func() result { return result{n: norm(a)} }):
		if !r.panicked {
			return Decision{Kind: Classify(a), Verdict: NeedsApproval, Normalized: r.n,
				Reason: fmt.Sprintf("el gate no alcanzó a leerla en %s; lo decide la persona, que ve la acción", limit.Round(time.Millisecond))}
		}
	case <-timer.C:
	}
	return Decision{Kind: Classify(a), Verdict: Block,
		Reason: fmt.Sprintf("el gate no alcanzó a leerla ni a identificarla en %s; bloquea por seguridad y la persona decide en su terminal", limit.Round(time.Millisecond))}
}

// Evaluate aplica la política: primero lo que se bloquea siempre, después lo
// que solo lee y, para el resto, el hash de la acción exacta.
func (ps Paths) Evaluate(a Action) Decision {
	a = ps.absCwd(a)
	ps.glob = &globWork{}
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
			if ps.glob.cut {
				d.Kind, d.Reason = KindOther, ps.globCutReason()
				break
			}
			return d // Allow
		}
	case KindShell:
		if len(a.Command) > maxShellBytes {
			// Un comando así no se lee: ni los bloqueos duros alcanzarían a verlo
			// entero en el plazo del hook. Lo ve la persona.
			d.Normalized = ps.Normalize(a)
			d.Verdict, d.Reason = NeedsApproval, whyShellTooLong
			return d
		}
		if h = ps.hardShell(a); h == nil {
			ok, why, cred := ps.readShell(a)
			switch {
			case ok && ps.glob.cut:
				d.Reason = ps.globCutReason()
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
	if d.Kind == KindShell && InfraEffect(a.Command) {
		d.Reason = "cambia infraestructura: se aprueba de a un uso"
	}
	if d.Reason == "" {
		d.Reason = fmt.Sprintf("%s con efectos", d.Kind)
	}
	return d
}

// maxCutShown es cuánto del comodín que cortó la lectura cabe en el motivo.
const maxCutShown = 60

// globCutReason es por qué pide aprobación lo que se leería con un comodín (o
// con tantas rutas) que el gate no alcanzó a expandir entero: lo que no vio no
// lo da por permitido.
func (ps Paths) globCutReason() string {
	w := ps.glob
	why := w.cutWhy
	if why == "" {
		why = whyTooMany
	}
	if w.cutAt == "" {
		// Lo que se cortó no fue un comodín sino las rutas que se resuelven.
		return fmt.Sprintf("el comando %s; lo decide la persona, que lo ve", why)
	}
	return fmt.Sprintf("un comodín (%q) %s; lo decide la persona, que ve el comando", shortGlob(w.cutAt), why)
}

// shortGlob acorta un patrón para mostrarlo: el motivo no lleva un comodín de
// miles de caracteres, sino el trozo donde aparece el primer comodín.
func shortGlob(p string) string {
	r := []rune(p)
	if len(r) <= maxCutShown {
		return p
	}
	at := 0
	for i, c := range r {
		if c == '*' || c == '?' || c == '[' {
			at = i
			break
		}
	}
	from := max(0, at-maxCutShown/2)
	to := min(len(r), from+maxCutShown)
	out := string(r[from:to])
	if from > 0 {
		out = "…" + out
	}
	if to < len(r) {
		out += "…"
	}
	return out
}

// InfraEffect dice si un comando cambia infraestructura: su aprobación vale
// para un solo uso (ADR-0017).
func InfraEffect(cmd string) bool {
	for _, t := range cmdTexts(cmd) {
		if infra.Effect(t) {
			return true
		}
	}
	return false
}

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
