package web

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"

	"mawg/internal/magitrickle"
	"mawg/internal/store"
)

// Группа «устройство целиком через пул» (слово владельца 2026-10-28):
// один-несколько IP локальной сети, ВЕСЬ их трафик уходит в пул.
//
// Механика без правок форка (43-док): у группы sources=[IP] (src-гвард
// отсекает чужие хосты) + статическое правило subnet 0.0.0.0/0 (любое
// назначение). Доступ к самому роутеру не теряется архитектурно: адреса
// роутера живут в local-таблице маршрутизации (приоритет 0), её смотрят
// РАНЬШЕ fwmark-правила MT - помеченный пакет до роутера всё равно
// доставляется в INPUT.
//
// Приоритет: цепочки групп вшиты в PREROUTING по порядку списка, а MARK
// не терминирует - при совпадении назначений метку перезаписывает
// ПОСЛЕДНЯЯ сработавшая цепочка. Поэтому группа устройства ставится в
// КОНЕЦ списка: её метка побеждает над любыми группами назначений
// (домены/гео) и устройство целиком уходит в свой пул.

// deviceGroupColor - фирменный цвет чипа умного каскада (слово владельца
// 2026-10-25): группа устройства визуально того же семейства.
const deviceGroupColor = "#27d1ce"

const deviceGroupRule = "0.0.0.0/0"

// deviceGroupName - имя группы по умолчанию: «Устройство: <IP>» (+« +N»
// при нескольких адресах).
func deviceGroupName(first string, n int) string {
	if n > 1 {
		return fmt.Sprintf("Устройство: %s +%d", first, n-1)
	}
	return "Устройство: " + first
}

// buildDeviceGroup собирает группу устройства для пула.
func buildDeviceGroup(pool store.Pool, name string, sources []string) magitrickle.Group {
	return magitrickle.Group{
		Name:      name,
		Interface: pool.DeviceName(),
		Color:     deviceGroupColor,
		Enable:    true,
		Sources:   sources,
		Rules: []magitrickle.Rule{
			{Type: "subnet", Rule: deviceGroupRule, Enable: true},
		},
	}
}

// routerAddrConflicts - пересекается ли источник с адресами самого роутера.
// Адрес роутера в группе устройства ЗАПРЕЩЁН: исходящие пакеты туннеля
// (инкапсуляция WireGuard, запросы DNS-каскада) имеют src роутера, пройдут
// src-гвард и получат метку - маршрут по метке ушлёт их обратно в туннель.
// Петля (живой кейс 0.62: 6 млн пакетов за минуты) выжгает CPU и рвёт
// весь egress роутера. Адреса роутера сравниваются как отдельные ХОСТЫ:
// маска интерфейса описывает линк, а не «адреса роутера» (кейс 0.62:
// br0 /24 ложно накрыл всю локалку).
func routerAddrConflicts(src string, routerAddrs []net.IP) bool {
	ip := net.ParseIP(src)
	if ip == nil {
		_, ipnet, err := net.ParseCIDR(src)
		if err != nil {
			return false
		}
		for _, ra := range routerAddrs {
			if ipnet.Contains(ra) {
				return true
			}
		}
		return false
	}
	for _, ra := range routerAddrs {
		if ra.Equal(ip) {
			return true
		}
	}
	return false
}

// routerIPv4s - адреса (IPv4+loopback) всех интерфейсов роутера, хостами.
func routerIPv4s() []net.IP {
	var out []net.IP
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, ifc := range ifaces {
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if v4 := ipnet.IP.To4(); v4 != nil {
				out = append(out, v4)
			}
		}
	}
	return out
}

// mtCreateDeviceGroup - POST /api/v1/mt/device-group {pool, name?, ips[],
// confirm?}: создать группу устройства и поставить её последней
// (сильнейшая позиция). Коллизии адресов (другой пул / глобальные
// исключения) - 409 со списком и подтверждением, не запрет (слово
// владельца 30.10, паттерн петлезащиты DnsView).
func (s *Server) mtCreateDeviceGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Pool    string   `json:"pool"`
		Name    string   `json:"name"`
		IPs     []string `json:"ips"`
		Confirm bool     `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Pool == "" {
		writeErr(w, fmt.Errorf("нужен pool и хотя бы один IP устройства"))
		return
	}
	pool, ok := s.store.Pool(req.Pool)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if pool.Disabled {
		writeErr(w, fmt.Errorf("пул %s выключен - включите его, иначе устройство останется без интернета (blackhole)", pool.Name))
		return
	}
	sources, err := normalizeSources(req.IPs)
	if err != nil {
		writeErr(w, err)
		return
	}
	if len(sources) == 0 {
		writeErr(w, fmt.Errorf("нужен хотя бы один IP устройства (например 192.168.1.50; можно подсеть 192.168.1.0/24)"))
		return
	}
	// адрес роутера нельзя: петля туннеля (см. routerAddrConflicts)
	routerAddrs := routerIPv4s()
	for _, src := range sources {
		if routerAddrConflicts(src, routerAddrs) {
			writeErr(w, fmt.Errorf("%s - это адрес самого роутера: его трафик в пул заводить нельзя (инкапсуляция туннеля замкнётся на себя). Укажите адрес устройства локальной сети - телефон, ноутбук", src))
			return
		}
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = deviceGroupName(sources[0], len(sources))
	}
	// коллизии адресов между пулами и с глобальными исключениями
	// (97-док): 409 + подтверждение, предупреждаем именами пулов
	conflicts := findBindingConflicts(s.store.DeviceBindings(), s.store.Excludes(), pool.Name, sources)
	if len(conflicts) > 0 && !req.Confirm {
		bindingConflicts409(w, conflicts)
		return
	}
	if len(conflicts) > 0 {
		s.store.LogEvent("rules", "device-bindings", fmt.Sprintf(
			"коллизия адресов подтверждена вручную: %s -> пул %s", strings.Join(sources, ", "), pool.Name))
	}
	client := s.mtClient()
	if err := client.EnsureHealthy(r.Context()); err != nil {
		writeErr(w, err)
		return
	}
	// дубль имени не создаём (панель показывает группы по имени)
	groups, err := client.GroupsWithRules(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	for _, g := range groups {
		if g.Name == name {
			writeErr(w, fmt.Errorf("группа %q уже есть - дайте другое имя", name))
			return
		}
	}
	g := buildDeviceGroup(pool, name, sources)
	// AddGroup = append в конец списка одним bulk PUT в защитном окне
	if err := s.engine.RetryMTWrite("panel", func() error {
		return client.AddGroup(r.Context(), g)
	}); err != nil {
		writeErr(w, err)
		return
	}
	client.RefreshShadow(r.Context())
	// зеркало связки в реестре (97-док): карточка пула и коллизии
	s.deviceBindingUpsert(pool.Name, name, sources)
	s.store.LogEvent("rules", "magitrickle", fmt.Sprintf(
		"группа устройства %q: весь трафик %d адрес(ов) через пул %s (%s)", name, len(sources), pool.Name, g.Interface))
	// созданную группу (с id от MT) возвращаем перечитыванием
	if groups, err = client.GroupsWithRules(r.Context()); err == nil {
		for _, ng := range groups {
			if ng.Name == name {
				writeJSON(w, http.StatusOK, ng)
				return
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "created"})
}

// deviceGroupOf - выглядит ли группа как «устройство целиком» (для бейджа
// в списке групп: sources непусты и есть статика 0.0.0.0/0).
func deviceGroupOf(g magitrickle.Group) bool {
	if len(g.Sources) == 0 {
		return false
	}
	for _, r := range g.Rules {
		if r.Type == "subnet" && r.Rule == deviceGroupRule && r.Enable {
			return true
		}
	}
	return false
}

// ipHint - краткая подсказка валидации адреса для UI-диалога.
func ipHint(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if ip := net.ParseIP(s); ip != nil {
		return ""
	}
	if _, _, err := net.ParseCIDR(s); err == nil {
		return ""
	}
	return "не IP и не подсеть"
}
