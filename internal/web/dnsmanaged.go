package web

// «Маршрутизация DNS» - переключатель роли резолвера (115-док, слово
// владельца 09.11 второе, разграничение ролей MagiTrickle):
//   - ловить DNS-запросы устройств и по ним вести трафик (ipset-ы
//     доменных групп) - СУТЬ MagiTrickle, не отключается никогда
//     (MT_DNSOR живёт в обоих положениях переключателя);
//   - «MT сам отправляет куда настроено» - это маршрутизация DNS: MT
//     отвечает через свой каскад («Резервные серверы») и зоны. Её и
//     переключаем: false = апстримы форка подменяются на 127.0.0.1:53
//     (резолвер роутера - ip host/hosts видны устройствам), таблица зон
//     форка приостанавливается (стор цел), на OpenWrt dnsmasq уходит на
//     прямые серверы (иначе петля dnsmasq->MT->dnsmasq). Наблюдение не
//     отключается: пробы/статусы/журнал показывают, куда что пошло.
//
// Флаг в Settings трёхзначный (nil = MT отвечает, живые установки не
// меняют поведение). Список «Резервных серверов» на момент выключения
// сохраняется в Settings.DnsSavedUpstreams - он жил только в конфиге
// форка; зоны при ресторе повторно применяются из стора (Store.DnsZones).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"mawg/internal/dnsmgr"
	"mawg/internal/magitrickle"
	"mawg/internal/store"
)

// routerUpstream - адрес резолвера роутера для подмены апстримов форка:
// ndnproxy (Keenetic) / dnsmasq (OpenWrt) на 127.0.0.1:53. Петли нет:
// ndm loopback-апстримы не принимает вовсе, dnsmasq в момент выключения
// уводится на прямые серверы.
const routerUpstream = "127.0.0.1:53"

type dnsRoutingView struct {
	// Routing - nil = MT отвечает (дефолт); false = отвечает роутер.
	Routing *bool  `json:"routing"`
	Flavor  string `json:"mtFlavor,omitempty"`
	Remap53 bool   `json:"remap53"`
	// Platform - "openwrt" | "keenetic": карточка ветвит подсказку.
	Platform string `json:"platform"`
}

func (s *Server) dnsRoutingView() dnsRoutingView {
	v := dnsRoutingView{Routing: s.store.DnsRouting(), Platform: s.backend.Name()}
	if c := s.casc(); c != nil {
		v.Flavor = string(c.BackendFlavor())
	}
	if run, ok := s.dnsRunner(); ok {
		var st dnsmgr.State
		var err error
		if s.backend.Name() == store.PlatformKeenetic {
			st, err = dnsmgr.CollectKeenetic(run, s.backend.MTConfigPath())
		} else {
			st, err = dnsmgr.Collect(run, s.backend.MTConfigPath())
		}
		if err == nil {
			v.Remap53 = st.MT.Remap53
		}
	}
	return v
}

func (s *Server) dnsManagedGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.dnsRoutingView())
}

// dnsManagedSet - {"routing": true|false, "confirm": false}. Выключение
// без confirm - честный 409 (подмена апстримов живьём, зоны уходят).
func (s *Server) dnsManagedSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Routing bool `json:"routing"`
		Confirm bool `json:"confirm"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeErr(w, fmt.Errorf("кривой запрос"))
		return
	}
	prev := s.store.DnsRouting()
	if !req.Routing && !req.Confirm {
		routerDNS := s.routerDNSName()
		writeJSON(w, http.StatusConflict, map[string]any{
			"needConfirm": true,
			"error": "MagiTrickle продолжит ловить DNS-запросы и вести трафик по группам - это не отключается. " +
				"Снимается только его роль резолвера: отвечать будет " + routerDNS + " (локальные записи заработают), " +
				"каскад «Резервных серверов» и зоны приостановятся и вернутся при включении. Пробы и статусы останутся.",
		})
		return
	}
	if req.Routing {
		if prev == nil || *prev {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "state": s.dnsRoutingView()})
			return
		}
		if err := s.applyRouterUpstream(false); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if err := s.store.SetDnsRouting(nil); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if err := s.store.SetDnsSavedUpstreams(nil); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		s.store.LogEvent("dns", "routing", "маршрутизация DNS возвращена: отвечает каскад MagiTrickle")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "state": s.dnsRoutingView()})
		return
	}
	// выключение: MT перестаёт отвечать, резолвером становится роутер
	if c := s.casc(); c != nil && c.BackendFlavor() == magitrickle.FlavorMtC {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "бэкенд MT - mt-c: подмена апстримов не поддерживается (конфиг другой)"})
		return
	}
	saved := s.currentMTUpstreams()
	if err := s.store.SetDnsSavedUpstreams(saved); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := s.suspendZones(r); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	if run, ok := s.dnsRunner(); ok && s.backend.Name() != store.PlatformKeenetic {
		// dnsmasq уходит на прямые серверы ДО подмены апстримов форка:
		// MT -> 127.0.0.1:53 = dnsmasq, и если dnsmasq смотрит в MT - петля
		if st, err := dnsmgr.Collect(run, ""); err == nil && st.Dnsmasq.InMT {
			if err := dnsmgr.SetDnsmasqTarget(run, "direct"); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "dnsmasq: " + err.Error()})
				return
			}
		}
	}
	if err := s.applyRouterUpstream(true); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := s.store.SetDnsRouting(boolPtr(false)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.store.LogEvent("dns", "routing", "маршрутизация DNS выключена: отвечает "+s.routerDNSName()+", MagiTrickle продолжает ловить запросы и вести трафик; список «Резервных» сохранён")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "state": s.dnsRoutingView()})
}

// routerDNSName - как назвать резолвер роутера в текстах (словарь §0
// 115-дока): ndm-резолвер Keenetic / dnsmasq OpenWrt.
func (s *Server) routerDNSName() string {
	if s.backend.Name() == store.PlatformKeenetic {
		return "DNS Keenetic"
	}
	return "DNS роутера"
}

// currentMTUpstreams - живой список апстримов форка (для сохранения):
// rev8+ стейт из памяти MT, фолбэк - дисковый конфиг. Loopback уже в
// составе не считаем «пользовательским» списком - вернём пустой.
func (s *Server) currentMTUpstreams() []string {
	var out []string
	if st, err := s.mtClient().DnsUpstreams(rContext()); err == nil && st != nil {
		for _, u := range st.Upstreams {
			if u.Address != "" && u.Address != routerUpstream {
				out = append(out, u.Address)
			}
		}
		return out
	}
	path := s.backend.MTConfigPath()
	if path == "" {
		return nil
	}
	if data, err := os.ReadFile(path); err == nil {
		_, list := dnsmgr.ParseMTUpstreams(string(data))
		for _, a := range list {
			if a != routerUpstream {
				out = append(out, a)
			}
		}
	}
	return out
}

// suspendZones - таблица зон форка очищается (стор цел, рестор из него).
// Зоны без rev13 - пропускаем молча: их и нет.
func (s *Server) suspendZones(r *http.Request) error {
	if len(s.store.DnsZones()) == 0 || !s.mtDnsZonesCapable(r.Context()) {
		return nil
	}
	if _, err := s.applyDnsZones(r.Context(), nil); err != nil {
		return fmt.Errorf("зоны: %w", err)
	}
	s.store.LogEvent("dns", "routing", "зоны приостановлены (таблица форка очищена, вернутся при включении маршрутизации DNS)")
	return nil
}

// applyRouterUpstream - подмена апстримов форка: router - на резолвер
// роутера, restore - обратно. Восстановление: включённая политика
// каскада переконфигурируется из неё (композит + dns-tunnel), иначе -
// сохранённый список; пусто = резолвер роутера остаётся (честно в журнал).
func (s *Server) applyRouterUpstream(router bool) error {
	client := s.mtClient()
	if router {
		if err := client.SetDnsUpstreams(rContext(), []string{routerUpstream}, nil); err != nil {
			return fmt.Errorf("подмена апстримов MT: %w", err)
		}
		return nil
	}
	var addrs []string
	if p := s.store.DNSCascade(); p != nil && p.Enabled {
		composed, err := composeCascadePolicy(*p)
		if err != nil {
			return fmt.Errorf("политика каскада: %w", err)
		}
		if p.Tunnel != "" {
			ips, failed := upstreamHostIPs(rContext(), composed)
			for _, h := range failed {
				s.store.LogEvent("dns", "routing", "адрес "+h+" не разрезолвился для статики dns-tunnel (верну без него)")
			}
			if _, err := s.ensureDnsTunnelGroup(rContext(), p.Tunnel, ips); err != nil {
				return fmt.Errorf("dns-tunnel: %w", err)
			}
		}
		addrs = composed
	} else {
		addrs = s.store.DnsSavedUpstreams()
	}
	if len(addrs) == 0 {
		s.store.LogEvent("dns", "routing", "сохранённого списка «Резервных серверов» нет - апстримом остаётся резолвер роутера; задайте список в панели")
		return nil
	}
	if err := client.SetDnsUpstreams(rContext(), addrs, nil); err != nil {
		return fmt.Errorf("возврат апстримов MT: %w", err)
	}
	return nil
}

// dnsManagedGate - PUT-ы DNS-раздела при выключенной маршрутизации DNS:
// править каскад/зоны/политику/dnsmasq/ndm нельзя (их ответы всё равно не
// используются). false = ответ уже записан (409).
// dnsmasqMTLoopRisk - направить dnsmasq в MT опасно, когда MT отвечает
// через резолвер роутера (апстрим 127.0.0.1:53) и health его пробу валит
// (петля dnsmasq → MT → dnsmasq уже кусается; живой кейс 0.62 09.11:
// историческая форма «dnsmasq в MT» маскировалась параллельным ISP-
// апстримом dnsmasq, явный список включил health-пробы - и петля стала
// видимой и сломала DNS устройств). Рестор из dead - хистерезис форка.
func (s *Server) dnsmasqMTLoopRisk() bool {
	st, err := s.mtClient().DnsUpstreams(rContext())
	if err != nil || st == nil {
		return false // MT молчит - нечего утверждать, пусть механика покажет
	}
	for _, u := range st.Upstreams {
		if u.Address == routerUpstream && !u.Alive {
			return true
		}
	}
	return false
}

func (s *Server) dnsManagedGate(w http.ResponseWriter) bool {
	if v := s.store.DnsRouting(); v != nil && !*v {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "маршрутизация DNS выключена (отвечает " + s.routerDNSName() + "): правки раздела сейчас не используются - включите «Маршрутизацию DNS» в карточке DNS",
		})
		return false
	}
	return true
}

// rContext - фоновый контекст для клиентских вызовов MT вне HTTP-запроса.
func rContext() context.Context { return context.Background() }

func boolPtr(v bool) *bool { return &v }
