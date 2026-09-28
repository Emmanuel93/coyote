package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Emmanuel93/coyote/internal/approval"
	"github.com/Emmanuel93/coyote/internal/attribution"
	"github.com/Emmanuel93/coyote/internal/ccf"
	"github.com/Emmanuel93/coyote/internal/fsx"
	"github.com/Emmanuel93/coyote/internal/gate"
	"github.com/Emmanuel93/coyote/internal/identity"
	"github.com/Emmanuel93/coyote/internal/ledger"
	"github.com/Emmanuel93/coyote/internal/project"
	"github.com/Emmanuel93/coyote/internal/userdir"
)

const gateUsage = "check [--ide claude-code|cursor|codex|copilot] < hook.json | attribution < hook.json | pr --repo nombre=ruta... [--policy warn|fail]"

func cmdGate(a *app, args []string) error {
	if len(args) == 0 {
		return fail(2, "uso: coyote gate "+gateUsage)
	}
	switch args[0] {
	case "attribution":
		data, err := io.ReadAll(io.LimitReader(a.stdin, 1<<20))
		if err != nil {
			return err
		}
		if msg, err := attributionBlock(data); err != nil {
			return err
		} else if msg != "" {
			return fail(2, "%s", msg)
		}
		return nil
	case "check":
		return gateCheck(a, args[1:])
	case "pr":
		return gatePR(a, args[1:])
	}
	return fail(2, "uso: coyote gate "+gateUsage)
}

// attributionBlock revisa R15 en la entrada de un hook: autoría de una
// herramienta de IA o atribución en un mensaje que se publica.
func attributionBlock(data []byte) (string, error) {
	text, idents := hookText(data)
	if text == "" && len(idents) == 0 {
		return "", nil
	}
	attr, err := attribution.Default()
	if err != nil {
		return "", err
	}
	for _, id := range idents {
		if f, ok := attr.AIIdentity(id[0], id[1]); ok {
			return fmt.Sprintf("coyote: bloqueado por R15: la autoría del commit sería una herramienta de IA (%s). Usa la identidad de una persona.", f.Text), nil
		}
	}
	if found := attr.Check(text, true); len(found) > 0 {
		return fmt.Sprintf("coyote: bloqueado por R15: el mensaje lleva atribución a herramientas de IA (%s: %q). Quita esa línea y vuelve a intentar.",
			found[0].PatternID, shortText(found[0].Text, 100)), nil
	}
	return "", nil
}

// gateCheck es el hook previo de los IDEs. Cualquier error bloquea: un gate
// que se cae no puede dejar pasar acciones (falla cerrado).
func gateCheck(a *app, args []string) (err error) {
	fs := a.flags("gate check", "[--ide claude-code|cursor|codex|copilot] < hook.json")
	ide := fs.String("ide", "", "IDE que llama al hook (por defecto se deduce de la entrada)")
	if _, perr := parseArgs(fs, args); perr != nil {
		return perr
	}
	deny := func(ide, msg string) error {
		return &exitError{code: gate.Respond(ide, false, msg, a.stdout, a.stderr)}
	}
	defer func() {
		if r := recover(); r != nil {
			err = deny(*ide, fmt.Sprintf("coyote: el gate falló (%v); bloquea por seguridad", r))
		}
	}()
	data, rerr := io.ReadAll(io.LimitReader(a.stdin, gate.MaxInput+1))
	if rerr != nil || len(data) > gate.MaxInput {
		return deny(*ide, "coyote: la entrada del hook es ilegible o demasiado grande; el gate bloquea por seguridad")
	}
	act, perr := gate.Parse(data, *ide)
	if act.IDE == "" {
		act.IDE = *ide
	}
	if perr != nil {
		return deny(act.IDE, "coyote: "+perr.Error()+"; el gate bloquea por seguridad")
	}
	g, gerr := a.newGateRun(act)
	if gerr != nil {
		if gateRoot(a, act) != "" {
			// El proyecto existe pero no se puede leer: nada pasa hasta que la persona lo corrija.
			return deny(act.IDE, "coyote: "+gerr.Error()+"; el gate bloquea todas las herramientas hasta que la persona lo corrija")
		}
		// Sin proyecto: se lee, pero nunca credenciales; lo demás se bloquea.
		if gate.Classify(act) == gate.KindRead {
			state, _ := userdir.StateDir()
			cwd := act.Cwd
			if cwd == "" {
				cwd, _ = a.workdir()
			}
			if d := gate.NewPaths(cwd, gate.Home(), state).Evaluate(act); d.Verdict != gate.Allow {
				return deny(act.IDE, "coyote: bloqueado siempre: "+d.Reason)
			}
			gate.Respond(act.IDE, true, "", a.stdout, a.stderr)
			return nil
		}
		return deny(act.IDE, "coyote: "+gerr.Error()+"; el gate bloquea las acciones con efectos")
	}
	if msg, aerr := attributionBlock(data); aerr != nil || msg != "" {
		if msg == "" {
			msg = "coyote: no pude revisar la atribución (" + aerr.Error() + "); el gate bloquea por seguridad"
		}
		g.event("fail", "bloqueado: atribución a IA (R15)", nil, "")
		return deny(act.IDE, msg)
	}
	if msg, allow := g.decide(); !allow {
		return deny(act.IDE, msg)
	}
	gate.Respond(act.IDE, true, "", a.stdout, a.stderr)
	return nil
}

// gateRun reúne lo que el gate necesita para decidir sobre una acción.
type gateRun struct {
	a      *app
	act    gate.Action
	root   string
	cfg    *project.Config
	paths  gate.Paths
	person identity.Person
	agent  string
}

func (a *app) newGateRun(act gate.Action) (*gateRun, error) {
	root := gateRoot(a, act)
	if root == "" {
		return nil, fmt.Errorf("no encuentro el proyecto coyote (coyote/project.yaml)")
	}
	cfg, err := project.Load(root)
	if err != nil {
		return nil, err
	}
	state, err := userdir.StateDir()
	if err != nil {
		return nil, err
	}
	agent := identity.Sanitize(act.AgentType)
	if act.AgentType == "" {
		agent = act.IDE
	}
	return &gateRun{a: a, act: act, root: root, cfg: cfg, paths: gate.NewPaths(root, gate.Home(), state),
		person: identity.Resolve(root), agent: agent}, nil
}

// gateRoot busca el proyecto del hook: el que abrió el IDE, luego las raíces
// que reporta el IDE y la carpeta de la acción.
func gateRoot(a *app, act gate.Action) string {
	cands := []string{os.Getenv("CLAUDE_PROJECT_DIR"), os.Getenv("CURSOR_PROJECT_DIR")}
	cands = append(cands, act.Roots...)
	cands = append(cands, act.Cwd)
	if wd, err := a.workdir(); err == nil {
		cands = append(cands, wd)
	}
	for _, c := range cands {
		if c == "" {
			continue
		}
		if root, err := project.FindRoot(c); err == nil {
			return root
		}
	}
	return ""
}

// decide aplica la política y, si hace falta, busca la aprobación. Devuelve
// el mensaje para el agente y si la acción pasa.
func (g *gateRun) decide() (string, bool) {
	d := g.paths.Evaluate(g.act)
	switch d.Verdict {
	case gate.Allow:
		return "", true
	case gate.Block:
		g.blocked(d)
		return fmt.Sprintf("coyote: bloqueado siempre: %s. Un agente no puede pedir aprobación para esto; si hace falta, la persona lo hace en su terminal.", d.Reason), false
	}
	unlock, err := approval.Lock(g.root)
	if err != nil {
		return "coyote: " + err.Error(), false
	}
	defer unlock()
	store, err := g.store()
	if err != nil {
		return "coyote: " + err.Error() + "; el gate bloquea por seguridad", false
	}
	records, _, err := store.Records()
	if err != nil {
		return "coyote: " + err.Error(), false
	}
	entries, _, err := ledger.Open(g.root).ReadAll()
	if err != nil {
		return "coyote: " + err.Error(), false
	}
	used, revoked := approval.Usage(entries)
	if st, ok := store.Find(d.Hash, store.Statuses(records, used, revoked)); ok {
		if err := g.event("ok", "aprobado: "+d.Object, []string{"apr:" + st.ID, "hash:" + shortHash(d.Hash)}, st.WS); err != nil {
			return "coyote: no pude registrar el uso de la aprobación (" + err.Error() + "); el gate bloquea", false
		}
		return "", true
	}
	cwd := g.act.Cwd
	if cwd == "" {
		cwd = g.root
	}
	p, isNew, err := store.Enqueue(approval.Proposal{Hash: d.Hash, Object: d.Object, Tool: g.act.Tool, Kind: d.Kind.String(),
		Path: d.Path, Cwd: g.paths.Rel(cwd), Command: g.act.Command, Input: g.act.Input,
		RequestedBy: g.person.Actor(g.agent), IDE: g.act.IDE, Session: g.act.Session, Reason: d.Reason})
	if err != nil {
		return "coyote: no pude encolar la propuesta (" + err.Error() + "); el gate bloquea", false
	}
	if p.Status == "rejected" {
		return fmt.Sprintf("coyote: la persona rechazó esta acción (%s): %s. No la repitas; ajusta el plan o pregunta.", p.ID, p.RejectReason), false
	}
	now := g.a.now()
	if isNew || now.Sub(p.Logged) > 10*time.Minute {
		if g.event("pend", "en cola: "+d.Object, []string{"prop:" + p.ID, "hash:" + shortHash(d.Hash)}, "") == nil {
			p.Logged = now
			_ = store.Save(p)
		}
	}
	return fmt.Sprintf("coyote: acción no aprobada (%s). Quedó en la cola como %s. Pide a la persona que la revise con `coyote review %s` "+
		"y la apruebe con `coyote approve %s` en su terminal; después repite exactamente la misma llamada. "+
		"Si el paso tiene más cambios, propónlos también: se aprueban juntos con `coyote approve --all`.", d.Object, p.ID, p.ID, p.ID), false
}

func (g *gateRun) store() (*approval.Store, error) {
	key, err := userdir.Key("approvals")
	if err != nil {
		return nil, err
	}
	return &approval.Store{Root: g.root, Project: g.cfg.Name, Key: key, Now: g.a.now}, nil
}

// blocked registra un bloqueo, una vez cada diez minutos por acción.
func (g *gateRun) blocked(d gate.Decision) {
	unlock, err := approval.Lock(g.root)
	if err != nil {
		return
	}
	defer unlock()
	rel := ".coyote/gate-blocked.json"
	seen := map[string]time.Time{}
	if data, err := fsx.ReadFile(g.root, rel, 1<<20); err == nil {
		_ = json.Unmarshal(data, &seen)
	}
	now := g.a.now()
	if t, ok := seen[d.Hash]; ok && now.Sub(t) < 10*time.Minute {
		return
	}
	for h, t := range seen {
		if now.Sub(t) > time.Hour {
			delete(seen, h)
		}
	}
	seen[d.Hash] = now
	if g.event("fail", "bloqueado: "+d.Reason, []string{"hash:" + shortHash(d.Hash)}, "") != nil {
		return
	}
	if fsx.NoSymlinks(g.root, rel) == nil {
		b, _ := json.Marshal(seen)
		_ = os.WriteFile(filepath.Join(g.root, filepath.FromSlash(rel)), b, 0o600)
	}
}

// event escribe una decisión del gate en el ledger, a nombre de la persona y
// del agente que reporta el IDE.
func (g *gateRun) event(status, what string, refs []string, ws string) error {
	if ws == "" {
		ws = "-"
	}
	what = safeLedgerText(what)
	line := ccf.Line{TS: g.a.now(), Actor: g.person.Actor(g.agent), Project: ws, Repo: g.cfg.Name, Type: "gate",
		Scope: "gate", What: ccf.ShortWhat(what, ccf.MaxWhatWords), Refs: refs, Status: status}
	_, err := ledger.Open(g.root).Append(line, g.person.Slug)
	return err
}

// safeLedgerText deja un texto apto para un archivo versionado: sin secretos ni
// atribución a IA (R15).
func safeLedgerText(s string) string {
	s = gate.Redact(gate.Visible(s))
	if attr, err := attribution.Default(); err == nil && len(attr.Check(s, true)) > 0 {
		if i := strings.Index(s, ":"); i > 0 {
			return s[:i+1] + " [texto omitido por R15]"
		}
		return "[texto omitido por R15]"
	}
	return s
}

func shortHash(h string) string {
	h = strings.TrimPrefix(h, "sha256:")
	if len(h) > 16 {
		h = h[:16]
	}
	return h
}
