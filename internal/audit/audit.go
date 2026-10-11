// Package audit arma el reporte de auditoría de un proyecto desde el ledger,
// los registros de coyote/approvals y la historia de git (ADR-0024). Es de
// solo lectura, no usa red ni modelos y dice qué verifica y qué no: es
// evidencia para una auditoría, no una certificación.
package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Emmanuel93/coyote/internal/attribution"
	"github.com/Emmanuel93/coyote/internal/ccf"
	"github.com/Emmanuel93/coyote/internal/ledger"
	"github.com/Emmanuel93/coyote/internal/usage"
)

// Git corre un comando de lectura de git en la raíz del proyecto.
type Git func(args ...string) (string, error)

// Input es lo que el reporte lee.
type Input struct {
	Project     string
	Root        string
	From, To    time.Time // periodo [From, To)
	Now         time.Time
	Entries     []ledger.Entry
	Problems    []ledger.Problem
	Approvals   []Approval
	MonthlyUSD  float64
	Git         Git // nil: sin historia de git
	Attribution *attribution.Config
}

// Approval es un registro de coyote/approvals: la decisión de un gate de
// release (Gate) o la aprobación de una acción exacta (Type "action").
type Approval struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Gate        string `json:"gate"`
	Release     string `json:"release"`
	Decision    string `json:"decision"`
	Approver    string `json:"approver"`
	Date        string `json:"date"`
	Response    string `json:"response"`
	Object      string `json:"object"`
	Uses        int    `json:"uses"`
	TS          string `json:"ts"`
	Expires     string `json:"expires"`
	WS          string `json:"ws"`
	RequestedBy string `json:"requested_by"`
}

// LoadApprovals lee los registros de coyote/approvals.
func LoadApprovals(root string) ([]Approval, error) {
	dir := filepath.Join(root, "coyote", "approvals")
	files, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Approval
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") || !f.Type().IsRegular() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil || len(data) > 1<<20 {
			continue
		}
		var a Approval
		if json.Unmarshal(data, &a) == nil && a.ID != "" {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Resultados de una verificación.
const (
	OK   = "ok"
	Warn = "aviso"
	Fail = "falla"
	Info = "info"
)

// Check es una verificación de integridad con su detalle.
type Check struct {
	Name   string   `json:"name"`
	Result string   `json:"result"`
	Detail []string `json:"detail,omitempty"`
}

// Count es una fila de conteo y costo.
type Count struct {
	Key    string  `json:"key"`
	Events int     `json:"events"`
	Cost   float64 `json:"cost_usd"`
}

// Release es la decisión de un gate de release.
type Release struct {
	ID, Gate, Release, Decision, Approver, Date, RecordedBy string
}

// Report es el reporte completo.
type Report struct {
	Project    string    `json:"project"`
	From       time.Time `json:"from"`
	To         time.Time `json:"to"`
	Generated  time.Time `json:"generated"`
	Events     int       `json:"events"`
	People     []string  `json:"people"`
	Agents     []string  `json:"agents"`
	ByType     []Count   `json:"by_type"`
	ByActor    []Count   `json:"by_actor"`
	Releases   []Release `json:"releases"`
	Approved   int       `json:"approved"`
	Rejected   int       `json:"rejected"`
	GateUses   int       `json:"gate_uses"`
	GateQueued int       `json:"gate_queued"`
	GateDenied int       `json:"gate_denied"`
	Grants     []string  `json:"grants"`
	Autonomous []string  `json:"autonomous"`
	CostUSD    float64   `json:"cost_usd"`
	MonthlyUSD float64   `json:"monthly_usd"`
	ByPerson   []Count   `json:"by_person"`
	ByAgent    []Count   `json:"by_agent"`
	ByModel    []Count   `json:"by_model"`
	ByWS       []Count   `json:"by_workstream"`
	Checks     []Check   `json:"checks"`
	Commits    int       `json:"commits"`
	Signed     int       `json:"signed"`
	// Head y LedgerSHA256 fijan sobre qué se armó el reporte: comparados con
	// los del reporte anterior, delatan una historia reescrita.
	Head         string `json:"head,omitempty"`
	LedgerSHA256 string `json:"ledger_sha256"`
}

// Failed dice si alguna verificación falló.
func (r *Report) Failed() bool {
	for _, c := range r.Checks {
		if c.Result == Fail {
			return true
		}
	}
	return false
}

// Build arma el reporte.
func Build(in Input) *Report {
	r := &Report{Project: in.Project, From: in.From, To: in.To, Generated: in.Now}
	var period []ledger.Entry
	for _, e := range in.Entries {
		if !e.Line.TS.Before(in.From) && e.Line.TS.Before(in.To) {
			period = append(period, e)
		}
	}
	r.Events = len(period)
	byType, byActor := map[string]*Count{}, map[string]*Count{}
	byPerson, byAgent, byModel, byWS := map[string]*Count{}, map[string]*Count{}, map[string]*Count{}, map[string]*Count{}
	people, agents := map[string]bool{}, map[string]bool{}
	bump := func(m map[string]*Count, k string, cost float64) {
		if k == "" {
			k = "-"
		}
		if m[k] == nil {
			m[k] = &Count{Key: k}
		}
		m[k].Events++
		m[k].Cost += cost
	}
	approvals := map[string]Approval{}
	for _, a := range in.Approvals {
		approvals[a.ID] = a
	}
	links, recordedBy := linkApprovals(in.Entries, in.Approvals)
	for _, e := range period {
		l := e.Line
		cost := 0.0
		if l.Cost != nil && !ledger.SummaryTypes[l.Type] {
			cost = l.Cost.In + l.Cost.Out
		}
		person, agent := usage.Person(l.Actor), usage.Agent(l.Actor)
		people[person] = true
		if agent != "" && agent != "-" {
			agents[agent] = true
		}
		bump(byType, l.Type, cost)
		bump(byActor, l.Actor, cost)
		if cost > 0 {
			r.CostUSD += cost
			bump(byPerson, person, cost)
			bump(byAgent, agent, cost)
			bump(byModel, usage.Model(l), cost)
			bump(byWS, l.Project, cost)
		}
		switch l.Type {
		case "apr":
			r.Approved++
		case "rej":
			r.Rejected++
		case "gate":
			switch {
			case hasRef(l.Refs, "auth:ws"):
				r.Autonomous = append(r.Autonomous, fmt.Sprintf("%s %s: %s", l.TS.Format("2006-01-02"), l.Actor, l.What))
			case l.Status == "ok":
				r.GateUses++
			case l.Status == "pend":
				r.GateQueued++
			case l.Status == "fail":
				r.GateDenied++
			}
		}
	}
	for _, a := range in.Approvals {
		switch {
		case a.Gate != "" && dateIn(a.Date, in.From, in.To):
			r.Releases = append(r.Releases, Release{a.ID, a.Gate, a.Release, a.Decision, a.Approver, a.Date, recordedBy[a.ID]})
		case a.Type == "action" && inPeriod(a.TS, in.From, in.To) && (a.Uses > 1 || longGrant(a)):
			r.Grants = append(r.Grants, fmt.Sprintf("%s · %s · %d usos hasta %s · aprobó %s", a.ID, oneLine(a.Object, 80), a.Uses, a.Expires, a.Approver))
		}
	}
	for p := range people {
		r.People = append(r.People, p)
	}
	for a := range agents {
		r.Agents = append(r.Agents, a)
	}
	sort.Strings(r.People)
	sort.Strings(r.Agents)
	r.ByType, r.ByActor = sorted(byType), sorted(byActor)
	r.ByPerson, r.ByAgent, r.ByModel, r.ByWS = sorted(byPerson), sorted(byAgent), sorted(byModel), sorted(byWS)
	r.MonthlyUSD = in.MonthlyUSD
	r.Checks = append(r.Checks, checkAgentApprovals(period, approvals, links), checkReleaseEvents(r.Releases),
		checkFuture(in.Entries, in.Now), checkInvalid(in.Problems, in.Root))
	r.LedgerSHA256 = ledgerSum(in)
	if in.Git != nil {
		if out, err := in.Git("rev-parse", "HEAD"); err == nil {
			r.Head = strings.TrimSpace(out)
		}
		h, herr := readHistory(in)
		r.Checks = append(r.Checks, checkAppendOnly(in, h, herr), checkBackdated(in, h, herr), checkActors(in, period))
		c, commits, signed := checkCommits(in)
		r.Checks = append(r.Checks, c)
		r.Commits, r.Signed = commits, signed
	} else {
		r.Checks = append(r.Checks, Check{Name: "historia de git", Result: Warn, Detail: []string{"el proyecto no es un repo de git: no se pudo revisar que el ledger solo agregue"}})
	}
	return r
}

// dateIn dice si una fecha AAAA-MM-DD cae en el periodo.
func dateIn(date string, from, to time.Time) bool {
	d, err := time.Parse("2006-01-02", date)
	return err == nil && !d.Before(from.Truncate(24*time.Hour)) && d.Before(to)
}

// link es lo que respalda un evento apr o rej: los registros de
// coyote/approvals que lo respaldan y, si alguno no sirve, por qué.
type link struct {
	ids []string
	why []string
}

func entryKey(e ledger.Entry) string {
	return fmt.Sprintf("%s:%d:%s", e.File, e.LineNo, e.Line.String())
}

// linkApprovals vincula, en orden de fecha, cada apr y rej de todo el ledger
// con los registros que lo respaldan: los que cita (apr:P-0001) o, si no
// cita ninguno, el del gate que nombra al empezar. Un registro de un gate de
// release respalda un solo evento: el primero que nombra su gate, dice su
// decisión y no es anterior a ella. Así un agente no respalda una aprobación
// cualquiera citando el registro de otro gate. Devuelve también quién
// registró cada gate, con eventos de cualquier mes: un registro tardío
// cuenta para el mes de la decisión.
func linkApprovals(entries []ledger.Entry, list []Approval) (map[string]link, map[string]string) {
	byID := map[string]Approval{}
	for _, a := range list {
		byID[a.ID] = a
	}
	list = append([]Approval(nil), list...)
	sort.SliceStable(list, func(i, j int) bool {
		return list[i].Date < list[j].Date || list[i].Date == list[j].Date && list[i].ID < list[j].ID
	})
	ordered := append([]ledger.Entry(nil), entries...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Line.TS.Before(ordered[j].Line.TS) })
	links, claimed, recordedBy := map[string]link{}, map[string]string{}, map[string]string{}
	for _, e := range ordered {
		l := e.Line
		if l.Type != "apr" && l.Type != "rej" {
			continue
		}
		var lk link
		claim := func(a Approval) {
			if a.Gate != "" {
				claimed[a.ID] = l.TS.Format("2006-01-02 15:04Z")
				recordedBy[a.ID] = l.Actor
			}
			lk.ids = append(lk.ids, a.ID)
		}
		if cited := refs(l.Refs, "apr:"); len(cited) > 0 {
			// Los registros citados: todos tienen que respaldarlo.
			for _, id := range cited {
				a, ok := byID[id]
				switch {
				case !ok:
					lk.why = append(lk.why, id+" no existe en coyote/approvals/")
				case mismatch(l, a) != "":
					lk.why = append(lk.why, id+": "+mismatch(l, a))
				case a.Gate != "" && claimed[id] != "":
					lk.why = append(lk.why, id+" ya respalda el evento del "+claimed[id])
				default:
					claim(a)
				}
			}
		} else {
			// Por el gate que nombra: el primer registro libre que coincide. Un
			// gate rechazado y después aprobado tiene dos registros.
			first, _, _ := strings.Cut(strings.TrimSpace(l.What), " ")
			var whys []string
			for _, a := range list {
				if a.Gate == "" || a.Gate != first {
					continue
				}
				if why := mismatch(l, a); why != "" {
					whys = append(whys, a.ID+": "+why)
					continue
				}
				if at := claimed[a.ID]; at != "" {
					whys = append(whys, a.ID+" ya respalda el evento del "+at)
					continue
				}
				claim(a)
				break
			}
			if len(lk.ids) == 0 {
				lk.why = whys
			}
		}
		links[entryKey(e)] = lk
	}
	return links, recordedBy
}

// mismatch dice por qué un registro no respalda un evento, o "" si lo
// respalda. Para la aprobación de una acción exacta basta con que exista.
func mismatch(l ccf.Line, a Approval) string {
	if a.Gate == "" {
		return ""
	}
	first, _, _ := strings.Cut(strings.TrimSpace(l.What), " ")
	decision := strings.ToLower(a.Decision)
	switch {
	case first != a.Gate:
		return fmt.Sprintf("es del gate %s y el evento no lo nombra al empezar", a.Gate)
	case l.Type == "apr" && !strings.HasPrefix(decision, "approv") && !strings.HasPrefix(decision, "aprob"):
		return "el registro no aprueba"
	case l.Type == "rej" && !strings.HasPrefix(decision, "reject") && !strings.HasPrefix(decision, "rechaz"):
		return "el registro no rechaza"
	}
	if d, err := time.Parse("2006-01-02", a.Date); err == nil && l.TS.Before(d.AddDate(0, 0, -1)) {
		return "el evento es anterior a la decisión"
	}
	return ""
}

// ledgerSum es la huella del ledger hasta el fin del periodo: las líneas
// con fecha anterior a To, tal como están en HEAD (o en el disco, sin git).
// Regenerar un mes ya auditado da la misma huella, salvo que ese mes o uno
// anterior se haya reescrito o le hayan llegado líneas con fecha pasada.
func ledgerSum(in Input) string {
	h := sha256.New()
	add := func(file string, n int, text string) {
		if l, err := ccf.Parse(text); err == nil && l.TS.Before(in.To) {
			fmt.Fprintf(h, "%s:%d %s\n", file, n, text)
		}
	}
	if in.Git != nil {
		if out, err := in.Git("-c", "core.quotePath=false", "ls-tree", "-r", "-z", "--name-only", "HEAD", "--", ledgerDir); err == nil {
			files := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
			sort.Strings(files)
			for _, f := range files {
				if f == "" {
					continue
				}
				blob, err := in.Git("cat-file", "blob", "HEAD:./"+f)
				if err != nil {
					fmt.Fprintf(h, "%s: ilegible\n", f)
					continue
				}
				for i, line := range strings.Split(blob, "\n") {
					add(f, i+1, line)
				}
			}
			return hex.EncodeToString(h.Sum(nil))
		}
	}
	for _, e := range in.Entries {
		add(filepath.Base(e.File), e.LineNo, e.Line.String())
	}
	return hex.EncodeToString(h.Sum(nil))
}

func inPeriod(ts string, from, to time.Time) bool {
	t, err := time.Parse(time.RFC3339, ts)
	return err == nil && !t.Before(from) && t.Before(to)
}

// longGrant dice si una aprobación dura más de una hora: una concesión.
func longGrant(a Approval) bool {
	from, err1 := time.Parse(time.RFC3339, a.TS)
	to, err2 := time.Parse(time.RFC3339, a.Expires)
	return err1 == nil && err2 == nil && to.Sub(from) > time.Hour
}

func refs(rs []string, prefix string) []string {
	var out []string
	for _, r := range rs {
		if v, ok := strings.CutPrefix(r, prefix); ok {
			out = append(out, v)
		}
	}
	return out
}

func hasRef(rs []string, want string) bool {
	for _, r := range rs {
		if r == want {
			return true
		}
	}
	return false
}

func sorted(m map[string]*Count) []Count {
	out := make([]Count, 0, len(m))
	for _, c := range m {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Cost != out[j].Cost {
			return out[i].Cost > out[j].Cost
		}
		if out[i].Events != out[j].Events {
			return out[i].Events > out[j].Events
		}
		return out[i].Key < out[j].Key
	})
	return out
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// checkAgentApprovals: una aprobación o un rechazo que registró un agente
// necesita un registro que lo respalde, con la respuesta de la persona.
func checkAgentApprovals(period []ledger.Entry, approvals map[string]Approval, links map[string]link) Check {
	c := Check{Name: "aprobaciones registradas por un agente", Result: OK}
	for _, e := range period {
		l := e.Line
		if (l.Type != "apr" && l.Type != "rej") || usage.Agent(l.Actor) == "" || usage.Agent(l.Actor) == "-" {
			continue
		}
		lk := links[entryKey(e)]
		when := l.TS.Format("2006-01-02 15:04Z")
		if len(lk.ids) == 0 || len(lk.why) > 0 {
			why := "no cita ningún registro de coyote/approvals/"
			if len(lk.why) > 0 {
				why = strings.Join(lk.why, "; ")
			}
			c.Result = Fail
			c.Detail = append(c.Detail, fmt.Sprintf("%s %s registró %q sin un registro que lo respalde: %s", when, l.Actor, oneLine(l.What, 60), why))
			continue
		}
		answered, gated := true, true
		for _, id := range lk.ids {
			if strings.TrimSpace(approvals[id].Response) == "" {
				answered = false
			}
			if approvals[id].Gate == "" {
				gated = false
			}
		}
		if !gated {
			// Un agente no aprueba acciones: solo registra la decisión de un
			// gate, y un registro sin gate respaldaría cualquier cosa.
			c.Result = Fail
			c.Detail = append(c.Detail, fmt.Sprintf("%s %s registró %q con %s, que no es el registro de un gate", when, l.Actor, oneLine(l.What, 60), strings.Join(lk.ids, ", ")))
			continue
		}
		if !answered {
			c.Result = Fail
			c.Detail = append(c.Detail, fmt.Sprintf("%s %s registró %s: el registro no tiene la respuesta de la persona", when, l.Actor, strings.Join(lk.ids, ", ")))
			continue
		}
		if c.Result == OK {
			c.Result = Warn
		}
		c.Detail = append(c.Detail, fmt.Sprintf("%s %s registró %s: la respuesta de la persona está en coyote/approvals/", when, l.Actor, strings.Join(lk.ids, ", ")))
	}
	return c
}

// checkReleaseEvents: cada decisión de un gate de release del periodo tiene
// su evento apr en el ledger (A3), aunque se haya registrado después.
func checkReleaseEvents(releases []Release) Check {
	c := Check{Name: "gates de release con su evento en el ledger", Result: OK}
	for _, x := range releases {
		if x.RecordedBy != "" {
			continue
		}
		c.Result = Warn
		c.Detail = append(c.Detail, fmt.Sprintf("%s (%s) está en coyote/approvals/ pero no tiene evento apr en el ledger", x.Gate, x.ID))
	}
	return c
}

// checkFuture: un evento con fecha futura afloja topes y ventanas.
func checkFuture(entries []ledger.Entry, now time.Time) Check {
	c := Check{Name: "eventos con fecha futura", Result: OK}
	for _, e := range entries {
		if e.Line.TS.After(now.Add(5 * time.Minute)) {
			c.Result = Fail
			c.Detail = append(c.Detail, fmt.Sprintf("%s:%d %s", filepath.Base(e.File), e.LineNo, e.Line.TS.Format("2006-01-02 15:04Z")))
		}
	}
	return c
}

func checkInvalid(probs []ledger.Problem, root string) Check {
	c := Check{Name: "líneas inválidas del ledger", Result: OK}
	for _, p := range probs {
		c.Result = Fail
		c.Detail = append(c.Detail, fmt.Sprintf("%s:%d %v", rel(root, p.File), p.LineNo, p.Err))
	}
	return c
}

func rel(root, p string) string {
	if r, err := filepath.Rel(root, p); err == nil {
		return filepath.ToSlash(r)
	}
	return p
}

// filePerson saca la persona del nombre del archivo del ledger (DD-persona.ccf).
func filePerson(p string) string {
	base := strings.TrimSuffix(path.Base(filepath.ToSlash(p)), ".ccf")
	if _, person, ok := strings.Cut(base, "-"); ok {
		return "@" + person
	}
	return ""
}
