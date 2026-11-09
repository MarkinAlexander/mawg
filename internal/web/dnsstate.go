package web

// Хендлеры/логика состояния DNS (из server.go).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"mawg/internal/dnsmgr"
	"mawg/internal/dnsprobe"
	"mawg/internal/magitrickle"
	"mawg/internal/platform"
	"mawg/internal/store"
)

// dnsProbe - зонд DNS-уровней канарейкой (63-док): A-запрос
// deb.torproject.org через каждый уровень и сверка с каноническим набором.
// По умолчанию - стандартный набор (dnsmasq/MT/апстрим/DoH/DoT);
// ?kind=udp&server=host:port[&sni=...] - один уровень (отладка/тесты).
func (s *Server) dnsProbe(w http.ResponseWriter, r *http.Request) {
	// переключатель пути проверки (слово владельца: «чтобы пользователь
	// видел работает или нет, помогает или нет»): all - уровни политики
	// ПЕРВЫМИ (путь устройств через MT, «рабочий уровень» отвечает про
	// него) + прямые проверки как диагностика «сервер сам по себе»;
	// direct - только прямой путь; mt - только цепочка политики
	// (политика выключена - пусто, клиент покажет хинт)
	path := r.URL.Query().Get("path")
	policyLevels := func() []dnsprobe.Level {
		if p := s.store.DNSCascade(); p != nil && p.Enabled {
			return policyProbeLevels(*p)
		}
		return nil
	}
	var levels []dnsprobe.Level
	switch path {
	case "direct":
		levels = dnsprobe.Defaults()
	case "mt":
		levels = policyLevels()
	default:
		levels = append(policyLevels(), dnsprobe.Defaults()...)
	}
	// свои серверы пользователя (слово владельца: «свои списки для
	// проверок, dot, doh, udp») - наравне со штатными уровнями (в режиме
	// mt не добавляем: это проверка пути политики)
	if path != "mt" {
		for _, cs := range s.store.DNSProbeServers() {
			if l, err := convertProbeServer(cs); err == nil {
				levels = append(levels, l)
			}
		}
	}
	kind, server := r.URL.Query().Get("kind"), r.URL.Query().Get("server")
	if kind != "" && server != "" {
		levels = []dnsprobe.Level{{Level: server, Kind: kind, Server: server, SNI: r.URL.Query().Get("sni")}}
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": dnsprobe.Run(levels, 3*time.Second)})
}

func (s *Server) dnsRunner() (dnsmgr.Runner, bool) {
	if sr, ok := s.backend.(platform.ShellRunner); ok {
		return sr.RunShell, true
	}
	return nil, false
}

// dnsState - состояние DNS-стека роутера (46-док §3): openwrt - uci +
// iptables + remap53; keenetic - только факт перехвата MT_DNSOR и
// апстримы MT (71-док, дизайн-слово владельца: собственные серверы
// Keenetic панель не трогает).
func (s *Server) dnsState(w http.ResponseWriter, r *http.Request) {
	run, ok := s.dnsRunner()
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "платформа пока без DNS-менеджера"})
		return
	}
	var st dnsmgr.State
	var err error
	if s.backend.Name() == store.PlatformKeenetic {
		st, err = dnsmgr.CollectKeenetic(run, s.backend.MTConfigPath())
	} else {
		st, err = dnsmgr.Collect(run, s.backend.MTConfigPath())
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// dnsDnsmasq - переключение dnsmasq: {"mode":"mt"} - в MT:3553 («живучий»
// профиль, 63-док: noresolv/cachesize/resolvfile не трогаются, прядевой
// список прячется в uci), {"mode":"direct"} - рестор обратно.
func (s *Server) dnsDnsmasq(w http.ResponseWriter, r *http.Request) {
	run, ok := s.dnsRunner()
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "платформа пока без DNS-менеджера (срез OpenWrt)"})
		return
	}
	if s.backend.Name() == store.PlatformKeenetic {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "на Keenetic служба DNS не переключается: панель не трогает собственные серверы Keenetic (управление - «Резервные серверы» MagiTrickle)"})
		return
	}
	var req struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeErr(w, fmt.Errorf("кривой запрос"))
		return
	}
	if !s.dnsManagedGate(w) {
		return
	}
	if req.Mode != "mt" && req.Mode != "direct" {
		writeErr(w, fmt.Errorf("mode: mt|direct"))
		return
	}
	if req.Mode == "mt" && s.dnsmasqMTLoopRisk() {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "замкнёт петлю: MagiTrickle сейчас отвечает через резолвер роутера (127.0.0.1:53), и его проверка уже падает - направить dnsmasq в MT значит зациклить dnsmasq → MT → dnsmasq. Верните dnsmasq на прямые серверы (он уже там) или задайте внешний список в «Резервных серверах»",
		})
		return
	}
	if err := dnsmgr.SetDnsmasqTarget(run, req.Mode); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	st, err := dnsmgr.Collect(run, s.backend.MTConfigPath())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "state": st})
}

// dnsKeeneticOwn - собственные DNS-серверы Keenetic (только просмотр,
// 71-док: панель их не меняет): DoH/DoT секции dns-proxy и простые
// ip/ipv6 name-server по подключения - из running-config через ndmc.
// Вердикты «работает?» даёт GET /dns/probe?kind=&server= по каждому.
func (s *Server) dnsKeeneticOwn(w http.ResponseWriter, r *http.Request) {
	if s.backend.Name() != store.PlatformKeenetic {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "собственные DNS-серверы есть только на Keenetic"})
		return
	}
	run, ok := s.dnsRunner()
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "платформа без шелла"})
		return
	}
	own, err := dnsmgr.CollectKeeneticOwn(run)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, own)
}

// dnsUpstreams - состояние каскада апстримов dns-proxy MT (форк rev8+,
// 64-док §6): живой стейт менеджера (alive/active/причины) через прокси
// в MT. MT без rev8 (Кинетик rev4, стенд до апгрейда) - честный
// {"capable":false}: панель показывает апстримы из config.yaml и хинт.
// healthCapable (rev14+) - отдельный флаг: редактор политики зондов
// показывается только там, где форк умеет её принимать.
func (s *Server) dnsUpstreams(w http.ResponseWriter, r *http.Request) {
	if !s.mtDnsUpstreamsCapable(r.Context()) {
		writeJSON(w, http.StatusOK, map[string]any{"capable": false})
		return
	}
	st, err := s.mtClient().DnsUpstreams(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "MT недоступен: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"capable":       true,
		"healthCapable": s.mtDnsHealthCapable(r.Context()),
		"state":         st,
	})
}

// validateDnsHealthPatch - диапазоны полей политики зондов (rev14, те же
// в форке): интервал 5-300с, таймаут 1-10с, циклы 1-20, пауза 0-3600с.
func validateDnsHealthPatch(h *magitrickle.DnsHealthPatch) error {
	if h == nil {
		return nil
	}
	if h.IntervalSec != nil && (*h.IntervalSec < 5 || *h.IntervalSec > 300) {
		return fmt.Errorf("интервал проверки: %dс - от 5 до 300 секунд", *h.IntervalSec)
	}
	if h.TimeoutSec != nil && (*h.TimeoutSec < 1 || *h.TimeoutSec > 10) {
		return fmt.Errorf("таймаут: %dс - от 1 до 10 секунд", *h.TimeoutSec)
	}
	if h.FailCycles != nil && (*h.FailCycles < 1 || *h.FailCycles > 20) {
		return fmt.Errorf("циклов до отключения: %d - от 1 до 20", *h.FailCycles)
	}
	if h.RestoreCycles != nil && (*h.RestoreCycles < 1 || *h.RestoreCycles > 20) {
		return fmt.Errorf("циклов до возврата: %d - от 1 до 20", *h.RestoreCycles)
	}
	if h.RestoreGapSec != nil && *h.RestoreGapSec > 3600 {
		return fmt.Errorf("пауза возврата: %dс - от 0 до 3600 секунд", *h.RestoreGapSec)
	}
	return nil
}

// dnsUpstreamsSet - правка апстримов {"upstreams": [...]} и/или политики
// зондов {"health": {...}} dns-proxy MT. Прямую правку config.yaml из
// mawg не делаем - гонка с записью конфига самим MT; форк-эндпоинт
// сохраняет конфиг и горячо подменяет сам. Запись только health
// (upstreams отсутствует, rev14+) список не трогает - потому петлезащита
// к ней не применяется.
func (s *Server) dnsUpstreamsSet(w http.ResponseWriter, r *http.Request) {
	if !s.dnsManagedGate(w) {
		return
	}
	if !s.mtDnsUpstreamsCapable(r.Context()) {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "правка апстримов требует MagiTrickle 0.8.2-rev8+ (обновите MT)"})
		return
	}
	var req struct {
		Upstreams []string                    `json:"upstreams"`
		Health    *magitrickle.DnsHealthPatch `json:"health"`
		Confirm   bool                        `json:"confirm"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeErr(w, fmt.Errorf("кривой запрос"))
		return
	}
	if req.Health != nil {
		if err := validateDnsHealthPatch(req.Health); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if !s.mtDnsHealthCapable(r.Context()) {
			writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "настройка проверки серверов требует MagiTrickle 0.8.2-rev14+ (обновите MT)"})
			return
		}
	}
	if req.Upstreams == nil && req.Health == nil {
		writeErr(w, fmt.Errorf("пустой запрос: нет ни списка серверов, ни настроек проверки"))
		return
	}
	// это РАБОЧИЙ каскад DNS форка (не только проверки): пустой список
	// оставил бы роутер без резолва при перехвате DNS в MT - минимум один
	if req.Upstreams != nil && len(req.Upstreams) == 0 {
		writeErr(w, fmt.Errorf("нужен хотя бы один сервер: это рабочая цепочка DNS, а не только цели проверки"))
		return
	}
	// петлезащита: dnsmasq направлен в MT, а в списке адрес dnsmasq
	// (127.0.0.1:53/свой IP:53) - петля dnsmasq->MT->dnsmasq; без confirm
	// честный 409 со списком (предупреждение+подтверждение, слово
	// владельца 2026-10-18). Health-only запись список не меняет - её не касаемся.
	if req.Upstreams != nil {
		risky := s.loopRiskUpstreams(req.Upstreams)
		if len(risky) > 0 && !req.Confirm {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":    "замкнёт петлю: dnsmasq сейчас направлен в MT, а среди адресов есть его адрес (" + strings.Join(risky, ", ") + ")",
				"loopRisk": risky,
			})
			return
		}
		if len(risky) > 0 {
			s.store.LogEvent("dns", "upstreams", "петлевой риск подтверждён вручную: "+strings.Join(risky, ", "))
		}
	}
	if err := s.mtClient().SetDnsUpstreams(r.Context(), req.Upstreams, req.Health); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	st, err := s.mtClient().DnsUpstreams(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "state": st})
}
