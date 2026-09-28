package product

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Roles de una entrada del mapa.
const (
	Exposes   = "expone"  // un servicio atiende la ruta
	Calls     = "llama"   // un cliente llama la ruta
	Publishes = "publica" // un módulo publica en el tópico
	Listens   = "escucha" // un módulo consume el tópico
)

// Entry es una interfaz encontrada en el código: una ruta HTTP que se expone o
// se llama, o un tópico que se publica o se escucha, con su ubicación.
type Entry struct {
	Repo   string
	Module string
	Role   string
	Method string // GET, POST… o "" si no se sabe (HTTP); "" en eventos
	Path   string // ruta normalizada o nombre del tópico
	Raw    string // texto original, para revisar
	File   string // relativo al repo, con barras
	Line   int
	// Unresolved indica que la ruta o el tópico no se pudo leer (una variable,
	// una propiedad): se reporta en lugar de callarse.
	Unresolved bool
}

// Kind devuelve http o event.
func (e Entry) Kind() string {
	if e.Role == Publishes || e.Role == Listens {
		return "event"
	}
	return "http"
}

// Ref devuelve repo@sha:ruta#Llínea.
func (e Entry) Ref(sha string) string {
	r := e.Repo
	if sha != "" {
		r += "@" + sha
	}
	return fmt.Sprintf("%s:%s#L%d", r, e.File, e.Line)
}

// Source es un repo del producto que se lee sin modificarlo.
type Source struct {
	Name string
	Dir  string
}

// Scan es lo que se sacó de un repo.
type Scan struct {
	Repo     string
	SHA      string
	Identity Identity
	Entries  []Entry
	Files    int // archivos revisados
}

// maxFile acota lo que se lee de cada archivo.
const maxFile = 2 << 20

// codeExt son las extensiones que revisan los extractores.
var codeExt = map[string]bool{".java": true, ".kt": true, ".dart": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".mjs": true}

// skipDirs no se recorren cuando el repo no es git (en git se usan los archivos versionados).
var skipDirs = map[string]bool{".git": true, "node_modules": true, "build": true, "dist": true, "target": true,
	".dart_tool": true, "vendor": true, ".gradle": true, ".idea": true, "coverage": true, ".next": true, "out": true}

// files enumera los archivos del repo: los versionados si es git, o un
// recorrido que salta carpetas de dependencias y compilación.
func files(dir string) ([]string, string, error) {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		cmd := gitRead(dir, "ls-files", "-z")
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err == nil {
			var list []string
			for _, f := range strings.Split(out.String(), "\x00") {
				if f != "" {
					list = append(list, f)
				}
			}
			return list, HeadSHA(dir), nil
		}
	}
	var list []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] && p != dir {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			rel, _ := filepath.Rel(dir, p)
			list = append(list, filepath.ToSlash(rel))
		}
		return nil
	})
	return list, "", err
}

// readRegular lee un archivo del repo solo si es regular y acotado: un
// symlink o un archivo especial no se sigue.
func readRegular(dir, rel string) ([]byte, bool) {
	p := filepath.Join(dir, filepath.FromSlash(rel))
	info, err := os.Lstat(p)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxFile {
		return nil, false
	}
	data, err := os.ReadFile(p)
	if err != nil || bytes.IndexByte(data, 0) >= 0 {
		return nil, false
	}
	return data, true
}

// Extract recorre un repo sin modificarlo y devuelve su identidad y sus interfaces.
func Extract(src Source) (*Scan, error) {
	info, err := os.Stat(src.Dir)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("%s: no encuentro la carpeta %s", src.Name, src.Dir)
	}
	list, sha, err := files(src.Dir)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, errors.New(src.Name + ": no hay archivos que revisar")
	}
	sc := &Scan{Repo: src.Name, SHA: sha}
	sc.Identity = identify(src.Dir, list)
	mods := newModules(list)
	consts := map[string]constTable{} // módulo → constantes
	repoConsts := constTable{}
	var javaFiles []string
	for _, f := range list {
		ext := strings.ToLower(path.Ext(f))
		if !codeExt[ext] || isTestPath(f) {
			continue
		}
		data, ok := readRegular(src.Dir, f)
		if !ok {
			continue
		}
		sc.Files++
		mod := mods.of(f)
		text := string(data)
		switch ext {
		case ".java", ".kt":
			javaFiles = append(javaFiles, f)
			if consts[mod] == nil {
				consts[mod] = constTable{}
			}
			consts[mod].add(f, text)
			repoConsts.add(f, text)
		case ".dart":
			sc.Entries = append(sc.Entries, dartCalls(text, src.Name, mod, f)...)
		default:
			sc.Entries = append(sc.Entries, tsCalls(text, src.Name, mod, f)...)
		}
	}
	// Spring va después: los tópicos por constante se resuelven con todas las
	// constantes del módulo.
	for _, f := range javaFiles {
		data, _ := readRegular(src.Dir, f)
		mod := mods.of(f)
		text := string(data)
		sc.Entries = append(sc.Entries, springEntries(text, src.Name, mod, f, resolver(f, text, consts[mod], repoConsts))...)
	}
	sort.SliceStable(sc.Entries, func(i, j int) bool {
		a, b := sc.Entries[i], sc.Entries[j]
		if a.Module != b.Module {
			return a.Module < b.Module
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})
	return sc, nil
}

// extractText saca las interfaces de un solo archivo: sirve para comparar dos
// versiones de un archivo en un diff. Las constantes se resuelven solo con
// las del mismo archivo.
func extractText(repo, mod, file, text string) []Entry {
	if text == "" {
		return nil
	}
	switch strings.ToLower(path.Ext(file)) {
	case ".java", ".kt":
		consts := constTable{}
		consts.add(file, text)
		return springEntries(text, repo, mod, file, resolver(file, text, consts))
	case ".dart":
		return dartCalls(text, repo, mod, file)
	case ".ts", ".tsx", ".js", ".jsx", ".mjs":
		return tsCalls(text, repo, mod, file)
	}
	return nil
}

// isTestPath deja fuera pruebas, mocks y ejemplos: no son interfaces reales.
func isTestPath(f string) bool {
	l := strings.ToLower(f)
	for _, part := range []string{"/test/", "/tests/", "/integration_test/", "/__tests__/", "/e2e/", "/mock/", "/mocks/", "/fixtures/", "/testdata/", "/examples/"} {
		if strings.Contains("/"+l, part) {
			return true
		}
	}
	for _, suf := range []string{"_test.dart", ".spec.ts", ".test.ts", ".spec.tsx", ".test.tsx", ".spec.js", ".test.js", ".stories.tsx", ".mock.ts"} {
		if strings.HasSuffix(l, suf) {
			return true
		}
	}
	base := path.Base(f)
	for _, suf := range []string{"Test.java", "Tests.java", "IT.java", "Test.kt", "Tests.kt"} {
		if strings.HasSuffix(base, suf) {
			return true
		}
	}
	return false
}

// lineOf devuelve la línea (desde 1) del índice i en text.
func lineOf(text string, i int) int {
	return strings.Count(text[:i], "\n") + 1
}

// modules asigna cada archivo a su módulo: la carpeta más profunda que tiene
// un archivo de build (build.gradle, pom.xml, pubspec.yaml, package.json,
// project.json, go.mod), o la raíz.
type modules struct{ roots []string }

var buildFiles = map[string]bool{"build.gradle": true, "build.gradle.kts": true, "pom.xml": true, "pubspec.yaml": true,
	"package.json": true, "project.json": true, "go.mod": true}

func newModules(list []string) modules {
	var m modules
	for _, f := range list {
		if buildFiles[path.Base(f)] {
			if d := path.Dir(f); d != "." {
				m.roots = append(m.roots, d)
			}
		}
	}
	sort.Slice(m.roots, func(i, j int) bool { return len(m.roots[i]) > len(m.roots[j]) })
	return m
}

// of devuelve el módulo de un archivo; "." si no pertenece a ninguno.
func (m modules) of(f string) string {
	for _, r := range m.roots {
		if strings.HasPrefix(f, r+"/") {
			return r
		}
	}
	return "."
}

// ModuleName abrevia la ruta de un módulo a su último segmento.
func ModuleName(mod string) string {
	if mod == "." || mod == "" {
		return "raíz"
	}
	return path.Base(mod)
}
