// Package runner corre un paso de un agente con Claude Code en modo headless
// (ADR-0012) y lee su resultado: texto, turnos, tokens y costo estimado.
// Corre en la máquina de la persona, con su Claude Code y su sesión; los
// hooks del proyecto (el gate de coyote) aplican también aquí.
package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Request es una corrida.
type Request struct {
	Bin      string // claude, o la ruta en COYOTE_CLAUDE
	Dir      string // raíz del proyecto: ahí están sus agentes y su gate
	Agent    string
	Model    string
	MaxTurns int
	MaxUSD   float64
	AddDirs  []string // carpetas que el agente puede leer además del proyecto
	Tools    []string // herramientas que corren sin preguntar; el gate decide cada acción
	Prompt   string   // va por la entrada estándar
	Timeout  time.Duration
	Env      []string // variables extra para Claude Code
	Resume   string   // sesión que se retoma: el agente repite lo que la persona aprobó
}

// sessionRe acota el id de una sesión que se retoma: nunca empieza con guion.
var sessionRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{7,127}$`)

// ValidSession informa si un id de sesión se puede pasar a --resume.
func ValidSession(id string) bool { return sessionRe.MatchString(id) }

// Args arma la línea de Claude Code. --permission-mode dontAsk niega lo que
// no está permitido en lugar de esperar una respuesta que en headless nadie da.
func Args(r Request) []string {
	args := []string{"-p", "--output-format", "json", "--agent", r.Agent, "--permission-mode", "dontAsk"}
	if ValidSession(r.Resume) {
		args = append(args, "--resume", r.Resume)
	}
	if r.Model != "" {
		args = append(args, "--model", r.Model)
	}
	args = append(args, "--max-turns", strconv.Itoa(r.MaxTurns), "--max-budget-usd", strconv.FormatFloat(r.MaxUSD, 'f', 2, 64))
	if len(r.Tools) > 0 {
		args = append(args, "--allowedTools", strings.Join(r.Tools, ","))
	}
	for _, d := range r.AddDirs {
		args = append(args, "--add-dir", d)
	}
	return args
}

// Usage son los tokens de una corrida o de un modelo.
type Usage struct {
	Input      int64 `json:"input_tokens"`
	CacheRead  int64 `json:"cache_read_input_tokens"`
	CacheWrite int64 `json:"cache_creation_input_tokens"`
	Output     int64 `json:"output_tokens"`
}

// InputTotal es toda la entrada: sin caché, leída de caché y escrita en caché.
func (u Usage) InputTotal() int64 { return u.Input + u.CacheRead + u.CacheWrite }

// ModelUsage es el uso y el costo de un modelo dentro de la corrida.
type ModelUsage struct {
	Usage
	CostUSD float64
}

// Result es lo que devolvió Claude Code.
type Result struct {
	Subtype    string
	IsError    bool
	Text       string
	SessionID  string
	Turns      int
	DurationMS int64
	CostUSD    float64 // estimación del cliente, no la factura
	Usage      Usage
	Models     map[string]ModelUsage
	Denials    int // acciones que Claude Code negó por permisos
	ExitCode   int
	Stderr     string
	TimedOut   bool
}

// OK informa si la corrida terminó bien.
func (r *Result) OK() bool {
	return r.ExitCode == 0 && !r.IsError && (r.Subtype == "" || r.Subtype == "success")
}

// MainModel es el modelo que más costó en la corrida.
func (r *Result) MainModel() string {
	names := make([]string, 0, len(r.Models))
	for m := range r.Models {
		names = append(names, m)
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := r.Models[names[i]], r.Models[names[j]]
		if a.CostUSD != b.CostUSD {
			return a.CostUSD > b.CostUSD
		}
		return names[i] < names[j]
	})
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

// Status resume el desenlace en palabras.
func (r *Result) Status() string {
	switch {
	case r.TimedOut:
		return "se venció el tiempo"
	case r.Subtype == "error_max_turns":
		return "llegó al tope de turnos"
	case strings.Contains(r.Subtype, "budget"):
		return "llegó al tope de dólares"
	case r.OK():
		return "terminó"
	case r.Subtype != "":
		return "terminó con error (" + r.Subtype + ")"
	}
	return fmt.Sprintf("terminó con error (código %d)", r.ExitCode)
}

// raw es la salida JSON de claude -p --output-format json; los campos que
// falten quedan en cero.
type raw struct {
	Type       string  `json:"type"`
	Subtype    string  `json:"subtype"`
	IsError    bool    `json:"is_error"`
	Result     string  `json:"result"`
	SessionID  string  `json:"session_id"`
	NumTurns   int     `json:"num_turns"`
	DurationMS int64   `json:"duration_ms"`
	TotalCost  float64 `json:"total_cost_usd"`
	Usage      Usage   `json:"usage"`
	ModelUsage map[string]struct {
		InputTokens              int64   `json:"inputTokens"`
		OutputTokens             int64   `json:"outputTokens"`
		CacheReadInputTokens     int64   `json:"cacheReadInputTokens"`
		CacheCreationInputTokens int64   `json:"cacheCreationInputTokens"`
		CostUSD                  float64 `json:"costUSD"`
	} `json:"modelUsage"`
	PermissionDenials []json.RawMessage `json:"permission_denials"`
}

// Parse lee la salida de Claude Code. Acepta un objeto JSON o, si hubo
// líneas antes, el último objeto de tipo result.
func Parse(out []byte) (*Result, error) {
	var r raw
	text := bytes.TrimSpace(out)
	var list []raw
	if bytes.HasPrefix(text, []byte("[")) && json.Unmarshal(text, &list) == nil {
		// Algunas versiones devuelven la lista de mensajes: vale el último resultado.
		found := false
		for i := len(list) - 1; i >= 0; i-- {
			if list[i].Type == "result" {
				r, found = list[i], true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("la salida de Claude Code no trae un resultado")
		}
	} else if err := json.Unmarshal(text, &r); err != nil {
		found := false
		lines := bytes.Split(text, []byte("\n"))
		for i := len(lines) - 1; i >= 0; i-- {
			var x raw
			if json.Unmarshal(bytes.TrimSpace(lines[i]), &x) == nil && x.Type == "result" {
				r, found = x, true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("la salida de Claude Code no es JSON: %s", firstLine(string(text)))
		}
	}
	res := &Result{Subtype: r.Subtype, IsError: r.IsError, Text: r.Result, SessionID: r.SessionID, Turns: r.NumTurns,
		DurationMS: r.DurationMS, CostUSD: r.TotalCost, Usage: r.Usage, Models: map[string]ModelUsage{}, Denials: len(r.PermissionDenials)}
	for name, m := range r.ModelUsage {
		res.Models[name] = ModelUsage{Usage: Usage{Input: m.InputTokens, CacheRead: m.CacheReadInputTokens,
			CacheWrite: m.CacheCreationInputTokens, Output: m.OutputTokens}, CostUSD: m.CostUSD}
	}
	return res, nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// maxOutput acota lo que se lee de Claude Code.
const maxOutput = 32 << 20

type capped struct {
	buf   bytes.Buffer
	limit int
	over  bool
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.limit - c.buf.Len(); room > 0 {
		if len(p) > room {
			c.buf.Write(p[:room])
			c.over = true
		} else {
			c.buf.Write(p)
		}
	} else {
		c.over = true
	}
	return len(p), nil
}

// ErrNotFound indica que no hay Claude Code instalado.
var ErrNotFound = errors.New("no encuentro Claude Code (claude) en el PATH; instálalo o indica la ruta en COYOTE_CLAUDE")

// Run corre Claude Code y lee su resultado. Un código de salida distinto de
// cero no es un error si Claude Code dejó su resultado: se reporta en Result.
func Run(ctx context.Context, r Request) (*Result, error) {
	bin := r.Bin
	if bin == "" {
		bin = "claude"
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return nil, ErrNotFound
	}
	if r.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.Timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, path, Args(r)...)
	cmd.Dir = r.Dir
	cmd.Stdin = strings.NewReader(r.Prompt)
	cmd.Env = append(os.Environ(), r.Env...)
	setGroup(cmd)
	cmd.WaitDelay = 5 * time.Second // un hijo que retiene la salida no cuelga a coyote
	stdout := &capped{limit: maxOutput}
	stderr := &capped{limit: 64 << 10}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	runErr := cmd.Run()
	res, perr := Parse(stdout.buf.Bytes())
	if perr != nil {
		res = &Result{}
	}
	res.Stderr = strings.TrimSpace(stderr.buf.String())
	res.TimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
	var exitErr *exec.ExitError
	switch {
	case runErr == nil:
	case errors.As(runErr, &exitErr):
		res.ExitCode = exitErr.ExitCode()
		if res.ExitCode < 0 {
			res.ExitCode = 1
		}
	default:
		return nil, fmt.Errorf("no pude correr Claude Code: %w", runErr)
	}
	if perr != nil && !res.TimedOut {
		msg := perr.Error()
		if res.Stderr != "" {
			msg += "; " + firstLine(res.Stderr)
		}
		return res, errors.New(msg)
	}
	if stdout.over {
		return res, fmt.Errorf("la salida de Claude Code pasó de %d MB", maxOutput>>20)
	}
	return res, nil
}
