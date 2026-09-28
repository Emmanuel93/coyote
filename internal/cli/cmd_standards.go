package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Emmanuel93/coyote/internal/attribution"
	"github.com/Emmanuel93/coyote/internal/ccfdoc"
	"github.com/Emmanuel93/coyote/internal/gitx"
	"github.com/Emmanuel93/coyote/internal/identity"
	"github.com/Emmanuel93/coyote/internal/project"
	"github.com/Emmanuel93/coyote/internal/standards"
)

func cmdStandards(a *app, args []string) error {
	if len(args) == 0 {
		return fail(2, "uso: coyote standards lint | show | explain <id> | diff")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "lint":
		return standardsLint(a, rest)
	case "show", "diff":
		return standardsShow(a, sub == "diff", rest)
	case "explain":
		return standardsExplain(a, rest)
	}
	return fail(2, "subcomando desconocido %q; usa lint, show, explain o diff", sub)
}

func standardsLint(a *app, args []string) error {
	fs := a.flags("standards lint", "[--strict] [--json] [--scripts]")
	strict := fs.Bool("strict", false, "también falla con hallazgos SHOULD")
	asJSON := fs.Bool("json", false, "salida JSON")
	scripts := fs.Bool("scripts", false, "corre los checks script de rules.yaml (ejecutan comandos del repo o del hub)")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	root, cfg, err := a.project()
	if err != nil {
		return err
	}
	st, res, err := a.lint(root, cfg, *scripts)
	if err != nil {
		return err
	}
	failing := res.Failing(*strict)
	if *asJSON {
		enc := json.NewEncoder(a.stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(map[string]any{"layers": st.Layers, "warnings": st.Warnings, "result": res, "failing": failing}); err != nil {
			return err
		}
	} else {
		for _, w := range st.Warnings {
			fmt.Fprintln(a.stdout, "aviso: "+w)
		}
		if profile, _ := profileAndWaivers(root, cfg); profile != cfg.Type {
			fmt.Fprintf(a.stdout, "aviso: el perfil del estándar (%s) no es el tipo del proyecto (%s); las reglas de perfil siguen al perfil\n", profile, cfg.Type)
		}
		printFindings(a, res)
		fmt.Fprintf(a.stdout, "Capas: %s · %d reglas verificadas · %d MUST · %d SHOULD · %d dispensadas\n",
			strings.Join(st.Layers, " → "), res.Checked, res.Count("MUST", false), res.Count("SHOULD", false), res.Count("", true))
		if n := res.ScriptsSkipped(); n > 0 {
			fmt.Fprintf(a.stdout, "%d checks script sin correr: revísalos en rules.yaml y usa --scripts\n", n)
		}
	}
	if failing > 0 {
		return fail(1, "")
	}
	return nil
}

func printFindings(a *app, res *standards.Result) {
	groups := []struct {
		title  string
		match  func(standards.Finding) bool
		reason bool
	}{
		{"MUST", func(f standards.Finding) bool { return !f.Waived && f.Level == "MUST" }, false},
		{"SHOULD", func(f standards.Finding) bool { return !f.Waived && f.Level == "SHOULD" }, false},
		{"MAY", func(f standards.Finding) bool { return !f.Waived && f.Level == "MAY" }, false},
		{"Dispensadas", func(f standards.Finding) bool { return f.Waived }, true},
	}
	for _, g := range groups {
		tw := table(a.stdout)
		n := 0
		for _, f := range res.Findings {
			if !g.match(f) {
				continue
			}
			if n == 0 {
				fmt.Fprintln(a.stdout, g.title)
			}
			n++
			where := f.Path
			if f.Line > 0 {
				where = fmt.Sprintf("%s:%d", f.Path, f.Line)
			}
			msg := f.Msg
			if g.reason {
				msg += " (motivo: " + f.WaiveReason + ")"
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\n", f.RuleID, where, msg)
			if f.Fix != "" && !g.reason {
				fmt.Fprintf(tw, "  \t\tarreglo: %s\n", f.Fix)
			}
		}
		tw.Flush()
	}
}

func standardsShow(a *app, onlyDiff bool, args []string) error {
	fs := a.flags("standards show", "")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	root, cfg, err := a.project()
	if err != nil {
		return err
	}
	st, err := standards.Load(root, a.now())
	if err != nil {
		return err
	}
	profile, _ := profileAndWaivers(root, cfg)
	head := fmt.Sprintf("Capas: %s · perfil %s", strings.Join(st.Layers, " → "), profile)
	if profile != cfg.Type {
		head += fmt.Sprintf(" (el tipo del proyecto es %s)", cfg.Type)
	}
	fmt.Fprintf(a.stdout, "%s · idioma de docs %s, de código %s\n", head, st.Language.Docs, st.Language.Code)
	tw := table(a.stdout)
	shown := 0
	if st.Detached {
		reason := st.DetachReason
		if reason == "" {
			reason = "sin motivo"
		}
		fmt.Fprintf(tw, "  -\t\tsin coyote:default (extends: none)\t[motivo: %s]\n", reason)
		for _, r := range st.Dropped() {
			fmt.Fprintf(tw, "  %s\tFUERA\t%s\t[era %s en coyote:default]\n", r.ID, r.Title, r.Level)
			shown++
		}
	}
	for _, r := range st.Rules {
		if onlyDiff && r.Source == standards.DefaultRef && r.Note == "" {
			continue
		}
		state := r.Level
		if r.Disabled {
			state = "OFF"
		}
		extra := r.Source
		if r.Note != "" {
			extra += " · " + r.Note
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t[%s]\n", r.ID, state, r.Title, extra)
		shown++
	}
	tw.Flush()
	if onlyDiff && shown == 0 {
		fmt.Fprintln(a.stdout, "  sin diferencias con coyote:default")
	}
	for _, w := range st.Warnings {
		fmt.Fprintln(a.stdout, "aviso: "+w)
	}
	return nil
}

func standardsExplain(a *app, args []string) error {
	if len(args) == 0 {
		return fail(2, "uso: coyote standards explain <id>")
	}
	root, _, err := a.project()
	if err != nil {
		return err
	}
	st, err := standards.Load(root, a.now())
	if err != nil {
		return err
	}
	r := st.Find(args[0])
	if r == nil {
		return fail(1, "no existe la regla %s", args[0])
	}
	tw := table(a.stdout)
	fmt.Fprintf(tw, "%s\t%s\n", r.ID, r.Title)
	level := r.Level
	if r.Disabled {
		level = "desactivada (era " + r.Origin + ")"
	} else if r.Origin != "" && r.Origin != r.Level {
		level += " (original " + r.Origin + ")"
	}
	fmt.Fprintf(tw, "nivel\t%s\n", level)
	if r.Scope != "" {
		fmt.Fprintf(tw, "alcance\t%s\n", r.Scope)
	}
	if len(r.Profiles) > 0 {
		fmt.Fprintf(tw, "perfiles\t%s\n", strings.Join(r.Profiles, ", "))
	}
	fmt.Fprintf(tw, "capa\t%s\n", r.Source)
	if r.Note != "" {
		fmt.Fprintf(tw, "ajuste\t%s\n", r.Note)
	}
	if r.Why != "" {
		fmt.Fprintf(tw, "por qué\t%s\n", r.Why)
	}
	if r.Fix != "" {
		fmt.Fprintf(tw, "arreglo\t%s\n", r.Fix)
	}
	checks := r.AllChecks()
	if len(checks) == 0 {
		fmt.Fprintf(tw, "verificación\tde proceso: la aplican gates y agentes, no el lint\n")
	}
	for _, c := range checks {
		detail := c.Type
		for _, p := range []struct{ k, v string }{{"files", strings.Join(c.Files, ", ")}, {"paths", strings.Join(c.Paths, ", ")},
			{"except", strings.Join(c.Except, ", ")}, {"pattern", c.Pattern}, {"script", c.Script}} {
			if p.v != "" {
				detail += fmt.Sprintf(" %s=%s", p.k, p.v)
			}
		}
		if c.Max > 0 {
			detail += fmt.Sprintf(" max=%d", c.Max)
		}
		if c.Commits > 0 {
			detail += fmt.Sprintf(" commits=%d", c.Commits)
		}
		fmt.Fprintf(tw, "verificación\t%s\n", detail)
	}
	return tw.Flush()
}

type doctorCheck struct {
	name, state, detail string // state: ok, warn, fail
}

func cmdDoctor(a *app, args []string) error {
	fs := a.flags("doctor", "[--ide claude-code|cursor|all]")
	ide := fs.String("ide", "", "prueba además el gate y la instalación de ese IDE")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	var ides []string
	if *ide != "" {
		var err error
		if ides, err = ideList(*ide); err != nil {
			return err
		}
	}
	var checks []doctorCheck
	add := func(name, state, detail string) { checks = append(checks, doctorCheck{name, state, detail}) }
	if v := gitx.Version(); v == "" {
		add("git", "fail", "no está instalado")
	} else {
		add("git", "ok", v)
	}
	root, cfg, err := a.project()
	if err != nil {
		add("proyecto", "fail", err.Error())
		return a.printDoctor(checks)
	}
	add("proyecto", "ok", fmt.Sprintf("%s (%s) en %s", cfg.Name, cfg.Type, root))
	if gitx.IsRepo(root) {
		add("repo git", "ok", "rama "+gitx.Branch(root))
		person := identity.Resolve(root)
		name, email, identErr := gitx.Ident(root, "author")
		attr, _ := attribution.Default()
		switch {
		case !person.Configured() || identErr != nil:
			add("autoría", "fail", "configura git user.name y user.email; los commits llevan la autoría de una persona")
		case attr != nil:
			if f, ok := attr.AIIdentity(name, email); ok {
				add("autoría", "fail", "la identidad de git es una herramienta de IA ("+f.Text+"); usa la tuya")
				break
			}
			fallthrough
		default:
			add("autoría", "ok", fmt.Sprintf("%s <%s> · ledger @%s", name, email, person.Slug))
		}
		switch {
		case project.HookCurrent(root):
			add("hook commit-msg", "ok", "quita atribución de IA en cada commit")
		case project.HookInstalled(root):
			add("hook commit-msg", "warn", "instalado, pero de otra versión o con cambios; revísalo y corre coyote hooks install --force")
		default:
			add("hook commit-msg", "warn", "no instalado; corre coyote hooks install")
		}
	} else {
		add("repo git", "fail", "el proyecto no está en un repo git")
	}
	attr, err := attribution.Default()
	if err != nil {
		add("filtro de atribución", "fail", err.Error())
	} else {
		sample := "feat: x\n\nCo-Authored-By: " + "Cla" + "ude <noreply@" + "anthropic.com>\n"
		if out, found := attr.ScrubLines(sample, true); len(found) == 1 && strings.TrimSpace(out) == "feat: x" {
			add("filtro de atribución", "ok", fmt.Sprintf("identidades de bot, %d patrones de línea y %d de frase", len(attr.LinePatterns), len(attr.PhrasePatterns)))
		} else {
			add("filtro de atribución", "fail", "no quitó un trailer de prueba")
		}
	}
	cs, cd := claudeSettingsState(root)
	add(".claude/settings.json", cs, cd)
	for _, name := range []string{ccfdoc.ReadmeFile, ccfdoc.ContextFile} {
		ds := loadDoc(root, name)
		state := "ok"
		switch {
		case !ds.present || ccfdoc.HasErrors(ds.issues):
			state = "fail"
		case len(ds.issues) > 0:
			state = "warn"
		}
		detail := ds.summary()
		for _, i := range ds.issues {
			detail += "; " + i.String()
			break
		}
		add(name, state, detail)
	}
	st, res, err := a.lint(root, cfg, false)
	if err != nil {
		add("estándar", "fail", err.Error())
	} else {
		if f := checkAgentsMD(&standards.Context{Root: root, Standard: st, Autonomy: cfg.Autonomy}, standards.Check{}); len(f) > 0 {
			add("AGENTS.md", "warn", f[0].Msg+"; corre coyote generate agents")
		} else {
			add("AGENTS.md", "ok", "vigente")
		}
		state, detail := "ok", fmt.Sprintf("%s · %d reglas verificadas", strings.Join(st.Layers, " → "), res.Checked)
		if n := res.Count("SHOULD", false); n > 0 {
			state, detail = "warn", detail+fmt.Sprintf(" · %d SHOULD", n)
		}
		if n := res.Count("MUST", false); n > 0 {
			state, detail = "fail", detail+fmt.Sprintf(" · %d MUST (corre coyote standards lint)", n)
		}
		add("estándar", state, detail)
	}
	if _, err := exec.LookPath("coyote"); err != nil {
		add("coyote en PATH", "warn", "no está; los hooks usan el filtro básico (make install lo instala)")
	} else {
		add("coyote en PATH", "ok", "los hooks usan los patrones completos")
	}
	for _, x := range ides {
		a.ideChecks(root, cfg, x, add)
	}
	return a.printDoctor(checks)
}

func claudeSettingsState(root string) (string, string) {
	data, err := os.ReadFile(filepath.Join(root, ".claude", "settings.json"))
	if err != nil {
		return "warn", "no existe; coyote init la crea con la atribución apagada y el gate"
	}
	var s struct {
		Attribution struct {
			Commit *string `json:"commit"`
			PR     *string `json:"pr"`
		} `json:"attribution"`
		IncludeCoAuthoredBy *bool `json:"includeCoAuthoredBy"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return "warn", "JSON inválido: " + err.Error()
	}
	off := s.Attribution.Commit != nil && *s.Attribution.Commit == "" && s.Attribution.PR != nil && *s.Attribution.PR == ""
	legacyOff := s.IncludeCoAuthoredBy != nil && !*s.IncludeCoAuthoredBy
	full := strings.Contains(string(data), "coyote-gate.sh")
	gate := strings.Contains(string(data), "gate attribution")
	switch {
	case (off || legacyOff) && full:
		return "ok", "atribución apagada y gate humano activo (coyote install)"
	case (off || legacyOff) && gate:
		return "ok", "atribución apagada y gate de atribución; el gate humano se activa con coyote install --ide claude-code"
	case off || legacyOff:
		return "warn", "atribución apagada, pero falta el gate PreToolUse"
	default:
		return "warn", "la atribución de Claude Code sigue activa; agrega attribution.commit y attribution.pr vacíos"
	}
}

func (a *app) printDoctor(checks []doctorCheck) error {
	tw := table(a.stdout)
	fails, warns := 0, 0
	for _, c := range checks {
		mark := "✓"
		switch c.state {
		case "warn":
			mark = "!"
			warns++
		case "fail":
			mark = "✗"
			fails++
		}
		fmt.Fprintf(tw, "%s %s\t%s\n", mark, c.name, c.detail)
	}
	tw.Flush()
	fmt.Fprintf(a.stdout, "%d fallas · %d avisos\n", fails, warns)
	if fails > 0 {
		return fail(1, "")
	}
	return nil
}
