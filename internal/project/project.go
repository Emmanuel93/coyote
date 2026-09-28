// Package project define el proyecto Coyote (coyote/project.yaml) y crea o
// adopta proyectos: coyote init.
package project

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
	"time"

	"go.yaml.in/yaml/v3"

	coyote "github.com/Emmanuel93/coyote"
	"github.com/Emmanuel93/coyote/internal/agentsmd"
	"github.com/Emmanuel93/coyote/internal/ccf"
	"github.com/Emmanuel93/coyote/internal/ccfdoc"
	"github.com/Emmanuel93/coyote/internal/fsx"
	"github.com/Emmanuel93/coyote/internal/gitx"
	"github.com/Emmanuel93/coyote/internal/identity"
	"github.com/Emmanuel93/coyote/internal/ledger"
	"github.com/Emmanuel93/coyote/internal/standards"
)

// ConfigPath es la ruta del archivo de proyecto relativa a la raíz.
const ConfigPath = "coyote/project.yaml"

// HookMarker identifica el hook commit-msg de coyote.
const HookMarker = "# coyote: commit-msg"

// ErrNotProject indica que no se encontró coyote/project.yaml.
var ErrNotProject = errors.New("no es un proyecto coyote: falta coyote/project.yaml (corre coyote init)")

// Autonomies son los modos de autonomía válidos.
var Autonomies = []string{"manual", "supervised", "autonomous"}

// RepoRef es un repo enlazado a un proyecto multi-repo.
type RepoRef struct {
	Name   string `yaml:"name"`
	URL    string `yaml:"url,omitempty"`
	Path   string `yaml:"path,omitempty"`
	Branch string `yaml:"branch,omitempty"`
}

// Config es coyote/project.yaml.
type Config struct {
	Version  int    `yaml:"version"`
	Name     string `yaml:"name"`
	Type     string `yaml:"type"`
	Hub      string `yaml:"hub,omitempty"`
	Language struct {
		Docs string `yaml:"docs,omitempty"`
		Code string `yaml:"code,omitempty"`
	} `yaml:"language,omitempty"`
	Autonomy string `yaml:"autonomy,omitempty"`
	Budgets  struct {
		MonthlyUSD float64 `yaml:"monthly_usd,omitempty"`
	} `yaml:"budgets,omitempty"`
	Pace struct {
		Profile               string  `yaml:"profile,omitempty"`
		WritesPerMinute       int     `yaml:"writes_per_minute,omitempty"`
		MinGapSeconds         int     `yaml:"min_gap_seconds,omitempty"`
		ReserveForInteractive float64 `yaml:"reserve_for_interactive,omitempty"`
	} `yaml:"pace,omitempty"`
	Repos []RepoRef `yaml:"repos,omitempty"`
}

var nameRe = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N}._-]*$`)

// FindRoot sube desde start hasta encontrar coyote/project.yaml.
func FindRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(ConfigPath))); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ErrNotProject
		}
		dir = parent
	}
}

// Load lee y valida coyote/project.yaml.
func Load(root string) (*Config, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(ConfigPath)))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotProject
		}
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", ConfigPath, err)
	}
	if c.Autonomy == "" {
		c.Autonomy = "manual"
	}
	if c.Name == "" {
		c.Name = filepath.Base(root)
	}
	return &c, c.Validate()
}

// Validate revisa los campos del proyecto.
func (c *Config) Validate() error {
	var errs []string
	if !nameRe.MatchString(c.Name) {
		errs = append(errs, fmt.Sprintf("name inválido %q", c.Name))
	}
	if c.Type != "" && !ccfdoc.ValidProjectType(c.Type) {
		errs = append(errs, fmt.Sprintf("type %q inválido (%s)", c.Type, strings.Join(ccfdoc.ProjectTypes, ", ")))
	}
	valid := false
	for _, a := range Autonomies {
		valid = valid || a == c.Autonomy
	}
	if !valid {
		errs = append(errs, fmt.Sprintf("autonomy %q inválido (%s)", c.Autonomy, strings.Join(Autonomies, ", ")))
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s: %s", ConfigPath, strings.Join(errs, "; "))
	}
	return nil
}

// InitOptions configura coyote init.
type InitOptions struct {
	Dir, Name, Type, Hub, Purpose string
	User                          identity.Person
	NoGit, NoHooks, NoClaude      bool
	Now                           time.Time
}

// Action es lo que init hizo con un archivo.
type Action struct{ Path, Status string }

// InitResult resume coyote init.
type InitResult struct {
	Root       string
	Name       string
	Actions    []Action
	GitInit    bool
	Warnings   []string
	LedgerPath string
}

// Init crea o adopta un proyecto sin sobrescribir archivos existentes.
func Init(o InitOptions) (*InitResult, error) {
	if o.Type == "" {
		o.Type = "other"
	}
	if !ccfdoc.ValidProjectType(o.Type) {
		return nil, fmt.Errorf("tipo %q inválido; usa %s", o.Type, strings.Join(ccfdoc.ProjectTypes, ", "))
	}
	dir, err := filepath.Abs(o.Dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if o.Name == "" {
		o.Name = filepath.Base(dir)
	}
	if !nameRe.MatchString(o.Name) {
		return nil, fmt.Errorf("nombre %q inválido: usa letras, números, punto, guion o guion bajo", o.Name)
	}
	if o.Purpose == "" {
		o.Purpose = "TODO: qué hace este repo en una línea"
	}
	res := &InitResult{Root: dir, Name: o.Name}
	if !o.NoGit && !gitx.IsRepo(dir) {
		if err := gitx.Init(dir); err != nil {
			return nil, err
		}
		res.GitInit = true
	}
	data := map[string]string{"Name": o.Name, "Type": o.Type, "Hub": o.Hub, "User": o.User.Slug,
		"Date": o.Now.Format("2006-01-02"), "Purpose": strings.ReplaceAll(o.Purpose, "|", "/")}
	files := []struct {
		tmpl, dst string
		skip      bool
	}{
		{"README.md.tmpl", "README.md", false},
		{"README.coyote.md.tmpl", ccfdoc.ReadmeFile, false},
		{"CONTEXT.coyote.md.tmpl", ccfdoc.ContextFile, false},
		{"CLAUDE.md.tmpl", "CLAUDE.md", false},
		{"coyoteignore.tmpl", ".coyoteignore", false},
		{"project.yaml.tmpl", ConfigPath, false},
		{"rules.yaml.tmpl", "coyote/standards/rules.yaml", false},
		{"claude-settings.json.tmpl", ".claude/settings.json", o.NoClaude},
	}
	for _, f := range files {
		if f.skip {
			continue
		}
		status, err := writeTemplate(dir, f.tmpl, f.dst, data)
		if err != nil {
			return nil, err
		}
		res.Actions = append(res.Actions, Action{f.dst, status})
	}
	for _, keep := range []string{"coyote/decisions", "coyote/workstreams", "coyote/approvals", "coyote/runbooks"} {
		if err := fsx.NoSymlinks(dir, keep+"/.gitkeep"); err != nil {
			res.Warnings = append(res.Warnings, err.Error())
			continue
		}
		p := filepath.Join(dir, filepath.FromSlash(keep))
		if entries, err := os.ReadDir(p); err == nil && len(entries) > 0 {
			continue
		}
		if err := os.MkdirAll(p, 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(p, ".gitkeep"), nil, 0o644); err != nil {
			return nil, err
		}
	}
	status, err := ensureLine(filepath.Join(dir, ".gitignore"), ".coyote/")
	if err != nil {
		return nil, err
	}
	res.Actions = append(res.Actions, Action{".gitignore", status})

	st, err := standards.Load(dir, o.Now)
	if err != nil {
		return nil, err
	}
	cfg, err := Load(dir)
	if err != nil {
		return nil, err
	}
	content, err := agentsmd.Generate(dir, st, cfg.Autonomy)
	if err != nil {
		return nil, err
	}
	status, err = agentsmd.Write(dir, content, false)
	if err != nil {
		res.Warnings = append(res.Warnings, err.Error())
	}
	res.Actions = append(res.Actions, Action{"AGENTS.md", status})

	if !o.NoHooks && gitx.IsRepo(dir) {
		p, status, err := InstallHook(dir, false, true)
		if err != nil {
			res.Warnings = append(res.Warnings, err.Error())
		} else {
			rel, _ := filepath.Rel(dir, p)
			res.Actions = append(res.Actions, Action{filepath.ToSlash(rel), status})
		}
	}
	changed := res.GitInit
	for _, a := range res.Actions {
		changed = changed || a.Status == "creado" || a.Status == "actualizado"
	}
	if !changed {
		return res, nil // nada nuevo: no hay nada que registrar
	}
	line := ccf.Line{TS: o.Now, Actor: o.User.Actor(""), Project: "-", Repo: o.Name, Type: "init",
		Scope: "coyote", What: "proyecto inicializado con coyote", Status: "ok"}
	if res.LedgerPath, err = ledger.Open(dir).Append(line, o.User.Slug); err != nil {
		return nil, err
	}
	return res, nil
}

func writeTemplate(dir, tmpl, dst string, data map[string]string) (string, error) {
	target := filepath.Join(dir, filepath.FromSlash(dst))
	if _, err := os.Lstat(target); err == nil {
		return "existe", nil // también un symlink, aunque apunte a nada: no se escribe a través de él
	}
	if err := fsx.NoSymlinks(dir, dst); err != nil {
		return "omitido: " + err.Error(), nil
	}
	raw, err := fs.ReadFile(coyote.Templates, "templates/project/"+tmpl)
	if err != nil {
		return "", err
	}
	t, err := template.New(tmpl).Option("missingkey=error").Parse(string(raw))
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}
	return "creado", os.WriteFile(target, buf.Bytes(), 0o644)
}

func ensureLine(path, line string) (string, error) {
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "omitido", nil // no se escribe a través de un symlink
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "creado", os.WriteFile(path, []byte(line+"\n"), 0o644)
	}
	if err != nil {
		return "", err
	}
	for _, l := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(l)
		if t == line || t == "/"+line || t == strings.TrimSuffix(line, "/") {
			return "existe", nil
		}
	}
	content := string(data)
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return "actualizado", os.WriteFile(path, []byte(content+line+"\n"), 0o644)
}

// hookHeader abre el hook que genera coyote; un hook que no empieza así es ajeno.
const hookHeader = "#!/bin/sh\n" + HookMarker

// InstallHook instala el hook commit-msg de coyote. Nunca pisa un hook ajeno
// sin force; con onlyCreate (coyote init) tampoco actualiza uno de coyote. No
// instala en un core.hooksPath fuera del repo, que afectaría a otros repos.
func InstallHook(dir string, force, onlyCreate bool) (string, string, error) {
	hooks, outside, err := gitx.HooksDir(dir)
	if err != nil {
		return "", "", err
	}
	p := filepath.Join(hooks, "commit-msg")
	if outside && !force {
		return p, "omitido", fmt.Errorf("core.hooksPath apunta fuera del repo (%s); coyote no instala hooks compartidos: agrega a tu hook commit-msg la línea coyote attribution scrub --in-place --commit-msg \"$1\"", hooks)
	}
	data, err := fs.ReadFile(coyote.Templates, "templates/project/commit-msg")
	if err != nil {
		return "", "", err
	}
	status := "creado"
	if existing, err := os.ReadFile(p); err == nil {
		switch {
		case bytes.Equal(existing, data):
			return p, "existe", nil
		case force:
		case !strings.HasPrefix(string(existing), hookHeader):
			return p, "omitido", fmt.Errorf("ya hay un hook commit-msg ajeno en %s; agrégale la línea coyote attribution scrub --in-place --commit-msg \"$1\" o usa coyote hooks install --force", p)
		case onlyCreate:
			return p, "existe", nil
		default:
			// Un hook de coyote distinto de la plantilla es de otra versión o tiene
			// cambios de la persona: no se pisa sin --force.
			return p, "omitido", fmt.Errorf("el hook commit-msg de %s tiene cambios o es de otra versión; revísalo y usa coyote hooks install --force para reemplazarlo", p)
		}
		status = "actualizado"
	}
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(p, data, 0o755); err != nil {
		return "", "", err
	}
	return p, status, os.Chmod(p, 0o755)
}

// HookCurrent informa si el hook commit-msg es exactamente el de esta versión de coyote.
func HookCurrent(dir string) bool {
	hooks, _, err := gitx.HooksDir(dir)
	if err != nil {
		return false
	}
	have, err := os.ReadFile(filepath.Join(hooks, "commit-msg"))
	if err != nil {
		return false
	}
	want, err := fs.ReadFile(coyote.Templates, "templates/project/commit-msg")
	return err == nil && bytes.Equal(have, want)
}

// HookInstalled informa si el hook commit-msg limpia la atribución: el de
// coyote o uno propio que ejecuta coyote attribution scrub.
func HookInstalled(dir string) bool {
	hooks, _, err := gitx.HooksDir(dir)
	if err != nil {
		return false
	}
	data, err := os.ReadFile(filepath.Join(hooks, "commit-msg"))
	if err != nil {
		return false
	}
	// Cuenta una línea que ejecuta la limpieza, no un comentario que la menciona.
	for _, line := range strings.Split(string(data), "\n") {
		if t := strings.TrimSpace(line); t != "" && !strings.HasPrefix(t, "#") && strings.Contains(t, "attribution scrub") {
			return true
		}
	}
	return false
}

var (
	repoNameRe = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N}._-]*$`)
	scpLikeRe  = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+:[^\s]+$`)
)

// ValidateRepo revisa un repo antes de guardarlo: nombre simple, URL sin
// credenciales ni transportes que ejecuten comandos, y nada que git pueda
// confundir con una opción.
func ValidateRepo(r RepoRef) error {
	if !repoNameRe.MatchString(r.Name) {
		return fmt.Errorf("nombre de repo inválido %q: usa letras, números, punto, guion o guion bajo", r.Name)
	}
	if r.URL == "" && r.Path == "" {
		return fmt.Errorf("el repo %s necesita una URL o una ruta local", r.Name)
	}
	for _, v := range []string{r.URL, r.Path, r.Branch} {
		if strings.HasPrefix(strings.TrimSpace(v), "-") {
			return fmt.Errorf("%q no puede empezar con guion", v)
		}
		if strings.ContainsAny(v, "\n\r\x00") {
			return fmt.Errorf("valor inválido %q", v)
		}
	}
	if u := r.URL; u != "" {
		switch {
		case strings.Contains(u, "::"):
			return fmt.Errorf("transporte no permitido en %q", u)
		case scpLikeRe.MatchString(u):
		case strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "ssh://") || strings.HasPrefix(u, "file://"):
			rest := u[strings.Index(u, "://")+3:]
			host := rest
			if i := strings.Index(rest, "/"); i >= 0 {
				host = rest[:i]
			}
			if at := strings.LastIndex(host, "@"); at >= 0 && strings.Contains(host[:at], ":") {
				return fmt.Errorf("la URL lleva credenciales; usa la URL sin usuario ni token (git usa tus credenciales del sistema)")
			}
			if strings.HasPrefix(u, "https://") && strings.Contains(host, "@") {
				return fmt.Errorf("la URL https lleva usuario; quítalo y deja que git use tu credential helper")
			}
		default:
			return fmt.Errorf("URL no soportada %q: usa https://, ssh://, git@host:owner/repo.git o file://", u)
		}
	}
	return nil
}

// AddRepo agrega o actualiza un repo en coyote/project.yaml. Edita el texto
// para conservar comentarios, líneas en blanco y formato; si la sección repos
// tiene una forma que no reconoce, recurre a reescribir el YAML. Nunca deja un
// project.yaml inválido: si lo escrito no se puede cargar, restaura el original.
func AddRepo(root string, r RepoRef) (string, error) {
	if err := ValidateRepo(r); err != nil {
		return "", err
	}
	if err := fsx.NoSymlinks(root, ConfigPath); err != nil {
		return "", err
	}
	p := filepath.Join(root, filepath.FromSlash(ConfigPath))
	data, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	if _, err := Load(root); err != nil {
		return "", err
	}
	out, status, ok := addRepoText(string(data), r)
	if !ok {
		b, st, err := addRepoNode(data, r)
		if err != nil {
			return "", err
		}
		out, status = string(b), st
	}
	if err := os.WriteFile(p, []byte(out), 0o644); err != nil {
		return "", err
	}
	if _, err := Load(root); err != nil {
		_ = os.WriteFile(p, data, 0o644) // lo escrito no se puede leer: se deja como estaba
		return "", err
	}
	return status, nil
}

// yamlQuote devuelve un escalar YAML entre comillas dobles.
func yamlQuote(v string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v) + `"`
}

func repoLine(indent string, r RepoRef) string {
	parts := []string{"name: " + r.Name}
	for _, kv := range [][2]string{{"url", r.URL}, {"path", r.Path}, {"branch", r.Branch}} {
		if kv[1] != "" {
			parts = append(parts, kv[0]+": "+yamlQuote(kv[1]))
		}
	}
	return indent + "- { " + strings.Join(parts, ", ") + " }"
}

var (
	reposKeyRe = regexp.MustCompile(`^repos:\s*(#.*)?$`)
	reposEmpty = regexp.MustCompile(`^repos:\s*(\[\s*\]|~|null)?\s*(#.*)?$`)
	itemRe     = regexp.MustCompile(`^(\s*)-\s`)
)

// addRepoText edita la sección repos como texto; ok=false si no reconoce su forma.
func addRepoText(content string, r RepoRef) (string, string, bool) {
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	key := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "repos:") {
			key = i
			break
		}
	}
	if key < 0 {
		return strings.Join(lines, "\n") + "\nrepos:\n" + repoLine("  ", r) + "\n", "agregado", true
	}
	if !reposEmpty.MatchString(lines[key]) {
		return "", "", false // repos: [ ... ] en una línea u otra forma: se reescribe con YAML
	}
	if !reposKeyRe.MatchString(lines[key]) {
		lines[key] = "repos:"
	}
	end := len(lines)
	for j := key + 1; j < len(lines); j++ {
		t := strings.TrimSpace(lines[j])
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if !strings.HasPrefix(lines[j], " ") && !strings.HasPrefix(lines[j], "-") {
			end = j
			break
		}
	}
	indent, last := "  ", key
	nameRe := regexp.MustCompile(`^\s*-\s*\{.*\bname:\s*"?` + regexp.QuoteMeta(r.Name) + `"?\s*[,}]`)
	blockName := regexp.MustCompile(`^\s*-\s*name:\s*"?` + regexp.QuoteMeta(r.Name) + `"?\s*(#.*)?$`)
	for j := key + 1; j < end; j++ {
		l := lines[j]
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if m := itemRe.FindStringSubmatch(l); m != nil {
			indent = m[1]
			if nameRe.MatchString(l) {
				lines[j] = repoLine(indent, r)
				return strings.Join(lines, "\n") + "\n", "actualizado", true
			}
			if blockName.MatchString(l) {
				return "", "", false // entrada en varias líneas: se reescribe con YAML
			}
			if !strings.HasPrefix(t, "- {") || !strings.HasSuffix(t, "}") {
				return "", "", false
			}
		} else if !strings.HasPrefix(t, "#") {
			return "", "", false
		}
		last = j
	}
	out := append([]string{}, lines[:last+1]...)
	out = append(out, repoLine(indent, r))
	out = append(out, lines[last+1:]...)
	return strings.Join(out, "\n") + "\n", "agregado", true
}

// addRepoNode agrega o actualiza el repo reescribiendo el YAML (puede perder formato).
func addRepoNode(data []byte, r RepoRef) ([]byte, string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, "", fmt.Errorf("%s: %w", ConfigPath, err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, "", fmt.Errorf("%s no es un mapa YAML", ConfigPath)
	}
	top := doc.Content[0]
	var repos *yaml.Node
	for i := 0; i+1 < len(top.Content); i += 2 {
		if top.Content[i].Value == "repos" {
			repos = top.Content[i+1]
			if repos.Kind != yaml.SequenceNode {
				repos.Kind, repos.Tag, repos.Value, repos.Content = yaml.SequenceNode, "!!seq", "", nil
			}
		}
	}
	if repos == nil {
		repos = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		top.Content = append(top.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "repos"}, repos)
	}
	entry := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Style: yaml.FlowStyle}
	for _, kv := range [][2]string{{"name", r.Name}, {"url", r.URL}, {"path", r.Path}, {"branch", r.Branch}} {
		if kv[1] == "" {
			continue
		}
		entry.Content = append(entry.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: kv[0]},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: kv[1]})
	}
	status := "agregado"
	replaced := false
	for i, n := range repos.Content {
		for j := 0; j+1 < len(n.Content); j += 2 {
			if n.Content[j].Value == "name" && n.Content[j+1].Value == r.Name {
				repos.Content[i], replaced, status = entry, true, "actualizado"
			}
		}
	}
	if !replaced {
		repos.Content = append(repos.Content, entry)
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, "", err
	}
	if err := enc.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), status, nil
}
