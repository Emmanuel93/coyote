// Package workstream lee el plan de un workstream (plan.yaml), valida el
// contrato de cada paso (A2) y deriva su estado del ledger (ADR-0014): qué
// pasos corrieron, cuáles esperan la revisión de la persona y cuál sigue.
// El estado no vive en otro archivo: el ledger ya es la fuente de verdad y se
// comparte con el equipo en cada pull.
package workstream

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/Emmanuel93/coyote/internal/fsx"
)

// Base es la carpeta de los workstreams, relativa a la raíz del proyecto.
const Base = "coyote/workstreams"

// Human es el agente de un paso que hace la persona.
const Human = "persona"

// Modes van de más a menos puntos de control. El modo cambia cuándo se
// detiene el motor, nunca lo que el gate deja pasar.
var Modes = []string{"manual", "supervised", "autonomous"}

// Límites de un paso y de un plan.
const (
	MaxStepUSD  = 1000.0
	MaxPlanUSD  = 10000.0
	MaxTurns    = 500
	maxSections = 12
)

var (
	// IDRe es el id de un workstream: W-0001.
	IDRe      = regexp.MustCompile(`^W-\d{4}$`)
	dirRe     = regexp.MustCompile(`^W-\d{4}(-[A-Za-z0-9._-]+)?$`)
	stepIDRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$`)
	repoRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	riskRe    = regexp.MustCompile(`^R[123]$`)
	agentRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	errNoPlan = errors.New("sin plan")
)

// List acepta un texto o una lista de textos: output: internal/ci y
// output: [docs/a.md, docs/b.md] valen igual.
type List []string

// UnmarshalYAML implementa yaml.Unmarshaler.
func (l *List) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Tag == "!!null" {
			*l = nil
			return nil
		}
		*l = List{n.Value}
		return nil
	case yaml.SequenceNode:
		out := make(List, 0, len(n.Content))
		for _, c := range n.Content {
			if c.Kind != yaml.ScalarNode {
				return fmt.Errorf("línea %d: se esperaba un texto", c.Line)
			}
			out = append(out, c.Value)
		}
		*l = out
		return nil
	}
	return fmt.Errorf("línea %d: se esperaba un texto o una lista de textos", n.Line)
}

// Step es un paso del plan con su contrato (A2).
type Step struct {
	ID       string   `yaml:"id" json:"id"`
	Does     string   `yaml:"does" json:"does"`
	Task     string   `yaml:"task,omitempty" json:"task,omitempty"`
	Agent    string   `yaml:"agent" json:"agent"`
	Input    List     `yaml:"input,omitempty" json:"input,omitempty"`
	Output   List     `yaml:"output,omitempty" json:"output,omitempty"`
	Sections []string `yaml:"sections,omitempty" json:"sections,omitempty"`
	Risk     string   `yaml:"risk,omitempty" json:"risk,omitempty"`
	Scope    string   `yaml:"scope,omitempty" json:"scope,omitempty"`
	MaxUSD   float64  `yaml:"max_usd,omitempty" json:"max_usd,omitempty"`
	MaxTurns int      `yaml:"max_turns,omitempty" json:"max_turns,omitempty"`
	Gate     string   `yaml:"gate,omitempty" json:"gate,omitempty"`
}

// IsHuman informa si el paso lo hace la persona.
func (s Step) IsHuman() bool { return s.Agent == Human }

// Plan es plan.yaml de un workstream.
type Plan struct {
	ID           string            `yaml:"id"`
	Title        string            `yaml:"title"`
	Owner        string            `yaml:"owner"`
	Autonomy     string            `yaml:"autonomy"`
	Risk         string            `yaml:"risk"`
	BudgetUSD    float64           `yaml:"budget_usd"`
	Reasoning    string            `yaml:"reasoning"`
	AuthorizedBy string            `yaml:"authorized_by"`
	Gate         string            `yaml:"gate"` // gate de release que cierra el plan (G5); informativo
	Steps        []Step            `yaml:"steps"`
	Close        map[string]string `yaml:"close"`

	Dir string `yaml:"-"` // carpeta del workstream, relativa a la raíz
	Rel string `yaml:"-"` // plan.yaml, relativo a la raíz
	Raw []byte `yaml:"-"`
}

// Find busca la carpeta de un workstream: coyote/workstreams/W-0004 o
// W-0004-algo. Devuelve la ruta relativa a la raíz.
func Find(root, id string) (string, error) {
	if !IDRe.MatchString(id) {
		return "", fmt.Errorf("workstream %q inválido: usa W-0001", id)
	}
	entries, _ := os.ReadDir(filepath.Join(root, filepath.FromSlash(Base)))
	for _, e := range entries {
		if e.IsDir() && (e.Name() == id || strings.HasPrefix(e.Name(), id+"-")) {
			if !dirRe.MatchString(e.Name()) {
				// Las rutas de los artefactos van al ledger, que no admite espacios.
				return "", fmt.Errorf("la carpeta %q no sirve para un workstream: usa %s-nombre con letras, números, punto, guion y guion bajo", e.Name(), id)
			}
			return Base + "/" + e.Name(), nil
		}
	}
	return "", fmt.Errorf("no existe el workstream %s en %s/", id, Base)
}

// Load lee plan.yaml de un workstream.
func Load(root, id string) (*Plan, error) {
	dir, err := Find(root, id)
	if err != nil {
		return nil, err
	}
	rel := dir + "/plan.yaml"
	if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); os.IsNotExist(err) {
		return nil, fmt.Errorf("%s no tiene plan: escribe %s (docs/specs/workstream-v1.md): %w", id, rel, errNoPlan)
	}
	data, err := fsx.ReadFile(root, rel, 1<<20)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	p, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	p.Dir, p.Rel = dir, rel
	return p, nil
}

// NoPlan informa si el error de Load es porque el workstream no tiene plan.
func NoPlan(err error) bool { return errors.Is(err, errNoPlan) }

// Parse decodifica un plan; los campos desconocidos son un error.
func Parse(data []byte) (*Plan, error) {
	var p Plan
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	p.Raw = append([]byte(nil), data...)
	return &p, nil
}

// All lista los workstreams que tienen plan, ordenados por id.
func All(root string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(Base)))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || !dirRe.MatchString(name) {
			continue
		}
		if fsx.Regular(root, Base+"/"+name+"/plan.yaml") && (len(out) == 0 || !contains(out, name[:6])) {
			out = append(out, name[:6])
		}
	}
	sort.Strings(out)
	return out, nil
}

// Mode es el modo con el que corre el plan: el del plan, sin pasar el del
// proyecto, que es el techo. capped informa si el proyecto lo bajó.
func Mode(plan, project string) (mode string, capped bool) {
	if plan == "" {
		plan = "manual"
	}
	if project == "" {
		project = "manual"
	}
	if Rank(plan) > Rank(project) {
		return project, true
	}
	return plan, false
}

// Rank ordena los modos: manual 0, supervised 1, autonomous 2; -1 si no existe.
func Rank(mode string) int {
	for i, m := range Modes {
		if m == mode {
			return i
		}
	}
	return -1
}

// StepRisk es el riesgo del paso: el suyo o el del plan; R1 si ninguno lo dice.
func (p *Plan) StepRisk(s Step) string {
	switch {
	case s.Risk != "":
		return s.Risk
	case p.Risk != "":
		return p.Risk
	}
	return "R1"
}

// Index devuelve la posición de un paso o -1.
func (p *Plan) Index(id string) int {
	for i, s := range p.Steps {
		if s.ID == id {
			return i
		}
	}
	return -1
}

// AuthHash identifica la autorización de correr este plan exacto en
// autonomous (A4): cambiar una coma del plan pide autorizarlo de nuevo.
func (p *Plan) AuthHash(project string) string {
	h := sha256.New()
	fmt.Fprintf(h, "coyote-ws-autonomous\x00%s\x00%s\x00", project, p.ID)
	h.Write(p.Raw)
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// Input es una entrada de un paso: el artefacto de un paso anterior, un
// archivo del proyecto o el impacto de un diff en el producto.
type Input struct {
	Kind  string // step, file o diff
	Value string
}

// ParseInput lee step:ID, diff:repo=RANGO, file:ruta o una ruta a secas.
func ParseInput(s string) (Input, error) {
	s = strings.TrimSpace(s)
	switch {
	case strings.HasPrefix(s, "step:"):
		id := strings.TrimPrefix(s, "step:")
		if !stepIDRe.MatchString(id) {
			return Input{}, fmt.Errorf("entrada %q: paso inválido", s)
		}
		return Input{"step", id}, nil
	case strings.HasPrefix(s, "diff:"):
		v := strings.TrimPrefix(s, "diff:")
		repo, rng, ok := strings.Cut(v, "=")
		if !ok || !repoRe.MatchString(repo) || rng == "" || strings.HasPrefix(rng, "-") || strings.ContainsAny(rng, " \t\n\\") {
			return Input{}, fmt.Errorf("entrada %q: usa diff:repo=RANGO (main...HEAD)", s)
		}
		return Input{"diff", v}, nil
	}
	p, err := CleanPath(strings.TrimPrefix(s, "file:"))
	if err != nil {
		return Input{}, fmt.Errorf("entrada %q: %v", s, err)
	}
	return Input{"file", p}, nil
}

// CleanPath valida una ruta de entrada: relativa, dentro del proyecto y
// fuera de .git y .coyote.
func CleanPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "~") || strings.Contains(p, "\\") || filepath.IsAbs(p) {
		return "", errors.New("usa una ruta relativa a la raíz del proyecto")
	}
	c := path.Clean(p)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return "", errors.New("la ruta sale del proyecto")
	}
	// En macOS el disco no distingue mayúsculas: .GIT es .git.
	for _, seg := range strings.Split(c, "/") {
		if strings.EqualFold(seg, ".git") || strings.EqualFold(seg, ".coyote") {
			return "", fmt.Errorf("%s no es una entrada: es de git o la caché de coyote", seg)
		}
	}
	return c, nil
}

// Finding es un hallazgo de la revisión de un plan.
type Finding struct {
	Level string `json:"level"` // error o aviso
	Step  string `json:"step,omitempty"`
	Msg   string `json:"msg"`
}

func (f Finding) String() string {
	if f.Step == "" {
		return f.Msg
	}
	return f.Step + ": " + f.Msg
}

// Errors cuenta los errores: un plan con errores no corre.
func Errors(fs []Finding) int {
	n := 0
	for _, f := range fs {
		if f.Level == "error" {
			n++
		}
	}
	return n
}

// CheckOptions es lo que la revisión necesita saber del proyecto.
type CheckOptions struct {
	Agents    map[string]bool       // agentes definidos en el proyecto
	Installed map[string]bool       // instalados en Claude Code; nil no revisa
	Exists    func(rel string) bool // archivos de entrada; nil no revisa
	Project   string                // autonomy del proyecto (el techo)
}

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

// Check revisa el plan: el contrato de cada paso (A2), las entradas y lo que
// cada modo exige. Los errores impiden correrlo.
func (p *Plan) Check(o CheckOptions) []Finding {
	var out []Finding
	add := func(level, step, format string, a ...any) {
		out = append(out, Finding{Level: level, Step: step, Msg: fmt.Sprintf(format, a...)})
	}
	dirID := path.Base(p.Dir)
	switch {
	case p.ID == "":
		add("error", "", "falta id")
	case !IDRe.MatchString(p.ID):
		add("error", "", "id %q inválido: usa W-0001", p.ID)
	case p.Dir != "" && dirID != p.ID && !strings.HasPrefix(dirID, p.ID+"-"):
		add("error", "", "el id %s no coincide con la carpeta %s", p.ID, dirID)
	}
	if p.Autonomy != "" && Rank(p.Autonomy) < 0 {
		add("error", "", "autonomy %q inválido: %s", p.Autonomy, strings.Join(Modes, ", "))
	}
	if p.Risk != "" && !riskRe.MatchString(p.Risk) {
		add("error", "", "risk %q inválido: R1, R2 o R3", p.Risk)
	}
	if !finite(p.BudgetUSD) || p.BudgetUSD < 0 || p.BudgetUSD > MaxPlanUSD {
		add("error", "", "budget_usd inválido: entre 0 y %.0f dólares", MaxPlanUSD)
	}
	if p.Autonomy == "autonomous" && !(p.BudgetUSD > 0) {
		add("error", "", "autonomous exige budget_usd: un loop autónomo corre dentro de sus topes (A4)")
	}
	if mode, capped := Mode(p.Autonomy, o.Project); capped {
		add("aviso", "", "el proyecto permite hasta %s: el plan corre como %s", orManual(o.Project), mode)
	}
	if len(p.Steps) == 0 {
		add("error", "", "el plan no tiene pasos")
	}
	seen := map[string]int{}
	sum := 0.0
	for i, s := range p.Steps {
		id := s.ID
		switch {
		case id == "":
			id = fmt.Sprintf("paso %d", i+1)
			add("error", id, "falta id")
		case !stepIDRe.MatchString(id):
			add("error", id, "id inválido: letras, números, punto, guion y guion bajo, hasta 32")
		case seen[id] > 0:
			add("error", id, "id repetido")
		}
		seen[s.ID] = i + 1
		if strings.TrimSpace(s.Does) == "" {
			add("error", id, "falta does: qué hace el paso (A2)")
		}
		if s.Risk != "" && !riskRe.MatchString(s.Risk) {
			add("error", id, "risk %q inválido: R1, R2 o R3", s.Risk)
		}
		if s.Gate != "" && s.Gate != "human" {
			add("error", id, "gate %q inválido: human o nada", s.Gate)
		}
		switch {
		case s.Agent == "":
			add("error", id, "falta agent: un agente coyote-* o %s", Human)
		case s.IsHuman():
		case !agentRe.MatchString(s.Agent):
			add("error", id, "agente %q inválido", s.Agent)
		case o.Agents != nil && !o.Agents[s.Agent]:
			add("error", id, "el agente %s no existe en el proyecto", s.Agent)
		case o.Installed != nil && !o.Installed[s.Agent]:
			add("aviso", id, "%s no está instalado para Claude Code: corre coyote install --ide claude-code", s.Agent)
		}
		if !s.IsHuman() && s.Agent != "" {
			switch {
			case !finite(s.MaxUSD) || s.MaxUSD < 0 || s.MaxUSD > MaxStepUSD:
				add("error", id, "max_usd inválido: entre 0.01 y %.0f dólares", MaxStepUSD)
			case s.MaxUSD < 0.01:
				add("error", id, "falta max_usd: el tope en dólares del paso (A2)")
			}
			if s.MaxTurns < 0 || s.MaxTurns > MaxTurns {
				add("error", id, "max_turns inválido: entre 1 y %d", MaxTurns)
			}
			if len(s.Output) == 0 {
				add("error", id, "falta output: la salida que se espera del paso (A2)")
			}
			if finite(s.MaxUSD) && s.MaxUSD > 0 {
				sum += s.MaxUSD
			}
		}
		if len(s.Sections) > maxSections {
			add("error", id, "sections: hasta %d secciones", maxSections)
		}
		for _, sec := range s.Sections {
			if t := strings.TrimSpace(sec); t == "" || len(t) > 80 || strings.ContainsAny(t, "\n\r#") {
				add("error", id, "sección inválida %q: un título de hasta 80 caracteres, sin #", sec)
			}
		}
		for _, raw := range s.Input {
			in, err := ParseInput(raw)
			if err != nil {
				add("error", id, "%v", err)
				continue
			}
			switch in.Kind {
			case "step":
				j := p.Index(in.Value)
				switch {
				case j < 0:
					add("error", id, "entrada step:%s: ese paso no existe", in.Value)
				case j >= i:
					add("error", id, "entrada step:%s: ese paso corre después; la entrada tiene que ser de un paso anterior", in.Value)
				case p.Steps[j].IsHuman():
					add("error", id, "entrada step:%s: es un paso de la persona y no deja artefacto; cita el archivo que produce", in.Value)
				}
			case "file":
				if o.Exists != nil && !o.Exists(in.Value) {
					add("aviso", id, "la entrada %s no existe todavía", in.Value)
				}
			}
		}
	}
	if p.BudgetUSD > 0 && sum > p.BudgetUSD+1e-9 {
		add("aviso", "", "los topes de los pasos suman $%.2f, más que el del plan ($%.2f): el plan se detiene al llegar a su tope", sum, p.BudgetUSD)
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func orManual(m string) string {
	if m == "" {
		return "manual"
	}
	return m
}
