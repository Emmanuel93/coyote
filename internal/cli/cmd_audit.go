package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Emmanuel93/coyote/internal/attribution"
	"github.com/Emmanuel93/coyote/internal/audit"
	"github.com/Emmanuel93/coyote/internal/fsx"
	"github.com/Emmanuel93/coyote/internal/gitx"
	"github.com/Emmanuel93/coyote/internal/ledger"
	"github.com/Emmanuel93/coyote/internal/product"
)

// cmdAudit arma el reporte de auditoría de un mes (ADR-0024).
func cmdAudit(a *app, args []string) error {
	fs := a.flags("audit", "[--month AAAA-MM] [--json] [--write]")
	month := fs.String("month", "", "mes a auditar, AAAA-MM; por defecto, el actual (UTC)")
	asJSON := fs.Bool("json", false, "salida en JSON")
	write := fs.Bool("write", false, "deja el reporte en coyote/audit/AAAA-MM.md para revisarlo y hacerle commit")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	root, cfg, err := a.project()
	if err != nil {
		return err
	}
	now := a.now()
	from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	if *month != "" {
		t, err := time.Parse("2006-01", *month)
		if err != nil {
			return fail(2, "--month %q: usa AAAA-MM", *month)
		}
		from = t
	}
	entries, probs, err := ledger.Open(root).ReadAll()
	if err != nil {
		return err
	}
	approvals, err := audit.LoadApprovals(root)
	if err != nil {
		return err
	}
	attr, err := attribution.Default()
	if err != nil {
		return err
	}
	in := audit.Input{Project: cfg.Name, Root: root, From: from, To: from.AddDate(0, 1, 0), Now: now,
		Entries: entries, Problems: probs, Approvals: approvals, MonthlyUSD: cfg.Budgets.MonthlyUSD, Attribution: attr}
	if gitx.IsRepo(root) && gitx.HasCommits(root) {
		in.Git = func(args ...string) (string, error) { return product.Git(root, args...) }
	}
	r := audit.Build(in)
	if *asJSON {
		enc := json.NewEncoder(a.stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(r); err != nil {
			return err
		}
	} else if *write {
		rel := "coyote/audit/" + from.Format("2006-01") + ".md"
		if err := fsx.NoSymlinks(root, rel); err != nil {
			return fail(1, "%v", err)
		}
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(r.Markdown()), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "reporte en %s: revísalo y hazle commit\n", rel)
	} else {
		fmt.Fprint(a.stdout, r.Markdown())
	}
	if r.Failed() {
		return fail(1, "coyote audit: hay verificaciones con falla; el detalle está en la sección Integridad")
	}
	return nil
}
