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
	"strconv"
	"strings"
	"time"
)

const (
	// Repo - репозиторий mawg-форка MagiTrickle с релизными пакетами.
	Repo = "MarkinAlexander/magitrickle"

	latestAPI  = "https://api.github.com/repos/" + Repo + "/releases/latest"
	minPackage = 200_000 // sanity размера скачанного пакета

	cmdTimeout = 180 * time.Second

	// opkgPackage - имя пакета в базе opkg (для снятия битой записи).
	opkgPackage = "magitrickle"
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

	// Detect/Latest/Fetch/Run/PackageManagerFn/TmpDir/MinSize - подмены для тестов.
	Detect  func() (target, detail string, err error)
	Latest  func(ctx context.Context) (*Release, error)
	Fetch   func(ctx context.Context, url, dest string) (int64, error)
	Run     func(argv0 string, args ...string) ([]byte, error)
	TmpDir  string
	MinSize int64

	// PackageManagerFn - подмена менеджера пакетов (тесты: apk на раннере без apk).
	PackageManagerFn func() (mgr, ext string)

	// OpkgConf - подмена пути opkg.conf (тесты); пусто - стандартный путь
	// платформы (/opt/etc/opkg.conf на Кинетике, /etc/opkg.conf на OpenWrt).
	OpkgConf string
}

// DetectTarget определяет PLATFORM/TARGET в терминах ассетов релиза форка:
// keenetic -> сырая архитектура Entware из opkg print-architecture (Кинетики
// бывают не только mipsel: живой кейс - новые модели с Entware aarch64-3.10),
// openwrt -> opkg print-architecture (24.10 и старше, печатает ровно целевые
// имена вида x86_64/i386_pentium-mmx/mipsel_24kc) либо apk --print-arch (25+).
// Выбор конкретного пакета (приоритет _kn, фолбэк _generic) - в PickAsset.
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
		return a, "opkg print-architecture (Entware)", nil
	case "openwrt":
		if out, e := exec.Command("opkg", "print-architecture").Output(); e == nil {
			if a := FirstOpkgArch(string(out)); a != "" {
				return a, "opkg print-architecture", nil
			}
		}
		// OpenWrt 25 (apk): --print-arch печатает МАШИННЫЙ arch (aarch64),
		// а пакеты собраны под таргет из /etc/apk/arch (aarch64_cortex-a53
		// и т.п.) - солвер отказывает на несовпадении (живой кейс
		// WBR3000UAX, 25.12.5). Сначала файл, фолбэк --print-arch.
		if out, e := os.ReadFile("/etc/apk/arch"); e == nil {
			if a := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0]); a != "" {
				return a, "/etc/apk/arch", nil
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

// PickAsset выбирает пакет релиза под платформу/таргет/менеджер пакетов.
// Приоритеты (assetCandidates): Entware (Кинетик) - сборка _kn, фолбэк plain
// (есть не для всех таргетов: у armv7-3.2 _kn-сборки нет); OpenWrt - точный
// таргет, а для голой архитектуры без сабтаргета (живой кейс: opkg печатает
// ровно aarch64) - фолбэк <arch>_generic. Чего нет - честный отказ со
// списком имеющихся пакетов.
func PickAsset(rel *Release, platform, target, ext string) (*Asset, error) {
	candidates := assetCandidates(platform, target, ext)
	var pkgs []*Asset
	for i := range rel.Assets {
		n := rel.Assets[i].Name
		if strings.HasSuffix(n, ".ipk") || strings.HasSuffix(n, ".apk") {
			pkgs = append(pkgs, &rel.Assets[i])
		}
	}
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("в релизе %s нет пакетов", rel.TagName)
	}
	for _, suffix := range candidates {
		for _, a := range pkgs {
			if strings.HasSuffix(a.Name, suffix) {
				return a, nil
			}
		}
	}
	return nil, fmt.Errorf("в релизе %s нет пакета для %s/%s: есть %s",
		rel.TagName, platform, target, assetNames(pkgs))
}

// assetCandidates - суффиксы имён ассетов в порядке приоритета.
func assetCandidates(platform, target, ext string) []string {
	p := "_" + platform + "_" + target + ext
	if platform == "entware" {
		// _kn (сборка с тегами entware,entware_kn) предпочтительна, plain -
		// фолбэк; если target уже с _kn, первый кандидат просто не сматчится
		return []string{strings.TrimSuffix(p, ext) + "_kn" + ext, p}
	}
	if !strings.Contains(target, "_") {
		return []string{p, "_" + platform + "_" + target + "_generic" + ext}
	}
	return []string{p}
}

func assetNames(pkgs []*Asset) string {
	names := make([]string, 0, len(pkgs))
	for _, a := range pkgs {
		names = append(names, a.Name)
	}
	return strings.Join(names, ", ")
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
	if opts.PackageManagerFn != nil {
		mgr, ext = opts.PackageManagerFn()
	}
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
	step("скачано %s", humanSize(size))

	initScript, socket, bin := ServicePaths(opts.Platform)
	res.Bin = bin

	// стоп до установки: замена файла работающего бинаря падает ETXTBSY;
	// на свежей установке службы ещё нет - отказ молча терпим
	if out, e := run(initScript, "stop"); e != nil {
		step("стоп %s пропущен (службы нет?): %s", initScript, firstLine(string(out)))
		// чужая установка: MT жив из путей мимо пакета, гасить его нечем
		// (init-скрипта нет) - новый MT тогда не поднимется на его портах.
		if pids := magitricklePids(); len(pids) > 0 {
			for pid, exe := range pids {
				step("живой MT мимо пакета (pid %d, %s) - гашу", pid, exe)
				_, _ = run("kill", "-9", strconv.Itoa(pid))
			}
		}
	} else {
		step("остановлен %s", initScript)
	}
	// стейл-сокет после убитого форграунда молча валит старт init-скриптом
	_ = os.Remove(socket)

	out, err := installPackage(run, mgr, dest)
	// opkg молча пропускает установку при совпавшей версии («is up to
	// date») - на диске может остаться чужой/старый бинарь (живой кейс:
	// ручная подмена бинаря без переключения базы opkg, тест владельца
	// на 0.42). Переустанавливаем принудительно.
	if err == nil && mgr == "opkg" && strings.Contains(strings.ToLower(string(out)), "up to date") {
		step("opkg пропустил установку (up to date) - переустанавливаю принудительно")
		out, err = installPackage(run, mgr, dest, "--force-reinstall")
	}
	// opkg не берёт файл пакета, когда его архитектуры нет в opkg.conf:
	// файл молча игнорируется («has no valid architecture»), установка
	// срывается в download из фидов («not available from any configured
	// src», «incompatible with the architectures configured»), а в базе
	// может оставаться битая запись с той же чужой архитектурой (живой
	// кейс Keenetic-aarch64: в opkg.conf только «arch aarch64-3.10», нет
	// ни «arch all», ни «aarch64-3.10_kn»). Лечение: дописать
	// недостающие arch-строки, снять битую запись, поставить из файла.
	if err != nil && mgr == "opkg" && opkgArchRefused(string(out)) {
		step("opkg не взял пакет (его архитектуры нет в opkg.conf / битая запись) - дополняю конфиг и ставлю из файла")
		conf := opts.OpkgConf
		if conf == "" {
			conf = defaultOpkgConf(opts.Platform)
		}
		if conf != "" {
			if ensureOpkgArch(conf, "all", 50) {
				step("opkg.conf: добавлена строка arch all 50")
			}
			if arch := assetArch(asset.Name, ext); arch != "" && ensureOpkgArch(conf, arch, 300) {
				step("opkg.conf: добавлена строка arch %s", arch)
			}
		}
		_, _ = run("opkg", "remove", opkgPackage)
		out, err = installPackage(run, mgr, dest)
	}
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
			step("%s update не удался: %v", mgr, uerr)
		}
		out2, err2 := installPackage(run, mgr, dest)
		// apk-солвер отказывает и при несовпадении arch устройства с arch
		// пакета (живой кейс: /etc/apk/arch = aarch64, пакет aarch64_generic,
		// v0.7.3 у пользователя); финальная попытка с --arch <arch пакета>
		if err2 != nil && mgr == "apk" && strings.Contains(strings.ToLower(string(out2)), "unable to select packages") {
			if arch := assetArch(asset.Name, ext); arch != "" {
				step("повторяю с --arch %s", arch)
				out3, err3 := installPackage(run, mgr, dest, "--arch", arch)
				if err3 == nil {
					out, err2 = out3, nil
				} else {
					out2, err2 = out3, err3
				}
			}
		}
		if err2 != nil {
			_, _ = run(initScript, "start")
			res.Log = log.String()
			return res, fmt.Errorf("%s install (повтор): %v: %s", mgr, err2, firstLine(string(out2)))
		}
		out = out2
	}
	step("пакет установлен: %s", firstLine(string(out)))
	_ = os.Remove(dest)

	// enable - только OpenWrt (procd): скрипты Entware (S99*) не умеют
	// enable и отвечают Usage - такой «ошибочный» шаг в логе пугал
	// (слово владельца 2026-10-18); автозапуск Entware даёт имя S99
	if opts.Platform != "keenetic" {
		if out, e := run(initScript, "enable"); e != nil {
			step("enable %s: %s", initScript, firstLine(string(out)))
		}
	}
	if out, e := run(initScript, "start"); e != nil {
		res.Log = log.String()
		return res, fmt.Errorf("служба MT не стартовала (%s start): %v: %s", initScript, e, firstLine(string(out)))
	}
	step("служба запущена: %s start", initScript)

	// старт-скрипт проверяет живость мгновенно - демон может умереть сразу
	// после (живой кейс Keenetic-aarch64: plain entware-iptables без
	// ndm-расширений, демон падает на iptables-save «Can't find library
	// for match 'ndmmark'», а скрипт уже отчитался done). Перепроверка и
	// самолечение известного случая: apt-аналог - поставить keenetic-_kn
	// сборку iptables из keenetic-фида (её версия выше plain).
	if procAlive() {
		time.Sleep(postStartDelay)
		if len(magitricklePids()) == 0 {
			out, perr := probeIptablesSave(run, opts.Platform)
			if perr != nil && iptablesNdmmarkBroken(string(out)) {
				step("демон умер: iptables без ndm-расширений Keenetic - обновляю iptables до keenetic-сборки и стартую заново")
				_ = updateLists(run, "opkg")
				if iout, ie := run("opkg", "install", "iptables"); ie != nil {
					step("обновить iptables не вышло: %s", firstLine(string(iout)))
				}
				_, _ = run(initScript, "start")
				time.Sleep(postStartDelay)
			}
			if len(magitricklePids()) == 0 {
				res.Log = log.String()
				return res, fmt.Errorf("демон завершился сразу после старта - смотрите вывод iptables-save и журнал роутера (частая причина: зависимости/конфиг)")
			}
			step("демон жив после перепроверки")
		}
	}
	res.Log = log.String()
	return res, nil
}

func installPackage(run func(string, ...string) ([]byte, error), mgr, dest string, extraArgs ...string) ([]byte, error) {
	if mgr == "apk" {
		args := append([]string{"add", "--allow-untrusted"}, extraArgs...)
		return run("apk", append(args, dest)...)
	}
	args := append([]string{"install"}, extraArgs...)
	return run("opkg", append(args, dest)...)
}

// assetArch - arch-часть имени пакета релиза (magitrickle_<ver>_<platform>_
// <arch>.ext): apk-солвер сверяет её с /etc/apk/arch устройства, и у части
// сборок (живой кейс: устройство с arch «aarch64», пакет aarch64_generic)
// они не совпадают - тогда add отказывает «unable to select packages».
func assetArch(assetName, ext string) string {
	s := strings.TrimSuffix(strings.TrimPrefix(assetName, "magitrickle_"), ext)
	parts := strings.SplitN(s, "_", 3)
	if len(parts) < 3 {
		return ""
	}
	return parts[2]
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
// по зависимостям или солверу apk, а не поломанный пакет.
func depRetry(mgr, out string) bool {
	s := strings.ToLower(out)
	switch {
	case strings.Contains(s, "cannot find package"),
		strings.Contains(s, "unknown package"),
		strings.Contains(s, "no satisfiable"),
		strings.Contains(s, "unable to select packages"),
		strings.Contains(s, "depend"):
		return true
	}
	return false
}

// opkgArchRefused - вывод opkg означает «пакет есть, но его архитектура
// не в arch-списке opkg.conf»: локальный файл игнорируется, установка
// уходит в download из фидов.
func opkgArchRefused(out string) bool {
	s := strings.ToLower(out)
	switch {
	case strings.Contains(s, "has no valid architecture"),
		strings.Contains(s, "not available from any configured src"),
		strings.Contains(s, "incompatible with the architectures"),
		strings.Contains(s, "unknown package"):
		return true
	}
	return false
}

// defaultOpkgConf - путь opkg.conf платформы (пусто - права/файла нет).
func defaultOpkgConf(platform string) string {
	if platform == "keenetic" {
		return "/opt/etc/opkg.conf"
	}
	return "/etc/opkg.conf"
}

// ensureOpkgArch дописывает «arch <имя> <приоритет>» в opkg.conf, если
// такой архитектуры там ещё нет (приоритет сдвигается вверх, чтобы не
// совпасть с занятым). Возвращает true, если файл изменён.
func ensureOpkgArch(path, arch string, prio int) bool {
	if arch == "" {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	want := "arch " + arch
	used := map[int]bool{}
	for _, line := range lines {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] != "arch" {
			continue
		}
		if f[1] == arch {
			return false
		}
		if len(f) >= 3 {
			if n, e := strconv.Atoi(f[2]); e == nil {
				used[n] = true
			}
		}
	}
	for used[prio] {
		prio += 10
	}
	out := strings.Join(lines, "\n") + "\n" + want + " " + strconv.Itoa(prio) + "\n"
	return os.WriteFile(path, []byte(out), 0o644) == nil
}

// procDir - каталог процессов для magitricklePids (подмена в тестах).
var procDir = "/proc"

// procAlive - есть ли у процесса mawg /proc (Linux-роутер - да, Windows -
// нет); подмена в тестах через TestMain.
var procAlive = func() bool {
	_, err := os.Stat(filepath.Join(procDir, "self"))
	return err == nil
}

// postStartDelay - пауза перед перепроверкой живости демона (подмена в тестах).
var postStartDelay = 3 * time.Second

// magitricklePids - живые процессы magitrickled: pid -> путь бинаря.
// Чужие установки ставят MT мимо пакета, гасить их нечем (init-скрипта
// нет) - новый MT не поднимется на занятых портах.
func magitricklePids() map[int]string {
	pids := map[int]string{}
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return pids
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(name)
		if err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join(procDir, name, "comm"))
		if err != nil || strings.TrimSpace(string(b)) != "magitrickled" {
			continue
		}
		exe, _ := os.Readlink(filepath.Join(procDir, name, "exe"))
		pids[pid] = exe
	}
	return pids
}

// probeIptablesSave - iptables-save; на Кинетике entware-бинарь живёт в
// /opt/sbin и может не попасть в PATH процесса mawg - фолбэк полным путём.
func probeIptablesSave(run func(string, ...string) ([]byte, error), platform string) ([]byte, error) {
	out, err := run("iptables-save")
	if err == nil || platform != "keenetic" {
		return out, err
	}
	return run("/opt/sbin/iptables-save")
}

// iptablesNdmmarkBroken - вывод iptables-save означает «стоит plain
// entware-iptables без ndm-расширений»: правила прошивки Keenetic
// используют ndmmark, демон MT умирает на чистке правил при старте.
func iptablesNdmmarkBroken(out string) bool {
	s := strings.ToLower(out)
	return strings.Contains(s, "ndmmark") || strings.Contains(s, "can't find library")
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

// humanSize - размер по-человечески (слово владельца: «скачано 2451067
// байт» нечитаемо): килобайты, от мегабайта - МБ с десятыми.
func humanSize(n int64) string {
	if n >= 1<<20 {
		return fmt.Sprintf("%.1f МБ", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.0f КБ", float64(n)/1024)
}

func mb(n int64) string {
	return fmt.Sprintf("%.1f МБ", float64(n)/float64(1<<20))
}
