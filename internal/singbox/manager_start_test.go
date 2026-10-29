//go:build linux

package singbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// startSharedIfManaged - инцидент Кинетика 2026-10-29: Apply при остановленном
// сервисе отказывал «не буду стартовать чужой сервис», и включение ядра из
// панели молча не работало. Слово владельца: если ядро ставили из mawg
// (маркер), сервис поднимаем сами; чужое - по-прежнему не трогаем.
func TestStartSharedIfManaged(t *testing.T) {
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "sing-box")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// фейковый init-скрипт: старт отмечает себя файлом
	started := filepath.Join(tmp, "started")
	script := filepath.Join(tmp, "S99sing-box")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch "+started+"\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	markerDir := filepath.Join(tmp, "marker")
	newMgr := func() *Manager {
		return &Manager{eng: &Engine{Bin: bin}, Mode: "shared",
			SharedInit: script, MarkerDir: markerDir, ClashPort: 22990}
	}

	// без маркера - прежний отказ, init-скрипт не зовётся
	if err := newMgr().startSharedIfManaged(200 * time.Millisecond); err == nil ||
		!strings.Contains(err.Error(), "не буду стартовать чужой сервис") {
		t.Fatalf("без маркера ждём прежний отказ: %v", err)
	}
	if _, err := os.Stat(started); !os.IsNotExist(err) {
		t.Fatalf("чужой сервис не должен стартовать: %v", err)
	}

	// маркер есть, бинаря нет - честный отказ (снести ядро и звать сервис
	// мы не умеем)
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(markerDir, LXMarkerName), []byte("version=1.14.4-lx.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newMgr()
	m.eng.Bin = filepath.Join(tmp, "missing")
	if err := m.startSharedIfManaged(200 * time.Millisecond); err == nil ||
		!strings.Contains(err.Error(), "не найден") {
		t.Fatalf("без бинаря ждём отказ: %v", err)
	}

	// маркер + бинарь на месте: скрипт start вызван; clash-API фейка не
	// поднимается, потому зов кончается таймаутом ожидания - это честно
	if err := newMgr().startSharedIfManaged(200 * time.Millisecond); err == nil ||
		!strings.Contains(err.Error(), "сервис не стартовал") {
		t.Fatalf("ждём таймаут ожидания clash-API: %v", err)
	}
	if _, err := os.Stat(started); err != nil {
		t.Fatalf("управляемый сервис должен был стартовать: %v", err)
	}
}
