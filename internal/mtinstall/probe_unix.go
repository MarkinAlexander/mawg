//go:build unix

package mtinstall

import (
	"os/exec"
	"syscall"
)

// Ручной запуск демона - отдельной группой, гасится вся группа: сигнал
// только демону оставляет его ребёнка держать канал вывода, и Wait висит,
// пока тот не умрёт (Linux-тест на скрипте «sleep 30» стоял полные 30с).
func probeSetGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func probeSignalGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	return syscall.Kill(-cmd.Process.Pid, sig)
}
