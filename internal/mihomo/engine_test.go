package mihomo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// как singbox.TestDetectBinPresentButUnresponsive: бинар на месте, но
// version-зонд не прошёл (роутер без памяти) - не «не найден»
func TestDetectBinPresentButUnresponsive(t *testing.T) {
	old := binCandidates
	t.Cleanup(func() { binCandidates = old })
	dir := t.TempDir()
	broken := filepath.Join(dir, "mihomo")
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
	if _, err := Detect(); err == nil || !strings.Contains(err.Error(), "не найден") || strings.Contains(err.Error(), "на месте") {
		t.Errorf("без бинаря жду «не найден»: %v", err)
	}
}
