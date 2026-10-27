package singbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Сервисное управление ядром для «Система → Ядра VPN»: автозагрузка и
// uninstall. Пути приходят явными аргументами (LXTargetForPlatform +
// sharedPaths web-слоя) - юнит-тесты гоняют на временных каталогах,
// не трогая систему.

// KeeneticDisabledPrefix - Entware-скрипты не умеют enable/disable:
// автозагрузку даёт только имя с S-префиксом. Выключение - переименование
// в disabled.<имя> в том же каталоге (паттерн как у mtinstall).
const KeeneticDisabledPrefix = "disabled."

// KeeneticDisabledPath - путь «выключенного» варианта init-скрипта.
func KeeneticDisabledPath(initScript string) string {
	return filepath.Join(filepath.Dir(initScript), KeeneticDisabledPrefix+filepath.Base(initScript))
}

// AutostartEnabled - стоит ли сервис в автозагрузку. rcDir непуст для
// OpenWrt (симлинк /etc/rc.d/S*<имя> от procd enable); пуст для Keenetic
// (детект по S-префиксу имени самого скрипта).
func AutostartEnabled(initScript, rcDir string) bool {
	if initScript == "" {
		return false
	}
	base := filepath.Base(initScript)
	if rcDir == "" {
		return strings.HasPrefix(base, "S")
	}
	entries, err := os.ReadDir(rcDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		n := e.Name()
		if n == base || strings.HasPrefix(n, "S") && strings.HasSuffix(n, base) {
			return true
		}
	}
	return false
}

// SetKeeneticAutostart - включить/выключить автозагрузку переименованием
// S99sing-box <-> disabled.S99sing-box. Идемпотентно.
func SetKeeneticAutostart(initScript string, enable bool) error {
	disabled := KeeneticDisabledPath(initScript)
	if enable {
		if _, err := os.Stat(initScript); err == nil {
			return nil // уже включён
		}
		return os.Rename(disabled, initScript)
	}
	if _, err := os.Stat(disabled); err == nil {
		return nil // уже выключен
	}
	return os.Rename(initScript, disabled)
}

// CmdRunner - запуск команды (инъекция для тестов OpenWrt-ветки).
type CmdRunner func(ctx context.Context, name string, args ...string) error

// SetOpenwrtAutostart - procd enable/disable у init-скрипта.
func SetOpenwrtAutostart(ctx context.Context, initScript string, enable bool, run CmdRunner) error {
	if run == nil {
		run = func(ctx context.Context, name string, args ...string) error {
			c := exec.CommandContext(ctx, name, args...)
			c.Stdout = nil
			c.Stderr = nil
			return c.Run()
		}
	}
	verb := "disable"
	if enable {
		verb = "enable"
	}
	return run(ctx, initScript, verb)
}

// UninstallLX - удаляет ядро, установленное mawg (маркер обязан быть):
// останавливать процесс/сервис - забота вызывающего. Если при установке
// был сохранён чужой бинарь (<bin>.pre-lx), он возвращается на место;
// иначе бинарь удаляется. Маркер снимается. Восстановлен ли чужой -
// в restored.
func UninstallLX(target LXTarget) (restored bool, err error) {
	if !markerExists(target.MarkerDir) {
		return false, nil
	}
	backup := target.Bin + ".pre-lx"
	if _, statErr := os.Stat(backup); statErr == nil {
		if err = os.Rename(backup, target.Bin); err != nil {
			return false, err
		}
		restored = true
	} else if rmErr := os.Remove(target.Bin); rmErr != nil && !os.IsNotExist(rmErr) {
		return false, rmErr
	}
	if err = os.RemoveAll(target.MarkerDir); err != nil {
		return restored, err
	}
	return restored, nil
}

// ManagedByMawg - ядро установлено mawg (маркер на месте).
func ManagedByMawg(target LXTarget) bool {
	return markerExists(target.MarkerDir)
}

// ServiceCmd - выполнить действие init-скрипта (start/stop/restart).
func ServiceCmd(ctx context.Context, initScript, action string, run CmdRunner) error {
	if run == nil {
		run = func(ctx context.Context, name string, args ...string) error {
			return exec.CommandContext(ctx, name, args...).Run()
		}
	}
	c, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return run(c, initScript, action)
}
