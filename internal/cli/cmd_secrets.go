package cli

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Emmanuel93/coyote/internal/fsx"
	"github.com/Emmanuel93/coyote/internal/glob"
	"github.com/Emmanuel93/coyote/internal/product"
	"github.com/Emmanuel93/coyote/internal/project"
	"github.com/Emmanuel93/coyote/internal/secrets"
	"github.com/Emmanuel93/coyote/internal/standards"
)

// coyote secrets (ADR-0016): qué archivos de secretos tiene el proyecto y
// qué variables guardan, sin sus valores, y dónde hay un secreto escrito.
// Los dos subcomandos solo leen y nunca muestran un valor: un agente los
// corre sin aprobación.

const secretsUsage = "list [--json] | scan [--staged | --range base...head] [--json]"

func cmdSecrets(a *app, args []string) error {
	if len(args) == 0 {
		return fail(2, "uso: coyote secrets "+secretsUsage)
	}
	switch args[0] {
	case "list":
		return secretsList(a, args[1:])
	case "scan":
		return secretsScan(a, args[1:])
	}
	return fail(2, "uso: coyote secrets "+secretsUsage)
}

// secretRules devuelve las reglas del proyecto que contiene dir, o las de
// siempre si dir no está en un proyecto coyote. Un project.yaml que no se
// puede leer (por ejemplo, porque pide un coyote más nuevo) es un error: sin
// sus secrets.files, un secreto declarado pasaría.
func secretRules(dir string) (secrets.Rules, string, error) {
	root, err := project.FindRoot(dir)
	if err != nil {
		return secrets.Rules{}, "", nil
	}
	cfg, err := project.Load(root)
	if err != nil {
		return secrets.Rules{}, root, fmt.Errorf("%v; sin las reglas de secretos del proyecto no se revisa nada", err)
	}
	return cfg.Secrets, root, nil
}

// secretSkipDirs no se recorren al listar: dependencias y compilación.
var secretSkipDirs = map[string]bool{".git": true, "node_modules": true, "vendor": true, "build": true, "dist": true,
	"target": true, ".gradle": true, ".dart_tool": true, "Pods": true, ".venv": true, "venv": true,
	"__pycache__": true, ".idea": true, ".next": true, ".coyote": true}

const secretsListLimit = 100000

type secretEntry struct {
	Path  string   `json:"path"`
	Kind  string   `json:"kind"`
	Names []string `json:"names,omitempty"`
}

func secretsList(a *app, args []string) error {
	fl := a.flags("secrets list", "[--json] [--no-names]")
	asJSON := fl.Bool("json", false, "salida en JSON")
	noNames := fl.Bool("no-names", false, "solo rutas y tipos: no abre los archivos")
	if _, err := parseArgs(fl, args); err != nil {
		return err
	}
	dir, err := a.workdir()
	if err != nil {
		return err
	}
	_, root, err := secretRules(dir)
	if err != nil {
		return fail(1, "%v", err)
	}
	if root == "" {
		root = dir
	}
	// Cada archivo con las reglas del proyecto que lo contiene.
	rs := newRuleSet(root)
	var list []secretEntry
	n := 0
	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		n++
		if n > secretsListLimit {
			return filepath.SkipAll
		}
		if d.IsDir() {
			if p != root && secretSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		rules, prel := rs.For(rel)
		kind, ok := rules.Kind(prel)
		if !ok {
			return nil
		}
		// Un .pem sin llave privada (un certificado, una llave pública) no es
		// secreto; con --no-names no se abre nada y se lista igual.
		if !*noNames {
			proj := strings.TrimSuffix(filepath.ToSlash(p), prel)
			if _, ok := rules.KindAt(filepath.FromSlash(proj), prel); !ok {
				return nil
			}
		}
		e := secretEntry{Path: rel, Kind: kind}
		if *noNames {
			list = append(list, e)
			return nil
		}
		if info, err := d.Info(); err == nil && info.Size() <= 1<<20 {
			if data, err := fsx.ReadCapped(p, fsx.MaxText); err == nil {
				e.Names = secrets.Names(rel, data)
			}
		}
		list = append(list, e)
		return nil
	})
	if walkErr != nil {
		return walkErr
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Path < list[j].Path })
	// La lista sale igual; sin las reglas de un proyecto no está completa.
	done := func() error {
		if rs.err != nil {
			return fail(1, "%v", rs.err)
		}
		return nil
	}
	if *asJSON {
		if list == nil {
			list = []secretEntry{}
		}
		b, _ := json.MarshalIndent(list, "", "  ")
		fmt.Fprintln(a.stdout, string(b))
		return done()
	}
	if len(list) == 0 {
		fmt.Fprintln(a.stdout, "No hay archivos de secretos en el proyecto.")
		return done()
	}
	w := table(a.stdout)
	for _, e := range list {
		names := "-"
		if len(e.Names) > 0 {
			names = strings.Join(e.Names, ", ")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", e.Path, e.Kind, names)
	}
	w.Flush()
	if n > secretsListLimit {
		fmt.Fprintf(a.stdout, "(el proyecto tiene más de %d archivos: la lista puede estar incompleta)\n", secretsListLimit)
	}
	fmt.Fprintln(a.stdout, "Solo nombres: un agente no lee los valores; los pone la persona.")
	return done()
}

func secretsScan(a *app, args []string) error {
	fs := a.flags("secrets scan", "[--staged | --range base...head] [--json]")
	staged := fs.Bool("staged", false, "revisa lo preparado para el commit")
	rng := fs.String("range", "", "revisa lo que agrega un rango de git (base...head)")
	asJSON := fs.Bool("json", false, "salida en JSON")
	hook := fs.Bool("hook", false, "modo del hook commit-msg: solo avisa si encuentra algo")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	if *staged && *rng != "" {
		return fail(2, "usa --staged o --range, no los dos")
	}
	dir, err := a.workdir()
	if err != nil {
		return err
	}
	_, root, err := secretRules(dir)
	if err != nil {
		return fail(1, "%v", err)
	}
	var found []secrets.Finding
	var rulesErr error
	switch {
	case *staged || *rng != "":
		// Las rutas de git son relativas a la raíz del repo; cada una lleva las
		// reglas del proyecto coyote que la contiene. Así el hook commit-msg, que
		// git corre en la raíz, aplica las de un proyecto en una subcarpeta.
		top, _, err := product.GitPrefix(dir)
		if err != nil {
			return fail(1, "%v", err)
		}
		if *staged {
			files, err := product.StagedAdded(dir, false)
			if err != nil {
				return fail(1, "%v", err)
			}
			rs := newRuleSet(top)
			found = scanAdded(files, rs.For, func(p string) (string, bool) { return product.IndexText(dir, p) })
			rulesErr = rs.err
			break
		}
		files, right, err := product.AddedText(dir, *rng)
		if err != nil {
			return fail(1, "%v", err)
		}
		rs := newRuleSet(top)
		found = scanAdded(files, rs.For, func(p string) (string, bool) { return product.FileAt(dir, right, p) })
		rulesErr = rs.err
	default:
		if root != "" {
			dir = root
		}
		files, err := product.TrackedFiles(dir)
		if err != nil {
			// Fuera de git se recorren los archivos, sin dependencias ni compilación.
			if files, err = standards.ListFiles(dir); err != nil {
				return fail(1, "%v", err)
			}
		}
		// Cada archivo con las reglas del proyecto que lo contiene: desde la
		// raíz de un monorepo, un proyecto en una subcarpeta aplica las suyas.
		rs := newRuleSet(dir)
		found = scanTracked(dir, files, rs.For)
		rulesErr = rs.err
	}
	if *asJSON {
		if found == nil {
			found = []secrets.Finding{}
		}
		b, _ := json.MarshalIndent(found, "", "  ")
		fmt.Fprintln(a.stdout, string(b))
	} else if !*hook || len(found) > 0 {
		printSecretFindings(a, found, *hook)
	}
	if rulesErr != nil {
		// Lo que se encontró ya se mostró; sin las reglas de un proyecto el
		// escaneo no está completo y no puede dar el visto bueno.
		return fail(1, "%v", rulesErr)
	}
	if len(found) > 0 {
		return fail(1, "")
	}
	return nil
}

// scanTracked revisa archivos versionados: su nombre y su contenido.
func scanTracked(dir string, files []string, rulesFor func(string) (secrets.Rules, string)) []secrets.Finding {
	var out []secrets.Finding
	for _, f := range files {
		rules, rel := rulesFor(f)
		if rules.Allowed(rel) {
			continue
		}
		text, readable := product.WorktreeText(dir, f)
		if kind, ok := rules.Kind(rel); ok && (!readable || secrets.Confirm(kind, []byte(text))) {
			out = append(out, secrets.Finding{Path: f, Kind: "archivo de secretos (" + kind + ")"})
			continue
		}
		if !readable {
			continue
		}
		out = append(out, secrets.Scan(f, text)...)
	}
	return out
}

// ruleSet da las reglas de secretos de cada ruta de git: las del proyecto
// coyote que la contiene (su coyote/project.yaml más cercano), con la ruta
// relativa a ese proyecto; fuera de un proyecto, las de siempre.
type ruleSet struct {
	top   string
	roots map[string]string // carpeta → raíz del proyecto ("" si no hay)
	rules map[string]secrets.Rules
	err   error // el primer project.yaml que no se pudo leer
}

func newRuleSet(top string) *ruleSet {
	return &ruleSet{top: top, roots: map[string]string{}, rules: map[string]secrets.Rules{}}
}

// For devuelve las reglas para una ruta relativa a la raíz del repo y la
// ruta relativa al proyecto. Las reglas son las del proyecto más cercano,
// más los archivos que declaran como secretos los proyectos que lo
// contienen (un proyecto anidado no tapa las del de afuera); una dispensa
// vale solo si la dan todos. Un proyecto dentro de dependencias (vendor,
// node_modules) no cuenta. Un project.yaml que no se puede leer queda en err,
// con su ruta, y sus archivos se revisan con las reglas de siempre.
func (r *ruleSet) For(gitPath string) (secrets.Rules, string) {
	abs := filepath.Join(r.top, filepath.FromSlash(gitPath))
	chain := r.chain(filepath.Dir(abs))
	if len(chain) == 0 {
		return secrets.Rules{}, gitPath
	}
	relTo := func(root string) (string, bool) {
		rel, err := filepath.Rel(root, abs)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", false
		}
		return filepath.ToSlash(rel), true
	}
	near, ok := relTo(chain[0])
	if !ok {
		return secrets.Rules{}, gitPath
	}
	base := r.load(chain[0])
	rules := secrets.Rules{Files: append([]string(nil), base.Files...), Allow: base.Allow}
	for _, outer := range chain[1:] {
		rel, ok := relTo(outer)
		if !ok {
			continue
		}
		o := r.load(outer)
		if glob.Any(o.Files, rel) {
			rules.Files = append(rules.Files, near)
		}
		if rules.Allowed(near) && !o.Allowed(rel) {
			rules.Allow = nil
		}
	}
	return rules, near
}

// chain son los proyectos que contienen dir, del más cercano al de afuera,
// dentro del repo y fuera de las dependencias.
func (r *ruleSet) chain(dir string) []string {
	var out []string
	for d := dir; ; {
		root, ok := r.roots[d]
		if !ok {
			root, _ = project.FindRoot(d)
			r.roots[d] = root
		}
		if root == "" || !within(r.top, root) {
			return out
		}
		if !dependency(r.top, root) {
			out = append(out, root)
		}
		parent := filepath.Dir(root)
		if parent == root {
			return out
		}
		d = parent
	}
}

// load lee las reglas de secretos de un proyecto, una vez.
func (r *ruleSet) load(root string) secrets.Rules {
	rules, ok := r.rules[root]
	if !ok {
		if cfg, err := project.Load(root); err == nil {
			rules = cfg.Secrets
		} else if r.err == nil {
			rel, _ := filepath.Rel(r.top, root)
			r.err = fmt.Errorf("%s: %v; sus archivos se revisaron con las reglas de siempre, sin las del proyecto", filepath.ToSlash(filepath.Join(rel, project.ConfigPath)), err)
		}
		r.rules[root] = rules
	}
	return rules
}

// within dice si p está dentro de top (o es top).
func within(top, p string) bool {
	rel, err := filepath.Rel(top, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// dependency dice si un proyecto vive dentro de las dependencias de otro
// (vendor, node_modules, third_party): no es un proyecto de este repo.
func dependency(top, root string) bool {
	rel, err := filepath.Rel(top, root)
	if err != nil {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part == "vendor" || part == "node_modules" || part == "third_party" {
			return true
		}
	}
	return false
}

// scanAdded revisa lo que agrega un cambio: los archivos de secretos nuevos
// por su nombre, las líneas agregadas y, completos, los que git lista sin
// hunks (binarios, vacíos o con -diff). rulesFor da las reglas de cada ruta
// y la ruta relativa a su proyecto.
func scanAdded(files []product.AddedFile, rulesFor func(string) (secrets.Rules, string), whole func(string) (string, bool)) []secrets.Finding {
	var out []secrets.Finding
	for _, f := range files {
		rules, rel := rulesFor(f.Path)
		if rules.Allowed(rel) {
			continue
		}
		if kind, ok := rules.Kind(rel); ok {
			confirmed := true
			if secrets.NeedsContent(kind) {
				text, readable := whole(f.Path)
				confirmed = !readable || secrets.Confirm(kind, []byte(text))
			}
			if confirmed {
				out = append(out, secrets.Finding{Path: f.Path, Kind: "archivo de secretos (" + kind + ")"})
				continue
			}
		}
		// Lo que git lista sin hunks, o con hunks de texto con bytes 0 (un
		// UTF-16 marcado con diff en .gitattributes), se lee completo y
		// decodificado en la revisión nueva.
		nul := false
		for _, l := range f.Lines {
			if strings.IndexByte(l.Text, 0) >= 0 {
				nul = true
				break
			}
		}
		if f.Whole || nul {
			if text, ok := whole(f.Path); ok {
				out = append(out, secrets.Scan(f.Path, text)...)
			}
			continue
		}
		// Las líneas agregadas seguidas se revisan juntas: una llave ocupa varias.
		for i := 0; i < len(f.Lines); {
			j := i + 1
			for j < len(f.Lines) && f.Lines[j].Line == f.Lines[j-1].Line+1 {
				j++
			}
			block := make([]string, 0, j-i)
			for _, l := range f.Lines[i:j] {
				block = append(block, l.Text)
			}
			found := secrets.ScanFrom(f.Path, f.Lines[i].Line, strings.Join(block, "\n"))
			if len(found) == 0 {
				// El cuerpo de una llave pegado bajo un encabezado que ya estaba en el
				// archivo: el encabezado no es una línea agregada.
				for _, l := range f.Lines[i:j] {
					if !secrets.KeyBody(l.Text) || strings.Contains(l.Text, secrets.AllowMarker) {
						continue
					}
					if text, ok := whole(f.Path); ok && secrets.HeaderBefore(text, l.Line) {
						found = append(found, secrets.Finding{Path: f.Path, Line: l.Line, Kind: "llave privada", Hint: "BEGIN … PRIVATE KEY"})
					}
					break
				}
			}
			out = append(out, found...)
			i = j
		}
	}
	return out
}

func printSecretFindings(a *app, found []secrets.Finding, hook bool) {
	out := a.stdout
	if hook {
		out = a.stderr
	}
	if len(found) == 0 {
		fmt.Fprintln(out, "Sin secretos.")
		return
	}
	fmt.Fprintf(out, "coyote: %d %s (R18); nunca se muestra el valor:\n", len(found), pluralWord(len(found), "secreto", "secretos"))
	w := table(out)
	for _, f := range found {
		where := f.Path
		if f.Line > 0 {
			where = fmt.Sprintf("%s:%d", f.Path, f.Line)
		}
		detail := f.Kind
		if f.Hint != "" {
			detail += " (" + f.Hint + ")"
		}
		fmt.Fprintf(w, "  %s\t%s\n", where, detail)
	}
	w.Flush()
	fmt.Fprintf(out, "Muévelos a una variable de entorno o al gestor de secretos y rótalos. Un falso positivo se dispensa con %s en la línea o en secrets.allow de coyote/project.yaml.\n", secrets.AllowMarker)
}
