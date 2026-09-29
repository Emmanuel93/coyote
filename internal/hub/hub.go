// Package hub lee el hub de la organización (ADR-0018): un proyecto coyote de
// tipo hub con coyote/hub.yaml y la capa del estándar de la organización. Se
// lee del commit de su ref con git de plomería, nunca del árbol de trabajo:
// lo que no tiene commit no rige.
package hub

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/Emmanuel93/coyote/internal/fsx"
	"github.com/Emmanuel93/coyote/internal/product"
)

// Rutas dentro del hub y la rama que rige si el proyecto no fija otra.
const (
	ConfFile      = "coyote/hub.yaml"
	StandardsFile = "coyote/standards/rules.yaml"
	DomainsDir    = "domains"
	DefaultRef    = "main"
	// MaxFile es el tope de un archivo del hub: más que cualquier
	// configuración legítima y poco para un objeto que no debería estar ahí.
	MaxFile = 1 << 20
)

// ErrNotFound indica que el archivo no está en el commit del hub.
var ErrNotFound = errors.New("no está en el commit del hub")

// Ref es la referencia al hub en project.yaml: una ruta, o {path, ref}.
type Ref struct {
	Path string `yaml:"path"`
	Ref  string `yaml:"ref,omitempty"`
}

// UnmarshalYAML acepta una cadena (la ruta del clon) o {path, ref}.
func (r *Ref) UnmarshalYAML(n *yaml.Node) error {
	*r = Ref{}
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Tag == "!!null" {
			return nil
		}
		var s string
		if err := n.Decode(&s); err != nil {
			return err
		}
		r.Path = strings.TrimSpace(s)
		return nil
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			switch k := n.Content[i].Value; k {
			case "path", "ref":
				if n.Content[i+1].Kind != yaml.ScalarNode {
					return fmt.Errorf("hub.%s: va una cadena", k)
				}
			default:
				return fmt.Errorf("hub: clave desconocida %q; usa path y ref", k)
			}
		}
		type plain Ref
		var p plain
		if err := n.Decode(&p); err != nil {
			return err
		}
		r.Path, r.Ref = strings.TrimSpace(p.Path), strings.TrimSpace(p.Ref)
		return nil
	}
	return errors.New("hub: va la ruta del clon o {path, ref}")
}

// MarshalYAML escribe una cadena si no hay ref.
func (r Ref) MarshalYAML() (any, error) {
	if r.Ref == "" {
		return r.Path, nil
	}
	return struct {
		Path string `yaml:"path"`
		Ref  string `yaml:"ref"`
	}{r.Path, r.Ref}, nil
}

// Empty informa si el proyecto no declara hub.
func (r Ref) Empty() bool { return r.Path == "" && r.Ref == "" }

// refRe acepta ramas, etiquetas y commits: sin opciones, rangos ni sufijos
// de revisión que cambien lo que se lee.
var refRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$`)

// Validate revisa la referencia sin tocar el disco.
func (r Ref) Validate() error {
	switch {
	case r.Empty():
		return nil
	case r.Path == "":
		return errors.New("hub: ref sin path; path es la ruta del clon del hub")
	case strings.Contains(r.Path, "://") || strings.HasPrefix(r.Path, "git@"):
		return fmt.Errorf("hub: %q es una URL; clona el hub y pon en path la ruta del clon (y en ref la rama o el commit)", r.Path)
	case r.Ref != "" && (!refRe.MatchString(r.Ref) || strings.Contains(r.Ref, "..") || strings.HasSuffix(r.Ref, "/") || strings.HasSuffix(r.Ref, ".lock")):
		return fmt.Errorf("hub: ref inválido %q; usa una rama, una etiqueta o un commit", r.Ref)
	}
	return nil
}

// Dir devuelve la carpeta del clon: relativa al proyecto o con ~.
func (r Ref) Dir(root string) string {
	p := r.Path
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
		}
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	return filepath.Clean(p)
}

// FromProject lee solo la clave hub de coyote/project.yaml. Sirve a quien no
// puede cargar la configuración completa del proyecto (el estándar).
func FromProject(root string) (Ref, error) {
	data, err := fsx.ReadCapped(filepath.Join(root, "coyote", "project.yaml"), fsx.MaxText)
	if err != nil {
		if os.IsNotExist(err) {
			return Ref{}, nil
		}
		return Ref{}, err
	}
	var p struct {
		Hub Ref `yaml:"hub"`
	}
	if err := yaml.Unmarshal(data, &p); err != nil {
		return Ref{}, fmt.Errorf("coyote/project.yaml: %w", err)
	}
	return p.Hub, p.Hub.Validate()
}

// Hub es un hub resuelto en un commit.
type Hub struct {
	Dir    string // clon local
	Ref    string // la rama, etiqueta o commit que rige
	Commit string // el commit que rige
	Conf   *Conf  // coyote/hub.yaml en ese commit
	// Missing indica que el commit no tiene coyote/hub.yaml: sin admins,
	// presupuesto ni proyectos.
	Missing bool
}

// Short devuelve el commit abreviado.
func (h *Hub) Short() string {
	if len(h.Commit) > 7 {
		return h.Commit[:7]
	}
	return h.Commit
}

// String describe qué rige: la organización, la ref y el commit.
func (h *Hub) String() string {
	org := h.Conf.Org
	if org == "" {
		org = filepath.Base(h.Dir)
	}
	return fmt.Sprintf("%s (%s@%s)", org, h.Ref, h.Short())
}

// Open resuelve el hub de un proyecto. Devuelve nil sin error si el proyecto
// no declara hub. Un hub declarado que no se puede leer es un error: el
// estándar no vuelve en silencio a coyote:default.
func Open(root string, r Ref) (*Hub, error) {
	if r.Empty() {
		return nil, nil
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	dir := r.Dir(root)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("hub: no encuentro el clon en %s; clónalo ahí o corrige hub en coyote/project.yaml", dir)
	}
	if !isTopLevel(dir) {
		return nil, fmt.Errorf("hub: %s no es la raíz de un repo git; el hub se lee de sus commits", dir)
	}
	ref := r.Ref
	if ref == "" {
		ref = DefaultRef
	}
	commit, err := resolve(dir, ref)
	if err != nil {
		return nil, fmt.Errorf("hub: %s no tiene %q (%v); haz commit en el hub o fija otra ref en coyote/project.yaml", dir, ref, err)
	}
	h := &Hub{Dir: dir, Ref: ref, Commit: commit, Conf: &Conf{}}
	data, err := h.Read(ConfFile)
	switch {
	case errors.Is(err, ErrNotFound):
		h.Missing = true
	case err != nil:
		return nil, err
	default:
		c, err := Parse(data)
		if err != nil {
			return nil, fmt.Errorf("hub %s: %s: %w", h, ConfFile, err)
		}
		h.Conf = c
	}
	return h, nil
}

// isTopLevel informa si dir es la raíz de su propio repo: una carpeta dentro
// de otro repo leería los commits de ese otro.
func isTopLevel(dir string) bool {
	out, err := product.GitRead(dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return false
	}
	top := strings.TrimSpace(string(out))
	a, err1 := filepath.EvalSymlinks(top)
	b, err2 := filepath.EvalSymlinks(dir)
	return err1 == nil && err2 == nil && a == b
}

var shaRe = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

func resolve(dir, ref string) (string, error) {
	var stderr bytes.Buffer
	cmd := product.GitRead(dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	sha := strings.TrimSpace(string(out))
	if err != nil || !shaRe.MatchString(sha) {
		return "", errors.New("no es una rama, etiqueta ni commit de ese repo")
	}
	return sha, nil
}

// CleanRel valida una ruta dentro del hub: relativa, con barras y sin salir
// del repo.
func CleanRel(rel string) (string, error) {
	c := path.Clean(strings.ReplaceAll(strings.TrimSpace(rel), `\`, "/"))
	if c == "." || c == "" || strings.HasPrefix(c, "/") || c == ".." || strings.HasPrefix(c, "../") || strings.ContainsAny(c, "\x00\n:") {
		return "", fmt.Errorf("ruta %q fuera del hub", rel)
	}
	return c, nil
}

// Read lee un archivo del commit que rige. Un symlink, una carpeta o un
// archivo de más de MaxFile no se leen.
func (h *Hub) Read(rel string) ([]byte, error) {
	rel, err := CleanRel(rel)
	if err != nil {
		return nil, err
	}
	out, err := product.GitRead(h.Dir, "ls-tree", "-l", "-z", h.Commit, "--", rel).Output()
	if err != nil {
		return nil, fmt.Errorf("hub %s: no puedo listar %s: %w", h, rel, err)
	}
	entry := strings.TrimSuffix(string(out), "\x00")
	if entry == "" {
		return nil, fmt.Errorf("%s %w", rel, ErrNotFound)
	}
	meta, name, ok := strings.Cut(entry, "\t")
	f := strings.Fields(meta)
	if !ok || name != rel || len(f) != 4 {
		return nil, fmt.Errorf("%s %w", rel, ErrNotFound)
	}
	mode, typ, sha, size := f[0], f[1], f[2], f[3]
	switch {
	case typ != "blob":
		return nil, fmt.Errorf("hub %s: %s no es un archivo", h, rel)
	case mode == "120000":
		return nil, fmt.Errorf("hub %s: %s es un symlink; el hub no sigue enlaces", h, rel)
	}
	n, err := strconv.ParseInt(size, 10, 64)
	if err != nil || n > MaxFile {
		return nil, fmt.Errorf("hub %s: %s pesa más de %d bytes", h, rel, MaxFile)
	}
	cmd := product.GitRead(h.Dir, "cat-file", "blob", sha)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	data, rerr := io.ReadAll(io.LimitReader(pipe, MaxFile+1))
	werr := cmd.Wait()
	if rerr != nil || werr != nil || int64(len(data)) > MaxFile {
		return nil, fmt.Errorf("hub %s: no puedo leer %s", h, rel)
	}
	return data, nil
}

// ProjectDir devuelve la carpeta del clon de un proyecto de la organización:
// relativa al hub o con ~; "" si no la declara.
func (h *Hub) ProjectDir(p Project) string {
	if strings.TrimSpace(p.Path) == "" {
		return ""
	}
	return Ref{Path: p.Path}.Dir(h.Dir)
}

// Inside informa si el clon del hub vive dentro de root: un hub así lo
// controla el propio proyecto y no tiene la exención de la capa del hub.
func Inside(root, dir string) bool {
	r, err := filepath.Rel(filepath.Clean(root), filepath.Clean(dir))
	return err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}
