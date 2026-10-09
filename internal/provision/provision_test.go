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

	// чужое upstream-ядро: статус «чужое upstream», замена одной кнопкой
	withFS(t, 63607828, 26214400*1024, false)
	r := fakeRunner{bin + " version": "sing-box version 1.13.3\n"}
	item := lxCoreItem(r.run, "keenetic")
	if item.Installed || item.Action != "singbox-lx-replace" || item.StatusText != "чужое upstream" {
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

	// lx без маркера - миграция: считается своим, после кнопки появится маркер
	withFS(t, 63607828, 26214400*1024, false)
	r = fakeRunner{bin + " version": "sing-box version 1.14.2-lx.6\n"}
	item = lxCoreItem(r.run, "keenetic")
	if !item.Installed || item.Note == "" || item.ActionLabel != "пометить своим и обновить" {
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

	// openwrt-пути
	withFS(t, 63607828, 26214400*1024, true)
	r = fakeRunner{"/usr/bin/sing-box version": "sing-box version 1.14.2-lx.7\n"}
	item = lxCoreItem(r.run, "openwrt")
	if !item.Installed {
		t.Fatalf("openwrt lx: %+v", item)
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
