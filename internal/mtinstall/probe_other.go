//go:build !unix

package mtinstall

import (
	"os/exec"
	"syscall"
)

// Windows (хост сборки и тестов): групп процессов нет - сигнал самому демону.
func probeSetGroup(*exec.Cmd) {}

func probeSignalGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	return cmd.Process.Signal(sig)
}
