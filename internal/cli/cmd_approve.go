package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Emmanuel93/coyote/internal/approval"
	"github.com/Emmanuel93/coyote/internal/ccf"
	"github.com/Emmanuel93/coyote/internal/fsx"
	"github.com/Emmanuel93/coyote/internal/gate"
	"github.com/Emmanuel93/coyote/internal/identity"
	"github.com/Emmanuel93/coyote/internal/ledger"
	"github.com/Emmanuel93/coyote/internal/project"
	"github.com/Emmanuel93/coyote/internal/standards"
	"github.com/Emmanuel93/coyote/internal/tty"
	"github.com/Emmanuel93/coyote/internal/userdir"
)

// isTerminal se reemplaza en las pruebas.
var isTerminal = tty.Stdin

// agentEnv son variables que delatan que coyote corre dentro de una sesión de
// agente: COYOTE_IDE la pone coyote install en la configuración del IDE.
var agentEnv = []string{"COYOTE_IDE", "CLAUDECODE", "CURSOR_AGENT"}

// human exige que quien aprueba, rechaza o revoca sea una persona en su
// terminal, fuera de una sesión de agente.
func (a *app) human(action string) error {
	for _, k := range agentEnv {
		if os.Getenv(k) != "" {
			return fail(1, "coyote %s: esta terminal es de una sesión de agente (%s); %s lo hace la persona en su propia terminal, fuera del IDE", action, k, action)
		}
	}
	if !isTerminal() {
		return fail(1, "coyote %s necesita una terminal interactiva: las decisiones del gate las toma una persona", action)
	}
	return nil
}

type approvalCtx struct {
	root   string
	cfg    *project.Config
	store  *approval.Store
	person identity.Person
	paths  gate.Paths
}

func (a *app) approvalCtx() (*approvalCtx, error) {
	root, cfg, err := a.project()
	if err != nil {
		return nil, err
	}
	key, err := userdir.Key("approvals")
	if err != nil {
		return nil, err
	}
	state, err := userdir.StateDir()
	if err != nil {
		return nil, err
	}
	return &approvalCtx{root: root, cfg: cfg, person: identity.Resolve(root),
		store: &approval.Store{Root: root, Project: cfg.Name, Key: key, Now: a.now},
		paths: gate.NewPaths(root, gate.Home(), state)}, nil
}

func (c *approvalCtx) statuses() ([]approval.Status, map[string]error, error) {
	records, problems, err := c.store.Records()
	if err != nil {
		return nil, nil, err
	}
	entries, _, err := ledger.Open(c.root).ReadAll()
	if err != nil {
		return nil, nil, err
	}
	used, revoked := approval.Usage(entries)
	return c.store.Statuses(records, used, revoked), problems, nil
}

func (a *app) record(c *approvalCtx, typ, ws, what string, refs []string) error {
	if ws == "" {
		ws = "-"
	}
	line := ccf.Line{TS: a.now(), Actor: c.person.Actor(""), Project: ws, Repo: c.cfg.Name, Type: typ, Scope: "gate",
		What: ccf.ShortWhat(safeLedgerText(what), ccf.MaxWhatWords), Refs: refs, Status: "ok"}
	_, err := ledger.Open(c.root).Append(line, c.person.Slug)
	return err
}

func ago(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "hace segundos"
	case d < time.Hour:
		return fmt.Sprintf("hace %d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("hace %d h", int(d.Hours()))
	}
	return fmt.Sprintf("hace %d días", int(d.Hours()/24))
}

func cmdApprovals(a *app, args []string) error {
	fs := a.flags("approvals", "[--all] [--json]")
	all := fs.Bool("all", false, "incluye las aprobaciones vigentes, agotadas, vencidas y revocadas")
	asJSON := fs.Bool("json", false, "salida en JSON")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	c, err := a.approvalCtx()
	if err != nil {
		return err
	}
	queue, err := c.store.Queue()
	if err != nil {
		return err
	}
	var sts []approval.Status
	var problems map[string]error
	if *all || *asJSON {
		if sts, problems, err = c.statuses(); err != nil {
			return err
		}
	}
	if *asJSON {
		type out struct {
			Pending   []approval.Proposal `json:"pending"`
			Approvals []approval.Status   `json:"approvals"`
			Problems  map[string]string   `json:"problems,omitempty"`
		}
		o := out{Pending: queue, Approvals: sts, Problems: map[string]string{}}
		for i := range o.Pending {
			o.Pending[i].Input = nil
		}
		for k, v := range problems {
			o.Problems[k] = v.Error()
		}
		b, _ := json.MarshalIndent(o, "", "  ")
		fmt.Fprintln(a.stdout, string(b))
		return nil
	}
	now := a.now()
	if len(queue) == 0 {
		fmt.Fprintln(a.stdout, "No hay propuestas en la cola.")
	} else {
		w := table(a.stdout)
		fmt.Fprintln(w, "ID\tPEDIDA\tPOR\tACCIÓN\tESTADO")
		for _, p := range queue {
			state := "pendiente (" + plural(p.Attempts, "intento", "intentos") + ")"
			if p.Status == "rejected" {
				state = "rechazada: " + shortText(p.RejectReason, 40)
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", p.ID, ago(now, p.First), oneLineVisible(p.RequestedBy), oneLineVisible(shortText(p.Object, 70)), oneLineVisible(state))
		}
		w.Flush()
		fmt.Fprintln(a.stdout, "\nRevisa con coyote review <id>; aprueba con coyote approve <id> [--uses N] [--for 1h] o todas con coyote approve --all.")
	}
	if *all {
		fmt.Fprintln(a.stdout, "\nAprobaciones")
		if len(sts) == 0 {
			fmt.Fprintln(a.stdout, "  ninguna")
		}
		w := table(a.stdout)
		for _, st := range sts {
			state := "vigente"
			switch {
			case st.Revoked:
				state = "revocada"
			case st.Expired:
				state = "vencida"
			case st.Left <= 0:
				state = "agotada"
			}
			exp, _ := time.Parse(time.RFC3339, st.Expires)
			fmt.Fprintf(w, "  %s\t%s\t%d/%d usos\tvence %s\t%s\n", st.ID, oneLineVisible(shortText(st.Object, 60)), st.Left, st.Uses, exp.Local().Format("01-02 15:04"), state)
		}
		w.Flush()
		for name, e := range problems {
			fmt.Fprintf(a.stdout, "  ✗ coyote/approvals/%s: %v\n", name, e)
		}
	}
	return nil
}

func cmdReview(a *app, args []string) error {
	fs := a.flags("review", "[id...]")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	c, err := a.approvalCtx()
	if err != nil {
		return err
	}
	var props []approval.Proposal
	if len(pos) == 0 {
		if props, err = c.store.Queue(); err != nil {
			return err
		}
		if len(props) == 0 {
			fmt.Fprintln(a.stdout, "No hay propuestas en la cola.")
			return nil
		}
	}
	for _, ref := range pos {
		p, err := c.store.Get(ref)
		if err != nil {
			return err
		}
		props = append(props, p)
	}
	now := a.now()
	for i, p := range props {
		if i > 0 {
			fmt.Fprintln(a.stdout)
		}
		var out strings.Builder
		review(&out, c, p, now)
		// Nada de lo que escribió el agente puede redibujar la terminal de la persona.
		fmt.Fprint(a.stdout, gate.Visible(out.String()))
	}
	return nil
}

// review describe una propuesta para decidir sobre ella.
func review(w *strings.Builder, c *approvalCtx, p approval.Proposal, now time.Time) {
	fmt.Fprintf(w, "%s · %s\n", p.ID, p.Object)
	from := ""
	if p.IDE != "" {
		from = " desde " + p.IDE
	}
	fmt.Fprintf(w, "  pedida por %s%s · %s · %s\n", p.RequestedBy, from, ago(now, p.First), plural(p.Attempts, "intento", "intentos"))
	if p.Reason != "" {
		fmt.Fprintf(w, "  necesita aprobación: %s\n", p.Reason)
	}
	if p.Status == "rejected" {
		fmt.Fprintf(w, "  rechazada por %s: %s\n", p.RejectedBy, p.RejectReason)
	}
	fmt.Fprintln(w, strings.TrimRight(c.detail(p), "\n"))
	fmt.Fprintf(w, "  hash %s\n", p.Hash)
}

// detail muestra lo que la persona aprueba: el comando, el diff o la entrada.
func (c *approvalCtx) detail(p approval.Proposal) string {
	if p.InputOmitted {
		return "  (la entrada pesa más de 2 MB y no se guardó; revísala en el IDE)"
	}
	in := p.Input
	switch p.Kind {
	case "shell":
		return fmt.Sprintf("  en %s:\n    $ %s", p.Cwd, strings.ReplaceAll(p.Command, "\n", "\n      "))
	case "archivo":
		oldS, okOld := in["old_string"].(string)
		newS, okNew := in["new_string"].(string)
		content, okContent := firstText(in, "content", "file_contents", "contents", "file_text")
		switch {
		case okOld && okNew:
			return textDiff(oldS+"\n", newS+"\n", p.Path)
		case okContent:
			current := ""
			if p.Path != "" && !filepath.IsAbs(p.Path) {
				if data, err := fsx.ReadFile(c.root, p.Path, 8<<20); err == nil {
					current = string(data)
				}
			}
			return textDiff(current, content, p.Path)
		}
	}
	b, _ := json.MarshalIndent(in, "  ", "  ")
	return "  entrada:\n  " + string(b)
}

// textDiff usa el git del sistema para mostrar un diff unificado.
func textDiff(before, after, name string) string {
	dir, err := os.MkdirTemp("", "coyote-review-")
	if err != nil {
		return after
	}
	defer os.RemoveAll(dir)
	a, b := filepath.Join(dir, "actual"), filepath.Join(dir, "propuesto")
	if os.WriteFile(a, []byte(before), 0o600) != nil || os.WriteFile(b, []byte(after), 0o600) != nil {
		return after
	}
	var out bytes.Buffer
	cmd := exec.Command("git", "diff", "--no-index", "--no-color", "--unified=3", "--", a, b)
	cmd.Stdout = &out
	_ = cmd.Run() // sale con 1 cuando hay diferencias
	var res strings.Builder
	fmt.Fprintf(&res, "  --- actual/%s\n  +++ propuesto/%s\n", name, name)
	body := false
	for _, l := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		if strings.HasPrefix(l, "@@") {
			body = true
		}
		if body {
			res.WriteString("  " + l + "\n")
		}
	}
	if !body {
		res.WriteString("  (sin cambios)\n")
	}
	return res.String()
}

func cmdApprove(a *app, args []string) error {
	fs := a.flags("approve", "<id>... | --all | --bash COMANDO [--cwd DIR] [--uses N] [--for 1h] [--ws W]")
	all := fs.Bool("all", false, "aprueba todas las propuestas pendientes")
	bash := fs.String("bash", "", "aprueba este comando antes de que el agente lo pida")
	cwd := fs.String("cwd", ".", "carpeta del comando, relativa al proyecto (con --bash)")
	uses := fs.Int("uses", 1, fmt.Sprintf("veces que se puede usar (1 a %d)", approval.MaxUses))
	dur := fs.Duration("for", time.Hour, "vigencia (máximo 24h)")
	ws := fs.String("ws", "", "workstream al que pertenece")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if err := a.human("approve"); err != nil {
		return err
	}
	if *uses < 1 || *uses > approval.MaxUses {
		return fail(2, "--uses va de 1 a %d", approval.MaxUses)
	}
	if *dur < time.Minute || *dur > approval.MaxDuration {
		return fail(2, "--for va de 1m a 24h")
	}
	c, err := a.approvalCtx()
	if err != nil {
		return err
	}
	unlock, err := approval.Lock(c.root)
	if err != nil {
		return err
	}
	defer unlock()
	var props []approval.Proposal
	switch {
	case *bash != "":
		dir := *cwd
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(c.root, dir)
		}
		act := gate.Action{Tool: "Bash", Command: *bash, Cwd: dir, Input: map[string]any{"command": *bash}}
		d := c.paths.Evaluate(act)
		switch d.Verdict {
		case gate.Allow:
			fmt.Fprintln(a.stdout, "Ese comando es de solo lectura: no necesita aprobación.")
			return nil
		case gate.Block:
			return fail(1, "ese comando se bloquea siempre: %s", d.Reason)
		}
		id, err := c.store.NewID()
		if err != nil {
			return err
		}
		props = append(props, approval.Proposal{ID: id, Hash: d.Hash, Object: d.Object, Tool: "Bash", Kind: d.Kind.String(),
			Cwd: c.paths.Rel(dir), Command: *bash, RequestedBy: c.person.Actor(""), Reason: "aprobado antes de pedirse"})
	case *all:
		q, err := c.store.Queue()
		if err != nil {
			return err
		}
		for _, p := range q {
			if p.Status == "pending" {
				props = append(props, p)
			}
		}
	default:
		if len(pos) == 0 {
			fs.Usage()
			return fail(2, "")
		}
		for _, ref := range pos {
			p, err := c.store.Get(ref)
			if err != nil {
				return err
			}
			props = append(props, p)
		}
	}
	if len(props) == 0 {
		fmt.Fprintln(a.stdout, "No hay propuestas pendientes.")
		return nil
	}
	for _, p := range props {
		r, err := c.store.Approve(p, c.person.Actor(""), *uses, *dur, *ws)
		if err != nil {
			return fmt.Errorf("%s: %w", p.ID, err)
		}
		if err := a.record(c, "apr", *ws, "aprobado: "+r.Object, []string{"apr:" + r.ID, "hash:" + shortHash(r.Hash)}); err != nil {
			return err
		}
		exp, _ := time.Parse(time.RFC3339, r.Expires)
		fmt.Fprintf(a.stdout, "✓ %s aprobada · %d uso(s) · vence %s · %s\n", r.ID, r.Uses, exp.Local().Format("15:04"), oneLineVisible(r.Object))
	}
	fmt.Fprintf(a.stdout, "Registro en coyote/approvals/; el agente puede repetir la llamada.\n")
	return nil
}

func cmdReject(a *app, args []string) error {
	fs := a.flags("reject", "<id>... --reason TEXTO")
	reason := fs.String("reason", "", "motivo que verá el agente (obligatorio)")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if err := a.human("reject"); err != nil {
		return err
	}
	if len(pos) == 0 || !standards.Meaningful(*reason) {
		fs.Usage()
		return fail(2, "indica qué propuesta y un --reason con sentido: el agente lo usa para corregir el plan")
	}
	c, err := a.approvalCtx()
	if err != nil {
		return err
	}
	unlock, err := approval.Lock(c.root)
	if err != nil {
		return err
	}
	defer unlock()
	for _, ref := range pos {
		p, err := c.store.Get(ref)
		if err != nil {
			return err
		}
		if _, err := c.store.Reject(p, c.person.Actor(""), *reason); err != nil {
			return err
		}
		if err := a.record(c, "rej", "", "rechazado: "+*reason, []string{"prop:" + p.ID, "hash:" + shortHash(p.Hash)}); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "✗ %s rechazada · el agente verá: %s\n", p.ID, *reason)
	}
	return nil
}

func cmdRevoke(a *app, args []string) error {
	fs := a.flags("revoke", "<id> --reason TEXTO")
	reason := fs.String("reason", "", "motivo (obligatorio)")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if err := a.human("revoke"); err != nil {
		return err
	}
	if len(pos) != 1 || !standards.Meaningful(*reason) {
		fs.Usage()
		return fail(2, "indica una aprobación y un --reason con sentido")
	}
	c, err := a.approvalCtx()
	if err != nil {
		return err
	}
	unlock, err := approval.Lock(c.root)
	if err != nil {
		return err
	}
	defer unlock()
	sts, _, err := c.statuses()
	if err != nil {
		return err
	}
	ref := pos[0]
	if !strings.HasPrefix(ref, "P-") {
		ref = "P-" + ref
	}
	var found []approval.Status
	for _, st := range sts {
		if st.ID == ref || strings.HasPrefix(st.ID, ref) {
			found = append(found, st)
		}
	}
	if len(found) != 1 {
		return fail(1, "%s coincide con %d aprobaciones de acción; revisa con coyote approvals --all", ref, len(found))
	}
	st := found[0]
	if st.Revoked {
		fmt.Fprintf(a.stdout, "%s ya estaba revocada.\n", st.ID)
		return nil
	}
	if err := a.record(c, "rej", st.WS, "revocado: "+*reason, []string{"apr:" + st.ID, "hash:" + shortHash(st.Hash)}); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "✗ %s revocada · %s\n", st.ID, oneLineVisible(st.Object))
	return nil
}

func cmdPropose(a *app, args []string) error {
	fs := a.flags("propose", "--bash COMANDO [--cwd DIR]")
	bash := fs.String("bash", "", "comando que se quiere correr")
	cwd := fs.String("cwd", ".", "carpeta del comando, relativa al proyecto")
	agent := fs.String("agent", "", "agente que lo pide")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	if *bash == "" {
		fs.Usage()
		return fail(2, "")
	}
	c, err := a.approvalCtx()
	if err != nil {
		return err
	}
	dir := *cwd
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(c.root, dir)
	}
	d := c.paths.Evaluate(gate.Action{Tool: "Bash", Command: *bash, Cwd: dir, Input: map[string]any{"command": *bash}})
	switch d.Verdict {
	case gate.Allow:
		fmt.Fprintln(a.stdout, "Ese comando es de solo lectura: no necesita aprobación.")
		return nil
	case gate.Block:
		return fail(1, "ese comando se bloquea siempre: %s", d.Reason)
	}
	who, err := a.agentFor(c.root, *agent)
	if err != nil {
		return err
	}
	unlock, err := approval.Lock(c.root)
	if err != nil {
		return err
	}
	defer unlock()
	p, _, err := c.store.Enqueue(approval.Proposal{Hash: d.Hash, Object: d.Object, Tool: "Bash", Kind: d.Kind.String(),
		Cwd: c.paths.Rel(dir), Command: *bash, RequestedBy: c.person.Actor(who), Reason: d.Reason})
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "%s en la cola · %s\nLa persona la aprueba con coyote approve %s\n", p.ID, oneLineVisible(p.Object), p.ID)
	return nil
}

func firstText(m map[string]any, keys ...string) (string, bool) {
	for _, k := range keys {
		if s, ok := m[k].(string); ok {
			return s, true
		}
	}
	return "", false
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// oneLineVisible escapa controles y saltos: una celda de tabla es una línea.
func oneLineVisible(s string) string {
	return strings.ReplaceAll(gate.Visible(s), "\n", "\\n")
}
