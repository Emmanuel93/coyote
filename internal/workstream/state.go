package workstream

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Emmanuel93/coyote/internal/fsx"
	"github.com/Emmanuel93/coyote/internal/ledger"
)

// Estados de un paso.
const (
	Pending = "pending" // no ha corrido, o la corrida no llegó a arrancar
	Review  = "review"  // corrió bien y espera la revisión de la persona
	Done    = "done"    // la persona lo aceptó; en un paso suyo, lo hizo
	Failed  = "failed"  // la última corrida falló
	Blocked = "blocked" // la corrida dejó acciones en la cola del gate
	Redo    = "redo"    // la persona pidió rehacerlo
)

// Motivos por los que el motor se detiene.
const (
	StopReview   = "review"   // un paso espera la revisión de la persona
	StopHuman    = "human"    // el paso siguiente lo hace la persona
	StopFailed   = "failed"   // un paso falló
	StopBlocked  = "blocked"  // un paso espera decisiones en la cola del gate
	StopBudget   = "budget"   // el plan llegó a su tope
	StopEvaluate = "evaluate" // autonomous terminó: quien lo autorizó evalúa (A4)
	StopDone     = "done"     // todos los pasos están hechos
)

// MinRunUSD es lo mínimo que tiene que quedar del plan para lanzar un paso.
const MinRunUSD = 0.01

// StepState es un paso con lo que el ledger dice de él.
type StepState struct {
	Step
	Status   string    `json:"status"`
	Doc      string    `json:"doc,omitempty"`      // último artefacto
	Feedback string    `json:"feedback,omitempty"` // revisión de la persona para rehacerlo
	Why      string    `json:"why,omitempty"`      // qué pasó en la última corrida que no terminó bien
	Runs     int       `json:"runs"`
	CostUSD  float64   `json:"cost_usd"`
	At       time.Time `json:"at,omitempty"`
}

// State es el plan con el estado de cada paso.
type State struct {
	Plan  *Plan
	Steps []StepState
	Spent float64 // costo estimado de todas las corridas del workstream
	Runs  int
}

func ref(refs []string, key string) string {
	for _, r := range refs {
		if v, ok := strings.CutPrefix(r, key+":"); ok {
			return v
		}
	}
	return ""
}

// Fold deriva el estado del plan de los eventos del ledger (ya ordenados por
// tiempo): run con step: marca cómo terminó cada corrida, apr la revisión de
// la persona y rej su pedido de rehacerlo. Las corridas sin step: del mismo
// workstream (coyote run --ws) cuentan para el presupuesto del plan.
func Fold(p *Plan, entries []ledger.Entry) *State {
	s := &State{Plan: p}
	idx := map[string]int{}
	for i, st := range p.Steps {
		s.Steps = append(s.Steps, StepState{Step: st, Status: Pending})
		idx[st.ID] = i
	}
	for _, e := range entries {
		l := e.Line
		if l.Project != p.ID {
			continue
		}
		cost := 0.0
		if l.Cost != nil {
			cost = l.Cost.Total()
		}
		if l.Type == "run" && l.Status != "skip" {
			s.Spent += cost
			s.Runs++
		}
		i, ok := idx[ref(l.Refs, "step")]
		if !ok {
			continue
		}
		st := &s.Steps[i]
		doc := ref(l.Refs, "doc")
		switch l.Type {
		case "run":
			st.At = l.TS
			if l.Status == "skip" {
				st.Why = l.What // no llegó a correr: el paso sigue como estaba
				continue
			}
			st.Runs++
			st.CostUSD += cost
			if doc != "" {
				st.Doc = doc
			}
			switch l.Status {
			case "ok":
				st.Status, st.Why = Review, ""
			case "pend":
				st.Status, st.Why = Blocked, l.What
			default:
				st.Status, st.Why = Failed, l.What
			}
		case "apr":
			st.Status, st.Why, st.Feedback, st.At = Done, "", "", l.TS
		case "rej":
			st.Status, st.Why, st.Feedback, st.At = Redo, "", doc, l.TS
		}
	}
	return s
}

// Remaining es lo que queda del tope del plan (negativo si se pasó) y si el
// plan tiene tope.
func (s *State) Remaining() (float64, bool) {
	if s.Plan.BudgetUSD <= 0 {
		return 0, false
	}
	return s.Plan.BudgetUSD - s.Spent, true
}

// Next dice qué hace el motor en un modo: correr el primer paso pendiente o
// detenerse, y por qué. En autonomous pasa de largo los pasos por revisar,
// salvo los que piden gate: human; la persona los evalúa al final.
type Next struct {
	Run    bool
	Index  int    // paso que corre o donde se detiene; -1 si no hay
	Reason string // por qué se detiene
}

// Next calcula el siguiente movimiento del motor.
func (s *State) Next(mode string) Next {
	last := -1
	for i, st := range s.Steps {
		switch st.Status {
		case Done:
			continue
		case Review:
			if mode == "autonomous" && st.Gate != "human" {
				last = i
				continue
			}
			return Next{Index: i, Reason: StopReview}
		case Failed:
			return Next{Index: i, Reason: StopFailed}
		case Blocked:
			return Next{Index: i, Reason: StopBlocked}
		}
		if st.IsHuman() {
			return Next{Index: i, Reason: StopHuman}
		}
		if r, capped := s.Remaining(); capped && r < MinRunUSD {
			return Next{Index: i, Reason: StopBudget}
		}
		return Next{Run: true, Index: i}
	}
	if last >= 0 {
		return Next{Index: last, Reason: StopEvaluate}
	}
	return Next{Index: -1, Reason: StopDone}
}

// ToReview son los pasos por revisar hasta el índice dado, inclusive.
func (s *State) ToReview(upTo int) []int {
	var out []int
	for i := 0; i <= upTo && i < len(s.Steps); i++ {
		if s.Steps[i].Status == Review {
			out = append(out, i)
		}
	}
	return out
}

// Label es el estado en palabras.
func Label(status string) string {
	switch status {
	case Pending:
		return "pendiente"
	case Review:
		return "por revisar"
	case Done:
		return "hecho"
	case Failed:
		return "falló"
	case Blocked:
		return "en la cola del gate"
	case Redo:
		return "por rehacer"
	}
	return status
}

var headingRe = regexp.MustCompile(`^#{1,6}\s+(.+?)\s*#*\s*$`)

// MissingSections devuelve las secciones del esquema de salida que el texto
// no trae como títulos de Markdown. Un título que empieza con la sección
// cuenta ("## Riesgos (3)" cumple "Riesgos"); los bloques de código no.
func MissingSections(text string, sections []string) []string {
	var heads []string
	fence := ""
	for _, l := range strings.Split(text, "\n") {
		t := strings.TrimSpace(l)
		if fence != "" {
			if strings.HasPrefix(t, fence) {
				fence = ""
			}
			continue
		}
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fence = t[:3]
			continue
		}
		if m := headingRe.FindStringSubmatch(t); m != nil {
			heads = append(heads, strings.ToLower(strings.Trim(m[1], "*_ ")))
		}
	}
	var out []string
	for _, sec := range sections {
		want := strings.ToLower(strings.TrimSpace(sec))
		found := false
		for _, h := range heads {
			if strings.HasPrefix(h, want) {
				found = true
				break
			}
		}
		if !found {
			out = append(out, sec)
		}
	}
	return out
}

// Lock evita dos motores sobre el mismo workstream en esta copia del
// proyecto. El lock lleva el proceso que lo tiene; uno de un proceso que ya
// terminó se recupera.
func Lock(root, id string) (func(), error) {
	rel := ".coyote/ws/" + id + ".lock"
	if !IDRe.MatchString(id) {
		return nil, fmt.Errorf("workstream %q inválido", id)
	}
	if err := fsx.NoSymlinks(root, rel); err != nil {
		return nil, err
	}
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 3; attempt++ {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			fmt.Fprintf(f, "%d\n", os.Getpid())
			f.Close()
			return func() { _ = os.Remove(p) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		data, _ := os.ReadFile(p)
		if pid, _ := strconv.Atoi(strings.TrimSpace(string(data))); pid > 0 && alive(pid) {
			return nil, fmt.Errorf("el motor ya corre %s en el proceso %d; espera a que termine", id, pid)
		}
		_ = os.Remove(p) // quedó de un proceso que ya no existe
	}
	return nil, fmt.Errorf("no pude tomar el lock de %s", id)
}

func alive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
