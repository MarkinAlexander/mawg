package singbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// жалоба 24.11: на роутере без памяти version-зонд убивает OOM - бинар на
// диске есть, а Detect молчал «не найден», и карточка ядер звала ставить
// ядро поверх имеющегося
func TestDetectBinPresentButUnresponsive(t *testing.T) {
	old := binCandidates
	t.Cleanup(func() { binCandidates = old })
	dir := t.TempDir()
	broken := filepath.Join(dir, "sing-box")
	if err := os.WriteFile(broken, []byte("это не исполняемый файл"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "нет-бинаря")

	binCandidates = []string{missing, broken}
	if bin, ok := BinPresent(); !ok || bin != broken {
		t.Fatalf("BinPresent = %q %v, жду %q", bin, ok, broken)
	}
	if _, err := Detect(); err == nil || !strings.Contains(err.Error(), broken+" на месте") {
		t.Errorf("жду честную ошибку про бинарь на месте: %v", err)
	}

	binCandidates = []string{missing}
	if _, ok := BinPresent(); ok {
		t.Error("BinPresent должен быть пуст без бинаря")
	}
	if _, err := Detect(); err == nil || !strings.Contains(err.Error(), "не найден") || strings.Contains(err.Error(), "на месте") {
		t.Errorf("без бинаря жду «не найден»: %v", err)
	}
}
