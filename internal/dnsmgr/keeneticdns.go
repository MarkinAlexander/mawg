package dnsmgr

// Режим «DNS самого роутера» на Keenetic (98-док): панель исторически в
// ndm не пишет (дизайн-слово 69-док §5), но ndm без единого апстрима
// оглохнет - обновления, opkg и hostname-эндпоинты пулов (например WARP)
// резолвит именно он (живой кейс 31.10: галки «игнорировать провайдера»
// + стёртый DoH = ноль запросов, при живой локалке через MT). По явному
// согласию пользователя пишем ОДНУ запись ip name-server: страховочный
// сервер или MT:3553; рестор - снятие ровно этой записи. Синтаксис RCI
// подтверждён мануалом + живым диффом (69-док §1/§3):
//   add:    ndmc -c "ip name-server <ip> [port] ["" on <iface>]"
//   remove: ndmc -c "no ip name-server ..."
//   persist: ndmc -c "system configuration save"
// ndmc молча проглатывает кривые команды (код возврата не показателен) -
// каждая запись/снятие верифицируется чтением running-config.

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// ndmMTArgs - аргументы name-server режима «через MagiTrickle»
// (dns-proxy слушает [::]:3553, loopback входит).
const ndmMTArgs = "127.0.0.1 3553"

// NameServerArgs - каноническая строка аргументов RCI после
// "ip name-server": "<ip>[ <port>][ "" on <iface>]". Applied хранится
// ровно в этом виде - add/remove строятся конкатенацией.
func NameServerArgs(spare, iface string) (string, error) {
	t := strings.TrimSpace(spare)
	if t == "" {
		return "", fmt.Errorf("сервер не указан")
	}
	host, port := t, ""
	if h, p, err := net.SplitHostPort(t); err == nil {
		host, port = h, p
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		return "", fmt.Errorf("нужен IP-адрес (имя ndm резолвить нечем), получено %q", spare)
	}
	if ip.To4() == nil {
		return "", fmt.Errorf("IPv6 этим переключателем не поддерживается (ipv6 name-server - отдельная команда)")
	}
	args := ip.String()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n <= 0 || n > 65535 {
			return "", fmt.Errorf("порт: %q", port)
		}
		args += " " + port
	}
	if iface = strings.TrimSpace(iface); iface != "" {
		if strings.ContainsAny(iface, "\"'\n;") {
			return "", fmt.Errorf("подключение: недопустимые символы в %q", iface)
		}
		args += ` "" on ` + iface
	}
	return args, nil
}

// ApplyKeeneticDNS применяет режим: "off" снимает applied (если была),
// "spare" ставит ip[:port] (опционально on <iface>), "mt" - 127.0.0.1:3553.
// applied - прежняя запись панели (аргументы RCI); возвращается новая
// (пустая для off). Каждое изменение верифицируется чтением.
func ApplyKeeneticDNS(r Runner, mode, spare, iface, applied string) (string, error) {
	var target string
	switch mode {
	case "off":
		target = ""
	case "spare":
		args, err := NameServerArgs(spare, iface)
		if err != nil {
			return "", err
		}
		target = args
	case "mt":
		target = ndmMTArgs
	default:
		return "", fmt.Errorf("неизвестный режим %q", mode)
	}
	if applied = strings.TrimSpace(applied); applied == target {
		return target, nil // уже стоит
	}
	if applied != "" {
		if err := ndmNameServerRemove(r, applied); err != nil {
			return "", err
		}
	}
	if target != "" {
		if err := ndmNameServerAdd(r, target); err != nil {
			return "", err
		}
	}
	return target, nil
}

func ndmNameServerAdd(r Runner, args string) error {
	out, err := r(ndmcCmd("ip name-server "+args)+" ; "+ndmcCmd("system configuration save"), 30*time.Second)
	if err != nil {
		return fmt.Errorf("ndmc ip name-server: %w: %s", err, strings.TrimSpace(out))
	}
	if !KeeneticDNSLive(r, args) {
		return fmt.Errorf("Кинетик не принял запись ip name-server %s (проверьте версию прошивки)", args)
	}
	return nil
}

func ndmNameServerRemove(r Runner, args string) error {
	// no-форма RCI опознаёт запись по ведущей части (как no https upstream
	// [<url>]): пробуем точную строку, затем обрезки справа - пустое
	// доменное поле ("" on iface) и порт могут не матчиться
	variants := removeVariants(args)
	var lastErr error
	for _, v := range variants {
		out, err := r(ndmcCmd("no ip name-server "+v)+" ; "+ndmcCmd("system configuration save"), 30*time.Second)
		if err != nil {
			lastErr = fmt.Errorf("ndmc no ip name-server: %w: %s", err, strings.TrimSpace(out))
			continue
		}
		if !KeeneticDNSLive(r, args) {
			return nil
		}
		lastErr = fmt.Errorf("запись %s ещё стоит после no ip name-server %s", args, v)
	}
	return fmt.Errorf("Кинетик не снял запись ip name-server %s: %v", args, lastErr)
}

// removeVariants - формы no-команды от точной к самой короткой:
// `8.8.8.8 "" on ISP` → `8.8.8.8 on ISP` → `8.8.8.8` (порт в составе).
func removeVariants(args string) []string {
	out := []string{args}
	fields := strings.Fields(args)
	if len(fields) > 1 && fields[len(fields)-2] == "on" {
		trimmed := strings.Join(fields[:len(fields)-3], " ") + " on " + fields[len(fields)-1]
		out = append(out, strings.TrimSpace(trimmed))
		trimmed = strings.Join(fields[:len(fields)-3], " ")
		out = append(out, strings.TrimSpace(trimmed))
	} else if len(fields) > 1 {
		out = append(out, fields[0])
	}
	return out
}

// ndmcCmd - вызов RCI через CLI: одинарные кавычки снаружи (пустое
// доменное поле `""` доходит до ndmc буквально); аргументы валидированы
// в NameServerArgs - одиночных кавычек и служебных символов там нет.
func ndmcCmd(rci string) string {
	return `ndmc -c '` + rci + `'`
}

// KeeneticDNSLive - запись стоит в running-config (верификация чтением):
// строка вида "ip name-server <args>" (глобальная или с on <iface>).
func KeeneticDNSLive(r Runner, args string) bool {
	args = strings.TrimSpace(args)
	if args == "" {
		return false
	}
	out, err := KeeneticRunningConfig(r)
	if err != nil {
		return false
	}
	return NameServerInConfig(out, args)
}

// KeeneticRunningConfig - running-config живьём (одна точка чтения для
// верификаций: name-server записи, перехват, собственные серверы).
func KeeneticRunningConfig(r Runner) (string, error) {
	out, err := r(ndmcCmd("show running-config"), 30*time.Second)
	if err != nil {
		return "", fmt.Errorf("running-config: %w", err)
	}
	return out, nil
}

// NameServerInConfig - поиск записи "ip name-server <args>" в тексте
// running-config ("ip no name-servers" и чужие записи не совпадают).
func NameServerInConfig(config, args string) bool {
	return nameServerPresent(config, args)
}

// SetKeeneticIntercept - глобальный перехват ndm (system profile):
// `dns-proxy intercept enable` ловит весь :53 - транзит клиентов с чужим
// DNS и собственные запросы роутера (эмпирика 31.10: nslookup к 8.8.8.8
// отвечает профильный upstream). Выключение - `no intercept enable`.
// Обе стороны верифицируются чтением running-config.
func SetKeeneticIntercept(r Runner, enable bool) error {
	cmd := "dns-proxy no intercept enable"
	if enable {
		cmd = "dns-proxy intercept enable"
	}
	out, err := r(ndmcCmd(cmd)+" ; "+ndmcCmd("system configuration save"), 30*time.Second)
	if err != nil {
		return fmt.Errorf("ndmc %s: %w: %s", cmd, err, strings.TrimSpace(out))
	}
	cfg, err := KeeneticRunningConfig(r)
	if err != nil || ParseKeeneticOwn(cfg).Intercept != enable {
		return fmt.Errorf("Кинетик не принял dns-proxy %s", cmd)
	}
	return nil
}

// nameServerPresent - поиск записи в тексте running-config: строка
// "ip name-server <args...>" начинается точно с наших аргументов
// ("ip no name-servers" и записи других серверов не совпадают).
func nameServerPresent(config, args string) bool {
	prefix := "ip name-server "
	for _, raw := range strings.Split(config, "\n") {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		rest := strings.TrimPrefix(line, prefix)
		if rest == args || strings.HasPrefix(rest, args+" ") {
			return true
		}
	}
	return false
}
