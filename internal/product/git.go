package product

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// gitRead prepara un comando de solo lectura de git sobre un repo del
// producto. La configuración del repo no puede ejecutar programas (sin
// fsmonitor, hooks, diff externo ni textconv) y git no toma el candado del
// índice, así que leer un repo nunca lo modifica. safe.directory se limita a
// la carpeta que la persona registró en el producto.
func gitRead(dir string, args ...string) *exec.Cmd {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	base := []string{
		"-c", "core.fsmonitor=false",
		"-c", "core.hooksPath=/dev/null",
		"-c", "diff.external=",
		"-c", "safe.directory=" + abs,
		"-C", abs,
	}
	cmd := exec.Command("git", append(base, args...)...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "GIT_NO_REPLACE_OBJECTS=1", "GIT_PAGER=cat")
	return cmd
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
