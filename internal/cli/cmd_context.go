package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/Emmanuel93/coyote/internal/ccf"
	"github.com/Emmanuel93/coyote/internal/fsx"
	"github.com/Emmanuel93/coyote/internal/identity"
	"github.com/Emmanuel93/coyote/internal/index"
	"github.com/Emmanuel93/coyote/internal/ledger"
	"github.com/Emmanuel93/coyote/internal/project"
	"github.com/Emmanuel93/coyote/internal/tokens"
)

// cmdGet agrupa las lecturas de contexto: coyote get context [repo].
func cmdGet(a *app, args []string) error {
	if len(args) == 0 || args[0] != "context" {
		return fail(2, "uso: coyote get context [repo] [--scope S] [--query Q] [--budget N] [--format md|ccf]")
	}
	fs := a.flags("get context", "[repo] [--scope S] [--query Q] [--budget N] [--format md|ccf]")
	scope := fs.String("scope", "", "ámbito: módulo o tema, p. ej. pagos")
	query := fs.String("query", "", "consulta libre para ordenar lo relevante")
	budget := fs.Int("budget", 2000, "tope de tokens del paquete")
	format := fs.String("format", "md", "md para personas y agentes, ccf compacto")
	events := fs.Int("events", 8, "eventos recientes del ledger")
	pos, err := parseArgs(fs, args[1:])
	if err != nil {
		return err
	}
	if *budget < 100 {
		return fail(2, "--budget mínimo 100 tokens")
	}
	repo := ""
	if len(pos) > 0 {
		repo = pos[0]
	}
	root, name, err := a.contextRoot(repo)
	if err != nil {
		return err
	}
	ix, err := index.Build(root)
	if err != nil {
		return err
	}
	p := ix.Pack(name, index.PackOptions{Scope: *scope, Query: *query, Budget: *budget, Events: *events, Now: a.now()})
	switch *format {
	case "md":
		fmt.Fprint(a.stdout, p.Markdown())
	case "ccf":
		fmt.Fprint(a.stdout, p.CCF())
	default:
		return fail(2, "--format %q inválido; usa md o ccf", *format)
	}
	return nil
}

// contextRoot resuelve de qué proyecto se pide contexto: el actual, una ruta
// local a otro proyecto coyote o un repo registrado en coyote/project.yaml.
func (a *app) contextRoot(repo string) (string, string, error) {
	wd, err := a.workdir()
	if err != nil {
		return "", "", err
	}
	if repo == "" || repo == "." {
		root, cfg, err := a.project()
		if err != nil {
			return "", "", err
		}
		return root, cfg.Name, nil
	}
	if p := repo; strings.ContainsAny(p, "/\\") || strings.HasPrefix(p, ".") {
		if !filepath.IsAbs(p) {
			p = filepath.Join(wd, p)
		}
		if _, err := os.Stat(filepath.Join(p, filepath.FromSlash(project.ConfigPath))); err == nil {
			cfg, err := project.Load(p)
			if err != nil {
				return "", "", err
			}
			return p, cfg.Name, nil
		}
	}
	root, cfg, err := a.project()
	if err != nil {
		return "", "", err
	}
	if repo == cfg.Name {
		return root, cfg.Name, nil
	}
	for _, r := range cfg.Repos {
		if r.Name == repo {
			dir, err := a.repoDocs(root, r)
			return dir, r.Name, err
		}
	}
	return "", "", fail(1, "no conozco el repo %q: regístralo con coyote repo add %s <url>", repo, repo)
}

func cmdAsk(a *app, args []string) error {
	fs := a.flags("ask", "\"pregunta\" [--limit N] [--kind context,adr,doc,event] [--scope S] [--json] [--record]")
	limit := fs.Int("limit", 5, "máximo de resultados")
	kinds := fs.String("kind", "", "clases separadas por coma: readme, context, adr, doc, workstream, event")
	scope := fs.String("scope", "", "solo este ámbito")
	repo := fs.String("repo", "", "pregunta a otro repo del proyecto")
	asJSON := fs.Bool("json", false, "salida JSON")
	record := fs.Bool("record", false, "registra la pregunta en el ledger (tipo ask)")
	ws := fs.String("ws", "-", "workstream, con --record")
	agent := fs.String("agent", "", "agente que pregunta, con --record")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	question := strings.TrimSpace(strings.Join(pos, " "))
	if question == "" {
		fs.Usage()
		return fail(2, "")
	}
	root, name, err := a.contextRoot(*repo)
	if err != nil {
		return err
	}
	start := time.Now()
	ix, err := index.Build(root)
	if err != nil {
		return err
	}
	var ks []string
	for _, k := range strings.Split(*kinds, ",") {
		if k = strings.TrimSpace(k); k != "" {
			ks = append(ks, k)
		}
	}
	all := ix.Search(question, index.SearchOptions{Kinds: ks, Scope: *scope})
	hits := all
	if *limit > 0 && len(hits) > *limit {
		hits = hits[:*limit]
	}
	elapsed := time.Since(start)
	cost := 0
	for _, h := range hits {
		cost += h.Tokens
	}
	if *asJSON {
		type out struct {
			Kind, Type, Scope, Title, Text, Ref string
			Score                               float64
		}
		res := make([]out, len(hits))
		for i, h := range hits {
			res[i] = out{h.Kind, h.Type, h.Scope, h.Title, h.Text, h.Ref(), h.Score}
		}
		enc := json.NewEncoder(a.stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(map[string]any{"repo": name, "question": question, "results": res, "matches": len(all), "tokens": cost}); err != nil {
			return err
		}
	} else if len(hits) == 0 {
		fmt.Fprintln(a.stdout, "sin resultados; prueba con otras palabras o con coyote get context --scope <ámbito>")
	} else {
		top := hits[0].Score
		for i, h := range hits {
			label := h.Kind
			switch {
			case h.Kind == index.KindContext || h.Kind == index.KindReadme:
				label = fmt.Sprintf("[%s] %s", h.Type, orDash(h.Scope))
			case h.Kind == index.KindEvent:
				label = fmt.Sprintf("%s %s %s", h.Time.Format("2006-01-02"), h.Title, h.Type)
			case h.Title != "":
				label = h.Title
			}
			fmt.Fprintf(a.stdout, "%d. %s: %s\n   %s · %.0f%%\n", i+1, label, shortText(h.Text, 220), h.Ref(), 100*h.Score/top)
		}
		fmt.Fprintf(a.stdout, "~%d tokens · %d de %d coincidencias · índice de %s: %d fragmentos (%d archivos leídos, %d de caché) en %s\n",
			cost, len(hits), len(all), name, len(ix.Chunks), ix.Parsed, ix.Reused, elapsed.Round(time.Millisecond))
	}
	if *record {
		if err := a.recordAsk(question, hits, *ws, *agent); err != nil {
			return err
		}
	}
	return nil
}

// recordAsk deja la pregunta en el ledger con las referencias de sus mejores resultados.
func (a *app) recordAsk(question string, hits []index.Hit, ws, agent string) error {
	root, cfg, err := a.project()
	if err != nil {
		return err
	}
	if agent, err = a.agentFor(root, agent); err != nil {
		return err
	}
	person := identity.Resolve(root)
	var refs []string
	for i, h := range hits {
		if i == 3 {
			break
		}
		refs = append(refs, "doc:"+safeRef(h.Ref()))
	}
	line := ccf.Line{TS: a.now(), Actor: person.Actor(agent), Project: ledgerID(ws), Repo: cfg.Name, Type: "ask",
		Scope: "-", What: ccf.ShortWhat(question, ccf.MaxWhatWords), Refs: refs, Status: "ok"}
	_, err = ledger.Open(root).Append(line, person.Slug)
	return err
}

// safeRef deja una referencia sin espacios (de ningún tipo) ni separadores.
func safeRef(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.Is(unicode.Z, r) || r == '|' {
			return '_'
		}
		return r
	}, s)
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func cmdIndex(a *app, args []string) error {
	fs := a.flags("index", "[--rebuild] [--json]")
	rebuild := fs.Bool("rebuild", false, "descarta la caché y vuelve a leer todo")
	asJSON := fs.Bool("json", false, "salida JSON")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	root, cfg, err := a.project()
	if err != nil {
		return err
	}
	if *rebuild {
		if err := fsx.NoSymlinks(root, index.CachePath); err != nil {
			return fail(1, "%v", err)
		}
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(index.CachePath))); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	start := time.Now()
	ix, err := index.Build(root)
	if err != nil {
		return err
	}
	elapsed := time.Since(start)
	kinds := map[string]int{}
	total := 0
	files := map[string]bool{}
	for _, c := range ix.Chunks {
		kinds[c.Kind]++
		total += c.Tokens
		files[c.Path] = true
	}
	if *asJSON {
		enc := json.NewEncoder(a.stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"project": cfg.Name, "chunks": len(ix.Chunks), "files": len(files), "tokens": total,
			"parsed": ix.Parsed, "reused": ix.Reused, "by_kind": kinds, "ms": elapsed.Milliseconds()})
	}
	fmt.Fprintf(a.stdout, "índice de %s: %d fragmentos de %d archivos · ~%d tokens en total · %d leídos, %d de caché · %s\n",
		cfg.Name, len(ix.Chunks), len(files), total, ix.Parsed, ix.Reused, elapsed.Round(time.Millisecond))
	tw := table(a.stdout)
	for _, k := range []string{index.KindReadme, index.KindContext, index.KindADR, index.KindDoc, index.KindWorkstream, index.KindEvent} {
		if kinds[k] > 0 {
			fmt.Fprintf(tw, "  %s\t%d\n", k, kinds[k])
		}
	}
	tw.Flush()
	fmt.Fprintf(a.stdout, "Un paquete de contexto típico usa --budget 2000; leer todo costaría ~%d tokens.\n", total+tokens.Estimate(cfg.Name))
	return nil
}
