package platform

import (
	"fmt"
	"os/exec"
	"time"
)

// RunBoundedOutput / RunBoundedCombined - запуск внешней команды с потолком
// по времени. На роутере с битым интерфейсом или флешкой ip/wg/wc могут
// зависнуть навсегда: без потолка каждый опрос панели и ротатора плодит
// новый повисший процесс, вместе они съедают CPU. При таймауте убивается
// вся группа процессов: у sh -c 'a | b' иначе выживают осиротевшие
// участники пайплайна (opkg|grep, df|tail|awk).

// RunBoundedOutput - stdout-вывод команды (cmd.Output()).
func RunBoundedOutput(cmd *exec.Cmd, timeout time.Duration) (string, error) {
	return bounded(cmd, timeout, cmd.Output)
}

// RunBoundedCombined - stdout+stderr (cmd.CombinedOutput()).
func RunBoundedCombined(cmd *exec.Cmd, timeout time.Duration) (string, error) {
	return bounded(cmd, timeout, cmd.CombinedOutput)
}

func bounded(cmd *exec.Cmd, timeout time.Duration, collect func() ([]byte, error)) (string, error) {
	setupTreeKill(cmd)
	done := make(chan struct{})
	var out []byte
	var err error
	go func() {
		out, err = collect()
		close(done)
	}()
	select {
	case <-done:
		return string(out), err
	case <-time.After(timeout):
		killTree(cmd)
		<-done
		return string(out), fmt.Errorf("таймаут %s: %s убит(ы)", timeout, cmd.Path)
	}
}
