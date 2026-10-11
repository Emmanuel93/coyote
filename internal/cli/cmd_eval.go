package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	// IDs son las preguntas medidas: quitar una del golden para esconder una
	// regresión hace fallar a eval.
	IDs     []string `json:"ids,omitempty"`
	Updated string   `json:"updated"`
}

// evalPath resuelve una ruta de eval: el golden y la línea base son
// archivos del repo en evals/, nunca de afuera ni de otra carpeta (una línea
// base escrita en .claude/ reemplazaría la configuración del IDE).
func evalPath(root, p, flag, ext string) (string, error) {
	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, abs)
	}
	rel, err := filepath.Rel(root, filepath.Clean(abs))
	rel = filepath.ToSlash(rel)
	if err != nil || !strings.HasPrefix(rel, "evals/") || !strings.HasSuffix(rel, ext) || strings.Contains(rel, "/../") {
		return "", fail(2, "--%s tiene que ser un archivo %s dentro de evals/ del proyecto", flag, ext)
	}
	return rel, nil
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
	grel, err := evalPath(root, *goldenPath, "golden", ".jsonl")
	if err != nil {
		return err
	}
	brel, err := evalPath(root, *basePath, "baseline", ".json")
	if err != nil {
		return err
	}
	gdata, err := fsx.ReadFile(root, grel, golden.MaxFile)
	if errors.Is(err, os.ErrNotExist) {
		return fail(1, "no hay golden en %s: escribe preguntas con sus fuentes (docs/specs/eval-v1.md)", grel)
	}
	if err != nil {
		return fail(1, "%v", err)
	}
	qs, err := golden.Parse(grel, gdata)
	if err != nil {
		return fail(1, "%v", err)
	}
	ix, err := index.Build(root)
	if err != nil {
		return err
	}
	// Sin los eventos del ledger: crecen con cada commit y moverían la medida
	// sin que cambie el contexto (ADR-0025).
	ix = ix.Without(index.KindEvent)
	results, sum := golden.Run(ix, cfg.Name, qs, *budget)
	var base *evalBaseline
	switch data, err := fsx.ReadFile(root, brel, golden.MaxFile); {
	case err == nil:
		base = &evalBaseline{}
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(base); err != nil {
			return fail(1, "%s: %v", brel, err)
		}
	case !errors.Is(err, os.ErrNotExist):
		return fail(1, "%v", err)
	}
	var regressions []string
	if base != nil && !*update {
		if base.Budget != *budget {
			return fail(2, "la línea base se midió con --budget %d; mide igual o actualízala", base.Budget)
		}
		regressions = golden.Regression(base.Summary, sum, evalTolerance)
		have := map[string]bool{}
		for _, q := range qs {
			have[q.ID] = true
		}
		var gone []string
		for _, id := range base.IDs {
			if !have[id] {
				gone = append(gone, id)
			}
		}
		switch {
		case len(gone) > 0:
			regressions = append(regressions, fmt.Sprintf("faltan %d preguntas de la línea base (%s): quitarlas se revisa en el PR con --update-baseline", len(gone), strings.Join(gone, ", ")))
		case len(base.IDs) == 0 && sum.Questions < base.Questions:
			regressions = append(regressions, fmt.Sprintf("el golden tiene %d preguntas y la línea base, %d", sum.Questions, base.Questions))
		}
	}
	if *update {
		if err := fsx.NoSymlinks(root, brel); err != nil {
			return fail(1, "%v", err)
		}
		ids := make([]string, 0, len(qs))
		for _, q := range qs {
			ids = append(ids, q.ID)
		}
		nb := evalBaseline{Golden: grel, Budget: *budget, Summary: sum, IDs: ids, Updated: a.now().Format("2006-01-02")}
		data, _ := json.MarshalIndent(nb, "", "  ")
		bp := filepath.Join(root, filepath.FromSlash(brel))
		if err := os.MkdirAll(filepath.Dir(bp), 0o755); err != nil {
			return err
		}
		if err := fsx.WriteAtomic(bp, append(data, '\n'), 0o644); err != nil {
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
		fmt.Fprintf(a.stdout, "golden %s: %d preguntas · paquete de %d tokens · sin modelos\n", grel, sum.Questions, *budget)
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
			fmt.Fprintf(a.stdout, "línea base escrita en %s: revísala y hazle commit\n", brel)
		case base == nil:
			fmt.Fprintf(a.stdout, "sin línea base: corre coyote eval --update-baseline y hazle commit a %s\n", brel)
		case len(regressions) == 0:
			fmt.Fprintf(a.stdout, "✓ sin regresiones frente a %s (tolerancia de %.0f puntos)\n", brel, evalTolerance)
		default:
			for _, r := range regressions {
				fmt.Fprintf(a.stdout, "✗ %s\n", r)
			}
		}
	}
	if len(regressions) > 0 {
		return fail(1, "coyote eval: el contexto empeoró frente a la línea base (ADR-0025)")
	}
	if base == nil && !*update {
		return fail(1, "coyote eval: sin línea base no hay contra qué medir")
	}
	return nil
}

func rank(rr float64) string {
	if rr == 0 {
		return "fuera de los 10"
	}
	return fmt.Sprintf("%.0f", 1/rr)
}
