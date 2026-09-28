// Package attribution detecta y elimina atribuciones a herramientas o modelos
// de IA en mensajes de commit, PRs y documentos (regla R15, ADR-0005).
//
// Hay tres clases de hallazgo:
//   - identidades: trailers Co-authored-by, autor o committer que son una
//     herramienta (su nombre de bot o su buzón), nunca una persona;
//   - líneas: pies de firma, trailers de sesión y enlaces a sesiones; la línea
//     completa sobra y se elimina;
//   - frases: una atribución dentro de un texto útil. No se reescribe nunca:
//     se reporta para que la persona la corrija.
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

// CoauthorID es el identificador de los trailers Co-authored-by de herramientas.
const CoauthorID = "coauthor-ai"

// Pattern es una expresión regular con identificador.
type Pattern struct {
	ID      string `yaml:"id"`
	Pattern string `yaml:"pattern"`
	re      *regexp.Regexp
	loose   *regexp.Regexp // sin anclar al inicio de línea, para comandos de una sola línea
}

// Identity reconoce identidades de herramientas: el nombre (con límites de
// palabra) o el buzón completo. Así una persona llamada Claude, Devin o Haider,
// o alguien con correo de un proveedor, nunca cuenta.
type Identity struct {
	Names  string `yaml:"names"`
	Emails string `yaml:"emails"`
	names  *regexp.Regexp
	emails *regexp.Regexp
}

// Config es el contenido de attribution.yaml.
type Config struct {
	Version        int               `yaml:"version"`
	Vars           map[string]string `yaml:"vars"`
	Identity       Identity          `yaml:"identity"`
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

var (
	trailerRe      = regexp.MustCompile(`(?i)^\s*co-authored-by\s*:\s*(.*)$`)
	looseTrailerRe = regexp.MustCompile(`(?i)co-authored-by\s*:\s*([^<\n"'\\]*)(?:<([^>\n]*)>)?`)
	scissorsRe     = regexp.MustCompile(`^\S -+ >8 -+\s*$`)
)

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
	compile := func(id, p string) (*regexp.Regexp, error) {
		p = expand.Replace(p)
		if strings.Contains(p, "{{") {
			return nil, fmt.Errorf("patrón %s: variable sin definir", id)
		}
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("patrón %s: %w", id, err)
		}
		return re, nil
	}
	var err error
	if c.Identity.names, err = compile("identity.names", c.Identity.Names); err != nil {
		return nil, err
	}
	if c.Identity.emails, err = compile("identity.emails", c.Identity.Emails); err != nil {
		return nil, err
	}
	for _, list := range [][]Pattern{c.LinePatterns, c.PhrasePatterns} {
		for i := range list {
			list[i].Pattern = expand.Replace(list[i].Pattern)
			if list[i].re, err = compile(list[i].ID, list[i].Pattern); err != nil {
				return nil, err
			}
			if list[i].loose, err = compile(list[i].ID, unanchor(list[i].Pattern)); err != nil {
				return nil, err
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

// AIIdentity informa si una identidad de git (autor, committer o coautor) es
// la de una herramienta de IA.
func (c *Config) AIIdentity(name, email string) (Finding, bool) {
	name, email = strings.TrimSpace(name), strings.Trim(strings.TrimSpace(email), "<>")
	if (name != "" && c.Identity.names.MatchString(name)) || (email != "" && c.Identity.emails.MatchString(email)) {
		text := name
		if email != "" {
			text += " <" + email + ">"
		}
		return Finding{Line: 1, PatternID: CoauthorID, Text: strings.TrimSpace(text)}, true
	}
	return Finding{}, false
}

// splitIdent separa "Nombre <correo>" en sus partes.
func splitIdent(s string) (name, email string) {
	s = strings.TrimSpace(s)
	lt := strings.Index(s, "<")
	if lt < 0 {
		return s, ""
	}
	gt := strings.Index(s[lt:], ">")
	if gt < 0 {
		return strings.TrimSpace(s[:lt]), strings.TrimSpace(s[lt+1:])
	}
	return strings.TrimSpace(s[:lt]), strings.TrimSpace(s[lt+1 : lt+gt])
}

// lineMatch reconoce una línea que es atribución completa.
func (c *Config) lineMatch(line string) string {
	if m := trailerRe.FindStringSubmatch(line); m != nil {
		if _, ok := c.AIIdentity(splitIdent(m[1])); ok {
			return CoauthorID
		}
	}
	for _, p := range c.LinePatterns {
		if p.re.MatchString(line) {
			return p.ID
		}
	}
	return ""
}

func (c *Config) phraseMatch(line string) string {
	for _, p := range c.PhrasePatterns {
		if p.re.MatchString(line) {
			return p.ID
		}
	}
	return ""
}

// Check reporta las líneas con atribución (identidades, líneas y frases).
// loose=true busca también a mitad de línea, útil para comandos como
// git commit -m "..." -m "...".
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
	if id := c.lineMatch(line); id != "" {
		return id
	}
	if loose {
		for _, m := range looseTrailerRe.FindAllStringSubmatch(line, -1) {
			name, email := m[1], m[2]
			if _, ok := c.AIIdentity(name, email); ok {
				return CoauthorID
			}
		}
		for _, p := range c.LinePatterns {
			if p.loose.MatchString(line) {
				return p.ID
			}
		}
	}
	return c.phraseMatch(line)
}

// body separa un mensaje de commit en lo que git conserva y lo que va después
// de la línea de tijeras (# --- >8 ---): el diff de git commit -v, que git
// descarta y que no se revisa ni se toca.
func body(text string, commitMsg bool) (lines []string, tail []string) {
	lines = strings.Split(text, "\n")
	if !commitMsg {
		return lines, nil
	}
	for i, l := range lines {
		if scissorsRe.MatchString(l) {
			return lines[:i], lines[i:]
		}
	}
	return lines, nil
}

// ScrubLines quita las líneas que son atribución completa: trailers de
// herramientas, pies de firma, trailers y enlaces de sesión. No toca las
// frases dentro de un texto útil: Phrases las reporta para que la persona las
// reescriba. Con commitMsg=true respeta la línea de tijeras de git y los
// comentarios (#) que no son atribución.
func (c *Config) ScrubLines(text string, commitMsg bool) (string, []Finding) {
	lines, tail := body(text, commitMsg)
	var keep []string
	var found []Finding
	for i, line := range lines {
		if id := c.lineMatch(line); id != "" {
			found = append(found, Finding{i + 1, id, strings.TrimSpace(line)})
			continue
		}
		keep = append(keep, line)
	}
	out := strings.TrimRight(strings.Join(keep, "\n"), " \t\n")
	if out != "" {
		out += "\n"
	}
	if len(tail) > 0 {
		out += strings.Join(tail, "\n")
	}
	return out, found
}

// Phrases reporta frases de atribución dentro de texto útil. Con
// commitMsg=true omite los comentarios de git y lo que va después de las tijeras.
func (c *Config) Phrases(text string, commitMsg bool) []Finding {
	lines, _ := body(text, commitMsg)
	var out []Finding
	for i, line := range lines {
		if commitMsg && strings.HasPrefix(line, "#") {
			continue
		}
		if id := c.phraseMatch(line); id != "" {
			out = append(out, Finding{i + 1, id, strings.TrimSpace(line)})
		}
	}
	return out
}

// Allowed informa si la ruta está en la lista de rutas donde los patrones
// aparecen como datos de prueba y no como atribución.
func (c *Config) Allowed(rel string) bool {
	return glob.Any(c.AllowPaths, rel)
}

// EmptyMessage informa si un mensaje de commit quedó sin contenido útil.
func EmptyMessage(msg string) bool {
	lines, _ := body(msg, true)
	for _, l := range lines {
		if t := strings.TrimSpace(l); t != "" && !strings.HasPrefix(t, "#") {
			return false
		}
	}
	return true
}
