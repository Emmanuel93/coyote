package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Emmanuel93/coyote/internal/approval"
	"github.com/Emmanuel93/coyote/internal/attribution"
	"github.com/Emmanuel93/coyote/internal/ccf"
	"github.com/Emmanuel93/coyote/internal/fsx"
	"github.com/Emmanuel93/coyote/internal/gate"
	"github.com/Emmanuel93/coyote/internal/hub"
	"github.com/Emmanuel93/coyote/internal/identity"
	"github.com/Emmanuel93/coyote/internal/infra"
	"github.com/Emmanuel93/coyote/internal/ledger"
	"github.com/Emmanuel93/coyote/internal/project"
	"github.com/Emmanuel93/coyote/internal/secrets"
	"github.com/Emmanuel93/coyote/internal/userdir"
)

const gateUsage = "check [--ide IDE] < hook.json | attribution < hook.json | pr --repo nombre=ruta... [--policy warn|fail]"

func cmdGate(a *app, args []string) error {
	if len(args) == 0 {
		return fail(2, "uso: coyote gate "+gateUsage)
	}
	switch args[0] {
	case "attribution":
		data, err := io.ReadAll(io.LimitReader(a.stdin, gate.MaxInput+1))
		if err != nil {
			return err
		}
		if len(data) > gate.MaxInput {
			return fail(2, "coyote: bloqueado por R15: la entrada del hook es demasiado grande para revisarla")
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
	if len(text) > maxAttributionText {
		return fmt.Sprintf("coyote: bloqueado por R15: el texto que se publica pesa más de %d MiB y el gate no lo revisa entero. Publícalo en partes más cortas.",
			maxAttributionText>>20), nil
	}
	if found := attr.Check(text, true); len(found) > 0 {
		return fmt.Sprintf("coyote: bloqueado por R15: el mensaje lleva atribución a herramientas de IA (%s: %q). Quita esa línea y vuelve a intentar.",
			found[0].PatternID, shortText(found[0].Text, 100)), nil
	}
	return "", nil
}

// hookReply entrega la respuesta del hook una sola vez. La dan por turno el
// cuerpo del hook y su plazo total: la primera que llega gana y la otra se
// descarta devolviendo el código de la primera. Así un plazo que vence justo
// cuando el cuerpo termina no escribe dos respuestas (un paso y una negación), y
// un cuerpo que sigue corriendo cuando el hook ya salió no escribe nada.
type hookReply struct {
	a    *app
	mu   sync.Mutex
	ide  string
	sent bool
	code int
}

// setIDE anota el IDE que se dedujo de la entrada, para el formato de la
// respuesta si el plazo vence más tarde.
func (h *hookReply) setIDE(ide string) {
	if ide == "" {
		return
	}
	h.mu.Lock()
	h.ide = ide
	h.mu.Unlock()
}

// send responde si nadie lo hizo (ide vacío es el último que se conoce) y
// devuelve el código de la respuesta que quedó.
func (h *hookReply) send(ide string, allow bool, msg string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sent {
		return h.code
	}
	if ide == "" {
		ide = h.ide
	}
	h.sent = true
	h.code = gate.Respond(ide, allow, msg, h.a.stdout, h.a.stderr)
	return h.code
}

// gateCheck es el hook previo de los IDEs. Cualquier error bloquea: un gate
// que se cae no puede dejar pasar acciones (falla cerrado). Tampoco se cuelga:
// el cuerpo corre aparte y, si no respondió a los gate.HookDeadline, el hook
// niega por su cuenta (o sale con el paso que ya había dado) en vez de esperar
// a que el IDE lo mate y, con eso, deje pasar la acción.
func gateCheck(a *app, args []string) error {
	fs := a.flags("gate check", "[--ide "+strings.Join(gate.IDEs, "|")+"] < hook.json")
	ide := fs.String("ide", "", "IDE para el que se instaló el hook (por defecto se deduce de la entrada)")
	if _, perr := parseArgs(fs, args); perr != nil {
		return perr
	}
	if *ide != "" && !gate.KnownIDE(*ide) {
		// Un hook con un IDE desconocido igual se evalúa: el formato de la
		// entrada dice cuál es.
		*ide = ""
	}
	reply := &hookReply{a: a, ide: *ide}
	done := make(chan error, 1)
	go func() { done <- gateCheckRun(a, reply, *ide) }()
	timer := time.NewTimer(gate.HookDeadline)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		code := reply.send("", false, fmt.Sprintf("coyote: el gate no terminó de revisar la acción en %s; bloquea por seguridad", gate.HookDeadline))
		if code == 0 {
			return nil // ya había dejado pasar la acción; lo que tarda es anotarla
		}
		return &exitError{code: code}
	}
}

// gateCheckRun es el cuerpo del hook: lee la entrada, revisa R15, decide y
// responde por reply.
func gateCheckRun(a *app, reply *hookReply, ide string) (err error) {
	deny := func(ide, msg string) error {
		return &exitError{code: reply.send(ide, false, msg)}
	}
	defer func() {
		if r := recover(); r != nil {
			err = deny(ide, fmt.Sprintf("coyote: el gate falló (%v); bloquea por seguridad", r))
		}
	}()
	data, rerr := io.ReadAll(io.LimitReader(a.stdin, gate.MaxInput+1))
	if rerr != nil || len(data) > gate.MaxInput {
		return deny(ide, "coyote: la entrada del hook es ilegible o demasiado grande; el gate bloquea por seguridad")
	}
	act, perr := gate.Parse(data, ide)
	if act.IDE == "" {
		act.IDE = ide
	}
	reply.setIDE(act.IDE)
	if perr != nil {
		return deny(act.IDE, "coyote: "+perr.Error()+"; el gate bloquea por seguridad")
	}
	g, gerr := a.newGateRun(act)
	if gerr == nil {
		defer g.pulse()
	}
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
			if d := gate.NewPaths(cwd, gate.Home(), state).EvaluateWithin(act, gate.EvalTimeout); d.Verdict != gate.Allow {
				return deny(act.IDE, "coyote: bloqueado siempre: "+d.Reason)
			}
			reply.send(act.IDE, true, "")
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
	reply.send(act.IDE, true, "")
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
	paths, err := gatePaths(root, cfg)
	if err != nil {
		return nil, err
	}
	agent := identity.Sanitize(act.AgentType)
	if act.AgentType == "" {
		agent = act.IDE
	}
	return &gateRun{a: a, act: act, root: root, cfg: cfg, paths: paths,
		person: identity.Resolve(root), agent: agent}, nil
}

// gatePaths arma las rutas y reglas con las que el gate juzga una acción de un
// proyecto: el IDE, el hub, los secretos de cada proyecto del repo y el
// inventario de infraestructura. Lo usan el gate y quien propone o aprueba un
// comando, para que juzguen igual.
func gatePaths(root string, cfg *project.Config) (gate.Paths, error) {
	state, err := userdir.StateDir()
	if err != nil {
		return gate.Paths{}, err
	}
	paths := gate.NewPaths(root, gate.Home(), state)
	if !cfg.Hub.Empty() {
		// El clon del hub declarado: un agente del proyecto no escribe nada en
		// él, tampoco en su carpeta de git, que decide qué commit rige
		// (ADR-0018).
		dir := cfg.Hub.Dir(root)
		if hub.Inside(dir, root) {
			// Un proyecto dentro del repo del hub (un monorepo): el resto del
			// clon es el propio proyecto; se protegen el hub.yaml y el estándar.
			paths.Global = append(paths.Global, filepath.Join(dir, "coyote", "hub.yaml"), filepath.Join(dir, "coyote", "standards"))
		} else {
			paths.Global = append(paths.Global, dir)
			paths.GitDirs = append(paths.GitDirs, gate.GitDirsOf(dir)...)
			paths.HubDir, paths.HubPath = dir, cfg.Hub.Path
		}
	}
	paths.Secrets = cfg.Secrets
	// Un monorepo, abierto en su raíz o en un proyecto anidado: cada ruta del
	// repo lleva las reglas del proyecto que la contiene y las de los de
	// afuera, como en el escaneo del commit; una ruta fuera del repo, solo las
	// de siempre. Se razona sobre rutas reales, sin enlaces.
	phys := paths.Real(root)
	top := repoTop(phys)
	// Cada checkout del repo (el principal y los worktrees enlazados) con las
	// reglas de sus proyectos: la ruta de un archivo lleva las del checkout que
	// la contiene, el más cercano.
	type checkout struct {
		dir string
		rs  *ruleSet
	}
	checkouts := []checkout{{top, newRuleSet(top)}}
	others := repoCheckouts(top)
	for _, o := range others {
		checkouts = append(checkouts, checkout{o, newRuleSet(o)})
	}
	paths.SecretsFor = func(rel string) (secrets.Rules, string, string, error) {
		abs := filepath.Join(phys, filepath.FromSlash(rel))
		var c *checkout
		for i := range checkouts {
			if within(checkouts[i].dir, abs) && (c == nil || len(checkouts[i].dir) > len(c.dir)) {
				c = &checkouts[i]
			}
		}
		if c == nil {
			return secrets.Rules{}, "", filepath.ToSlash(abs), nil
		}
		git, _ := filepath.Rel(c.dir, abs)
		git = filepath.ToSlash(git)
		rules, near, err := c.rs.forPath(git)
		return rules, filepath.Join(c.dir, filepath.FromSlash(strings.TrimSuffix(git, near))), near, err
	}
	// Con el IDE en un proyecto anidado, el gobierno de los de afuera, dentro
	// del mismo repo, y el de los otros checkouts se protege igual que el propio.
	paths.Outer = append(outerDirs(phys, top), others...)
	paths.Infra, _, paths.InfraErr = infra.Load(root)
	return paths, nil
}

// outerDirs son las carpetas entre un proyecto anidado y la raíz de su repo,
// ésta incluida: los proyectos de afuera.
func outerDirs(phys, top string) []string {
	var out []string
	for d := filepath.Dir(phys); d != phys && within(top, d); {
		out = append(out, d)
		up := filepath.Dir(d)
		if up == d {
			break
		}
		d = up
	}
	return out
}

// maxCheckouts acota cuántos worktrees de un repo se consideran.
const maxCheckouts = 64

// repoCheckouts devuelve, sin enlaces, los otros checkouts del repo de top: el
// principal y los worktrees enlazados (git worktree add), que comparten la
// carpeta común de git. Cada uno tiene su propio árbol de trabajo, con lo que
// git no sigue (los secretos ignorados) y con el project.yaml de su rama.
func repoCheckouts(top string) []string {
	dirs := gate.GitDirsOf(top)
	if len(dirs) == 0 {
		return nil
	}
	common := dirs[len(dirs)-1]
	var out []string
	seen := map[string]bool{top: true}
	add := func(d string) {
		d = filepath.Clean(d)
		if real, err := filepath.EvalSymlinks(d); err == nil {
			d = real
		} else {
			return
		}
		if !seen[d] && len(out) < maxCheckouts {
			seen[d] = true
			out = append(out, d)
		}
	}
	if filepath.Base(common) == ".git" {
		add(filepath.Dir(common))
	}
	entries, _ := os.ReadDir(filepath.Join(common, "worktrees"))
	for _, e := range entries {
		data, err := fsx.ReadCapped(filepath.Join(common, "worktrees", e.Name(), "gitdir"), 4096)
		if err != nil {
			continue
		}
		gd := strings.TrimSpace(string(data))
		if gd == "" {
			continue
		}
		if !filepath.IsAbs(gd) {
			gd = filepath.Join(common, "worktrees", e.Name(), gd)
		}
		add(filepath.Dir(gd)) // gitdir apunta al .git del worktree
	}
	return out
}

// repoTop es la carpeta de más arriba que cuenta para las reglas de secretos
// de un proyecto: la raíz de su repo de git, o el proyecto mismo si no está en
// uno.
func repoTop(root string) string {
	for d := root; ; {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return root
		}
		d = parent
	}
}

// gateRoot busca el proyecto del hook: el que abrió el IDE, luego las raíces
// que reporta el IDE y la carpeta de la acción.
func gateRoot(a *app, act gate.Action) string {
	cands := []string{os.Getenv("CLAUDE_PROJECT_DIR"), os.Getenv("CURSOR_PROJECT_DIR"), os.Getenv("GEMINI_PROJECT_DIR"),
		os.Getenv("DEVIN_PROJECT_DIR")}
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
	d := g.paths.EvaluateWithin(g.act, gate.EvalTimeout)
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

// pulse anota la llamada en el latido del IDE, con el que coyote doctor mide
// su nivel (ADR-0015). Corre después de decidir y sin afectar la decisión.
func (g *gateRun) pulse() {
	unlock, err := approval.Lock(g.root)
	if err != nil {
		return
	}
	defer unlock()
	p := gate.LoadPulses(g.root)
	p.Record(g.act, g.a.now())
	_ = gate.SavePulses(g.root, p)
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
