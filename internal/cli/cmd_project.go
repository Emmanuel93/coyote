package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Emmanuel93/coyote/internal/agentsmd"
	"github.com/Emmanuel93/coyote/internal/approval"
	"github.com/Emmanuel93/coyote/internal/ccf"
	"github.com/Emmanuel93/coyote/internal/ccfdoc"
	"github.com/Emmanuel93/coyote/internal/fsx"
	"github.com/Emmanuel93/coyote/internal/gitx"
	"github.com/Emmanuel93/coyote/internal/identity"
	"github.com/Emmanuel93/coyote/internal/ledger"
	"github.com/Emmanuel93/coyote/internal/project"
	"github.com/Emmanuel93/coyote/internal/standards"
	"github.com/Emmanuel93/coyote/internal/version"
)

func cmdVersion(a *app, args []string) error {
	fmt.Fprintln(a.stdout, "coyote "+version.String())
	return nil
}

func cmdInit(a *app, args []string) error {
	fs := a.flags("init", "[nombre] [--type T] [--purpose TEXTO] [--hub URL]")
	typ := fs.String("type", "other", "tipo: "+strings.Join(ccfdoc.ProjectTypes, ", "))
	purpose := fs.String("purpose", "", "qué hace el repo, en una línea")
	hub := fs.String("hub", "", "ruta del clon del hub de la organización (ADR-0018)")
	noGit := fs.Bool("no-git", false, "no inicializa git")
	noHooks := fs.Bool("no-hooks", false, "no instala el hook commit-msg")
	noClaude := fs.Bool("no-claude", false, "no crea .claude/settings.json")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	wd, err := a.workdir()
	if err != nil {
		return err
	}
	dir := wd
	if len(pos) > 0 {
		dir = pos[0]
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(wd, dir)
		}
	}
	res, err := project.Init(project.InitOptions{Dir: dir, Type: *typ, Hub: *hub, Purpose: *purpose,
		User: identity.Resolve(wd), NoGit: *noGit, NoHooks: *noHooks, NoClaude: *noClaude, Now: a.now()})
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "Proyecto %s listo en %s\n", res.Name, res.Root)
	if res.GitInit {
		fmt.Fprintln(a.stdout, "  git       repo inicializado en la rama main")
	}
	tw := table(a.stdout)
	for _, act := range res.Actions {
		fmt.Fprintf(tw, "  %s\t%s\n", act.Status, act.Path)
	}
	if res.LedgerPath != "" {
		fmt.Fprintf(tw, "  registrado\t%s\n", rel(res.Root, res.LedgerPath))
	} else {
		fmt.Fprintf(tw, "  sin cambios\t%s\n", "el proyecto ya estaba completo")
	}
	tw.Flush()
	for _, w := range res.Warnings {
		fmt.Fprintln(a.stdout, "  aviso: "+w)
	}
	fmt.Fprintln(a.stdout, "Siguiente: completa README.coyote.md (purpose, run, test) y corre coyote doctor.")
	return nil
}

type statusReport struct {
	Project    string            `json:"project"`
	Type       string            `json:"type"`
	Root       string            `json:"root"`
	Branch     string            `json:"branch,omitempty"`
	Dirty      int               `json:"dirty"`
	Autonomy   string            `json:"autonomy"`
	Features   map[string]bool   `json:"features"`
	Documents  map[string]string `json:"documents"`
	Layers     []string          `json:"layers"`
	Hub        string            `json:"hub,omitempty"` // organización, ref y commit del hub que rige
	chain      string
	Must       int      `json:"must"`
	Should     int      `json:"should"`
	Waived     int      `json:"waived"`
	Events     int      `json:"events"`
	Today      int      `json:"events_today"`
	Last       string   `json:"last_event,omitempty"`
	Approvals  int      `json:"approvals"`
	Gate       []string `json:"gate"`
	Pending    int      `json:"pending"`
	Warnings   []string `json:"warnings,omitempty"`
	LedgerBugs int      `json:"ledger_invalid_lines"`
}

func cmdStatus(a *app, args []string) error {
	fs := a.flags("status", "[--json]")
	asJSON := fs.Bool("json", false, "salida JSON")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	root, cfg, err := a.project()
	if err != nil {
		return err
	}
	r := statusReport{Project: cfg.Name, Type: cfg.Type, Root: root, Autonomy: cfg.Autonomy, Documents: map[string]string{}, Features: map[string]bool{}}
	for name := range project.KnownFeatures {
		r.Features[name] = cfg.Feature(name)
	}
	if gitx.IsRepo(root) {
		r.Branch, r.Dirty = gitx.Branch(root), gitx.Dirty(root)
	}
	for _, name := range []string{ccfdoc.ReadmeFile, ccfdoc.ContextFile} {
		r.Documents[name] = loadDoc(root, name).summary()
	}
	st, res, err := a.lint(root, cfg, false)
	if err != nil {
		return err
	}
	r.Documents["AGENTS.md"] = "vigente"
	if f := checkAgentsMD(&standards.Context{Root: root, Standard: st, Autonomy: cfg.Autonomy}, standards.Check{}); len(f) > 0 {
		r.Documents["AGENTS.md"] = f[0].Msg
	}
	r.Layers, r.Warnings, r.chain = st.Layers, st.Warnings, st.Chain()
	if st.Hub != nil {
		r.Hub = st.Hub.String()
	}
	r.Must, r.Should, r.Waived = res.Count("MUST", false), res.Count("SHOULD", false), res.Count("", true)
	entries, probs, err := ledger.Open(root).ReadAll()
	if err != nil {
		return err
	}
	r.Events, r.LedgerBugs = len(entries), len(probs)
	today := a.now().Format("2006-01-02")
	for _, e := range entries {
		if e.Line.TS.Format("2006-01-02") == today {
			r.Today++
		}
	}
	if n := len(entries); n > 0 {
		last := entries[n-1].Line
		r.Last = last.TS.Format(ccf.TSLayout) + " " + last.Type + " " + last.What
	}
	if items, err := os.ReadDir(filepath.Join(root, "coyote", "approvals")); err == nil {
		for _, it := range items {
			if strings.HasSuffix(it.Name(), ".json") {
				r.Approvals++
			}
		}
	}
	r.Gate = gateInstalled(root)
	if q, err := (&approval.Store{Root: root, Now: a.now}).Queue(); err == nil {
		for _, p := range q {
			if p.Status == "pending" {
				r.Pending++
			}
		}
	}
	if *asJSON {
		enc := json.NewEncoder(a.stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	head := fmt.Sprintf("coyote · %s (%s)", r.Project, r.Type)
	if r.Branch != "" {
		head += fmt.Sprintf(" · rama %s · %d cambios sin commit", r.Branch, r.Dirty)
	}
	fmt.Fprintln(a.stdout, head)
	tw := table(a.stdout)
	fmt.Fprintln(tw, "Documentos\t")
	for _, name := range []string{ccfdoc.ReadmeFile, ccfdoc.ContextFile, "AGENTS.md"} {
		fmt.Fprintf(tw, "  %s\t%s\n", name, r.Documents[name])
	}
	fmt.Fprintf(tw, "Estándar\t%s · %d MUST · %d SHOULD · %d dispensadas\n", r.chain, r.Must, r.Should, r.Waived)
	ledgerLine := fmt.Sprintf("%d eventos · %d hoy", r.Events, r.Today)
	if r.Last != "" {
		ledgerLine += " · último " + shortText(r.Last, 60)
	}
	if r.LedgerBugs > 0 {
		ledgerLine += fmt.Sprintf(" · %d líneas inválidas", r.LedgerBugs)
	}
	fmt.Fprintf(tw, "Ledger\t%s\n", ledgerLine)
	gateLine := "no instalado (coyote install --ide claude-code o cursor)"
	if len(r.Gate) > 0 {
		gateLine = "activo en " + strings.Join(r.Gate, ", ")
	}
	fmt.Fprintf(tw, "Gate\t%s · %d propuestas pendientes\n", gateLine, r.Pending)
	fmt.Fprintf(tw, "Aprobaciones\t%d registros\n", r.Approvals)
	mode := r.Autonomy
	if mode != "manual" {
		mode += " (el motor de workstreams se detiene menos; el gate aplica igual)"
	}
	fmt.Fprintf(tw, "Autonomía\t%s\n", mode)
	names := make([]string, 0, len(r.Features))
	for name := range r.Features {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		state := "apagada"
		if r.Features[name] {
			state = "prendida"
		}
		fmt.Fprintf(tw, "Bandera %s\t%s\n", name, state)
	}
	tw.Flush()
	for _, w := range r.Warnings {
		fmt.Fprintln(a.stdout, "aviso: "+w)
	}
	return nil
}

func cmdNote(a *app, args []string) error {
	fs := a.flags("note", "<texto> --type T [--scope S] [--ref R] [--agent A]")
	typ := fs.String("type", "", "tipo: "+strings.Join(ccfdoc.ContextTypeNames(), ", "))
	scope := fs.String("scope", "general", "ámbito: módulo o tema, sin espacios")
	ref := fs.String("ref", "-", "referencia: ruta#L, ADR o workstream")
	file := fs.String("file", ccfdoc.ContextFile, "archivo de contexto")
	ws := fs.String("ws", "-", "workstream")
	agent := fs.String("agent", "", "agente que registra la nota a nombre de la persona")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if *typ == "" || len(pos) == 0 {
		fs.Usage()
		return fail(2, "")
	}
	root, cfg, err := a.project()
	if err != nil {
		return err
	}
	if *agent, err = a.agentFor(root, *agent); err != nil {
		return err
	}
	text := strings.Join(pos, " ")
	entry, err := ccfdoc.FormatEntry(*typ, *scope, text, *ref)
	if err != nil {
		return err
	}
	if err := fsx.NoSymlinks(root, *file); err != nil {
		return fail(1, "%v", err)
	}
	p := filepath.Join(root, *file)
	data, err := fsx.ReadCapped(p, fsx.MaxText)
	if err != nil {
		return fail(1, "falta %s; corre coyote init", *file)
	}
	updated := ccfdoc.AppendEntry(string(data), entry, a.now().Format("2006-01-02"))
	d, issues := ccfdoc.Parse(*file, []byte(updated))
	for _, i := range append(issues, d.Validate()...) {
		if i.Error {
			return fail(1, "la nota no se agregó: %s", i.String())
		}
	}
	if err := os.WriteFile(p, []byte(updated), 0o644); err != nil {
		return err
	}
	person := identity.Resolve(root)
	scopeID := ledgerID(strings.Split(entry, "|")[1])
	line := ccf.Line{TS: a.now(), Actor: person.Actor(*agent), Project: ledgerID(*ws), Repo: cfg.Name, Type: "note",
		Scope: scopeID, What: ccf.ShortWhat(*typ+": "+text, ccf.MaxWhatWords), Refs: []string{"doc:" + filepath.ToSlash(*file)}, Status: "ok"}
	if _, err := ledger.Open(root).Append(line, person.Slug); err != nil {
		return fmt.Errorf("la nota se agregó pero el ledger falló: %w", err)
	}
	fmt.Fprintf(a.stdout, "nota agregada a %s: %s\n", *file, entry)
	if status, err := a.refreshAgents(root, cfg); err != nil {
		fmt.Fprintln(a.stderr, "aviso: AGENTS.md no se actualizó: "+err.Error())
	} else if status == "actualizado" {
		fmt.Fprintln(a.stdout, "AGENTS.md actualizado")
	}
	return nil
}

// refreshAgents regenera AGENTS.md si lo generó coyote; uno ajeno no se toca.
func (a *app) refreshAgents(root string, cfg *project.Config) (string, error) {
	if got, err := fsx.ReadCapped(filepath.Join(root, "AGENTS.md"), fsx.MaxText); err == nil && !strings.HasPrefix(string(got), agentsmd.Marker) {
		return "omitido", nil
	}
	st, err := standards.Load(root, a.now())
	if err != nil {
		return "", err
	}
	content, err := agentsmd.Generate(root, st, cfg.Autonomy)
	if err != nil {
		return "", err
	}
	return agentsmd.Write(root, content, false)
}

func cmdRecord(a *app, args []string) error {
	fs := a.flags("record", "<type> <qué> [opciones]")
	scope := fs.String("scope", "-", "ámbito")
	ws := fs.String("ws", "-", "workstream (W-0001) o -")
	repo := fs.String("repo", "", "repo (por defecto, el nombre del proyecto)")
	refs := fs.String("refs", "", "referencias clave:valor separadas por espacios")
	tk := fs.String("tokens", "", "tokens entrada/caché/salida, p. ej. 12.4k/8.7k/1.1k")
	cost := fs.String("cost", "", "USD entrada+salida, p. ej. 0.009+0.011")
	status := fs.String("status", "ok", "ok, pend, fail o skip")
	agent := fs.String("agent", "", "agente que actuó a nombre de la persona")
	ts := fs.String("ts", "", "marca de tiempo RFC 3339 (por defecto, ahora)")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 2 {
		fs.Usage()
		return fail(2, "tipos: %s", strings.Join(ccf.TypeNames(), ", "))
	}
	root, cfg, err := a.project()
	if err != nil {
		return err
	}
	if *agent, err = a.agentFor(root, *agent); err != nil {
		return err
	}
	person := identity.Resolve(root)
	when := a.now()
	if *ts != "" {
		if when, err = ccf.ParseTS(*ts); err != nil {
			return err
		}
	}
	if *repo == "" {
		*repo = cfg.Name
	}
	line := ccf.Line{TS: when, Actor: person.Actor(*agent), Project: *ws, Repo: *repo, Type: pos[0],
		Scope: *scope, What: strings.Join(pos[1:], " "), Refs: strings.Fields(*refs), Status: *status}
	if *tk != "" {
		t, err := ccf.ParseTokens(*tk)
		if err != nil {
			return err
		}
		line.Tokens = &t
	}
	if *cost != "" {
		c, err := ccf.ParseCost(*cost)
		if err != nil {
			return err
		}
		line.Cost = &c
	}
	p, err := ledger.Open(root).Append(line, person.Slug)
	if err != nil {
		return err
	}
	fmt.Fprintln(a.stdout, line.String())
	fmt.Fprintln(a.stderr, "registrado en "+rel(root, p))
	return nil
}

func parseSince(s string, now time.Time) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if strings.HasSuffix(s, "d") {
		if n, err := strconv.Atoi(strings.TrimSuffix(s, "d")); err == nil {
			return now.AddDate(0, 0, -n), nil
		}
	}
	if d, err := time.ParseDuration(s); err == nil {
		return now.Add(-d), nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}
	return time.Time{}, fail(2, "--since inválido %q (usa 7d, 24h o 2026-09-01)", s)
}

func cmdLog(a *app, args []string) error {
	fs := a.flags("log", "[--limit N] [--type T] [--user U] [--ws W] [--since 7d] [--raw]")
	limit := fs.Int("limit", 20, "máximo de eventos")
	typ := fs.String("type", "", "filtra por tipo")
	user := fs.String("user", "", "filtra por persona (sin @)")
	ws := fs.String("ws", "", "filtra por workstream")
	since := fs.String("since", "", "desde: 7d, 24h o 2026-09-01")
	raw := fs.Bool("raw", false, "imprime las líneas CCF tal cual")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	root, _, err := a.project()
	if err != nil {
		return err
	}
	from, err := parseSince(*since, a.now())
	if err != nil {
		return err
	}
	entries, probs, err := ledger.Open(root).ReadAll()
	if err != nil {
		return err
	}
	who := strings.TrimPrefix(*user, "@")
	var sel []ledger.Entry
	for i := len(entries) - 1; i >= 0; i-- {
		l := entries[i].Line
		switch {
		case *typ != "" && l.Type != *typ,
			who == "system" && l.Actor != "system",
			who != "" && who != "system" && !strings.HasPrefix(l.Actor+"/", "@"+who+"/"),
			*ws != "" && l.Project != *ws,
			!from.IsZero() && l.TS.Before(from):
			continue
		}
		sel = append(sel, entries[i])
	}
	total := len(sel)
	tk, cost := ledger.Totals(sel)
	if ledger.SummaryTypes[*typ] {
		// Pidieron solo cierres: se suman los cierres, que en el total general no cuentan.
		tk, cost = ledger.SummaryTotals(sel)
	}
	if *limit > 0 && len(sel) > *limit {
		sel = sel[:*limit]
	}
	if *raw {
		for _, e := range sel {
			fmt.Fprintln(a.stdout, e.Line.String())
		}
		return nil
	}
	tw := table(a.stdout)
	fmt.Fprintln(tw, "cuándo\tquién\ttipo\támbito\tqué\ttokens\tcosto\testado")
	for _, e := range sel {
		l := e.Line
		tks, cs := "-", "-"
		if l.Tokens != nil {
			tks = l.Tokens.String()
		}
		if l.Cost != nil {
			cs = "$" + ccf.FormatUSD(l.Cost.Total())
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", l.TS.Format(ccf.TSLayout), l.Actor, l.Type, l.Scope, shortText(l.What, 48), tks, cs, l.Status)
	}
	tw.Flush()
	fmt.Fprintf(a.stdout, "%d de %d eventos · tokens %s · costo $%s (entrada $%s, salida $%s)\n",
		len(sel), total, tk.String(), ccf.FormatUSD(cost.Total()), ccf.FormatUSD(cost.In), ccf.FormatUSD(cost.Out))
	if len(probs) > 0 {
		fmt.Fprintf(a.stdout, "aviso: %d líneas inválidas en el ledger (primera: %s:%d %v)\n", len(probs), rel(root, probs[0].File), probs[0].LineNo, probs[0].Err)
	}
	return nil
}

func cmdGenerate(a *app, args []string) error {
	if len(args) == 0 || args[0] != "agents" {
		return fail(2, "uso: coyote generate agents [--check] [--force]")
	}
	fs := a.flags("generate agents", "[--check] [--force]")
	check := fs.Bool("check", false, "falla si AGENTS.md no está vigente, sin escribir")
	force := fs.Bool("force", false, "reemplaza un AGENTS.md que no generó coyote")
	if _, err := parseArgs(fs, args[1:]); err != nil {
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
	content, err := agentsmd.Generate(root, st, cfg.Autonomy)
	if err != nil {
		return err
	}
	if *check {
		got, _ := fsx.ReadCapped(filepath.Join(root, "AGENTS.md"), fsx.MaxText)
		if string(got) != content {
			return fail(1, "AGENTS.md desactualizado; corre coyote generate agents")
		}
		fmt.Fprintln(a.stdout, "AGENTS.md vigente")
		return nil
	}
	status, err := agentsmd.Write(root, content, *force)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "AGENTS.md %s\n", status)
	return nil
}

func cmdHooks(a *app, args []string) error {
	if len(args) == 0 || args[0] != "install" {
		return fail(2, "uso: coyote hooks install [--force]")
	}
	fs := a.flags("hooks install", "[--force]")
	force := fs.Bool("force", false, "reemplaza un hook commit-msg ajeno")
	if _, err := parseArgs(fs, args[1:]); err != nil {
		return err
	}
	root, _, err := a.project()
	if err != nil {
		return err
	}
	p, status, err := project.InstallHook(root, *force, false)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "hook commit-msg %s en %s\n", status, p)
	return nil
}

// gateInstalled dice en qué IDEs está instalado el gate humano.
func gateInstalled(root string) []string {
	var out []string
	for _, f := range []struct{ ide, rel string }{{"claude-code", ".claude/settings.json"}, {"cursor", ".cursor/hooks.json"}} {
		if data, err := fsx.ReadCapped(filepath.Join(root, filepath.FromSlash(f.rel)), fsx.MaxText); err == nil && strings.Contains(string(data), "coyote-gate.sh") {
			out = append(out, f.ide)
		}
	}
	return out
}
