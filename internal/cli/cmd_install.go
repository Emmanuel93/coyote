package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Emmanuel93/coyote/internal/agents"
	"github.com/Emmanuel93/coyote/internal/agentsmd"
	"github.com/Emmanuel93/coyote/internal/ccf"
	"github.com/Emmanuel93/coyote/internal/gate"
	"github.com/Emmanuel93/coyote/internal/identity"
	"github.com/Emmanuel93/coyote/internal/install"
	"github.com/Emmanuel93/coyote/internal/ledger"
	"github.com/Emmanuel93/coyote/internal/project"
	"github.com/Emmanuel93/coyote/internal/standards"
	"github.com/Emmanuel93/coyote/internal/userdir"
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
	return nil, fail(2, "--ide va con %s o all (los demás IDEs llegan en v0.5)", strings.Join(install.IDEs, ", "))
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
	if cur, err := os.ReadFile(filepath.Join(root, "AGENTS.md")); err == nil {
		owns = strings.HasPrefix(string(cur), agentsmd.Marker)
	}
	var all []install.Change
	for i, ide := range ides {
		o := install.Options{Root: root, IDE: ide, Set: set, OwnsMD: owns}
		if i == 0 {
			o.AgentsMD = md
		}
		cs, err := install.Plan(o)
		if err != nil {
			return nil, err
		}
		all = append(all, cs...)
	}
	return all, nil
}

func cmdInstall(a *app, args []string) error {
	fs := a.flags("install", "--ide claude-code|cursor|all [--check] [--dry-run]")
	ide := fs.String("ide", "", "IDE a configurar: claude-code, cursor o all")
	check := fs.Bool("check", false, "falla si la configuración no está vigente, sin escribir (para CI)")
	dry := fs.Bool("dry-run", false, "muestra los cambios sin escribir")
	if _, err := parseArgs(fs, args); err != nil {
		return err
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
	pendingN := 0
	for _, c := range changes {
		if c.Pending() {
			pendingN++
		}
	}
	switch {
	case *check:
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
		line := ccf.Line{TS: a.now(), Actor: person.Actor(""), Project: "-", Repo: cfg.Name, Type: "chore", Scope: "gate",
			What: "coyote install para " + strings.Join(ides, " y ") + ": gate, agentes y skills", Status: "ok"}
		if _, err := ledger.Open(root).Append(line, person.Slug); err != nil {
			return err
		}
	}
	fmt.Fprintf(a.stdout, "Listo. Prueba el gate con coyote doctor --ide %s y haz commit de la configuración.\n", *ide)
	if cfg.Autonomy != "" && cfg.Autonomy != "manual" {
		fmt.Fprintf(a.stdout, "Aviso: autonomy es %s en project.yaml; hasta v0.4 el gate aplica manual.\n", cfg.Autonomy)
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

func ensureIgnored(root string) error {
	p := filepath.Join(root, ".gitignore")
	data, err := os.ReadFile(p)
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
	return os.WriteFile(p, []byte(s+"# Estado local de coyote (índice, cola del gate); nunca se versiona\n.coyote/\n"), 0o644)
}

// ideChecks prueba el gate de un IDE en esta máquina, sin efectos: la
// configuración, el hook, que coyote se encuentre y decisiones simuladas.
func (a *app) ideChecks(root string, cfg *project.Config, ide string, add func(name, state, detail string)) {
	changes, err := a.installPlan(root, cfg, []string{ide})
	if err != nil {
		add("instalación "+ide, "fail", err.Error())
		return
	}
	n := 0
	for _, c := range changes {
		if c.Pending() {
			n++
		}
	}
	if n > 0 {
		add("instalación "+ide, "warn", fmt.Sprintf("%d archivos por crear o actualizar; corre coyote install --ide %s", n, ide))
	} else {
		add("instalación "+ide, "ok", "hook, agentes, skills y atribución apagada vigentes")
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
		add("autonomía", "warn", cfg.Autonomy+" llega en v0.4; hoy el gate aplica manual")
	} else {
		add("autonomía", "ok", "manual: cada acción con efectos se aprueba")
	}
	if _, err := userdir.Key("approvals"); err != nil {
		add("gate: clave local", "fail", err.Error())
	} else {
		add("gate: clave local", "ok", "las aprobaciones se firman en esta máquina")
	}
}
