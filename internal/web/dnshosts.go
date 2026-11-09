package web

// Карточка «Локальные записи» (115-док, слово владельца 09.11 третье):
// свой список хостов (IPv4/IPv6) + чтение записей резолвера роутера
// (/etc/hosts OpenWrt, ip host Keenetic) с правкой и удалением, проба
// каждой записи через путь устройств (dns-proxy MT :3553 - то, что
// реально видят устройства при любом положении «Маршрутизации DNS»).
//
// Свои записи применяются в резолвер роутера: Keenetic - ip host через
// ndmc (ipv6-половина неприменима - ndm не знает ipv6 host, честно
// показываем); OpenWrt - свой файл /etc/hosts.d/mawg из addn-hosts
// dnsmasq (/etc/hosts не трогается, его записи только показываются и
// удаляются по явному действию). Проба: dnsprobe.Lookup на :3553 -
// «видна на пути устройств / не видна» - прозрачность без догадок.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"mawg/internal/dnsmgr"
	"mawg/internal/dnsprobe"
	"mawg/internal/store"
)

// hostProbeServer - путь устройств: dns-proxy MT отвечает устройствам
// через DNAT на :3553, так что проба записи на 127.0.0.1:3553 показывает
// ровно то, что увидит устройство.
const hostProbeServer = "127.0.0.1:3553"

// hostRouterServer - резолвер роутера (ndnproxy Keenetic / dnsmasq
// OpenWrt): проба на 127.0.0.1:53 показывает, что роутер знает сам
// (ip host/hosts). Совместно с пробой пути устройств атрибутирует
// ответ: «видна», «видит только роутер» (на пути устройств внешний
// каскад - живой кейс владельца: ntc.party vs github-пиновки, чьи IP
// совпадают с публичными), «видна, но мимо роутера».
const hostRouterServer = "127.0.0.1:53"

func (s *Server) dnsHostsReady(w http.ResponseWriter) (dnsmgr.Runner, bool) {
	run, ok := s.dnsRunner()
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "платформа без шелла"})
		return nil, false
	}
	return run, true
}

// dnsHostsGet - GET /dns/hosts: записи резолвера роутера + свой список
// из стора (каждая своя помечена "own": true в router-выборке, чтобы
// панель показывала «это ваше» прямо в таблице роутера).
func (s *Server) dnsHostsGet(w http.ResponseWriter, r *http.Request) {
	run, ok := s.dnsHostsReady(w)
	if !ok {
		return
	}
	isKeen := s.backend.Name() == store.PlatformKeenetic
	var router []dnsmgr.HostEntry
	if isKeen {
		cfg, err := dnsmgr.KeeneticRunningConfig(run)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		router = dnsmgr.ParseKeeneticHosts(cfg)
	} else {
		var err error
		router, err = dnsmgr.CollectHostsOpenWrt(run)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	ownByName := map[string]bool{}
	for _, h := range s.store.DnsHosts() {
		ownByName[strings.ToLower(h.Name)] = true
	}
	type routerView struct {
		dnsmgr.HostEntry
		Own bool `json:"own"`
	}
	out := make([]routerView, 0, len(router))
	for _, e := range router {
		_, isOwn := ownByName[strings.ToLower(e.Name)]
		out = append(out, routerView{HostEntry: e, Own: isOwn})
	}
	// стабильный порядок: по имени
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, http.StatusOK, map[string]any{
		"platform": s.backend.Name(),
		"router":   out,
		"own":      s.store.DnsHosts(),
		// Keenetic: ipv6-половина своих записей неприменима (ndm не знает
		// ipv6 host, проверено 09.11) - панель честно показывает это
		"v6Capable": !isKeen,
	})
}

// dnsHostsSet - PUT /dns/hosts {hosts}: заменить свой список целиком и
// применить diff в резолвер роутера (Keenetic: ndmc ip host; OpenWrt:
// файл /etc/hosts.d/mawg + dnsmasq restart).
func (s *Server) dnsHostsSet(w http.ResponseWriter, r *http.Request) {
	run, ok := s.dnsHostsReady(w)
	if !ok {
		return
	}
	var req struct {
		Hosts []store.DnsHost `json:"hosts"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(&req); err != nil {
		writeErr(w, fmt.Errorf("кривой запрос"))
		return
	}
	want, err := validateDnsHosts(req.Hosts)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if s.backend.Name() == store.PlatformKeenetic {
		if err := dnsmgr.ApplyKeeneticHosts(run, hostsToWant(want), hostsToWant(s.store.DnsHosts())); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	} else {
		if err := dnsmgr.ApplyHostsOpenWrt(run, hostsToWant(want)); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	if err := s.store.SetDnsHosts(want); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.store.LogEvent("dns", "hosts", fmt.Sprintf("свой список локальных записей применён (%d шт.)", len(want)))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "own": want})
}

// dnsHostsRouterSet - POST /dns/hosts/router: правка «его данных» -
// записи резолвера роутера мимо своего списка: {"op":"set"|"del",
// "name","ipv4","ipv6"}. Keenetic: ndmc ip host (set/del); OpenWrt:
// только del строки /etc/hosts точным совпадением (set своих - через
// PUT /dns/hosts, чужой файл не пишем).
func (s *Server) dnsHostsRouterSet(w http.ResponseWriter, r *http.Request) {
	run, ok := s.dnsHostsReady(w)
	if !ok {
		return
	}
	var req struct {
		Op   string `json:"op"`
		Name string `json:"name"`
		IPv4 string `json:"ipv4"`
		IPv6 string `json:"ipv6"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeErr(w, fmt.Errorf("кривой запрос"))
		return
	}
	name, err := dnsmgr.ValidateHostName(req.Name)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	isKeen := s.backend.Name() == store.PlatformKeenetic
	switch req.Op {
	case "del":
		if isKeen {
			if err := dnsmgr.KeeneticHostRemove(run, name); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
		} else {
			line := req.IPv4 + " " + name
			if req.IPv6 != "" && req.IPv4 == "" {
				line = req.IPv6 + " " + name
			}
			if err := dnsmgr.RemoveHostsLineOpenWrt(run, line); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
		}
		s.store.LogEvent("dns", "hosts", "запись резолвера роутера удалена: "+name)
	case "set":
		if !isKeen {
			writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "правка /etc/hosts не поддерживается: добавляйте записи в «Свои» - они применяются в свой файл dnsmasq"})
			return
		}
		ip, err := dnsmgr.ValidateHostIP(req.IPv4, false)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if ip == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "нужен IPv4: ndm хранит ip host только с адресом"})
			return
		}
		if err := dnsmgr.KeeneticHostAdd(run, name, ip); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		s.store.LogEvent("dns", "hosts", "запись Keenetic обновлена: "+name+" -> "+ip)
	default:
		writeErr(w, fmt.Errorf("op: set|del"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// dnsHostsProbe - GET /dns/hosts/probe?name=X: проба записи по ДВУМ
// путям: устройство (127.0.0.1:3553 - dns-proxy MT, то, что реально
// увидит устройство) и резолвер роутера (127.0.0.1:53 - ip host/hosts).
// Разница атрибутирует ответ: совпадение на обоих - «видна»; только у
// роутера - «видит только роутер» (на пути устройств внешний каскад);
// только на пути - «мимо роутера» (совпадение с публичным IP).
func (s *Server) dnsHostsProbe(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		writeErr(w, fmt.Errorf("нужен name"))
		return
	}
	deviceA, deviceAAAA, devErr := dnsprobe.Lookup(hostProbeServer, name, 3*time.Second)
	routerA, routerAAAA, rtrErr := dnsprobe.Lookup(hostRouterServer, name, 3*time.Second)
	if devErr != nil && rtrErr != nil {
		writeJSON(w, http.StatusOK, map[string]any{"name": name, "error": devErr.Error()})
		return
	}
	res := map[string]any{"name": name}
	if devErr != nil {
		res["deviceError"] = devErr.Error()
	} else {
		res["device"] = map[string]any{"ipv4": deviceA, "ipv6": deviceAAAA}
	}
	if rtrErr != nil {
		// резолвер роутера молчит (OpenWrt-профиль без параллельного
		// апстрима) - не ошибка всей пробы: путь устройств показан
		res["routerError"] = rtrErr.Error()
	} else {
		res["router"] = map[string]any{"ipv4": routerA, "ipv6": routerAAAA}
	}
	writeJSON(w, http.StatusOK, res)
}

// validateDnsHosts - валидация+канонизация своего списка: имена доменные
// и уникальные, IPv4/IPv6 корректные, хотя бы один адрес на запись.
func validateDnsHosts(list []store.DnsHost) ([]store.DnsHost, error) {
	if len(list) > 256 {
		return nil, fmt.Errorf("записей больше 256")
	}
	out := make([]store.DnsHost, 0, len(list))
	seen := map[string]bool{}
	for i, h := range list {
		name, err := dnsmgr.ValidateHostName(h.Name)
		if err != nil {
			return nil, fmt.Errorf("запись %d: %w", i+1, err)
		}
		if seen[name] {
			continue // дубликаты схлопываем
		}
		seen[name] = true
		v4, err := dnsmgr.ValidateHostIP(h.IPv4, false)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		v6, err := dnsmgr.ValidateHostIP(h.IPv6, true)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if v4 == "" && v6 == "" {
			return nil, fmt.Errorf("%s: нужен IPv4 или IPv6", name)
		}
		id := h.ID
		if id == "" {
			id = fmt.Sprintf("h%08x", fnv32a(name))
		}
		out = append(out, store.DnsHost{ID: id, Name: name, IPv4: v4, IPv6: v6})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// hostsToWant - стор -> параметры применения.
func hostsToWant(list []store.DnsHost) []dnsmgr.HostWant {
	out := make([]dnsmgr.HostWant, 0, len(list))
	for _, h := range list {
		out = append(out, dnsmgr.HostWant{Name: h.Name, IPv4: h.IPv4, IPv6: h.IPv6})
	}
	return out
}
