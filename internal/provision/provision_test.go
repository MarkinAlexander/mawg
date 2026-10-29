package provision

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

const versionOut = `
          release: 5.01.C.3.0-1
            title: 5.1.3
             arch: mips
           components: acl,base,wireguard,
                       wireguard-server
      description: Keenetic Giga (KN-1011)
            model: Giga (KN-1011)
`

func TestParseKeeneticVersion(t *testing.T) {
	info := parseKeeneticVersion(versionOut)
	if info.title != "5.1.3" {
		t.Fatalf("title = %q", info.title)
	}
	if info.description != "Keenetic Giga (KN-1011)" {
		t.Fatalf("description = %q", info.description)
	}
	if info.major != 5 || info.minor != 1 {
		t.Fatalf("gate version = %d.%d", info.major, info.minor)
	}
	if !info.components["wireguard"] {
		t.Fatal("wireguard component not parsed")
	}
}

// fakeRunner - подмена shell для lxCoreItem: знает `version` бинаря.
type fakeRunner map[string]string

func (f fakeRunner) run(script string, _ time.Duration) (string, error) {
	for pattern, out := range f {
		if strings.Contains(script, pattern) {
			return out, nil
		}
	}
	return "", fmt.Errorf("команда не поддержана: %s", script)
}

// withFS - подмена stat-проверок lxCoreItem (размер бинаря, маркер) и
// свободного места; реальных файлов на машине теста нет.
func withFS(t *testing.T, size, free int64, marker bool) {
	t.Helper()
	oldStat, oldFree := lxFileStat, lxDiskFree
	lxFileStat = func(p string) (int64, time.Time, bool) {
		if strings.HasSuffix(p, ".installed-by-mawg") {
			return 0, time.Time{}, marker
		}
		if size > 0 {
			return size, time.Unix(12345, 0), true
		}
		return 0, time.Time{}, false
	}
	lxDiskFree = func(string) int64 { return free }
	lxVerMu.Lock()
	lxVerCache = map[string]lxVerEntry{}
	lxVerMu.Unlock()
	t.Cleanup(func() {
		lxFileStat, lxDiskFree = oldStat, oldFree
		lxVerMu.Lock()
		lxVerCache = map[string]lxVerEntry{}
		lxVerMu.Unlock()
	})
}

func TestLxCoreItemStates(t *testing.T) {
	bin := "/opt/bin/sing-box"

	// чужое upstream-ядро: статус «чужое» (короткий - чип в таблице не рвётся), замена одной кнопкой
	withFS(t, 63607828, 26214400*1024, false)
	r := fakeRunner{bin + " version": "sing-box version 1.13.3\n"}
	item := lxCoreItem(r.run, "keenetic")
	if item.Installed || item.Action != "singbox-lx-replace" || item.StatusText != "чужое" {
		t.Fatalf("чужое ядро: %+v", item)
	}
	if item.FreeBytes != 26214400*1024 || item.BackupBytes != 63607828 {
		t.Fatalf("место/размер: %+v", item)
	}
	if !strings.Contains(item.Confirm, "1.13.3") || !strings.Contains(item.Note, "не трогает") {
		t.Fatalf("confirm/note: %q %q", item.Confirm, item.Note)
	}
	if strings.Contains(item.Confirm, ".pre-lx") {
		t.Fatalf("подтверждение не должно обещать бэкап безусловно: %q", item.Confirm)
	}

	// своё lx-ядро с маркером: обновление
	withFS(t, 63607828, 26214400*1024, true)
	r = fakeRunner{bin + " version": "sing-box version 1.14.2-lx.7\n"}
	item = lxCoreItem(r.run, "keenetic")
	if !item.Installed || item.Action != "singbox-lx" || item.Note != "" || item.StatusText != "" {
		t.Fatalf("lx с маркером: %+v", item)
	}

	// lx без маркера - не наша сборка: кнопка заменяет бинарь своей сборкой
	withFS(t, 63607828, 26214400*1024, false)
	r = fakeRunner{bin + " version": "sing-box version 1.14.2-lx.6\n"}
	item = lxCoreItem(r.run, "keenetic")
	if !item.Installed || item.Note == "" || item.ActionLabel != "заменить своей сборкой" {
		t.Fatalf("lx без маркера: %+v", item)
	}

	// ядра нет: установка с нуля, статус «не установлен»
	withFS(t, 0, 0, false)
	item = lxCoreItem(fakeRunner{}.run, "keenetic")
	if item.Installed || item.Action != "singbox-lx" || item.Version != "" || item.StatusText != "не установлен" {
		t.Fatalf("нет ядра: %+v", item)
	}
	if !strings.Contains(item.Confirm, "не установлено") {
		t.Fatalf("confirm: %q", item.Confirm)
	}
	if item.ActionLabel != "установить ядро" {
		t.Fatalf("метка установки должна быть нейтральной к источнику: %+v", item)
	}

	// бинарь есть, version-запуск не удался, но маркер знает версию:
	// честная версия нашей установки вместо «не читается» (инцидент
	// Кинетика 29.10 - version-запуск попал на занятое окно)
	withFS(t, 63607828, 26214400*1024, true)
	oldVer, oldSrc := markerVersionOf, markerSourceOf
	markerVersionOf = func(string) string { return "1.14.4-lx.25" }
	markerSourceOf = func(string) string { return "Leadaxe/sing-box-lx" }
	t.Cleanup(func() { markerVersionOf, markerSourceOf = oldVer, oldSrc })
	item = lxCoreItem(fakeRunner{}.run, "keenetic")
	if !item.Installed || item.Version != "1.14.4-lx.25" || item.StatusText != "из маркера" {
		t.Fatalf("фолбэк версии из маркера: %+v", item)
	}
	if item.Source != "lx" || item.ActionLabel != "переустановить ядро" {
		t.Fatalf("источник/метка из маркера: %+v", item)
	}
	if !strings.Contains(item.Confirm, "1.14.4-lx.25") {
		t.Fatalf("confirm должен называть версию из маркера: %q", item.Confirm)
	}

	// маркера нет - прежний честный статус, метка нейтральная
	markerVersionOf = func(string) string { return "" }
	item = lxCoreItem(fakeRunner{}.run, "keenetic")
	if item.Installed || item.StatusText != "файл есть, версия не читается (роутер занят?)" || item.ActionLabel != "переустановить ядро" {
		t.Fatalf("версия не читается без маркера: %+v", item)
	}

	// openwrt-пути
	withFS(t, 63607828, 26214400*1024, true)
	r = fakeRunner{"/usr/bin/sing-box version": "sing-box version 1.14.2-lx.7\n"}
	item = lxCoreItem(r.run, "openwrt")
	if !item.Installed {
		t.Fatalf("openwrt lx: %+v", item)
	}
}

// сбой/таймаут запуска (upx-старт дольше потолка) не закрепляет пустую
// версию в кэше - после успешного запуска версия появляется
func TestCachedBinVersionRetriesAfterFailure(t *testing.T) {
	oldStat := lxFileStat
	mtime := time.Unix(777, 0)
	lxFileStat = func(p string) (int64, time.Time, bool) { return 17612508, mtime, true }
	t.Cleanup(func() { lxFileStat = oldStat })
	lxVerMu.Lock()
	lxVerCache = map[string]lxVerEntry{}
	lxVerMu.Unlock()

	calls, fail := 0, true
	run := func(script string, _ time.Duration) (string, error) {
		calls++
		if fail {
			return "", fmt.Errorf("таймаут: процесс убит")
		}
		return "sing-box version 1.14.2-lx.12\n", nil
	}
	if v := cachedBinVersion("/opt/bin/sing-box", run); v != "" {
		t.Fatalf("при сбое версия должна быть пустой: %q", v)
	}
	fail = false
	if v := cachedBinVersion("/opt/bin/sing-box", run); v != "1.14.2-lx.12" {
		t.Fatalf("после успеха: %q", v)
	}
	if v := cachedBinVersion("/opt/bin/sing-box", run); v != "1.14.2-lx.12" || calls != 2 {
		t.Fatalf("кэш после успеха: %q, вызовов %d", v, calls)
	}
}

// версия бинаря кэшируется по stat-ключу: `sing-box version` на медленной
// флешке дорог (вычитывает весь бинарь), опрос панели не должен его плодить
func TestCachedBinVersionReusesStatKey(t *testing.T) {
	oldStat := lxFileStat
	mtime := time.Unix(999, 0)
	lxFileStat = func(p string) (int64, time.Time, bool) { return 40576104, mtime, true }
	t.Cleanup(func() { lxFileStat = oldStat })

	calls := 0
	run := func(script string, _ time.Duration) (string, error) {
		calls++
		return "sing-box version 1.14.2-lx.12\n", nil
	}
	lxVerMu.Lock()
	lxVerCache = map[string]lxVerEntry{}
	lxVerMu.Unlock()
	for i := 0; i < 3; i++ {
		if v := cachedBinVersion("/usr/bin/sing-box", run); v != "1.14.2-lx.12" {
			t.Fatalf("версия: %q", v)
		}
	}
	if calls != 1 {
		t.Fatalf("запусков version: %d, ожидался 1", calls)
	}
	// файл заменили - версия проверяется заново
	lxFileStat = func(p string) (int64, time.Time, bool) { return 41000000, mtime, true }
	if v := cachedBinVersion("/usr/bin/sing-box", run); v != "1.14.2-lx.12" {
		t.Fatalf("версия после замены: %q", v)
	}
	if calls != 2 {
		t.Fatalf("запусков после замены: %d, ожидалось 2", calls)
	}
}

// пункт mihomo в таблице зависимостей (29.10): версия через -v, кнопка
// установки/обновления, действие mihomo идёт POST /system/install
func TestMihomoCoreItemStates(t *testing.T) {
	bin := "/opt/bin/mihomo"

	// не установлен: кнопка «установить»
	withFS(t, 0, 0, false)
	item := mihomoCoreItem(fakeRunner{}.run, "keenetic")
	if item.Installed || item.Action != "mihomo" || item.ActionLabel != "установить" || item.Version != "" {
		t.Fatalf("нет mihomo: %+v", item)
	}
	if !strings.Contains(item.Confirm, "MetaCubeX") {
		t.Fatalf("confirm: %q", item.Confirm)
	}

	// установлен нашей панелью: «обновить ядро» без примечаний
	withFS(t, 63000000, 26214400*1024, true)
	r := fakeRunner{bin + " -v": "Mihomo Meta v1.19.33 linux-mipsle-softfloat with go1.24\r\n"}
	item = mihomoCoreItem(r.run, "keenetic")
	if !item.Installed || item.Version != "v1.19.33" || item.ActionLabel != "обновить ядро" || item.Note != "" {
		t.Fatalf("установлен с маркером: %+v", item)
	}

	// установлен вручную: примечание про маркер
	withFS(t, 63000000, 26214400*1024, false)
	item = mihomoCoreItem(r.run, "keenetic")
	if !item.Installed || item.Note == "" {
		t.Fatalf("установлен без маркера: %+v", item)
	}

	// openwrt-путь
	withFS(t, 63000000, 26214400*1024, true)
	r = fakeRunner{"/usr/bin/mihomo -v": "Mihomo Meta v1.19.4 darwin-arm64\n"}
	item = mihomoCoreItem(r.run, "openwrt")
	if !item.Installed || item.Version != "v1.19.4" {
		t.Fatalf("openwrt: %+v", item)
	}
}
