//go:build unix

package runner

import (
	"os/exec"
	"syscall"
)

// setGroup corre Claude Code en su propio grupo de procesos: al vencer el
// tiempo se termina junto con lo que haya lanzado.
func setGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
}
