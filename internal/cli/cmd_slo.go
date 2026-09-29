package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/Emmanuel93/coyote/internal/fsx"
	"github.com/Emmanuel93/coyote/internal/gitx"
	"github.com/Emmanuel93/coyote/internal/project"
	"github.com/Emmanuel93/coyote/internal/slo"
	"github.com/Emmanuel93/coyote/internal/web"
)

func init() {
	sloViews = func(root string) ([]web.SLO, error) {
		files, err := slo.LoadAll(root)
		if err != nil {
			return nil, err
		}
		var out []web.SLO
		for _, f := range files {
			if f.Spec == nil {
				out = append(out, web.SLO{Service: f.Path, Name: "—", Problem: shortText(f.Err.Error(), 160)})
				continue
			}
			stale, serr := slo.Stale(root, f.Spec)
			for _, s := range slo.Summaries(f.Spec) {
				v := web.SLO{Service: s.Service, Name: s.Name, Objective: s.Objective, PeriodDays: s.PeriodDays,
					Page: s.Page, Ticket: s.Ticket, Runbook: s.Runbook, Current: !stale && serr == nil}
				if serr != nil {
					v.Problem = shortText(serr.Error(), 160)
				}
				out = append(out, v)
			}
		}
		return out, nil
	}
}

func cmdSLO(a *app, args []string) error {
	if len(args) == 0 {
		return fail(2, "uso: coyote slo check [--json] | rules [servicio] [--check] [--stdout]")
	}
	switch args[0] {
	case "check":
		return a.sloCheck(args[1:])
	case "rules":
		return a.sloRules(args[1:])
	}
	return fail(2, "coyote slo: subcomando desconocido %q (check, rules)", args[0])
}

func (a *app) sloCheck(args []string) error {
	fs := a.flags("slo check", "[--json]")
	asJSON := fs.Bool("json", false, "salida en JSON")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	root, err := a.sloRoot()
	if err != nil {
		return err
	}
	files, err := slo.LoadAll(root)
	if err != nil {
		return err
	}
	problems := slo.Check(root, files)
	if *asJSON {
		type sum struct {
			File     string        `json:"file"`
			Service  string        `json:"service,omitempty"`
			SLOs     []slo.Summary `json:"slos,omitempty"`
			Problems []string      `json:"problems,omitempty"`
		}
		var out []sum
		for _, f := range files {
			s := sum{File: f.Path}
			if f.Spec != nil {
				s.Service, s.SLOs = f.Spec.Service, slo.Summaries(f.Spec)
			}
			out = append(out, s)
		}
		var ps []string
		for _, p := range problems {
			ps = append(ps, p.Path+": "+p.Msg)
		}
		enc := json.NewEncoder(a.stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(map[string]any{"files": out, "problems": ps}); err != nil {
			return err
		}
	} else {
		if len(files) == 0 {
			fmt.Fprintf(a.stdout, "Sin SLOs: declara %s/<servicio>.yaml (docs/specs/slo-v1.md).\n", slo.Dir)
		}
		tw := table(a.stdout)
		for _, f := range files {
			if f.Spec == nil {
				continue
			}
			for _, s := range slo.Summaries(f.Spec) {
				alerts := "sin alertas"
				switch {
				case s.Page && s.Ticket:
					alerts = "page y ticket"
				case s.Page:
					alerts = "page"
				case s.Ticket:
					alerts = "ticket"
				}
				fmt.Fprintf(tw, "  %s\t%s\t%s %%\t%d días\tpresupuesto %s\t%s\n", s.Service, s.Name, trimNum(s.Objective), s.PeriodDays, errorBudgetText(s.ErrorBudgetMinutes), alerts)
			}
		}
		tw.Flush()
		for _, p := range problems {
			fmt.Fprintf(a.stdout, "✗ %s: %s\n", p.Path, p.Msg)
		}
	}
	if len(problems) > 0 {
		return fail(1, "")
	}
	if !*asJSON && len(files) > 0 {
		fmt.Fprintln(a.stdout, "SLOs válidos y reglas vigentes.")
	}
	return nil
}

func trimNum(v float64) string {
	s := fmt.Sprintf("%.4f", v)
	for s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}
	if s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}
	return s
}

func errorBudgetText(min float64) string {
	switch {
	case min >= 120:
		return fmt.Sprintf("%.1f h", min/60)
	case min >= 1:
		return fmt.Sprintf("%.0f min", min)
	}
	return fmt.Sprintf("%.0f s", min*60)
}

func (a *app) sloRules(args []string) error {
	fs := a.flags("slo rules", "[servicio] [--check] [--stdout]")
	check := fs.Bool("check", false, "falla si las reglas no están al día, sin escribir (para CI)")
	stdout := fs.Bool("stdout", false, "imprime las reglas de un servicio en vez de escribirlas")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	root, err := a.sloRoot()
	if err != nil {
		return err
	}
	files, err := slo.LoadAll(root)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fail(1, "sin SLOs: declara %s/<servicio>.yaml", slo.Dir)
	}
	want := ""
	if len(pos) > 0 {
		want = pos[0]
	}
	if *stdout && want == "" && len(files) > 1 {
		return fail(2, "--stdout imprime un servicio: coyote slo rules <servicio> --stdout")
	}
	found, stale, bad := false, 0, 0
	for _, f := range files {
		if f.Spec == nil {
			if want == "" {
				fmt.Fprintf(a.stderr, "✗ %s: %v\n", f.Path, f.Err)
				bad++
			}
			continue
		}
		if want != "" && f.Spec.Service != want {
			continue
		}
		found = true
		out, err := slo.Rules(f.Spec)
		if err != nil {
			return err
		}
		if *stdout {
			_, err := a.stdout.Write(out)
			return err
		}
		rel := slo.RulesPath(f.Spec.Service)
		cur, rerr := fsx.ReadFile(root, rel, 4*slo.MaxFile)
		status := "vigente"
		switch {
		case rerr != nil:
			status = "creado"
		case !bytes.Equal(cur, out):
			status = "actualizado"
		}
		if status != "vigente" {
			stale++
			if *check {
				status = "no está al día"
			} else {
				if err := fsx.NoSymlinks(root, rel); err != nil {
					return err
				}
				if err := fsx.WriteAtomic(filepath.Join(root, filepath.FromSlash(rel)), out, 0o644); err != nil {
					return err
				}
			}
		}
		fmt.Fprintf(a.stdout, "  %-12s %s\n", status, rel)
	}
	if want != "" && !found {
		return fail(1, "no hay SLOs válidos del servicio %s en %s", want, slo.Dir)
	}
	switch {
	case bad > 0:
		return fail(1, "%d %s de SLOs no validan", bad, pluralWord(bad, "archivo", "archivos"))
	case *check && stale > 0:
		return fail(1, "las reglas no están al día; corre coyote slo rules")
	}
	return nil
}

// sloRoot es la raíz del proyecto coyote o, en un repo del producto que no
// lo es, la raíz del repo git: los SLOs viven junto al servicio.
func (a *app) sloRoot() (string, error) {
	root, _, err := a.project()
	if err == nil {
		return root, nil
	}
	if !errors.Is(err, project.ErrNotProject) {
		return "", err
	}
	wd, werr := a.workdir()
	if werr != nil {
		return "", werr
	}
	if top, gerr := gitx.TopLevel(wd); gerr == nil && top != "" {
		return top, nil
	}
	return "", err
}
