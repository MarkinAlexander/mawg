package mtinstall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const rev23Pkg = "magitrickle_0.8.2-23_openwrt_aarch64_cortex-a53.ipk"

// fakeDaemon - фиктивный /proc с процессом magitrickled: set(true)
// ставит, set(false) убирает.
type fakeDaemon struct {
	t   *testing.T
	dir string
}

func newFakeDaemon(t *testing.T) *fakeDaemon {
	t.Helper()
	d := &fakeDaemon{t: t, dir: t.TempDir()}
	old := procDir
	procDir = d.dir
	t.Cleanup(func() { procDir = old })
	return d
}

func (d *fakeDaemon) set(present bool) {
	d.t.Helper()
	pid := filepath.Join(d.dir, "4242")
	if err := os.RemoveAll(pid); err != nil {
		d.t.Fatal(err)
	}
	if !present {
		return
	}
	if err := os.MkdirAll(pid, 0o755); err != nil {
		d.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pid, "comm"), []byte("magitrickled\n"), 0o644); err != nil {
		d.t.Fatal(err)
	}
}

// installOpts - установка rev23 на OpenWrt/opkg под уже стоящую версию
// (путь из жалобы 24.11: «up to date» -> force-reinstall).
func (d *fakeDaemon) installOpts(run func(string, ...string) ([]byte, error)) Options {
	return Options{
		Platform: "openwrt",
		Detect:   func() (string, string, error) { return "aarch64_cortex-a53", "opkg print-architecture", nil },
		Latest: func(context.Context) (*Release, error) {
			return &Release{TagName: "0.8.2-rev23", Assets: []Asset{
				{Name: rev23Pkg, URL: "http://x/ipk", Size: 9 << 20},
			}}, nil
		},
		Fetch:  fetchFake(300_000),
		Run:    run,
		TmpDir: d.t.TempDir(),
	}
}

func fakeProbe(t *testing.T, out string, alive bool) {
	t.Helper()
	orig := daemonProbe
	daemonProbe = func(string, time.Duration, string) (string, bool) { return out, alive }
	t.Cleanup(func() { daemonProbe = orig })
}

// enableChecks - послестартовая проверка включена (по умолчанию в тестах
// заглушена: нет /proc с живым magitrickled)
func enableChecks(t *testing.T) {
	t.Helper()
	oldAlive, oldDelay := procAlive, postStartDelay
	procAlive = func() bool { return true }
	postStartDelay = 0
	t.Cleanup(func() { procAlive, postStartDelay = oldAlive, oldDelay })
}

// живой вывод ВМ 0.62 (24.10.8, rev23, порт веб-интерфейса занят): демон
// умирает через 0.1с после старта, procd после 5 рестартов бросает
const liveCrashOut = "\x1b[90m12:17PM\x1b[0m \x1b[32mINF\x1b[0m \x1b[1mstarting MagiTrickle daemon\x1b[0m " +
	"version=\x1b[36m0.8.2-rev23-mawg-dev\x1b[0m\n" +
	"\x1b[90m12:17PM\x1b[0m \x1b[32mINF\x1b[0m \x1b[1mstarting service\x1b[0m\n" +
	"\x1b[90m12:17PM\x1b[0m \x1b[31mERR\x1b[0m \x1b[1mfailed to start application\x1b[0m " +
	"error=\x1b[33m\x1b[0m\"setup http fail: error while listening HTTP [::]:8090: " +
	"listen tcp 0.0.0.0:8090: bind: address already in use\"\n" +
	"\x1b[90m12:17PM\x1b[0m \x1b[32mINF\x1b[0m \x1b[1mshutting down service\x1b[0m\n"

// жалоба 24.11: служба отчиталась, демона нет - в ошибке должна быть
// причина из уст самого демона, а не отписка про iptables-save
func TestInstallDaemonCrashWords(t *testing.T) {
	enableChecks(t)
	fd := newFakeDaemon(t)
	run := &fakeRun{script: map[string]resp{
		"uci|-q get magitrickle.main.enabled": {out: "1\n"},
	}}
	fakeProbe(t, liveCrashOut, false)
	res, err := Install(context.Background(), fd.installOpts(run.run))
	if err == nil {
		t.Fatal("жду ошибку: демон службой не поднят")
	}
	for _, want := range []string{"сразу после старта", "setup http fail", "bind: address already in use"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("в ошибке нет %q: %v", want, err)
		}
	}
	for _, want := range []string{"- демон: ", "12:17PM ERR failed to start application", "запускаю бинарь вручную"} {
		if !strings.Contains(res.Log, want) {
			t.Errorf("в журнале установки нет %q:\n%s", want, res.Log)
		}
	}
	if strings.Contains(res.Log, "\x1b[") {
		t.Errorf("цветовые коды демона должны сниматься:\n%s", res.Log)
	}
	// служба восстановлена после ручного запуска: последний вызов - start
	if n := len(run.calls); n == 0 || run.calls[n-1] != "/etc/init.d/magitrickle start" {
		t.Errorf("после диагностики служба не запущена; хвост: %v", run.calls[max(0, n-3):])
	}
	if run.saw("uci set") {
		t.Errorf("включённая в uci служба не должна трогаться; вызовы: %v", run.calls)
	}
}

// openwrt-скрипт пакета при uci enabled != 1 молча не стартует ничего:
// установка включает службу сама (кнопка «обновить MT» = MT нужен живым)
func TestInstallUciDisabledEnablesService(t *testing.T) {
	enableChecks(t)
	fd := newFakeDaemon(t)
	run := &fakeRun{script: map[string]resp{
		"uci|-q get magitrickle.main.enabled": {out: "0\n"},
	}}
	real := run.run
	runFn := func(argv0 string, args ...string) ([]byte, error) {
		out, err := real(argv0, args...)
		if argv0 == "uci" && strings.Join(args, " ") == "commit magitrickle" {
			fd.set(true) // служба включена - демон поднялся
		}
		return out, err
	}
	fakeProbe(t, "", false)
	res, err := Install(context.Background(), fd.installOpts(runFn))
	if err != nil {
		t.Fatalf("жду успех после включения службы: %v\n%s", err, res.Log)
	}
	// секции может не быть вовсе (пустой conffile): без magitrickle.main=magitrickle
	// uci set отвечает «Invalid argument» (проверено на 0.62, 24.10.8)
	for _, want := range []string{"touch /etc/config/magitrickle", "uci set magitrickle.main=magitrickle", "uci set magitrickle.main.enabled=1", "uci commit magitrickle"} {
		if !run.saw(want) {
			t.Errorf("нет %q; вызовы: %v", want, run.calls)
		}
	}
	if !strings.Contains(res.Log, "выключена в uci") {
		t.Errorf("в журнале нет честной строки о включении службы:\n%s", res.Log)
	}
	if strings.Contains(res.Log, "вручную") {
		t.Errorf("поднявшийся демон не должен запускаться вручную:\n%s", res.Log)
	}
}

// бинарь вручную живёт, службой не поднимается - честная ошибка без
// выдуманной причины
func TestInstallProbeAliveServiceDead(t *testing.T) {
	enableChecks(t)
	fd := newFakeDaemon(t)
	run := &fakeRun{script: map[string]resp{
		"uci|-q get magitrickle.main.enabled": {out: "1\n"},
	}}
	fakeProbe(t, "12:17PM INF starting service\n", true)
	_, err := Install(context.Background(), fd.installOpts(run.run))
	if err == nil || !strings.Contains(err.Error(), "вручную бинарь работает") {
		t.Fatalf("жду честную ошибку про исправный бинарь: %v", err)
	}
}

// procd-стоп мгновенен: не вышедший по TERM демон добивается ДО установки
// (иначе postinst стартует службу поверх живого старичка)
func TestInstallWaitsOldDaemonDeath(t *testing.T) {
	old := stopWait
	stopWait = 60 * time.Millisecond
	t.Cleanup(func() { stopWait = old })

	fd := newFakeDaemon(t)
	fd.set(true)
	run := &fakeRun{}
	real := run.run
	runFn := func(argv0 string, args ...string) ([]byte, error) {
		if argv0 == "kill" {
			fd.set(false)
		}
		return real(argv0, args...)
	}
	res, err := Install(context.Background(), fd.installOpts(runFn))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Log, "не вышел по TERM") {
		t.Errorf("в журнале нет добивания старого демона:\n%s", res.Log)
	}
	killIdx, installIdx := -1, -1
	for i, c := range run.calls {
		if c == "kill -9 4242" && killIdx == -1 {
			killIdx = i
		}
		if strings.HasPrefix(c, "opkg install ") && installIdx == -1 {
			installIdx = i
		}
	}
	if killIdx == -1 || installIdx == -1 || killIdx > installIdx {
		t.Errorf("старый демон должен умереть до opkg install: kill=%d install=%d; вызовы: %v", killIdx, installIdx, run.calls)
	}
}

// здоровый путь: демон появился после start и пережил перепроверку
func TestInstallPostStartAlive(t *testing.T) {
	enableChecks(t)
	fd := newFakeDaemon(t)
	run := &fakeRun{}
	real := run.run
	runFn := func(argv0 string, args ...string) ([]byte, error) {
		out, err := real(argv0, args...)
		if argv0 == "/etc/init.d/magitrickle" && strings.Join(args, " ") == "start" {
			fd.set(true)
		}
		return out, err
	}
	fakeProbe(t, "", false)
	res, err := Install(context.Background(), fd.installOpts(runFn))
	if err != nil {
		t.Fatalf("жду успех: %v\n%s", err, res.Log)
	}
	if strings.Contains(res.Log, "вручную") || run.saw("kill -9") {
		t.Errorf("диагностика не должна срабатывать на живом демоне:\n%s\nвызовы: %v", res.Log, run.calls)
	}
}

func TestInstallReinstallFlag(t *testing.T) {
	fd := newFakeDaemon(t)
	opts := fd.installOpts(nil)
	upToDate := &fakeRun{script: map[string]resp{
		"opkg|install " + filepath.Join(opts.TmpDir, rev23Pkg): {
			out: "Package magitrickle (0.8.2-23) installed in root is up to date.",
		},
	}}
	opts.Run = upToDate.run
	res, err := Install(context.Background(), opts)
	if err != nil || !res.SameVersion {
		t.Fatalf("opkg «up to date» + force-reinstall: жду SameVersion: %+v %v", res, err)
	}
	fresh := &fakeRun{}
	opts.Run = fresh.run
	if res, err = Install(context.Background(), opts); err != nil || res.SameVersion {
		t.Errorf("обычная установка не должна помечать SameVersion: %+v %v", res, err)
	}
}

func TestLastWords(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want string
	}{
		{"err", liveCrashOut, "12:17PM ERR failed to start application error=\"setup http fail: " +
			"error while listening HTTP [::]:8090: listen tcp 0.0.0.0:8090: bind: address already in use\""},
		{"ftl", "12:17PM FTL process 3350 is already running\n", "12:17PM FTL process 3350 is already running"},
		{"panic", "panic: runtime error: index out of range\n\ngoroutine 1 [running]:\n", "panic: runtime error: index out of range"},
		{"exec", "fork/exec /usr/bin/magitrickled: exec format error", "fork/exec /usr/bin/magitrickled: exec format error"},
		{"none", "", ""},
	}
	for _, c := range cases {
		if got := lastWords(c.out); got != c.want {
			t.Errorf("%s: lastWords = %q, хочу %q", c.name, got, c.want)
		}
	}
	// без ошибочной строки - просто последняя; debug DNS-поток не мешает
	out := "12:17PM DBG request received\n12:17PM INF service stopped\n12:17PM DBG request received\n"
	if got := lastWords(out); got != "12:17PM INF service stopped" {
		t.Errorf("последняя содержательная строка: %q", got)
	}
	if lines := tailLines(out, 6); len(lines) != 1 {
		t.Errorf("DBG-строки должны отсеиваться: %v", lines)
	}
	if got := clip(strings.Repeat("я", wordsMax+5)); len([]rune(got)) != wordsMax+1 {
		t.Errorf("длинная строка обрезается по руне: %d рун", len([]rune(got)))
	}
}

func TestForegroundProbeExecFail(t *testing.T) {
	out, alive := foregroundProbe(filepath.Join(t.TempDir(), "нет-бинаря"), time.Second, "openwrt")
	if alive || !strings.Contains(out, "нет-бинаря") {
		t.Fatalf("отсутствующий бинарь: alive=%v out=%q", alive, out)
	}
}

// форграунд-проба: процесс, переживший окно, глушится и считается живым
func TestForegroundProbeSurvivor(t *testing.T) {
	if _, err := os.Stat("/bin/sleep"); err != nil {
		t.Skip("нет /bin/sleep: форграунд-проба проверяется на Linux")
	}
	out, alive := foregroundProbe("/bin/sleep", 300*time.Millisecond, "openwrt")
	if !alive {
		t.Fatalf("переживший окно процесс должен считаться живым: %q", out)
	}
}

// жалоба 24.11, загрузка файла через панель: opkg признаёт аргумент файлом
// пакета только по расширению - mawg-core-upload.bin уходил как «имя
// пакета» («Unknown package '/tmp/…/mawg-core-upload.bin'»)
func TestInstallLocalPackageOpkgAlias(t *testing.T) {
	newFakeDaemon(t)
	staged := filepath.Join(t.TempDir(), "mawg-core-upload.bin")
	if err := os.WriteFile(staged, make([]byte, 300_000), 0o644); err != nil {
		t.Fatal(err)
	}
	link := staged + ".ipk"
	if _, err := opkgAlias(staged); err != nil {
		t.Skipf("хост не умеет ни символические, ни жёсткие ссылки: %v", err)
	}
	_ = os.Remove(link)
	run := &fakeRun{}
	res, err := Install(context.Background(), Options{
		Platform:         "openwrt",
		LocalPackage:     staged,
		Detect:           func() (string, string, error) { return "", "", errors.New("opkg недоступен") },
		PackageManagerFn: func() (string, string) { return "opkg", ".ipk" },
		Run:              run.run,
	})
	if err != nil {
		t.Fatalf("%v\n%s", err, res.Log)
	}
	if !run.saw("opkg install "+link) || run.has("opkg install "+staged) {
		t.Errorf("opkg должен получить файл с расширением .ipk; вызовы: %v", run.calls)
	}
	if !strings.Contains(res.Log, "пакет из загруженного файла: mawg-core-upload.bin") {
		t.Errorf("в журнале должно остаться имя загруженного файла:\n%s", res.Log)
	}
	for _, gone := range []string{link, staged} {
		if _, serr := os.Stat(gone); !os.IsNotExist(serr) {
			t.Errorf("%s остался после установки", gone)
		}
	}
}

// жалоба 24.11 после перезагрузки: база opkg пуста, списков нет - opkg
// отбрасывает пакет со строкой «cannot find dependency», а следующая его
// строка («incompatible with the architectures configured») выглядела как
// проблема архитектуры; настоящий затык - упавший opkg update
func TestInstallOpkgErrorsReachLog(t *testing.T) {
	newFakeDaemon(t)
	opts := newFakeDaemon(t).installOpts(nil)
	dest := filepath.Join(opts.TmpDir, rev23Pkg)
	run := &fakeRun{script: map[string]resp{
		"opkg|install " + dest: {
			out: "Unknown package 'magitrickle'.\nCollected errors:\n" +
				" * pkg_hash_check_unresolved: cannot find dependency iptables-nft for magitrickle\n" +
				" * pkg_hash_fetch_best_installation_candidate: Packages for magitrickle found, but incompatible with the architectures configured\n" +
				" * opkg_install_cmd: Cannot install package magitrickle.\n",
			err: errors.New("exit status 255"),
		},
		"opkg|update": {
			out: "Updated list of available packages in /var/opkg-lists/openwrt_core\n" +
				"*** Failed to download the package list from https://x/base/Packages.gz\n\n" +
				" * opkg_download: Failed to download https://x/base/Packages.sig, wget returned 4.\n" +
				" * opkg_download: Check your network settings and connectivity.\n",
			err: errors.New("exit status 1"),
		},
	}}
	opts.Run = run.run
	res, err := Install(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "install (повтор)") ||
		!strings.Contains(err.Error(), "pkg_hash_check_unresolved: cannot find dependency iptables-nft for magitrickle") {
		t.Fatalf("жду ошибку с настоящей строкой opkg: %v", err)
	}
	for _, want := range []string{
		"opkg: pkg_hash_check_unresolved: cannot find dependency iptables-nft for magitrickle",
		"opkg update не удался: exit status 1",
		"opkg: opkg_download: Failed to download https://x/base/Packages.sig, wget returned 4.",
	} {
		if !strings.Contains(res.Log, want) {
			t.Errorf("в журнале нет %q:\n%s", want, res.Log)
		}
	}
}

func TestBlame(t *testing.T) {
	out := "Installing mawgtest (1) to root...\nCollected errors:\n" +
		" * pkg_hash_check_unresolved: cannot find dependency bmon for mawgtest\n\n" +
		"Signature check failed.\n" +
		"*** Failed to download the package list from https://x/Packages.gz\n" +
		"ERROR: unable to select packages:\n"
	want := []string{
		"pkg_hash_check_unresolved: cannot find dependency bmon for mawgtest",
		"Signature check failed.",
		"Failed to download the package list from https://x/Packages.gz",
		"ERROR: unable to select packages:",
	}
	if got := blame(out); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("blame = %q, хочу %q", got, want)
	}
	if got := blame("ok\n\n"); len(got) != 0 {
		t.Errorf("без ошибок список должен быть пуст: %q", got)
	}
}
