//go:build linux

package singbox

// KillProcs - гасит процессы (SIGTERM -> SIGKILL), возвращает число
// погашенных. Для конвейера замены бинаря: процессы с заменяемого
// бинаря оставались бы сиротами с удалённым образом.

import (
	"syscall"
	"time"
)

func KillProcs(ps []ProcInfo) int {
	if len(ps) == 0 {
		return 0
	}
	for _, p := range ps {
		syscall.Kill(p.PID, syscall.SIGTERM)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		alive := 0
		for _, p := range ps {
			if syscall.Kill(p.PID, 0) == nil {
				alive++
			}
		}
		if alive == 0 {
			return len(ps)
		}
		time.Sleep(200 * time.Millisecond)
	}
	for _, p := range ps {
		syscall.Kill(p.PID, syscall.SIGKILL)
	}
	return len(ps)
}
