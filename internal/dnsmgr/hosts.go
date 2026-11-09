package dnsmgr

// Локальные записи резолвера роутера (115-док, слово владельца 09.11
// третье: «читать с роутера /etc/hosts и ip host кинетика; править его
// данные и создавать свои»).
//
// Keenetic: записи живут в ndm (`ip host <домен> <ip>`, глобально);
// правка - ndmc + system configuration save, верификация чтением
// running-config (как у name-server - ndmc молча глотает ошибки).
// `ipv6 host` в ndm НЕ СУЩЕСТВУЕТ (проверено 09.11: "no such command:
// host") - ipv6-часть своих записей на Keenetic неприменима, панель
// показывает это честно.
//
// OpenWrt: свои записи mawg держит в СВОЁМ файле /etc/hosts.d/mawg,
// подключённом к dnsmasq списком addn-hosts (uci dhcp addnhosts) -
// чужой /etc/hosts не трогаем вовсе (чтение для показа и удаление
// строк - по явному действию, точным совпадением). dnsmasq перечитывает
// hosts-файлы сам; restart - только при первой привязке addn-hosts.

import (
	"fmt"
	"net"
	"strings"
	"time"
)

// HostEntry - запись резолвера роутера, как она лежит у него.
type HostEntry struct {
	Name string `json:"name"`
	IPv4 string `json:"ipv4,omitempty"`
	IPv6 string `json:"ipv6,omitempty"`
	// Source - откуда запись: "ndm" (ip host Keenetic), "hosts"
	// (/etc/hosts OpenWrt), "mawg" (свой файл mawg на OpenWrt).
	Source string `json:"source"`
}

// MawgHostsFile - свой файл mawg на OpenWrt (addn-hosts dnsmasq).
const MawgHostsFile = "/etc/hosts.d/mawg"

// ValidateHostName - доменное имя записи: непустое, без пробелов и
// служебных символов; точку-суффикс снимаем (можно с "www.example.com.").
func ValidateHostName(name string) (string, error) {
	n := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(name), ".")))
	if n == "" {
		return "", fmt.Errorf("пустое имя")
	}
	if strings.ContainsAny(n, " \t/#") || strings.Contains(n, "..") || strings.HasPrefix(n, ".") {
		return "", fmt.Errorf("кривое имя %q", name)
	}
	return n, nil
}

// ValidateHostIP - валидный IP нужной семьи; пусто = «не задан».
func ValidateHostIP(ip string, v6 bool) (string, error) {
	t := strings.TrimSpace(ip)
	if t == "" {
		return "", nil
	}
	p := net.ParseIP(t)
	if p == nil {
		return "", fmt.Errorf("кривой IP %q", ip)
	}
	if v6 && p.To4() != nil {
		return "", fmt.Errorf("%q - IPv4, а поле для IPv6", ip)
	}
	if !v6 && p.To4() == nil {
		return "", fmt.Errorf("%q - не IPv4", ip)
	}
	return p.String(), nil
}

// ParseKeeneticHosts - записи `ip host`/`ipv6 host` из running-config
// (глобальные; записи пула/интерфейса у ndm не имеют on-формы и выглядят
// так же - читаем все).
func ParseKeeneticHosts(config string) []HostEntry {
	var out []HostEntry
	idx := map[string]int{}
	for _, raw := range strings.Split(config, "\n") {
		line := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		var v6 bool
		var rest string
		switch {
		case strings.HasPrefix(line, "ip host "):
			rest = strings.TrimPrefix(line, "ip host ")
		case strings.HasPrefix(line, "ipv6 host "):
			rest = strings.TrimPrefix(line, "ipv6 host ")
			v6 = true
		default:
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 2 {
			continue
		}
		name, ip := strings.ToLower(fields[0]), fields[1]
		if i, ok := idx[name]; ok {
			if v6 {
				out[i].IPv6 = ip
			} else {
				out[i].IPv4 = ip
			}
			continue
		}
		e := HostEntry{Name: name, Source: "ndm"}
		if v6 {
			e.IPv6 = ip
		} else {
			e.IPv4 = ip
		}
		out = append(out, e)
		idx[name] = len(out) - 1
	}
	return out
}

// HostWant - запись своего списка для применения (без store-зависимости;
// web конвертирует из store.DnsHost). IPv6 в want - только для OpenWrt:
// ndm не имеет ipv6 host, на Keenetic половина игнорируется честно.
type HostWant struct {
	Name string
	IPv4 string
	IPv6 string
}

// ApplyKeeneticHosts - приводит ip host-записи СВОЕГО списка к want
// (prev - прежний список стора: удаляем то, что исчезло/изменилось,
// ставим то, что появилось). Имена сравниваются без регистра, ipv6-
// половина на Keenetic неприменима (ndm не знает ipv6 host). Каждое
// изменение верифицируется чтением running-config.
func ApplyKeeneticHosts(r Runner, want, prev []HostWant) error {
	cfg, err := KeeneticRunningConfig(r)
	if err != nil {
		return fmt.Errorf("running-config: %w", err)
	}
	curByName := map[string]HostEntry{}
	for _, e := range ParseKeeneticHosts(cfg) {
		curByName[strings.ToLower(e.Name)] = e
	}
	wantByName := map[string]HostWant{}
	for _, w := range want {
		wantByName[strings.ToLower(w.Name)] = w
	}
	prevByName := map[string]HostWant{}
	for _, p := range prev {
		prevByName[strings.ToLower(p.Name)] = p
	}
	// удалить: были нашими, но исчезли/изменились в want
	for name := range prevByName {
		w, kept := wantByName[name]
		if kept && strings.EqualFold(w.IPv4, prevByName[name].IPv4) {
			continue
		}
		if _, exists := curByName[name]; !exists {
			continue // уже нет - нечего удалять
		}
		if err := KeeneticHostRemove(r, name); err != nil {
			return err
		}
	}
	// добавить/обновить
	for name, w := range wantByName {
		if cur, exists := curByName[name]; exists && strings.EqualFold(cur.IPv4, w.IPv4) {
			continue
		}
		if err := KeeneticHostAdd(r, name, w.IPv4); err != nil {
			return err
		}
	}
	return nil
}

// KeeneticHostAdd - `ip host <имя> <ip>` + save + верификация чтением
// (правка «его данных» - явное действие в карточке «Локальные записи»).
func KeeneticHostAdd(r Runner, name, ip string) error {
	out, err := r(ndmcCmd("ip host "+name+" "+ip)+" ; "+ndmcCmd("system configuration save"), 30*time.Second)
	if err != nil {
		return fmt.Errorf("ndmc ip host %s: %w: %s", name, err, strings.TrimSpace(out))
	}
	cfg, err := KeeneticRunningConfig(r)
	if err != nil {
		return err
	}
	for _, e := range ParseKeeneticHosts(cfg) {
		if strings.EqualFold(e.Name, name) && strings.EqualFold(e.IPv4, ip) {
			return nil
		}
	}
	return fmt.Errorf("Кинетик не применил ip host %s %s", name, ip)
}

// KeeneticHostRemove - `no ip host <имя>` + save + верификация.
func KeeneticHostRemove(r Runner, name string) error {
	out, err := r(ndmcCmd("no ip host "+name)+" ; "+ndmcCmd("system configuration save"), 30*time.Second)
	if err != nil {
		return fmt.Errorf("ndmc no ip host %s: %w: %s", name, err, strings.TrimSpace(out))
	}
	cfg, err := KeeneticRunningConfig(r)
	if err != nil {
		return err
	}
	for _, e := range ParseKeeneticHosts(cfg) {
		if strings.EqualFold(e.Name, name) {
			return fmt.Errorf("запись ip host %s ещё стоит после no ip host", name)
		}
	}
	return nil
}

// parseHostsFile - строки hosts-файла (имя может быть с несколькими
// адресами на строке и несколькими именами - разбираем посильно:
// комментарий #, поля, адрес -> имена).
func parseHostsFile(content string, source string) []HostEntry {
	var out []HostEntry
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		ip := net.ParseIP(fields[0])
		if ip == nil {
			continue
		}
		for _, name := range fields[1:] {
			name = strings.TrimSuffix(strings.ToLower(name), ".")
			if name == "" {
				continue
			}
			e := HostEntry{Name: name, Source: source}
			if ip.To4() != nil {
				e.IPv4 = ip.String()
			} else {
				e.IPv6 = ip.String()
			}
			out = append(out, e)
		}
	}
	return out
}

// CollectHostsOpenWrt - записи резолвера OpenWrt: /etc/hosts (source
// hosts) + свой файл mawg (source mawg), если подключён.
func CollectHostsOpenWrt(r Runner) ([]HostEntry, error) {
	var out []HostEntry
	if data, err := r("cat /etc/hosts 2>/dev/null", 10*time.Second); err == nil {
		out = append(out, parseHostsFile(data, "hosts")...)
	}
	if data, err := r("cat "+MawgHostsFile+" 2>/dev/null", 10*time.Second); err == nil {
		out = append(out, parseHostsFile(data, "mawg")...)
	}
	return out, nil
}

// ApplyHostsOpenWrt - приводит СВОЙ файл mawg к списку want (строка на
// каждый адрес: IPv4 и IPv6 отдельно) и убеждается, что файл подключён
// к dnsmasq (uci dhcp addnhosts). /etc/hosts не трогаем. Restart
// dnsmasq - каждый раз (файл мог пустеть).
func ApplyHostsOpenWrt(r Runner, want []HostWant) error {
	var b strings.Builder
	for _, w := range want {
		name := strings.ToLower(strings.TrimSpace(w.Name))
		if name == "" {
			continue
		}
		if w.IPv4 != "" {
			b.WriteString(w.IPv4 + " " + name + "\n")
		}
		if w.IPv6 != "" {
			b.WriteString(w.IPv6 + " " + name + "\n")
		}
	}
	script := `mkdir -p /etc/hosts.d
cat > ` + MawgHostsFile + ` <<'MAWGHOSTS'
` + b.String() + `MAWGHOSTS
uci -q del_list dhcp.@dnsmasq[0].addnhosts='` + MawgHostsFile + `'
uci -q add_list dhcp.@dnsmasq[0].addnhosts='` + MawgHostsFile + `'
uci -q commit dhcp
/etc/init.d/dnsmasq restart
`
	out, err := r(script, 60*time.Second)
	if err != nil {
		return fmt.Errorf("addn-hosts: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}

// RemoveHostsLineOpenWrt - удалить из /etc/hosts строку точным
// совпадением (правка «его данных», слово владельца; остальное - нетронутым).
func RemoveHostsLineOpenWrt(r Runner, line string) error {
	line = strings.TrimSpace(line)
	if line == "" || strings.ContainsAny(line, "\r\n") {
		return fmt.Errorf("кривая строка hosts")
	}
	quoted := strings.ReplaceAll(line, "'", `'\'`)
	script := `grep -vxF '` + quoted + `' /etc/hosts > /tmp/hosts.new && mv /tmp/hosts.new /etc/hosts`
	out, err := r(script, 30*time.Second)
	if err != nil {
		return fmt.Errorf("правка /etc/hosts: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}
