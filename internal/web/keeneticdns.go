package web

// «DNS самого роутера» на Keenetic (98-док). Историческая граница «панель
// в ndm не пишет» (69/71-доки) остаётся дефолтом, но по ЯВНОМУ согласию
// пользователя mawg кормит ndm одной записью ip name-server: spare -
// страховочный сервер (ndm без единого апстрима оглохнет: обновления,
// opkg, hostname-эндпоинты пулов вроде WARP). Рестор - снятие ровно той
// записи (dnsmgr.ApplyKeeneticDNS).
// Режим «в MT» снят (инцидент 01.11: ndm не принимает 127.0.0.1 как
// апстрим и портов не поддерживает - MT:3553 через name-server
// невыразим); перехват DNS клиентов делает сам MT (DNAT :53->:3553).

import (
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
	// Intercept - глобальный перехват ndm (system profile): весь :53
	// отвечает сам Keenetic через свои апстримы.
	Intercept bool `json:"intercept"`
}

func (s *Server) keeneticDNSState() keeneticDNSState {
	set := s.store.KeeneticDNS()
	st := keeneticDNSState{Mode: set.Mode, Spare: set.Spare, Iface: set.Iface, Applied: set.Applied}
	if run, ok := s.dnsRunner(); ok {
		// один ndmc-вызов на обе верификации (запись + перехват)
		if cfg, err := dnsmgr.KeeneticRunningConfig(run); err == nil {
			st.Live = set.Applied != "" && dnsmgr.NameServerInConfig(cfg, set.Applied)
			st.Intercept = dnsmgr.ParseKeeneticOwn(cfg).Intercept
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
		Mode      string `json:"mode"`
		Spare     string `json:"spare"`
		Iface     string `json:"iface"`
		Confirm   bool   `json:"confirm"`
		Intercept *bool  `json:"intercept"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeErr(w, fmt.Errorf("кривой запрос"))
		return
	}
	prev := s.store.KeeneticDNS()

	// тумблер перехвата ndm - отдельное действие, mode не трогает
	if req.Intercept != nil {
		if s.setKeeneticIntercept(w, *req.Intercept, req.Confirm) {
			st := s.keeneticDNSState()
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "state": st})
		}
		return
	}

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
		// режим снят (инцидент 01.11): ndm не принимает 127.0.0.1 как
		// апстрим и портов не поддерживает - MT:3553 через name-server
		// невыразим; перехват DNS делает сам MT (DNAT). Старая вкладка
		// UI получает честный отказ вместо «проверьте прошивку».
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "режим «в MT» снят: ndm не принимает 127.0.0.1 как апстрим и портов не поддерживает (второе поле ip name-server - домен). Перехват DNS на Keenetic делает сам MagiTrickle - прописывать ничего не нужно",
		})
		return
	case "off":
		// рестор - безопасно, согласие не требуется
	default:
		writeErr(w, fmt.Errorf("mode: off|spare"))
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

// setKeeneticIntercept - применение тумблера перехвата ndm; false = ответ
// уже записан клиенту (ошибка/409), true = применено.
func (s *Server) setKeeneticIntercept(w http.ResponseWriter, enable, confirm bool) bool {
	if enable && !confirm {
		// перехват всего :53 - честное предупреждение перед записью
		writeJSON(w, http.StatusConflict, map[string]any{
			"needConfirm": true,
			"error":       "Keenetic начнёт отвечать на ВСЕ запросы порта 53 - и клиентов с чужим DNS, и собственные; резолвером станут его апстримы.",
		})
		return false
	}
	// (гард «перехват + mt = петля» удалён вместе с режимом mt - инцидент
	// 01.11: ndm больше не направляется в MT, петле неоткуда взяться)
	if err := dnsmgr.SetKeeneticIntercept(s.dnsRunnerFn(), enable); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return false
	}
	if enable {
		s.store.LogEvent("dns", "keenetic-dns", "перехват DNS Keenetic включён (system profile): весь :53 отвечает ndm")
	} else {
		s.store.LogEvent("dns", "keenetic-dns", "перехват DNS Keenetic выключен")
	}
	return true
}

// spareLoopGuard - страховочный сервер не должен указывать на системный
// резолвер (127.0.0.1:53 / свой IP:53 = ndm сам себе): петля ndm->ndm.
// (Портовые указания на MT сняты вместе с режимом "mt" - NameServerArgs
// отвергает порты раньше.)
func (s *Server) spareLoopGuard(args string) error {
	if dnsmasqLoopHost(nameServerArgsHost(args), dnsmgr.SelfIPs()) {
		return fmt.Errorf("адрес указывает на системный резолвер самого роутера - ndm замкнётся на себе; возьмите внешний сервер")
	}
	return nil
}

// cleanMTUpstreamLoop удалён вместе с режимом "mt" (инцидент 01.11:
// ndm не принимает 127.0.0.1 как апстрим, портов не поддерживает -
// MT:3553 через name-server невыразим).

// keeneticDNSEventText - строка журнала по режиму.
func (s *Server) keeneticDNSEventText(mode, applied string) string {
	switch mode {
	case "spare":
		return "DNS роутера: страховочный сервер " + applied + " (ip name-server записан в Keenetic)"
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
