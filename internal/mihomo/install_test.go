package mihomo

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fakeVer = "v1.19.33"

// fakeCore - релиз MetaCubeX с одним ассетом под arch; Fetch отдаёт
// gz-бинарь с верным digest; Run отвечает -v на любой бинарь.
type fakeCore struct {
	arch    string
	tag     string
	digest  string
	gz      []byte
	fetched int
	runBin  string // содержимое «бинаря» для проверки подмены
}

func newFakeCore(t *testing.T, arch, tag string) *fakeCore {
	t.Helper()
	bin := []byte("FAKE MIHOMO BINARY " + tag)
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write(bin)
	zw.Close()
	sum := sha256.Sum256(buf.Bytes())
	return &fakeCore{arch: arch, tag: tag, digest: hex.EncodeToString(sum[:]), gz: buf.Bytes(), runBin: string(bin)}
}

func (f *fakeCore) discover(ctx context.Context) ([]Release, error) {
	name := "mihomo-linux-" + f.arch + "-" + f.tag + ".gz"
	rel := Release{Tag: f.tag, Names: map[string]string{f.arch: name},
		Sizes: map[string]int64{f.arch: int64(len(f.gz))}, Digest: map[string]string{name: "sha256:" + f.digest}}
	// рядом второй арх и go120-вариант: не должны мешать выбору
	rel.Names["amd64"] = "mihomo-linux-amd64-" + f.tag + ".gz"
	rel.Names["386-go120"] = "mihomo-linux-386-go120-" + f.tag + ".gz"
	return []Release{rel}, nil
}

func (f *fakeCore) fetch(ctx context.Context, url, dest string) (int64, error) {
	f.fetched++
	if !strings.Contains(url, "mihomo-linux-"+f.arch+"-") {
		return 0, fmt.Errorf("неожижанный url %s", url)
	}
	if err := os.WriteFile(dest, f.gz, 0o644); err != nil {
		return 0, err
	}
	return int64(len(f.gz)), nil
}

func (f *fakeCore) run(argv0 string, args ...string) ([]byte, error) {
	if _, err := os.Stat(argv0); err != nil {
		return nil, fmt.Errorf("запуск несуществующего бинаря %s", argv0)
	}
	if len(args) == 1 && args[0] == "-v" {
		return []byte("Mihomo Meta " + fakeVer + " linux 386 with go1.26.9\n"), nil
	}
	return nil, fmt.Errorf("неожижанная команда %v", args)
}

func (f *fakeCore) opts(target Target) InstallOptions {
	return InstallOptions{
		Target:   target,
		Arch:     f.arch,
		Discover: f.discover,
		Fetch:    f.fetch,
		Run:      f.run,
		TmpDir:   tDir(),
		MinSize:  16,
	}
}

func tDir() string { return os.TempDir() }

func TestParseAssetName(t *testing.T) {
	cases := []struct {
		name string
		arch string
		ok   bool
	}{
		{"mihomo-linux-mipsle-softfloat-v1.19.33.gz", "mipsle-softfloat", true},
		{"mihomo-linux-mips-softfloat-v1.19.33.gz", "mips-softfloat", true},
		{"mihomo-linux-386-v1.19.33.gz", "386", true},
		{"mihomo-linux-arm64-v1.19.33.gz", "arm64", true},
		{"mihomo-linux-armv7-v1.19.33.gz", "armv7", true},
		{"mihomo-linux-386-go120-v1.19.33.gz", "", false},
		{"mihomo-linux-amd64-v1.19.33.deb", "", false},
		{"mihomo-windows-amd64-v1.19.33.zip", "", false},
	}
	for _, c := range cases {
		arch, ok := parseAssetName(c.name)
		if ok != c.ok || arch != c.arch {
			t.Fatalf("%s: получили %q,%v ожидалось %q,%v", c.name, arch, ok, c.arch, c.ok)
		}
	}
}

func TestMihomoVersionParse(t *testing.T) {
	v, err := MihomoVersion("mihomo", func(argv0 string, args ...string) ([]byte, error) {
		return []byte("Mihomo Meta v1.19.33 linux 386 with go1.26.9 9b0e5c5\n"), nil
	})
	if err != nil || v != "v1.19.33" {
		t.Fatalf("версия: %q %v", v, err)
	}
}

func TestInstallFresh(t *testing.T) {
	dir := t.TempDir()
	target := Target{Bin: filepath.Join(dir, "bin", "mihomo"), MarkerDir: filepath.Join(dir, "marker"), DiskPath: dir}
	fc := newFakeCore(t, "386", "v1.19.33")
	res, err := InstallCore(context.Background(), fc.opts(target))
	if err != nil {
		t.Fatalf("установка: %v (лог: %s)", err, res.Log)
	}
	if res.Version != fakeVer || res.Bin != target.Bin {
		t.Fatalf("результат: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(target.MarkerDir, MarkerName)); err != nil {
		t.Fatal("маркер не записан")
	}
	data, err := os.ReadFile(target.Bin)
	if err != nil || string(data) != fc.runBin {
		t.Fatalf("бинарь подменён неверно: %v", err)
	}
	if res.Backup != "" {
		t.Fatalf("бэкапа у свежей установки быть не должно: %+v", res)
	}
}

func TestInstallForeignRefused(t *testing.T) {
	dir := t.TempDir()
	target := Target{Bin: filepath.Join(dir, "mihomo"), MarkerDir: filepath.Join(dir, "marker")}
	if err := os.WriteFile(target.Bin, []byte("чужой mihomo"), 0o755); err != nil {
		t.Fatal(err)
	}
	fc := newFakeCore(t, "386", "v1.19.33")
	_, err := InstallCore(context.Background(), fc.opts(target))
	if err == nil || !strings.Contains(err.Error(), "чужой") {
		t.Fatalf("чужой бинарь должен отказываться: %v", err)
	}
	if fc.fetched != 0 {
		t.Fatal("при отказе скачивания быть не должно")
	}
	// с явным согласием - замена с бэкапом
	opts := fc.opts(target)
	opts.ReplaceForeign = true
	res, err := InstallCore(context.Background(), opts)
	if err != nil {
		t.Fatalf("замена по согласию: %v (лог: %s)", err, res.Log)
	}
	if res.Backup != target.Bin+".pre-mawg" {
		t.Fatalf("бэкап чужого: %+v", res)
	}
	saved, _ := os.ReadFile(res.Backup)
	if string(saved) != "чужой mihomo" {
		t.Fatalf("в бэкапе должно быть старое содержимое: %q", saved)
	}
}

func TestInstallUpToDate(t *testing.T) {
	dir := t.TempDir()
	target := Target{Bin: filepath.Join(dir, "mihomo"), MarkerDir: filepath.Join(dir, "marker")}
	os.MkdirAll(target.MarkerDir, 0o755)
	os.WriteFile(filepath.Join(target.MarkerDir, MarkerName), []byte("tag="+fakeVer+"\n"), 0o644)
	os.WriteFile(target.Bin, []byte("наша установка"), 0o755)
	fc := newFakeCore(t, "386", fakeVer)
	res, err := InstallCore(context.Background(), fc.opts(target))
	if err != nil {
		t.Fatal(err)
	}
	if !res.UpToDate || fc.fetched != 0 {
		t.Fatalf("актуальная версия не перекачивается: %+v fetched=%d", res, fc.fetched)
	}
}

func TestInstallDigestMismatch(t *testing.T) {
	dir := t.TempDir()
	target := Target{Bin: filepath.Join(dir, "mihomo"), MarkerDir: filepath.Join(dir, "marker")}
	fc := newFakeCore(t, "386", "v1.19.33")
	fc.digest = strings.Repeat("0", 64)
	res, err := InstallCore(context.Background(), fc.opts(target))
	if err == nil || !strings.Contains(err.Error(), "контрольная сумма") {
		t.Fatalf("битый digest должен ловиться: %v", err)
	}
	if _, serr := os.Stat(target.Bin); serr == nil {
		t.Fatal("бинарь не должен ставиться при битой сумме")
	}
	_ = res
}

func TestInstallArchMissing(t *testing.T) {
	dir := t.TempDir()
	target := Target{Bin: filepath.Join(dir, "mihomo"), MarkerDir: filepath.Join(dir, "marker")}
	fc := newFakeCore(t, "386", "v1.19.33")
	opts := fc.opts(target)
	opts.Arch = "riscv64"
	_, err := InstallCore(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "нет ассета") {
		t.Fatalf("отсутствующий арх - честная ошибка: %v", err)
	}
}
