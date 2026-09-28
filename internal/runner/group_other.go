//go:build !unix

package runner

import "os/exec"

func setGroup(cmd *exec.Cmd) {}
