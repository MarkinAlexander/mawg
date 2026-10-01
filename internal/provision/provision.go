package provision

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Item struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Installed      bool   `json:"installed"`
	Version        string `json:"version"`
	Action         string `json:"action,omitempty"`
	Confirm        string `json:"confirm,omitempty"`
	RequiresReboot bool   `json:"requiresReboot,omitempty"`
	Note           string `json:"note,omitempty"`
}

type Result struct {
	Platform string `json:"platform"`
	Items    []Item `json:"items"`
}

type Runner func(script string, timeout time.Duration) (string, error)

func shellRun(script string, timeout time.Duration) (string, error) {
	cmd := exec.Command("/bin/sh", "-c", script)
	if os.Getenv("PATH") == "" {
		cmd.Env = []string{"PATH=/sbin:/usr/sbin:/bin:/usr/bin:/opt/bin:/opt/sbin"}
	}
	done := make(chan struct{})
	var out []byte
	var err error
	go func() {
		out, err = cmd.CombinedOutput()
		close(done)
	}()
	select {
	case <-done:
		return string(out), err
	case <-time.After(timeout):
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		<-done
		return string(out), fmt.Errorf("timeout")
	}
}

func opkgInstalled(pkg string) (bool, string) {
	out, _ := shellRun("opkg list-installed 2>/dev/null | grep '^"+pkg+" '", 15*time.Second)
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

func openwrtRelease() string {
	out, _ := shellRun(". /etc/openwrt_release 2>/dev/null; echo $DISTRIB_RELEASE", 5*time.Second)
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

func CheckOpenwrt() Result {
	res := Result{Platform: "openwrt", Items: []Item{}}

	if ok, ver := opkgInstalled("kmod-wireguard"); ok {
		res.Items = append(res.Items, Item{ID: "wireguard", Title: "WireGuard (модуль ядра)", Installed: true, Version: ver})
	} else {
		res.Items = append(res.Items, Item{
			ID: "wireguard", Title: "WireGuard (модуль ядра)", Installed: false,
			Action:  "opkg update && opkg install kmod-wireguard wireguard-tools luci-proto-wireguard",
			Confirm: "Установить пакеты WireGuard из официального репозитория?",
		})
	}

	awgOk, awgVer := opkgInstalled("amneziawg-tools")
	kmodOk, _ := opkgInstalled("kmod-amneziawg")
	if awgOk && awgToolsVersion(awgVer) >= 3 {
		res.Items = append(res.Items, Item{ID: "awg", Title: "AmneziaWG (обфускация)", Installed: true, Version: awgVer})
	} else if awgOk {
		release := openwrtRelease()
		note := "Установлена версия 1.x без I-пакетов: часть обфусцированных конфигов (Proton) не подключится."
		action := ""
		if release == "24.10.8" || strings.HasPrefix(release, "25.") {
			action = "wget -4 -qO /tmp/awg-install.sh https://raw.githubusercontent.com/Slava-Shchipunov/awg-openwrt/refs/heads/master/amneziawg-install.sh && sh /tmp/awg-install.sh -e -n < /dev/null"
			note += " Доступно обновление до 3.1."
		} else {
			note += " Для обновления нужна прошивка 24.10.8 или новее (сейчас " + release + "), затем повторите проверку."
		}
		res.Items = append(res.Items, Item{ID: "awg", Title: "AmneziaWG (обфускация)", Installed: false, Version: awgVer, Action: action,
			Confirm: "Обновить пакеты AmneziaWG до 3.1? Заменяется модуль ядра, после установки нужен перезапуск интерфейсов.",
			Note:    note})
	} else if kmodOk {
		res.Items = append(res.Items, Item{ID: "awg", Title: "AmneziaWG (обфускация)", Installed: true, Version: "kmod без утилит"})
		res.Items = append(res.Items, Item{
			ID: "awg-tools", Title: "Утилиты AmneziaWG", Installed: false,
			Action:  "opkg update && opkg install amneziawg-tools",
			Confirm: "Установить утилиты amneziawg-tools?",
		})
	} else {
		res.Items = append(res.Items, Item{
			ID: "awg", Title: "AmneziaWG (обфускация)", Installed: false,
			Action:  "opkg update && opkg install kmod-amneziawg amneziawg-tools",
			Confirm: "Установить пакеты AmneziaWG из репозитория?",
			Note:    "В официальном репозитории версия 1.x; для I-пакетов (AWG 2.0+) нужна прошивка 24.10.8+ и обновление до 3.1.",
		})
	}

	if ok, ver := opkgInstalled("magitrickle"); ok {
		res.Items = append(res.Items, Item{ID: "magitrickle", Title: "MagiTrickle (маршрутизация по доменам)", Installed: true, Version: ver})
	} else {
		res.Items = append(res.Items, Item{
			ID: "magitrickle", Title: "MagiTrickle (маршрутизация по доменам)", Installed: false,
			Action:  "wget -qO /tmp/mt-repo.sh http://bin.magitrickle.dev/packages/add_repo.sh && sh /tmp/mt-repo.sh && opkg update && opkg install magitrickle && (/etc/init.d/magitrickle enable && /etc/init.d/magitrickle start)",
			Confirm: "Добавить репозиторий bin.magitrickle.dev и установить MagiTrickle с зависимостями?",
		})
	}

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
			Action:  "wget -qO /tmp/mt-repo.sh http://bin.magitrickle.dev/packages/add_repo.sh && sh /tmp/mt-repo.sh && opkg update && opkg install magitrickle socat && chmod +x /opt/etc/init.d/S99magitrickle && /opt/etc/init.d/S99magitrickle start",
			Confirm: "Добавить репозиторий bin.magitrickle.dev и установить MagiTrickle с socat?",
		})
	}

	return res
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
