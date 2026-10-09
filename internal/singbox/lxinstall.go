package singbox

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	LXFlavorPlain = "plain" // по умолчанию
	LXFlavorUPX   = "upx"   // opt-in: меньше на диске, дороже по RAM и старту

	lxMinTarball = 10 << 20 // sanity размера скачанного архива

	LXMarkerName = ".installed-by-mawg"
)

// LXTarget - пути установки ядра на платформе.
type LXTarget struct {
	Bin       string
	MarkerDir string
	DiskPath  string // что показывать в df при подтверждении
}

// LXTargetForPlatform - Keenetic (Entware) /opt/bin, OpenWrt /usr/bin.
func LXTargetForPlatform(platform string) LXTarget {
	if platform == "keenetic" {
		return LXTarget{Bin: "/opt/bin/sing-box", MarkerDir: "/opt/etc/sing-box-lx", DiskPath: "/opt"}
	}
	return LXTarget{Bin: "/usr/bin/sing-box", MarkerDir: "/etc/sing-box-lx", DiskPath: "/"}
}

// DetectLxArch определяет архитектуру в терминах ассетов lx-релиза:
// сначала opkg (Entware честно знает порядок байт), затем ELF-заголовок
// работающего бинаря, uname - последним (Keenetic печатает "mips" для
// обоих порядков, на деле там mipsle-softfloat).
func DetectLxArch() (arch string, detail string, err error) {
	if out, e := exec.Command("opkg", "print-architecture").Output(); e == nil {
		if a := archFromOpkg(string(out)); a != "" {
			return a, "opkg print-architecture", nil
		}
	}
	if a := archFromELF("/proc/self/exe"); a != "" {
		return a, "ELF-заголовок работающего бинаря", nil
	}
	out, e := exec.Command("uname", "-m").Output()
	if e != nil {
		return "", "", fmt.Errorf("архитектуру определить не удалось: %v", e)
	}
	a, note := archFromUname(strings.TrimSpace(string(out)))
	if a == "" {
		return "", "", fmt.Errorf("архитектура %q не поддерживается релизами lx", strings.TrimSpace(string(out)))
	}
	detail = "uname"
	if note != "" {
		detail += " (" + note + ")"
	}
	return a, detail, nil
}

// archFromOpkg разбирает вывод `opkg print-architecture`.
func archFromOpkg(out string) string {
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] != "arch" {
			continue
		}
		switch a := f[1]; {
		case strings.Contains(a, "mipsel"), strings.Contains(a, "mipsle"):
			return "mipsle-softfloat"
		case a == "mips" || strings.HasPrefix(a, "mips_") || strings.HasPrefix(a, "mips-"):
			return "mips-softfloat"
		case strings.HasPrefix(a, "aarch64"), a == "arm64":
			return "arm64"
		case strings.HasPrefix(a, "armv7"), strings.HasPrefix(a, "arm_"), strings.HasPrefix(a, "arm-"):
			return "armv7"
		case strings.HasPrefix(a, "i386"), strings.HasPrefix(a, "i486"),
			strings.HasPrefix(a, "i586"), strings.HasPrefix(a, "i686"), a == "x86":
			return "386"
		case a == "x86_64" || a == "amd64":
			return "amd64"
		case a == "riscv64":
			return "riscv64"
		}
	}
	return ""
}

// archFromELF читает EI_DATA и e_machine работающего бинаря: байт 5 - порядок
// байт (1 = little-endian), e_machine (байты 18-19) уточняет семейство.
func archFromELF(path string) string {
	b := make([]byte, 20)
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	if _, err := io.ReadFull(f, b); err != nil {
		return ""
	}
	if string(b[0:4]) != "\x7fELF" {
		return ""
	}
	le := b[5] == 1
	var machine int
	if le {
		machine = int(b[18]) | int(b[19])<<8
	} else {
		machine = int(b[18])<<8 | int(b[19])
	}
	switch {
	case machine == 8 && le: // EM_MIPS
		return "mipsle-softfloat"
	case machine == 8:
		return "mips-softfloat"
	case machine == 40: // EM_ARM
		return "armv7"
	case machine == 183: // EM_AARCH64
		return "arm64"
	case machine == 3: // EM_386
		return "386"
	case machine == 62: // EM_X86_64
		return "amd64"
	case machine == 243: // EM_RISCV
		return "riscv64"
	}
	return ""
}

// archFromUname мапит uname -m; mips без уточнения - это либо Keenetic
// (врёт, там little-endian), либо честный big-endian OpenWrt: как install.sh
// mawg, предполагаем little-endian и пишем об этом.
func archFromUname(m string) (arch, note string) {
	switch {
	case m == "aarch64" || m == "arm64":
		return "arm64", ""
	case strings.HasPrefix(m, "armv"):
		return "armv7", ""
	case m == "i386" || m == "i486" || m == "i586" || m == "i686" || m == "x86":
		return "386", ""
	case m == "x86_64" || m == "amd64":
		return "amd64", ""
	case m == "riscv64":
		return "riscv64", ""
	case m == "mipsle" || m == "mipsel":
		return "mipsle-softfloat", ""
	case m == "mips":
		return "mipsle-softfloat", "uname говорит mips, предполагаю little-endian"
	}
	return "", ""
}

type PickedAsset struct {
	Source Source
	Tag    string
	Asset  string
	Size   int64
}

func (p PickedAsset) URL() string {
	return fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s", p.Source.Owner, p.Source.Repo, p.Tag, p.Asset)
}

func (p PickedAsset) SumsURL() string {
	return fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/SHA256SUMS", p.Source.Owner, p.Source.Repo, p.Tag)
}

// PickAsset ищет по отчётам дискавери (релизы от новых к старым, источники
// по порядку) ассет arch+flavor. Никаких тихих подмен flavor: чего нет -
// честный ответ со списком того, что есть.
func PickAsset(reports []SourceReport, arch, flavor string) (*PickedAsset, error) {
	if flavor != LXFlavorPlain && flavor != LXFlavorUPX {
		return nil, fmt.Errorf("неизвестный профиль ядра %q (plain или upx)", flavor)
	}
	var have []string
	for _, rep := range reports {
		if rep.Error != "" {
			continue
		}
		archs := map[string]bool{}
		for _, rel := range rep.Releases {
			for a := range rel.Matrix {
				archs[a] = true
			}
			n, ok := rel.Names[arch]
			if !ok {
				continue
			}
			name, size := n.Plain, n.PlainSz
			if flavor == LXFlavorUPX {
				name, size = n.UPX, n.UPXSz
			}
			if name == "" {
				continue
			}
			return &PickedAsset{Source: rep.Source, Tag: rel.Tag, Asset: name, Size: size}, nil
		}
		for a := range archs {
			have = append(have, fmt.Sprintf("%s: %s", rep.Source, a))
		}
	}
	if len(have) == 0 {
		return nil, fmt.Errorf("в источниках lx нет доступных релизов")
	}
	return nil, fmt.Errorf("в этом источнике нет ассета для %s (%s): есть %s",
		arch, flavor, strings.Join(have, "; "))
}

// HasLXSuffix отличает lx-ядро от upstream: только у lx в версии есть "-lx.".
func HasLXSuffix(version string) bool { return strings.Contains(version, "-lx.") }

// parseLxVer разбирает "1.14.2-lx.12" на базу и номер сборки lx.
func parseLxVer(v string) (base string, n int, ok bool) {
	v = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "v"))
	i := strings.Index(v, "-lx.")
	if i < 0 {
		return "", 0, false
	}
	base, tail := v[:i], v[i+4:]
	fmt.Sscanf(tail, "%d", &n)
	return base, n, true
}

// LXNewer говорит, стоит ли ставить candidate вместо installed: сравнение
// суффикса -lx.N (не selfupdate-сравнение, тот lx-версии не понимает).
func LXNewer(candidate, installed string) bool {
	cb, cn, cok := parseLxVer(candidate)
	ib, in, iok := parseLxVer(installed)
	if !cok {
		return false
	}
	if !iok {
		return true
	}
	if cb != ib {
		return true
	}
	return cn > in
}

// SingBoxVersion запускает `bin version` и достаёт версию из первой строки.
func SingBoxVersion(bin string, run func(argv0 string, args ...string) ([]byte, error)) (string, error) {
	if run == nil {
		run = func(argv0 string, args ...string) ([]byte, error) {
			return exec.Command(argv0, args...).Output()
		}
	}
	out, err := run(bin, "version")
	if err != nil {
		return "", err
	}
	line := strings.SplitN(string(out), "\n", 2)[0]
	ver := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "sing-box version"))
	if ver == "" {
		return "", fmt.Errorf("не удалось разобрать вывод version")
	}
	return ver, nil
}

type LXInstallOptions struct {
	Flavor string // plain|upx, пусто = plain
	Target LXTarget

	// ReplaceForeign - явное согласие заменить чужое (upstream) ядро,
	// DropForeignBackup - без сохранения копии <bin>.pre-lx (диск дороже).
	ReplaceForeign    bool
	DropForeignBackup bool

	// Arch, Discover, Fetch, Run, TmpDir, MinTarball - подмены для тестов.
	Arch       string
	Discover   func(ctx context.Context) []SourceReport
	Fetch      func(ctx context.Context, url, dest string) (int64, error)
	Run        func(argv0 string, args ...string) ([]byte, error)
	TmpDir     string
	MinTarball int64 // 0 = lxMinTarball
}

type LXInstallResult struct {
	Tag      string `json:"tag"`
	Version  string `json:"version"`
	Bin      string `json:"bin"`
	Previous string `json:"previous,omitempty"`
	Backup   string `json:"backup,omitempty"`
	UpToDate bool   `json:"upToDate,omitempty"`
	Log      string `json:"log"`
}

// InstallLXCore ставит lx-ядро из релиза: скачать ассет+SHA256SUMS,
// sanity размера, распаковать, ТЕСТ-ЗАПУСК в /tmp, только потом подмена
// рабочего бинаря. Чужое ядро (без -lx. и без маркера) не трогается без
// явного ReplaceForeign - тогда старое сохраняется рядом как <bin>.pre-lx.
func InstallLXCore(ctx context.Context, opts LXInstallOptions) (LXInstallResult, error) {
	res := LXInstallResult{}
	if opts.Flavor == "" {
		opts.Flavor = LXFlavorPlain
	}
	if opts.Target.Bin == "" {
		return res, fmt.Errorf("не задан путь установки ядра")
	}
	fetch := opts.Fetch
	if fetch == nil {
		fetch = fetchTo
	}
	run := opts.Run
	if run == nil {
		run = func(argv0 string, args ...string) ([]byte, error) {
			return exec.Command(argv0, args...).Output()
		}
	}
	tmp := opts.TmpDir
	if tmp == "" {
		tmp = "/tmp"
	}
	var log strings.Builder
	step := func(format string, a ...any) {
		log.WriteString("- " + fmt.Sprintf(format, a...) + "\n")
	}

	arch := opts.Arch
	if arch == "" {
		a, detail, err := DetectLxArch()
		if err != nil {
			return res, err
		}
		arch = a
		step("архитектура %s (%s)", arch, detail)
	} else {
		step("архитектура %s", arch)
	}

	discover := opts.Discover
	if discover == nil {
		discover = Discovery
	}
	pick, err := PickAsset(discover(ctx), arch, opts.Flavor)
	if err != nil {
		return res, err
	}
	res.Tag = pick.Tag
	step("релиз %s из %s: %s", pick.Tag, pick.Source, pick.Asset)

	// чужое ядро не трогаем без согласия, отказ - до скачивания
	prevVer := ""
	if out, verr := SingBoxVersion(opts.Target.Bin, run); verr == nil {
		prevVer = out
		res.Previous = prevVer
		if !HasLXSuffix(prevVer) {
			marked := markerExists(opts.Target.MarkerDir)
			if !marked && !opts.ReplaceForeign {
				return res, fmt.Errorf("в %s стоит чужое ядро %s (без -lx. и маркера mawg): без явного согласия не трогаю", opts.Target.Bin, prevVer)
			}
		}
	}

	// ядро не старее релиза и стоит НАША сборка - не перекачиваем;
	// чужую/ручную сборку той же версии (маркера с нашим источником нет)
	// заменяем своей: храним только то, что сами собираем
	releaseVer := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(pick.Tag), "v"))
	if HasLXSuffix(prevVer) && !LXNewer(releaseVer, prevVer) && markerSource(opts.Target.MarkerDir) == pick.Source.String() {
		res.Version = prevVer
		res.UpToDate = true
		step("lx-ядро %s уже актуально (наша сборка из %s)", prevVer, pick.Source)
		res.Log = log.String()
		return res, nil
	}

	tarball := filepath.Join(tmp, pick.Asset)
	size, err := fetch(ctx, pick.URL(), tarball)
	if err != nil {
		return res, fmt.Errorf("скачать %s не удалось: %v", pick.Asset, err)
	}
	minSize := opts.MinTarball
	if minSize == 0 {
		minSize = lxMinTarball
	}
	if size < minSize {
		os.Remove(tarball)
		return res, fmt.Errorf("скачанный файл подозрительно мал (%d байт)", size)
	}
	step("скачано %d байт", size)

	sumsPath := filepath.Join(tmp, "SHA256SUMS-"+pick.Tag)
	if _, err := fetch(ctx, pick.SumsURL(), sumsPath); err != nil {
		step("SHA256SUMS недоступен, проверка суммы пропущена")
	} else if want, ok := sumsLineFor(sumsPath, pick.Asset); !ok {
		step("в SHA256SUMS нет строки для %s, проверка суммы пропущена", pick.Asset)
	} else if got, gerr := fileSHA256(tarball); gerr != nil {
		return res, gerr
	} else if got != want {
		return res, fmt.Errorf("контрольная сумма не совпала: %s, ожидалось %s", got, want)
	} else {
		step("checksum ok")
	}

	dir := filepath.Join(tmp, "sing-box-lx-"+pick.Tag)
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return res, err
	}
	if _, err := run("tar", "-xzf", tarball, "-C", dir); err != nil {
		return res, fmt.Errorf("распаковка не удалась: %v", err)
	}
	bin, err := findSingBox(dir)
	if err != nil {
		return res, err
	}
	_ = os.Remove(tarball)
	_ = os.Remove(sumsPath)

	ver, err := SingBoxVersion(bin, run)
	if err != nil {
		_ = os.RemoveAll(dir)
		return res, fmt.Errorf("тест-запуск скачанного ядра не удался: %v", err)
	}
	if !HasLXSuffix(ver) {
		_ = os.RemoveAll(dir)
		return res, fmt.Errorf("скачанный бинарь не lx-ядро (version: %s)", ver)
	}
	res.Version = ver
	step("тест-запуск в /tmp ok: %s", ver)

	if err := os.MkdirAll(filepath.Dir(opts.Target.Bin), 0o755); err != nil {
		_ = os.RemoveAll(dir)
		return res, err
	}
	staged := opts.Target.Bin + ".lx-new"
	if err := copyFile(bin, staged, 0o755); err != nil {
		_ = os.RemoveAll(dir)
		return res, err
	}
	_ = os.RemoveAll(dir)
	foreign := prevVer != "" && !HasLXSuffix(prevVer)
	if foreign {
		backup := opts.Target.Bin + ".pre-lx"
		if _, exists := os.Stat(backup); exists == nil {
			step("бэкап чужого ядра уже есть: %s", backup)
		} else if opts.DropForeignBackup {
			step("чужое ядро %s удаляется без сохранения копии (выбрано в диалоге)", prevVer)
		} else if err := copyFile(opts.Target.Bin, backup, 0o755); err != nil {
			os.Remove(staged)
			return res, fmt.Errorf("сохранить чужое ядро перед заменой не удалось (место кончилось?): %v; можно повторить с отключённым сохранением старого", err)
		} else {
			res.Backup = backup
			step("чужое ядро %s сохранено как %s", prevVer, backup)
		}
	}
	if err := os.Rename(staged, opts.Target.Bin); err != nil {
		os.Remove(staged)
		return res, err
	}
	res.Bin = opts.Target.Bin

	if err := os.MkdirAll(opts.Target.MarkerDir, 0o755); err != nil {
		return res, err
	}
	marker := fmt.Sprintf("tag=%s\nversion=%s\nsource=%s\ninstalled=%s\nflavor=%s\n", pick.Tag, ver, pick.Source.String(), time.Now().Format(time.RFC3339), opts.Flavor)
	if err := os.WriteFile(filepath.Join(opts.Target.MarkerDir, LXMarkerName), []byte(marker), 0o644); err != nil {
		return res, err
	}
	final, err := SingBoxVersion(opts.Target.Bin, run)
	if err != nil || !HasLXSuffix(final) {
		return res, fmt.Errorf("после установки %s не отвечает как lx-ядро", opts.Target.Bin)
	}
	step("установлено: %s (%s)", final, opts.Target.Bin)
	res.Log = log.String()
	return res, nil
}

func markerExists(dir string) bool {
	if dir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, LXMarkerName))
	return err == nil
}

// markerSource - источник последней установки (source= в маркере). Пусто
// для старых маркеров и ручных установок: такое ядро ставили не из нашего
// форка, равноверсионная замена своей сборкой разрешена.
func markerSource(dir string) string {
	if dir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(dir, LXMarkerName))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "source="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// findSingBox ищет бинарь sing-box в распакованном дереве.
func findSingBox(dir string) (string, error) {
	var found string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || found != "" {
			return err
		}
		if d.IsDir() || d.Name() != "sing-box" {
			return nil
		}
		if info, e := d.Info(); e == nil && info.Mode().IsRegular() {
			found = path
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", fmt.Errorf("в архиве нет бинаря sing-box")
	}
	return found, nil
}

// sumsLineFor достаёт сумму строки с ассетом из SHA256SUMS.
func sumsLineFor(path, asset string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) == 2 && f[1] == asset {
			return f[0], true
		}
	}
	return "", false
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// fetchTo - дефолтное скачивание в файл (http/https).
func fetchTo(ctx context.Context, url, dest string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("github ответил %d на %s", resp.StatusCode, url)
	}
	f, err := os.Create(dest)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(dest)
		return 0, err
	}
	return n, nil
}

// extractTar - распаковка tar.gz как `tar -xzf`; в тестах зовётся из подмены Run.
func extractTar(tarball, dir string) error {
	f, err := os.Open(tarball)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := filepath.Clean(hdr.Name)
		if strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			continue
		}
		dst := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return err
		}
		out.Close()
	}
}
