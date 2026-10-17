package mtinstall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	}
	for _, c := range cases {
		if got := FirstOpkgArch(c.out); got != c.want {
			t.Errorf("FirstOpkgArch(%q) = %q, хочу %q", c.out, got, c.want)
		}
	}
}

func TestPickAsset(t *testing.T) {
	rel := &Release{TagName: "0.8.2-rev9", Assets: []Asset{
		{Name: "magitrickle_0.8.2-9_openwrt_x86_64.ipk", URL: "u1", Size: 1},
		{Name: "magitrickle_0.8.2-r9_openwrt_x86_64.apk", URL: "u2", Size: 1},
		{Name: "magitrickle_0.8.2-9_entware_mipsel-3.4_kn.ipk", URL: "u3", Size: 1},
		{Name: "SHA256SUMS"},
	}}
	if a, err := PickAsset(rel, "openwrt", "x86_64", ".ipk"); err != nil || a.Name != "magitrickle_0.8.2-9_openwrt_x86_64.ipk" {
		t.Fatalf("ipk: %v %+v", err, a)
	}
	if a, err := PickAsset(rel, "openwrt", "x86_64", ".apk"); err != nil || a.Name != "magitrickle_0.8.2-r9_openwrt_x86_64.apk" {
		t.Fatalf("apk: %v %+v", err, a)
	}
	if a, err := PickAsset(rel, "entware", "mipsel-3.4_kn", ".ipk"); err != nil || a.Name != "magitrickle_0.8.2-9_entware_mipsel-3.4_kn.ipk" {
		t.Fatalf("kn: %v %+v", err, a)
	}
	// суффикс без _kn не должен матчить kn-таргет
	if _, err := PickAsset(rel, "entware", "mipsel-3.4", ".ipk"); err == nil {
		t.Fatal("mipsel-3.4 без _kn не должен находиться")
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
		Detect:   func() (string, string, error) { return "mipsel-3.4_kn", "opkg+_kn", nil },
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
	} {
		if !depRetry("opkg", s) {
			t.Errorf("depRetry(%q) = false", s)
		}
	}
	if depRetry("opkg", "md5 sum mismatch") {
		t.Error("depRetry не должен срабатывать на битый пакет")
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
