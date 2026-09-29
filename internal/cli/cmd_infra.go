package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Emmanuel93/coyote/internal/fsx"
	"github.com/Emmanuel93/coyote/internal/infra"
	"github.com/Emmanuel93/coyote/internal/product"
)

// coyote infra (ADR-0017): el inventario de la infraestructura (R13), su
// revisión contra el repo y la lectura del plan de Terraform. Todo es
// lectura: coyote nunca corre Terraform ni habla con una nube.

const infraUsage = "check [--file inventario] [--json] | plan <plan.json> [--format text|md|json] | propose"

func cmdInfra(a *app, args []string) error {
	if len(args) == 0 {
		return fail(2, "uso: coyote infra "+infraUsage)
	}
	switch args[0] {
	case "check":
		return infraCheck(a, args[1:])
	case "plan":
		return infraPlan(a, args[1:])
	case "propose":
		return infraPropose(a, args[1:])
	}
	return fail(2, "uso: coyote infra "+infraUsage)
}

func infraCheck(a *app, args []string) error {
	fl := a.flags("infra check", "[--file inventario] [--json]")
	file := fl.String("file", "", "inventario a revisar (por defecto, coyote/infra.yaml del repo)")
	asJSON := fl.Bool("json", false, "salida en JSON")
	if _, err := parseArgs(fl, args); err != nil {
		return err
	}
	dir, err := a.workdir()
	if err != nil {
		return err
	}
	var inv *infra.Inventory
	if *file == "" {
		var ok bool
		inv, ok, err = infra.Load(dir)
		if !ok {
			return fail(1, "falta %s (R13): propónlo con coyote infra propose", infra.Path)
		}
	} else {
		var data []byte
		if data, err = fsx.ReadCapped(*file, infra.MaxInventory); err != nil {
			return fail(1, "%v", err)
		}
		inv, err = infra.Parse(data)
	}
	if err != nil {
		return fail(1, "%v", err)
	}
	tracked, _ := product.TrackedFiles(dir)
	found := infra.Check(dir, inv, tracked)
	errs := 0
	for _, f := range found {
		if f.Level == infra.Error {
			errs++
		}
	}
	if *asJSON {
		if found == nil {
			found = []infra.Finding{}
		}
		b, _ := json.MarshalIndent(found, "", "  ")
		fmt.Fprintln(a.stdout, string(b))
	} else {
		active, scaffold := 0, 0
		for _, s := range inv.Stacks {
			if s.Status == "active" {
				active++
			} else {
				scaffold++
			}
		}
		fmt.Fprintf(a.stdout, "Inventario: %s · %d ambientes (%s) · %d stacks activos y %d de andamiaje\n",
			inv.Tool, len(inv.Environments), strings.Join(inv.EnvNames(), ", "), active, scaffold)
		for _, n := range inv.EnvNames() {
			e := inv.Environments[n]
			budget := "sin presupuesto"
			if e.Budget != nil && e.Budget.CapUSD > 0 {
				budget = fmt.Sprintf("meta $%g, tope $%g al mes", e.Budget.TargetUSD, e.Budget.CapUSD)
			}
			fmt.Fprintf(a.stdout, "  %s: apply %s · %s\n", n, e.Apply, budget)
		}
		if len(found) == 0 {
			fmt.Fprintln(a.stdout, "\nEl inventario coincide con el repo.")
		} else {
			fmt.Fprintln(a.stdout)
			w := table(a.stdout)
			for _, f := range found {
				mark := "!"
				if f.Level == infra.Error {
					mark = "✗"
				}
				fmt.Fprintf(w, "  %s %s\t%s\n", mark, f.Where, f.Msg)
			}
			w.Flush()
			fmt.Fprintf(a.stdout, "%d %s · %d %s\n", errs, pluralWord(errs, "error", "errores"), len(found)-errs, pluralWord(len(found)-errs, "aviso", "avisos"))
		}
	}
	if errs > 0 {
		return fail(1, "")
	}
	return nil
}

func infraPlan(a *app, args []string) error {
	fl := a.flags("infra plan", "<plan.json> [--format text|md|json]")
	format := fl.String("format", "text", "text, md (para un PR) o json")
	pos, err := parseArgs(fl, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fail(2, "uso: coyote infra plan <plan.json>: la salida de terraform show -json plan.tfplan (- para la entrada estándar)")
	}
	var r io.Reader = a.stdin
	if pos[0] != "-" {
		f, err := os.Open(pos[0])
		if err != nil {
			return err
		}
		defer f.Close()
		r = f
	}
	s, err := infra.ReadPlan(r)
	if err != nil {
		return fail(1, "%v", err)
	}
	switch *format {
	case "json":
		b, _ := json.MarshalIndent(s, "", "  ")
		fmt.Fprintln(a.stdout, string(b))
	case "md":
		fmt.Fprint(a.stdout, planMarkdown(s))
	case "text":
		fmt.Fprintf(a.stdout, "Plan: %s · riesgo %s\n", s.Headline(), s.Risk)
		if reasons := s.Reasons(); len(reasons) > 0 {
			fmt.Fprintf(a.stdout, "R3 por %s\n", strings.Join(reasons, ", "))
		}
		if len(s.Changes) > 0 {
			fmt.Fprintln(a.stdout)
			w := table(a.stdout)
			for _, c := range s.Changes {
				fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n", c.Risk, c.Action, c.Address, c.Why)
			}
			w.Flush()
		}
		if len(s.Costly) > 0 {
			fmt.Fprintf(a.stdout, "\nCambia recursos que mueven el costo (%s): compáralo con el presupuesto del ambiente en coyote/infra.yaml.\n", strings.Join(s.Costly, ", "))
		}
	default:
		return fail(2, "--format %q: usa text, md o json", *format)
	}
	return nil
}

// planMarkdown es el resumen del plan para un comentario de PR: direcciones
// y tipos, nunca valores.
func planMarkdown(s *infra.PlanSummary) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**Plan de Terraform (%s).** %s.", s.Risk, capFirst(s.Headline()))
	if reasons := s.Reasons(); len(reasons) > 0 {
		fmt.Fprintf(&b, " R3 por %s.", strings.Join(reasons, ", "))
	}
	b.WriteString("\n\n")
	if len(s.Changes) > 0 {
		b.WriteString("| Riesgo | Acción | Recurso | Por qué |\n|---|---|---|---|\n")
		for i, c := range s.Changes {
			if i == ciMaxRows {
				fmt.Fprintf(&b, "| … | | %d recursos más | |\n", len(s.Changes)-ciMaxRows)
				break
			}
			fmt.Fprintf(&b, "| %s | %s | `%s` | %s |\n", c.Risk, c.Action, codeCell(c.Address), mdCell(c.Why))
		}
		b.WriteString("\n")
	}
	if len(s.Costly) > 0 {
		fmt.Fprintf(&b, "Cambia recursos que mueven el costo: %s.\n\n", mdCell(strings.Join(s.Costly, ", ")))
	}
	return b.String()
}

func infraPropose(a *app, args []string) error {
	fl := a.flags("infra propose", "")
	if _, err := parseArgs(fl, args); err != nil {
		return err
	}
	dir, err := a.workdir()
	if err != nil {
		return err
	}
	inv, err := infra.Propose(dir)
	if err != nil {
		return fail(1, "%v", err)
	}
	fmt.Fprint(a.stdout, inv.YAML(filepath.Base(dir)))
	return nil
}
