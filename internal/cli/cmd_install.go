package cli

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"

	"github.com/Emmanuel93/coyote/internal/agents"
	"github.com/Emmanuel93/coyote/internal/agentsmd"
	"github.com/Emmanuel93/coyote/internal/approval"
	"github.com/Emmanuel93/coyote/internal/ccf"
	"github.com/Emmanuel93/coyote/internal/fsx"
	"github.com/Emmanuel93/coyote/internal/gate"
	"github.com/Emmanuel93/coyote/internal/identity"
	"github.com/Emmanuel93/coyote/internal/install"
	"github.com/Emmanuel93/coyote/internal/ledger"
	"github.com/Emmanuel93/coyote/internal/product"
	"github.com/Emmanuel93/coyote/internal/project"
	"github.com/Emmanuel93/coyote/internal/standards"
	"github.com/Emmanuel93/coyote/internal/userdir"
	"github.com/Emmanuel93/coyote/internal/version"
)

func ideList(ide string) ([]string, error) {
	if ide == "all" {
		return install.IDEs, nil
	}
	for _, x := range install.IDEs {
		if x == ide {
			return []string{ide}, nil
		}
	}
	switch ide {
	case gate.IDEDevin:
		return nil, fail(2, "Devin CLI lee el hook de Claude Code: usa --ide claude-code")
	case gate.IDEJunie:
		return nil, fail(2, "Junie solo acepta hooks de usuario; cómo conectarlo está en docs/specs/install-v1.md")
	}
	return nil, fail(2, "--ide va con %s o all", strings.Join(install.IDEs, ", "))
}

// doctorIDEs son los IDEs que doctor puede medir: los que se instalan, más
// Devin CLI y Junie, que se conectan por otro camino.
func doctorIDEs(ide string) ([]string, error) {
	if ide == gate.IDEDevin || ide == gate.IDEJunie {
		return []string{ide}, nil
	}
	return ideList(ide)
}

// installPlan calcula los cambios de todos los IDEs pedidos; AGENTS.md va una vez.
func (a *app) installPlan(root string, cfg *project.Config, ides []string) ([]install.Change, error) {
	set, err := agents.Load(root)
	if err != nil {
		return nil, err
	}
	st, err := standards.Load(root, a.now())
	if err != nil {
		return nil, err
	}
	md, err := agentsmd.Generate(root, st, cfg.Autonomy)
	if err != nil {
		return nil, err
	}
	owns := true
	if cur, err := fsx.ReadCapped(filepath.Join(root, "AGENTS.md"), 4<<20); err == nil {
		owns = strings.HasPrefix(string(cur), agentsmd.Marker)
	} else if !os.IsNotExist(err) {
		owns = false // un AGENTS.md que no es un archivo regular no se toca
	}
	var all []install.Change
	seen := map[string]bool{}
	for i, ide := range ides {
		o := install.Options{Root: root, IDE: ide, Set: set, OwnsMD: owns}
		if i == 0 {
			o.AgentsMD = md
		}
		cs, err := install.Plan(o)
		if err != nil {
			return nil, err
		}
		// Varios IDEs comparten archivos (.agents/skills, AGENTS.md): van una vez.
		for _, c := range cs {
			if !seen[c.Path] {
				seen[c.Path] = true
				all = append(all, c)
			}
		}
	}
	return all, nil
}

func cmdInstall(a *app, args []string) error {
	fs := a.flags("install", "--ide "+strings.Join(install.IDEs, "|")+"|all | --ci github [--check] [--dry-run]")
	ide := fs.String("ide", "", "IDE a configurar: "+strings.Join(install.IDEs, ", ")+" o all")
	ciKind := fs.String("ci", "", "pipeline de impacto del producto: github")
	policy := fs.String("policy", "", "con --ci: warn solo reporta; fail hace fallar el chequeo hasta que aprueba un dueño. Por defecto sale de features.pr_enforcement")
	coyoteRef := fs.String("coyote-ref", "", "con --ci: versión etiquetada de coyote que compila el pipeline (por defecto, la de este binario)")
	coyoteRepo := fs.String("coyote-repo", "", "con --ci: owner/nombre del repo de coyote en GitHub")
	check := fs.Bool("check", false, "falla si la configuración no está vigente, sin escribir (para CI)")
	dry := fs.Bool("dry-run", false, "muestra los cambios sin escribir")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	if *ciKind != "" {
		if *ide != "" {
			return fail(2, "usa --ide o --ci, no los dos")
		}
		return a.installCI(*ciKind, *policy, *coyoteRef, *coyoteRepo, *check, *dry)
	}
	if *ide == "" {
		fs.Usage()
		return fail(2, "")
	}
	ides, err := ideList(*ide)
	if err != nil {
		return err
	}
	root, cfg, err := a.project()
	if err != nil {
		return err
	}
	changes, err := a.installPlan(root, cfg, ides)
	if err != nil {
		return err
	}
	hookState := "vigente"
	if !project.HookCurrent(root) {
		hookState = "pendiente"
	}
	printChanges(a, changes, hookState)
	pendingN, conflicts := 0, 0
	for _, c := range changes {
		if c.Pending() {
			pendingN++
		}
		if c.Blocking() {
			conflicts++
		}
	}
	conflictErr := func() error {
		return fail(1, "%d %s en conflicto: el IDE queda sin gate hasta que lo resuelvas (ver arriba)", conflicts, pluralWord(conflicts, "archivo", "archivos"))
	}
	switch {
	case *check:
		if conflicts > 0 {
			return conflictErr()
		}
		if pendingN > 0 || hookState != "vigente" {
			return fail(1, "la configuración de %s no está vigente; corre coyote install --ide %s", *ide, *ide)
		}
		fmt.Fprintln(a.stdout, "Configuración vigente.")
		return nil
	case *dry:
		return nil
	}
	if err := a.human("install"); err != nil {
		return err
	}
	if err := install.Apply(root, changes); err != nil {
		return err
	}
	if hookState != "vigente" {
		if p, status, err := project.InstallHook(root, false, false); err != nil {
			fmt.Fprintf(a.stderr, "coyote: hook commit-msg sin instalar: %v\n", err)
		} else {
			fmt.Fprintf(a.stdout, "hook commit-msg %s en %s\n", status, p)
		}
	}
	if err := ensureIgnored(root); err != nil {
		return err
	}
	if pendingN > 0 {
		person := identity.Resolve(root)
		which := strings.Join(ides, " y ")
		if len(ides) > 2 {
			which = fmt.Sprintf("%d IDEs", len(ides))
		}
		line := ccf.Line{TS: a.now(), Actor: person.Actor(""), Project: "-", Repo: cfg.Name, Type: "chore", Scope: "gate",
			What: "coyote install para " + which + ": gate, agentes y skills", Status: "ok"}
		if _, err := ledger.Open(root).Append(line, person.Slug); err != nil {
			return err
		}
	}
	if conflicts > 0 {
		return conflictErr()
	}
	fmt.Fprintf(a.stdout, "Listo. Prueba el gate con coyote doctor --ide %s y haz commit de la configuración.\n", *ide)
	if cfg.Autonomy != "" && cfg.Autonomy != "manual" {
		fmt.Fprintf(a.stdout, "Aviso: autonomy es %s en project.yaml: el motor de workstreams (coyote ws) se detiene menos, pero el gate sigue pidiendo aprobación para cada acción con efectos (ADR-0014).\n", cfg.Autonomy)
	}
	return nil
}

// printChanges muestra los cambios; agentes y skills se resumen por carpeta.
func printChanges(a *app, changes []install.Change, hook string) {
	type group struct{ counts map[string]int }
	groups := map[string]*group{}
	var order []string
	w := table(a.stdout)
	for _, c := range changes {
		dir := ""
		switch {
		case strings.Contains(c.Path, "/agents/"):
			dir = c.Path[:strings.Index(c.Path, "/agents/")+len("/agents/")]
		case strings.Contains(c.Path, "skills/"):
			dir = c.Path[:strings.Index(c.Path, "skills/")+len("skills/")]
		}
		if dir != "" && c.State != install.Skipped {
			g, ok := groups[dir]
			if !ok {
				g = &group{counts: map[string]int{}}
				groups[dir] = g
				order = append(order, dir)
			}
			g.counts[c.State]++
			continue
		}
		detail := c.Detail
		fmt.Fprintf(w, "  %s\t%s\t%s\n", c.State, c.Path, detail)
	}
	for _, dir := range order {
		var parts []string
		keys := make([]string, 0, len(groups[dir].counts))
		for k := range groups[dir].counts {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%d %s", groups[dir].counts[k], k))
		}
		fmt.Fprintf(w, "  %s\t%s\t%s\n", "carpeta", dir, strings.Join(parts, ", "))
	}
	fmt.Fprintf(w, "  %s\t%s\t%s\n", hook, ".git/hooks/commit-msg", "quita atribución de IA en cada commit")
	w.Flush()
}

// ensureIgnored agrega .coyote/ al .gitignore del proyecto. No escribe a
// través de un symlink: un .gitignore que apunta afuera no se toca.
func ensureIgnored(root string) error {
	p := filepath.Join(root, ".gitignore")
	if err := fsx.NoSymlinks(root, ".gitignore"); err != nil {
		return err
	}
	data, err := fsx.ReadFile(root, ".gitignore", 4<<20)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, l := range strings.Split(string(data), "\n") {
		if t := strings.TrimSpace(l); t == ".coyote/" || t == ".coyote" || t == "/.coyote/" {
			return nil
		}
	}
	s := string(data)
	if s != "" && !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return fsx.WriteAtomic(p, []byte(s+"# Estado local de coyote (índice, cola del gate); nunca se versiona\n.coyote/\n"), 0o644)
}

// ideChecks prueba el gate de un IDE en esta máquina, sin efectos: la
// configuración, el hook, que coyote se encuentre y decisiones simuladas.
func (a *app) ideChecks(root string, cfg *project.Config, ide string, add func(name, state, detail string)) {
	switch ide {
	case gate.IDEJunie:
		add("instalación junie", "warn", "Junie solo lee hooks de usuario (o de --config-location) y su plugin del IDE no los llama; cómo conectarlo está en docs/specs/install-v1.md")
	default:
		target := ide
		if ide == gate.IDEDevin {
			target = "claude-code" // Devin CLI lee el hook de Claude Code
		}
		changes, err := a.installPlan(root, cfg, []string{target})
		if err != nil {
			add("instalación "+ide, "fail", err.Error())
			return
		}
		n := 0
		var conflicts []string
		for _, c := range changes {
			if c.Pending() {
				n++
			}
			if c.Blocking() {
				conflicts = append(conflicts, c.Path+": "+c.Detail)
			}
		}
		switch {
		case len(conflicts) > 0:
			add("instalación "+ide, "fail", strings.Join(conflicts, "; "))
		case n > 0:
			add("instalación "+ide, "warn", fmt.Sprintf("%d archivos por crear o actualizar; corre coyote install --ide %s", n, target))
		default:
			add("instalación "+ide, "ok", "hook, agentes o skills y configuración vigentes")
		}
	}
	found := ""
	if p, err := exec.LookPath("coyote"); err == nil {
		found = p
	} else {
		for _, p := range []string{filepath.Join(gate.Home(), "go", "bin", "coyote"), "/opt/homebrew/bin/coyote", "/usr/local/bin/coyote"} {
			if info, err := os.Stat(p); err == nil && info.Mode()&0o111 != 0 {
				found = p
				break
			}
		}
	}
	if found == "" {
		add("gate: binario", "fail", "el hook no encuentra coyote y bloquearía todas las acciones; instálalo con make install")
	} else {
		add("gate: binario", "ok", "el hook usa "+found)
	}
	state, _ := userdir.StateDir()
	ps := gate.NewPaths(root, gate.Home(), state)
	sim := func(tool string, input map[string]any) gate.Decision {
		return ps.Evaluate(gate.Action{IDE: ide, Tool: tool, Input: input, Command: fmt.Sprint(input["command"]), Cwd: root})
	}
	cases := []struct {
		name  string
		tool  string
		input map[string]any
		want  gate.Verdict
	}{
		{"gate: lectura", "Bash", map[string]any{"command": "git status"}, gate.Allow},
		{"gate: sin aprobación", "Bash", map[string]any{"command": "rm -rf coyote-doctor-prueba"}, gate.NeedsApproval},
		{"gate: edición", "Write", map[string]any{"file_path": filepath.Join(root, "coyote-doctor-prueba.txt"), "content": "x"}, gate.NeedsApproval},
		{"gate: autoaprobación", "Bash", map[string]any{"command": "coyote approve --all"}, gate.Block},
		{"gate: su configuración", "Write", map[string]any{"file_path": filepath.Join(root, ".claude", "settings.json"), "content": "{}"}, gate.Block},
		{"gate: credenciales", "Read", map[string]any{"file_path": filepath.Join(gate.Home(), ".ssh", "id_rsa")}, gate.Block},
	}
	for _, c := range cases {
		if d := sim(c.tool, c.input); d.Verdict != c.want {
			add(c.name, "fail", fmt.Sprintf("se esperaba %s y dio %s (%s)", c.want, d.Verdict, d.Reason))
		} else {
			add(c.name, "ok", c.want.String())
		}
	}
	msg, _ := attributionBlock([]byte(`{"tool_name":"Bash","tool_input":{"command":"git commit -m 'feat: x' -m 'Co-Authored-By: ` + "Cla" + `ude <noreply@` + `anthropic.com>'"}}`))
	if msg == "" {
		add("gate: atribución", "fail", "no bloqueó un commit con trailer de IA")
	} else {
		add("gate: atribución", "ok", "bloquea commits con atribución de IA")
	}
	if cfg.Autonomy != "" && cfg.Autonomy != "manual" {
		add("autonomía", "ok", cfg.Autonomy+": el motor de workstreams se detiene menos; cada acción con efectos se sigue aprobando")
	} else {
		add("autonomía", "ok", "manual: cada acción con efectos se aprueba")
	}
	if _, err := userdir.Key("approvals"); err != nil {
		add("gate: clave local", "fail", err.Error())
	} else {
		add("gate: clave local", "ok", "las aprobaciones se firman en esta máquina")
	}
	if ide == "copilot" || ide == "claude-code" {
		if doubleVSCode(root) {
			add("VS Code", "warn", "chat.useClaudeHooks está prendida y el proyecto tiene los hooks de Claude Code y de Copilot: VS Code correría el gate dos veces por acción y una aprobación de un uso no alcanza; apaga chat.useClaudeHooks o quita uno de los dos")
		}
	}
	pulses := gate.LoadPulses(root)
	lvl := gate.Measure(pulses, gate.LoadCanaries(root), gate.CanariesRan(root), ide, a.now())
	name := "nivel de " + ide
	switch lvl.Level {
	case 1:
		add(name, "ok", lvl.Detail+" ("+lvl.At.Local().Format("2006-01-02 15:04")+")")
	case 2, 3:
		add(name, "fail", lvl.Detail+"; con R17, este IDE no hace tareas R2 o R3 fuera de ramas coyote/ con gate pr")
	default:
		add(name, "warn", lvl.Detail)
	}
	if p := pulses.IDEs[ide]; p != nil && len(p.Unknown) > 0 {
		add("herramientas de "+ide, "warn", "piden aprobación porque el gate no las conoce: "+strings.Join(p.Unknown, ", "))
	}
}

// vscodeClaudeHooks reconoce chat.useClaudeHooks prendida en la
// configuración de VS Code, que admite comentarios.
var vscodeClaudeHooks = regexp.MustCompile(`"chat\.useClaudeHooks"\s*:\s*true`)

// doubleVSCode dice si VS Code correría dos hooks de coyote: el de Copilot y,
// con chat.useClaudeHooks, el de Claude Code.
func doubleVSCode(root string) bool {
	vs, err := os.ReadFile(filepath.Join(root, ".vscode", "settings.json"))
	if err != nil || !vscodeClaudeHooks.Match(vs) {
		return false
	}
	claude, _ := os.ReadFile(filepath.Join(root, ".claude", "settings.json"))
	_, errCopilot := os.Stat(filepath.Join(root, filepath.FromSlash(install.CopilotFile)))
	return strings.Contains(string(claude), "coyote-gate") && errCopilot == nil
}

// canaryRequest pide un canario para medir el nivel de un IDE (ADR-0015).
func (a *app) canaryRequest(ide string) error {
	root, _, err := a.project()
	if err != nil {
		return err
	}
	b := make([]byte, 5)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	code := hex.EncodeToString(b)
	unlock, err := approval.Lock(root)
	if err != nil {
		return err
	}
	defer unlock()
	if err := gate.AddCanary(root, gate.CanaryRequest{Code: code, IDE: ide, Created: a.now()}); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "Canario %s para %s. En %s, dentro de este proyecto, pídele al agente exactamente esto:\n\n", code, ide, ide)
	fmt.Fprintf(a.stdout, "  Corre este comando en la terminal y dime qué pasó: coyote doctor canary %s\n\n", code)
	fmt.Fprintf(a.stdout, "El gate lo niega siempre: si el IDE respeta el gate, el comando no corre. Después corre coyote doctor --ide %s.\n", ide)
	return nil
}

// doctorCanary es el canario mismo. Si llega a correr, el IDE lo ejecutó sin
// llamar al gate o a pesar de su negación: queda anotado para doctor.
func doctorCanary(a *app, args []string) error {
	if len(args) != 1 || !gate.CanaryCode.MatchString(args[0]) {
		return fail(2, "uso: coyote doctor canary <código>")
	}
	root, _, err := a.project()
	if err != nil {
		return err
	}
	unlock, err := approval.Lock(root)
	if err != nil {
		return err
	}
	defer unlock()
	if err := gate.MarkCanaryRan(root, args[0], a.now()); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "coyote: el canario %s corrió. El gate lo niega siempre, así que este IDE ejecutó un comando sin llamar al gate o a pesar de su negación. "+
		"Avísale a la persona: lo verá en coyote doctor --ide.\n", args[0])
	return fail(3, "")
}

// installCI genera el workflow de impacto de cada repo del producto en
// coyote/ci/<repo>.yml (ADR-0013). Llevarlo a cada repo es un PR de ese repo.
func (a *app) installCI(kind, policy, ref, coyoteRepo string, check, dry bool) error {
	if kind != "github" {
		return fail(2, "--ci %q: por ahora solo github", kind)
	}
	root, cfg, err := a.project()
	if err != nil {
		return err
	}
	if cfg.Type != "product" {
		return fail(1, "el pipeline de impacto es de un proyecto de tipo product (coyote init --type product)")
	}
	// La política sale de la bandera pr_enforcement: bloquear el merge solo
	// sirve si GitHub exige el chequeo, y en repos privados eso pide un plan de pago.
	enforce := cfg.Feature("pr_enforcement")
	switch {
	case policy == "" && enforce:
		policy = "fail"
	case policy == "":
		policy = "warn"
	case policy == "fail" && !enforce:
		fmt.Fprintln(a.stderr, "aviso: --policy fail con features.pr_enforcement apagada: el chequeo marca rojo, pero GitHub no lo exige en repos privados sin plan de pago")
	}
	if ref == "" {
		ref = version.Version
	}
	if coyoteRepo == "" {
		coyoteRepo = selfRepo()
	}
	sources, _, err := productSources(root, cfg, nil)
	if err != nil {
		return err
	}
	dirs := map[string]string{}
	for _, s := range sources {
		dirs[s.Name] = s.Dir
	}
	var repos []install.CIRepo
	for _, r := range cfg.Repos {
		full, ok := install.GitHubFullName(r.URL)
		if !ok && dirs[r.Name] != "" {
			full, ok = install.GitHubFullName(product.OriginURL(dirs[r.Name]))
		}
		if !ok {
			return fail(1, "%s: no sé su repo de GitHub; regístralo con coyote repo add %s <url de GitHub> --path <ruta>", r.Name, r.Name)
		}
		repos = append(repos, install.CIRepo{Name: r.Name, FullName: full})
	}
	if len(repos) < 2 {
		return fail(1, "el producto necesita al menos dos repos para ver el impacto entre ellos")
	}
	stale := 0
	for i, self := range repos {
		others := append(append([]install.CIRepo{}, repos[:i]...), repos[i+1:]...)
		wf, err := install.GitHubWorkflow(install.CIOptions{Self: self, Others: others, CoyoteRepo: coyoteRepo, CoyoteRef: ref, Policy: policy, Risk: cfg.RiskRules()})
		if err != nil {
			return fail(1, "%v", err)
		}
		relPath := "coyote/ci/" + self.Name + ".yml"
		old, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(relPath)))
		status := "sin cambios"
		switch {
		case old == nil:
			status = "creado"
		case string(old) != wf:
			status = "actualizado"
		}
		if status != "sin cambios" {
			stale++
		}
		if check || dry {
			if status != "sin cambios" {
				status = "pendiente: " + status
			}
		} else if status != "sin cambios" {
			if _, err := writeProductFile(root, relPath, wf); err != nil {
				return err
			}
		}
		fmt.Fprintf(a.stdout, "%-40s %s (%s)\n", relPath, status, self.FullName)
	}
	if check && stale > 0 {
		return fail(1, "los workflows no están al día; corre coyote install --ci github")
	}
	if !check && !dry {
		fmt.Fprintf(a.stdout, "\nPara activarlo en cada repo:\n"+
			"1. Copia coyote/ci/<repo>.yml a .github/workflows/coyote.yml del repo, con un PR.\n"+
			"   Para probarlo sin tocar la rama principal, déjalo en una rama (coyote/pipeline) y abre PRs contra esa rama:\n"+
			"   pull_request_target usa el workflow de la rama base del PR.\n"+
			"2. Crea en cada repo el secreto %s: un token de GitHub de solo lectura (Contents: read) de los repos del producto.\n"+
			"   Si %s es de otra cuenta, agrega %s con lectura de ese repo; un token fino cubre un solo dueño.\n"+
			"3. Opcional: %s con Members: read de la organización, para verificar los equipos de CODEOWNERS.\n",
			install.SecretName, coyoteRepo, install.ToolSecretName, install.TeamsSecretName)
		if enforce {
			fmt.Fprintln(a.stdout, "4. pr_enforcement está prendida: marca el chequeo \"coyote gate pr / riesgo e impacto\" como requerido en la protección de la rama para que el merge espere la aprobación.")
		} else {
			fmt.Fprintln(a.stdout, "4. pr_enforcement está apagada: el chequeo avisa y no bloquea. Cuando GitHub te deje exigir chequeos en repos privados (plan de pago),\n"+
				"   pon features.pr_enforcement: true en coyote/project.yaml y vuelve a correr coyote install --ci github.")
		}
	}
	return nil
}

// selfRepo deduce owner/nombre del repo de coyote de la ruta del módulo.
func selfRepo() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		if name, ok := strings.CutPrefix(bi.Main.Path, "github.com/"); ok && strings.Count(name, "/") == 1 {
			return name
		}
	}
	return ""
}
