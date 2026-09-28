// Package attribution detecta y elimina atribuciones a herramientas o modelos
// de IA en mensajes de commit, PRs y documentos (regla R15, ADR-0005).
package attribution

import (
	"fmt"
	"io/fs"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"

	coyote "github.com/Emmanuel93/coyote"
	"github.com/Emmanuel93/coyote/internal/glob"
)

// Pattern es una expresión regular con identificador.
type Pattern struct {
	ID      string `yaml:"id"`
	Pattern string `yaml:"pattern"`
	re      *regexp.Regexp
	loose   *regexp.Regexp // sin anclar al inicio de línea, para comandos de una sola línea
}

// Config es el contenido de attribution.yaml.
type Config struct {
	Version        int               `yaml:"version"`
	Vars           map[string]string `yaml:"vars"`
	LinePatterns   []Pattern         `yaml:"line_patterns"`
	PhrasePatterns []Pattern         `yaml:"phrase_patterns"`
	AllowPaths     []string          `yaml:"allow_paths"`
}

// Finding es una atribución encontrada.
type Finding struct {
	Line      int
	PatternID string
	Text      string
}

// Load interpreta y compila una configuración.
func Load(data []byte) (*Config, error) {
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("attribution.yaml: %w", err)
	}
	var pairs []string
	for k, v := range c.Vars {
		pairs = append(pairs, "{{"+k+"}}", v)
	}
	expand := strings.NewReplacer(pairs...)
	for _, list := range [][]Pattern{c.LinePatterns, c.PhrasePatterns} {
		for i := range list {
			list[i].Pattern = expand.Replace(list[i].Pattern)
			if strings.Contains(list[i].Pattern, "{{") {
				return nil, fmt.Errorf("patrón %s: variable sin definir", list[i].ID)
			}
			re, err := regexp.Compile(list[i].Pattern)
			if err != nil {
				return nil, fmt.Errorf("patrón %s: %w", list[i].ID, err)
			}
			list[i].re = re
			if list[i].loose, err = regexp.Compile(unanchor(list[i].Pattern)); err != nil {
				return nil, fmt.Errorf("patrón %s: %w", list[i].ID, err)
			}
		}
	}
	return &c, nil
}

// unanchor quita el ancla de inicio de línea para buscar a mitad de un comando.
func unanchor(p string) string {
	switch {
	case strings.HasPrefix(p, "(?i)^"):
		return "(?i)" + p[len("(?i)^"):]
	case strings.HasPrefix(p, "^"):
		return p[1:]
	}
	return p
}

// Default carga los patrones del estándar default embebido.
func Default() (*Config, error) {
	data, err := fs.ReadFile(coyote.Standards, "standards/default/attribution.yaml")
	if err != nil {
		return nil, err
	}
	return Load(data)
}

// Check reporta las líneas con atribución. loose=true busca también a mitad
// de línea, útil para comandos como git commit -m "..." -m "...".
func (c *Config) Check(text string, loose bool) []Finding {
	var out []Finding
	for i, line := range strings.Split(text, "\n") {
		if id := c.match(line, loose); id != "" {
			out = append(out, Finding{Line: i + 1, PatternID: id, Text: strings.TrimSpace(line)})
		}
	}
	return out
}

func (c *Config) match(line string, loose bool) string {
	for _, p := range c.LinePatterns {
		if p.re.MatchString(line) || (loose && p.loose.MatchString(line)) {
			return p.ID
		}
	}
	for _, p := range c.PhrasePatterns {
		if p.re.MatchString(line) {
			return p.ID
		}
	}
	return ""
}

var spaces = regexp.MustCompile(`[ \t]{2,}`)

// Scrub elimina líneas de atribución y frases de atribución dentro de líneas
// útiles; es lo que se aplica a archivos. Con commitMsg=true no reescribe los
// comentarios de git (líneas con #), salvo los que son atribución: con
// git commit -m, git conserva esas líneas en el commit.
func (c *Config) Scrub(text string, commitMsg bool) (string, []Finding) {
	return c.scrub(text, commitMsg, true)
}

// ScrubLines quita solo las líneas de atribución (trailers, pies, enlaces).
// Es lo que se aplica a un mensaje de commit: las frases dentro del texto no
// se reescriben en silencio; Phrases las reporta para rechazar el commit.
func (c *Config) ScrubLines(text string, commitMsg bool) (string, []Finding) {
	return c.scrub(text, commitMsg, false)
}

// Phrases reporta frases de atribución (no líneas completas) en text.
func (c *Config) Phrases(text string, commitMsg bool) []Finding {
	var out []Finding
	for i, line := range strings.Split(text, "\n") {
		if commitMsg && strings.HasPrefix(line, "#") {
			continue
		}
		for _, p := range c.PhrasePatterns {
			if p.re.MatchString(line) {
				out = append(out, Finding{i + 1, p.ID, strings.TrimSpace(line)})
				break
			}
		}
	}
	return out
}

func (c *Config) scrub(text string, commitMsg, phrases bool) (string, []Finding) {
	var keep []string
	var found []Finding
	for i, line := range strings.Split(text, "\n") {
		if id := c.lineMatch(line); id != "" {
			found = append(found, Finding{i + 1, id, strings.TrimSpace(line)})
			continue
		}
		if !phrases || (commitMsg && strings.HasPrefix(line, "#")) {
			keep = append(keep, line)
			continue
		}
		cleaned := line
		for _, p := range c.PhrasePatterns {
			if p.re.MatchString(cleaned) {
				found = append(found, Finding{i + 1, p.ID, strings.TrimSpace(line)})
				cleaned = p.re.ReplaceAllString(cleaned, p.replacement())
			}
		}
		if cleaned != line {
			cleaned = strings.TrimRight(spaces.ReplaceAllString(cleaned, " "), " ,;:-")
			if strings.TrimSpace(cleaned) == "" {
				continue
			}
		}
		keep = append(keep, cleaned)
	}
	out := strings.TrimRight(strings.Join(keep, "\n"), " \t\n")
	if out != "" {
		out += "\n"
	}
	return out, found
}

// replacement conserva el grupo keep del patrón (la puntuación final) si existe.
func (p Pattern) replacement() string {
	for _, name := range p.re.SubexpNames() {
		if name == "keep" {
			return "${keep}"
		}
	}
	return ""
}

// AIIdentity informa si una identidad de git (autor o committer) es la de una
// herramienta de IA: se evalúa como si fuera un trailer de coautoría.
func (c *Config) AIIdentity(name, email string) (Finding, bool) {
	line := "Co-authored-by: " + strings.TrimSpace(name) + " <" + strings.TrimSpace(email) + ">"
	if id := c.lineMatch(line); id != "" {
		return Finding{Line: 1, PatternID: id, Text: strings.TrimSpace(name) + " <" + strings.TrimSpace(email) + ">"}, true
	}
	return Finding{}, false
}

func (c *Config) lineMatch(line string) string {
	for _, p := range c.LinePatterns {
		if p.re.MatchString(line) {
			return p.ID
		}
	}
	return ""
}

// Allowed informa si la ruta está en la lista de rutas donde los patrones
// aparecen como ejemplos y no como atribución.
func (c *Config) Allowed(rel string) bool {
	return glob.Any(c.AllowPaths, rel)
}

// EmptyMessage informa si un mensaje de commit quedó sin contenido útil.
func EmptyMessage(msg string) bool {
	for _, l := range strings.Split(msg, "\n") {
		if t := strings.TrimSpace(l); t != "" && !strings.HasPrefix(t, "#") {
			return false
		}
	}
	return true
}
