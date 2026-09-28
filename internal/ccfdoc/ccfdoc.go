// Package ccfdoc implementa CCF-doc v1: README.coyote.md (identidad del repo) y
// CONTEXT.coyote.md (contexto vivo). Cada archivo tiene frontmatter YAML y una
// línea por hecho, con tipos cerrados y tope de tokens. La especificación está
// en docs/specs/ccf-doc-v1.md.
package ccfdoc

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/Emmanuel93/coyote/internal/tokens"
)

// Kind distingue los dos documentos.
type Kind int

// Tipos de documento.
const (
	Unknown Kind = iota
	Readme
	Context
)

// Nombres y topes de CCF-doc v1.
const (
	ReadmeFile                 = "README.coyote.md"
	ContextFile                = "CONTEXT.coyote.md"
	ReadmeMaxTokens            = 300
	ReadmeMaxTokensWithModules = 400
	ContextMaxTokens           = 1500
	MaxEntryWords              = 20
	MaxPurposeWords            = 30
)

// ProjectTypes son los tipos de proyecto válidos; también son perfiles del estándar.
var ProjectTypes = []string{"backend", "mobile", "web", "infra", "hub", "product", "tool", "library", "docs", "data", "other"}

// ValidProjectType informa si t es un tipo de proyecto válido.
func ValidProjectType(t string) bool {
	for _, v := range ProjectTypes {
		if v == t {
			return true
		}
	}
	return false
}

// readmeTypes define cuántos campos lleva cada tipo después del tipo (mínimo, máximo).
var readmeTypes = map[string][2]int{
	"purpose": {1, 1}, "run": {1, 1}, "test": {1, 1}, "build": {1, 1},
	"entry": {2, 2}, "mod": {3, 4}, "docs": {2, 2}, "dep": {2, 2},
}

// ContextTypes son los tipos de CONTEXT.coyote.md con su significado.
var ContextTypes = map[string]string{
	"inv":  "invariante que no se rompe",
	"dec":  "decisión vigente",
	"gap":  "trampa o brecha conocida",
	"how":  "cómo hacer algo",
	"term": "término del dominio",
	"risk": "riesgo abierto",
	"todo": "pendiente",
}

// ContextTypeNames devuelve los tipos de contexto en orden alfabético.
func ContextTypeNames() []string {
	out := make([]string, 0, len(ContextTypes))
	for k := range ContextTypes {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Entry es una línea tipo|campo|...
type Entry struct {
	Line   int
	Type   string
	Fields []string
}

// Issue es un problema de formato; Error=false indica advertencia.
type Issue struct {
	Line  int
	Error bool
	Msg   string
}

func (i Issue) String() string {
	level := "advertencia"
	if i.Error {
		level = "error"
	}
	if i.Line > 0 {
		return fmt.Sprintf("línea %d: %s: %s", i.Line, level, i.Msg)
	}
	return level + ": " + i.Msg
}

// HasErrors informa si hay algún error (no solo advertencias).
func HasErrors(issues []Issue) bool {
	for _, i := range issues {
		if i.Error {
			return true
		}
	}
	return false
}

// Doc es un documento CCF-doc interpretado.
type Doc struct {
	Kind    Kind
	Name    string
	Front   map[string]any
	Entries []Entry
	Tokens  int
}

// KindOf deduce el tipo de documento por el nombre de archivo.
func KindOf(name string) Kind {
	switch strings.ToLower(filepath.Base(name)) {
	case strings.ToLower(ReadmeFile):
		return Readme
	case strings.ToLower(ContextFile):
		return Context
	}
	return Unknown
}

// Parse interpreta el documento; devuelve problemas de estructura.
func Parse(name string, data []byte) (*Doc, []Issue) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	d := &Doc{Kind: KindOf(name), Name: name, Front: map[string]any{}, Tokens: tokens.Estimate(text)}
	var issues []Issue
	lines := strings.Split(text, "\n")
	i := 0
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		end := -1
		for j := 1; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) == "---" {
				end = j
				break
			}
		}
		if end < 0 {
			return d, append(issues, Issue{1, true, "frontmatter sin cierre ---"})
		}
		if err := yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &d.Front); err != nil {
			issues = append(issues, Issue{1, true, "frontmatter YAML inválido: " + err.Error()})
		}
		if d.Front == nil {
			d.Front = map[string]any{}
		}
		i = end + 1
	} else {
		issues = append(issues, Issue{1, true, "falta el frontmatter (--- coyote: 1 ... ---)"})
	}
	for ; i < len(lines); i++ {
		raw := strings.TrimSpace(lines[i])
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		parts := strings.Split(raw, "|")
		for k := range parts {
			parts[k] = strings.TrimSpace(parts[k])
		}
		d.Entries = append(d.Entries, Entry{Line: i + 1, Type: parts[0], Fields: parts[1:]})
	}
	return d, issues
}

// Limit devuelve el tope de tokens del documento.
func (d *Doc) Limit() int {
	switch d.Kind {
	case Readme:
		if len(d.All("mod")) > 0 {
			return ReadmeMaxTokensWithModules
		}
		return ReadmeMaxTokens
	case Context:
		return ContextMaxTokens
	}
	return 0
}

// Validate aplica la gramática y los topes de CCF-doc v1.
func (d *Doc) Validate() []Issue {
	var out []Issue
	add := func(line int, isErr bool, format string, a ...any) {
		out = append(out, Issue{line, isErr, fmt.Sprintf(format, a...)})
	}
	if fmt.Sprint(d.Front["coyote"]) != "1" {
		add(1, true, "frontmatter: falta coyote: 1")
	}
	if strings.TrimSpace(d.FrontString("repo")) == "" {
		add(1, true, "frontmatter: falta repo")
	}
	switch d.Kind {
	case Readme:
		d.validateReadme(add)
	case Context:
		d.validateContext(add)
	default:
		add(0, true, "nombre no reconocido: se espera %s o %s", ReadmeFile, ContextFile)
	}
	if limit := d.Limit(); limit > 0 && d.Tokens > limit {
		hint := ""
		if d.Kind == Context {
			hint = "; archiva entradas antiguas"
		}
		add(0, true, "excede el tope: ~%d de %d tokens%s", d.Tokens, limit, hint)
	}
	return out
}

func (d *Doc) validateReadme(add func(int, bool, string, ...any)) {
	t := d.FrontString("type")
	switch {
	case t == "":
		add(1, true, "frontmatter: falta type (%s)", strings.Join(ProjectTypes, ", "))
	case !ValidProjectType(t):
		add(1, false, "frontmatter: type %q no es estándar (%s)", t, strings.Join(ProjectTypes, ", "))
	}
	if len(d.FrontList("owners")) == 0 {
		add(1, false, "frontmatter: sin owners")
	}
	counts := map[string]int{}
	for _, e := range d.Entries {
		ar, ok := readmeTypes[e.Type]
		if !ok {
			if _, isCtx := ContextTypes[e.Type]; isCtx {
				add(e.Line, true, "%s es un tipo de CONTEXT.coyote.md", e.Type)
			} else {
				add(e.Line, true, "tipo desconocido %q", e.Type)
			}
			continue
		}
		counts[e.Type]++
		if len(e.Fields) < ar[0] || len(e.Fields) > ar[1] {
			add(e.Line, true, "%s espera %s campos y tiene %d", e.Type, arity(ar), len(e.Fields))
			continue
		}
		for k, f := range e.Fields {
			if f == "" {
				add(e.Line, true, "%s: campo %d vacío", e.Type, k+1)
			}
			if strings.Contains(strings.ToUpper(f), "TODO") {
				add(e.Line, false, "%s: completa el campo (tiene TODO)", e.Type)
			}
		}
		switch e.Type {
		case "purpose":
			if n := words(e.Fields[0]); n > MaxPurposeWords {
				add(e.Line, true, "purpose tiene %d palabras; máximo %d", n, MaxPurposeWords)
			}
		case "entry", "docs", "dep", "mod":
			desc := e.Fields[1]
			if n := words(desc); n > MaxEntryWords {
				add(e.Line, true, "%s: descripción de %d palabras; máximo %d", e.Type, n, MaxEntryWords)
			}
		}
	}
	switch counts["purpose"] {
	case 0:
		add(0, true, "falta purpose")
	case 1:
	default:
		add(0, true, "purpose repetido")
	}
	if counts["run"] == 0 {
		add(0, false, "falta run")
	}
	if counts["test"] == 0 {
		add(0, false, "falta test")
	}
}

func (d *Doc) validateContext(add func(int, bool, string, ...any)) {
	for _, e := range d.Entries {
		if _, ok := ContextTypes[e.Type]; !ok {
			if _, isReadme := readmeTypes[e.Type]; isReadme {
				add(e.Line, true, "%s es un tipo de README.coyote.md", e.Type)
			} else {
				add(e.Line, true, "tipo desconocido %q (%s)", e.Type, strings.Join(ContextTypeNames(), ", "))
			}
			continue
		}
		if len(e.Fields) != 3 {
			add(e.Line, true, "%s espera tipo|ámbito|texto|ref y tiene %d campos", e.Type, len(e.Fields)+1)
			continue
		}
		scope, text, ref := e.Fields[0], e.Fields[1], e.Fields[2]
		if scope == "" {
			add(e.Line, true, "ámbito vacío")
		}
		if text == "" {
			add(e.Line, true, "texto vacío")
		}
		if ref == "" {
			add(e.Line, true, "referencia vacía; usa - si no hay")
		}
		if n := words(text); n > MaxEntryWords {
			add(e.Line, true, "texto de %d palabras; máximo %d", n, MaxEntryWords)
		}
	}
}

func arity(ar [2]int) string {
	if ar[0] == ar[1] {
		return fmt.Sprint(ar[0])
	}
	return fmt.Sprintf("%d a %d", ar[0], ar[1])
}

func words(s string) int { return len(strings.Fields(s)) }

// FrontString devuelve una clave del frontmatter como texto.
func (d *Doc) FrontString(key string) string {
	if v, ok := d.Front[key]; ok && v != nil {
		if s, ok := v.(string); ok {
			return s
		}
		return fmt.Sprint(v)
	}
	return ""
}

// FrontList devuelve una clave del frontmatter como lista de textos.
func (d *Doc) FrontList(key string) []string {
	list, _ := d.Front[key].([]any)
	var out []string
	for _, v := range list {
		if s := strings.TrimSpace(fmt.Sprint(v)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// First devuelve la primera entrada del tipo dado, o nil.
func (d *Doc) First(typ string) *Entry {
	for i := range d.Entries {
		if d.Entries[i].Type == typ {
			return &d.Entries[i]
		}
	}
	return nil
}

// All devuelve las entradas del tipo dado.
func (d *Doc) All(typ string) []Entry {
	var out []Entry
	for _, e := range d.Entries {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

// Waiver es una dispensa de regla declarada por el proyecto.
type Waiver struct{ ID, Reason, ADR string }

// Profile devuelve el perfil del estándar: standards.profile o type.
func (d *Doc) Profile() string {
	if st, ok := d.Front["standards"].(map[string]any); ok {
		if p, ok := st["profile"].(string); ok && p != "" {
			return p
		}
	}
	return d.FrontString("type")
}

// Waivers devuelve las dispensas de standards.waive: [R9] o [{id, reason, adr}].
func (d *Doc) Waivers() []Waiver {
	st, _ := d.Front["standards"].(map[string]any)
	list, _ := st["waive"].([]any)
	var out []Waiver
	for _, it := range list {
		switch v := it.(type) {
		case string:
			out = append(out, Waiver{ID: v})
		case map[string]any:
			// Solo cuentan motivos y ADRs escritos como texto; [] o false no son un motivo.
			reason, _ := v["reason"].(string)
			adr, _ := v["adr"].(string)
			out = append(out, Waiver{ID: str(v["id"]), Reason: strings.TrimSpace(reason), ADR: strings.TrimSpace(adr)})
		}
	}
	return out
}

func str(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// FormatEntry arma y valida una línea de CONTEXT.coyote.md.
func FormatEntry(typ, scope, text, ref string) (string, error) {
	if _, ok := ContextTypes[typ]; !ok {
		return "", fmt.Errorf("tipo %q inválido; usa %s", typ, strings.Join(ContextTypeNames(), ", "))
	}
	clean := func(s string) string { return strings.Join(strings.Fields(strings.ReplaceAll(s, "|", "/")), " ") }
	scope, text, ref = clean(scope), clean(text), clean(ref)
	if scope == "" {
		scope = "general"
	}
	if ref == "" {
		ref = "-"
	}
	if text == "" {
		return "", fmt.Errorf("el texto está vacío")
	}
	if n := words(text); n > MaxEntryWords {
		return "", fmt.Errorf("el texto tiene %d palabras; máximo %d", n, MaxEntryWords)
	}
	if strings.ContainsAny(scope+ref, " ") {
		return "", fmt.Errorf("ámbito y referencia van sin espacios")
	}
	return strings.Join([]string{typ, scope, text, ref}, "|"), nil
}

// AppendEntry agrega entry al final y actualiza updated: en el frontmatter.
func AppendEntry(content, entry, today string) string {
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		for j := 1; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) != "---" {
				continue
			}
			found := false
			for k := 1; k < j; k++ {
				if strings.HasPrefix(lines[k], "updated:") {
					lines[k] = "updated: " + today
					found = true
				}
			}
			if !found {
				lines = append(lines[:j], append([]string{"updated: " + today}, lines[j:]...)...)
			}
			break
		}
	}
	out := strings.Join(lines, "\n")
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out + entry + "\n"
}
