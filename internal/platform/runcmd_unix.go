//go:build unix

package platform

import (
	"os/exec"
	"syscall"
)

func setupTreeKill(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	// вся группа (пайплайны shell-скриптов) и сам процесс
	syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	syscall.Kill(cmd.Process.Pid, syscall.SIGKILL)
}
