package singbox

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestArchFromOpkg(t *testing.T) {
	cases := []struct {
		out, want string
	}{
		{"arch all 1\narch mipsel-3.4 10\n", "mipsle-softfloat"},
		{"arch mipsle-3.4 10\n", "mipsle-softfloat"},
		{"arch mips-3.4 10\n", "mips-softfloat"},
		{"arch aarch64_generic 10\n", "arm64"},
		{"arch arm_cortex-a7_vfpv4 10\n", "armv7"},
		{"arch i386_pentium4 10\n", "386"},
		{"arch x86_64 10\n", "amd64"},
		{"arch riscv64 10\n", "riscv64"},
		{"dest / root\n", ""},
	}
	for _, c := range cases {
		if got := archFromOpkg(c.out); got != c.want {
			t.Errorf("archFromOpkg(%q) = %q, want %q", c.out, got, c.want)
		}
	}
}

func TestArchFromUname(t *testing.T) {
	cases := []struct {
		m, want, note string
	}{
		{"x86_64", "amd64", ""},
		{"i686", "386", ""},
		{"aarch64", "arm64", ""},
		{"armv7l", "armv7", ""},
		{"riscv64", "riscv64", ""},
		{"mipsle", "mipsle-softfloat", ""},
		{"mips", "mipsle-softfloat", "uname говорит mips, предполагаю little-endian"},
		{"sparc", "", ""},
	}
	for _, c := range cases {
		got, note := archFromUname(c.m)
		if got != c.want || (c.note == "" && note != "") || (c.note != "" && note == "") {
			t.Errorf("archFromUname(%q) = %q %q, want %q", c.m, got, note, c.want)
		}
	}
}

func TestArchFromELF(t *testing.T) {
	cases := []struct {
		name    string
		machine []byte
		data    byte
		want    string
	}{
		{"mipsle", []byte{8, 0}, 1, "mipsle-softfloat"},
		{"mips-be", []byte{0, 8}, 2, "mips-softfloat"},
		{"amd64", []byte{0x3e, 0}, 1, "amd64"},
		{"386", []byte{3, 0}, 1, "386"},
		{"arm64", []byte{0xb7, 0}, 1, "arm64"},
		{"armv7", []byte{0x28, 0}, 1, "armv7"},
		{"riscv64", []byte{0xf3, 0}, 1, "riscv64"},
	}
	dir := t.TempDir()
	for _, c := range cases {
		hdr := make([]byte, 20)
		copy(hdr[0:4], "\x7fELF")
		hdr[5] = c.data
		copy(hdr[18:20], c.machine)
		p := filepath.Join(dir, "elf-"+c.name)
		if err := os.WriteFile(p, hdr, 0o644); err != nil {
			t.Fatal(err)
		}
		if got := archFromELF(p); got != c.want {
			t.Errorf("archFromELF(%s) = %q, want %q", c.name, got, c.want)
		}
	}
	if got := archFromELF(filepath.Join(dir, "нет")); got != "" {
		t.Errorf("archFromELF(нет) = %q", got)
	}
}

func TestPickAsset(t *testing.T) {
	fork := Source{Owner: "MarkinAlexander", Repo: "sing-box-lx"}
	alt := Source{Owner: "example", Repo: "sing-box-lx"}
	reports := []SourceReport{
		{Source: alt, Error: "github ответил 403"},
		{Source: fork, Releases: []Release{
			{Tag: "v1.14.2-lx.8", Matrix: map[string]Flavors{"amd64": {Plain: true}}, Names: map[string]FlavorAssets{"amd64": {Plain: "sing-box-1.14.2-lx.8-linux-amd64.tar.gz"}}},
			{Tag: "v1.14.2-lx.7", Matrix: map[string]Flavors{"mipsle-softfloat": {Plain: true, UPX: true}},
				Names: map[string]FlavorAssets{"mipsle-softfloat": {Plain: "p7.tar.gz", PlainSz: 100, UPX: "u7.tar.gz", UPXSz: 30}}},
		}},
	}
	pick, err := PickAsset(reports, "mipsle-softfloat", LXFlavorPlain)
	if err != nil {
		t.Fatal(err)
	}
	if pick.Asset != "p7.tar.gz" || pick.Tag != "v1.14.2-lx.7" || pick.Source != fork {
		t.Fatalf("plain: %+v", pick)
	}
	if !strings.HasSuffix(pick.URL(), "/releases/download/v1.14.2-lx.7/p7.tar.gz") {
		t.Fatalf("url: %s", pick.URL())
	}
	pick, err = PickAsset(reports, "mipsle-softfloat", LXFlavorUPX)
	if err != nil || pick.Asset != "u7.tar.gz" {
		t.Fatalf("upx: %+v err=%v", pick, err)
	}
	if _, err := PickAsset(reports, "386", LXFlavorPlain); err == nil || !strings.Contains(err.Error(), "нет ассета для 386") {
		t.Fatalf("386: %v", err)
	}
	if _, err := PickAsset(reports, "386", "zip"); err == nil || !strings.Contains(err.Error(), "plain или upx") {
		t.Fatalf("flavor: %v", err)
	}
	if _, err := PickAsset(nil, "amd64", LXFlavorPlain); err == nil || !strings.Contains(err.Error(), "нет доступных релизов") {
		t.Fatalf("empty: %v", err)
	}
}

func TestLXNewer(t *testing.T) {
	cases := []struct {
		cand, inst string
		want       bool
	}{
		{"1.14.2-lx.8", "1.14.2-lx.7", true},
		{"1.14.2-lx.7", "1.14.2-lx.7", false},
		{"1.14.2-lx.6", "1.14.2-lx.7", false},
		{"1.15.0-lx.1", "1.14.2-lx.99", true},
		{"v1.14.2-lx.8", "1.14.2-lx.7", true},
		{"1.14.2-lx.1", "1.13.3", true},
		{"мусор", "1.14.2-lx.7", false},
	}
	for _, c := range cases {
		if got := LXNewer(c.cand, c.inst); got != c.want {
			t.Errorf("LXNewer(%q,%q) = %v, want %v", c.cand, c.inst, got, c.want)
		}
	}
}

// fakeLXCore - окружение для сквозного прогона InstallLXCore: tar.gz с
// бинарем, SHA256SUMS, подмена Fetch/Discover/Run.
type fakeLXCore struct {
	dir        string
	tarball    string
	tarBytes   []byte
	tag        string
	asset      string
	targetBin  string
	markerDir  string
	targetVer  string // что отвечает "version" у целевого пути до подмены
	replaced   bool   // после tar-распаковки целимся в lx-версию
	binContent string
}

func newFakeLXCore(t *testing.T, tag string) *fakeLXCore {
	t.Helper()
	f := &fakeLXCore{dir: t.TempDir(), tag: tag}
	var buf bytes.Buffer
	inner := fmt.Sprintf("sing-box-%s-linux-mipsle-softfloat/sing-box", tag)
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := "#!/fake/sing-box-lx\n"
	if err := tw.WriteHeader(&tar.Header{Name: inner, Mode: 0o755, Size: int64(len(body)), ModTime: time.Now()}); err != nil {
		t.Fatal(err)
	}
	tw.Write([]byte(body))
	tw.Close()
	gz.Close()
	f.tarBytes = buf.Bytes()
	f.asset = fmt.Sprintf("sing-box-%s-linux-mipsle-softfloat.tar.gz", tag)
	f.tarball = filepath.Join(f.dir, f.asset)
	f.binContent = body
	sum := sha256.Sum256(f.tarBytes)
	if err := os.WriteFile(filepath.Join(f.dir, "SHA256SUMS"), []byte(fmt.Sprintf("%x  %s\n", sum, f.asset)), 0o644); err != nil {
		t.Fatal(err)
	}
	f.targetBin = filepath.Join(f.dir, "bin", "sing-box")
	f.markerDir = filepath.Join(f.dir, "marker")
	return f
}

func (f *fakeLXCore) reports() []SourceReport {
	return []SourceReport{{Source: Sources[0], Releases: []Release{{
		Tag:    "v" + f.tag,
		Matrix: map[string]Flavors{"mipsle-softfloat": {Plain: true}},
		Names:  map[string]FlavorAssets{"mipsle-softfloat": {Plain: f.asset, PlainSz: int64(len(f.tarBytes))}},
	}}}}
}

func (f *fakeLXCore) opts(replaceForeign bool) LXInstallOptions {
	return LXInstallOptions{
		Flavor: LXFlavorPlain, Arch: "mipsle-softfloat", MinTarball: 1,
		Target:         LXTarget{Bin: f.targetBin, MarkerDir: f.markerDir},
		ReplaceForeign: replaceForeign,
		Discover:       func(ctx context.Context) []SourceReport { return f.reports() },
		Fetch: func(ctx context.Context, url, dest string) (int64, error) {
			if strings.HasSuffix(url, f.asset) {
				return int64(len(f.tarBytes)), os.WriteFile(dest, f.tarBytes, 0o644)
			}
			if strings.HasSuffix(url, "SHA256SUMS") {
				data, _ := os.ReadFile(filepath.Join(f.dir, "SHA256SUMS"))
				return int64(len(data)), os.WriteFile(dest, data, 0o644)
			}
			return 0, fmt.Errorf("неизвестный url %s", url)
		},
		Run: f.run,
	}
}

func (f *fakeLXCore) run(argv0 string, args ...string) ([]byte, error) {
	if argv0 == "tar" {
		// -xzf tarball -C dir
		if err := extractTar(args[1], args[3]); err != nil {
			return nil, err
		}
		f.replaced = true
		return nil, nil
	}
	if strings.HasSuffix(argv0, "sing-box") {
		ver := f.targetVer
		if strings.Contains(argv0, "sing-box-lx-"+f.tag) || f.replaced {
			ver = f.tag
		}
		if ver == "" {
			return nil, fmt.Errorf("%s: не найден", argv0)
		}
		return []byte("sing-box version " + ver + "\nEnvironment: go1.26\n"), nil
	}
	return nil, fmt.Errorf("неизвестная команда %s", argv0)
}

func TestInstallLXCoreFresh(t *testing.T) {
	f := newFakeLXCore(t, "1.14.2-lx.7")
	res, err := InstallLXCore(context.Background(), f.opts(false))
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != "1.14.2-lx.7" || res.Tag != "v1.14.2-lx.7" || res.Backup != "" {
		t.Fatalf("res: %+v", res)
	}
	if _, err := os.Stat(f.targetBin); err != nil {
		t.Fatalf("бинарь не установлен: %v", err)
	}
	marker, err := os.ReadFile(filepath.Join(f.markerDir, LXMarkerName))
	if err != nil || !strings.Contains(string(marker), "version=1.14.2-lx.7") || !strings.Contains(string(marker), "flavor=plain") || !strings.Contains(string(marker), "source="+Sources[0].String()) {
		t.Fatalf("маркер: %s err=%v", marker, err)
	}
	if !strings.Contains(res.Log, "checksum ok") || !strings.Contains(res.Log, "тест-запуск") {
		t.Fatalf("лог: %s", res.Log)
	}
}

func TestInstallLXCoreForeignRefusalAndReplace(t *testing.T) {
	f := newFakeLXCore(t, "1.14.2-lx.7")
	f.targetVer = "1.13.3"
	if err := os.MkdirAll(filepath.Dir(f.targetBin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.targetBin, []byte("upstream-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := InstallLXCore(context.Background(), f.opts(false))
	if err == nil || !strings.Contains(err.Error(), "чужое ядро") {
		t.Fatalf("ожидался отказ на чужом ядре: %v", err)
	}
	if got, _ := os.ReadFile(f.targetBin); string(got) != "upstream-binary" {
		t.Fatalf("чужое ядро тронуто: %q", got)
	}
	res, err := InstallLXCore(context.Background(), f.opts(true))
	if err != nil {
		t.Fatal(err)
	}
	if res.Backup == "" || res.Previous != "1.13.3" {
		t.Fatalf("res: %+v", res)
	}
	backup, err := os.ReadFile(res.Backup)
	if err != nil || string(backup) != "upstream-binary" {
		t.Fatalf("бэкап чужого ядра: %q err=%v", backup, err)
	}
}

func TestInstallLXCoreMigrateExistingLX(t *testing.T) {
	f := newFakeLXCore(t, "1.14.2-lx.7")
	f.targetVer = "1.14.2-lx.6"
	if err := os.MkdirAll(filepath.Dir(f.targetBin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.targetBin, []byte("old-lx"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := InstallLXCore(context.Background(), f.opts(false))
	if err != nil {
		t.Fatal(err)
	}
	if res.Backup != "" {
		t.Fatalf("lx-ядро не чужое, бэкап не нужен: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(f.markerDir, LXMarkerName)); err != nil {
		t.Fatalf("маркер не создан: %v", err)
	}
}

func TestInstallLXCoreForeignDropBackup(t *testing.T) {
	// замена чужого ядра без сохранения копии - для устройств без места
	f := newFakeLXCore(t, "1.14.2-lx.7")
	f.targetVer = "1.13.3"
	if err := os.MkdirAll(filepath.Dir(f.targetBin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.targetBin, []byte("upstream-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	opts := f.opts(true)
	opts.DropForeignBackup = true
	res, err := InstallLXCore(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Backup != "" {
		t.Fatalf("бэкап не должен создаваться: %+v", res)
	}
	if _, err := os.Stat(f.targetBin + ".pre-lx"); !os.IsNotExist(err) {
		t.Fatalf("файл .pre-lx не должен существовать: %v", err)
	}
	if !strings.Contains(res.Log, "без сохранения копии") {
		t.Fatalf("лог: %s", res.Log)
	}
}

func TestInstallLXCoreBadChecksum(t *testing.T) {
	f := newFakeLXCore(t, "1.14.2-lx.7")
	if err := os.WriteFile(filepath.Join(f.dir, "SHA256SUMS"), []byte("deadbeef  "+f.asset+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := InstallLXCore(context.Background(), f.opts(false))
	if err == nil || !strings.Contains(err.Error(), "контрольная сумма") {
		t.Fatalf("ожидалась ошибка суммы: %v", err)
	}
}

func TestInstallLXCoreNotLXBinary(t *testing.T) {
	f := newFakeLXCore(t, "1.13.3") // версия без -lx. - тест-запуск обязан отвергнуть
	sum := sha256.Sum256(f.tarBytes)
	if err := os.WriteFile(filepath.Join(f.dir, "SHA256SUMS"), []byte(fmt.Sprintf("%x  %s\n", sum, f.asset)), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := InstallLXCore(context.Background(), f.opts(false))
	if err == nil || !strings.Contains(err.Error(), "не lx-ядро") {
		t.Fatalf("ожидался отказ тест-запуска: %v", err)
	}
}

func TestInstallLXCoreMissingArch(t *testing.T) {
	f := newFakeLXCore(t, "1.14.2-lx.7")
	opts := f.opts(false)
	opts.Discover = func(ctx context.Context) []SourceReport {
		return []SourceReport{{Source: Sources[0], Releases: []Release{{Tag: "v1", Matrix: map[string]Flavors{"amd64": {Plain: true}}, Names: map[string]FlavorAssets{"amd64": {Plain: "a.tar.gz"}}}}}}
	}
	_, err := InstallLXCore(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "нет ассета для mipsle-softfloat") {
		t.Fatalf("честный ответ про отсутствие: %v", err)
	}
}

// равная версия нашей сборки (маркер с источником) - не перекачиваем
func TestInstallLXCoreUpToDateOurs(t *testing.T) {
	f := newFakeLXCore(t, "1.14.2-lx.7")
	f.targetVer = "1.14.2-lx.7"
	if err := os.MkdirAll(filepath.Dir(f.targetBin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.targetBin, []byte("our-lx"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(f.markerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := fmt.Sprintf("tag=v1.14.2-lx.7\nversion=1.14.2-lx.7\nsource=%s\nflavor=plain\n", Sources[0])
	if err := os.WriteFile(filepath.Join(f.markerDir, LXMarkerName), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := InstallLXCore(context.Background(), f.opts(false))
	if err != nil {
		t.Fatal(err)
	}
	if !res.UpToDate {
		t.Fatalf("наша сборка той же версии не требует перекачки: %+v", res)
	}
	if got, _ := os.ReadFile(f.targetBin); string(got) != "our-lx" {
		t.Fatalf("бинарь не должен меняться: %q", got)
	}
	if !strings.Contains(res.Log, "наша сборка") {
		t.Fatalf("лог: %s", res.Log)
	}
}

// равная версия, но сборка не наша (маркера нет или старый без source=) -
// заменяем своей сборкой той же версии
func TestInstallLXCoreSameVersionForeignSourceReplaces(t *testing.T) {
	f := newFakeLXCore(t, "1.14.2-lx.7")
	f.targetVer = "1.14.2-lx.7"
	if err := os.MkdirAll(filepath.Dir(f.targetBin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.targetBin, []byte("foreign-lx"), 0o755); err != nil {
		t.Fatal(err)
	}
	// старый маркер без source= - ставили до появления источника в маркере
	if err := os.MkdirAll(f.markerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := "tag=v1.14.2-lx.7\nversion=1.14.2-lx.7\nflavor=plain\n"
	if err := os.WriteFile(filepath.Join(f.markerDir, LXMarkerName), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := InstallLXCore(context.Background(), f.opts(false))
	if err != nil {
		t.Fatal(err)
	}
	if res.UpToDate {
		t.Fatalf("чужая сборка той же версии должна заменяться: %+v", res)
	}
	if got, _ := os.ReadFile(f.targetBin); string(got) != f.binContent {
		t.Fatalf("бинарь не заменён нашей сборкой: %q", got)
	}
	marker, _ := os.ReadFile(filepath.Join(f.markerDir, LXMarkerName))
	if !strings.Contains(string(marker), "source="+Sources[0].String()) {
		t.Fatalf("маркер без источника: %s", marker)
	}
}
