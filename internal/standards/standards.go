// Package standards carga el estándar por capas (default de la herramienta,
// hub de la organización, proyecto) y lo valida con checks declarativos.
// La especificación está en docs/specs/standards-v1.md.
package standards

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"time"
	"unicode"

	"go.yaml.in/yaml/v3"

	coyote "github.com/Emmanuel93/coyote"

	"github.com/Emmanuel93/coyote/internal/fsx"
	"github.com/Emmanuel93/coyote/internal/hub"
	"github.com/Emmanuel93/coyote/internal/yamlx"
)

// DefaultRef es el nombre del estándar que trae la herramienta.
const DefaultRef = "coyote:default"

var levelRank = map[string]int{"MAY": 1, "SHOULD": 2, "MUST": 3}

// Rank devuelve el peso de un nivel (MUST > SHOULD > MAY).
func Rank(level string) int { return levelRank[level] }

// Check es una verificación declarativa de una regla.
type Check struct {
	Type    string   `yaml:"type"`
	Files   []string `yaml:"files,omitempty"`
	Paths   []string `yaml:"paths,omitempty"`
	Except  []string `yaml:"except,omitempty"`
	Pattern string   `yaml:"pattern,omitempty"`
	Max     int      `yaml:"max,omitempty"`
	Commits int      `yaml:"commits,omitempty"`
	Script  string   `yaml:"script,omitempty"`
}

// Override ajusta una regla de una capa anterior.
type Override struct {
	Level    string   `yaml:"level,omitempty"`
	Until    string   `yaml:"until,omitempty"`
	Reason   string   `yaml:"reason,omitempty"`
	ADR      string   `yaml:"adr,omitempty"`
	Disabled bool     `yaml:"disabled,omitempty"`
	Except   []string `yaml:"except,omitempty"` // rutas que los checks de la regla dejan fuera
}

// Rule es una regla del estándar.
type Rule struct {
	ID       string    `yaml:"id"`
	Title    string    `yaml:"title,omitempty"`
	Level    string    `yaml:"level,omitempty"`
	Scope    string    `yaml:"scope,omitempty"`
	Profiles []string  `yaml:"profiles,omitempty"`
	Why      string    `yaml:"why,omitempty"`
	Fix      string    `yaml:"fix,omitempty"`
	Check    *Check    `yaml:"check,omitempty"`
	Checks   []Check   `yaml:"checks,omitempty"`
	Override *Override `yaml:"override,omitempty"`
	Reason   string    `yaml:"reason,omitempty"` // obligatorio si una redefinición cambia checks o perfiles de una MUST

	Source   string `yaml:"-"` // capa que la definió
	Origin   string `yaml:"-"` // nivel antes de los ajustes
	Note     string `yaml:"-"` // ajuste aplicado
	Disabled bool   `yaml:"-"`
}

// AllChecks devuelve check y checks juntos.
func (r *Rule) AllChecks() []Check {
	var out []Check
	if r.Check != nil {
		out = append(out, *r.Check)
	}
	return append(out, r.Checks...)
}

// AppliesTo informa si la regla aplica al perfil del proyecto.
func (r *Rule) AppliesTo(profile string) bool {
	if len(r.Profiles) == 0 {
		return true
	}
	for _, p := range r.Profiles {
		if p == profile {
			return true
		}
	}
	return false
}

// Language es el idioma de docs y de código.
type Language struct {
	Docs string `yaml:"docs,omitempty"`
	Code string `yaml:"code,omitempty"`
}

// File es el contenido de un rules.yaml.
type File struct {
	Version  int      `yaml:"version"`
	Name     string   `yaml:"name,omitempty"`
	Extends  string   `yaml:"extends,omitempty"`
	Reason   string   `yaml:"reason,omitempty"` // obligatorio con extends: none
	Language Language `yaml:"language,omitempty"`
	Rules    []Rule   `yaml:"rules"`
}

// Standard es el estándar fusionado de todas las capas.
type Standard struct {
	Layers   []string
	Rules    []*Rule
	Language Language
	Warnings []string
	// Detached indica que la cadena no parte de coyote:default (extends: none);
	// DetachReason es el motivo declarado. Sin motivo, el lint falla (S0).
	Detached     bool
	DetachReason string
	// Unjustified son reglas MUST que una capa intentó redefinir con otros checks
	// o perfiles sin reason; se mantiene la definición anterior y el lint falla (S1).
	Unjustified []string
	// Hub es el hub que rige, con su commit; nil si el estándar no lo usa.
	Hub *hub.Hub
	// HubSkipped indica que coyote/project.yaml declara un hub y la cadena no
	// pasa por él; HubSkipReason es el motivo declarado. Sin motivo, las
	// reglas de la organización no rigen y el lint falla (S2).
	HubSkipped    bool
	HubSkipReason string
}

// placeholders son motivos de relleno que no cuentan como motivo.
var placeholders = map[string]bool{"todo": true, "tbd": true, "xxx": true, "n/a": true, "na": true, "none": true,
	"ninguno": true, "ninguna": true, "pendiente": true, "test": true, "prueba": true, "wip": true, "fixme": true,
	"asdf": true, "sin motivo": true, "por definir": true, "temporal": true, "temp": true, "nada": true, "motivo": true, "reason": true}

// Meaningful informa si un motivo dice algo: al menos tres letras o dígitos y
// no es un marcador de relleno como TODO, xxx o n/a.
func Meaningful(reason string) bool {
	t := strings.ToLower(strings.Trim(strings.TrimSpace(reason), ".-_:;!¡?¿*#()[]{}\"'`"))
	if placeholders[t] {
		return false
	}
	n := 0
	for _, r := range t {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			n++
		}
	}
	return n >= 3
}

// Find devuelve la regla con ese id o nil.
func (s *Standard) Find(id string) *Rule {
	for _, r := range s.Rules {
		if strings.EqualFold(r.ID, id) {
			return r
		}
	}
	return nil
}

func (s *Standard) warn(format string, a ...any) {
	s.Warnings = append(s.Warnings, fmt.Sprintf(format, a...))
}

// ParseFile interpreta un rules.yaml.
// Una clave desconocida (por ejemplo Override: en vez de override:) es un
// error: de otro modo la regla se redefiniría sin que nadie lo notara.
func ParseFile(data []byte) (*File, error) {
	// Un solo documento, sin anclas ni alias: un segundo documento se
	// ignoraría sin aviso, con las reglas que traiga.
	if err := yamlx.Check(data); err != nil {
		return nil, err
	}
	var f File
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return &f, nil
}

// LoadDefault carga el estándar default embebido.
func LoadDefault() (*File, error) {
	data, err := fs.ReadFile(coyote.Standards, "standards/default/rules.yaml")
	if err != nil {
		return nil, err
	}
	return ParseFile(data)
}

// Load resuelve la cadena extends desde coyote/standards/rules.yaml del proyecto.
func Load(root string, now time.Time) (*Standard, error) {
	chain, warnings, h, err := resolve(root)
	if err != nil {
		return nil, err
	}
	st := &Standard{Warnings: warnings, Hub: h}
	// Un proyecto que declara hub lo tiene en su cadena: sin esa capa, las
	// reglas de la organización no rigen (ADR-0018).
	if ref, err := hub.FromProject(root); err == nil && !ref.Empty() && h == nil {
		st.HubSkipped = true
		for _, l := range chain {
			if l.name == "proyecto" && Meaningful(l.file.Reason) {
				st.HubSkipReason = strings.TrimSpace(l.file.Reason)
			}
		}
	}
	if len(chain) == 0 || chain[0].name != DefaultRef {
		st.Detached = true
		for _, l := range chain {
			if strings.TrimSpace(l.file.Extends) == "none" && Meaningful(l.file.Reason) {
				st.DetachReason = strings.TrimSpace(l.file.Reason)
			}
		}
		if st.DetachReason == "" {
			st.warn("el estándar no parte de coyote:default (extends: none) y no declara reason")
		}
	}
	for _, l := range chain {
		st.apply(l, now)
	}
	return st, nil
}

// Chain describe las capas para mostrarlas; la primera del hub lleva su
// organización, ref y commit.
func (s *Standard) Chain() string {
	parts := make([]string, 0, len(s.Layers))
	shown := false
	for _, l := range s.Layers {
		if s.Hub != nil && !shown && strings.HasPrefix(l, "hub") {
			l += " " + s.Hub.String()
			shown = true
		}
		parts = append(parts, l)
	}
	return strings.Join(parts, " → ")
}

// Dropped devuelve las reglas del default que no están en un estándar desprendido.
func (s *Standard) Dropped() []*Rule {
	if !s.Detached {
		return nil
	}
	f, err := LoadDefault()
	if err != nil {
		return nil
	}
	var out []*Rule
	for i := range f.Rules {
		if s.Find(f.Rules[i].ID) == nil {
			out = append(out, &f.Rules[i])
		}
	}
	return out
}

type layer struct {
	name string
	file *File
}

// source es de dónde sale una capa: un archivo del disco o un archivo del
// commit que rige en el hub.
type source struct {
	hub  *hub.Hub
	path string // ruta absoluta en disco, o relativa dentro del hub
}

func (s source) key() string {
	if s.hub != nil {
		return "hub:" + s.hub.Commit + ":" + s.path
	}
	return s.path
}

func (s source) String() string {
	if s.hub != nil {
		return s.path + " del hub " + s.hub.String()
	}
	return s.path
}

func (s source) read() ([]byte, error) {
	if s.hub != nil {
		return s.hub.Read(s.path)
	}
	return fsx.ReadCapped(s.path, fsx.MaxText)
}

func (s source) missing(err error) bool {
	if s.hub != nil {
		return errors.Is(err, hub.ErrNotFound)
	}
	return os.IsNotExist(err)
}

func resolve(root string) ([]layer, []string, *hub.Hub, error) {
	var warnings []string
	var chain []layer
	var h *hub.Hub
	// El clon del hub declarado: un extends que lo lee del disco tomaría su
	// árbol de trabajo, con cambios sin commit.
	hubDir := ""
	if ref, err := hub.FromProject(root); err == nil && !ref.Empty() {
		hubDir = ref.Dir(root)
	}
	seen := map[string]bool{}
	cur := source{path: filepath.Join(root, "coyote", "standards", "rules.yaml")}
	name := "proyecto"
	for depth := 0; depth <= 6; depth++ {
		switch cur.path {
		case DefaultRef:
			f, err := LoadDefault()
			if err != nil {
				return nil, warnings, h, err
			}
			return append([]layer{{DefaultRef, f}}, chain...), warnings, h, nil
		case "none":
			return chain, warnings, h, nil
		}
		if seen[cur.key()] {
			return nil, warnings, h, fmt.Errorf("ciclo en extends: %s", cur)
		}
		seen[cur.key()] = true
		data, err := cur.read()
		if err != nil {
			switch {
			case cur.missing(err) && depth == 0:
				cur = source{path: DefaultRef}
				continue
			case cur.missing(err) && cur.hub != nil && cur.path == hub.StandardsFile:
				warnings = append(warnings, fmt.Sprintf("el hub %s no tiene %s; debajo del proyecto rige coyote:default", cur.hub, hub.StandardsFile))
				cur = source{path: DefaultRef}
				continue
			}
			return nil, warnings, h, fmt.Errorf("estándar %s: %w", cur, err)
		}
		f, err := ParseFile(data)
		if err != nil {
			return nil, warnings, h, fmt.Errorf("%s: %w", cur, err)
		}
		chain = append([]layer{{name, f}}, chain...)
		next := strings.TrimSpace(f.Extends)
		switch {
		case next == "" || next == DefaultRef:
			cur = source{path: DefaultRef}
		case next == "none":
			cur = source{path: "none"}
		case next == "hub":
			if cur.hub != nil {
				return nil, warnings, h, fmt.Errorf("%s: el hub no puede extender otro hub", cur)
			}
			ref, err := hub.FromProject(root)
			if err != nil {
				return nil, warnings, h, err
			}
			if h, err = hub.Open(root, ref); err != nil {
				return nil, warnings, nil, err
			}
			if h == nil {
				warnings = append(warnings, "extends: hub, pero coyote/project.yaml no declara hub; rige coyote:default")
				cur = source{path: DefaultRef}
				continue
			}
			cur, name = source{hub: h, path: hub.StandardsFile}, "hub"
			if hub.Inside(root, h.Dir) {
				name = "hub dentro del repo" // lo controla el propio proyecto: no tiene la exención del hub
			}
		case cur.hub != nil:
			// Dentro del hub, un extends relativo se lee del mismo commit y no
			// sale del repo: la capa de la organización no depende de la
			// máquina de nadie.
			if strings.HasPrefix(next, "/") || filepath.IsAbs(next) {
				return nil, warnings, h, fmt.Errorf("%s: extends %q es una ruta absoluta; dentro del hub va relativa", cur, next)
			}
			rel, err := hub.CleanRel(path.Join(path.Dir(cur.path), filepath.ToSlash(next)))
			if err != nil {
				return nil, warnings, h, fmt.Errorf("%s: extends %q sale del hub", cur, next)
			}
			cur = source{hub: cur.hub, path: rel}
		default:
			if !filepath.IsAbs(next) {
				next = filepath.Join(filepath.Dir(cur.path), next)
			}
			if hubDir != "" && hub.Inside(hubDir, next) {
				return nil, warnings, h, fmt.Errorf("%s: extends %q lee el clon del hub del disco, con lo que tenga sin commit; usa extends: hub, que lee el commit que rige (ADR-0018)", cur, strings.TrimSpace(f.Extends))
			}
			cur, name = source{path: filepath.Clean(next)}, strings.TrimSpace(f.Extends)
		}
	}
	return nil, warnings, h, fmt.Errorf("cadena extends demasiado larga")
}

func (s *Standard) apply(l layer, now time.Time) {
	s.Layers = append(s.Layers, l.name)
	if l.file.Language.Docs != "" {
		s.Language.Docs = l.file.Language.Docs
	}
	if l.file.Language.Code != "" {
		s.Language.Code = l.file.Language.Code
	}
	for _, r := range l.file.Rules {
		r.ID = strings.TrimSpace(r.ID)
		r.Level = strings.ToUpper(strings.TrimSpace(r.Level))
		if r.ID == "" {
			s.warn("%s: regla sin id ignorada", l.name)
			continue
		}
		existing := s.Find(r.ID)
		if existing == nil {
			if r.Override != nil && r.Title == "" {
				s.warn("%s: ajuste de %s, que no existe en las capas anteriores", l.name, r.ID)
				continue
			}
			if r.Level == "" {
				r.Level = "SHOULD"
			}
			if _, ok := levelRank[r.Level]; !ok {
				s.warn("%s: %s tiene nivel inválido; se usa SHOULD", l.name, r.ID)
				r.Level = "SHOULD"
			}
			r.Source, r.Origin, r.Override = l.name, r.Level, nil
			nr := r
			s.Rules = append(s.Rules, &nr)
			continue
		}
		if r.Override != nil {
			s.override(existing, r.Override, l.name, now)
			continue
		}
		if r.Level == "" {
			r.Level = existing.Level
		}
		if levelRank[r.Level] < levelRank[existing.Level] {
			s.warn("%s: %s no puede bajar de nivel al redefinirse; usa override con reason", l.name, r.ID)
			r.Level = existing.Level
		}
		// Redefinir es legítimo (un hub adapta el default), pero queda visible en
		// standards show y diff para que nadie debilite un check sin que se note.
		// Lo que la redefinición no declara se hereda, incluidos los checks: para
		// quitarlos hay que desactivar la regla con override y reason.
		if r.Title == "" {
			r.Title = existing.Title
		}
		if r.Scope == "" {
			r.Scope = existing.Scope
		}
		if r.Why == "" {
			r.Why = existing.Why
		}
		if r.Fix == "" {
			r.Fix = existing.Fix
		}
		if len(r.Profiles) == 0 {
			r.Profiles = existing.Profiles
		}
		if len(r.AllChecks()) == 0 {
			r.Check, r.Checks = existing.Check, existing.Checks
		}
		// Cambiar los checks o los perfiles de una MUST puede vaciarla (un check
		// trivial, profiles: [nadie]); eso es relajarla y exige un motivo real. El
		// hub de la organización queda exento solo si vive fuera del repo.
		weakens := existing.Level == "MUST" &&
			(!reflect.DeepEqual(r.AllChecks(), existing.AllChecks()) || !reflect.DeepEqual(r.Profiles, existing.Profiles))
		if weakens && l.name != "hub" && !Meaningful(r.Reason) {
			s.Unjustified = append(s.Unjustified, r.ID)
			s.warn("%s: redefine %s (MUST de %s) con otros checks o perfiles sin reason; rige la definición anterior", l.name, r.ID, existing.Source)
			continue
		}
		r.Source, r.Origin = l.name, existing.Origin
		r.Note = fmt.Sprintf("redefinida en %s sobre %s", l.name, existing.Source)
		if Meaningful(r.Reason) {
			r.Note += "; motivo: " + strings.TrimSpace(r.Reason)
		}
		if existing.Level == "MUST" && l.name != "hub" {
			s.warn("%s: redefine %s (MUST de %s); revísalo con coyote standards diff", l.name, r.ID, existing.Source)
		}
		*existing = r
	}
}

func (s *Standard) override(r *Rule, o *Override, layer string, now time.Time) {
	if o.Until != "" {
		until, err := time.Parse("2006-01-02", o.Until)
		if err != nil {
			s.warn("%s: %s tiene until inválido %q", layer, r.ID, o.Until)
			return
		}
		if now.After(until.Add(24 * time.Hour)) {
			s.warn("%s: el ajuste de %s venció el %s; rige %s", layer, r.ID, o.Until, r.Level)
			return
		}
	}
	level := strings.ToUpper(strings.TrimSpace(o.Level))
	if level != "" {
		if _, ok := levelRank[level]; !ok {
			s.warn("%s: %s tiene nivel inválido %q en el ajuste", layer, r.ID, o.Level)
			return
		}
	}
	relax := o.Disabled || len(o.Except) > 0 || (level != "" && levelRank[level] < levelRank[r.Level])
	if relax && !Meaningful(o.Reason) {
		s.warn("%s: %s se relaja sin un reason real; el ajuste no se aplica", layer, r.ID)
		return
	}
	before := r.Level
	if o.Disabled {
		r.Disabled = true
	}
	if level != "" {
		r.Level = level
	}
	if len(o.Except) > 0 {
		if r.Check != nil {
			c := *r.Check
			c.Except = append(append([]string{}, c.Except...), o.Except...)
			r.Check = &c
		}
		checks := make([]Check, len(r.Checks))
		for i, c := range r.Checks {
			c.Except = append(append([]string{}, c.Except...), o.Except...)
			checks[i] = c
		}
		r.Checks = checks
	}
	note := fmt.Sprintf("ajustada en %s: %s → %s", layer, before, r.Level)
	if level == "" || level == before {
		note = fmt.Sprintf("ajustada en %s", layer)
	}
	if len(o.Except) > 0 {
		note += "; excluye " + strings.Join(o.Except, ", ")
	}
	if o.Disabled {
		note = fmt.Sprintf("desactivada en %s", layer)
	}
	if o.Until != "" {
		note += " hasta " + o.Until
	}
	if o.Reason != "" {
		note += "; motivo: " + o.Reason
	}
	if o.ADR != "" {
		note += "; " + o.ADR
	}
	r.Note = note
}
