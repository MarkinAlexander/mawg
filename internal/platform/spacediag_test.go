package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// BigFilesTop - топ крупнейших файлов от limit: подсказка «что занимает
// /tmp» в ошибках установки ядер (жалоба 11.2026: exit status 2 без цифр)
func TestBigFilesTop(t *testing.T) {
	dir := t.TempDir()
	big := map[string]int64{"core.tar.gz": 30 << 20, "mihomo.gz": 12 << 20, "notes.txt": 2 << 20}
	for name, size := range big {
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(size); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	if err := os.WriteFile(filepath.Join(dir, "tiny.conf"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := BigFilesTop(dir, 1<<20, 2)
	if len(got) != 2 {
		t.Fatalf("топ-2: %v", got)
	}
	if !strings.Contains(got[0], "30 МБ core.tar.gz") || !strings.Contains(got[1], "12 МБ mihomo.gz") {
		t.Fatalf("порядок и размеры: %v", got)
	}
	// limit отсекает мелочь, каталоги не считаются
	if len(BigFilesTop(dir, 16<<20, 5)) != 1 {
		t.Fatalf("limit: %v", BigFilesTop(dir, 16<<20, 5))
	}
}

// SpaceDiag - строка диагностики осмысленна (или пуста там, где ничего
// выяснить нельзя), без паник на пустых каталогах
func TestSpaceDiag(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "core.tar.gz"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = SpaceDiag(dir)
	_ = SpaceDiag(filepath.Join(dir, "нет-такого"))
}
