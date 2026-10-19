package warp

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"net"
	"strconv"
	"strings"
)

// Эндпоинты данных WARP. Объединение проверенных источников:
//  - LxBox (app/assets/warp_endpoints.json): почти любой адрес блоков
//    ниже на любом порту - живой anycast WARP, DPI режет прежде всего
//    engage...:2408;
//  - warp-generator (script.js generateRandomEndpoint): /24-префиксы
//    8.x с случайным октетом 1-10 и полный пул портов.
// Слово владельца: порты и адреса - любые в рамках того, что есть у
// warp-generator и LxBox; каждый конфиг после создания проверяется
// живьём, нерабочие пары видны честно.

// V4Blocks - CIDR-блоки anycast WARP для ГЕНЕРАЦИИ эндпоинтов.
// 162.159.193.0/24 сюда НЕ входит: по руководству пользователей
// (help-guide.notion.site Cloudflare-WARP, слово владельца) 162.159.193.*
// - только Zero Trust WireGuard, 162.159.197.* - только Zero Trust
// MASQUE; обычная регистрация там не отвечает.
var V4Blocks = []string{
	"162.159.192.0/24",
	"162.159.195.0/24",
	"188.114.96.0/22",
}

// detectOnlyBlocks - блоки для РАСПОЗНАВАНИЯ варп-пулов (детект), но не
// для генерации: Zero Trust-сети.
var detectOnlyBlocks = []string{
	"162.159.193.0/24",
}

// shortPrefixes - /24-префиксы warp-generator (адрес = префикс + октет 1-10).
var shortPrefixes = []string{
	"8.6.112", "8.34.70", "8.34.146", "8.35.211",
	"8.39.125", "8.39.204", "8.39.214", "8.47.69",
}

// DomainEndpoint - штатное доменное имя эндпоинта из ответа регистрации.
const DomainEndpoint = "engage.cloudflareclient.com"

// Ports - основные порты (LxBox ports) + ports_extra (проверенные
// дополнительные порты anycast).
var Ports = []int{
	2408, 500, 1701, 4500,
	854, 859, 864, 878, 880, 890, 891, 894, 903, 908, 928, 934, 939, 942,
	943, 945, 946, 955, 968, 987, 988, 1002, 1010, 1014, 1018, 1070, 1074,
	1180, 1387, 1843, 2371, 2506, 3138, 3476, 3581, 3854, 4177, 4198, 4233,
	5279, 5956, 7103, 7152, 7156, 7281, 7559, 8319, 8742, 8854, 8886,
}

// Presets - фиксированный список проверенных пар (LxBox endpoints_preset):
// выбор «из списка» в диалоге генерации.
var Presets = []string{
	DomainEndpoint + ":2408",
	DomainEndpoint + ":500",
	DomainEndpoint + ":1701",
	DomainEndpoint + ":4500",
	"162.159.192.1:2408",
	"188.114.96.1:2408",
	"162.159.192.192:934",
}

func randInt(n int) int {
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0 // крипто-RAND не падает практически никогда
	}
	return int(v.Int64())
}

// randomHost - случайный адрес: блок anycast (любой IP внутри, кроме
// адреса сети и броадкаста) или /24-префикс warp-generator (октет 1-10,
// как на сайте).
func randomHost() string {
	if randInt(len(V4Blocks)+len(shortPrefixes)) < len(V4Blocks) {
		_, ipnet, err := net.ParseCIDR(V4Blocks[randInt(len(V4Blocks))])
		if err == nil {
			ones, bits := ipnet.Mask.Size()
			hostBits := uint(bits - ones)
			base := binary.BigEndian.Uint32(ipnet.IP.To4())
			mask := uint32(1)<<hostBits - 1
			off := uint32(1 + randInt(1<<hostBits-2))
			ip := make(net.IP, 4)
			binary.BigEndian.PutUint32(ip, base&^mask|off)
			return ip.String()
		}
	}
	return shortPrefixes[randInt(len(shortPrefixes))] + "." + strconv.Itoa(1+randInt(10))
}

// RandomEndpoint - случайная пара адрес:порт из объединённого пула.
func RandomEndpoint() string {
	return randomHost() + ":" + strconv.Itoa(Ports[randInt(len(Ports))])
}

// RandomPairs - n РАЗЛИЧНЫХ пар «адрес:порт» (слово владельца: из
// скольких-то конфигов хоть один жив - если провайдер придавит один
// эндпоинт/порт, выживут остальные). avoid - пары, которые брать нельзя
// (уже есть в пуле).
func RandomPairs(n int, avoid map[string]bool) ([]string, error) {
	out := make([]string, 0, n)
	seen := map[string]bool{}
	for e := range avoid {
		seen[e] = true
	}
	combos := (len(V4Blocks)*1020 + len(shortPrefixes)*10) * len(Ports) // оценка, коллизии редки
	if n > combos {
		return nil, fmt.Errorf("слишком много конфигов для пула различных эндпоинтов: %d", n)
	}
	for len(out) < n {
		e := RandomEndpoint()
		if seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	return out, nil
}

// ValidEndpoint - host:port с хостом-доменом или IP, порт 1-65535.
func ValidEndpoint(e string) error {
	host, port, err := net.SplitHostPort(strings.TrimSpace(e))
	if err != nil {
		return errors.New("эндпоинт должен быть в форме host:port")
	}
	if host == "" {
		return errors.New("эндпоинт без адреса")
	}
	if net.ParseIP(host) == nil {
		for _, label := range strings.Split(host, ".") {
			if label == "" {
				return errors.New("эндпоинт: неверное имя хоста")
			}
		}
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return errors.New("эндпоинт: порт 1-65535")
	}
	return nil
}

// IsWarpEndpointHost - адрес/имя относится к датацентрам WARP: IP из
// anycast-блоков (или /24-префиксов warp-generator), либо домен
// engage.cloudflareclient.com. По этому признаку панель узнаёт варп-пул
// (владелец: кнопка генерации прямо в карточке распознанного пула).
func IsWarpEndpointHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	h = strings.Trim(h, "[]")
	if h == DomainEndpoint {
		return true
	}
	ip := net.ParseIP(h)
	if ip == nil {
		return false
	}
	ip4 := ip.To4()
	if ip4 == nil {
		// IPv6-сети WARP (LxBox v6_cidr): 2606:4700:d0::/64 и d1.
		for _, b := range []string{"2606:4700:d0::/64", "2606:4700:d1::/64"} {
			if contains(b, ip) {
				return true
			}
		}
		return false
	}
	for _, b := range append(append([]string{}, V4Blocks...), detectOnlyBlocks...) {
		if contains(b, ip4) {
			return true
		}
	}
	for _, p := range shortPrefixes {
		if strings.HasPrefix(ip4.String(), p+".") {
			return true
		}
	}
	return false
}

func contains(cidr string, ip net.IP) bool {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return false
	}
	return ipnet.Contains(ip)
}

// IsWarpEndpoint - "host:port" целиком варповый (host-часть).
func IsWarpEndpoint(endpoint string) bool {
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		return IsWarpEndpointHost(endpoint)
	}
	return IsWarpEndpointHost(host)
}
