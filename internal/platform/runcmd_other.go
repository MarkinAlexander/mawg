//go:build !unix

package platform

import (
	"os/exec"
)

// Windows-сборка для локальной отладки: групп процессов нет, убиваем только
// сам процесс.

func setupTreeKill(cmd *exec.Cmd) {}

func killTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	cmd.Process.Kill()
}
