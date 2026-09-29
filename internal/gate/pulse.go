package gate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Emmanuel93/coyote/internal/fsx"
)

// El latido es lo que el gate recuerda de las llamadas de cada IDE en un
// proyecto, para medir su nivel con el canario (ADR-0015). Vive en
// .coyote/gate/: nunca se versiona y ningún agente lo escribe.
const (
	pulseFile     = ".coyote/gate/ides.json"
	canaryFile    = ".coyote/gate/canary.json"
	canaryRanFile = ".coyote/gate/canary-ran.json"
	maxTools      = 64
	maxUnknown    = 32
	maxCanaries   = 32
	maxToolName   = 64
	maxPulseBytes = 1 << 20
)

// Pulse son las llamadas de un IDE al gate.
type Pulse struct {
	Last    time.Time            `json:"last"`
	Calls   int                  `json:"calls"`
	Tools   map[string]int       `json:"tools,omitempty"`
	Unknown []string             `json:"unknown,omitempty"` // herramientas que el gate no conoce
	Canary  map[string]time.Time `json:"canary,omitempty"`  // código del canario → cuándo lo negó
}

// Pulses son los latidos de todos los IDEs de un proyecto.
type Pulses struct {
	IDEs map[string]*Pulse `json:"ides"`
}

// LoadPulses lee los latidos; si no hay o no se pueden leer, empieza vacío.
func LoadPulses(root string) Pulses {
	p := Pulses{}
	if data, err := fsx.ReadFile(root, pulseFile, maxPulseBytes); err == nil {
		_ = json.Unmarshal(data, &p)
	}
	if p.IDEs == nil {
		p.IDEs = map[string]*Pulse{}
	}
	return p
}

// Record anota una llamada de un IDE: la herramienta, si el gate no la
// conoce y el canario, si la acción es la shell del IDE corriéndolo (el gate
// la niega siempre). Los nombres de herramientas se acortan: el archivo del
// latido tiene tope y no debe perder el canario.
func (p *Pulses) Record(a Action, now time.Time) {
	if a.IDE == "" {
		return
	}
	a.Tool = shortName(a.Tool)
	if p.IDEs == nil {
		p.IDEs = map[string]*Pulse{}
	}
	x := p.IDEs[a.IDE]
	if x == nil {
		x = &Pulse{}
		p.IDEs[a.IDE] = x
	}
	x.Last = now.UTC()
	x.Calls++
	if x.Tools == nil {
		x.Tools = map[string]int{}
	}
	if _, ok := x.Tools[a.Tool]; ok || len(x.Tools) < maxTools {
		x.Tools[a.Tool]++
	}
	if Unknown(a) && !contains(x.Unknown, a.Tool) && len(x.Unknown) < maxUnknown {
		x.Unknown = append(x.Unknown, a.Tool)
		sort.Strings(x.Unknown)
	}
	if code, ok := CanaryRun(a.Command); ok && Classify(a) == KindShell {
		if x.Canary == nil {
			x.Canary = map[string]time.Time{}
		}
		x.Canary[code] = now.UTC()
		trimTimes(x.Canary, maxCanaries)
	}
}

// shortName acorta un nombre de herramienta a maxToolName caracteres.
func shortName(s string) string {
	if r := []rune(s); len(r) > maxToolName {
		return string(r[:maxToolName]) + "…"
	}
	return s
}

// Unknown informa si el gate no conoce la herramienta: no es de shell, de
// archivos, de lectura ni MCP. Una herramienta así pide aprobación; el latido
// la anota para sumarla a la tabla del IDE.
func Unknown(a Action) bool {
	if Classify(a) != KindOther {
		return false
	}
	_, mcp := mcpName(a.Tool)
	return !mcp
}

// SavePulses escribe los latidos.
func SavePulses(root string, p Pulses) error {
	return writeState(root, pulseFile, p)
}

// CanaryRequest es un canario pedido con coyote doctor --ide X --canary.
type CanaryRequest struct {
	Code    string    `json:"code"`
	IDE     string    `json:"ide"`
	Created time.Time `json:"created"`
}

type canaryState struct {
	Requests []CanaryRequest `json:"requests"`
}

// LoadCanaries devuelve los canarios pedidos, del más viejo al más nuevo.
func LoadCanaries(root string) []CanaryRequest {
	var st canaryState
	if data, err := fsx.ReadFile(root, canaryFile, maxPulseBytes); err == nil {
		_ = json.Unmarshal(data, &st)
	}
	return st.Requests
}

// AddCanary guarda un canario pedido; se conservan los últimos.
func AddCanary(root string, r CanaryRequest) error {
	if !CanaryCode.MatchString(r.Code) {
		return fmt.Errorf("código de canario inválido")
	}
	reqs := append(LoadCanaries(root), r)
	if len(reqs) > maxCanaries {
		reqs = reqs[len(reqs)-maxCanaries:]
	}
	return writeState(root, canaryFile, canaryState{Requests: reqs})
}

// CanariesRan devuelve los canarios que llegaron a correr: código → cuándo.
func CanariesRan(root string) map[string]time.Time {
	ran := map[string]time.Time{}
	if data, err := fsx.ReadFile(root, canaryRanFile, maxPulseBytes); err == nil {
		_ = json.Unmarshal(data, &ran)
	}
	return ran
}

// MarkCanaryRan anota que un canario corrió: el gate lo niega siempre, así
// que el IDE lo ejecutó sin llamar al gate o sin respetar su negación.
func MarkCanaryRan(root, code string, now time.Time) error {
	if !CanaryCode.MatchString(code) {
		return fmt.Errorf("código de canario inválido")
	}
	ran := CanariesRan(root)
	ran[code] = now.UTC()
	trimTimes(ran, maxCanaries)
	return writeState(root, canaryRanFile, ran)
}

// Level es el nivel medido de un IDE en esta máquina.
type Level struct {
	Level  int // 1, 2 o 3; 0 si todavía no se midió
	Detail string
	At     time.Time
}

// Measure calcula el nivel de un IDE con su último canario:
//   - el gate lo negó y el comando no corrió: nivel 1;
//   - el gate lo negó y el comando corrió de todos modos: nivel 2;
//   - el comando corrió y el gate nunca se enteró: nivel 3.
//
// Sin canario, o mientras el agente no lo corre, queda sin medir.
func Measure(p Pulses, reqs []CanaryRequest, ran map[string]time.Time, ide string, now time.Time) Level {
	var req *CanaryRequest
	for i := range reqs {
		if reqs[i].IDE == ide {
			req = &reqs[i]
		}
	}
	x := p.IDEs[ide]
	if req == nil {
		if x == nil {
			return Level{Detail: fmt.Sprintf("el gate no tiene llamadas de %s en este proyecto; mídelo con coyote doctor --ide %s --canary", ide, ide)}
		}
		return Level{Detail: fmt.Sprintf("el gate recibe llamadas de %s (la última, %s); mide si respeta la negación con coyote doctor --ide %s --canary",
			ide, x.Last.Local().Format("2006-01-02 15:04"), ide)}
	}
	// Solo cuenta el hook del IDE medido: el canario que llega por el hook de
	// otro IDE no dice nada de este.
	var seenAt time.Time
	seen := false
	if x != nil {
		seenAt, seen = x.Canary[req.Code]
	}
	ranAt, didRun := ran[req.Code]
	switch {
	case seen && !didRun:
		return Level{Level: 1, At: seenAt, Detail: "nivel 1: el IDE llamó al gate y respetó la negación del canario"}
	case seen && didRun:
		return Level{Level: 2, At: ranAt, Detail: "nivel 2: el IDE llamó al gate, pero corrió el canario aunque el gate lo negó"}
	}
	var others []string
	for n, px := range p.IDEs {
		if px == nil || n == ide {
			continue
		}
		if _, ok := px.Canary[req.Code]; ok {
			others = append(others, n)
		}
	}
	sort.Strings(others)
	if len(others) > 0 {
		return Level{Detail: fmt.Sprintf("el canario %s llegó por el hook de %s, no por el de %s: pide uno nuevo con coyote doctor --ide %s --canary y córrelo desde %s",
			req.Code, strings.Join(others, " y "), ide, ide, ide)}
	}
	if didRun {
		return Level{Level: 3, At: ranAt, Detail: "nivel 3: el canario corrió y el gate nunca se enteró; el IDE no llama al gate (hooks apagados, carpeta sin confianza o IDE sin hooks)"}
	}
	msg := fmt.Sprintf("canario %s pendiente: pide al agente de %s que corra `coyote doctor canary %s` y vuelve a correr coyote doctor --ide %s", req.Code, ide, req.Code, ide)
	if x == nil || x.Last.Before(req.Created) {
		if now.Sub(req.Created) > 10*time.Minute {
			msg += fmt.Sprintf("; desde que lo pediste, el gate no recibió ninguna llamada de %s", ide)
		}
	}
	return Level{Detail: msg}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// trimTimes deja las n entradas más recientes.
func trimTimes(m map[string]time.Time, n int) {
	for len(m) > n {
		oldK, oldT := "", time.Time{}
		for k, t := range m {
			if oldK == "" || t.Before(oldT) {
				oldK, oldT = k, t
			}
		}
		delete(m, oldK)
	}
}

func writeState(root, rel string, v any) error {
	if err := fsx.NoSymlinks(root, rel); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return fsx.WriteAtomic(p, append(b, '\n'), 0o600)
}
