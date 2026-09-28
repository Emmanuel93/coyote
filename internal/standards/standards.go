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
	"path/filepath"
	"reflect"
	"strings"
	"time"
	"unicode"

	"go.yaml.in/yaml/v3"

	coyote "github.com/Emmanuel93/coyote"
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
	chain, warnings, err := resolve(root)
	if err != nil {
		return nil, err
	}
	st := &Standard{Warnings: warnings}
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

func resolve(root string) ([]layer, []string, error) {
	var warnings []string
	var chain []layer
	seen := map[string]bool{}
	cur := filepath.Join(root, "coyote", "standards", "rules.yaml")
	name := "proyecto"
	for depth := 0; depth <= 6; depth++ {
		switch cur {
		case DefaultRef:
			f, err := LoadDefault()
			if err != nil {
				return nil, warnings, err
			}
			return append([]layer{{DefaultRef, f}}, chain...), warnings, nil
		case "none":
			return chain, warnings, nil
		}
		if seen[cur] {
			return nil, warnings, fmt.Errorf("ciclo en extends: %s", cur)
		}
		seen[cur] = true
		data, err := os.ReadFile(cur)
		if err != nil {
			if os.IsNotExist(err) && depth == 0 {
				cur = DefaultRef
				continue
			}
			return nil, warnings, fmt.Errorf("estándar %s: %w", cur, err)
		}
		f, err := ParseFile(data)
		if err != nil {
			return nil, warnings, fmt.Errorf("%s: %w", cur, err)
		}
		chain = append([]layer{{name, f}}, chain...)
		next := strings.TrimSpace(f.Extends)
		switch {
		case next == "" || next == DefaultRef:
			cur = DefaultRef
		case next == "none":
			cur = "none"
		case next == "hub":
			if hub := hubRules(root); hub != "" {
				cur, name = hub, "hub"
				if inside(root, hub) {
					name = "hub dentro del repo" // lo controla el propio proyecto: no tiene la exención del hub
				}
			} else {
				warnings = append(warnings, "extends: hub sin hub local; se usa coyote:default (el hub remoto llega en v0.2)")
				cur = DefaultRef
			}
		default:
			if !filepath.IsAbs(next) {
				next = filepath.Join(filepath.Dir(cur), next)
			}
			cur, name = filepath.Clean(next), strings.TrimSpace(f.Extends)
		}
	}
	return nil, warnings, fmt.Errorf("cadena extends demasiado larga")
}

// inside informa si path queda dentro de root.
func inside(root, path string) bool {
	r, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}

// hubRules devuelve el rules.yaml del hub si project.yaml apunta a un hub local.
func hubRules(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "coyote", "project.yaml"))
	if err != nil {
		return ""
	}
	var p struct {
		Hub string `yaml:"hub"`
	}
	if yaml.Unmarshal(data, &p) != nil {
		return ""
	}
	h := strings.TrimSpace(p.Hub)
	if h == "" || strings.Contains(h, "://") || strings.HasPrefix(h, "git@") {
		return ""
	}
	if strings.HasPrefix(h, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			h = filepath.Join(home, h[2:])
		}
	}
	if !filepath.IsAbs(h) {
		h = filepath.Join(root, h)
	}
	p2 := filepath.Join(h, "coyote", "standards", "rules.yaml")
	if _, err := os.Stat(p2); err != nil {
		return ""
	}
	return p2
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
