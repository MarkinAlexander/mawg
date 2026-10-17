// Package mtinstall ставит MagiTrickle (mawg-форк) пакетом из релиза
// GitHub: детект таргета, выбор ассета под менеджер пакетов, скачивание,
// установка штатным opkg/apk с зависимостями, запуск службы. Заменяет путь
// bin.magitrickle.dev (апстримный репозиторий без форка rev-фич).
package mtinstall

import (
	"context"
	"encoding/json"
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
	// Repo - репозиторий mawg-форка MagiTrickle с релизными пакетами.
	Repo = "MarkinAlexander/magitrickle"

	latestAPI  = "https://api.github.com/repos/" + Repo + "/releases/latest"
	minPackage = 200_000 // sanity размера скачанного пакета

	cmdTimeout = 180 * time.Second
)

// Asset - файл релиза (magitrickle_<ver>_<PLATFORM>_<TARGET>.ipk/.apk).
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// Release - ответ GitHub API о последнем релизе форка.
type Release struct {
	TagName string  `json:"tag_name"`
	Assets  []Asset `json:"assets"`
}

type Result struct {
	Tag   string `json:"tag"`
	Asset string `json:"asset"`
	Bin   string `json:"bin"`
	Log   string `json:"log"`
}

type Options struct {
	// Platform - имя бекенда (keenetic|openwrt); keenetic = entware-таргеты
	// с суффиксом _kn и путями /opt, openwrt = таргет из opkg/apk и /usr.
	Platform string

	// Detect/Latest/Fetch/Run - подмены для тестов.
	Detect  func() (target, detail string, err error)
	Latest  func(ctx context.Context) (*Release, error)
	Fetch   func(ctx context.Context, url, dest string) (int64, error)
	Run     func(argv0 string, args ...string) ([]byte, error)
	TmpDir  string
	MinSize int64
}

// DetectTarget определяет PLATFORM/TARGET в терминах ассетов релиза форка:
// keenetic -> entware mipsel-3.4_kn (opkg Entware знает порядок байт,
// _kn - суффикс таргетов Кинетика), openwrt -> opkg print-architecture
// (24.10 и старше, печатает ровно целевые имена вида x86_64/i386_pentium-mmx/
// mipsel_24kc) либо apk --print-arch (25+).
func DetectTarget(platform string) (target, detail string, err error) {
	switch platform {
	case "keenetic":
		out, e := exec.Command("opkg", "print-architecture").Output()
		if e != nil {
			return "", "", fmt.Errorf("opkg print-architecture не ответил: %v", e)
		}
		a := FirstOpkgArch(string(out))
		if a == "" {
			return "", "", fmt.Errorf("не удалось разобрать вывод opkg: %q", strings.TrimSpace(string(out)))
		}
		if !strings.Contains(a, "mipsel") && !strings.Contains(a, "mipsle") {
			return "", "", fmt.Errorf("неожиданная архитектура Entware %q: таргеты Кинетика - mipsel", a)
		}
		return a + "_kn", "opkg print-architecture + _kn (Keenetic)", nil
	case "openwrt":
		if out, e := exec.Command("opkg", "print-architecture").Output(); e == nil {
			if a := FirstOpkgArch(string(out)); a != "" {
				return a, "opkg print-architecture", nil
			}
		}
		if out, e := exec.Command("apk", "--print-arch").Output(); e == nil {
			if a := strings.TrimSpace(string(out)); a != "" {
				return a, "apk --print-arch", nil
			}
		}
		return "", "", fmt.Errorf("не нашёл ни opkg, ни apk: целевой таргет определить нечем")
	}
	return "", "", fmt.Errorf("платформа %q не поддерживается", platform)
}

// FirstOpkgArch разбирает вывод `opkg print-architecture`: строки
// «arch <имя> <приоритет>», целевое имя - первая неслужебная (noarch/all).
func FirstOpkgArch(out string) string {
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] != "arch" {
			continue
		}
		switch f[1] {
		case "noarch", "all":
			continue
		}
		return f[1]
	}
	return ""
}

// PackageManager: apk-based OpenWrt 25+ ставит .apk, opkg (24.10-,
// Entware/Keenetic) - .ipk.
func PackageManager(platform string) (mgr, ext string) {
	if platform != "keenetic" {
		if _, err := exec.LookPath("apk"); err == nil {
			return "apk", ".apk"
		}
	}
	return "opkg", ".ipk"
}

// PickAsset выбирает пакет релиза под платформу/таргет/менеджер пакетов;
// чего нет - честный отказ со списком имеющихся пакетов.
func PickAsset(rel *Release, platform, target, ext string) (*Asset, error) {
	suffix := "_" + platform + "_" + target + ext
	var have []string
	for i := range rel.Assets {
		a := &rel.Assets[i]
		name := a.Name
		if !strings.HasSuffix(name, ".ipk") && !strings.HasSuffix(name, ".apk") {
			continue
		}
		have = append(have, name)
		if strings.HasSuffix(name, suffix) {
			return a, nil
		}
	}
	if len(have) == 0 {
		return nil, fmt.Errorf("в релизе %s нет пакетов", rel.TagName)
	}
	return nil, fmt.Errorf("в релизе %s нет пакета для %s/%s: есть %s",
		rel.TagName, platform, target, strings.Join(have, ", "))
}

// assetPlatform переводит имя бекенда в PLATFORM ассетов: Keenetic ставится
// из entware-таргетов (пути /opt), OpenWrt - из openwrt (/usr).
func assetPlatform(backendName string) string {
	if backendName == "keenetic" {
		return "entware"
	}
	return "openwrt"
}

// ServicePaths - init-скрипт, сокет и бинарь MT на платформе.
func ServicePaths(platform string) (initScript, socket, bin string) {
	if platform == "keenetic" {
		return "/opt/etc/init.d/S99magitrickle", "/opt/var/run/magitrickle.sock", "/opt/bin/magitrickled"
	}
	return "/etc/init.d/magitrickle", "/var/run/magitrickle.sock", "/usr/bin/magitrickled"
}

// LatestRelease опрашивает GitHub API о последнем релизе форка.
func LatestRelease(ctx context.Context) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestAPI, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github ответил %d на %s", resp.StatusCode, latestAPI)
	}
	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

// Install ставит MT пакетом из релиза форка. Порядок важен: всё сетевое
// (запрос релиза, скачивание) - ПОКА MT жив: на Keenetic DNS завёрнут в
// MT_DNSOR, остановленный MT роняет резолв; затем стоп, установка, старт.
func Install(ctx context.Context, opts Options) (Result, error) {
	res := Result{}
	detect := opts.Detect
	if detect == nil {
		detect = func() (string, string, error) { return DetectTarget(opts.Platform) }
	}
	latest := opts.Latest
	if latest == nil {
		latest = LatestRelease
	}
	fetch := opts.Fetch
	if fetch == nil {
		fetch = fetchTo
	}
	run := opts.Run
	if run == nil {
		run = func(argv0 string, args ...string) ([]byte, error) {
			cctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
			defer cancel()
			return exec.CommandContext(cctx, argv0, args...).CombinedOutput()
		}
	}
	tmp := opts.TmpDir
	if tmp == "" {
		tmp = "/tmp"
	}
	minSize := opts.MinSize
	if minSize == 0 {
		minSize = minPackage
	}
	var log strings.Builder
	step := func(format string, a ...any) {
		log.WriteString("- " + fmt.Sprintf(format, a...) + "\n")
	}

	target, detail, err := detect()
	if err != nil {
		res.Log = log.String()
		return res, err
	}
	platform := assetPlatform(opts.Platform)
	mgr, ext := PackageManager(opts.Platform)
	step("таргет %s/%s (%s), менеджер %s", platform, target, detail, mgr)

	rel, err := latest(ctx)
	if err != nil {
		res.Log = log.String()
		return res, fmt.Errorf("узнать последний релиз %s не удалось: %v", Repo, err)
	}
	asset, err := PickAsset(rel, platform, target, ext)
	if err != nil {
		res.Log = log.String()
		return res, err
	}
	res.Tag = rel.TagName
	res.Asset = asset.Name
	step("релиз %s: %s (%s)", rel.TagName, asset.Name, mb(asset.Size))

	dest := filepath.Join(tmp, asset.Name)
	size, err := fetch(ctx, asset.URL, dest)
	if err != nil {
		res.Log = log.String()
		return res, fmt.Errorf("скачать %s не удалось: %v", asset.Name, err)
	}
	if size < minSize {
		os.Remove(dest)
		res.Log = log.String()
		return res, fmt.Errorf("скачанный файл подозрительно мал (%d байт)", size)
	}
	step("скачано %d байт", size)

	initScript, socket, bin := ServicePaths(opts.Platform)
	res.Bin = bin

	// стоп до установки: замена файла работающего бинаря падает ETXTBSY;
	// на свежей установке службы ещё нет - отказ молча терпим
	if out, e := run(initScript, "stop"); e != nil {
		step("стоп %s пропущен (службы нет?): %s", initScript, firstLine(string(out)))
	} else {
		step("остановлен %s", initScript)
	}
	// стейл-сокет после убитого форграунда молча валит старт init-скриптом
	_ = os.Remove(socket)

	out, err := installPackage(run, mgr, dest)
	if err != nil {
		// зависимости могли не резолвнуться по пустым спискам - обновляем и
		// повторяем раз; после неудачи MT остаётся остановленным - лучше
		// вернуть роутер в рабочее состояние, чем оставить без MT
		if !depRetry(mgr, string(out)) {
			_, _ = run(initScript, "start")
			res.Log = log.String()
			return res, fmt.Errorf("%s install: %v: %s", mgr, err, firstLine(string(out)))
		}
		step("первая попытка упала по зависимостям, обновляю списки и повторяю")
		if uerr := updateLists(run, mgr); uerr != nil {
			step("opkg update не удался: %v", uerr)
		}
		out2, err2 := installPackage(run, mgr, dest)
		if err2 != nil {
			_, _ = run(initScript, "start")
			res.Log = log.String()
			return res, fmt.Errorf("%s install (повтор): %v: %s", mgr, err2, firstLine(string(out2)))
		}
		out = out2
	}
	step("пакет установлен: %s", firstLine(string(out)))
	_ = os.Remove(dest)

	if out, e := run(initScript, "enable"); e != nil {
		step("enable %s: %s", initScript, firstLine(string(out)))
	}
	if out, e := run(initScript, "start"); e != nil {
		res.Log = log.String()
		return res, fmt.Errorf("служба MT не стартовала (%s start): %v: %s", initScript, e, firstLine(string(out)))
	}
	step("служба запущена: %s start", initScript)
	res.Log = log.String()
	return res, nil
}

func installPackage(run func(string, ...string) ([]byte, error), mgr, dest string) ([]byte, error) {
	if mgr == "apk" {
		return run("apk", "add", "--allow-untrusted", dest)
	}
	return run("opkg", "install", dest)
}

func updateLists(run func(string, ...string) ([]byte, error), mgr string) error {
	if mgr == "apk" {
		_, err := run("apk", "update")
		return err
	}
	_, err := run("opkg", "update")
	return err
}

// depRetry - стоит ли повторять установку после обновления списков: отказ
// по зависимостям, а не поломанный пакет.
func depRetry(mgr, out string) bool {
	s := strings.ToLower(out)
	switch {
	case strings.Contains(s, "cannot find package"),
		strings.Contains(s, "unknown package"),
		strings.Contains(s, "no satisfiable"),
		strings.Contains(s, "depend"):
		return true
	}
	return false
}

// fetchTo - скачивание в файл (http/https, редиректы GitHub обрабатывает
// сам http-клиент).
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

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i > 0 {
		return s[:i]
	}
	return s
}

func mb(n int64) string {
	return fmt.Sprintf("%.1f МБ", float64(n)/float64(1<<20))
}
