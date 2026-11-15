package wgconf

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
)

// Мультипул одного провайдера (120-док, слово владельца 15.11): пулы
// разводятся разными локальными адресами подсети провайдера - Proton
// живьём принимает 10.2.0.3/32 и 10.2.0.4/32, WARP 172.16.0.5/32.
// Здесь: опознание провайдера, инкремент адреса, переписывание Address
// в тексте конфига.

// Provider - провайдер локальной адресации конфига. "" - не опознан:
// поведение пула без изменений (premium/free и прочие не трогаем).
type Provider string

const (
	ProviderProton Provider = "proton"
	ProviderWARP   Provider = "warp"
)

// Подсети локальных адресов. Proton: живые конфиги владельца
// (local-docs/fixtures-proton) несут Address 10.2.0.2/32 + DNS 10.2.0.1,
// а эндпоинты - ГОЛЫЕ IP серверов (89.39.107.113 и пр., домена
// protonvpn.net в конфигах нет) - детект только по паре «Address в /24
// + DNS провайдера», не по endpoint. WARP: 172.16.0.x из ответа
// регистрации (владелец: 172.16.0.5 живьём), DNS в конфигах нет -
// детект по подсети.
const protonDNS = "10.2.0.1"

var (
	protonV4Net = mustProviderNet("10.2.0.0/24")
	warpV4Net   = mustProviderNet("172.16.0.0/24")
)

func mustProviderNet(cidr string) *net.IPNet {
	_, n, err := net.ParseCIDR(cidr)
	if err != nil {
		panic("константа подсети провайдера: " + err.Error())
	}
	return n
}

// DetectProvider - кто это по локальной адресации. Один признак - ещё не
// вердикт: 10.2.0.0/24 - обычный приватный диапазон, Protonом его делает
// только пара с его DNS; у WARP наоборот, DNS нет - достаточно подсети.
func DetectProvider(cfg Config) Provider {
	v4 := cfg.FirstIPv4()
	if v4 == "" {
		return ""
	}
	ip := net.ParseIP(strings.SplitN(v4, "/", 2)[0]).To4()
	if ip == nil {
		return ""
	}
	if protonV4Net.Contains(ip) {
		for _, d := range cfg.DNS {
			if strings.SplitN(d, "/", 2)[0] == protonDNS {
				return ProviderProton
			}
		}
		return ""
	}
	if warpV4Net.Contains(ip) {
		return ProviderWARP
	}
	return ""
}

// ProviderSubnet - подсеть локальных адресов провайдера (подсказка базы
// перенумерации, варнинг «адрес вне подсети»), пусто у неопознанного.
func ProviderSubnet(p Provider) string {
	switch p {
	case ProviderProton:
		return "10.2.0.0/24"
	case ProviderWARP:
		return "172.16.0.0/24"
	}
	return ""
}

// IncV4 - последний октет +n (подсети провайдеров /24): 10.2.0.2 +2 ->
// 10.2.0.4. Выход за 255 - «подсеть кончилась», дальше вести некуда.
func IncV4(addr string, n int) (string, error) {
	ip := net.ParseIP(strings.SplitN(strings.TrimSpace(addr), "/", 2)[0]).To4()
	if ip == nil {
		return "", fmt.Errorf("адрес %q - не IPv4", addr)
	}
	last := int(ip[3]) + n
	if last > 255 {
		return "", fmt.Errorf("в /24 не хватает свободных адресов: %s +%d", addr, n)
	}
	ip[3] = byte(last)
	return ip.String(), nil
}

// protonV6Re - v6 Proton зеркалит хвост v4: 10.2.0.2/32 живёт в паре с
// 2a07:b944::2:2/128 (конфиги владельца 15.11). Два пула с одним v6
// конфликтуют так же, как с одним v4, - при смене v4 хвост правится
// синхронно.
var protonV6Re = regexp.MustCompile(`^(2a07:b944::2:)([0-9]+)(/\d+)?$`)

// SetAddress - переписывает локальный v4-адрес в ТЕКСТЕ конфига плоской
// правкой строк (ключи, обфускация, I1-блобы остаются байт-в-байт -
// пересборка конфига их может потерять). newAddr - «10.2.0.3» или
// «10.2.0.3/32»; без префикса сохраняется прежний. v6 Proton
// синхронизируется с новым v4-хвостом.
func SetAddress(raw []byte, newAddr string) ([]byte, error) {
	addrPart := strings.TrimSpace(newAddr)
	prefix := ""
	if i := strings.Index(addrPart, "/"); i >= 0 {
		prefix = "/" + addrPart[i+1:]
		addrPart = addrPart[:i]
	}
	ip := net.ParseIP(addrPart).To4()
	if ip == nil {
		return nil, fmt.Errorf("локальный адрес %q - не IPv4", newAddr)
	}
	changed := false
	inIface := false
	out := make([]string, 0, 24)
	for _, line := range strings.Split(string(raw), "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "[") && strings.HasSuffix(trim, "]") {
			inIface = strings.EqualFold(trim, "[interface]")
			out = append(out, line)
			continue
		}
		eq := strings.Index(trim, "=")
		if !inIface || eq < 0 || !strings.HasPrefix(strings.ToLower(trim), "address") {
			out = append(out, line)
			continue
		}
		parts := strings.Split(trim[eq+1:], ",")
		for i, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if !strings.Contains(part, ":") {
				// v4: IP заменяем, префикс - новый или прежний
				pfx := prefix
				if pfx == "" {
					if old := strings.SplitN(part, "/", 2); len(old) == 2 {
						pfx = "/" + old[1]
					}
				}
				parts[i] = ip.String() + pfx
				changed = true
				continue
			}
			if m := protonV6Re.FindStringSubmatch(part); m != nil {
				pfx := ""
				if s := strings.SplitN(part, "/", 2); len(s) == 2 {
					pfx = "/" + s[1]
				}
				parts[i] = m[1] + strconv.Itoa(int(ip[3])) + pfx
			}
		}
		out = append(out, trim[:eq]+"= "+strings.Join(parts, ", "))
	}
	if !changed {
		return nil, fmt.Errorf("в [Interface] нет Address с IPv4 - менять нечего")
	}
	return []byte(strings.Join(out, "\n")), nil
}
