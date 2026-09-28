// Package router decide con qué modelo y con qué topes corre un paso de un
// agente (ADR-0012): el modelo del agente, un piso por riesgo y la
// degradación por presupuesto. La configuración vive en coyote/router.yaml;
// sin ese archivo valen los valores por defecto.
package router

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/Emmanuel93/coyote/internal/ccf"
	"github.com/Emmanuel93/coyote/internal/fsx"
)

// Path es la configuración del router, relativa a la raíz del proyecto.
const Path = "coyote/router.yaml"

// Price es el precio de un modelo en USD por millón de tokens. Sirve solo
// para separar el costo de entrada y el de salida: el total es el que
// reporta Claude Code.
type Price struct {
	Input      float64 `yaml:"input"`
	Output     float64 `yaml:"output"`
	CacheRead  float64 `yaml:"cache_read"`
	CacheWrite float64 `yaml:"cache_write"`
}

// Config es coyote/router.yaml.
type Config struct {
	Version   int               `yaml:"version"`
	Levels    []string          `yaml:"levels"`     // de menor a mayor capacidad y costo
	Default   string            `yaml:"default"`    // modelo si el agente no declara uno
	Agents    map[string]string `yaml:"agents"`     // agente → modelo; manda sobre el del agente
	RiskFloor map[string]string `yaml:"risk_floor"` // R1, R2, R3 → modelo mínimo
	Budget    struct {
		WarnAt float64 `yaml:"warn_at"` // fracción del tope mensual: avisa y baja un nivel (salvo R3)
		StopAt float64 `yaml:"stop_at"` // fracción del tope mensual: no corre
	} `yaml:"budget"`
	Limits struct {
		MaxTurns int     `yaml:"max_turns"`
		MaxUSD   float64 `yaml:"max_usd_per_run"`
	} `yaml:"limits"`
	Prices map[string]Price `yaml:"prices"` // opcional: clave = parte del nombre del modelo
}

// Default es la configuración sin coyote/router.yaml.
func Default() *Config {
	c := &Config{Version: 1, Levels: []string{"haiku", "sonnet", "opus"}, Default: "sonnet",
		Agents: map[string]string{}, RiskFloor: map[string]string{"R2": "sonnet", "R3": "opus"}}
	c.Budget.WarnAt, c.Budget.StopAt = 0.8, 1.0
	c.Limits.MaxTurns, c.Limits.MaxUSD = 30, 2
	return c
}

// Template es el router.yaml que escribe coyote router --init.
const Template = `# Router de modelos de coyote run (ADR-0012). Sin este archivo valen estos valores.
version: 1
levels: [haiku, sonnet, opus]   # de menor a mayor capacidad y costo; alias o IDs de Claude Code
default: sonnet                 # si el agente no declara modelo
agents: {}                      # agente → modelo, manda sobre el del agente (p. ej. coyote-dev: opus)
risk_floor:                     # modelo mínimo según el riesgo de la tarea (--risk)
  R2: sonnet
  R3: opus
budget:                         # sobre budgets.monthly_usd de coyote/project.yaml
  warn_at: 0.8                  # avisa y baja un nivel, salvo R3
  stop_at: 1.0                  # no corre
limits:
  max_turns: 30                 # por corrida, si el agente no declara otro
  max_usd_per_run: 2.00         # tope de cada corrida (--max-budget-usd de Claude Code)
# prices:                       # opcional, USD por millón de tokens: separa el costo de entrada y el de salida
#   sonnet: { input: 0, output: 0, cache_read: 0, cache_write: 0 }
`

// Load lee coyote/router.yaml sobre los valores por defecto y lo valida.
func Load(root string) (*Config, error) {
	c := Default()
	if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(Path))); os.IsNotExist(err) {
		return c, nil
	}
	data, err := fsx.ReadFile(root, Path, 1<<20)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", Path, err)
	}
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(c); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", Path, err)
	}
	return c, c.Validate()
}

// Validate revisa que los modelos existan en los niveles y que los topes tengan sentido.
func (c *Config) Validate() error {
	var errs []string
	seen := map[string]bool{}
	for _, l := range c.Levels {
		if strings.TrimSpace(l) == "" || seen[l] {
			errs = append(errs, fmt.Sprintf("levels: nivel vacío o repetido %q", l))
		}
		seen[l] = true
	}
	if len(c.Levels) == 0 {
		errs = append(errs, "levels: hace falta al menos un modelo")
	}
	check := func(where, m string) {
		if m != "" && !seen[m] {
			errs = append(errs, fmt.Sprintf("%s: %q no está en levels", where, m))
		}
	}
	check("default", c.Default)
	for a, m := range c.Agents {
		check("agents."+a, m)
	}
	for r, m := range c.RiskFloor {
		if r != "R1" && r != "R2" && r != "R3" {
			errs = append(errs, fmt.Sprintf("risk_floor: riesgo %q inválido (R1, R2 o R3)", r))
		}
		check("risk_floor."+r, m)
	}
	if !finite(c.Budget.WarnAt) || !finite(c.Budget.StopAt) || c.Budget.WarnAt <= 0 || c.Budget.StopAt <= 0 || c.Budget.WarnAt >= c.Budget.StopAt {
		errs = append(errs, "budget: warn_at y stop_at deben ser positivos y warn_at menor que stop_at")
	}
	if c.Limits.MaxTurns <= 0 || c.Limits.MaxTurns > 500 {
		errs = append(errs, "limits.max_turns: entre 1 y 500")
	}
	if !finite(c.Limits.MaxUSD) || c.Limits.MaxUSD <= 0 || c.Limits.MaxUSD > 1000 {
		errs = append(errs, "limits.max_usd_per_run: mayor que 0 y hasta 1000")
	}
	if len(errs) > 0 {
		sort.Strings(errs)
		return fmt.Errorf("%s: %s", Path, strings.Join(errs, "; "))
	}
	return nil
}

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

func (c *Config) level(m string) int {
	for i, l := range c.Levels {
		if l == m {
			return i
		}
	}
	return -1
}

// Input es lo que el router necesita para decidir una corrida.
type Input struct {
	Agent      string
	AgentModel string // el modelo que declara el agente
	AgentTurns int    // los turnos que declara el agente
	Risk       string // R1, R2 o R3; vacío es R1
	Model      string // pedido explícito (--model)
	MaxTurns   int    // pedido explícito (--max-turns)
	MaxUSD     float64
	MonthlyUSD float64 // tope mensual del proyecto; 0 es sin tope
	SpentUSD   float64 // gastado en el mes
}

// Decision es el modelo y los topes de una corrida, con el porqué.
type Decision struct {
	Model    string
	MaxTurns int
	MaxUSD   float64
	Notes    []string
	Refused  string // motivo para no correr
}

// Decide aplica, en orden: el modelo pedido o el del agente, el piso por
// riesgo, la degradación por presupuesto y los topes de la corrida.
func (c *Config) Decide(in Input) Decision {
	d := Decision{}
	risk := in.Risk
	if risk == "" {
		risk = "R1"
	}
	model, why := in.Model, "pedido con --model"
	switch {
	case model != "":
	case c.Agents[in.Agent] != "":
		model, why = c.Agents[in.Agent], "router.yaml para "+in.Agent
	case in.AgentModel != "" && in.AgentModel != "inherit":
		model, why = in.AgentModel, "el que declara "+in.Agent
	default:
		model, why = c.Default, "el modelo por defecto"
	}
	d.Notes = append(d.Notes, fmt.Sprintf("modelo %s: %s", model, why))
	ranked := c.level(model) >= 0
	if !ranked {
		d.Notes = append(d.Notes, "el modelo no está en levels: no se aplican piso ni degradación")
	}
	floor := c.RiskFloor[risk]
	if ranked && floor != "" && c.level(model) < c.level(floor) {
		d.Notes = append(d.Notes, fmt.Sprintf("el riesgo %s pide al menos %s", risk, floor))
		model = floor
	}
	d.MaxTurns = in.MaxTurns
	if d.MaxTurns <= 0 {
		d.MaxTurns = in.AgentTurns
	}
	if d.MaxTurns <= 0 {
		d.MaxTurns = c.Limits.MaxTurns
	}
	d.MaxUSD = in.MaxUSD
	if d.MaxUSD <= 0 || !finite(d.MaxUSD) {
		d.MaxUSD = c.Limits.MaxUSD
	}
	if !finite(in.SpentUSD) || !finite(in.MonthlyUSD) || in.SpentUSD < 0 || in.MonthlyUSD < 0 {
		d.Refused = "el gasto o el tope del mes no son números válidos: revisa el ledger y budgets.monthly_usd"
		d.Model = model
		return d
	}
	if in.MonthlyUSD > 0 {
		// El margen evita que 0.04/0.05 (0.7999… en coma flotante) quede bajo el 80 %.
		frac := in.SpentUSD/in.MonthlyUSD + 1e-9
		pct := int(math.Round(frac * 100))
		switch {
		case frac >= c.Budget.StopAt:
			d.Refused = fmt.Sprintf("el proyecto gastó $%.2f de $%.2f este mes (%d %%): no se corren agentes hasta que la persona suba budgets.monthly_usd en coyote/project.yaml",
				in.SpentUSD, in.MonthlyUSD, pct)
		case frac >= c.Budget.WarnAt && risk == "R3":
			d.Notes = append(d.Notes, fmt.Sprintf("presupuesto al %d %%: una tarea R3 no baja de modelo", pct))
		case frac >= c.Budget.WarnAt && ranked:
			lower := c.level(model) - 1
			if floor != "" && lower < c.level(floor) {
				lower = c.level(floor)
			}
			if lower >= 0 && lower < c.level(model) {
				d.Notes = append(d.Notes, fmt.Sprintf("presupuesto al %d %%: baja de %s a %s", pct, model, c.Levels[lower]))
				model = c.Levels[lower]
			} else {
				d.Notes = append(d.Notes, fmt.Sprintf("presupuesto al %d %%: ya está en el modelo mínimo permitido", pct))
			}
		}
		if left := in.MonthlyUSD - in.SpentUSD; left < d.MaxUSD && d.Refused == "" {
			d.MaxUSD = math.Floor(left*100) / 100
			d.Notes = append(d.Notes, fmt.Sprintf("tope de la corrida ajustado a lo que queda del mes: $%.2f", d.MaxUSD))
		}
	}
	if d.MaxUSD < 0.01 && d.Refused == "" {
		d.Refused = "el tope de la corrida es menor que un centavo"
	}
	d.Model = model
	return d
}

// Split separa el costo total de un modelo en entrada y salida con la tabla
// de precios. El total es siempre el que reportó Claude Code; sin precio para
// el modelo, todo queda como entrada y ok es false.
func (c *Config) Split(model string, total float64, input, cacheRead, cacheWrite, output int64) (ccf.Cost, bool) {
	var p Price
	found := false
	best := 0
	for key, pr := range c.Prices {
		if key != "" && strings.Contains(strings.ToLower(model), strings.ToLower(key)) && len(key) > best {
			p, found, best = pr, true, len(key)
		}
	}
	if !found || total <= 0 {
		return ccf.Cost{In: total}, found && total <= 0
	}
	in := (float64(input)*p.Input + float64(cacheRead)*p.CacheRead + float64(cacheWrite)*p.CacheWrite) / 1e6
	out := float64(output) * p.Output / 1e6
	if in+out <= 0 {
		return ccf.Cost{In: total}, false
	}
	outShare := total * out / (in + out)
	return ccf.Cost{In: total - outShare, Out: outShare}, true
}
