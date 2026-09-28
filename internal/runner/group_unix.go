//go:build unix

package runner

import (
	"os/exec"
	"syscall"
	"time"
)

// setGroup corre Claude Code en su propio grupo de procesos: al vencer el
// tiempo se termina junto con lo que haya lanzado.
func setGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		pgid := cmd.Process.Pid
		// Si algo del grupo no termina con SIGTERM, a los pocos segundos se mata.
		time.AfterFunc(3*time.Second, func() { _ = syscall.Kill(-pgid, syscall.SIGKILL) })
		return syscall.Kill(-pgid, syscall.SIGTERM)
	}
}
