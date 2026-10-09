//go:build unix

package platform

import (
	"os/exec"
	"testing"
	"time"
)

// пайплайн из двух sleep: без группового убийства выживает осиротевший
// участник и Output() ждёт его до конца - это и есть утёчка повисших
// процессов на роутере.
func TestRunBoundedKillsPipelineTree(t *testing.T) {
	start := time.Now()
	_, err := RunBoundedCombined(exec.Command("sh", "-c", "sleep 5 | sleep 5"), 300*time.Millisecond)
	if err == nil {
		t.Fatal("ожидалась ошибка таймаута")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("дерево пайплайна не убито по таймауту: %s", d)
	}
}
