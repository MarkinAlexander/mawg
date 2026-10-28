package provision

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"mawg/internal/platform"
	"mawg/internal/singbox"
)

type Item struct {
	ID             string   `json:"id"`
	Title          string   `json:"title"`
	Installed      bool     `json:"installed"`
	Version        string   `json:"version"`
	Action         string   `json:"action,omitempty"`
	ActionLabel    string   `json:"actionLabel,omitempty"`
	Flavors        []string `json:"flavors,omitempty"`
	Confirm        string   `json:"confirm,omitempty"`
	RequiresReboot bool     `json:"requiresReboot,omitempty"`
	Note           string   `json:"note,omitempty"`
	// StatusText - свой текст бейджа состояния вместо ок/нет («чужое upstream»).
	StatusText string `json:"statusText,omitempty"`
	// Source - источник установленного ядра (lx|extended|upstream): диалог
	// предвыбирает его, чтобы «обновить» не меняло ядро молча.
	Source string `json:"source,omitempty"`
	// FreeBytes/BackupBytes - место на диске и размер ядра: диалог решает,
	// влезет ли бэкап старого бинаря.
	FreeBytes   int64 `json:"freeBytes,omitempty"`
	BackupBytes int64 `json:"backupBytes,omitempty"`
}

type Result struct {
	Platform string `json:"platform"`
	Items    []Item `json:"items"`
}

type Runner func(script string, timeout time.Duration) (string, error)

// mtForkAction - маркер для web-обработчика: MagiTrickle ставится кодом
// mtinstall.Install из релиза mawg-форка (bin.magitrickle.dev - апстримный
// репозиторий без форка - оставлен как запасной путь в install.sh).
const MtForkAction = "mt-fork"

func shellRun(script string, timeout time.Duration) (string, error) {
	cmd := exec.Command("/bin/sh", "-c", script)
	if os.Getenv("PATH") == "" {
		cmd.Env = []string{"PATH=/sbin:/usr/sbin:/bin:/usr/bin:/opt/bin:/opt/sbin"}
	}
	// таймаут убивает всю группу: у пайплайнов (opkg list | grep) иначе
	// выживают осиротевшие участники и жгут CPU
	return platform.RunBoundedCombined(cmd, timeout)
}

func opkgHasPackage(run Runner, pkg string) bool {
	out, err := run("opkg list 2>/dev/null | grep -m1 '^"+pkg+" '", 20*time.Second)
	return err == nil && strings.TrimSpace(out) != ""
}

func opkgInstalled(pkg string) (bool, string) {
	out, _ := shellRun("opkg list-installed 2>/dev/null | grep '^"+pkg+" '", 15*time.Second)
	return parseOpkgInstalled(out)
}

func opkgInstalledWithRunner(run Runner, pkg string) (bool, string) {
	out, err := run("opkg list-installed 2>/dev/null | grep '^"+pkg+" '", 15*time.Second)
	if err != nil {
		return false, ""
	}
	return parseOpkgInstalled(out)
}

func parseOpkgInstalled(out string) (bool, string) {
	line := strings.TrimSpace(out)
	if line == "" {
		return false, ""
	}
	parts := strings.Fields(line)
	if len(parts) >= 3 && parts[1] == "-" {
		return true, parts[2]
	}
	if len(parts) >= 2 {
		return true, parts[1]
	}
	return true, ""
}

func openwrtRelease(run Runner) string {
	out, _ := run(". /etc/openwrt_release 2>/dev/null; echo $DISTRIB_RELEASE", 5*time.Second)
	return strings.TrimSpace(out)
}

var awg3Re = regexp.MustCompile(`^(\d+)\.`)

func awgToolsVersion(v string) int {
	m := awg3Re.FindStringSubmatch(v)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

const awgDirectAction = `rel=$(. /etc/openwrt_release 2>/dev/null; echo "$DISTRIB_RELEASE:$DISTRIB_ARCH") && ver=${rel%%:*} && arch=${rel##*:} && tgt=$(ubus call system board 2>/dev/null | tr -d ' ' | grep -o '"target":"[^"]*"' | cut -d'"' -f4 | tr '/' '_') && base="https://github.com/2Grey/awg-openwrt/releases/download/v$ver" && wget -4 -q -O /tmp/kmod-awg.ipk "$base/kmod-amneziawg_v${ver}_${arch}_${tgt}.ipk" && wget -4 -q -O /tmp/tools-awg.ipk "$base/amneziawg-tools_v${ver}_${arch}_${tgt}.ipk" && opkg install /tmp/kmod-awg.ipk /tmp/tools-awg.ipk && (modprobe amneziawg 2>/dev/null || true) && rm -f /tmp/kmod-awg.ipk /tmp/tools-awg.ipk`

const awgScriptAction = "wget -4 -qO /tmp/awg-install.sh https://raw.githubusercontent.com/2Grey/awg-openwrt/refs/heads/master/amneziawg-install.sh && sh /tmp/awg-install.sh -e -n < /dev/null"

func awgAction(release string) string {
	if strings.HasPrefix(release, "25.") {
		return awgScriptAction
	}
	return awgDirectAction
}

type openwrtPackages struct {
	run  Runner
	name string
}

func (p openwrtPackages) awgAction(release string) string {
	if p.name == "apk" {
		return awgScriptAction
	}
	return awgAction(release)
}

func (p openwrtPackages) install(pkgs string) string {
	if p.name == "apk" {
		return "apk update && apk add " + pkgs
	}
	return "opkg update && opkg install " + pkgs
}

func (p openwrtPackages) hasPackage(pkg string) bool {
	if p.name != "apk" {
		return opkgHasPackage(p.run, pkg)
	}
	out, err := p.run("apk search -x "+pkg, 20*time.Second)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		version, ok := strings.CutPrefix(strings.TrimSpace(line), pkg+"-")
		if ok && len(version) > 0 && version[0] >= '0' && version[0] <= '9' && len(strings.Fields(version)) == 1 {
			return true
		}
	}
	return false
}

func detectOpenwrtPackages(run Runner) openwrtPackages {
	if _, err := run("test -x /bin/opkg", 5*time.Second); err == nil {
		return openwrtPackages{run: run, name: "opkg"}
	}
	for _, name := range []string{"apk", "opkg"} {
		if _, err := run("command -v "+name+" >/dev/null 2>&1", 5*time.Second); err == nil {
			return openwrtPackages{run: run, name: name}
		}
	}
	return openwrtPackages{run: run}
}

func (p openwrtPackages) installed(pkg string) (bool, string) {
	if p.name != "apk" {
		return opkgInstalledWithRunner(p.run, pkg)
	}
	out, err := p.run("apk list --installed --manifest", 15*time.Second)
	if err != nil {
		return false, ""
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == pkg {
			return true, fields[1]
		}
	}
	return false, ""
}

func CheckOpenwrt() Result {
	return checkOpenwrt(shellRun)
}

func checkOpenwrt(run Runner) Result {
	res := Result{Platform: "openwrt", Items: []Item{}}
	packages := detectOpenwrtPackages(run)
	if packages.name == "" {
		res.Items = []Item{
			{ID: "wireguard", Title: "WireGuard (модуль ядра)"},
			{ID: "awg", Title: "AmneziaWG (обфускация)"},
			{ID: "magitrickle", Title: "MagiTrickle (маршрутизация по доменам)"},
		}
		for i := range res.Items {
			res.Items[i].Note = "Не найден менеджер пакетов apk или opkg; проверить установленные пакеты и предложить установку невозможно."
		}
		return res
	}

	if ok, ver := packages.installed("kmod-wireguard"); ok {
		res.Items = append(res.Items, Item{ID: "wireguard", Title: "WireGuard (модуль ядра)", Installed: true, Version: ver})
	} else {
		res.Items = append(res.Items, Item{
			ID: "wireguard", Title: "WireGuard (модуль ядра)", Installed: false,
			Action:  packages.install("kmod-wireguard wireguard-tools luci-proto-wireguard"),
			Confirm: "Установить пакеты WireGuard из официального репозитория?",
		})
	}

	awgOk, awgVer := packages.installed("amneziawg-tools")
	kmodOk, _ := packages.installed("kmod-amneziawg")
	if awgOk && awgToolsVersion(awgVer) >= 3 {
		res.Items = append(res.Items, Item{ID: "awg", Title: "AmneziaWG (обфускация)", Installed: true, Version: awgVer})
	} else if awgOk {
		release := openwrtRelease(run)
		note := "Установлена версия 1.x без I-пакетов: часть обфусцированных конфигов (Proton) не подключится."
		action := ""
		if packages.name == "apk" || strings.HasPrefix(release, "24.10") || strings.HasPrefix(release, "25.") {
			action = packages.awgAction(release)
			note += " Доступно обновление до 3.1."
		} else {
			note += " Для обновления нужна прошивка 24.10 или новее (сейчас " + release + "), затем повторите проверку."
		}
		res.Items = append(res.Items, Item{ID: "awg", Title: "AmneziaWG (обфускация)", Installed: false, Version: awgVer, Action: action,
			Confirm: "Обновить пакеты AmneziaWG до 3.1? Заменяется модуль ядра, после установки нужен перезапуск интерфейсов.",
			Note:    note})
	} else if kmodOk {
		res.Items = append(res.Items, Item{ID: "awg", Title: "AmneziaWG (обфускация)", Installed: true, Version: "kmod без утилит"})
		res.Items = append(res.Items, Item{
			ID: "awg-tools", Title: "Утилиты AmneziaWG", Installed: false,
			Action:  packages.install("amneziawg-tools"),
			Confirm: "Установить утилиты amneziawg-tools?",
		})
	} else {
		action := packages.install("kmod-amneziawg amneziawg-tools")
		confirm := "Установить пакеты AmneziaWG из официального репозитория?"
		note := "В официальном репозитории версия 1.x; для I-пакетов (AWG 2.0+) затем обновление до 3.1."
		if !packages.hasPackage("amneziawg-tools") {
			action = packages.awgAction(openwrtRelease(run))
			confirm = "Установить AmneziaWG 3.1 из репозитория 2Grey/awg-openwrt? Заменяется модуль ядра, после установки нужна перезагрузка."
			note = "Пакетов AmneziaWG в настроенных репозиториях нет; ставится сборка 3.1 (I-пакеты AWG 2.0/3.x) с заменой модуля ядра."
		}
		res.Items = append(res.Items, Item{
			ID: "awg", Title: "AmneziaWG (обфускация)", Installed: false,
			Action: action, Confirm: confirm, Note: note,
		})
	}

	if ok, ver := packages.installed("magitrickle"); ok {
		res.Items = append(res.Items, Item{ID: "magitrickle", Title: "MagiTrickle (маршрутизация по доменам)", Installed: true, Version: ver})
	} else {
		res.Items = append(res.Items, Item{
			ID: "magitrickle", Title: "MagiTrickle (маршрутизация по доменам)", Installed: false,
			Action:  MtForkAction,
			Confirm: "mawg определит архитектуру роутера, скачает пакет MagiTrickle (mawg-форк: источники групп, нативный OUTPUT-jump, API каскада DNS) из релиза GitHub и установит его штатным менеджером пакетов с зависимостями; служба будет включена и запущена.",
		})
	}

	res.Items = append(res.Items, lxCoreItem(run, "openwrt"))
	res.Items = append(res.Items, mihomoCoreItem(run, "openwrt"))
	return res
}

func CheckKeenetic(ndmc func(string) (string, error)) Result {
	res := Result{Platform: "keenetic", Items: []Item{}}
	out, err := ndmc("show version")
	info := parseKeeneticVersion(out)
	if err == nil && info.major > 0 {
		model := info.description
		if model == "" {
			model = info.model
		}
		if model == "" {
			model = "Keenetic"
		}
		version := info.title
		if version == "" {
			version = fmt.Sprintf("%d.%d", info.major, info.minor)
		}
		ok := info.major > 5 || (info.major == 5 && info.minor >= 1)
		item := Item{ID: "firmware", Title: "Платформа: " + model, Installed: ok, Version: version}
		if !ok {
			item.Note = "Для параметров AWG 2.0 нужна прошивка 5.1+. Обновите роутер штатными средствами."
		}
		res.Items = append(res.Items, item)

		if info.components["wireguard"] {
			res.Items = append(res.Items, Item{ID: "wireguard", Title: "Компонент WireGuard", Installed: true})
		} else {
			res.Items = append(res.Items, Item{
				ID: "wireguard", Title: "Компонент WireGuard", Installed: false,
				Action:  "/bin/ndmc -c 'components install wireguard' && /bin/ndmc -c 'components commit'",
				Confirm: "Установить системный компонент WireGuard и применить изменения?",
			})
		}
	}

	if ok, ver := opkgInstalled("magitrickle"); ok {
		res.Items = append(res.Items, Item{ID: "magitrickle", Title: "MagiTrickle (Entware)", Installed: true, Version: ver})
	} else {
		res.Items = append(res.Items, Item{
			ID: "magitrickle", Title: "MagiTrickle (Entware)", Installed: false,
			Action:  MtForkAction,
			Confirm: "mawg определит архитектуру, скачает пакет MagiTrickle (mawg-форк) из релиза GitHub и установит его opkg с зависимостями; служба S99magitrickle будет запущена.",
		})
	}

	res.Items = append(res.Items, lxCoreItem(shellRun, "keenetic"))
	res.Items = append(res.Items, mihomoCoreItem(shellRun, "keenetic"))
	return res
}

// lxFileStat - (размер, mtime, существует); подменяется в тестах. Реальные
// проверки ядра идут через stat/statfs, а не shell: wc -c < sing-box и df
// на медленной флешке (40МБ бинарь ~3.4с чтения) при опросе панели каждые
// 60с съедали CPU роутера пачкой одноимённых процессов.
var (
	lxFileStat = func(p string) (int64, time.Time, bool) {
		st, err := os.Stat(p)
		if err != nil {
			return 0, time.Time{}, false
		}
		return st.Size(), st.ModTime(), true
	}
	lxDiskFree = platform.DiskFreeBytes
)

// Кэш версии бинаря по stat-ключу (размер+mtime): `sing-box version`
// вычитывает весь бинарь с флешки (те же секунды), а версия меняется
// только вместе с файлом - при замене mtime всегда свежий.
var (
	lxVerMu    sync.Mutex
	lxVerCache = map[string]lxVerEntry{}
)

type lxVerEntry struct {
	size  int64
	mtime time.Time
	ver   string
}

func cachedBinVersion(bin string, run Runner) string {
	size, mtime, ok := lxFileStat(bin)
	if !ok {
		out, err := runBinVersion(bin, run)
		if err != nil {
			return ""
		}
		return parseBinVersion(out)
	}
	lxVerMu.Lock()
	c, hit := lxVerCache[bin]
	lxVerMu.Unlock()
	if hit && c.size == size && c.mtime.Equal(mtime) {
		return c.ver
	}
	ver := ""
	if out, err := runBinVersion(bin, run); err == nil {
		ver = parseBinVersion(out)
	} else {
		// таймаут/сбой запуска не кэшируем: «не установлен» не должен
		// закрепиться до замены бинаря
		return ""
	}
	lxVerMu.Lock()
	lxVerCache[bin] = lxVerEntry{size: size, mtime: mtime, ver: ver}
	lxVerMu.Unlock()
	return ver
}

func runBinVersion(bin string, run Runner) (string, error) {
	// upx-профиль на mipsle-softfloat стартует по 10+ секунд (распаковка),
	// потолок должен это переживать: 10с убивал запуск и ядро «пропадало»
	out, err := run(bin+" version 2>/dev/null", 90*time.Second)
	return out, err
}

func parseBinVersion(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return strings.TrimSpace(strings.TrimPrefix(line, "sing-box version"))
		}
	}
	return ""
}

// markerSourceOf - репозиторий из маркера установки ядра sing-box.
func markerSourceOf(markerDir string) string {
	if data, err := os.ReadFile(filepath.Join(markerDir, singbox.LXMarkerName)); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(line), "source="); ok {
				return strings.TrimSpace(v)
			}
		}
	}
	return ""
}

// mihomoCoreItem - пункт «ядро mihomo» в таблице зависимостей: версия,
// статус и кнопка установки/обновления (установка идёт кодом
// internal/mihomo.InstallCore через POST /system/install {id:mihomo}).
func mihomoCoreItem(run Runner, platform string) Item {
	bin, markerDir := "/usr/bin/mihomo", "/etc/mihomo-mawg"
	if platform == "keenetic" {
		bin, markerDir = "/opt/bin/mihomo", "/opt/etc/mihomo-mawg"
	}
	ver := cachedMihomoVersion(bin, run)
	_, _, marked := lxFileStat(filepath.Join(markerDir, singbox.LXMarkerName))
	item := Item{ID: "mihomo", Title: "Ядро mihomo (Clash.Meta)", Version: ver}
	if ver == "" {
		item.Action = "mihomo"
		item.ActionLabel = "установить"
		item.Confirm = "mawg скачает свежий релиз MetaCubeX/mihomo под архитектуру роутера (сумма - digest sha256 из GitHub API), прогонит тест-запуск и установит бинарь в " + bin + ". Распакованное ядро занимает ~54 МБ - на тесной флешке может не влезть, панель честно откажет по месту."
		return item
	}
	item.Installed = true
	item.Action = "mihomo"
	item.ActionLabel = "обновить ядро"
	item.Confirm = "Сейчас стоит mihomo " + ver + ". mawg скачает свежий релиз MetaCubeX/mihomo (той же версии - пропустит), проверит контрольную сумму и тест-запуск, затем заменит бинарь; движковые пулы mihomo будут пересобраны, соединения на них на секунды прервутся."
	if !marked {
		item.Note = "Ядро стоит без маркера mawg (ставили вручную) - обновление заменит бинарь нашей установкой."
	}
	return item
}

// cachedMihomoVersion - версия mihomo с кэшем по размеру+mtime бинаря
// (запуск -v на роутере не бесплатный, /system/check опрашивается часто).
func cachedMihomoVersion(bin string, run Runner) string {
	size, mtime, ok := lxFileStat(bin)
	if !ok {
		return ""
	}
	key := bin + ":mh"
	lxVerMu.Lock()
	c, hit := lxVerCache[key]
	lxVerMu.Unlock()
	if hit && c.size == size && c.mtime.Equal(mtime) {
		return c.ver
	}
	ver := ""
	if out, err := run(bin+" -v 2>/dev/null", 90*time.Second); err == nil {
		ver = parseMihomoVersion(out)
	} else {
		return "" // сбой запуска не кэшируем
	}
	lxVerMu.Lock()
	lxVerCache[key] = lxVerEntry{size: size, mtime: mtime, ver: ver}
	lxVerMu.Unlock()
	return ver
}

func parseMihomoVersion(out string) string {
	line := strings.SplitN(out, "\n", 2)[0]
	for _, f := range strings.Fields(line) {
		if len(f) > 1 && f[0] == 'v' && strings.Contains(f, ".") {
			return f
		}
	}
	return ""
}

// lxCoreItem - пункт «ядро sing-box» (источник установки выбирается в
// диалоге: extended/lx/апстрим). Action - маркер для web-обработчика
// (установка идёт кодом singbox.InstallLXCore), не shell-скрипт; пути
// совпадают с singbox.LXTargetForPlatform.
func lxCoreItem(run Runner, platform string) Item {
	bin, markerDir, disk := "/usr/bin/sing-box", "/etc/sing-box-lx", "/"
	if platform == "keenetic" {
		bin, markerDir, disk = "/opt/bin/sing-box", "/opt/etc/sing-box-lx", "/opt"
	}
	ver := cachedBinVersion(bin, run)
	_, _, marked := lxFileStat(markerDir + "/.installed-by-mawg")
	_, _, binExists := lxFileStat(bin)
	// свободное место и размер бинаря: хватит ли на копию старого ядра
	var backupBytes int64
	if size, _, ok := lxFileStat(bin); ok {
		backupBytes = size
	}
	item := Item{
		ID: "singbox-lx", Title: "Ядро sing-box", Version: ver,
		FreeBytes: lxDiskFree(disk), BackupBytes: backupBytes,
	}
	confirm := fmt.Sprintf("mawg скачает свежий релиз выбранного источника (extended / наш lx / апстрим - выбирается ниже), проверит контрольную сумму, прогонит тест-запуск во временном каталоге и только потом установит в %s. После установки движок mawg работает на этом ядре; в режиме «общего ядра» сервис sing-box будет перезапущен - соединения порвутся на несколько секунд.", bin)
	switch {
	case ver == "" && binExists:
		// бинарь стоит, но version-запуск не уложился в таймаут (upx-
		// распаковка на загруженном роутере) - показывать «не установлен»
		// и звать ставить заново было враньём, владельца это сбило
		item.StatusText = "файл есть, версия не читается (роутер занят?)"
		item.Action = "singbox-lx"
		item.ActionLabel = "переустановить lx"
		item.Confirm = "Ядро sing-box стоит, но версию прочитать не удалось - вероятно, роутер был занят и запуск не уложился в таймаут. Переустановка заменит бинарь нашей сборкой. " + confirm
	case ver == "":
		item.StatusText = "не установлен"
		item.Action = "singbox-lx"
		item.ActionLabel = "установить lx"
		item.Confirm = "Сейчас ядро sing-box не установлено. " + confirm
	case strings.Contains(ver, "-lx."), strings.Contains(ver, "-extended-"), marked:
		// наша установка любого источника (маркер) либо сборка семейства
		// lx/extended, поставленная вручную, - ядро рабочее, обновляемо
		item.Installed = true
		// источник точнее брать из маркера: Leadaxe-lx и наш форк по
		// версии неотличимы ("-lx."), а диалог предвыбирает источник
		item.Source = singbox.DetectSourceID(ver)
		if id, ok := singbox.SourceIDByRepo(markerSourceOf(markerDir)); ok {
			item.Source = id
		}
		item.Action = "singbox-lx"
		item.ActionLabel = "обновить ядро"
		item.Confirm = fmt.Sprintf("Сейчас стоит ядро %s. mawg переустановит его, если релиз новее, выбран другой источник или профиль (plain/сжатый) либо стоящая сборка не наша (нет маркера источника mawg). ", ver) + confirm
		if !marked {
			item.Note = "Ядро стоит без маркера mawg (ставили вручную) - обновление заменит бинарь нашей сборкой того же источника."
			item.ActionLabel = "заменить своей сборкой"
		}
	default:
		item.StatusText = "чужое"
		item.Action = "singbox-lx-replace"
		item.ActionLabel = "заменить"
		item.Note = fmt.Sprintf("Сейчас стоит чужое ядро (upstream %s): mawg его не трогает, замена - только по этой кнопке. Сохранять ли старое перед заменой - выбор в диалоге (зависит от свободного места).", ver)
		item.Confirm = fmt.Sprintf("Заменит чужое ядро %s (upstream %s) на релиз выбранного источника. Сервис sing-box будет перезапущен - соединения порвутся; если сервис не переподнимется с новым бинарем с первого раза, перезапустите его ещё раз. ", bin, ver) + confirm + " Расширенное ядро (lx или extended) нужно движку для xhttp/vless-шифрования и AWG-эндпоинтов."
	}
	return item
}

type keeneticVersion struct {
	title       string
	model       string
	description string
	major       int
	minor       int
	components  map[string]bool
}

var keeneticReleaseRe = regexp.MustCompile(`release:\s+(\d+)\.(\d+)`)
var keeneticCompRe = regexp.MustCompile(`components:\s*(.+)`)
var keeneticTitleRe = regexp.MustCompile(`title:\s*(.+)`)
var keeneticModelRe = regexp.MustCompile(`model:\s*(.+)`)
var keeneticDescRe = regexp.MustCompile(`description:\s*(.+)`)

func firstMatch(re *regexp.Regexp, out string) string {
	if m := re.FindStringSubmatch(out); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func parseKeeneticVersion(out string) keeneticVersion {
	info := keeneticVersion{components: map[string]bool{}}
	if m := keeneticReleaseRe.FindStringSubmatch(out); m != nil {
		info.major, _ = strconv.Atoi(m[1])
		info.minor, _ = strconv.Atoi(m[2])
	}
	info.title = firstMatch(keeneticTitleRe, out)
	info.model = firstMatch(keeneticModelRe, out)
	info.description = firstMatch(keeneticDescRe, out)
	inComp := false
	for _, line := range strings.Split(out, "\n") {
		if m := keeneticCompRe.FindStringSubmatch(line); m != nil {
			inComp = true
			for _, c := range strings.FieldsFunc(m[1], func(r rune) bool { return r == ',' || r == ' ' }) {
				if c = strings.TrimSpace(c); c != "" {
					info.components[c] = true
				}
			}
			continue
		}
		if inComp {
			if line != "" && line[0] == ' ' {
				for _, c := range strings.FieldsFunc(strings.TrimSpace(line), func(r rune) bool { return r == ',' || r == ' ' }) {
					if c = strings.TrimSpace(c); c != "" {
						info.components[c] = true
					}
				}
			} else {
				inComp = false
			}
		}
	}
	return info
}

func Install(action string) (string, error) {
	return shellRun(action, 300*time.Second)
}
