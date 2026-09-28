package product

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Leer un repo del producto nunca lo modifica ni ejecuta programas que el repo
// configure. gitRead corre git con:
//   - sin hooks ni fsmonitor, sin diff externo, textconv ni submódulos;
//   - sin ningún transporte: un clon parcial no puede traer objetos (un fetch
//     escribe en .git y corre uploadpack o ssh);
//   - los filtros clean, smudge y process que declare la configuración
//     anulados por nombre: git status o diff-index los correrían para
//     comparar el árbol de trabajo;
//   - sin el candado opcional del índice (GIT_OPTIONAL_LOCKS=0).
//
// safe.directory se limita a la carpeta que la persona registró.

var transports = []string{"file", "git", "ssh", "http", "https", "ext"}

// filterNameRe es lo que un nombre de filtro puede tener para anularlo con -c.
var filterNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// errUnsafeFilters indica filtros con nombres que no se pueden anular.
var errUnsafeFilters = errors.New("la configuración de git declara filtros con nombres inusuales; no se compara el árbol de trabajo para no ejecutarlos")

var filterCache sync.Map // carpeta → []string (argumentos -c) o error

// baseArgs son las opciones que anulan lo que ejecuta programas o escribe.
func baseArgs(abs string) []string {
	args := []string{
		"-c", "core.fsmonitor=false",
		"-c", "core.hooksPath=/dev/null",
		"-c", "diff.external=",
		"-c", "submodule.recurse=false",
		"-c", "protocol.allow=never",
		"-c", "safe.directory=" + abs,
	}
	for _, t := range transports {
		args = append(args, "-c", "protocol."+t+".allow=never")
	}
	return args
}

func absDir(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	return abs
}

// gitEnv es el entorno de git: sin candados opcionales, sin preguntas, sin
// traer objetos que falten y sin paginador.
func gitEnv() []string {
	return append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "GIT_NO_REPLACE_OBJECTS=1",
		"GIT_NO_LAZY_FETCH=1", "GIT_PAGER=cat", "GIT_ASKPASS=/bin/false", "SSH_ASKPASS=/bin/false")
}

// filterOverrides anula los filtros que declara la configuración (del repo y
// de la persona) para que comparar el árbol de trabajo no los ejecute.
func filterOverrides(abs string) ([]string, error) {
	if v, ok := filterCache.Load(abs); ok {
		if err, isErr := v.(error); isErr {
			return nil, err
		}
		return v.([]string), nil
	}
	args := append(baseArgs(abs), "-C", abs, "config", "-z", "--get-regexp", `^filter\..+\.(clean|smudge|process|required)$`)
	cmd := exec.Command("git", args...)
	cmd.Env = gitEnv()
	out, _ := cmd.Output() // sin filtros, git config sale con 1: no es un error
	seen := map[string]bool{}
	var over []string
	var result any
	for _, rec := range strings.Split(string(out), "\x00") {
		key, _, _ := strings.Cut(rec, "\n")
		if !strings.HasPrefix(key, "filter.") {
			continue
		}
		name := key[len("filter."):strings.LastIndex(key, ".")]
		if seen[name] {
			continue
		}
		seen[name] = true
		if !filterNameRe.MatchString(name) {
			result = errUnsafeFilters
			break
		}
		for _, attr := range []string{"clean", "smudge", "process"} {
			over = append(over, "-c", "filter."+name+"."+attr+"=")
		}
		over = append(over, "-c", "filter."+name+".required=false")
	}
	if result == nil {
		result = over
	}
	filterCache.Store(abs, result)
	if err, isErr := result.(error); isErr {
		return nil, err
	}
	return over, nil
}

// gitRead prepara un comando de solo lectura de git sobre un repo del producto.
func gitRead(dir string, args ...string) *exec.Cmd {
	abs := absDir(dir)
	base := baseArgs(abs)
	if over, err := filterOverrides(abs); err == nil {
		base = append(base, over...)
	}
	cmd := exec.Command("git", append(append(base, "-C", abs), args...)...)
	cmd.Env = gitEnv()
	return cmd
}

// gitWorktree es gitRead para los comandos que comparan el árbol de trabajo
// (status, diff-index): falla si hay filtros que no se pueden anular.
func gitWorktree(dir string, args ...string) (*exec.Cmd, error) {
	if _, err := filterOverrides(absDir(dir)); err != nil {
		return nil, err
	}
	return gitRead(dir, args...), nil
}

// HeadSHA devuelve el commit actual (abreviado) de un repo, o "" si no es git.
func HeadSHA(dir string) string {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return ""
	}
	out, err := gitRead(dir, "rev-parse", "--short=7", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
