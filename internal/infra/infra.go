// Package infra lee y revisa la infraestructura como código de un repo
// (ADR-0017): el inventario coyote/infra.yaml (R13), el plan de Terraform en
// JSON y los comandos que aplican cambios. Nunca corre Terraform ni habla con
// una nube: lee archivos.
package infra

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/Emmanuel93/coyote/internal/fsx"
)

// Path es el inventario relativo a la raíz del repo.
const Path = "coyote/infra.yaml"

// Políticas de apply de un ambiente.
const (
	Reviewed = "reviewed" // solo la persona o un pipeline con revisor
	Local    = "local"    // una persona puede aplicar desde su terminal
)

// Budget es el presupuesto mensual de un ambiente, en dólares.
type Budget struct {
	TargetUSD float64 `yaml:"target_usd"`
	CapUSD    float64 `yaml:"cap_usd"`
}

// Environment es un ambiente: demo, prod…
type Environment struct {
	VarFile  string   `yaml:"var_file,omitempty"`
	Budget   *Budget  `yaml:"budget,omitempty"`
	Apply    string   `yaml:"apply,omitempty"`
	Match    []string `yaml:"match,omitempty"`
	Schedule string   `yaml:"schedule,omitempty"`
}

// Stack es una composición de módulos para una nube y un ambiente.
type Stack struct {
	Path   string `yaml:"path"`
	Cloud  string `yaml:"cloud"`
	Env    string `yaml:"env"`
	Status string `yaml:"status"`
}

// Inventory es coyote/infra.yaml.
type Inventory struct {
	Version      int                     `yaml:"version"`
	Tool         string                  `yaml:"tool"`
	Versions     string                  `yaml:"versions,omitempty"`
	Owners       []string                `yaml:"owners,omitempty"`
	Environments map[string]*Environment `yaml:"environments"`
	Stacks       []Stack                 `yaml:"stacks,omitempty"`
	Commands     struct {
		Apply []string `yaml:"apply,omitempty"`
	} `yaml:"commands,omitempty"`
}

var (
	envNameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	cloudRe   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	ownerRe   = regexp.MustCompile(`^(@[A-Za-z0-9][A-Za-z0-9-]*(/[A-Za-z0-9][A-Za-z0-9._-]*)?|[^@\s]+@[^@\s]+\.[^@\s]+)$`)
)

// MaxInventory es el tamaño máximo del inventario.
const MaxInventory = 1 << 20

// Load lee el inventario de root. ok es false si no existe. Un inventario
// que es un symlink, no es un archivo regular o pesa más de 1 MiB es un
// error: el gate bloquea los cambios de infraestructura hasta corregirlo.
func Load(root string) (*Inventory, bool, error) {
	data, err := fsx.ReadFile(root, Path, MaxInventory)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	inv, err := Parse(data)
	return inv, true, err
}

// Parse lee un inventario y lo valida. Un campo desconocido es un error.
func Parse(data []byte) (*Inventory, error) {
	var inv Inventory
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&inv); err != nil {
		return nil, fmt.Errorf("%s: %w", Path, err)
	}
	for _, e := range inv.Environments {
		if e != nil && e.Apply == "" {
			e.Apply = Reviewed // sin decirlo, un ambiente se aplica con revisor
		}
	}
	if errs := inv.Validate(); len(errs) > 0 {
		return &inv, fmt.Errorf("%s: %s", Path, strings.Join(errs, "; "))
	}
	return &inv, nil
}

// Topes del inventario: el gate lo lee en cada comando y no puede tardar.
const (
	maxEnvironments = 64
	maxMarks        = 32
	maxMarkLen      = 128
	maxStacks       = 256
	maxApply        = 64
	maxOwners       = 64
)

// Validate revisa la forma del inventario, sin mirar el repo.
func (inv *Inventory) Validate() []string {
	var errs []string
	switch {
	case len(inv.Environments) > maxEnvironments:
		errs = append(errs, fmt.Sprintf("environments: %d ambientes; el máximo es %d", len(inv.Environments), maxEnvironments))
	case len(inv.Stacks) > maxStacks:
		errs = append(errs, fmt.Sprintf("stacks: %d stacks; el máximo es %d", len(inv.Stacks), maxStacks))
	case len(inv.Commands.Apply) > maxApply:
		errs = append(errs, fmt.Sprintf("commands.apply: %d comandos; el máximo es %d", len(inv.Commands.Apply), maxApply))
	case len(inv.Owners) > maxOwners:
		errs = append(errs, fmt.Sprintf("owners: %d dueños; el máximo es %d", len(inv.Owners), maxOwners))
	}
	if len(errs) > 0 {
		return errs
	}
	if inv.Version != 1 {
		errs = append(errs, fmt.Sprintf("version %d no existe; usa 1", inv.Version))
	}
	if inv.Tool != "terraform" && inv.Tool != "opentofu" {
		errs = append(errs, fmt.Sprintf("tool %q: usa terraform u opentofu", inv.Tool))
	}
	if len(inv.Environments) == 0 {
		errs = append(errs, "environments: declara al menos un ambiente")
	}
	for _, name := range inv.EnvNames() {
		e := inv.Environments[name]
		if !envNameRe.MatchString(name) {
			errs = append(errs, fmt.Sprintf("environments.%s: nombre inválido (minúsculas, números, - y _)", name))
		}
		if e == nil {
			errs = append(errs, fmt.Sprintf("environments.%s está vacío", name))
			continue
		}
		if e.Apply != Reviewed && e.Apply != Local {
			errs = append(errs, fmt.Sprintf("environments.%s.apply %q: usa reviewed o local", name, e.Apply))
		}
		if e.VarFile != "" && !relPath(e.VarFile) {
			errs = append(errs, fmt.Sprintf("environments.%s.var_file %q: ruta relativa al repo, sin ..", name, e.VarFile))
		}
		if b := e.Budget; b != nil && (b.TargetUSD < 0 || b.CapUSD < 0 || (b.CapUSD > 0 && b.TargetUSD > b.CapUSD)) {
			errs = append(errs, fmt.Sprintf("environments.%s.budget: la meta no puede pasar el tope ni ser negativa", name))
		}
		if len(e.Match) > maxMarks {
			errs = append(errs, fmt.Sprintf("environments.%s.match: %d marcas; el máximo es %d", name, len(e.Match), maxMarks))
		}
		for i, m := range e.Match {
			if i == maxMarks {
				break
			}
			if strings.TrimSpace(m) == "" || len(m) < 3 || len(m) > maxMarkLen {
				errs = append(errs, fmt.Sprintf("environments.%s.match: marca vacía, de menos de 3 caracteres o de más de %d %q", name, maxMarkLen, m))
			}
		}
	}
	seen := map[string]bool{}
	for i, s := range inv.Stacks {
		where := fmt.Sprintf("stacks[%d]", i)
		if !relPath(s.Path) {
			errs = append(errs, fmt.Sprintf("%s.path %q: ruta relativa al repo, sin ..", where, s.Path))
		}
		if seen[s.Path] {
			errs = append(errs, fmt.Sprintf("%s.path %q repetido", where, s.Path))
		}
		seen[s.Path] = true
		if !cloudRe.MatchString(s.Cloud) {
			errs = append(errs, fmt.Sprintf("%s.cloud %q inválido (gcp, aws, azure…)", where, s.Cloud))
		}
		if inv.Environments[s.Env] == nil {
			errs = append(errs, fmt.Sprintf("%s.env %q no está en environments", where, s.Env))
		}
		if s.Status != "active" && s.Status != "scaffold" {
			errs = append(errs, fmt.Sprintf("%s.status %q: usa active o scaffold", where, s.Status))
		}
	}
	for _, o := range inv.Owners {
		if !ownerRe.MatchString(o) {
			errs = append(errs, fmt.Sprintf("owners: %q no es @persona, @org/equipo ni un correo", o))
		}
	}
	for _, c := range inv.Commands.Apply {
		if len(strings.Fields(c)) == 0 {
			errs = append(errs, "commands.apply: comando vacío")
		}
	}
	if inv.Versions != "" && !relPath(inv.Versions) {
		errs = append(errs, fmt.Sprintf("versions %q: ruta relativa al repo, sin ..", inv.Versions))
	}
	return errs
}

// EnvNames devuelve los ambientes en orden.
func (inv *Inventory) EnvNames() []string {
	names := make([]string, 0, len(inv.Environments))
	for n := range inv.Environments {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func relPath(p string) bool {
	p = filepath.ToSlash(strings.TrimSpace(p))
	return p != "" && !strings.HasPrefix(p, "/") && !strings.Contains(p, "..") && !strings.HasPrefix(p, "~")
}
