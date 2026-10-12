package product

import (
	"bytes"
	"errors"
	"fmt"
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
//   - sin verificar firmas: log.showSignature apagado y los programas de firma
//     (gpg, ssh-keygen o los que declare la configuración) anulados;
//   - sin el candado opcional del índice (GIT_OPTIONAL_LOCKS=0).
//
// safe.directory se limita a la carpeta que la persona registró.

var transports = []string{"file", "git", "ssh", "http", "https", "ext"}

// noProgram es un programa que no se puede ejecutar: sustituye a los de firma.
const noProgram = "/dev/null"

// filterNameRe es lo que un nombre de filtro puede tener para anularlo con -c.
var filterNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// ErrUnsafeFilters indica filtros con nombres que no se pueden anular.
var ErrUnsafeFilters = errors.New("la configuración de git declara filtros con nombres inusuales; no se compara el árbol de trabajo para no ejecutarlos")

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
		// Ninguna lectura verifica firmas: gpg y ssh-keygen, o el programa que
		// declare la configuración, no corren aunque log.showSignature esté
		// encendido o un formato pida %G.
		"-c", "log.showSignature=false",
		"-c", "gpg.program=" + noProgram,
		"-c", "gpg.openpgp.program=" + noProgram,
		"-c", "gpg.x509.program=" + noProgram,
		"-c", "gpg.ssh.program=" + noProgram,
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
// Las variables GIT_* heredadas se descartan, salvo las que eligen de dónde
// sale la configuración: GIT_DIR o GIT_WORK_TREE (que git exporta en sus
// hooks y en los worktrees) harían leer otro repo, y GIT_CONFIG_PARAMETERS o
// GIT_CONFIG_COUNT meterían configuración.
func gitEnv() []string {
	env := make([]string, 0, len(os.Environ())+8)
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "GIT_") && !keepGitEnv[k] {
			continue
		}
		env = append(env, kv)
	}
	// Sin objetos de reemplazo ni injertos (.git/info/grafts): la historia es
	// la que guardan los commits.
	return append(env, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "GIT_NO_REPLACE_OBJECTS=1", "GIT_GRAFT_FILE=/dev/null",
		"GIT_NO_LAZY_FETCH=1", "GIT_PAGER=cat", "GIT_ASKPASS=/bin/false", "SSH_ASKPASS=/bin/false")
}

var keepGitEnv = map[string]bool{"GIT_CONFIG_NOSYSTEM": true, "GIT_CONFIG_GLOBAL": true, "GIT_CONFIG_SYSTEM": true}

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
			result = ErrUnsafeFilters
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

// GitRead prepara un comando de git de solo lectura sobre un repo ajeno (del
// producto o el hub): sin hooks, filtros, transportes ni candados opcionales.
func GitRead(dir string, args ...string) *exec.Cmd { return gitRead(dir, args...) }

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

// Git corre un comando de lectura de git con las mismas protecciones que el
// resto de la plomería: sin locks opcionales, sin fsmonitor, sin filtros ni
// diffs externos. Devuelve la salida estándar.
func Git(dir string, args ...string) (string, error) {
	return output(gitRead(dir, args...), args)
}

// GitStdin es Git para los comandos que leen de la entrada estándar (git
// cat-file --batch lee muchos objetos en un solo proceso).
func GitStdin(dir, stdin string, args ...string) (string, error) {
	cmd := gitRead(dir, args...)
	cmd.Stdin = strings.NewReader(stdin)
	return output(cmd, args)
}

// GitTree es Git para los comandos que comparan el árbol de trabajo (git blame
// sin revisión, git diff contra HEAD): corren los filtros clean del repo para
// normalizar lo que leen del disco. Si algún filtro tiene un nombre que no se
// puede anular, falla con ErrUnsafeFilters en vez de ejecutarlo.
func GitTree(dir string, args ...string) (string, error) {
	cmd, err := gitWorktree(dir, args...)
	if err != nil {
		return "", err
	}
	return output(cmd, args)
}

func output(cmd *exec.Cmd, args []string) (string, error) {
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %v %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
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

// OriginURL devuelve la URL del remoto origin de un repo, o "".
func OriginURL(dir string) string {
	out, err := gitRead(dir, "config", "--get", "remote.origin.url").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
