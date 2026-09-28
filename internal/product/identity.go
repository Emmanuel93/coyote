package product

import (
	"encoding/json"
	"path"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Identity es lo que un repo dice de sí mismo: stack, cómo se corre, sus
// módulos y sus documentos.
type Identity struct {
	Stacks    []string
	Type      string // backend, mobile, web u other (tipos de README.coyote.md)
	Purpose   string
	Run       string
	Test      string
	Build     string
	Modules   []Module
	Docs      []Doc
	Contracts []string
	Owners    []string // equipos o personas de CODEOWNERS (solo @nombres)
}

// Module es una unidad con su propio archivo de build.
type Module struct {
	Path        string
	Name        string
	Description string
}

// Doc es un documento del repo.
type Doc struct {
	Path  string
	Title string
}

// identify lee los archivos de build, el README y docs/ del repo.
func identify(dir string, list []string) Identity {
	var id Identity
	has := map[string]bool{}
	for _, f := range list {
		has[f] = true
	}
	read := func(rel string) string {
		data, ok := readRegular(dir, rel)
		if !ok {
			return ""
		}
		return string(data)
	}
	gradle := has["build.gradle.kts"] || has["build.gradle"] || has["settings.gradle.kts"] || has["settings.gradle"]
	switch {
	case gradle || has["pom.xml"]:
		stack := "gradle"
		if has["pom.xml"] {
			stack = "maven"
		}
		build := read("build.gradle.kts") + read("build.gradle") + read("pom.xml")
		for _, f := range list {
			if strings.HasSuffix(f, "build.gradle.kts") || strings.HasSuffix(f, "build.gradle") {
				if strings.Contains(read(f), "springframework") {
					build += " springframework"
					break
				}
			}
		}
		if strings.Contains(build, "springframework") {
			id.Stacks = append(id.Stacks, "spring")
		}
		id.Stacks = append(id.Stacks, stack)
		id.Type = "backend"
		if has["gradlew"] {
			id.Test, id.Build = "./gradlew test", "./gradlew build"
		} else if stack == "maven" {
			id.Test, id.Build = "mvn test", "mvn package"
		}
	}
	if has["pubspec.yaml"] || has["melos.yaml"] {
		id.Stacks = append(id.Stacks, "flutter")
		id.Type = "mobile"
		if has["melos.yaml"] {
			id.Stacks = append(id.Stacks, "melos")
			scripts := melosScripts(read("melos.yaml"))
			id.Test = pickScript(scripts, "melos run ", "test:all", "test", "tests")
			id.Build = pickScript(scripts, "melos run ", "build", "build:all")
			if id.Test == "" {
				id.Test = "melos exec -- flutter test"
			}
		} else {
			id.Test = "flutter test"
		}
	}
	if has["package.json"] {
		scripts := npmScripts(read("package.json"))
		if has["nx.json"] {
			id.Stacks = append(id.Stacks, "nx")
		}
		id.Stacks = append(id.Stacks, "node")
		if id.Type == "" {
			id.Type = "web"
		}
		if id.Test == "" {
			id.Test = pickScript(scripts, "npm run ", "test", "test:all", "test:unit")
		}
		if id.Build == "" {
			id.Build = pickScript(scripts, "npm run ", "build", "build:all")
		}
		id.Run = pickScript(scripts, "npm run ", "dev", "start", "serve")
	}
	if has["go.mod"] {
		id.Stacks = append(id.Stacks, "go")
		if id.Type == "" {
			id.Type = "backend"
		}
		id.Test, id.Build = "go test ./...", "go build ./..."
	}
	if id.Type == "" {
		id.Type = "other"
	}
	for _, s := range []string{"run_local.sh", "run-dev.sh", "run.sh", "scripts/run-local.sh"} {
		if has[s] && id.Run == "" {
			id.Run = "./" + s
		}
	}
	if id.Run == "" && (has["docker-compose.yml"] || has["compose.yaml"]) {
		id.Run = "docker compose up"
	}
	id.Purpose = purpose(read("README.md"))
	for _, f := range []string{".github/CODEOWNERS", "CODEOWNERS", "docs/CODEOWNERS", ".gitlab/CODEOWNERS"} {
		if has[f] {
			id.Owners = codeOwners(read(f))
			break
		}
	}
	for _, f := range list {
		base := path.Base(f)
		switch {
		case buildFiles[base] && path.Dir(f) != ".":
			m := Module{Path: path.Dir(f), Name: path.Base(path.Dir(f))}
			switch base {
			case "pubspec.yaml":
				m.Description = yamlField(read(f), "description")
			case "package.json", "project.json":
				m.Description = jsonField(read(f), "description")
			}
			if boilerplate(m.Description) {
				m.Description = ""
			}
			if m.Description == "" {
				m.Description = purpose(read(path.Join(m.Path, "README.md")))
			}
			m.Description = sentence(m.Description, 20)
			id.Modules = appendModule(id.Modules, m)
		case strings.HasPrefix(f, "docs/") && strings.HasSuffix(f, ".md"):
			id.Docs = append(id.Docs, Doc{Path: f, Title: title(read(f), base)})
		}
		if contractRe.MatchString(strings.ToLower(base)) {
			id.Contracts = append(id.Contracts, f)
		}
	}
	sort.Slice(id.Modules, func(i, j int) bool { return id.Modules[i].Path < id.Modules[j].Path })
	return id
}

var ownerRe = regexp.MustCompile(`^@[A-Za-z0-9][A-Za-z0-9._/-]*$`)

// codeOwners toma los dueños de la regla general (*) de CODEOWNERS; solo
// @usuario o @org/equipo, nunca correos.
func codeOwners(doc string) []string {
	var out []string
	for _, l := range strings.Split(doc, "\n") {
		f := strings.Fields(l)
		if len(f) < 2 || f[0] != "*" {
			continue
		}
		out = nil // la última regla general es la que vale
		for _, o := range f[1:] {
			if strings.HasPrefix(o, "#") {
				break
			}
			if ownerRe.MatchString(o) {
				out = append(out, o)
			}
		}
	}
	return out
}

var contractRe = regexp.MustCompile(`^(openapi|swagger|asyncapi)[^/]*\.(ya?ml|json)$`)

func appendModule(ms []Module, m Module) []Module {
	for _, x := range ms {
		if x.Path == m.Path {
			return ms
		}
	}
	return append(ms, m)
}

// purpose toma el primer párrafo en prosa: ni título, insignia, tabla,
// código, lista ni índice.
func purpose(readme string) string {
	for _, para := range strings.Split(strings.ReplaceAll(readme, "\r\n", "\n"), "\n\n") {
		p := strings.TrimSpace(para)
		if p == "" || strings.HasPrefix(p, "#") || strings.HasPrefix(p, "!") || strings.HasPrefix(p, "[!") ||
			strings.HasPrefix(p, "|") || strings.HasPrefix(p, "<") || strings.HasPrefix(p, "```") || strings.HasPrefix(p, "---") ||
			strings.HasPrefix(p, ">") || isList(p) {
			continue
		}
		p = strings.Join(strings.Fields(mdInline.ReplaceAllString(p, "$1")), " ")
		if boilerplate(p) {
			continue
		}
		return sentence(p, 30)
	}
	return ""
}

var listLine = regexp.MustCompile(`^\s*(?:[-*+]\s|\d+[.)]\s)`)

// isList informa si un párrafo es una lista o un índice.
func isList(p string) bool {
	lines := strings.Split(p, "\n")
	n := 0
	for _, l := range lines {
		if listLine.MatchString(l) {
			n++
		}
	}
	return n > 0 && n*2 >= len(lines) || regexp.MustCompile(`^\d+\.\s+\S+.*\d+\.\s`).MatchString(p)
}

// boilerplate reconoce las descripciones que dejan los generadores.
func boilerplate(s string) bool {
	l := strings.ToLower(s)
	for _, b := range []string{"this library was generated with nx", "this application was generated", "a new flutter project",
		"a new flutter package", "generated with angular cli", "this project was bootstrapped with", "todo"} {
		if strings.HasPrefix(l, b) {
			return true
		}
	}
	return false
}

// sentence acorta un texto a su primera oración o, si es más larga, a n
// palabras con puntos suspensivos.
func sentence(s string, n int) string {
	if i := strings.Index(s, ". "); i > 0 && len(strings.Fields(s[:i])) <= n {
		s = s[:i+1]
	}
	w := strings.Fields(s)
	if len(w) > n {
		return firstWords(strings.Join(w[:n-1], " "), n) + " …"
	}
	return firstWords(s, n)
}

var mdInline = regexp.MustCompile("\\[([^\\]]*)\\]\\([^)]*\\)|[*_`]")

func firstWords(s string, n int) string {
	w := strings.Fields(s)
	if len(w) > n {
		w = w[:n]
	}
	out := strings.Join(w, " ")
	return strings.NewReplacer("|", "/").Replace(out)
}

func title(md, fallback string) string {
	for _, l := range strings.Split(md, "\n") {
		if t := strings.TrimSpace(strings.TrimLeft(l, "#")); strings.HasPrefix(strings.TrimSpace(l), "#") && t != "" {
			return firstWords(t, 12)
		}
	}
	return strings.TrimSuffix(fallback, ".md")
}

func yamlField(doc, key string) string {
	var m map[string]any
	if yaml.Unmarshal([]byte(doc), &m) != nil {
		return ""
	}
	s, _ := m[key].(string)
	return firstWords(s, 20)
}

func jsonField(doc, key string) string {
	var m map[string]any
	if json.Unmarshal([]byte(doc), &m) != nil {
		return ""
	}
	s, _ := m[key].(string)
	return firstWords(s, 20)
}

func npmScripts(pkg string) map[string]string {
	var m struct {
		Scripts map[string]string `json:"scripts"`
	}
	_ = json.Unmarshal([]byte(pkg), &m)
	return m.Scripts
}

func melosScripts(doc string) map[string]string {
	var m struct {
		Scripts map[string]any `yaml:"scripts"`
	}
	_ = yaml.Unmarshal([]byte(doc), &m)
	out := map[string]string{}
	for k := range m.Scripts {
		out[k] = k
	}
	return out
}

// pickScript devuelve el primer script que exista, con su prefijo.
func pickScript(scripts map[string]string, prefix string, names ...string) string {
	for _, n := range names {
		if _, ok := scripts[n]; ok {
			return prefix + n
		}
	}
	return ""
}
