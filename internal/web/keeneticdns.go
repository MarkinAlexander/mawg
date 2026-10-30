package web

// «DNS самого роутера» на Keenetic (98-док). Историческая граница «панель
// в ndm не пишет» (69/71-доки) остаётся дефолтом, но по ЯВНОМУ согласию
// пользователя mawg кормит ndm одной записью ip name-server:
//   spare - страховочный сервер (ndm без единого апстрима оглохнет:
//           обновления, opkg, hostname-эндпоинты пулов вроде WARP);
//   mt    - DNS роутера через каскад MagiTrickle (канарейка и
//           автопереключение форка ведут и сам роутер).
// Рестор - снятие ровно той записи (dnsmgr.ApplyKeeneticDNS). Гард
// mt-режима: петлевые адреса (127.0.0.1:53/свой IP:53) вычищаются из
// «Резервных серверов» до включения - иначе ndm->MT->ndm.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"mawg/internal/dnsmgr"
	"mawg/internal/store"
)

// keeneticDNSState - ответ GET /dns/keenetic/dns.
type keeneticDNSState struct {
	Mode    string `json:"mode"`
	Spare   string `json:"spare,omitempty"`
	Iface   string `json:"iface,omitempty"`
	Applied string `json:"applied,omitempty"`
	Live    bool   `json:"live"`
}

func (s *Server) keeneticDNSState() keeneticDNSState {
	set := s.store.KeeneticDNS()
	st := keeneticDNSState{Mode: set.Mode, Spare: set.Spare, Iface: set.Iface, Applied: set.Applied}
	if set.Applied != "" {
		if run, ok := s.dnsRunner(); ok {
			st.Live = dnsmgr.KeeneticDNSLive(run, set.Applied)
		}
	}
	return st
}

// keeneticDNSGet - состояние режима «DNS самого роутера».
func (s *Server) keeneticDNSGet(w http.ResponseWriter, r *http.Request) {
	if !s.keeneticDNSReady(w) {
		return
	}
	writeJSON(w, http.StatusOK, s.keeneticDNSState())
}

// keeneticDNSReady - платформенный гейт: только Keenetic с шеллом.
func (s *Server) keeneticDNSReady(w http.ResponseWriter) bool {
	if s.backend.Name() != store.PlatformKeenetic {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "режим «DNS самого роутера» есть только на Keenetic (на OpenWrt mawg сам направляет dnsmasq)"})
		return false
	}
	if _, ok := s.dnsRunner(); !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "платформа без шелла"})
		return false
	}
	return true
}

// keeneticDNSSet - применение режима {"mode":"off|spare|mt","spare",
// "iface","confirm"}. mt без confirm - 409 (честное предупреждение о
// зависимости роутера от живого MT), повтор с confirm:true применяется.
func (s *Server) keeneticDNSSet(w http.ResponseWriter, r *http.Request) {
	if !s.keeneticDNSReady(w) {
		return
	}
	var req struct {
		Mode    string `json:"mode"`
		Spare   string `json:"spare"`
		Iface   string `json:"iface"`
		Confirm bool   `json:"confirm"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeErr(w, fmt.Errorf("кривой запрос"))
		return
	}
	prev := s.store.KeeneticDNS()

	var args string
	switch req.Mode {
	case "spare":
		var err error
		if args, err = dnsmgr.NameServerArgs(req.Spare, req.Iface); err != nil {
			writeErr(w, err)
			return
		}
		if err := s.spareLoopGuard(args); err != nil {
			writeErr(w, err)
			return
		}
	case "mt":
		if !req.Confirm {
			writeJSON(w, http.StatusConflict, map[string]any{
				"needConfirm": true,
				"error":       "DNS роутера встанет в зависимость от MagiTrickle: умер MT - умер резолв самого роутера (панель будет его рестартовать, но это честный SPOF). Панель также вычистит 127.0.0.1:53/свой IP:53 из «Резервных серверов» (иначе петля ndm→MT→ndm).",
			})
			return
		}
		if err := s.cleanMTUpstreamLoop(r.Context()); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "MT не отвечает или список не вычищен: " + err.Error()})
			return
		}
	case "off":
		// рестор - безопасно, согласие не требуется
	default:
		writeErr(w, fmt.Errorf("mode: off|spare|mt"))
		return
	}

	applied, err := dnsmgr.ApplyKeeneticDNS(s.dnsRunnerFn(), req.Mode, req.Spare, req.Iface, prev.Applied)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	next := store.KeeneticDNS{Mode: req.Mode, Spare: req.Spare, Iface: req.Iface, Applied: applied}
	if err := s.store.SetKeeneticDNS(next); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.store.LogEvent("dns", "keenetic-dns", s.keeneticDNSEventText(req.Mode, applied))
	st := s.keeneticDNSState()
	st.Live = applied != "" && dnsmgr.KeeneticDNSLive(s.dnsRunnerFn(), applied)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "state": st})
}

// dnsRunnerFn - Runner для dnsmgr (панель веб-хендлеров держит его один раз).
func (s *Server) dnsRunnerFn() dnsmgr.Runner {
	run, _ := s.dnsRunner()
	return run
}

// spareLoopGuard - страховочный сервер не должен указывать на системный
// резолвер (127.0.0.1:53 / свой IP:53 = ndm сам себе): петля ndm->ndm.
// Порт 3553 легален - это способ направить ndm в MT без режима mt.
func (s *Server) spareLoopGuard(args string) error {
	if dnsmasqLoopHost(nameServerArgsHost(args), dnsmgr.SelfIPs()) {
		return fmt.Errorf("адрес указывает на системный резолвер самого роутера - ndm замкнётся на себе; возьмите внешний сервер")
	}
	return nil
}

// cleanMTUpstreamLoop - перед mt-режимом вычистить петлевые адреса из
// рабочего каскада MT (панель его ведёт, так что правка легитимна).
func (s *Server) cleanMTUpstreamLoop(ctx context.Context) error {
	res, err := s.mtClient().DnsUpstreams(ctx)
	if err != nil {
		return fmt.Errorf("нет ответа MT: %w", err)
	}
	self := dnsmgr.SelfIPs()
	var keep, removed []string
	for _, u := range res.Upstreams {
		if dnsmasqLoopHost(u.Address, self) {
			removed = append(removed, u.Address)
			continue
		}
		keep = append(keep, u.Address)
	}
	if len(removed) == 0 {
		return nil
	}
	if len(keep) == 0 {
		return fmt.Errorf("в «Резервных серверах» только петлевые адреса (%s) - добавьте внешний сервер прежде режима «через MagiTrickle»", strings.Join(removed, ", "))
	}
	if err := s.mtClient().SetDnsUpstreams(ctx, keep, nil); err != nil {
		return fmt.Errorf("не вычистить %s из MT: %w", strings.Join(removed, ", "), err)
	}
	s.store.LogEvent("dns", "upstreams", "петлевые адреса вычищены перед режимом «DNS роутера через MT»: "+strings.Join(removed, ", "))
	return nil
}

// keeneticDNSEventText - строка журнала по режиму.
func (s *Server) keeneticDNSEventText(mode, applied string) string {
	switch mode {
	case "spare":
		return "DNS роутера: страховочный сервер " + applied + " (ip name-server записан в Keenetic)"
	case "mt":
		return "DNS роутера: через MagiTrickle (ip name-server 127.0.0.1 3553 записан в Keenetic)"
	default:
		if applied == "" {
			return "DNS роутера: режим снят, записи панели в Keenetic нет"
		}
		return "DNS роутера: режим снят, запись " + applied + " удалена из Keenetic"
	}
}

// nameServerArgsHost - хост из канонической строки аргументов
// ("8.8.8.8 5353" / `8.8.8.8 "" on ISP`).
func nameServerArgsHost(args string) string {
	f := strings.Fields(args)
	if len(f) == 0 {
		return ""
	}
	return f[0]
}
