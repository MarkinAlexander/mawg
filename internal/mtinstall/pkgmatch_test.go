package mtinstall

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTarGz(t *testing.T, path string, fill func(*tar.Writer)) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	fill(tw)
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func tarFile(t *testing.T, tw *tar.Writer, name, body string) {
	t.Helper()
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
}

// buildIpk - ipk как у релиза форка: tar.gz из debian-binary и
// data.tar.gz, внутри - бинарь MT с путём member и init-скрипт.
func buildIpk(t *testing.T, member, binBody string) string {
	t.Helper()
	dir := t.TempDir()
	data := filepath.Join(dir, "data.tar.gz")
	writeTarGz(t, data, func(tw *tar.Writer) {
		tarFile(t, tw, "./"+strings.TrimPrefix(member, "/"), binBody)
		tarFile(t, tw, "./etc/init.d/magitrickle", "#!/bin/sh\n")
	})
	ipk := filepath.Join(dir, rev23Pkg)
	writeTarGz(t, ipk, func(tw *tar.Writer) {
		tarFile(t, tw, "./debian-binary", "2.0\n")
		f, err := os.Open(data)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		st, _ := f.Stat()
		if err := tw.WriteHeader(&tar.Header{Name: "./data.tar.gz", Mode: 0o644, Size: st.Size()}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(tw, f); err != nil {
			t.Fatal(err)
		}
	})
	return ipk
}

const pkgBin = "/usr/bin/magitrickled"

func TestPkgBinMatches(t *testing.T) {
	ipk := buildIpk(t, pkgBin, "BINARY-REV23")
	bin := filepath.Join(t.TempDir(), "magitrickled")
	put := func(body string) {
		if err := os.WriteFile(bin, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// тот же бинарь - переустанавливать нечего
	put("BINARY-REV23")
	if same, err := pkgBinMatches(ipk, pkgBin, bin); err != nil || !same {
		t.Fatalf("совпавший бинарь: same=%v err=%v", same, err)
	}
	// подмена бинаря вручную (кейс 0.42)
	put("BINARY-OTHER")
	if same, err := pkgBinMatches(ipk, pkgBin, bin); err != nil || same {
		t.Fatalf("подменённый бинарь: same=%v err=%v", same, err)
	}
	// бинаря на диске нет (нулевой после жёсткой перезагрузки или удалён)
	if err := os.Remove(bin); err != nil {
		t.Fatal(err)
	}
	if same, err := pkgBinMatches(ipk, pkgBin, bin); err != nil || same {
		t.Fatalf("нет бинаря: same=%v err=%v", same, err)
	}
	// пакет не разобран - честная ошибка, вызывающий переустановит
	noBin := buildIpk(t, "/usr/bin/other", "x")
	if _, err := pkgBinMatches(noBin, pkgBin, bin); err == nil {
		t.Error("пакет без бинаря должен давать ошибку")
	}
	junk := filepath.Join(t.TempDir(), "junk.ipk")
	if err := os.WriteFile(junk, []byte("!<arch>\nar-format\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := pkgBinMatches(junk, pkgBin, bin); err == nil {
		t.Error("ar-пакет должен давать ошибку")
	}
	if _, err := pkgBinMatches(filepath.Join(t.TempDir(), "нет.ipk"), pkgBin, bin); err == nil {
		t.Error("отсутствующий пакет должен давать ошибку")
	}
}

// opkg «up to date» + совпавший бинарь: пакет не переписывается (флеш
// не трогается, версия та же - SameVersion), служба просто проверяется;
// подменённый бинарь (кейс 0.42) по-прежнему переустанавливается
// принудительно
func TestInstallUpToDateSkipsReinstall(t *testing.T) {
	oldPaths := servicePaths
	defer func() { servicePaths = oldPaths }()
	dir := t.TempDir()
	// установщик забирает загруженный пакет с собой - на каждый прогон свой
	member := filepath.ToSlash(filepath.Join(dir, "magitrickled"))
	bin := member
	initScript := filepath.Join(dir, "init")
	servicePaths = func(string) (string, string, string) {
		return initScript, filepath.Join(dir, "sock"), bin
	}
	opts := func() Options {
		return Options{
			Platform:         "openwrt",
			LocalPackage:     buildIpk(t, member, "BINARY-REV23"),
			MinSize:          1,
			Detect:           func() (string, string, error) { return "", "", errors.New("opkg недоступен") },
			PackageManagerFn: func() (string, string) { return "opkg", ".ipk" },
		}
	}
	upToDate := func(o Options) *fakeRun {
		run := &fakeRun{}
		run.script = map[string]resp{
			"opkg|install " + o.LocalPackage: {out: "Package magitrickle (0.8.2-23) installed in root is up to date."},
		}
		return run
	}

	put := func(body string) {
		if err := os.WriteFile(bin, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	put("BINARY-REV23")
	o := opts()
	run := upToDate(o)
	o.Run = run.run
	res, err := Install(context.Background(), o)
	if err != nil {
		t.Fatalf("%v\n%s", err, res.Log)
	}
	if !res.SameVersion || run.saw("--force-reinstall") {
		t.Errorf("совпавший бинарь: жду SameVersion без переустановки: %+v; вызовы: %v", res, run.calls)
	}
	if !strings.Contains(res.Log, "переустановку не делаю") || !run.saw(initScript+" start") {
		t.Errorf("жду отказ от переустановки и старт службы:\n%s\nвызовы: %v", res.Log, run.calls)
	}

	put("BINARY-OTHER")
	o = opts()
	run = upToDate(o)
	o.Run = run.run
	res, err = Install(context.Background(), o)
	if err != nil || !res.SameVersion || !run.saw("opkg install --force-reinstall "+o.LocalPackage) {
		t.Errorf("подменённый бинарь: жду принудительную переустановку: %+v %v; вызовы: %v", res, err, run.calls)
	}
}

// 0.52 (OpenWrt 25.12, apk): повторное нажатие «обновить MT» на уже
// стоящем rev23 - apk ничего не меняет, а панель 15с ждала смены версии и
// пугала «бинарь мог не замениться»
func TestApkSameVersion(t *testing.T) {
	installed := "magitrickle-0.8.2-r23 x86_64 {feeds/packages/feeds/magitrickle/net/magitrickle} (GPL-3.0-or-later) [installed]\n"
	run := func(out string) func(string, ...string) ([]byte, error) {
		return func(string, ...string) ([]byte, error) { return []byte(out), nil }
	}
	cases := []struct {
		asset, out string
		want       bool
	}{
		{"magitrickle_0.8.2-r23_openwrt_x86_64.apk", installed, true},
		{"magitrickle_0.8.2-r23_openwrt_aarch64_cortex-a53.apk", installed, true},
		{"magitrickle_0.8.2-r24_openwrt_x86_64.apk", installed, false},
		{"magitrickle_0.8.3_pre20261124120000.abc_r1_openwrt_x86_64.apk", "magitrickle-0.8.3_pre20261124120000.abc-r1 x86_64 {x} (GPL) [installed]\n", true},
		{"magitrickle_0.8.2-r23_openwrt_x86_64.apk", "", false}, // пакета нет в базе
		{"mawg-core-upload.bin", installed, false},              // загруженный файл: версии в имени нет
		{"magitrickle_0.8.2-r23_openwrt_x86_64.apk", "mawgtest-1 x86_64 {x} (GPL) [installed]\n", false},
	}
	for _, c := range cases {
		if got := apkSameVersion(run(c.out), c.asset); got != c.want {
			t.Errorf("apkSameVersion(%q) = %v, хочу %v", c.asset, got, c.want)
		}
	}
	if apkSameVersion(func(string, ...string) ([]byte, error) {
		return nil, errors.New("apk молчит")
	}, "magitrickle_0.8.2-r23_openwrt_x86_64.apk") {
		t.Error("отказ apk не должен считаться совпадением версий")
	}
}

// сквозной путь: apk + та же версия - SameVersion без «up to date» в выводе
func TestInstallApkSameVersionFlag(t *testing.T) {
	newFakeDaemon(t)
	opts := newFakeDaemon(t).installOpts(nil)
	opts.PackageManagerFn = func() (string, string) { return "apk", ".apk" }
	opts.Detect = func() (string, string, error) { return "x86_64", "/etc/apk/arch", nil }
	opts.Latest = func(context.Context) (*Release, error) {
		return &Release{TagName: "0.8.2-rev23", Assets: []Asset{
			{Name: "magitrickle_0.8.2-r23_openwrt_x86_64.apk", URL: "http://x/apk", Size: 9 << 20},
		}}, nil
	}
	run := &fakeRun{script: map[string]resp{
		"apk|list --installed": {out: "magitrickle-0.8.2-r23 x86_64 {feeds/packages/feeds/magitrickle/net/magitrickle} (GPL-3.0-or-later) [installed]\n"},
	}}
	opts.Run = run.run
	res, err := Install(context.Background(), opts)
	if err != nil || !res.SameVersion {
		t.Fatalf("apk + установленная та же версия: жду SameVersion: %+v %v", res, err)
	}
	if !run.saw("apk add --allow-untrusted ") {
		t.Errorf("установка apk должна пройти; вызовы: %v", run.calls)
	}
}
