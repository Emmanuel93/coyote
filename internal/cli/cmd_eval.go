package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Emmanuel93/coyote/internal/fsx"
	"github.com/Emmanuel93/coyote/internal/golden"
	"github.com/Emmanuel93/coyote/internal/index"
)

// evalTolerance son los puntos que una métrica puede bajar antes de fallar (ADR-0025).
const evalTolerance = 2.0

type evalBaseline struct {
	Golden string `json:"golden"`
	Budget int    `json:"budget"`
	golden.Summary
	Updated string `json:"updated"`
}

// cmdEval mide el contexto con el golden del proyecto, sin modelos.
func cmdEval(a *app, args []string) error {
	fs := a.flags("eval", "[--golden evals/golden.v1.jsonl] [--baseline evals/baseline.json] [--update-baseline] [--budget N] [--json] [-v]")
	goldenPath := fs.String("golden", "evals/golden.v1.jsonl", "preguntas con sus fuentes esperadas (JSONL)")
	basePath := fs.String("baseline", "evals/baseline.json", "línea base versionada")
	update := fs.Bool("update-baseline", false, "reescribe la línea base con la medida actual; el cambio se revisa en el PR")
	budget := fs.Int("budget", 2000, "presupuesto del paquete de contexto, como en get context")
	asJSON := fs.Bool("json", false, "salida en JSON")
	verbose := fs.Bool("v", false, "muestra cada pregunta que no salió completa")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	root, cfg, err := a.project()
	if err != nil {
		return err
	}
	gp := *goldenPath
	if !filepath.IsAbs(gp) {
		gp = filepath.Join(root, gp)
	}
	qs, err := golden.Load(gp)
	if errors.Is(err, os.ErrNotExist) {
		return fail(1, "no hay golden en %s: escribe preguntas con sus fuentes (docs/specs/eval-v1.md)", *goldenPath)
	}
	if err != nil {
		return fail(1, "%v", err)
	}
	ix, err := index.Build(root)
	if err != nil {
		return err
	}
	results, sum := golden.Run(ix, cfg.Name, qs, *budget)
	bp := *basePath
	if !filepath.IsAbs(bp) {
		bp = filepath.Join(root, bp)
	}
	var base *evalBaseline
	if data, err := os.ReadFile(bp); err == nil {
		base = &evalBaseline{}
		if err := json.Unmarshal(data, base); err != nil {
			return fail(1, "%s: %v", *basePath, err)
		}
	}
	var regressions []string
	if base != nil && !*update {
		if base.Budget != *budget {
			return fail(2, "la línea base se midió con --budget %d; mide igual o actualízala", base.Budget)
		}
		regressions = golden.Regression(base.Summary, sum, evalTolerance)
	}
	if *update {
		if rel, err := filepath.Rel(root, bp); err == nil {
			if err := fsx.NoSymlinks(root, filepath.ToSlash(rel)); err != nil {
				return fail(1, "%v", err)
			}
		}
		nb := evalBaseline{Golden: filepath.ToSlash(*goldenPath), Budget: *budget, Summary: sum, Updated: a.now().Format("2006-01-02")}
		data, _ := json.MarshalIndent(nb, "", "  ")
		if err := os.MkdirAll(filepath.Dir(bp), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(bp, append(data, '\n'), 0o644); err != nil {
			return err
		}
	}
	if *asJSON {
		enc := json.NewEncoder(a.stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(map[string]any{"summary": sum, "baseline": base, "regressions": regressions, "results": results}); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(a.stdout, "golden %s: %d preguntas · paquete de %d tokens · sin modelos\n", *goldenPath, sum.Questions, *budget)
		line := func(name string, v float64, b float64, pct bool) {
			unit := ""
			if pct {
				unit = " %"
			}
			if base != nil && !*update {
				fmt.Fprintf(a.stdout, "  %-22s %5.1f%s  (base %.1f)\n", name, v, unit, b)
			} else {
				fmt.Fprintf(a.stdout, "  %-22s %5.1f%s\n", name, v, unit)
			}
		}
		var b golden.Summary
		if base != nil {
			b = base.Summary
		}
		line("recall@5", sum.Recall, b.Recall, true)
		line("MRR", sum.MRR, b.MRR, false)
		line("cobertura del paquete", sum.Coverage, b.Coverage, true)
		if *verbose {
			for _, r := range results {
				if len(r.Missing) > 0 || r.Coverage < 1 {
					fmt.Fprintf(a.stdout, "  %s: recall %.2f, puesto %s, paquete %.2f; faltan %v\n", r.ID, r.Recall, rank(r.RR), r.Coverage, r.Missing)
				}
			}
		}
		switch {
		case *update:
			fmt.Fprintf(a.stdout, "línea base escrita en %s: revísala y hazle commit\n", *basePath)
		case base == nil:
			fmt.Fprintf(a.stdout, "sin línea base: corre coyote eval --update-baseline y hazle commit a %s\n", *basePath)
		case len(regressions) == 0:
			fmt.Fprintf(a.stdout, "✓ sin regresiones frente a %s (tolerancia de %.0f puntos)\n", *basePath, evalTolerance)
		default:
			for _, r := range regressions {
				fmt.Fprintf(a.stdout, "✗ %s\n", r)
			}
		}
	}
	if len(regressions) > 0 {
		return fail(1, "coyote eval: el contexto empeoró frente a la línea base (ADR-0025)")
	}
	return nil
}

func rank(rr float64) string {
	if rr == 0 {
		return "fuera de los 10"
	}
	return fmt.Sprintf("%.0f", 1/rr)
}
