package singbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// автозагрузка Keenetic/Entware: S-префикс имени; enable/disable -
// переименование S99sing-box <-> disabled.S99sing-box
func TestKeeneticAutostartRename(t *testing.T) {
	dir := t.TempDir()
	initScript := filepath.Join(dir, "S99sing-box")
	if err := os.WriteFile(initScript, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !AutostartEnabled(initScript, "") {
		t.Fatal("S-префикс = автозагрузка включена")
	}
	if err := SetKeeneticAutostart(initScript, false); err != nil {
		t.Fatal(err)
	}
	disabled := KeeneticDisabledPath(initScript)
	if _, err := os.Stat(disabled); err != nil {
		t.Fatal("выключенный вариант не появился")
	}
	if _, err := os.Stat(initScript); !os.IsNotExist(err) {
		t.Fatal("исходный скрипт должен был уйти")
	}
	if AutostartEnabled(disabled, "") {
		t.Fatal("disabled.-имя не должно считаться автозагрузкой")
	}
	if err := SetKeeneticAutostart(initScript, false); err != nil {
		t.Fatal("повторное выключение должно быть идемпотентным")
	}
	if err := SetKeeneticAutostart(initScript, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(initScript); err != nil {
		t.Fatal("скрипт не вернулся на место")
	}
}

// автозагрузка OpenWrt: симлинк /etc/rc.d/S99sing-box от procd enable
func TestOpenwrtAutostartRcDir(t *testing.T) {
	rc := t.TempDir()
	initScript := "/etc/init.d/sing-box"
	if AutostartEnabled(initScript, rc) {
		t.Fatal("симлинка нет - автозагрузки нет")
	}
	link := filepath.Join(rc, "S99sing-box")
	if err := os.Symlink(initScript, link); err != nil {
		t.Fatal(err)
	}
	if !AutostartEnabled(initScript, rc) {
		t.Fatal("симлинк S*<имя> = автозагрузка включена")
	}
	// procd enable/disable дёргается как команда init-скрипта
	var got []string
	run := func(ctx context.Context, name string, args ...string) error {
		got = append([]string{name}, args...)
		return nil
	}
	if err := SetOpenwrtAutostart(context.Background(), initScript, false, run); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != initScript || got[1] != "disable" {
		t.Fatalf("procd вызов: %v", got)
	}
}

// uninstall: без маркера ничего не делает, с маркером возвращает чужой
// бинарь из .pre-lx или удаляет наш, маркер снимается
func TestUninstallLX(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "sing-box")
	tgt := LXTarget{Bin: bin, MarkerDir: filepath.Join(dir, "marker")}

	restored, err := UninstallLX(tgt)
	if err != nil || restored {
		t.Fatal("без маркера uninstall ничего не должен делать")
	}

	if err := os.WriteFile(bin, []byte("ours"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(tgt.MarkerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tgt.MarkerDir, LXMarkerName), []byte("tag=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	restored, err = UninstallLX(tgt)
	if err != nil || restored {
		t.Fatalf("без бэкапа бинарь удаляется: restored=%v err=%v", restored, err)
	}
	if _, err := os.Stat(bin); !os.IsNotExist(err) {
		t.Fatal("бинарь не удалён")
	}
	if _, err := os.Stat(tgt.MarkerDir); !os.IsNotExist(err) {
		t.Fatal("маркер не снят")
	}

	// вариант с чужим бинарём: восстановление из .pre-lx
	if err := os.WriteFile(bin, []byte("ours2"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin+".pre-lx", []byte("foreign"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(tgt.MarkerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tgt.MarkerDir, LXMarkerName), []byte("tag=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	restored, err = UninstallLX(tgt)
	if err != nil || !restored {
		t.Fatalf("чужой бинарь должен вернуться: restored=%v err=%v", restored, err)
	}
	data, _ := os.ReadFile(bin)
	if string(data) != "foreign" {
		t.Fatal("на месте бинаря не прежний владелец")
	}
}
