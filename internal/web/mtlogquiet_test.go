package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// хвост флеш-лога переезжает в RAM, флеш-путь становится symlink
func TestMTLogMigrate(t *testing.T) {
	dir := t.TempDir()
	flash := filepath.Join(dir, "flash.log")
	ram := filepath.Join(dir, "ram.log")
	writeFile(t, flash, strings.Repeat("x", 300_000)+"TAIL-MARKER")

	if err := mtLogMigrate(flash, ram, 128<<10); err != nil {
		t.Fatal(err)
	}
	tail, err := os.ReadFile(ram)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(tail), "TAIL-MARKER") {
		t.Fatal("хвост не сохранён")
	}
	if len(tail) > 128<<10 {
		t.Fatalf("хвост больше keep: %d", len(tail))
	}
	link, err := os.Readlink(flash)
	if err != nil {
		t.Fatal("флеш-путь не стал symlink:", err)
	}
	if link != ram {
		t.Fatalf("symlink смотрит на %s", link)
	}
	// идемпотентность уровня Server: повторная миграция по symlink не должна трогать файлы
	if fi, err := os.Lstat(flash); err == nil && fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("ожидался symlink")
	}
}

// copytruncate: сверх cap хвост уходит в .old, файл усекается на месте
func TestMTLogRotate(t *testing.T) {
	dir := t.TempDir()
	ram := filepath.Join(dir, "ram.log")
	writeFile(t, ram, strings.Repeat("y", 300_000)+"END-MARKER")

	rotated, err := mtLogRotate(ram, 256<<10, 128<<10)
	if err != nil || !rotated {
		t.Fatalf("ротация не прошла: %v %v", rotated, err)
	}
	fi, err := os.Stat(ram)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() != 0 {
		t.Fatalf("активный файл не усечён: %d", fi.Size())
	}
	old, err := os.ReadFile(ram + ".old")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(old), "END-MARKER") {
		t.Fatal(".old не содержит хвост")
	}
	if len(old) > 128<<10 {
		t.Fatalf(".old больше keep: %d", len(old))
	}

	// ниже cap - трогать не надо
	writeFile(t, ram, "короткий")
	rotated, err = mtLogRotate(ram, 256<<10, 128<<10)
	if err != nil || rotated {
		t.Fatalf("маленький файл не должен ротироваться: %v %v", rotated, err)
	}
	data, _ := os.ReadFile(ram)
	if string(data) != "короткий" {
		t.Fatal("маленький файл изменён")
	}
}
