// Package cli implementa los comandos de coyote. Solo usa la biblioteca
// estándar (ADR-0004): un archivo por grupo de comandos y flags intercalados.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Emmanuel93/coyote/internal/agentsmd"
	"github.com/Emmanuel93/coyote/internal/ccfdoc"
	"github.com/Emmanuel93/coyote/internal/fsx"
	"github.com/Emmanuel93/coyote/internal/project"
	"github.com/Emmanuel93/coyote/internal/standards"
)

type app struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	dir    string
	now    func() time.Time
}

type command struct {
	name    string
	args    string
	summary string
	run     func(a *app, args []string) error
}

var commands []*command

func init() {
	commands = []*command{
		{"init", "[nombre] [--type T] [--purpose TEXTO] [--hub URL]", "crea o adopta un proyecto coyote", cmdInit},
		{"status", "[--json]", "estado: documentos, estándar, ledger, git", cmdStatus},
		{"note", "<texto> --type T [--scope S] [--ref R] [--agent A]", "agrega una entrada a CONTEXT.coyote.md", cmdNote},
		{"record", "<type> <qué> [--scope S] [--tokens e/c/s] [--cost e+s]", "escribe un evento en el ledger", cmdRecord},
		{"log", "[--limit N] [--type T] [--user U] [--since 7d]", "muestra el ledger con totales", cmdLog},
		{"commit", "-m <mensaje> [-a] [--ws W]", "commit con tu autoría, formato R2 y sin atribución de IA", cmdCommit},
		{"standards", "lint | show | explain <id> | diff", "estándar por capas y su validación", cmdStandards},
		{"attribution", "check [rutas] | scrub [archivo]", "detecta o quita atribución a herramientas de IA", cmdAttribution},
		{"gate", "check [--ide IDE] < hook.json", "gate humano para los hooks previos de los IDEs (ADR-0009)", cmdGate},
		{"secrets", "list | scan [--staged | --range base...head]", "archivos de secretos y sus nombres, sin valores; secretos escritos en el repo (ADR-0016)", cmdSecrets},
		{"infra", "check | plan <plan.json> | propose", "inventario de la infraestructura (R13), su revisión y el plan de Terraform (ADR-0017)", cmdInfra},
		{"approvals", "[--all] [--json]", "cola de propuestas y aprobaciones vigentes", cmdApprovals},
		{"review", "[id...]", "muestra el comando o el diff que se aprobaría", cmdReview},
		{"approve", "<id>... | --all | --bash CMD [--uses N] [--for 1h]", "aprueba acciones exactas (solo una persona, en su terminal)", cmdApprove},
		{"reject", "<id>... --reason TEXTO", "rechaza una propuesta; el agente verá el motivo", cmdReject},
		{"revoke", "<id> --reason TEXTO", "revoca una aprobación vigente", cmdRevoke},
		{"propose", "--bash CMD [--cwd DIR]", "encola un comando para que la persona lo apruebe", cmdPropose},
		{"get", "context [repo] [--scope S] [--query Q] [--budget N]", "paquete de contexto acotado para un agente", cmdGet},
		{"ask", "\"pregunta\" [--scope S] [--repo R]", "busca en el contexto del proyecto con referencias", cmdAsk},
		{"index", "[--rebuild]", "arma el índice local y muestra su tamaño", cmdIndex},
		{"repo", "add <nombre> [url] [--path P] | list | fetch [nombre]", "repos del proyecto y sus documentos", cmdRepo},
		{"map", "[--check] [--json]", "mapa de interfaces del producto: quién expone y quién usa cada endpoint y tópico", cmdMap},
		{"extract", "[repo...] [--stdout] [--check]", "propone README.coyote.md y CONTEXT.coyote.md de cada repo desde su código", cmdExtract},
		{"impact", "<endpoint|texto> | --topic T | --diff repo=RANGO [--format md]", "qué rompe un cambio en los repos del producto", cmdImpact},
		{"run", "--agent A \"tarea\" [--ws W] [--risk R] [--diff repo=RANGO] [--dry-run]", "corre un paso de un agente con Claude Code, con topes, gate y costo", cmdRun},
		{"ws", "check|status|run|continue <W> [--redo TEXTO | --retry]", "corre el plan de un workstream con puntos de control (ADR-0014)", cmdWS},
		{"ci", "impact --repo nombre=ruta... [--policy warn|fail] [--comment]", "impacto de un PR en los repos del producto, dentro del pipeline", cmdCI},
		{"router", "[--init] [--risk R]", "modelo y topes que el router da a cada agente", cmdRouter},
		{"close", "<W> [--dry-run]", "cierra un workstream con su consumo real desde el ledger", cmdClose},
		{"push", "[--remote origin] [--agent A] [--dry-run]", "publica con autoría, estándar y ritmo humano revisados", cmdPush},
		{"pull", "[--remote origin]", "trae cambios y resume el contexto nuevo", cmdPull},
		{"auth", "login | status [--check] | logout", "token de GitHub en el llavero, nunca en archivos", cmdAuth},
		{"web", "[--addr 127.0.0.1:7410]", "costos por proyecto y por persona en el navegador", cmdWeb},
		{"generate", "agents [--check]", "genera AGENTS.md desde los documentos coyote", cmdGenerate},
		{"install", "--ide IDE|all | --ci github [--check]", "instala el gate, los agentes y las skills en el IDE, o el pipeline del producto", cmdInstall},
		{"hooks", "install [--force]", "instala el hook commit-msg", cmdHooks},
		{"doctor", "[--ide IDE [--canary]]", "verifica herramienta, identidad, documentos, estándar, gate y el nivel medido de cada IDE", cmdDoctor},
		{"version", "", "muestra la versión", cmdVersion},
		{"help", "[comando]", "muestra esta ayuda", cmdHelp},
	}
	standards.Register("agents_md_current", checkAgentsMD)
}

// Main ejecuta la CLI y devuelve el código de salida.
func Main(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	a := &app{stdin: stdin, stdout: stdout, stderr: stderr, now: func() time.Time { return time.Now().UTC() }}
	for len(args) > 0 && args[0] == "-C" {
		if len(args) < 2 {
			fmt.Fprintln(stderr, "coyote: -C requiere una ruta")
			return 2
		}
		a.dir, args = args[1], args[2:]
	}
	if len(args) == 0 {
		args = []string{"help"}
	}
	switch args[0] {
	case "-h", "--help":
		args[0] = "help"
	case "-v", "--version":
		args[0] = "version"
	}
	for _, c := range commands {
		if c.name == args[0] {
			return a.exitCode(c.run(a, args[1:]))
		}
	}
	fmt.Fprintf(stderr, "coyote: comando desconocido %q; corre coyote help\n", args[0])
	return 2
}

type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }

func fail(code int, format string, a ...any) error {
	return &exitError{code, fmt.Sprintf(format, a...)}
}

func (a *app) exitCode(err error) int {
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return 0
	}
	var ee *exitError
	if errors.As(err, &ee) {
		if ee.msg != "" {
			fmt.Fprintln(a.stderr, ee.msg)
		}
		return ee.code
	}
	fmt.Fprintln(a.stderr, "coyote: "+err.Error())
	return 1
}

func (a *app) flags(name, usage string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	fs.Usage = func() {
		fmt.Fprintf(a.stderr, "uso: coyote %s %s\n", name, usage)
		fs.PrintDefaults()
	}
	return fs
}

// parseArgs permite flags antes y después de los argumentos posicionales.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, err
			}
			return nil, fail(2, "")
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return pos, nil
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}

func (a *app) workdir() (string, error) {
	d := a.dir
	if d == "" {
		var err error
		if d, err = os.Getwd(); err != nil {
			return "", err
		}
	}
	return filepath.Abs(d)
}

func (a *app) project() (string, *project.Config, error) {
	wd, err := a.workdir()
	if err != nil {
		return "", nil, err
	}
	root, err := project.FindRoot(wd)
	if err != nil {
		return "", nil, err
	}
	cfg, err := project.Load(root)
	if err != nil {
		return "", nil, err
	}
	return root, cfg, nil
}

type docState struct {
	name    string
	present bool
	doc     *ccfdoc.Doc
	issues  []ccfdoc.Issue
}

func loadDoc(root, name string) docState {
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		return docState{name: name}
	}
	d, issues := ccfdoc.Parse(name, data)
	return docState{name: name, present: true, doc: d, issues: append(issues, d.Validate()...)}
}

func (d docState) summary() string {
	if !d.present {
		return "falta"
	}
	errs, warns := 0, 0
	for _, i := range d.issues {
		if i.Error {
			errs++
		} else {
			warns++
		}
	}
	state := "válido"
	switch {
	case errs > 0:
		state = fmt.Sprintf("%d errores", errs)
	case warns > 0:
		state = fmt.Sprintf("válido, %d avisos", warns)
	}
	return fmt.Sprintf("%s · ~%d/%d tokens", state, d.doc.Tokens, d.doc.Limit())
}

func profileAndWaivers(root string, cfg *project.Config) (string, map[string]string) {
	profile := cfg.Type
	waivers := map[string]string{}
	if ds := loadDoc(root, ccfdoc.ReadmeFile); ds.doc != nil {
		if p := ds.doc.Profile(); p != "" {
			profile = p
		}
		for _, w := range ds.doc.Waivers() {
			reason := strings.TrimSpace(w.Reason)
			if !standards.Meaningful(reason) {
				reason = "" // un motivo de relleno cuenta como ninguno
			}
			if reason != "" && w.ADR != "" {
				reason += " (" + w.ADR + ")"
			}
			waivers[w.ID] = reason // vacío: Lint decide según el nivel de la regla
		}
	}
	return profile, waivers
}

func (a *app) lint(root string, cfg *project.Config, scripts bool) (*standards.Standard, *standards.Result, error) {
	st, err := standards.Load(root, a.now())
	if err != nil {
		return nil, nil, err
	}
	profile, waivers := profileAndWaivers(root, cfg)
	ctx, err := standards.NewContext(root, st, profile, waivers, cfg.Autonomy, a.now())
	if err != nil {
		return nil, nil, err
	}
	ctx.AllowScripts = scripts
	ctx.Secrets = cfg.Secrets
	return st, standards.Lint(ctx, st), nil
}

func checkAgentsMD(ctx *standards.Context, _ standards.Check) []standards.Finding {
	want, err := agentsmd.Generate(ctx.Root, ctx.Standard, ctx.Autonomy)
	if err != nil {
		return []standards.Finding{{Path: "AGENTS.md", Msg: err.Error()}}
	}
	got, err := fsx.ReadCapped(filepath.Join(ctx.Root, "AGENTS.md"), 4<<20)
	switch {
	case err != nil:
		return []standards.Finding{{Path: "AGENTS.md", Msg: "falta AGENTS.md"}}
	case !strings.HasPrefix(string(got), agentsmd.Marker):
		return []standards.Finding{{Path: "AGENTS.md", Msg: "AGENTS.md no lo generó coyote"}}
	case string(got) != want:
		return []standards.Finding{{Path: "AGENTS.md", Msg: "AGENTS.md desactualizado"}}
	}
	return nil
}

func table(w io.Writer) *tabwriter.Writer { return tabwriter.NewWriter(w, 0, 4, 2, ' ', 0) }

func rel(root, p string) string {
	if r, err := filepath.Rel(root, p); err == nil {
		return filepath.ToSlash(r)
	}
	return p
}

func shortText(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func cmdHelp(a *app, args []string) error {
	if len(args) > 0 {
		for _, c := range commands {
			if c.name == args[0] && c.name != "help" {
				return c.run(a, []string{"-h"})
			}
		}
		return fail(2, "comando desconocido %q", args[0])
	}
	fmt.Fprintln(a.stdout, "coyote — contexto versionado, gates humanos y costo trazable para agentes de IA")
	fmt.Fprintln(a.stdout, "\nuso: coyote [-C ruta] <comando> [opciones]\n\nComandos:")
	tw := table(a.stdout)
	for _, c := range commands {
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", c.name, c.args, c.summary)
	}
	tw.Flush()
	fmt.Fprintln(a.stdout, "\nMás ayuda: coyote help <comando>. Especificaciones en docs/specs de la herramienta.")
	return nil
}
