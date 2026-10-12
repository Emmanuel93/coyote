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
	"unicode"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"

	"github.com/Emmanuel93/coyote/internal/fsx"
	"github.com/Emmanuel93/coyote/internal/safetext"
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
	PRD          string            `yaml:"prd"` // de qué PRD parte el plan (R20, ADR-0028): una ruta del proyecto o step:ID
	BudgetUSD    float64           `yaml:"budget_usd"`
	Reasoning    string            `yaml:"reasoning"`
	AuthorizedBy string            `yaml:"authorized_by"`
	Gate         string            `yaml:"gate"` // gate de release que cierra el plan (G5); informativo
	Steps        []Step            `yaml:"steps"`
	Close        map[string]string `yaml:"close"`

	// Declared es el id que escribe plan.yaml. Load deja en ID el de la carpeta:
	// el workstream es el de su carpeta para todo, y un id distinto adentro (un
	// error típico al copiar una carpeta) es un error del plan que Check dice,
	// no un cambio de workstream.
	Declared string `yaml:"-"`
	loaded   bool

	Dir string `yaml:"-"` // carpeta del workstream, relativa a la raíz
	Rel string `yaml:"-"` // plan.yaml, relativo a la raíz
	Raw []byte `yaml:"-"`
}

// Find busca la carpeta de un workstream: coyote/workstreams/W-0004 o
// W-0004-algo. Devuelve la ruta relativa a la raíz. Si hay varias carpetas con
// ese id, vale la que tiene plan.yaml: una carpeta huérfana (corridas sueltas,
// un nombre viejo) no esconde el plan de otra. Con más de una con plan, o sin
// plan en ninguna, no se elige a ciegas.
func Find(root, id string) (string, error) {
	if !IDRe.MatchString(id) {
		return "", fmt.Errorf("workstream %q inválido: usa W-0001", id)
	}
	entries, _ := os.ReadDir(filepath.Join(root, filepath.FromSlash(Base)))
	var all, withPlan []string
	for _, e := range entries {
		if e.IsDir() && (e.Name() == id || strings.HasPrefix(e.Name(), id+"-")) {
			if !dirRe.MatchString(e.Name()) {
				// Las rutas de los artefactos van al ledger, que no admite espacios.
				return "", fmt.Errorf("la carpeta %q no sirve para un workstream: usa %s-nombre con letras, números, punto, guion y guion bajo", e.Name(), id)
			}
			all = append(all, e.Name())
			if fsx.Regular(root, Base+"/"+e.Name()+"/plan.yaml") {
				withPlan = append(withPlan, e.Name())
			}
		}
	}
	switch {
	case len(all) == 0:
		return "", fmt.Errorf("no existe el workstream %s en %s/", id, Base)
	case len(withPlan) == 1:
		return Base + "/" + withPlan[0], nil
	case len(all) == 1:
		return Base + "/" + all[0], nil
	}
	names := withPlan
	if len(names) == 0 {
		names = all
	}
	return "", fmt.Errorf("hay %d carpetas para %s en %s/ (%s): deja una", len(names), id, Base, strings.Join(names, ", "))
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
	p.Declared, p.ID, p.loaded = p.ID, id, true
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
	// Un modo que no existe es un error que Check dice; mientras tanto el plan
	// se trata como manual, y su texto no llega a ninguna salida.
	if plan == "" || Rank(plan) < 0 {
		plan = "manual"
	}
	if project == "" || Rank(project) < 0 {
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

// MaxRisk es el riesgo más alto del plan: el suyo o el de cualquiera de sus
// pasos. Un riesgo que no es R1, R2 o R3 no cuenta.
func (p *Plan) MaxRisk() string {
	max := "R1"
	for _, r := range append([]string{p.Risk}, stepRisks(p.Steps)...) {
		if riskRe.MatchString(r) && r > max {
			max = r
		}
	}
	return max
}

func stepRisks(steps []Step) []string {
	out := make([]string, 0, len(steps))
	for _, s := range steps {
		out = append(out, s.Risk)
	}
	return out
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
// autonomous (A4): cambiar una coma del plan pide autorizarlo de nuevo. Cubre
// también los archivos que el plan lee (Files): files da la huella de cada
// uno, y editar el PRD o una entrada después de aprobar cambia el hash.
func (p *Plan) AuthHash(project string, files map[string]string) string {
	h := sha256.New()
	fmt.Fprintf(h, "coyote-ws-autonomous\x00%s\x00%s\x00", project, p.ID)
	h.Write(p.Raw)
	for _, f := range p.Files() {
		if d, ok := files[f]; ok {
			fmt.Fprintf(h, "\x00%s\x00%s", f, d)
		}
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// Files son los archivos del proyecto que el plan lee: las entradas de
// archivo de sus pasos y el PRD, si es un archivo. Sin repetir y en orden.
func (p *Plan) Files() []string {
	seen := map[string]bool{}
	var out []string
	add := func(raw string) {
		if in, err := ParseInput(raw); err == nil && in.Kind == "file" && !seen[in.Value] {
			seen[in.Value] = true
			out = append(out, in.Value)
		}
	}
	add(p.PRD)
	for _, s := range p.Steps {
		for _, raw := range s.Input {
			add(raw)
		}
	}
	sort.Strings(out)
	return out
}

// Input es una entrada de un paso: el artefacto de un paso anterior, un
// archivo del proyecto o el impacto de un diff en el producto.
type Input struct {
	Kind  string // step, file o diff
	Value string
}

// prefixes son los prefijos con significado de una entrada: se escriben en
// minúsculas, tal cual, y una ruta no puede empezar con ellos.
var prefixes = []string{"step:", "diff:", "file:"}

// reservedPrefix devuelve el prefijo con significado con el que empieza s, en
// cualquier combinación de mayúsculas.
func reservedPrefix(s string) string {
	l := strings.ToLower(s)
	for _, p := range prefixes {
		if strings.HasPrefix(l, p) {
			return p
		}
	}
	return ""
}

// ParseInput lee step:ID, diff:repo=RANGO, file:ruta o una ruta a secas.
// STEP:S1 o file:step:S1 no valen como rutas: pasarían por un archivo con ese
// nombre y le dirían a quien lee el plan otra cosa que lo que corre.
func ParseInput(s string) (Input, error) {
	s = strings.TrimSpace(s)
	if p := reservedPrefix(s); p != "" && !strings.HasPrefix(s, p) {
		return Input{}, fmt.Errorf("entrada %q: %s se escribe en minúsculas", s, p)
	}
	if body, ok := strings.CutPrefix(s, "file:"); ok {
		if p := reservedPrefix(strings.TrimSpace(body)); p != "" {
			return Input{}, fmt.Errorf("entrada %q: una ruta no empieza con %s; escribe la entrada sin file:", s, p)
		}
	}
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
	// El prefijo se mira también en la ruta ya limpia: ./step:S1 o x/../file:step:S1
	// se muestran como step:S1 y file:step:S1, y no son un paso.
	if pre := reservedPrefix(p); pre != "" {
		return Input{}, fmt.Errorf("entrada %q: una ruta no empieza con %s; escribe la entrada sin ./ ni file:", s, pre)
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
	if !visible(p) {
		return "", errors.New("la ruta lleva saltos de línea, controles, espacios especiales o marcas que no se ven")
	}
	// Un espacio al borde de un nombre ("./ step:S1") se come el prefijo en lo que
	// se muestra: la entrada se lee como un paso y es un archivo.
	for _, seg := range strings.Split(p, "/") {
		if strings.TrimFunc(seg, unicode.IsSpace) != seg {
			return "", errors.New("un nombre de la ruta no empieza ni termina con espacios")
		}
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

// visible dice si una ruta se ve como es: cada carácter se dibuja (nada sin
// asignar, de formato, de control ni de uso privado, y de los espacios solo el
// común) y ningún nombre empieza con una marca ni con un selector de
// variación, que se pegarían a la barra y esconderían a qué archivo apunta. Una
// ruta que no se ve como es redibuja la salida de quien la muestra, o hace
// pasar un archivo por un paso (step:S1).
func visible(p string) bool {
	prev := rune(0) // el carácter anterior de este nombre; 0 al empezar uno
	for _, r := range p {
		if r == '/' {
			prev = 0
			continue
		}
		if r == utf8.RuneError || !unicode.IsPrint(r) || safetext.Invisible(prev, r) || prev == 0 && unicode.IsMark(r) {
			return false
		}
		prev = r
	}
	return true
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

// InputState es lo que se sabe de un archivo de entrada.
type InputState int

// Estados de un archivo de entrada.
const (
	InputOK       InputState = iota // un archivo de texto que el paso puede leer
	InputMissing                    // todavía no existe
	InputUnusable                   // existe, pero es un enlace, una carpeta o pesa de más: el paso no lo lee
	InputBinary                     // existe, pero no es texto: el paso lo recibe sin contenido
)

// CheckOptions es lo que la revisión necesita saber del proyecto.
type CheckOptions struct {
	Agents    map[string]bool             // agentes definidos en el proyecto
	Installed map[string]bool             // instalados en Claude Code; nil no revisa
	Exists    func(rel string) bool       // archivos de entrada; nil no revisa
	Inspect   func(rel string) InputState // como Exists, pero distingue lo que no se puede leer; manda sobre Exists
	Project   string                      // autonomy del proyecto (el techo)
	Mode      string                      // modo con el que corre esta vez (ws run --mode lo baja); vacío: el del plan, con el techo del proyecto
}

// state dice cómo está un archivo de entrada; InputOK si no se revisa.
func (o CheckOptions) state(rel string) InputState {
	switch {
	case o.Inspect != nil:
		return o.Inspect(rel)
	case o.Exists != nil && !o.Exists(rel):
		return InputMissing
	}
	return InputOK
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
	declared := p.ID
	if p.loaded {
		declared = p.Declared
	}
	switch {
	case declared == "":
		add("error", "", "falta id")
	case !IDRe.MatchString(declared):
		add("error", "", "id %q inválido: usa W-0001", declared)
	case p.Dir != "" && dirID != declared && !strings.HasPrefix(dirID, declared+"-"):
		add("error", "", "el id %s no coincide con la carpeta %s", declared, dirID)
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
	runMode := p.runMode(o)
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
				switch o.state(in.Value) {
				case InputMissing:
					if runMode == "autonomous" {
						// La persona autoriza los archivos que existen al autorizar (A4):
						// uno que falta detiene al loop antes de este paso.
						add("aviso", id, "la entrada %s no existe todavía: en autonomous el loop se detiene antes de este paso (A4); créala antes de autorizar el plan", in.Value)
					} else {
						add("aviso", id, "la entrada %s no existe todavía", in.Value)
					}
				case InputUnusable:
					add("error", id, "la entrada %s no es un archivo regular dentro del proyecto (un enlace, una carpeta o un archivo de más de 8 MB): el paso no la puede leer", in.Value)
				case InputBinary:
					add("aviso", id, "la entrada %s no es texto: el paso la recibe sin contenido", in.Value)
				}
			}
		}
	}
	if p.BudgetUSD > 0 && sum > p.BudgetUSD+1e-9 {
		add("aviso", "", "los topes de los pasos suman $%.2f, más que el del plan ($%.2f): el plan se detiene al llegar a su tope", sum, p.BudgetUSD)
	}
	p.checkPRD(o, add)
	return out
}

// runMode es el modo con el que corre el plan en esta revisión: el que pide
// quien la llama (ws run --mode lo baja) o, si no, el del plan con el techo del
// proyecto.
func (p *Plan) runMode(o CheckOptions) string {
	if o.Mode != "" {
		return o.Mode
	}
	mode, _ := Mode(p.Autonomy, o.Project)
	return mode
}

// PRDInput lee el prd: del plan: un archivo .md del proyecto o la entrega de un
// paso. Es lo que Check llama válido; lo que muestra ws check usa la misma regla.
func (p *Plan) PRDInput() (Input, error) {
	in, err := ParseInput(strings.TrimSpace(p.PRD))
	switch {
	case err == nil && in.Kind == "diff":
		err = errors.New("un diff no es un PRD")
	case err == nil && in.Kind == "file" && !strings.EqualFold(path.Ext(in.Value), ".md"):
		err = errors.New("el PRD es un documento Markdown")
	}
	return in, err
}

// checkPRD revisa de dónde parte el plan (R20, ADR-0028). Que falte el PRD en
// un plan R2 o R3 es un aviso: el plan corre, pero arranca sin saber qué se
// construye ni para quién. Un prd: que no dice lo que quiere decir es un error,
// igual que una entrada mal escrita. No juzga el contenido del PRD.
func (p *Plan) checkPRD(o CheckOptions, add func(level, step, format string, a ...any)) {
	ref := strings.TrimSpace(p.PRD)
	if ref == "" {
		if r := p.MaxRisk(); r != "R1" {
			add("aviso", "", "el plan es %s y no parte de un PRD (R20): escríbelo con coyote-pm y decláralo con prd: coyote/prd/<feature>.md (o prd: step:ID si lo escribe el primer paso)", r)
		}
		return
	}
	in, err := p.PRDInput()
	if err != nil {
		add("error", "", "prd %q inválido: usa una ruta .md del proyecto (coyote/prd/<feature>.md) o step:ID (%v)", ref, err)
		return
	}
	switch in.Kind {
	case "step":
		j := p.Index(in.Value)
		switch {
		case j < 0:
			add("error", "", "prd step:%s: ese paso no existe", in.Value)
			return
		case p.Steps[j].IsHuman():
			add("error", "", "prd step:%s: es un paso de la persona y no deja artefacto; el PRD es la entrega de un agente (coyote-pm) o un archivo", in.Value)
			return
		case p.agentBefore(j):
			add("aviso", "", "prd step:%s no es el primer paso: los pasos anteriores corren sin PRD", in.Value)
		}
		// En autonomous solo se detiene el paso que pide la revisión: sin ella,
		// el paso siguiente usaría un PRD que ninguna persona leyó (R20). Se mira
		// el modo con el que corre: supervised se detiene tras cada paso.
		if p.runMode(o) == "autonomous" && p.Steps[j].Gate != "human" {
			add("error", "", "prd step:%s: en autonomous el PRD pasa por una persona (R20): agrega gate: human al paso %s, o corre con --mode supervised, que se detiene tras cada paso", in.Value, in.Value)
		}
	case "file":
		// Si un agente lo recibe, la revisión de sus entradas ya dice cómo está.
		if !p.receives(in) {
			switch o.state(in.Value) {
			case InputMissing:
				add("aviso", "", "el PRD %s no existe todavía", in.Value)
			case InputUnusable:
				add("error", "", "el PRD %s no es un archivo regular dentro del proyecto (un enlace, una carpeta o un archivo de más de 8 MB)", in.Value)
			case InputBinary:
				add("aviso", "", "el PRD %s no es texto: ningún paso podría leerlo", in.Value)
			}
		}
	}
	if !p.receives(in) {
		add("aviso", "", "ningún paso recibe el PRD como entrada: agrega %s al input del paso que lo necesita", prdInput(in))
	}
}

// agentBefore informa si algún paso de agente corre antes del paso j: los de
// la persona no leen un PRD que falta.
func (p *Plan) agentBefore(j int) bool {
	for _, s := range p.Steps[:j] {
		if !s.IsHuman() {
			return true
		}
	}
	return false
}

// receives informa si algún paso de agente declara la entrada: un paso de la
// persona no la lleva a ningún agente.
func (p *Plan) receives(want Input) bool {
	for _, s := range p.Steps {
		if s.IsHuman() {
			continue
		}
		for _, raw := range s.Input {
			if got, err := ParseInput(raw); err == nil && got == want {
				return true
			}
		}
	}
	return false
}

// prdInput es la entrada que recibe un paso para leer el PRD.
func prdInput(in Input) string {
	if in.Kind == "step" {
		return "step:" + in.Value
	}
	return in.Value
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
