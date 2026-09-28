// Package index arma el índice local de un proyecto coyote a partir de lo que
// ya está en git: README.coyote.md, CONTEXT.coyote.md, ADRs, documentos
// citados, workstreams y el ledger. Vive en memoria y se guarda como caché en
// .coyote/index.json; nunca contradice a git porque se reconstruye desde él
// (ADR-0007). Respeta .coyoteignore (R12).
package index

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/Emmanuel93/coyote/internal/ccf"
	"github.com/Emmanuel93/coyote/internal/ccfdoc"
	"github.com/Emmanuel93/coyote/internal/fsx"
	"github.com/Emmanuel93/coyote/internal/glob"
	"github.com/Emmanuel93/coyote/internal/tokens"
	"github.com/Emmanuel93/coyote/internal/userdir"
)

// Clases de fragmento.
const (
	KindReadme     = "readme"
	KindContext    = "context"
	KindADR        = "adr"
	KindDoc        = "doc"
	KindWorkstream = "workstream"
	KindEvent      = "event"
)

// CachePath es la caché del índice, relativa a la raíz; .coyote/ no se versiona.
const CachePath = ".coyote/index.json"

const cacheVersion = 3

// Chunk es la unidad que se busca y se entrega como contexto.
type Chunk struct {
	Kind   string    `json:"k"`
	Type   string    `json:"t,omitempty"` // inv, dec, purpose, estado del ADR, tipo de evento...
	Scope  string    `json:"s,omitempty"`
	Title  string    `json:"h,omitempty"`
	Text   string    `json:"x"`
	Path   string    `json:"p"`
	Line   int       `json:"l,omitempty"`
	Time   time.Time `json:"d,omitempty"`
	Tokens int       `json:"n"`
}

// Ref devuelve dónde leer el fragmento completo: ruta#Llínea.
func (c Chunk) Ref() string {
	if c.Line > 0 {
		return fmt.Sprintf("%s#L%d", c.Path, c.Line)
	}
	return c.Path
}

// Index es el índice de un proyecto.
type Index struct {
	Root   string
	Chunks []Chunk
	// Reused y Parsed cuentan archivos tomados de la caché y leídos de nuevo.
	Reused, Parsed int
	bm25           *bm25
}

type fileEntry struct {
	Hash   string  `json:"sha256"`
	MAC    string  `json:"mac"` // HMAC con la clave local: una caché ajena o fabricada no valida
	Chunks []Chunk `json:"chunks"`
}

func entryMAC(key []byte, rel string, e *fileEntry) string {
	m := hmac.New(sha256.New, key)
	chunks, _ := json.Marshal(e.Chunks)
	m.Write([]byte(rel + "\x00" + e.Hash + "\x00"))
	m.Write(chunks)
	return hex.EncodeToString(m.Sum(nil))
}

// maxFile es el tamaño máximo de un archivo indexable.
const maxFile = 1 << 20

type cache struct {
	Version int                   `json:"version"`
	Files   map[string]*fileEntry `json:"files"`
}

// Build arma el índice de root usando la caché cuando los archivos no cambiaron.
func Build(root string) (*Index, error) {
	files, err := candidates(root)
	if err != nil {
		return nil, err
	}
	key, keyErr := userdir.Key("index")
	old := &cache{Files: map[string]*fileEntry{}}
	if keyErr == nil {
		old = readCache(root)
	}
	fresh := &cache{Version: cacheVersion, Files: map[string]*fileEntry{}}
	ix := &Index{Root: root}
	for _, rel := range files {
		data, err := fsx.ReadFile(root, rel, maxFile)
		if err != nil {
			continue // ilegible, especial, enorme o detrás de un symlink: no se indexa
		}
		// La caché se reutiliza solo si el contenido es idéntico (ADR-0007): fechas
		// y tamaños se pueden fabricar, el hash no.
		sum := sha256.Sum256(data)
		hash := hex.EncodeToString(sum[:])
		if e, ok := old.Files[rel]; ok && e.Hash == hash && hmac.Equal([]byte(e.MAC), []byte(entryMAC(key, rel, e))) {
			fresh.Files[rel] = e
			ix.Reused++
			continue
		}
		chunks, err := parseFile(rel, data)
		if err != nil {
			continue // un archivo ilegible no impide indexar el resto
		}
		e := &fileEntry{Hash: hash, Chunks: chunks}
		if keyErr == nil {
			e.MAC = entryMAC(key, rel, e)
		}
		fresh.Files[rel] = e
		ix.Parsed++
	}
	for _, rel := range files {
		if e, ok := fresh.Files[rel]; ok {
			ix.Chunks = append(ix.Chunks, e.Chunks...)
		}
	}
	if keyErr == nil && (ix.Parsed > 0 || len(fresh.Files) != len(old.Files)) {
		writeCache(root, fresh)
	}
	ix.bm25 = newBM25(ix.Chunks)
	return ix, nil
}

func readCache(root string) *cache {
	c := &cache{Files: map[string]*fileEntry{}}
	data, err := fsx.ReadFile(root, CachePath, 64<<20)
	if err != nil {
		return c
	}
	var got cache
	if json.Unmarshal(data, &got) != nil || got.Version != cacheVersion || got.Files == nil {
		return c
	}
	return &got
}

// writeCache guarda la caché si puede; si no, el índice funciona igual.
func writeCache(root string, c *cache) {
	if fsx.NoSymlinks(root, CachePath) != nil {
		return
	}
	p := filepath.Join(root, filepath.FromSlash(CachePath))
	if os.MkdirAll(filepath.Dir(p), 0o755) != nil {
		return
	}
	data, err := json.Marshal(c)
	if err != nil {
		return
	}
	tmp := p + ".tmp"
	if os.WriteFile(tmp, data, 0o644) == nil {
		_ = os.Rename(tmp, p)
	}
}

// candidates lista los archivos que entran al índice, sin los excluidos por .coyoteignore.
func candidates(root string) ([]string, error) {
	ignore := ignorePatterns(root)
	seen := map[string]bool{}
	var out []string
	add := func(rel string) {
		rel = filepath.ToSlash(filepath.Clean(rel))
		if seen[rel] || rel == ".." || strings.HasPrefix(rel, "../") || filepath.IsAbs(rel) || Ignored(ignore, rel) || !fsx.Regular(root, rel) {
			return
		}
		seen[rel] = true
		out = append(out, rel)
	}
	walk := func(dir string, match func(string) bool) {
		base := filepath.Join(root, filepath.FromSlash(dir))
		if fsx.NoSymlinks(root, dir) != nil {
			return
		}
		_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.Type()&fs.ModeSymlink != 0 {
				return nil
			}
			if d.IsDir() {
				return nil
			}
			if rel, err := filepath.Rel(root, p); err == nil && match(rel) {
				add(rel)
			}
			return nil
		})
	}
	for _, f := range []string{ccfdoc.ReadmeFile, ccfdoc.ContextFile, "README.md"} {
		add(f)
	}
	md := func(p string) bool { return strings.HasSuffix(p, ".md") }
	walk("coyote/decisions", md)
	walk("coyote/workstreams", func(p string) bool { return md(p) || strings.HasSuffix(p, ".yaml") })
	walk("coyote/ledger", func(p string) bool { return strings.HasSuffix(p, ".ccf") })
	if d, err := fsx.ReadFile(root, ccfdoc.ReadmeFile, maxFile); err == nil {
		doc, _ := ccfdoc.Parse(ccfdoc.ReadmeFile, d)
		for _, e := range doc.All("docs") {
			if len(e.Fields) == 0 {
				continue
			}
			p := filepath.ToSlash(filepath.Clean(e.Fields[0]))
			if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err == nil && info.IsDir() {
				walk(p, md)
			} else if md(p) {
				add(p)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// Ignored aplica .coyoteignore con la semántica de .gitignore para carpetas:
// un patrón que coincide con una carpeta excluye todo lo que tiene debajo,
// termine o no en "/".
func Ignored(patterns []string, rel string) bool {
	parts := strings.Split(rel, "/")
	for i := 1; i <= len(parts); i++ {
		if glob.Any(patterns, strings.Join(parts[:i], "/")) {
			return true
		}
	}
	return false
}

func ignorePatterns(root string) []string {
	data, err := fsx.ReadFile(root, ".coyoteignore", maxFile)
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(string(data), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") || strings.HasPrefix(l, "!") {
			continue
		}
		out = append(out, l)
	}
	return out
}

func parseFile(rel string, data []byte) ([]Chunk, error) {
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, fmt.Errorf("%s es binario", rel)
	}
	switch {
	case rel == ccfdoc.ReadmeFile || rel == ccfdoc.ContextFile:
		return parseCCFDoc(rel, data), nil
	case strings.HasPrefix(rel, "coyote/ledger/"):
		return parseLedger(rel, data), nil
	case strings.HasPrefix(rel, "coyote/decisions/"):
		return parseMarkdown(rel, data, KindADR), nil
	case strings.HasPrefix(rel, "coyote/workstreams/") && strings.HasSuffix(rel, ".yaml"):
		return parseYAML(rel, data, KindWorkstream), nil
	case strings.HasPrefix(rel, "coyote/workstreams/"):
		return parseMarkdown(rel, data, KindWorkstream), nil
	}
	return parseMarkdown(rel, data, KindDoc), nil
}

func chunk(c Chunk) Chunk {
	c.Text = strings.Join(strings.Fields(c.Text), " ")
	c.Tokens = tokens.Estimate(c.Title + " " + c.Text)
	return c
}

func parseCCFDoc(rel string, data []byte) []Chunk {
	d, _ := ccfdoc.Parse(rel, data)
	kind := KindReadme
	if d.Kind == ccfdoc.Context {
		kind = KindContext
	}
	var out []Chunk
	for _, e := range d.Entries {
		c := Chunk{Kind: kind, Type: e.Type, Path: rel, Line: e.Line}
		switch {
		case kind == KindContext && len(e.Fields) >= 2:
			c.Scope, c.Text = e.Fields[0], e.Fields[1]
			if len(e.Fields) > 2 && e.Fields[2] != "-" {
				c.Title = e.Fields[2] // referencia al detalle
			}
		case e.Type == "mod" && len(e.Fields) >= 3:
			c.Scope, c.Text, c.Title = e.Fields[0], e.Fields[1], e.Fields[2]
		default:
			c.Text = strings.Join(e.Fields, " · ")
		}
		out = append(out, chunk(c))
	}
	return out
}

func parseLedger(rel string, data []byte) []Chunk {
	var out []Chunk
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	n := 0
	for sc.Scan() {
		n++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		l, err := ccf.Parse(raw)
		if err != nil {
			continue
		}
		text := l.What
		if len(l.Refs) > 0 {
			text += " (" + strings.Join(l.Refs, " ") + ")"
		}
		out = append(out, chunk(Chunk{Kind: KindEvent, Type: l.Type, Scope: l.Scope, Title: l.Actor,
			Text: text, Path: rel, Line: n, Time: l.TS}))
	}
	return out
}

var (
	headingRe = regexp.MustCompile(`^(#{1,4})\s+(.+?)\s*#*\s*$`)
	fenceRe   = regexp.MustCompile("^\\s*(```|~~~)")
)

// maxSectionWords parte secciones largas en fragmentos manejables.
const maxSectionWords = 150

// parseMarkdown parte un documento por encabezados; cada sección larga se
// divide por párrafos. El título del documento acompaña a cada fragmento.
func parseMarkdown(rel string, data []byte, kind string) []Chunk {
	text := string(data)
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	start := 0
	status := ""
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		for j := 1; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) == "---" {
				var front map[string]any
				if yaml.Unmarshal([]byte(strings.Join(lines[1:j], "\n")), &front) == nil {
					if s, ok := front["status"].(string); ok {
						status = s
					}
				}
				start = j + 1
				break
			}
		}
	}
	docTitle := ""
	var out []Chunk
	heading, secLine := "", 0
	var para []string
	paraLine := 0
	inFence := false
	flush := func() {
		body := strings.TrimSpace(strings.Join(para, " "))
		para = para[:0]
		if body == "" && heading == "" {
			return
		}
		title := heading
		if docTitle != "" && heading != docTitle {
			title = docTitle + " — " + heading
		}
		words := strings.Fields(body)
		line := paraLine
		if line == 0 {
			line = secLine
		}
		for len(words) > 0 || body == "" {
			n := len(words)
			if n > maxSectionWords {
				n = maxSectionWords
			}
			out = append(out, chunk(Chunk{Kind: kind, Type: status, Title: strings.TrimSpace(title),
				Text: strings.Join(words[:n], " "), Path: rel, Line: line}))
			words = words[n:]
			if body == "" {
				break
			}
		}
	}
	for i := start; i < len(lines); i++ {
		l := lines[i]
		if fenceRe.MatchString(l) {
			inFence = !inFence
		}
		if !inFence {
			if m := headingRe.FindStringSubmatch(l); m != nil {
				flush()
				heading, secLine, paraLine = strings.TrimSpace(m[2]), i+1, 0
				if len(m[1]) == 1 && docTitle == "" {
					docTitle = heading
				}
				continue
			}
		}
		if strings.TrimSpace(l) == "" && !inFence {
			// Un párrafo termina; se agrupan párrafos hasta el tope de palabras.
			if len(strings.Fields(strings.Join(para, " "))) >= maxSectionWords/2 {
				flush()
				paraLine = 0
			}
			continue
		}
		if paraLine == 0 {
			paraLine = i + 1
		}
		para = append(para, strings.TrimSpace(l))
	}
	flush()
	var clean []Chunk
	for _, c := range out {
		if strings.TrimSpace(c.Text) != "" {
			clean = append(clean, c)
		}
	}
	return clean
}

// parseYAML indexa los textos de un YAML (planes de workstream) como un fragmento por clave de primer nivel.
func parseYAML(rel string, data []byte, kind string) []Chunk {
	var doc map[string]any
	if yaml.Unmarshal(data, &doc) != nil {
		return nil
	}
	title, _ := doc["title"].(string)
	id, _ := doc["id"].(string)
	var keys []string
	for k := range doc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []Chunk
	for _, k := range keys {
		var b strings.Builder
		collect(doc[k], &b)
		if strings.TrimSpace(b.String()) == "" {
			continue
		}
		out = append(out, chunk(Chunk{Kind: kind, Type: id, Title: strings.TrimSpace(id + " " + title + " — " + k), Text: b.String(), Path: rel}))
	}
	return out
}

func collect(v any, b *strings.Builder) {
	switch t := v.(type) {
	case string:
		b.WriteString(t + " ")
	case []any:
		for _, x := range t {
			collect(x, b)
		}
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			collect(t[k], b)
		}
	case nil:
	default:
		fmt.Fprintf(b, "%v ", t)
	}
}
