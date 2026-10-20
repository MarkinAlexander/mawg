package mtinstall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	// послестартовая перепроверка живости в тестах глушится (нет /proc
	// с живым magitrickled); тесты самого лечения включают её явно
	procAlive = func() bool { return false }
	os.Exit(m.Run())
}

func TestFirstOpkgArch(t *testing.T) {
	cases := []struct {
		out  string
		want string
	}{
		{"arch mipsel-3.4 16\n", "mipsel-3.4"},
		{"arch x86_64 50\narch noarch 1\narch all 1\n", "x86_64"},
		{"arch noarch 1\narch all 1\n", ""},
		{"", ""},
		{"мусор без полей\n", ""},
		// живой вывод пользователя (Keenetic aarch64, Entware + keendev):
		// all пропускается, берётся первая неслужебная архитектура
		{"arch all 100\narch aarch64-3.10 150\narch aarch64-3.10_kn 200\n", "aarch64-3.10"},
	}
	for _, c := range cases {
		if got := FirstOpkgArch(c.out); got != c.want {
			t.Errorf("FirstOpkgArch(%q) = %q, хочу %q", c.out, got, c.want)
		}
	}
}

func TestPickAsset(t *testing.T) {
	rel := &Release{TagName: "0.8.2-rev12", Assets: []Asset{
		{Name: "magitrickle_0.8.2-12_openwrt_x86_64.ipk", URL: "u1", Size: 1},
		{Name: "magitrickle_0.8.2-r12_openwrt_x86_64.apk", URL: "u2", Size: 1},
		{Name: "magitrickle_0.8.2-12_entware_mipsel-3.4_kn.ipk", URL: "u3", Size: 1},
		{Name: "magitrickle_0.8.2-12_entware_armv7-3.2.ipk", URL: "u4", Size: 1},
		{Name: "magitrickle_0.8.2-12_openwrt_aarch64_generic.ipk", URL: "u5", Size: 1},
		{Name: "magitrickle_0.8.2-12_openwrt_aarch64_cortex-a53.ipk", URL: "u6", Size: 1},
		{Name: "SHA256SUMS"},
	}}
	if a, err := PickAsset(rel, "openwrt", "x86_64", ".ipk"); err != nil || a.Name != "magitrickle_0.8.2-12_openwrt_x86_64.ipk" {
		t.Fatalf("ipk: %v %+v", err, a)
	}
	if a, err := PickAsset(rel, "openwrt", "x86_64", ".apk"); err != nil || a.Name != "magitrickle_0.8.2-r12_openwrt_x86_64.apk" {
		t.Fatalf("apk: %v %+v", err, a)
	}
	// точный _kn-таргет матчится как есть
	if a, err := PickAsset(rel, "entware", "mipsel-3.4_kn", ".ipk"); err != nil || a.Name != "magitrickle_0.8.2-12_entware_mipsel-3.4_kn.ipk" {
		t.Fatalf("kn: %v %+v", err, a)
	}
	// голая arch на Entware: _kn-сборка предпочтительна (живой кейс: opkg
	// Entware печатает aarch64-3.10, а mipsel-3.4 - прежний путь)
	rel.Assets = append(rel.Assets,
		Asset{Name: "magitrickle_0.8.2-12_entware_aarch64-3.10.ipk", URL: "u7", Size: 1},
		Asset{Name: "magitrickle_0.8.2-12_entware_aarch64-3.10_kn.ipk", URL: "u8", Size: 1},
	)
	// порядок ассетов в релизе не должен решать: kn первый даже когда plain раньше
	if a, err := PickAsset(rel, "entware", "aarch64-3.10", ".ipk"); err != nil || a.Name != "magitrickle_0.8.2-12_entware_aarch64-3.10_kn.ipk" {
		t.Fatalf("entware aarch64-3.10: %v %+v", err, a)
	}
	// _kn нет - фолбэк на plain (armv7-3.2 без kn-сборки)
	if a, err := PickAsset(rel, "entware", "armv7-3.2", ".ipk"); err != nil || a.Name != "magitrickle_0.8.2-12_entware_armv7-3.2.ipk" {
		t.Fatalf("armv7-3.2 plain: %v %+v", err, a)
	}
	// голый aarch64 на OpenWrt: фолбэк на _generic (живой кейс товарища)
	if a, err := PickAsset(rel, "openwrt", "aarch64", ".ipk"); err != nil || a.Name != "magitrickle_0.8.2-12_openwrt_aarch64_generic.ipk" {
		t.Fatalf("aarch64 generic: %v %+v", err, a)
	}
	// сабтаргет матчится точно, generic-фолбэк не перебивает
	if a, err := PickAsset(rel, "openwrt", "aarch64_cortex-a53", ".ipk"); err != nil || a.Name != "magitrickle_0.8.2-12_openwrt_aarch64_cortex-a53.ipk" {
		t.Fatalf("aarch64_cortex-a53: %v %+v", err, a)
	}
	_, err := PickAsset(rel, "openwrt", "riscv64_generic", ".ipk")
	if err == nil || !strings.Contains(err.Error(), "есть magitrickle_") {
		t.Fatalf("отсутствующий таргет: жду список имеющихся, получил %v", err)
	}
}

type resp struct {
	out string
	err error
}

// fakeRun пишет историю вызовов и отвечает по скрипту.
type fakeRun struct {
	calls    []string
	script   map[string]resp // точный ответ на команду "argv0|args"
	failOnce map[string]resp // один раз, затем дефолт "ok"
}

func (f *fakeRun) run(argv0 string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, argv0+" "+strings.Join(args, " "))
	key := argv0 + "|" + strings.Join(args, " ")
	if r, ok := f.failOnce[key]; ok {
		delete(f.failOnce, key)
		return []byte(r.out), r.err
	}
	if r, ok := f.script[key]; ok {
		return []byte(r.out), r.err
	}
	return []byte("ok"), nil
}

func (f *fakeRun) saw(prefix string) bool {
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func (f *fakeRun) has(s string) bool {
	for _, c := range f.calls {
		if c == s {
			return true
		}
	}
	return false
}

func testRelease() *Release {
	return &Release{TagName: "0.8.2-rev9", Assets: []Asset{
		{Name: "magitrickle_0.8.2-9_openwrt_x86_64.ipk", URL: "http://x/ipk", Size: 9 << 20},
		{Name: "magitrickle_0.8.2-r9_openwrt_x86_64.apk", URL: "http://x/apk", Size: 9 << 20},
	}}
}

func fetchFake(size int64) func(context.Context, string, string) (int64, error) {
	return func(_ context.Context, _, dest string) (int64, error) {
		if err := os.WriteFile(dest, make([]byte, size), 0o644); err != nil {
			return 0, err
		}
		return size, nil
	}
}

func TestInstallOpenwrtOpkg(t *testing.T) {
	dir := t.TempDir()
	run := &fakeRun{}
	res, err := Install(context.Background(), Options{
		Platform: "openwrt",
		Detect:   func() (string, string, error) { return "x86_64", "opkg", nil },
		Latest:   func(context.Context) (*Release, error) { return testRelease(), nil },
		Fetch:    fetchFake(300_000),
		Run:      run.run,
		TmpDir:   dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Tag != "0.8.2-rev9" || res.Asset != "magitrickle_0.8.2-9_openwrt_x86_64.ipk" {
		t.Fatalf("res: %+v", res)
	}
	if res.Bin != "/usr/bin/magitrickled" {
		t.Fatalf("bin: %s", res.Bin)
	}
	for _, want := range []string{
		"/etc/init.d/magitrickle stop",
		"/etc/init.d/magitrickle enable",
		"/etc/init.d/magitrickle start",
	} {
		if !run.saw(want) {
			t.Errorf("не вызвано %q; вызовы: %v", want, run.calls)
		}
	}
	if !run.has("opkg install "+filepath.Join(dir, "magitrickle_0.8.2-9_openwrt_x86_64.ipk")) {
		t.Errorf("нет opkg install пакета; вызовы: %v", run.calls)
	}
	if _, err := os.Stat(filepath.Join(dir, "magitrickle_0.8.2-9_openwrt_x86_64.ipk")); !os.IsNotExist(err) {
		t.Error("пакет не удалён из tmp")
	}
	if strings.Contains(res.Log, "пропущен") {
		t.Errorf("стоп живой службы не должен пропускаться: %s", res.Log)
	}
}

func TestInstallKeeneticKnSuffix(t *testing.T) {
	dir := t.TempDir()
	run := &fakeRun{}
	rel := &Release{TagName: "0.8.2-rev9", Assets: []Asset{
		{Name: "magitrickle_0.8.2-9_entware_mipsel-3.4.ipk", URL: "http://x/1"},
		{Name: "magitrickle_0.8.2-9_entware_mipsel-3.4_kn.ipk", URL: "http://x/2", Size: 8 << 20},
	}}
	res, err := Install(context.Background(), Options{
		Platform: "keenetic",
		Detect:   func() (string, string, error) { return "mipsel-3.4", "opkg print-architecture (Entware)", nil },
		Latest:   func(context.Context) (*Release, error) { return rel, nil },
		Fetch:    fetchFake(300_000),
		Run:      run.run,
		TmpDir:   dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Asset != "magitrickle_0.8.2-9_entware_mipsel-3.4_kn.ipk" {
		t.Fatalf("взят не kn-таргет: %s", res.Asset)
	}
	if res.Bin != "/opt/bin/magitrickled" {
		t.Fatalf("bin: %s", res.Bin)
	}
	if !run.saw("/opt/etc/init.d/S99magitrickle start") {
		t.Errorf("нет старта S99; вызовы: %v", run.calls)
	}
	// Entware-скрипты не умеют enable - шаг не делаем (шум «Usage» в логе)
	if run.saw("enable") {
		t.Errorf("enable не должен вызываться на Кинетике; вызовы: %v", run.calls)
	}
	if strings.Contains(res.Log, "байт") || !strings.Contains(res.Log, "КБ") {
		t.Errorf("размер скачивания должен быть человекочитаемым: %s", res.Log)
	}
}

// живой кейс: Кинетик на aarch64-Entware раньше отказывался целиком
// («неожиданная архитектура Entware»), теперь ставится из _kn-сборки
func TestInstallKeeneticAarch64(t *testing.T) {
	dir := t.TempDir()
	run := &fakeRun{}
	rel := &Release{TagName: "0.8.2-rev12", Assets: []Asset{
		{Name: "magitrickle_0.8.2-12_entware_aarch64-3.10.ipk", URL: "http://x/1"},
		{Name: "magitrickle_0.8.2-12_entware_aarch64-3.10_kn.ipk", URL: "http://x/2", Size: 8 << 20},
	}}
	res, err := Install(context.Background(), Options{
		Platform: "keenetic",
		Detect:   func() (string, string, error) { return "aarch64-3.10", "opkg print-architecture (Entware)", nil },
		Latest:   func(context.Context) (*Release, error) { return rel, nil },
		Fetch:    fetchFake(300_000),
		Run:      run.run,
		TmpDir:   dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Asset != "magitrickle_0.8.2-12_entware_aarch64-3.10_kn.ipk" {
		t.Fatalf("взят не kn-таргет: %s", res.Asset)
	}
	if !run.saw("/opt/etc/init.d/S99magitrickle start") {
		t.Errorf("нет старта S99; вызовы: %v", run.calls)
	}
}

func TestInstallFreshNoStop(t *testing.T) {
	dir := t.TempDir()
	run := &fakeRun{script: map[string]resp{
		"/etc/init.d/magitrickle|stop": {out: "not found", err: errors.New("exit 127")},
	}}
	res, err := Install(context.Background(), Options{
		Platform: "openwrt",
		Detect:   func() (string, string, error) { return "x86_64", "opkg", nil },
		Latest:   func(context.Context) (*Release, error) { return testRelease(), nil },
		Fetch:    fetchFake(300_000),
		Run:      run.run,
		TmpDir:   dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Log, "стоп") {
		t.Errorf("в логе должен быть честный пропуск стопа: %s", res.Log)
	}
}

func TestInstallPackageApkArgs(t *testing.T) {
	var got string
	run := func(argv0 string, args ...string) ([]byte, error) {
		got = argv0 + " " + strings.Join(args, " ")
		return nil, nil
	}
	if _, err := installPackage(run, "apk", "/tmp/p.apk"); err != nil {
		t.Fatal(err)
	}
	if got != "apk add --allow-untrusted /tmp/p.apk" {
		t.Fatalf("apk args: %s", got)
	}
}

func TestInstallDepRetry(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "magitrickle_0.8.2-9_openwrt_x86_64.ipk")
	run := &fakeRun{failOnce: map[string]resp{
		"opkg|install " + dest: {out: "Collected errors:\nCannot find package iptables-nft", err: errors.New("exit 5")},
	}}
	_, err := Install(context.Background(), Options{
		Platform: "openwrt",
		Detect:   func() (string, string, error) { return "x86_64", "opkg", nil },
		Latest:   func(context.Context) (*Release, error) { return testRelease(), nil },
		Fetch:    fetchFake(300_000),
		Run:      run.run,
		TmpDir:   dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !run.saw("opkg update") {
		t.Errorf("нет opkg update после зависимостного отказа; вызовы: %v", run.calls)
	}
	if !run.saw("/etc/init.d/magitrickle start") {
		t.Error("нет старта службы после повторной установки")
	}
}

func TestInstallLeavesRunningOnFatalError(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "magitrickle_0.8.2-9_openwrt_x86_64.ipk")
	run := &fakeRun{failOnce: map[string]resp{
		"opkg|install " + dest: {out: "md5 sum mismatch", err: errors.New("exit 4")},
	}}
	_, err := Install(context.Background(), Options{
		Platform: "openwrt",
		Detect:   func() (string, string, error) { return "x86_64", "opkg", nil },
		Latest:   func(context.Context) (*Release, error) { return testRelease(), nil },
		Fetch:    fetchFake(300_000),
		Run:      run.run,
		TmpDir:   dir,
	})
	if err == nil {
		t.Fatal("ожидалась ошибка установки")
	}
	// без зависимостного паттерна ретрая нет, но MT поднят обратно (старый бинарь цел)
	if !run.saw("/etc/init.d/magitrickle start") {
		t.Errorf("после отказа MT не поднят обратно; вызовы: %v", run.calls)
	}
	if run.saw("opkg update") {
		t.Error("не ожидался opkg update без зависимостного отказа")
	}
}

// opkg при совпавшей версии молча пропускает установку («up to date») -
// принудительная переустановка (живой кейс: бинарь подменён вручную,
// база opkg не переключена - тест владельца на 0.42)
func TestInstallOpkgUpToDateForced(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "magitrickle_0.8.2-9_openwrt_x86_64.ipk")
	run := &fakeRun{script: map[string]resp{
		"opkg|install " + dest: {out: "Package magitrickle (0.8.2-12) installed in root is up to date."},
	}}
	if _, err := Install(context.Background(), Options{
		Platform: "openwrt",
		Detect:   func() (string, string, error) { return "x86_64", "opkg", nil },
		Latest:   func(context.Context) (*Release, error) { return testRelease(), nil },
		Fetch:    fetchFake(300_000),
		Run:      run.run,
		TmpDir:   dir,
	}); err != nil {
		t.Fatal(err)
	}
	if !run.saw("opkg install --force-reinstall "+dest) {
		t.Errorf("нет принудительной переустановки; вызовы: %v", run.calls)
	}
	if !run.saw("/etc/init.d/magitrickle start") {
		t.Error("нет старта службы")
	}
}

// битая запись пакета в базе opkg (чужая архитектура): «up to date» ->
// force-reinstall срывается в download - лечится remove + чистая установка
// из файла (живой кейс Keenetic-aarch64, Entware с чужой записью 0.8.2-12)
func TestInstallOpkgBrokenRecordRemoveReinstall(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "magitrickle_0.8.2-12_entware_aarch64-3.10_kn.ipk")
	run := &fakeRun{script: map[string]resp{
		"opkg|install " + dest: {out: "Package magitrickle (0.8.2-12) installed in root is up to date."},
		"opkg|install --force-reinstall " + dest: {
			out:  "Collected errors:\n * opkg_download_pkg: Package magitrickle is not available from any configured src.",
			err:  errors.New("exit status 255"),
		},
	}}
	if _, err := Install(context.Background(), Options{
		Platform: "keenetic",
		Detect:   func() (string, string, error) { return "aarch64-3.10", "opkg print-architecture (Entware)", nil },
		Latest: func(context.Context) (*Release, error) {
			return &Release{TagName: "0.8.2-rev12", Assets: []Asset{
				{Name: "magitrickle_0.8.2-12_entware_aarch64-3.10_kn.ipk", URL: "http://x/kn", Size: 9 << 20},
			}}, nil
		},
		Fetch: fetchFake(300_000),
		Run:   run.run,
		TmpDir: dir,
	}); err != nil {
		t.Fatal(err)
	}
	if !run.saw("opkg remove magitrickle") {
		t.Errorf("up-to-date forced reinstall ne srabotal; calls: %v", run.calls)
	}
	if !run.saw("opkg install " + dest) {
		t.Errorf("net chistoy ustanovki iz fayla; calls: %v", run.calls)
	}
	if !run.saw("/opt/etc/init.d/S99magitrickle start") {
		t.Error("net starta sluzhby")
	}
}

func TestInstallSmallDownloadRejected(t *testing.T) {
	dir := t.TempDir()
	_, err := Install(context.Background(), Options{
		Platform: "openwrt",
		Detect:   func() (string, string, error) { return "x86_64", "opkg", nil },
		Latest:   func(context.Context) (*Release, error) { return testRelease(), nil },
		Fetch:    fetchFake(1000),
		Run:      func(string, ...string) ([]byte, error) { return nil, nil },
		TmpDir:   dir,
	})
	if err == nil || !strings.Contains(err.Error(), "подозрительно мал") {
		t.Fatalf("жду отказ на малый размер; err=%v", err)
	}
}

func TestInstallAssetMissing(t *testing.T) {
	dir := t.TempDir()
	_, err := Install(context.Background(), Options{
		Platform: "openwrt",
		Detect:   func() (string, string, error) { return "riscv64_generic", "opkg", nil },
		Latest:   func(context.Context) (*Release, error) { return testRelease(), nil },
		Fetch:    fetchFake(300_000),
		Run:      func(string, ...string) ([]byte, error) { return nil, nil },
		TmpDir:   dir,
	})
	if err == nil || !strings.Contains(err.Error(), "нет пакета для openwrt/riscv64_generic") {
		t.Fatalf("жду честный отказ с таргетом; err=%v", err)
	}
}

func TestDepRetryPatterns(t *testing.T) {
	for _, s := range []string{
		"Collected errors:\nCannot find package iptables-nft",
		"opkg_install_pkg: Package magitrickle depends on iptables-mod-conntrack-extra",
		"ERROR: unable to select packages: no satisfiable candidates",
		"apk install: ERROR: unable to select packages:",
	} {
		if !depRetry("opkg", s) && !depRetry("apk", s) {
			t.Errorf("depRetry(%q) = false", s)
		}
	}
	if depRetry("opkg", "md5 sum mismatch") {
		t.Error("depRetry не должен срабатывать на битый пакет")
	}
}

// живой кейс v0.7.3: устройство OpenWrt 25 с /etc/apk/arch = aarch64,
// пакет aarch64_generic - солвер отказывает «unable to select packages»,
// лечится повтором с --arch <arch пакета>
func TestInstallApkArchOverride(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "magitrickle_0.8.2-r12_openwrt_aarch64_generic.apk")
	run := &fakeRun{script: map[string]resp{
		"apk|add --allow-untrusted " + dest: {out: "ERROR: unable to select packages:", err: errors.New("exit status 1")},
	}}
	res, err := Install(context.Background(), Options{
		Platform: "openwrt",
		Detect:   func() (string, string, error) { return "aarch64", "apk --print-arch", nil },
		Latest: func(context.Context) (*Release, error) {
			return &Release{TagName: "0.8.2-rev12", Assets: []Asset{
				{Name: "magitrickle_0.8.2-r12_openwrt_aarch64_generic.apk", URL: "http://x/apk", Size: 9 << 20},
			}}, nil
		},
		Fetch:            fetchFake(300_000),
		Run:              run.run,
		PackageManagerFn: func() (string, string) { return "apk", ".apk" },
		TmpDir:           dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !run.saw("apk update") {
		t.Errorf("нет apk update перед повтором; вызовы: %v", run.calls)
	}
	if !run.saw("apk add --allow-untrusted --arch aarch64_generic "+dest) {
		t.Errorf("нет повтора с --arch; вызовы: %v", run.calls)
	}
	if !run.saw("/etc/init.d/magitrickle start") {
		t.Errorf("нет старта службы; вызовы: %v", run.calls)
	}
	if !strings.Contains(res.Log, "скачано 293 КБ") {
		t.Errorf("размер скачивания должен быть в КБ/МБ: %s", res.Log)
	}
}

// лечение iptables должно происходить ДО остановки MT (сеть жива) -
// предполётный чек, живой кейс Keenetic-aarch64
func TestInstallIptablesPreflight(t *testing.T) {
	oldAlive, oldDelay := procAlive, postStartDelay
	defer func() { procAlive, postStartDelay = oldAlive, oldDelay }()
	procAlive = func() bool { return true }
	postStartDelay = 0

	dir := t.TempDir()
	run := &fakeRun{script: map[string]resp{
		"iptables-save|": {
			out: "iptables-save v1.4.21: Can't find library for match 'ndmmark'",
			err: errors.New("exit status 1"),
		},
		"/opt/sbin/iptables-save|": {
			out: "iptables-save v1.4.21: Can't find library for match 'ndmmark'",
			err: errors.New("exit status 1"),
		},
	}}
	_, _ = Install(context.Background(), Options{
		Platform: "keenetic",
		Detect:   func() (string, string, error) { return "aarch64-3.10", "opkg print-architecture (Entware)", nil },
		Latest: func(context.Context) (*Release, error) {
			return &Release{TagName: "0.8.2-rev12", Assets: []Asset{
				{Name: "magitrickle_0.8.2-12_entware_aarch64-3.10_kn.ipk", URL: "http://x/kn", Size: 9 << 20},
			}}, nil
		},
		Fetch: fetchFake(300_000),
		Run:   run.run,
		TmpDir: dir,
	})
	stopIdx, healIdx := -1, -1
	for i, c := range run.calls {
		if c == "/opt/etc/init.d/S99magitrickle stop" && stopIdx == -1 {
			stopIdx = i
		}
		if c == "opkg install iptables" && healIdx == -1 {
			healIdx = i
		}
	}
	if stopIdx == -1 || healIdx == -1 || healIdx > stopIdx {
		t.Errorf("лечение iptables не сделано до остановки MT: stop=%d heal=%d; вызовы: %v", stopIdx, healIdx, run.calls)
	}
}

// iptablesNdmmarkBroken - живой вывод пользователя Keenetic-aarch64
// (plain entware-iptables без ndm-расширений).
func TestIptablesNdmmarkBroken(t *testing.T) {
	live := `failed to clear iptables: listing chains error: iptables-save failed: exit status 1: ` +
		`Can't find library for match 'ndmmark'`
	if !iptablesNdmmarkBroken(live) {
		t.Error("живой вывод ndmmark не распознан")
	}
	if iptablesNdmmarkBroken("") || iptablesNdmmarkBroken("Generating dump") {
		t.Error("чистый вывод не должен считаться битым")
	}
}

// живой кейс: пакет встал, start отчитался done, но демон умер сразу
// (plain iptables без ndmmark) - mtinstall сам обновляет iptables до
// keenetic-сборки и стартует заново; не вышло - честная ошибка
func TestInstallPostStartDeathIptablesHeal(t *testing.T) {
	oldAlive, oldDelay := procAlive, postStartDelay
	defer func() { procAlive, postStartDelay = oldAlive, oldDelay }()
	procAlive = func() bool { return true }
	postStartDelay = 0

	dir := t.TempDir()
	dest := filepath.Join(dir, "magitrickle_0.8.2-12_entware_aarch64-3.10_kn.ipk")
	run := &fakeRun{script: map[string]resp{
		"iptables-save|": {
			out: "iptables-save v1.4.21: Can't find library for match 'ndmmark'",
			err: errors.New("exit status 1"),
		},
		"/opt/sbin/iptables-save|": {
			out: "iptables-save v1.4.21: Can't find library for match 'ndmmark'",
			err: errors.New("exit status 1"),
		},
	}}
	_, err := Install(context.Background(), Options{
		Platform: "keenetic",
		Detect:   func() (string, string, error) { return "aarch64-3.10", "opkg print-architecture (Entware)", nil },
		Latest: func(context.Context) (*Release, error) {
			return &Release{TagName: "0.8.2-rev12", Assets: []Asset{
				{Name: "magitrickle_0.8.2-12_entware_aarch64-3.10_kn.ipk", URL: "http://x/kn", Size: 9 << 20},
			}}, nil
		},
		Fetch: fetchFake(300_000),
		Run:   run.run,
		TmpDir: dir,
	})
	if err == nil || !strings.Contains(err.Error(), "сразу после старта") {
		t.Fatalf("жду честную ошибку о смерти демона: %v", err)
	}
	for _, want := range []string{
		"opkg update",
		"opkg install iptables",
		"/opt/sbin/iptables-save", // фолбэк полным путём после отказа PATH-вызова
	} {
		if !run.saw(want) {
			t.Errorf("нет шага лечения %q; вызовы: %v", want, run.calls)
		}
	}
	if n := len(run.calls); n < 1 || run.calls[n-1] != "/opt/etc/init.d/S99magitrickle start" {
		t.Errorf("после лечения не было повторного старта; хвост: %v", run.calls[max(0, n-3):])
	}
	_ = dest
}

// демон умер, но iptables чист - лечение не должно дёргать opkg,
// ошибка честная
func TestInstallPostStartDeathNoHeal(t *testing.T) {
	oldAlive, oldDelay := procAlive, postStartDelay
	defer func() { procAlive, postStartDelay = oldAlive, oldDelay }()
	procAlive = func() bool { return true }
	postStartDelay = 0

	dir := t.TempDir()
	run := &fakeRun{}
	_, err := Install(context.Background(), Options{
		Platform: "openwrt",
		Detect:   func() (string, string, error) { return "x86_64", "opkg", nil },
		Latest:   func(context.Context) (*Release, error) { return testRelease(), nil },
		Fetch:    fetchFake(300_000),
		Run:      run.run,
		TmpDir:   dir,
	})
	if err == nil || !strings.Contains(err.Error(), "сразу после старта") {
		t.Fatalf("жду честную ошибку о смерти демона: %v", err)
	}
	if run.saw("opkg install iptables") {
		t.Errorf("лечение не должно запускаться при чистом iptables-save; вызовы: %v", run.calls)
	}
}

func TestAssetPlatformAndPaths(t *testing.T) {
	if assetPlatform("keenetic") != "entware" || assetPlatform("openwrt") != "openwrt" {
		t.Fatal("assetPlatform")
	}
	i, s, b := ServicePaths("keenetic")
	if i != "/opt/etc/init.d/S99magitrickle" || s != "/opt/var/run/magitrickle.sock" || b != "/opt/bin/magitrickled" {
		t.Fatalf("keenetic: %s %s %s", i, s, b)
	}
	i, s, b = ServicePaths("openwrt")
	if i != "/etc/init.d/magitrickle" || s != "/var/run/magitrickle.sock" || b != "/usr/bin/magitrickled" {
		t.Fatalf("openwrt: %s %s %s", i, s, b)
	}
}

// opkgArchRefused - все живые формулировки отказа «архитектуры нет в
// opkg.conf» (кейсы пользователя Keenetic-aarch64, Entware с одной
// arch-строкой в конфиге).
func TestOpkgArchRefused(t *testing.T) {
	for _, s := range []string{
		"Package magitrickle version 0.8.2-12 has no valid architecture, ignoring.",
		"opkg_download_pkg: Package magitrickle is not available from any configured src.",
		"pkg_hash_fetch_best_installation_candidate: Packages for magitrickle found, but incompatible with the architectures configured",
		"Unknown package 'magitrickle'.",
	} {
		if !opkgArchRefused(s) {
			t.Errorf("opkgArchRefused(%q) = false", s)
		}
	}
	if opkgArchRefused("md5 sum mismatch") || opkgArchRefused("Successfully installed") {
		t.Error("opkgArchRefused не должен срабатывать на посторонних ошибках")
	}
}

func TestEnsureOpkgArch(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "opkg.conf")
	if err := os.WriteFile(conf, []byte("arch aarch64-3.10 300\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// отсутствующий arch добавляется, занятый приоритет сдвигается
	if !ensureOpkgArch(conf, "all", 50) {
		t.Fatal("arch all не добавлен")
	}
	if !ensureOpkgArch(conf, "aarch64-3.10_kn", 300) {
		t.Fatal("kn-arch не добавлен")
	}
	data, err := os.ReadFile(conf)
	if err != nil {
		t.Fatal(err)
	}
	want := "arch aarch64-3.10 300\narch all 50\narch aarch64-3.10_kn 310\n"
	if string(data) != want {
		t.Fatalf("conf = %q, хочу %q", string(data), want)
	}
	// повторный вызов - изменений нет
	if ensureOpkgArch(conf, "aarch64-3.10_kn", 300) {
		t.Fatal("повторная добавка при уже существующей строке")
	}
	if ensureOpkgArch(filepath.Join(dir, "нет-такого"), "all", 50) {
		t.Fatal("отсутствующий файл должен молча пропускаться")
	}
	if ensureOpkgArch(conf, "", 50) {
		t.Fatal("пустая архитектура не должна добавляться")
	}
}

func TestMagitricklePids(t *testing.T) {
	old := procDir
	defer func() { procDir = old }()
	dir := t.TempDir()
	procDir = dir
	mk := func(pid, comm string) {
		d := filepath.Join(dir, pid)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "comm"), []byte(comm+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("42", "magitrickled")
	mk("43", "mawg")
	mk("не-число", "magitrickled")
	if err := os.WriteFile(filepath.Join(dir, "notapid"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	pids := magitricklePids()
	if len(pids) != 1 {
		t.Fatalf("pids = %v, жду только 42", pids)
	}
	if _, ok := pids[42]; !ok {
		t.Fatalf("pids = %v, нет 42", pids)
	}
}

// живой кейс: чужая установка - init-скрипта нет (stop отказом), но
// процесс magitrickled жив - он гасится kill'ом до замены файлов
func TestInstallKillsForeignMT(t *testing.T) {
	old := procDir
	defer func() { procDir = old }()
	dir := t.TempDir()
	procDir = dir
	mtd := filepath.Join(dir, "4242")
	if err := os.MkdirAll(mtd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mtd, "comm"), []byte("magitrickled\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := &fakeRun{script: map[string]resp{
		"/etc/init.d/magitrickle|stop": {out: "not found", err: errors.New("exit 127")},
	}}
	res, err := Install(context.Background(), Options{
		Platform: "openwrt",
		Detect:   func() (string, string, error) { return "x86_64", "opkg", nil },
		Latest:   func(context.Context) (*Release, error) { return testRelease(), nil },
		Fetch:    fetchFake(300_000),
		Run:      run.run,
		TmpDir:   t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !run.saw("kill -9 4242") {
		t.Errorf("чужой MT не погашен; вызовы: %v", run.calls)
	}
	if !strings.Contains(res.Log, "мимо пакета") {
		t.Errorf("в логе должно быть честное предупреждение о чужом MT: %s", res.Log)
	}
}

// живой кейс Keenetic-aarch64 (WBR3000UAX): opkg.conf без arch-строк -
// «Packages for magitrickle found, but incompatible with the architectures
// configured» + «Unknown package»; лечение: дописать arch all и arch пакета,
// снять битую запись, поставить из файла
func TestInstallOpkgArchHeal(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "opkg.conf")
	if err := os.WriteFile(conf, []byte("arch aarch64-3.10 300\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "magitrickle_0.8.2-12_entware_aarch64-3.10_kn.ipk")
	run := &fakeRun{failOnce: map[string]resp{
		"opkg|install " + dest: {
			out: "Unknown package 'magitrickle'.\nCollected errors:\n" +
				" * pkg_hash_fetch_best_installation_candidate: Packages for magitrickle found, but incompatible with the architectures configured\n" +
				" * opkg_install_cmd: Cannot install package magitrickle.",
			err: errors.New("exit status 255"),
		},
	}}
	if _, err := Install(context.Background(), Options{
		Platform: "keenetic",
		Detect:   func() (string, string, error) { return "aarch64-3.10", "opkg print-architecture (Entware)", nil },
		Latest: func(context.Context) (*Release, error) {
			return &Release{TagName: "0.8.2-rev12", Assets: []Asset{
				{Name: "magitrickle_0.8.2-12_entware_aarch64-3.10_kn.ipk", URL: "http://x/kn", Size: 9 << 20},
			}}, nil
		},
		Fetch:    fetchFake(300_000),
		Run:      run.run,
		OpkgConf: conf,
		TmpDir:   dir,
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(conf)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"arch all 50\n", "arch aarch64-3.10_kn 310\n"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("в opkg.conf нет %q: %q", want, string(data))
		}
	}
	if !run.saw("opkg remove magitrickle") {
		t.Errorf("битая запись не снята; вызовы: %v", run.calls)
	}
	// чистая установка из файла после лечения (второй вызов install)
	n := 0
	for _, c := range run.calls {
		if c == "opkg install "+dest {
			n++
		}
	}
	if n != 2 {
		t.Errorf("ожидался повтор install из файла (2 вызова), было %d; вызовы: %v", n, run.calls)
	}
	if !run.saw("/opt/etc/init.d/S99magitrickle start") {
		t.Error("нет старта службы")
	}
}
