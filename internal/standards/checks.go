package standards

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Emmanuel93/coyote/internal/attribution"
	"github.com/Emmanuel93/coyote/internal/ccfdoc"
	"github.com/Emmanuel93/coyote/internal/fsx"
	"github.com/Emmanuel93/coyote/internal/gitx"
	"github.com/Emmanuel93/coyote/internal/glob"
	"github.com/Emmanuel93/coyote/internal/infra"
	"github.com/Emmanuel93/coyote/internal/secrets"
	"github.com/Emmanuel93/coyote/internal/tokens"
)

// DefaultCommitPattern es el formato de R2 si la regla no define otro.
const DefaultCommitPattern = `^(feat|fix|docs|refactor|test|chore|ci|build|perf|style|revert)(\([a-z0-9._/-]+\))?!?: \S.*$`

// Finding es un incumplimiento encontrado por el lint.
type Finding struct {
	RuleID      string `json:"rule"`
	Level       string `json:"level"`
	Title       string `json:"title"`
	Path        string `json:"path,omitempty"`
	Line        int    `json:"line,omitempty"`
	Msg         string `json:"message"`
	Fix         string `json:"fix,omitempty"`
	Waived      bool   `json:"waived,omitempty"`
	WaiveReason string `json:"waive_reason,omitempty"`
}

// Result es el resultado de un lint.
type Result struct {
	Findings []Finding `json:"findings"`
	Checked  int       `json:"rules_checked"`
	Skipped  []string  `json:"rules_skipped,omitempty"`
}

// Count cuenta hallazgos por nivel; waived elige dispensados o no.
func (r *Result) Count(level string, waived bool) int {
	n := 0
	for _, f := range r.Findings {
		if f.Waived == waived && (level == "" || f.Level == level) {
			n++
		}
	}
	return n
}

// ScriptsSkipped cuenta los checks script que no corrieron.
func (r *Result) ScriptsSkipped() int {
	n := 0
	for _, s := range r.Skipped {
		if strings.Contains(s, "script omitido") {
			n++
		}
	}
	return n
}

// Failing cuenta los hallazgos que hacen fallar el lint.
func (r *Result) Failing(strict bool) int {
	n := r.Count("MUST", false)
	if strict {
		n += r.Count("SHOULD", false)
	}
	return n
}

// Context es lo que un check necesita del proyecto.
type Context struct {
	Root        string
	Profile     string
	Files       []string
	Now         time.Time
	Attribution *attribution.Config
	Waivers     map[string]string
	Standard    *Standard
	Autonomy    string
	// AllowScripts habilita los checks script. Ejecutan comandos definidos en
	// rules.yaml (del proyecto o del hub), así que solo corren cuando la persona
	// lo pide con coyote standards lint --scripts, nunca desde status o doctor.
	AllowScripts bool
	// Secrets son los archivos de secretos propios del proyecto y sus
	// dispensas (coyote/project.yaml), para el check secrets (R18).
	Secrets secrets.Rules
}

// NewContext arma el contexto de lint para el proyecto en root.
func NewContext(root string, st *Standard, profile string, waivers map[string]string, autonomy string, now time.Time) (*Context, error) {
	files, err := ListFiles(root)
	if err != nil {
		return nil, err
	}
	attr, err := attribution.Default()
	if err != nil {
		return nil, err
	}
	if waivers == nil {
		waivers = map[string]string{}
	}
	return &Context{Root: root, Profile: profile, Files: files, Now: now, Attribution: attr,
		Waivers: waivers, Standard: st, Autonomy: autonomy}, nil
}

// CheckFunc implementa un tipo de check.
type CheckFunc func(ctx *Context, c Check) []Finding

var registry = map[string]CheckFunc{}

// Register agrega un tipo de check; la CLI registra los que dependen de otros paquetes.
func Register(name string, fn CheckFunc) { registry[name] = fn }

func init() {
	Register("file_exists", checkFileExists)
	Register("file_max_tokens", checkFileMaxTokens)
	Register("file_max_lines", checkFileMaxLines)
	Register("regex_present", checkRegexPresent)
	Register("regex_absent", checkRegexAbsent)
	Register("path_forbidden", checkPathForbidden)
	Register("commit_format", checkCommitFormat)
	Register("ccfdoc_valid", checkCCFDoc)
	Register("attribution", checkAttribution)
	Register("script", checkScript)
	Register("secrets", checkSecrets)
	Register("infra", checkInfra)
}

// Lint aplica todas las reglas activas con check al proyecto.
func Lint(ctx *Context, st *Standard) *Result {
	res := &Result{}
	if st.Detached {
		res.Checked++
		if st.DetachReason == "" {
			res.Findings = append(res.Findings, Finding{RuleID: "S0", Level: "MUST", Title: "El estándar parte de coyote:default o de un hub",
				Path: "coyote/standards/rules.yaml", Msg: fmt.Sprintf("extends: none sin reason: quedan fuera %d reglas del default", len(st.Dropped())),
				Fix: "usa extends: coyote:default o declara reason: con el motivo"})
		}
	}
	for _, id := range st.Unjustified {
		res.Findings = append(res.Findings, Finding{RuleID: "S1", Level: "MUST", Title: "Una redefinición que relaja una MUST declara reason",
			Path: "coyote/standards/rules.yaml", Msg: id + " se redefine con otros checks o perfiles sin reason; rige la definición anterior",
			Fix: "agrega reason: con el motivo, o usa override con reason"})
	}
	for _, r := range st.Rules {
		if r.Disabled {
			res.Skipped = append(res.Skipped, r.ID+" desactivada")
			continue
		}
		if !r.AppliesTo(ctx.Profile) {
			res.Skipped = append(res.Skipped, r.ID+" no aplica al perfil "+ctx.Profile)
			continue
		}
		checks := r.AllChecks()
		if len(checks) == 0 {
			continue
		}
		res.Checked++
		for _, c := range checks {
			if c.Type == "script" && !ctx.AllowScripts {
				res.Skipped = append(res.Skipped, r.ID+" script omitido (usa --scripts)")
				continue
			}
			fn, ok := registry[c.Type]
			if !ok {
				res.Findings = append(res.Findings, Finding{RuleID: r.ID, Level: r.Level, Title: r.Title, Msg: "check desconocido " + c.Type})
				continue
			}
			for _, f := range fn(ctx, c) {
				f.RuleID, f.Title = r.ID, r.Title
				if f.Level == "" || levelRank[f.Level] > levelRank[r.Level] {
					f.Level = r.Level
				}
				if f.Fix == "" {
					f.Fix = r.Fix
				}
				if reason, ok := ctx.Waivers[r.ID]; ok {
					switch {
					case reason != "":
						f.Waived, f.WaiveReason = true, reason
					case r.Level == "MUST":
						f.Msg += " (la dispensa de una regla MUST necesita motivo; no se aplicó)"
					default:
						f.Waived, f.WaiveReason = true, "sin motivo"
					}
				}
				res.Findings = append(res.Findings, f)
			}
		}
	}
	sort.SliceStable(res.Findings, func(i, j int) bool {
		a, b := res.Findings[i], res.Findings[j]
		if a.Waived != b.Waived {
			return !a.Waived
		}
		if levelRank[a.Level] != levelRank[b.Level] {
			return levelRank[a.Level] > levelRank[b.Level]
		}
		if a.RuleID != b.RuleID {
			return a.RuleID < b.RuleID
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Line < b.Line
	})
	return res
}

// ListFiles lista los archivos del proyecto: los de git si es un repo, o un
// recorrido del directorio que salta dependencias y artefactos de build.
func ListFiles(root string) ([]string, error) {
	if gitx.IsRepo(root) {
		if files, err := gitx.ListFiles(root); err == nil {
			out := files[:0]
			for _, f := range files {
				if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(f))); err == nil {
					out = append(out, f)
				}
			}
			return out, nil
		}
	}
	skip := map[string]bool{".git": true, ".coyote": true, "node_modules": true, "vendor": true,
		"build": true, "dist": true, ".dart_tool": true, "target": true}
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && skip[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err == nil {
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

// AdoptedCommits devuelve hasta n commits desde que el proyecto adoptó coyote
// (el commit que agregó coyote/project.yaml, incluido). Con merges=true
// incluye los commits de merge, que también llevan mensaje propio.
func AdoptedCommits(root string, n int, merges bool) ([]gitx.Commit, error) {
	if !gitx.IsRepo(root) || !gitx.HasCommits(root) {
		return nil, nil
	}
	// La base es el commit más antiguo que agregó coyote/project.yaml: borrarlo y
	// volver a agregarlo no reinicia el historial que se revisa.
	adds, err := gitx.Run(root, "log", "--diff-filter=A", "--format=%H", "--", "coyote/project.yaml")
	adds = strings.TrimSpace(adds)
	if err != nil || adds == "" {
		return nil, nil
	}
	lines := strings.Split(adds, "\n")
	base := strings.TrimSpace(lines[len(lines)-1])
	commits, err := gitx.Log(root, n, merges, base+"..HEAD")
	if err != nil {
		return nil, err
	}
	first, err := gitx.Log(root, 1, merges, base)
	if err != nil {
		return nil, err
	}
	return append(commits, first...), nil
}

func (ctx *Context) abs(rel string) string {
	return filepath.Join(ctx.Root, filepath.FromSlash(rel))
}

func (ctx *Context) matchFiles(patterns, except []string) []string {
	var out []string
	for _, f := range ctx.Files {
		if glob.Any(patterns, f) && !glob.Any(except, f) {
			out = append(out, f)
		}
	}
	return out
}

func alternatives(entry string) []string {
	var out []string
	for _, p := range strings.Split(entry, "|") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (ctx *Context) exists(pattern string) bool {
	if glob.HasMeta(pattern) {
		return len(ctx.matchFiles([]string{pattern}, nil)) > 0
	}
	_, err := os.Stat(ctx.abs(pattern))
	return err == nil
}

func (ctx *Context) expand(entries []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(f string) {
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	for _, e := range entries {
		for _, alt := range alternatives(e) {
			if glob.HasMeta(alt) {
				for _, f := range ctx.matchFiles([]string{alt}, nil) {
					add(f)
				}
			} else if info, err := os.Stat(ctx.abs(alt)); err == nil && !info.IsDir() {
				add(alt)
			}
		}
	}
	return out
}

func (ctx *Context) readText(rel string) (string, bool) {
	// Solo archivos regulares y con tope: un symlink a /dev/zero no se lee.
	data, err := fsx.ReadCapped(ctx.abs(rel), 2<<20)
	if err != nil {
		return "", false
	}
	head := data
	if len(head) > 8000 {
		head = head[:8000]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return "", false
	}
	return string(data), true
}

func short(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 90 {
		return string(r[:90]) + "…"
	}
	return s
}

func checkFileExists(ctx *Context, c Check) []Finding {
	var out []Finding
	for _, entry := range c.Files {
		found := false
		for _, alt := range alternatives(entry) {
			if ctx.exists(alt) {
				found = true
				break
			}
		}
		if !found {
			out = append(out, Finding{Path: entry, Msg: "falta " + entry})
		}
	}
	return out
}

func checkFileMaxTokens(ctx *Context, c Check) []Finding {
	var out []Finding
	for _, f := range ctx.expand(c.Files) {
		if text, ok := ctx.readText(f); ok {
			if n := tokens.Estimate(text); c.Max > 0 && n > c.Max {
				out = append(out, Finding{Path: f, Msg: fmt.Sprintf("~%d tokens; máximo %d", n, c.Max)})
			}
		}
	}
	return out
}

func checkFileMaxLines(ctx *Context, c Check) []Finding {
	var out []Finding
	for _, f := range ctx.expand(c.Files) {
		if text, ok := ctx.readText(f); ok {
			n := strings.Count(text, "\n")
			if text != "" && !strings.HasSuffix(text, "\n") {
				n++
			}
			if c.Max > 0 && n > c.Max {
				out = append(out, Finding{Path: f, Msg: fmt.Sprintf("%d líneas; máximo %d", n, c.Max)})
			}
		}
	}
	return out
}

func checkRegexPresent(ctx *Context, c Check) []Finding {
	re, err := regexp.Compile(c.Pattern)
	if err != nil {
		return []Finding{{Msg: "patrón inválido: " + err.Error()}}
	}
	files := ctx.expand(c.Files)
	if len(files) == 0 {
		return []Finding{{Path: strings.Join(c.Files, ", "), Msg: "no hay archivos que revisar"}}
	}
	var out []Finding
	for _, f := range files {
		if text, ok := ctx.readText(f); ok && !re.MatchString(text) {
			out = append(out, Finding{Path: f, Msg: "no contiene " + c.Pattern})
		}
	}
	return out
}

func checkRegexAbsent(ctx *Context, c Check) []Finding {
	re, err := regexp.Compile(c.Pattern)
	if err != nil {
		return []Finding{{Msg: "patrón inválido: " + err.Error()}}
	}
	paths := c.Paths
	if len(paths) == 0 {
		paths = []string{"**"}
	}
	var out []Finding
	for _, f := range ctx.matchFiles(paths, c.Except) {
		text, ok := ctx.readText(f)
		if !ok {
			continue
		}
		perFile := 0
		for i, line := range strings.Split(text, "\n") {
			if re.MatchString(line) {
				out = append(out, Finding{Path: f, Line: i + 1, Msg: "patrón prohibido: " + short(line)})
				if perFile++; perFile >= 5 {
					break
				}
			}
		}
		if len(out) >= 50 {
			break
		}
	}
	return out
}

func checkPathForbidden(ctx *Context, c Check) []Finding {
	var out []Finding
	for _, f := range ctx.matchFiles(c.Paths, c.Except) {
		out = append(out, Finding{Path: f, Msg: "ruta prohibida"})
		if len(out) >= 50 {
			break
		}
	}
	return out
}

func checkCommitFormat(ctx *Context, c Check) []Finding {
	pattern := c.Pattern
	if pattern == "" {
		pattern = DefaultCommitPattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return []Finding{{Msg: "patrón inválido: " + err.Error()}}
	}
	n := c.Commits
	if n <= 0 {
		n = 50
	}
	commits, err := AdoptedCommits(ctx.Root, n, false)
	if err != nil {
		return []Finding{{Msg: "no se pudo leer el historial: " + err.Error()}}
	}
	var out []Finding
	if f, shallow := shallowFinding(ctx.Root); shallow {
		out = append(out, f)
	}
	for _, cm := range commits {
		if !re.MatchString(cm.Subject) {
			out = append(out, Finding{Path: "commit " + cm.Short(), Msg: fmt.Sprintf("%q no sigue tipo(ámbito): descripción", short(cm.Subject))})
		}
	}
	return out
}

func checkCCFDoc(ctx *Context, c Check) []Finding {
	var out []Finding
	for _, f := range c.Files {
		data, err := os.ReadFile(ctx.abs(f))
		if err != nil {
			continue // file_exists reporta los faltantes
		}
		d, issues := ccfdoc.Parse(f, data)
		issues = append(issues, d.Validate()...)
		for _, is := range issues {
			fd := Finding{Path: f, Line: is.Line, Msg: is.Msg}
			if !is.Error {
				fd.Level = "SHOULD"
			}
			out = append(out, fd)
		}
	}
	return out
}

func checkAttribution(ctx *Context, c Check) []Finding {
	cfg := ctx.Attribution
	if cfg == nil {
		var err error
		if cfg, err = attribution.Default(); err != nil {
			return []Finding{{Msg: err.Error()}}
		}
	}
	paths := c.Paths
	if len(paths) == 0 {
		paths = []string{"**/*.md"}
	}
	var out []Finding
	for _, f := range ctx.matchFiles(paths, c.Except) {
		if cfg.Allowed(f) {
			continue
		}
		if text, ok := ctx.readText(f); ok {
			for _, fd := range cfg.Check(text, false) {
				out = append(out, Finding{Path: f, Line: fd.Line, Msg: "atribución a IA (" + fd.PatternID + "): " + short(fd.Text)})
			}
		}
	}
	if c.Commits > 0 {
		commits, err := AdoptedCommits(ctx.Root, c.Commits, true)
		if err != nil {
			return append(out, Finding{Msg: "no se pudo leer el historial: " + err.Error()})
		}
		if f, shallow := shallowFinding(ctx.Root); shallow {
			out = append(out, f)
		}
		out = append(out, CommitAttribution(cfg, commits)...)
	}
	return out
}

// CommitAttribution revisa mensaje, autor y committer de cada commit.
func CommitAttribution(cfg *attribution.Config, commits []gitx.Commit) []Finding {
	var out []Finding
	for _, cm := range commits {
		where := "commit " + cm.Short()
		for _, fd := range cfg.Check(cm.Message(), false) {
			out = append(out, Finding{Path: where, Line: fd.Line, Msg: "atribución a IA (" + fd.PatternID + "): " + short(fd.Text)})
		}
		for _, id := range []struct{ role, name, email string }{
			{"autor", cm.AuthorName, cm.AuthorEmail}, {"committer", cm.CommitterName, cm.CommitterEmail}} {
			if fd, ok := cfg.AIIdentity(id.name, id.email); ok {
				out = append(out, Finding{Path: where, Msg: id.role + " es una herramienta de IA: " + short(fd.Text)})
			}
		}
	}
	return out
}

// shallowFinding avisa que un clon superficial no deja revisar el historial.
func shallowFinding(root string) (Finding, bool) {
	if !gitx.IsRepo(root) || !gitx.IsShallow(root) {
		return Finding{}, false
	}
	return Finding{Path: "historial", Msg: "clon superficial: el historial está incompleto (en CI usa fetch-depth: 0)"}, true
}

// checkSecrets revisa que ningún archivo del proyecto (los versionados y los
// nuevos que git no ignora) sea un archivo de secretos ni lleve un secreto
// escrito (R18, ADR-0016). Un hallazgo nunca muestra el valor.
func checkSecrets(ctx *Context, c Check) []Finding {
	var out []Finding
	for _, f := range ctx.Files {
		if glob.Any(c.Except, f) || ctx.Secrets.Allowed(f) {
			continue
		}
		text, readable := ctx.readText(f)
		if kind, ok := ctx.Secrets.Kind(f); ok && (!readable || secrets.Confirm(kind, []byte(text))) {
			out = append(out, Finding{Path: f, Msg: "archivo de secretos en el repo (" + kind + "): sácalo de git y agrégalo a .gitignore"})
			continue
		}
		if !readable {
			continue
		}
		for _, fd := range secrets.Scan(f, text) {
			out = append(out, Finding{Path: f, Line: fd.Line, Msg: fd.Kind + " (" + fd.Hint + "): muévelo a una variable de entorno o al gestor de secretos y rótalo"})
		}
	}
	return out
}

// checkInfra revisa el inventario de la infraestructura contra el repo (R13,
// ADR-0017): que exista, que sea válido y que coincida con los stacks, los
// ambientes, el estado remoto y los presupuestos. Los avisos cuentan como SHOULD.
func checkInfra(ctx *Context, c Check) []Finding {
	inv, ok, err := infra.Load(ctx.Root)
	switch {
	case !ok:
		return []Finding{{Path: infra.Path, Msg: "falta el inventario de la infraestructura; propónlo con coyote infra propose"}}
	case err != nil:
		return []Finding{{Path: infra.Path, Msg: err.Error()}}
	}
	var out []Finding
	for _, f := range infra.Check(ctx.Root, inv, ctx.Files) {
		fd := Finding{Path: infra.Path, Msg: f.Where + ": " + f.Msg}
		if f.Level != infra.Error {
			fd.Level = "SHOULD"
		}
		out = append(out, fd)
	}
	return out
}

func checkScript(ctx *Context, c Check) []Finding {
	if strings.TrimSpace(c.Script) == "" {
		return nil
	}
	cctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "sh", "-c", c.Script)
	cmd.Dir = ctx.Root
	out, err := cmd.CombinedOutput()
	if err != nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		return []Finding{{Path: "script", Msg: fmt.Sprintf("%s falló: %s", c.Script, short(lines[len(lines)-1]))}}
	}
	return nil
}
