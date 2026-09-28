package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Emmanuel93/coyote/internal/agents"
	"github.com/Emmanuel93/coyote/internal/approval"
	"github.com/Emmanuel93/coyote/internal/ccf"
	"github.com/Emmanuel93/coyote/internal/fsx"
	"github.com/Emmanuel93/coyote/internal/gitx"
	"github.com/Emmanuel93/coyote/internal/identity"
	"github.com/Emmanuel93/coyote/internal/ledger"
	"github.com/Emmanuel93/coyote/internal/project"
	"github.com/Emmanuel93/coyote/internal/runner"
	"github.com/Emmanuel93/coyote/internal/tokens"
	"github.com/Emmanuel93/coyote/internal/workstream"
)

// coyote ws ejecuta el plan de un workstream (ADR-0014). Cada paso corre con
// el núcleo de coyote run; sus artefactos pasan a los pasos siguientes y el
// motor se detiene donde el modo lo pide:
//
//   - manual: un paso por cada coyote ws run; ws continue solo registra la revisión.
//   - supervised: después de cada paso; ws continue registra la revisión y corre el siguiente.
//   - autonomous: en un paso con gate: human, un paso de la persona, el tope
//     del plan o una falla. Exige la rama ws/<W> y la autorización de ese plan
//     exacto; al final lo evalúa quien lo autorizó (A4).
//
// En los tres modos lo que tiene efectos pasa por el gate y ningún agente se
// aprueba. El estado sale del ledger.

func cmdWS(a *app, args []string) error {
	const usage = "uso: coyote ws check|status|run|continue <W>"
	if len(args) == 0 {
		return fail(2, usage)
	}
	switch args[0] {
	case "check":
		return a.wsCheck(args[1:])
	case "status":
		return a.wsStatus(args[1:])
	case "run":
		return a.wsRun(args[1:])
	case "continue":
		return a.wsContinue(args[1:])
	}
	return fail(2, usage)
}

// wsCtx es un workstream con su plan, listo para el motor.
type wsCtx struct {
	root       string
	cfg        *project.Config
	plan       *workstream.Plan
	mode       string
	person     identity.Person
	authorized bool
}

// loadWS lee el plan y lo revisa.
func (a *app) loadWS(id string) (*wsCtx, []workstream.Finding, error) {
	if !workstream.IDRe.MatchString(id) {
		return nil, nil, fail(2, "workstream %q inválido: usa W-0001", id)
	}
	root, cfg, err := a.project()
	if err != nil {
		return nil, nil, err
	}
	plan, err := workstream.Load(root, id)
	if err != nil {
		return nil, nil, fail(1, "%v", err)
	}
	c := &wsCtx{root: root, cfg: cfg, plan: plan, person: identity.Resolve(root)}
	c.mode, _ = workstream.Mode(plan.Autonomy, cfg.Autonomy)
	findings := plan.Check(checkOptions(root, cfg))
	if !contains(gateInstalled(root), "claude-code") {
		findings = append(findings, workstream.Finding{Level: "aviso", Msg: "el gate no está instalado para Claude Code: los pasos no corren sin él; corre coyote install --ide claude-code"})
	}
	if c.mode == "autonomous" {
		if br := gitx.Branch(root); !wsBranch(br, id) {
			findings = append(findings, workstream.Finding{Level: "aviso", Msg: fmt.Sprintf("autonomous corre en la rama ws/%s y estás en %s (A4)", id, orDash(br))})
		}
	}
	return c, findings, nil
}

func checkOptions(root string, cfg *project.Config) workstream.CheckOptions {
	o := workstream.CheckOptions{Project: cfg.Autonomy, Exists: func(rel string) bool { return fsx.Regular(root, rel) }}
	if set, err := agents.Load(root); err == nil {
		o.Agents = map[string]bool{}
		for _, n := range set.Names() {
			o.Agents[n] = true
		}
		if contains(gateInstalled(root), "claude-code") {
			o.Installed = map[string]bool{}
			for n := range o.Agents {
				o.Installed[n] = fsx.Regular(root, ".claude/agents/"+n+".md")
			}
		}
	}
	return o
}

// lower baja el modo para esta corrida; nunca lo sube.
func (c *wsCtx) lower(m string) error {
	if m == "" {
		return nil
	}
	r := workstream.Rank(m)
	switch {
	case r < 0:
		return fail(2, "--mode %q inválido: manual o supervised", m)
	case r > workstream.Rank(c.mode):
		return fail(2, "--mode %s pide más autonomía que la del plan (%s): eso se cambia en plan.yaml y en coyote/project.yaml", m, c.mode)
	}
	c.mode = m
	return nil
}

func wsBranch(branch, id string) bool {
	b := strings.TrimPrefix(branch, "refs/heads/")
	return b == "ws/"+id || strings.HasPrefix(b, "ws/"+id+"-") || strings.HasPrefix(b, "ws/"+id+"/")
}

func (a *app) wsState(c *wsCtx) (*workstream.State, error) {
	entries, _, err := ledger.Open(c.root).ReadAll()
	if err != nil {
		return nil, err
	}
	return workstream.Fold(c.plan, entries), nil
}

func printPlanFindings(a *app, fs []workstream.Finding) {
	for _, f := range fs {
		mark := "!"
		if f.Level == "error" {
			mark = "✗"
		}
		fmt.Fprintf(a.stdout, "  %s %s\n", mark, f.String())
	}
}

func refuseInvalid(c *wsCtx, fs []workstream.Finding) error {
	if n := workstream.Errors(fs); n > 0 {
		return fail(1, "el plan tiene %d %s (A2): corrige %s; coyote ws check %s los muestra", n, pluralWord(n, "error", "errores"), c.plan.Rel, c.plan.ID)
	}
	return nil
}

// ---- ws check ----

func (a *app) wsCheck(args []string) error {
	fs := a.flags("ws check", "<W> [--json]")
	asJSON := fs.Bool("json", false, "salida en JSON")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		fs.Usage()
		return fail(2, "")
	}
	c, findings, err := a.loadWS(pos[0])
	if err != nil {
		return err
	}
	if *asJSON {
		b, _ := json.MarshalIndent(map[string]any{"id": c.plan.ID, "mode": c.mode, "findings": nonNil(findings), "errors": workstream.Errors(findings)}, "", "  ")
		fmt.Fprintln(a.stdout, string(b))
	} else {
		p := c.plan
		fmt.Fprintf(a.stdout, "%s · %s\n", p.ID, orDash(p.Title))
		fmt.Fprintf(a.stdout, "Modo %s (plan %s, proyecto %s) · %s · %s\n\n", c.mode, orManual(p.Autonomy), orManual(c.cfg.Autonomy), budgetText(p.BudgetUSD), plural(len(p.Steps), "paso", "pasos"))
		tw := table(a.stdout)
		fmt.Fprintln(tw, "  PASO\tAGENTE\tRIESGO\tTOPE\tENTRADAS\tREVISIÓN")
		for _, s := range p.Steps {
			top, gate := "-", "-"
			if !s.IsHuman() && s.MaxUSD > 0 {
				top = fmt.Sprintf("$%.2f", s.MaxUSD)
			}
			if s.Gate == "human" {
				gate = "siempre"
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\n", s.ID, s.Agent, p.StepRisk(s), top, orDash(shortText(strings.Join(s.Input, ", "), 50)), gate)
		}
		tw.Flush()
		fmt.Fprintln(a.stdout)
		if len(findings) == 0 {
			fmt.Fprintln(a.stdout, "Sin hallazgos: el plan cumple el contrato de cada paso (A2).")
		} else {
			printPlanFindings(a, findings)
			n := workstream.Errors(findings)
			fmt.Fprintf(a.stdout, "\n%s, %s.\n", plural(n, "error", "errores"), plural(len(findings)-n, "aviso", "avisos"))
		}
	}
	if n := workstream.Errors(findings); n > 0 {
		return fail(1, "")
	}
	return nil
}

func nonNil(fs []workstream.Finding) []workstream.Finding {
	if fs == nil {
		return []workstream.Finding{}
	}
	return fs
}

func orManual(m string) string {
	if m == "" {
		return "manual"
	}
	return m
}

func budgetText(usd float64) string {
	if usd <= 0 {
		return "sin tope de plan"
	}
	return fmt.Sprintf("tope del plan $%.2f", usd)
}

// ---- ws status ----

func (a *app) wsStatus(args []string) error {
	fs := a.flags("ws status", "[W] [--json]")
	asJSON := fs.Bool("json", false, "salida en JSON")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 1 {
		fs.Usage()
		return fail(2, "")
	}
	if len(pos) == 0 {
		return a.wsList(*asJSON)
	}
	c, findings, err := a.loadWS(pos[0])
	if err != nil {
		return err
	}
	st, err := a.wsState(c)
	if err != nil {
		return err
	}
	nx := st.Next(c.mode)
	hint := wsHint(c, st, nx)
	if *asJSON {
		out := map[string]any{"id": c.plan.ID, "title": c.plan.Title, "mode": c.mode, "budget_usd": c.plan.BudgetUSD,
			"spent_usd": round4(st.Spent), "runs": st.Runs, "steps": st.Steps, "findings": nonNil(findings),
			"next": map[string]any{"run": nx.Run, "step": stepAt(st, nx.Index), "reason": nx.Reason, "hint": hint}}
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Fprintln(a.stdout, string(b))
		return nil
	}
	fmt.Fprintf(a.stdout, "%s · %s · %s · %s\n\n", c.plan.ID, orDash(c.plan.Title), c.mode, spentText(st))
	tw := table(a.stdout)
	fmt.Fprintln(tw, "PASO\tAGENTE\tESTADO\tCORRIDAS\tCOSTO\tARTEFACTO")
	for _, s := range st.Steps {
		runs, cost := "-", "-"
		if s.Runs > 0 {
			runs, cost = strconv.Itoa(s.Runs), fmt.Sprintf("$%.4f", s.CostUSD)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", s.ID, s.Agent, workstream.Label(s.Status), runs, cost, orDash(s.Doc))
	}
	tw.Flush()
	if n := workstream.Errors(findings); n > 0 {
		fmt.Fprintf(a.stdout, "\nEl plan tiene %s: coyote ws check %s.\n", plural(n, "error", "errores"), c.plan.ID)
	}
	fmt.Fprintf(a.stdout, "\nSigue: %s\n", hint)
	return nil
}

func round4(f float64) float64 { return float64(int64(f*10000+0.5)) / 10000 }

func stepAt(st *workstream.State, i int) string {
	if i < 0 || i >= len(st.Steps) {
		return ""
	}
	return st.Steps[i].ID
}

func spentText(st *workstream.State) string {
	if st.Plan.BudgetUSD > 0 {
		return fmt.Sprintf("$%.2f de $%.2f del plan", st.Spent, st.Plan.BudgetUSD)
	}
	return fmt.Sprintf("$%.2f gastados, sin tope de plan", st.Spent)
}

func (a *app) wsList(asJSON bool) error {
	root, cfg, err := a.project()
	if err != nil {
		return err
	}
	ids, err := workstream.All(root)
	if err != nil {
		return err
	}
	entries, _, err := ledger.Open(root).ReadAll()
	if err != nil {
		return err
	}
	type row struct {
		ID     string  `json:"id"`
		Title  string  `json:"title"`
		Mode   string  `json:"mode"`
		Done   int     `json:"done"`
		Steps  int     `json:"steps"`
		Spent  float64 `json:"spent_usd"`
		Budget float64 `json:"budget_usd"`
		Next   string  `json:"next"`
		Error  string  `json:"error,omitempty"`
	}
	rows := []row{}
	for _, id := range ids {
		plan, err := workstream.Load(root, id)
		if err != nil {
			rows = append(rows, row{ID: id, Error: err.Error()})
			continue
		}
		mode, _ := workstream.Mode(plan.Autonomy, cfg.Autonomy)
		st := workstream.Fold(plan, entries)
		r := row{ID: id, Title: plan.Title, Mode: mode, Steps: len(st.Steps), Spent: round4(st.Spent), Budget: plan.BudgetUSD}
		for _, s := range st.Steps {
			if s.Status == workstream.Done {
				r.Done++
			}
		}
		nx := st.Next(mode)
		switch {
		case nx.Run:
			r.Next = st.Steps[nx.Index].ID + " · por correr"
		case nx.Reason == workstream.StopDone:
			r.Next = "completo"
		case nx.Reason == workstream.StopClosed:
			r.Next = "cerrado"
		case nx.Reason == workstream.StopBudget:
			r.Next = st.Steps[nx.Index].ID + " · tope del plan"
		case nx.Reason == workstream.StopHuman:
			r.Next = st.Steps[nx.Index].ID + " · lo haces tú"
		case nx.Reason == workstream.StopEvaluate:
			r.Next = "evaluar lo que corrió en autónomo"
		default:
			r.Next = st.Steps[nx.Index].ID + " · " + workstream.Label(st.Steps[nx.Index].Status)
		}
		rows = append(rows, r)
	}
	if asJSON {
		b, _ := json.MarshalIndent(rows, "", "  ")
		fmt.Fprintln(a.stdout, string(b))
		return nil
	}
	if len(rows) == 0 {
		fmt.Fprintf(a.stdout, "No hay workstreams con plan en %s/.\n", workstream.Base)
		return nil
	}
	tw := table(a.stdout)
	fmt.Fprintln(tw, "WORKSTREAM\tTÍTULO\tMODO\tPASOS\tGASTO\tSIGUE")
	for _, r := range rows {
		if r.Error != "" {
			fmt.Fprintf(tw, "%s\t\t\t\t\tplan inválido: %s\n", r.ID, shortText(r.Error, 60))
			continue
		}
		spent := fmt.Sprintf("$%.2f", r.Spent)
		if r.Budget > 0 {
			spent += fmt.Sprintf(" de $%.2f", r.Budget)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d/%d\t%s\t%s\n", r.ID, shortText(r.Title, 40), r.Mode, r.Done, r.Steps, spent, r.Next)
	}
	return tw.Flush()
}

// wsHint dice qué sigue, con el comando.
func wsHint(c *wsCtx, st *workstream.State, nx workstream.Next) string {
	id := c.plan.ID
	if nx.Run {
		s := st.Steps[nx.Index]
		return fmt.Sprintf("%s (%s, %s): corre con coyote ws run %s", s.ID, s.Agent, s.Does, id)
	}
	var s workstream.StepState
	if nx.Index >= 0 && nx.Index < len(st.Steps) {
		s = st.Steps[nx.Index]
	}
	switch nx.Reason {
	case workstream.StopReview:
		return fmt.Sprintf("%s espera tu revisión: lee %s; si está bien, coyote ws continue %s; si no, coyote ws continue %s --redo \"qué cambiar\"", s.ID, orDash(s.Doc), id, id)
	case workstream.StopHuman:
		return fmt.Sprintf("%s lo haces tú (%s); cuando termines, coyote ws continue %s", s.ID, s.Does, id)
	case workstream.StopFailed:
		why := ""
		if s.Why != "" {
			why = " (" + s.Why + ")"
		}
		return fmt.Sprintf("%s falló%s: revisa %s; repítelo con coyote ws continue %s --retry o pide cambios con --redo \"qué cambiar\"", s.ID, why, orDash(s.Doc), id)
	case workstream.StopBlocked:
		return fmt.Sprintf("%s dejó acciones en la cola del gate: decídelas con coyote approvals, coyote review y coyote approve o reject; luego coyote ws continue %s y el agente repite las aprobadas", s.ID, id)
	case workstream.StopBudget:
		return fmt.Sprintf("el plan llegó a su tope (%s): para seguir, sube budget_usd en %s", spentText(st), c.plan.Rel)
	case workstream.StopEvaluate:
		var ids []string
		for _, i := range st.ToReview(len(st.Steps) - 1) {
			ids = append(ids, st.Steps[i].ID)
		}
		return fmt.Sprintf("el plan corrió en autónomo: evalúa %s en la rama ws/%s y acéptalos con coyote ws continue %s antes del merge (A4)", strings.Join(ids, ", "), id, id)
	case workstream.StopDone:
		return fmt.Sprintf("el plan está completo: ciérralo con coyote close %s", id)
	case workstream.StopClosed:
		return fmt.Sprintf("%s está cerrado (coyote close): para seguir, abre otro workstream", id)
	}
	return nx.Reason
}

// ---- ws run ----

func (a *app) wsRun(args []string) error {
	fs := a.flags("ws run", "<W> [--mode manual|supervised] [--dry-run]")
	modeFlag := fs.String("mode", "", "corre con menos autonomía que la del plan: manual o supervised")
	dry := fs.Bool("dry-run", false, "muestra el paso que correría, con la decisión del router y la entrada, sin correr")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		fs.Usage()
		return fail(2, "")
	}
	c, findings, err := a.loadWS(pos[0])
	if err != nil {
		return err
	}
	if err := refuseInvalid(c, findings); err != nil {
		printPlanFindings(a, findings)
		return err
	}
	if err := c.lower(*modeFlag); err != nil {
		return err
	}
	if !*dry {
		if s := sessionIDE(); s != "" {
			return fail(1, "coyote ws no corre dentro de una sesión de agente (%s): la persona lanza el motor desde su terminal", s)
		}
		unlock, err := workstream.Lock(c.root, c.plan.ID)
		if err != nil {
			return fail(1, "%v", err)
		}
		defer unlock()
	}
	return a.wsLoop(c, nil, *dry)
}

// forced es la corrida que pide la persona con ws continue: repetir un paso,
// rehacerlo con su revisión o retomar la sesión que dejó acciones en la cola.
type forced struct {
	index  int
	resume string
}

// wsLoop corre pasos hasta que el modo, el plan o una falla lo detienen.
func (a *app) wsLoop(c *wsCtx, f *forced, dry bool) error {
	ran := 0
	var stop workstream.Next
	var stepErr error
	for guard := 0; ; guard++ {
		st, err := a.wsState(c)
		if err != nil {
			return err
		}
		nx := st.Next(c.mode)
		if f != nil {
			nx = workstream.Next{Run: true, Index: f.index}
		}
		if guard > 2*len(c.plan.Steps)+2 {
			return fail(1, "el motor dio demasiadas vueltas sin avanzar en %s; revisa coyote ws status %s", c.plan.ID, c.plan.ID)
		}
		if !nx.Run {
			stop = nx
			if dry {
				fmt.Fprintf(a.stdout, "No corre ningún paso ahora. %s\n", capFirst(wsHint(c, st, nx)))
				return nil
			}
			break
		}
		if r, capped := st.Remaining(); capped && r < workstream.MinRunUSD {
			stop = workstream.Next{Index: nx.Index, Reason: workstream.StopBudget}
			break
		}
		if c.mode == "autonomous" && !c.authorized {
			if dry {
				fmt.Fprintf(a.stdout, "En autonomous, antes de correr pide tu autorización de este plan exacto (A4) y la rama ws/%s.\n\n", c.plan.ID)
			} else if err := a.wsAuthorize(c); err != nil {
				return err
			}
		}
		sp, err := a.stepSpec(c, st, nx.Index, f)
		f = nil
		if err != nil {
			if !dry {
				s := st.Steps[nx.Index]
				ev := runEvent{agent: s.Agent, ws: c.plan.ID, step: s.ID, scope: s.Scope, task: s.Does, status: "skip", why: "sin entrada", refs: []string{"mode:" + c.mode}}
				_ = a.recordRun(c.root, c.cfg, ev)
			}
			return err
		}
		sp.dry = dry
		if !dry {
			fmt.Fprintf(a.stderr, "%s · paso %s (%s)\n", c.plan.ID, sp.step, c.mode)
		}
		out, err := a.execRun(c.root, c.cfg, sp)
		if dry || out == nil {
			return err
		}
		ran++
		if out.status == "skip" {
			// No arrancó (presupuesto del mes, gate o agente sin instalar): el
			// paso sigue igual y el motor se detiene con el motivo.
			return err
		}
		if err != nil {
			stepErr = err
		}
	}
	st, err := a.wsState(c)
	if err != nil {
		return err
	}
	if ran > 0 {
		if err := a.wsRecordStop(c, st, stop, ran); err != nil {
			return err
		}
	}
	fmt.Fprintf(a.stdout, "\n%s se detiene. %s\n", c.plan.ID, capFirst(wsHint(c, st, stop)))
	switch {
	case stepErr != nil:
		return stepErr
	case stop.Reason == workstream.StopBudget || stop.Reason == workstream.StopFailed || stop.Reason == workstream.StopClosed:
		return fail(1, "")
	}
	return nil
}

func capFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 {
		return s
	}
	return strings.ToUpper(string(r)) + s[n:]
}

// wsRecordStop deja en el ledger dónde y por qué se detuvo el motor.
func (a *app) wsRecordStop(c *wsCtx, st *workstream.State, stop workstream.Next, ran int) error {
	status := "ok"
	switch stop.Reason {
	case workstream.StopFailed:
		status = "fail"
	case workstream.StopBlocked:
		status = "pend"
	case workstream.StopBudget:
		status = "skip"
	}
	what := fmt.Sprintf("motor %s: %s, se detiene por %s", c.mode, plural(ran, "paso", "pasos"), stopWord(stop.Reason))
	refs := []string{"mode:" + c.mode, "stop:" + stop.Reason}
	if id := stepAt(st, stop.Index); id != "" {
		refs = append(refs, "at:"+safeRef(id))
	}
	return a.wsRecord(c, "plan", what, status, refs...)
}

func stopWord(reason string) string {
	switch reason {
	case workstream.StopReview:
		return "revisión"
	case workstream.StopHuman:
		return "paso de la persona"
	case workstream.StopFailed:
		return "falla"
	case workstream.StopBlocked:
		return "acciones en la cola"
	case workstream.StopBudget:
		return "tope del plan"
	case workstream.StopEvaluate:
		return "evaluación"
	case workstream.StopDone:
		return "plan completo"
	}
	return reason
}

func (a *app) wsRecord(c *wsCtx, typ, what, status string, refs ...string) error {
	line := ccf.Line{TS: a.now(), Actor: c.person.Actor(""), Project: c.plan.ID, Repo: c.cfg.Name, Type: typ, Scope: "-",
		What: ccf.ShortWhat(safeLedgerText(what), ccf.MaxWhatWords), Refs: refs, Status: status}
	_, err := ledger.Open(c.root).Append(line, c.person.Slug)
	return err
}

// wsAuthorize exige lo que A4 pide a un loop autónomo: la rama ws/<W> y la
// autorización de la persona para este plan exacto, atada a su hash. Sin
// ella, la pide en la cola del gate y no corre.
func (a *app) wsAuthorize(c *wsCtx) error {
	id := c.plan.ID
	if !gitx.IsRepo(c.root) {
		return fail(1, "A4: autonomous corre en una rama ws/%s y el proyecto no es un repo git", id)
	}
	if br := gitx.Branch(c.root); !wsBranch(br, id) {
		return fail(1, "A4: autonomous corre en la rama ws/%s y estás en %s: crea la rama con git switch -c ws/%s", id, orDash(br), id)
	}
	ac, err := a.approvalCtx()
	if err != nil {
		return err
	}
	unlock, err := approval.Lock(c.root)
	if err != nil {
		return err
	}
	defer unlock()
	hash := c.plan.AuthHash(c.cfg.Name)
	sts, _, err := ac.statuses()
	if err != nil {
		return err
	}
	if st, ok := ac.store.Find(hash, sts); ok {
		// Cada arranque del motor autónomo gasta un uso de la autorización.
		if err := a.record(ac, "gate", id, "motor autónomo autorizado: "+id, []string{"apr:" + st.ID, "hash:" + shortHash(hash), "auth:ws"}); err != nil {
			return err
		}
		c.authorized = true
		fmt.Fprintf(a.stderr, "autonomous: %s lo autorizó (%s; quedan %s)\n", st.Approver, st.ID, plural(st.Left-1, "uso", "usos"))
		return nil
	}
	p, isNew, err := ac.store.Enqueue(approval.Proposal{Hash: hash, Tool: "coyote ws", Kind: "ws", Path: c.plan.Rel,
		Object:      fmt.Sprintf("motor autónomo de %s: %s, %s, rama ws/%s", id, plural(len(c.plan.Steps), "paso", "pasos"), budgetText(c.plan.BudgetUSD), id),
		Input:       map[string]any{"plan": string(c.plan.Raw), "branch": gitx.Branch(c.root), "budget_usd": c.plan.BudgetUSD},
		RequestedBy: ac.person.Actor(""),
		Reason:      "A4: un loop autónomo corre solo con la autorización de este plan exacto; si el plan cambia, la autorización deja de valer"})
	if err != nil {
		return err
	}
	if isNew {
		if err := a.wsRecord(c, "gate", "en cola: motor autónomo de "+id, "pend", "prop:"+p.ID, "hash:"+shortHash(hash), "auth:ws"); err != nil {
			return err
		}
	}
	return fail(1, "%s corre en autónomo solo con tu autorización de este plan exacto (A4).\nRevísalo con coyote review %s, autorízalo con coyote approve %s [--uses N] [--for 8h] y vuelve a correr coyote ws run %s.", id, p.ID, p.ID, id)
}

// stepSpec arma la corrida de un paso: su contrato, sus entradas y el tope
// que queda del plan.
func (a *app) stepSpec(c *wsCtx, st *workstream.State, i int, f *forced) (runSpec, error) {
	s := st.Steps[i]
	task := strings.TrimSpace(s.Task)
	if task == "" {
		task = s.Does
	}
	title := c.plan.ID
	if c.plan.Title != "" {
		title += ", " + c.plan.Title
	}
	sp := runSpec{agent: s.Agent, task: task, ws: c.plan.ID, step: s.ID, risk: c.plan.StepRisk(s.Step), scope: s.Scope,
		maxUSD: s.MaxUSD, maxTurns: s.MaxTurns, sections: s.Sections, refs: []string{"mode:" + c.mode},
		heading: fmt.Sprintf("Paso %s de %s (%d de %d), en modo %s.", s.ID, title, i+1, len(st.Steps), c.mode)}
	if r, capped := st.Remaining(); capped {
		sp.capUSD = r
	}
	var out strings.Builder
	for _, o := range s.Output {
		out.WriteString("- " + o + "\n")
	}
	if len(s.Sections) > 0 {
		fmt.Fprintf(&out, "\nTu entrega tiene que traer estas secciones como títulos de Markdown: ## %s. Sin ellas el paso no pasa.\n", strings.Join(s.Sections, ", ## "))
	}
	sp.output = out.String()
	var in strings.Builder
	for _, raw := range s.Input {
		x, err := workstream.ParseInput(raw)
		if err != nil {
			return sp, fail(1, "%s: %v", s.ID, err)
		}
		switch x.Kind {
		case "diff":
			sp.diffs = append(sp.diffs, x.Value)
		case "step":
			j := c.plan.Index(x.Value)
			if j < 0 || st.Steps[j].Doc == "" {
				return sp, fail(1, "%s: el paso %s no dejó artefacto; revísalo con coyote ws status %s", s.ID, x.Value, c.plan.ID)
			}
			if err := inputDoc(&in, c.root, "Entrega del paso "+x.Value, st.Steps[j].Doc, true); err != nil {
				return sp, fail(1, "%s: %v", s.ID, err)
			}
		case "file":
			if err := inputDoc(&in, c.root, x.Value, x.Value, false); err != nil {
				return sp, fail(1, "%s: %v", s.ID, err)
			}
		}
	}
	// Al rehacer, el agente ve su entrega anterior y lo que la persona pidió cambiar.
	if s.Feedback != "" {
		if s.Status == workstream.Redo && s.Doc != "" {
			if err := inputDoc(&in, c.root, "Tu entrega anterior", s.Doc, true); err != nil {
				return sp, fail(1, "%s: %v", s.ID, err)
			}
		}
		if err := inputDoc(&in, c.root, "Lo que la persona pide cambiar", s.Feedback, true); err != nil {
			return sp, fail(1, "%s: %v", s.ID, err)
		}
	}
	sp.inputs = in.String()
	if f != nil {
		sp.resume = f.resume
	}
	return sp, nil
}

// maxInputTokens acota cada entrada de un paso; el resto lo lee el agente.
const maxInputTokens = 3000

// inputDoc agrega una entrada como dato, entre cercas: lo que trae es
// información para el agente, no instrucciones.
func inputDoc(b *strings.Builder, root, title, relPath string, artifact bool) error {
	data, err := fsx.ReadFile(root, relPath, 8<<20)
	if err != nil {
		return fmt.Errorf("no puedo leer la entrada %s: %v", relPath, err)
	}
	fmt.Fprintf(b, "## %s (%s)\n\n", title, relPath)
	if !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
		b.WriteString("(archivo binario: no se incluye)\n\n")
		return nil
	}
	text := string(data)
	if artifact {
		text = stripFront(text)
	}
	text, cut := clipTokens(strings.TrimSpace(text), maxInputTokens)
	fence := fenceFor(text)
	fmt.Fprintf(b, "%s\n%s\n%s\n", fence, text, fence)
	if cut {
		fmt.Fprintf(b, "(recortado: el resto está en %s; léelo con Read si lo necesitas)\n", relPath)
	}
	b.WriteString("\n")
	return nil
}

// stripFront quita el frontmatter YAML de un artefacto.
func stripFront(s string) string {
	if !strings.HasPrefix(s, "---\n") {
		return s
	}
	if i := strings.Index(s[4:], "\n---\n"); i >= 0 {
		return s[4+i+5:]
	}
	return s
}

// clipTokens recorta un texto a unos max tokens, en un borde de línea.
func clipTokens(s string, max int) (string, bool) {
	est := tokens.Estimate(s)
	if est <= max {
		return s, false
	}
	runes := []rune(s)
	keep := len(runes) * max / est
	out := string(runes[:keep])
	if i := strings.LastIndex(out, "\n"); i > len(out)/2 {
		out = out[:i]
	}
	return out, true
}

// fenceFor elige una cerca más larga que cualquier fila de acentos graves del texto.
func fenceFor(s string) string {
	longest, run := 0, 0
	for _, r := range s {
		if r == '`' {
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	if longest < 3 {
		return "```"
	}
	return strings.Repeat("`", longest+1)
}

// ---- ws continue ----

func (a *app) wsContinue(args []string) error {
	fs := a.flags("ws continue", "<W> [--redo \"qué cambiar\" | --retry] [--step ID] [--note TEXTO] [--mode manual|supervised]")
	redo := fs.String("redo", "", "pide rehacer el paso: lo que hay que cambiar")
	retry := fs.Bool("retry", false, "repite un paso que falló o que dejó acciones en la cola, sin retomar su sesión")
	stepID := fs.String("step", "", "paso al que se aplican --redo o --retry; por defecto, donde se detuvo el motor")
	note := fs.String("note", "", "nota breve de tu revisión; queda en el ledger")
	modeFlag := fs.String("mode", "", "sigue con menos autonomía que la del plan: manual o supervised")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		fs.Usage()
		return fail(2, "")
	}
	if strings.TrimSpace(*redo) != "" && *retry {
		return fail(2, "usa --redo o --retry, no los dos")
	}
	if *stepID != "" && strings.TrimSpace(*redo) == "" && !*retry {
		return fail(2, "--step va con --redo o --retry")
	}
	// Revisar lo que entregó un agente es una decisión de la persona.
	if err := a.human("ws continue"); err != nil {
		return err
	}
	c, findings, err := a.loadWS(pos[0])
	if err != nil {
		return err
	}
	if err := refuseInvalid(c, findings); err != nil {
		printPlanFindings(a, findings)
		return err
	}
	if err := c.lower(*modeFlag); err != nil {
		return err
	}
	unlock, err := workstream.Lock(c.root, c.plan.ID)
	if err != nil {
		return fail(1, "%v", err)
	}
	defer unlock()
	st, err := a.wsState(c)
	if err != nil {
		return err
	}
	nx := st.Next(c.mode)
	id := c.plan.ID

	if st.Closed {
		return fail(1, "%s está cerrado (coyote close): para seguir, abre otro workstream", id)
	}
	if strings.TrimSpace(*redo) != "" || *retry {
		i := nx.Index
		if *stepID != "" {
			if i = c.plan.Index(*stepID); i < 0 {
				return fail(2, "no existe el paso %s en %s", *stepID, id)
			}
		}
		if i < 0 {
			return fail(1, "no hay paso que repetir: el plan está completo")
		}
		s := st.Steps[i]
		if s.IsHuman() {
			return fail(1, "%s lo haces tú: cuando termines, coyote ws continue %s", s.ID, id)
		}
		allowed := []string{workstream.Failed, workstream.Blocked}
		if *retry {
			allowed = append(allowed, workstream.Redo)
		} else {
			allowed = append(allowed, workstream.Review)
		}
		if !contains(allowed, s.Status) {
			return fail(1, "%s está %s: %s aplica a un paso %s", s.ID, workstream.Label(s.Status), flagName(*retry), allowedText(*retry))
		}
		if s.Status == workstream.Blocked {
			if n := pendingCount(c.root); n > 0 {
				return fail(1, "quedan %s sin decidir en la cola del gate: revísalas con coyote approvals antes de seguir", plural(n, "acción", "acciones"))
			}
		}
		if !*retry {
			doc, err := a.writeReview(c, s, *redo)
			if err != nil {
				return err
			}
			if err := a.wsRecord(c, "rej", "rehacer "+s.ID+": "+*redo, "ok", "step:"+safeRef(s.ID), "doc:"+safeRef(doc)); err != nil {
				return err
			}
			fmt.Fprintf(a.stdout, "↺ %s vuelve a correr con tu revisión (%s)\n", s.ID, doc)
		}
		return a.wsLoop(c, &forced{index: i}, false)
	}

	switch nx.Reason {
	case workstream.StopFailed:
		s := st.Steps[nx.Index]
		return fail(1, "%s falló: repítelo con coyote ws continue %s --retry o pide cambios con --redo \"qué cambiar\"", s.ID, id)
	case workstream.StopBudget:
		return fail(1, "el plan llegó a su tope (%s): sube budget_usd en %s para seguir", spentText(st), c.plan.Rel)
	case workstream.StopDone:
		fmt.Fprintf(a.stdout, "%s está completo: ciérralo con coyote close %s\n", id, id)
		return nil
	case workstream.StopClosed:
		return fail(1, "%s está cerrado (coyote close): para seguir, abre otro workstream", id)
	case workstream.StopBlocked:
		s := st.Steps[nx.Index]
		if n := pendingCount(c.root); n > 0 {
			return fail(1, "quedan %s sin decidir en la cola del gate: revísalas con coyote approvals y decide con coyote approve o reject", plural(n, "acción", "acciones"))
		}
		session := artifactSession(c.root, s.Doc)
		if !runner.ValidSession(session) {
			fmt.Fprintf(a.stdout, "No encontré la sesión de %s en su artefacto: el paso corre de nuevo.\n", s.ID)
			session = ""
		}
		return a.wsLoop(c, &forced{index: nx.Index, resume: session}, false)
	}

	// Revisión: la persona acepta lo que corrió hasta aquí.
	upTo := nx.Index
	if nx.Run {
		upTo = nx.Index - 1
	}
	accepted := st.ToReview(upTo)
	for _, i := range accepted {
		s := st.Steps[i]
		what := "revisado " + s.ID + ": " + s.Does
		if strings.TrimSpace(*note) != "" {
			what = "revisado " + s.ID + ": " + *note
		}
		refs := []string{"step:" + safeRef(s.ID)}
		if s.Doc != "" {
			refs = append(refs, "doc:"+safeRef(s.Doc))
		}
		if err := a.wsRecord(c, "apr", what, "ok", refs...); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "✓ %s aceptado (%s)\n", s.ID, orDash(s.Doc))
	}
	if len(accepted) > 0 && c.mode == "autonomous" {
		a.warnEvaluator(c)
	}
	if nx.Reason == workstream.StopHuman {
		s := st.Steps[nx.Index]
		what := "hecho por la persona " + s.ID + ": " + s.Does
		if strings.TrimSpace(*note) != "" {
			what = "hecho por la persona " + s.ID + ": " + *note
		}
		if err := a.wsRecord(c, "apr", what, "ok", "step:"+safeRef(s.ID)); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "✓ %s hecho (%s)\n", s.ID, s.Does)
	}
	if len(accepted) == 0 && nx.Reason != workstream.StopHuman && c.mode == "manual" {
		return fail(1, "no hay nada que revisar en %s: corre el paso siguiente con coyote ws run %s", id, id)
	}
	if c.mode == "manual" {
		st, err = a.wsState(c)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "Sigue: %s\n", wsHint(c, st, st.Next(c.mode)))
		return nil
	}
	return a.wsLoop(c, nil, false)
}

func flagName(retry bool) string {
	if retry {
		return "--retry"
	}
	return "--redo"
}

func allowedText(retry bool) string {
	if retry {
		return "que falló, que dejó acciones en la cola o que pediste rehacer"
	}
	return "por revisar, que falló o que dejó acciones en la cola"
}

// pendingCount cuenta las propuestas sin decidir de la cola del gate.
func pendingCount(root string) int {
	q, err := (&approval.Store{Root: root}).Queue()
	if err != nil {
		return 0
	}
	n := 0
	for _, p := range q {
		if p.Status == "pending" {
			n++
		}
	}
	return n
}

// warnEvaluator recuerda que A4 pide que evalúe quien autorizó el loop.
func (a *app) warnEvaluator(c *wsCtx) {
	entries, _, err := ledger.Open(c.root).ReadAll()
	if err != nil {
		return
	}
	apr := ""
	for _, e := range entries {
		l := e.Line
		if l.Project == c.plan.ID && l.Type == "gate" && l.Status == "ok" && contains(l.Refs, "auth:ws") {
			for _, r := range l.Refs {
				if v, ok := strings.CutPrefix(r, "apr:"); ok {
					apr = v
				}
			}
		}
	}
	if apr == "" {
		return
	}
	data, err := fsx.ReadFile(c.root, "coyote/approvals/"+apr+".json", 1<<20)
	if err != nil {
		return
	}
	var r approval.Record
	if json.Unmarshal(data, &r) != nil || r.Approver == "" {
		return
	}
	if me := c.person.Actor(""); r.Approver != me {
		fmt.Fprintf(a.stderr, "aviso: %s autorizó el loop autónomo (%s) y lo evalúa %s; A4 pide que lo evalúe quien lo autorizó\n", r.Approver, apr, me)
	}
}

// writeReview guarda lo que la persona pide cambiar, junto a las corridas.
func (a *app) writeReview(c *wsCtx, s workstream.StepState, text string) (string, error) {
	dir := filepath.Join(c.root, filepath.FromSlash(c.plan.Dir))
	path := runArtifactPath(dir, s.ID+"-revision", a.now())
	relPath := rel(c.root, path)
	if err := fsx.NoSymlinks(c.root, relPath); err != nil {
		return "", fail(1, "%v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "---\nstep: %s\nreviewed: %q\nby: %q\nat: %s\n---\n", s.ID, orDash(s.Doc), c.person.Actor(""), a.now().UTC().Format("2006-01-02T15:04:05Z"))
	fmt.Fprintf(&b, "# Revisión de %s\n\n%s\n", s.ID, strings.TrimSpace(safeLedgerText(text)))
	if err := fsx.WriteAtomic(path, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return relPath, nil
}

// artifactSession lee la sesión de Claude Code del frontmatter de un artefacto.
func artifactSession(root, relPath string) string {
	if relPath == "" {
		return ""
	}
	data, err := fsx.ReadFile(root, relPath, 8<<20)
	if err != nil || !strings.HasPrefix(string(data), "---\n") {
		return ""
	}
	front := string(data)[4:]
	if i := strings.Index(front, "\n---\n"); i >= 0 {
		front = front[:i]
	}
	for _, l := range strings.Split(front, "\n") {
		if v, ok := strings.CutPrefix(l, "session: "); ok {
			if u, err := strconv.Unquote(strings.TrimSpace(v)); err == nil {
				return u
			}
			return strings.TrimSpace(v)
		}
	}
	return ""
}
