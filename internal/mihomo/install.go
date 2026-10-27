package mihomo

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"mawg/internal/platform"
	"mawg/internal/singbox"
)

const (
	MarkerName = ".installed-by-mawg"
	minBinSize = 4 << 20  // sanity размера gz-ассета
	foreignExt = ".pre-mawg" // бэкап чужого бинаря при замене
)

// Target - пути установки mihomo на платформе (как LXTarget у sing-box).
type Target struct {
	Bin       string
	MarkerDir string
	DiskPath  string
}

func TargetForPlatform(platform string) Target {
	if platform == "keenetic" {
		return Target{Bin: "/opt/bin/mihomo", MarkerDir: "/opt/etc/mihomo-mawg", DiskPath: "/opt"}
	}
	return Target{Bin: "/usr/bin/mihomo", MarkerDir: "/etc/mihomo-mawg", DiskPath: "/"}
}

// ArchDetect обёрнут для инъекции в тестах (словарь архов совпадает с
// суффиксами ассетов mihomo: mipsle-softfloat, 386, amd64, arm64, armv7…).
var archDetect = singbox.DetectLxArch

type InstallOptions struct {
	Target Target

	// ReplaceForeign - явное согласие заменить чужой бинарь mihomo.
	ReplaceForeign    bool
	DropForeignBackup bool

	// подмены для тестов
	Arch     string
	Discover func(ctx context.Context) ([]Release, error)
	Fetch    func(ctx context.Context, url, dest string) (int64, error)
	Run      func(argv0 string, args ...string) ([]byte, error)
	TmpDir   string
	MinSize  int64
	// DiskFree подменяется пакетной переменной diskFree (как в lxinstall).
}

type InstallResult struct {
	Tag      string `json:"tag"`
	Version  string `json:"version"`
	Bin      string `json:"bin"`
	Previous string `json:"previous,omitempty"`
	Backup   string `json:"backup,omitempty"`
	UpToDate bool   `json:"upToDate,omitempty"`
	Log      string `json:"log"`
}

// diskFree - свободное место (байты); подменяется в тестах.
var diskFree = platform.DiskFreeBytes

// InstallCore ставит mihomo из релиза MetaCubeX: скачать gz, сверить
// digest sha256, распаковать, ТЕСТ-ЗАПУСК в /tmp, только потом подмена
// рабочего бинаря. Чужой бинарь (без маркера) не трогается без
// ReplaceForeign - тогда старое сохраняется рядом как <bin>.pre-mawg.
func InstallCore(ctx context.Context, opts InstallOptions) (InstallResult, error) {
	res := InstallResult{}
	if opts.Target.Bin == "" {
		return res, fmt.Errorf("не задан путь установки mihomo")
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
		a, detail, err := archDetect()
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
		discover = DiscoverReleases
	}
	releases, err := discover(ctx)
	if err != nil {
		return res, err
	}
	pick, err := PickAsset(releases, arch)
	if err != nil {
		return res, err
	}
	res.Tag = pick.Tag
	step("релиз %s: %s", pick.Tag, pick.Asset)

	// чужой бинарь не трогаем без согласия, отказ - до скачивания
	prevVer := ""
	if out, verr := MihomoVersion(opts.Target.Bin, run); verr == nil {
		prevVer = out
		res.Previous = prevVer
		if !markerExists(opts.Target.MarkerDir) && !opts.ReplaceForeign {
			return res, fmt.Errorf("в %s стоит чужой mihomo %s (без маркера mawg): без явного согласия не трогаю", opts.Target.Bin, prevVer)
		}
	}

	// наша установка той же версии - не перекачиваем
	if markerExists(opts.Target.MarkerDir) && prevVer == pick.Tag {
		res.Version = prevVer
		res.UpToDate = true
		step("mihomo %s уже актуален (наша установка)", prevVer)
		res.Log = log.String()
		return res, nil
	}

	gzPath := filepath.Join(tmp, pick.Asset)
	size, err := fetch(ctx, pick.URL(), gzPath)
	if err != nil {
		if isNoSpace(err) {
			res.Log = log.String()
			return res, fmt.Errorf("не хватает места в %s для скачивания (~%s): это оперативная память, она освободится после перезагрузки роутера: %v", tmp, mb(pick.Size), err)
		}
		return res, fmt.Errorf("скачать %s не удалось: %v", pick.Asset, err)
	}
	minSize := opts.MinSize
	if minSize == 0 {
		minSize = minBinSize
	}
	if size < minSize {
		os.Remove(gzPath)
		return res, fmt.Errorf("скачанный файл подозрительно мал (%d байт)", size)
	}
	step("скачано %d байт", size)

	if pick.SHA256 != "" {
		got, gerr := fileSHA256(gzPath)
		if gerr != nil {
			return res, gerr
		}
		if got != pick.SHA256 {
			return res, fmt.Errorf("контрольная сумма не совпала: %s, ожидалось %s", got, pick.SHA256)
		}
		step("digest sha256 ok")
	} else {
		step("digest sha256 у ассета недоступен, проверка суммы пропущена")
	}

	bin := gzPath + ".bin"
	if err := gunzipTo(gzPath, bin); err != nil {
		os.Remove(gzPath)
		if isNoSpace(err) {
			res.Log = log.String()
			return res, fmt.Errorf("не хватает места в %s для распаковки бинаря: %v", tmp, err)
		}
		return res, fmt.Errorf("распаковка не удалась: %v", err)
	}
	_ = os.Remove(gzPath)
	os.Chmod(bin, 0o755)

	ver, err := MihomoVersion(bin, run)
	if err != nil {
		_ = os.Remove(bin)
		return res, fmt.Errorf("тест-запуск скачанного mihomo не удался: %v", err)
	}
	res.Version = ver
	step("тест-запуск в %s ok: %s", tmp, ver)

	if err := os.MkdirAll(filepath.Dir(opts.Target.Bin), 0o755); err != nil {
		_ = os.Remove(bin)
		return res, err
	}

	// место в ПЗУ: до атомарного rename новый бинарь лежит рядом со старым
	// (а при чужом с сохранением - ещё и копия старого)
	binSize := int64(0)
	if st, err := os.Stat(bin); err == nil {
		binSize = st.Size()
	}
	keepOld := prevVer != "" && !opts.DropForeignBackup
	need := binSize
	if keepOld {
		if st, err := os.Stat(opts.Target.Bin); err == nil {
			need += st.Size()
		}
	}
	binDir := filepath.Dir(opts.Target.Bin)
	if free := diskFree(binDir); free > 0 && free < need {
		// наша прежняя установка восстановима повторным скачиванием -
		// снимаем её до копирования, это освобождает место старого
		if markerExists(opts.Target.MarkerDir) {
			if rmErr := os.Remove(opts.Target.Bin); rmErr == nil {
				step("места в ПЗУ мало (свободно %s): прежняя наша установка снята до копирования, при сбое просто повторите установку", mb(free))
			}
		}
		if free = diskFree(binDir); free < need {
			res.Log = log.String()
			return res, fmt.Errorf("не хватает места в ПЗУ: нужно %s, свободно %s (от копии старого бинаря можно отказаться или освободить место)", mb(need), mb(free))
		}
	}

	staged := opts.Target.Bin + ".mawg-new"
	if err := copyFile(bin, staged, 0o755); err != nil {
		os.Remove(bin)
		os.Remove(staged)
		if isNoSpace(err) {
			res.Log = log.String()
			return res, fmt.Errorf("не хватает места в ПЗУ при копировании бинаря (%s): %v", mb(binSize), err)
		}
		return res, err
	}
	_ = os.Remove(bin)
	foreign := prevVer != "" && !markerExists(opts.Target.MarkerDir)
	if foreign {
		backup := opts.Target.Bin + foreignExt
		if _, exists := os.Stat(backup); exists == nil {
			step("бэкап чужого бинаря уже есть: %s", backup)
		} else if opts.DropForeignBackup {
			step("чужой mihomo %s удаляется без сохранения копии (выбрано в диалоге)", prevVer)
		} else if err := copyFile(opts.Target.Bin, backup, 0o755); err != nil {
			os.Remove(staged)
			return res, fmt.Errorf("сохранить чужой mihomo перед заменой не удалось (место кончилось?): %v; можно повторить с отключённым сохранением старого", err)
		} else {
			res.Backup = backup
			step("чужой mihomo %s сохранён как %s", prevVer, backup)
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
	marker := fmt.Sprintf("tag=%s\nversion=%s\nsource=MetaCubeX/mihomo\ninstalled=%s\n", pick.Tag, ver, time.Now().Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(opts.Target.MarkerDir, MarkerName), []byte(marker), 0o644); err != nil {
		return res, err
	}
	final, err := MihomoVersion(opts.Target.Bin, run)
	if err != nil {
		return res, fmt.Errorf("после установки %s не отвечает как mihomo", opts.Target.Bin)
	}
	step("установлено: %s (%s)", final, opts.Target.Bin)
	res.Log = log.String()
	return res, nil
}

// UninstallCore удаляет mihomo, установленный mawg (маркер обязателен):
// останавливать процесс - забота вызывающего. Сохранённый чужой бинарь
// возвращается на место, иначе наш удаляется.
func UninstallCore(target Target) (restored bool, err error) {
	if !markerExists(target.MarkerDir) {
		return false, nil
	}
	backup := target.Bin + foreignExt
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

// ManagedByMawg - mihomo установлен mawg (маркер на месте).
func ManagedByMawg(target Target) bool {
	return markerExists(target.MarkerDir)
}

func markerExists(dir string) bool {
	if dir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, MarkerName))
	return err == nil
}

func isNoSpace(err error) bool {
	return err != nil && (errors.Is(err, syscall.ENOSPC) || strings.Contains(err.Error(), "no space left"))
}

func mb(n int64) string {
	return fmt.Sprintf("%.0f МБ", float64(n)/float64(1<<20))
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

func gunzipTo(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	gz, err := gzip.NewReader(in)
	if err != nil {
		return err
	}
	defer gz.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, gz); err != nil {
		out.Close()
		return err
	}
	return out.Close()
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
