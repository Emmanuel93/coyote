package cli

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Emmanuel93/coyote/internal/fsx"
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
// siempre si dir no está en un proyecto coyote.
func secretRules(dir string) (secrets.Rules, string) {
	root, err := project.FindRoot(dir)
	if err != nil {
		return secrets.Rules{}, ""
	}
	cfg, err := project.Load(root)
	if err != nil {
		return secrets.Rules{}, root
	}
	return cfg.Secrets, root
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
	rules, root := secretRules(dir)
	if root == "" {
		root = dir
	}
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
		kind, ok := rules.Kind(rel)
		if !ok {
			return nil
		}
		// Un .pem sin llave privada (un certificado, una llave pública) no es
		// secreto; con --no-names no se abre nada y se lista igual.
		if !*noNames {
			if _, ok := rules.KindAt(root, rel); !ok {
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
	if *asJSON {
		if list == nil {
			list = []secretEntry{}
		}
		b, _ := json.MarshalIndent(list, "", "  ")
		fmt.Fprintln(a.stdout, string(b))
		return nil
	}
	if len(list) == 0 {
		fmt.Fprintln(a.stdout, "No hay archivos de secretos en el proyecto.")
		return nil
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
	return nil
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
	rules, root := secretRules(dir)
	var found []secrets.Finding
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
			found = scanAdded(files, newRuleSet(top).For, func(p string) (string, bool) { return product.IndexText(dir, p) })
			break
		}
		files, right, err := product.AddedText(dir, *rng)
		if err != nil {
			return fail(1, "%v", err)
		}
		found = scanAdded(files, newRuleSet(top).For, func(p string) (string, bool) { return product.FileAt(dir, right, p) })
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
		found = scanTracked(dir, files, rules)
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
	if len(found) > 0 {
		return fail(1, "")
	}
	return nil
}

// scanTracked revisa archivos versionados: su nombre y su contenido.
func scanTracked(dir string, files []string, rules secrets.Rules) []secrets.Finding {
	var out []secrets.Finding
	for _, f := range files {
		if rules.Allowed(f) {
			continue
		}
		text, readable := product.WorktreeText(dir, f)
		if kind, ok := rules.Kind(f); ok && (!readable || secrets.Confirm(kind, []byte(text))) {
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
}

func newRuleSet(top string) *ruleSet {
	return &ruleSet{top: top, roots: map[string]string{}, rules: map[string]secrets.Rules{}}
}

// For devuelve las reglas para una ruta relativa a la raíz del repo y la
// ruta relativa al proyecto.
func (r *ruleSet) For(gitPath string) (secrets.Rules, string) {
	abs := filepath.Join(r.top, filepath.FromSlash(gitPath))
	dir := filepath.Dir(abs)
	root, ok := r.roots[dir]
	if !ok {
		root, _ = project.FindRoot(dir)
		r.roots[dir] = root
	}
	if root == "" {
		return secrets.Rules{}, gitPath
	}
	rules, ok := r.rules[root]
	if !ok {
		if cfg, err := project.Load(root); err == nil {
			rules = cfg.Secrets
		}
		r.rules[root] = rules
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return secrets.Rules{}, gitPath
	}
	return rules, filepath.ToSlash(rel)
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
