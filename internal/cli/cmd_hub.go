package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Emmanuel93/coyote/internal/hub"
	"github.com/Emmanuel93/coyote/internal/identity"
	"github.com/Emmanuel93/coyote/internal/project"
	"github.com/Emmanuel93/coyote/internal/standards"
)

func cmdHub(a *app, args []string) error {
	if len(args) == 0 {
		return fail(2, "uso: coyote hub init [carpeta] [--org NOMBRE] | status [--json]")
	}
	switch args[0] {
	case "init":
		return a.hubInit(args[1:])
	case "status":
		return a.hubStatus(args[1:])
	}
	return fail(2, "coyote hub: subcomando desconocido %q (init, status)", args[0])
}

func (a *app) hubInit(args []string) error {
	fs := a.flags("hub init", "[carpeta] [--org NOMBRE]")
	org := fs.String("org", "", "nombre de la organización; por defecto, el de la carpeta")
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
	res, err := project.Init(project.InitOptions{Dir: dir, Type: "hub", Org: *org,
		Purpose: "Hub de la organización: estándar, dominios, admins, presupuesto y proyectos (ADR-0018)",
		User:    identity.Resolve(wd), Now: a.now()})
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "Hub %s listo en %s\n", res.Name, res.Root)
	tw := table(a.stdout)
	for _, act := range res.Actions {
		fmt.Fprintf(tw, "  %s\t%s\n", act.Status, act.Path)
	}
	tw.Flush()
	for _, w := range res.Warnings {
		fmt.Fprintln(a.stdout, "  aviso: "+w)
	}
	fmt.Fprintln(a.stdout, "Revisa coyote/hub.yaml y haz commit: el hub rige desde su commit en main, nunca desde el árbol de trabajo.")
	fmt.Fprintln(a.stdout, "En cada proyecto: hub: { path: <ruta del clon>, ref: main } en coyote/project.yaml y extends: hub en coyote/standards/rules.yaml.")
	return nil
}

// hubProject es lo que hub status dice de un proyecto de la organización.
type hubProject struct {
	Name   string `json:"name"`
	Dir    string `json:"dir,omitempty"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

// openHub abre el hub del proyecto; en el propio hub, su rama main.
func (a *app) openHub(root string, cfg *project.Config) (*hub.Hub, error) {
	if cfg.Type == "hub" {
		return hub.Open(root, hub.Ref{Path: root})
	}
	return hub.Open(root, cfg.Hub)
}

func (a *app) hubStatus(args []string) error {
	fs := a.flags("hub status", "[--json]")
	asJSON := fs.Bool("json", false, "salida en JSON")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	root, cfg, err := a.project()
	if err != nil {
		return err
	}
	h, err := a.openHub(root, cfg)
	if err != nil {
		return err
	}
	if h == nil {
		return fail(1, "este proyecto no declara hub; agrega hub: { path: <clon>, ref: main } en coyote/project.yaml")
	}
	projects := make([]hubProject, 0, len(h.Conf.Projects))
	for _, p := range h.Conf.Projects {
		projects = append(projects, checkHubProject(h, p, a))
	}
	if *asJSON {
		enc := json.NewEncoder(a.stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"org": h.Conf.Org, "dir": h.Dir, "ref": h.Ref, "commit": h.Commit,
			"missing_conf": h.Missing, "admins": h.Conf.Admins, "monthly_usd": h.Conf.Budgets.MonthlyUSD, "projects": projects})
	}
	fmt.Fprintf(a.stdout, "Hub %s · %s\n", h, h.Dir)
	if h.Missing {
		fmt.Fprintf(a.stdout, "  aviso: el commit %s no tiene %s: sin admins, presupuesto ni proyectos\n", h.Short(), hub.ConfFile)
	}
	tw := table(a.stdout)
	admins := "ninguno: cada persona ve solo lo suyo en la web (D10)"
	if len(h.Conf.Admins) > 0 {
		admins = strings.Join(h.Conf.Admins, ", ")
	}
	fmt.Fprintf(tw, "  admins\t%s\n", admins)
	budget := "sin tope"
	if b := h.Conf.Budgets.MonthlyUSD; b > 0 {
		budget = fmt.Sprintf("$%.2f al mes para la organización", b)
	}
	fmt.Fprintf(tw, "  presupuesto\t%s\n", budget)
	fmt.Fprintf(tw, "  proyectos\t%d\n", len(projects))
	for _, p := range projects {
		fmt.Fprintf(tw, "    %s\t%s\t%s\n", p.Name, p.State, p.Detail)
	}
	return tw.Flush()
}

// checkHubProject revisa si un proyecto de la organización tiene clon y si
// sigue a este hub en el mismo commit.
func checkHubProject(h *hub.Hub, p hub.Project, a *app) hubProject {
	out := hubProject{Name: p.Name, Dir: h.ProjectDir(p)}
	if out.Dir == "" {
		out.State, out.Detail = "sin ruta", "declara path en coyote/hub.yaml para verlo"
		return out
	}
	if _, err := os.Stat(filepath.Join(out.Dir, project.ConfigPath)); err != nil {
		out.State, out.Detail = "sin clon", "no hay proyecto coyote en "+out.Dir
		return out
	}
	cfg, err := project.Load(out.Dir)
	if err != nil {
		out.State, out.Detail = "error", err.Error()
		return out
	}
	if cfg.Hub.Empty() {
		out.State, out.Detail = "sin hub", "su coyote/project.yaml no declara hub"
		return out
	}
	theirs, err := hub.Open(out.Dir, cfg.Hub)
	if err != nil {
		out.State, out.Detail = "error", err.Error()
		return out
	}
	if !sameDir(theirs.Dir, h.Dir) {
		out.State, out.Detail = "otro hub", theirs.Dir
		return out
	}
	st, err := standards.Load(out.Dir, a.now())
	switch {
	case err != nil:
		out.State, out.Detail = "error", err.Error()
	case st.Hub == nil:
		out.State, out.Detail = "sin capa", "su estándar no extiende el hub (extends: hub)"
	case theirs.Commit != h.Commit:
		out.State, out.Detail = "otro commit", fmt.Sprintf("rige %s@%s", theirs.Ref, theirs.Short())
	default:
		out.State, out.Detail = "sigue el hub", fmt.Sprintf("%s@%s", theirs.Ref, theirs.Short())
	}
	return out
}

func sameDir(a, b string) bool {
	x, err1 := filepath.EvalSymlinks(a)
	y, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && x == y
}
