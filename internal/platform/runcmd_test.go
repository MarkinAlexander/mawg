package platform

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestRunBoundedKillsOnTimeout(t *testing.T) {
	start := time.Now()
	_, err := RunBoundedCombined(exec.Command("sleep", "5"), 300*time.Millisecond)
	if err == nil {
		t.Fatal("ожидалась ошибка таймаута")
	}
	if !strings.Contains(err.Error(), "таймаут") {
		t.Fatalf("ошибка не про таймаут: %v", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("процесс не убит по таймауту: %s", d)
	}
}

func TestRunBoundedPassesOutput(t *testing.T) {
	out, err := RunBoundedOutput(exec.Command("sh", "-c", "echo -n hi"), 5*time.Second)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if out != "hi" {
		t.Fatalf("вывод: %q", out)
	}
}
