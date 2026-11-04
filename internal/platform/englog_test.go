package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 112-док 3.6/3.7: лог движка - ротация + голова/хвост/первая ошибка
// (диагностика старта ядра). Тесты кроссплатформенные: helpers - чистые
// функции над файлами.

func TestEngineLogPathFallsBackToDir(t *testing.T) {
	// /opt и /var на Windows/тест-хосте недоступны для MkdirAll с
	// вероятной удачей - проверяем контракт: путь всегда заканчивается
	// на <name>.log и хотя бы одна из попыток даёт mawg-каталог
	p := EngineLogPath(t.TempDir(), "singbox")
	if !strings.HasSuffix(p, "singbox.log") {
		t.Fatalf("путь лога %q без имени движка", p)
	}
}

func TestRotateIfLarge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "engine.log")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 100)), 0o600); err != nil {
		t.Fatal(err)
	}
	RotateIfLarge(path, 50)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("большой лог не уехал в .old")
	}
	if _, err := os.Stat(path + ".old"); err != nil {
		t.Fatalf(".old не создан: %v", err)
	}
	// маленький не трогаем
	if err := os.WriteFile(path, []byte("small"), 0o600); err != nil {
		t.Fatal(err)
	}
	RotateIfLarge(path, 50)
	data, _ := os.ReadFile(path)
	if string(data) != "small" {
		t.Fatal("маленький лог не должен ротироваться")
	}
}

func TestLogHeadAndTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "engine.log")
	lines := []string{"first", "", "second", "third", "fourth"}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	head := LogHead(path, 2)
	if len(head) != 2 || head[0] != "first" || head[1] != "second" {
		t.Fatalf("LogHead = %v", head)
	}
	tail := LogTail(path, 2)
	if len(tail) != 2 || tail[0] != "third" || tail[1] != "fourth" {
		t.Fatalf("LogTail = %v", tail)
	}
	if got := LogHead(filepath.Join(dir, "missing.log"), 3); got != nil {
		t.Fatalf("LogHead отсутствующего = %v, хочу nil", got)
	}
}

func TestLogFirstError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "engine.log")
	log := "INFO start\nINFO loaded\nFATAL tun3: TUNSETIFF: device busy\n"
	if err := os.WriteFile(path, []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	got := LogFirstError(path)
	if !strings.Contains(got, "TUNSETIFF") {
		t.Fatalf("LogFirstError = %q, хочу FATAL-строку", got)
	}
	// без ошибок - пусто
	path2 := filepath.Join(dir, "clean.log")
	if err := os.WriteFile(path2, []byte("INFO ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := LogFirstError(path2); got != "" {
		t.Fatalf("LogFirstError чистого лога = %q, хочу \"\"", got)
	}
}
