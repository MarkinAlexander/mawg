package web

// «DNS управляется mawg» - главный переключатель DNS-раздела (115-док,
// слово владельца 09.11: «позволить пользователю вообще отключать DNS
// через mawg и MagiTrickle, максимум - канарейки»). off = режим «не лезть»:
// в yaml MT пишется dnsProxy.disableRemap53 (перехват :53 не создаётся при
// старте MT - start.go форка), MT рестартует, на OpenWrt dnsmasq
// возвращается на прямые серверы; PUT-ы DNS гейтятся (dnsManagedGate),
// ndm не пишется вовсе; канарейки (GET /dns/probe, свои серверы, ndm-
// серверы карточки Keenetic) живут - проба идёт с роутера напрямую,
// DNAT ей не нужен. Флаг в Settings трёхзначный (nil = дефолт
// «управляется»), чтобы живые установки не меняли поведение.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"mawg/internal/dnsmgr"
	"mawg/internal/magitrickle"
	"mawg/internal/store"
)

type dnsManagedView struct {
	// Managed - nil = дефолт «управляется»; false = режим «не лезть».
	Managed *bool  `json:"managed"`
	Flavor  string `json:"mtFlavor,omitempty"`
	Remap53 bool   `json:"remap53"`
	// Platform - "openwrt" | "keenetic": карточка ветвит подсказку.
	Platform string `json:"platform"`
}

func (s *Server) dnsManagedView() dnsManagedView {
	v := dnsManagedView{Managed: s.store.DnsManaged(), Platform: s.backend.Name()}
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
	writeJSON(w, http.StatusOK, s.dnsManagedView())
}

// dnsManagedSet - {"managed": true|false, "confirm": false}. off без
// confirm - честный 409 (перехват снимается рестартом MT, доменные группы
// перестают матчить); on после off возвращает перехват (yaml + рестарт MT).
func (s *Server) dnsManagedSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Managed bool `json:"managed"`
		Confirm bool `json:"confirm"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeErr(w, fmt.Errorf("кривой запрос"))
		return
	}
	prev := s.store.DnsManaged()
	if !req.Managed && !req.Confirm {
		writeJSON(w, http.StatusConflict, map[string]any{
			"needConfirm": true,
			"error": "mawg и MagiTrickle перестанут вмешиваться в DNS: перехват порта 53 снимается " +
				"(правка настроек MagiTrickle + его перезапуск), доменные группы MagiTrickle перестанут " +
				"работать - им нужен перехват DNS (пулы по устройствам продолжат). Устройства будут " +
				"резолвить через DNS Keenetic - локальные записи (ip host) заработают. Проверки DNS " +
				"(канарейки) останутся.",
		})
		return
	}
	if req.Managed {
		if prev == nil || *prev {
			// уже управляется: yaml не трогаем (его мог править владелец
			// руками), рестарт без причины не делаем
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "state": s.dnsManagedView()})
			return
		}
		onPath := s.backend.MTConfigPath()
		if onPath == "" {
			writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "платформа не знает пути конфига MagiTrickle"})
			return
		}
		if err := dnsmgr.SetMTRemap53(onPath, false); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if err := s.applyMTRemap(false); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if err := s.store.SetDnsManaged(nil); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		s.store.LogEvent("dns", "managed", "DNS управляется mawg: перехват MagiTrickle возвращён (yaml + рестарт MT)")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "state": s.dnsManagedView()})
		return
	}
	// off: «mawg и MT не лезут»
	if c := s.casc(); c != nil && c.BackendFlavor() == magitrickle.FlavorMtC {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "бэкенд MT - mt-c: правка перехвата DNS не поддерживается (конфиг другой)"})
		return
	}
	path := s.backend.MTConfigPath()
	if path == "" {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "платформа не знает пути конфига MagiTrickle"})
		return
	}
	if err := dnsmgr.SetMTRemap53(path, true); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if run, ok := s.dnsRunner(); ok && s.backend.Name() != store.PlatformKeenetic {
		// dnsmasq в MT вернём на прямые серверы - иначе DNAT всё равно
		// уводил бы клиентов в MT мимо вернувшегося dnsmasq (115-док)
		if st, err := dnsmgr.Collect(run, ""); err == nil && st.Dnsmasq.InMT {
			if err := dnsmgr.SetDnsmasqTarget(run, "direct"); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "dnsmasq: " + err.Error()})
				return
			}
		}
	}
	if err := s.applyMTRemap(true); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := s.store.SetDnsManaged(boolPtr(false)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.store.LogEvent("dns", "managed", "DNS управляется mawg: ВЫКЛЮЧЕНО - перехват MagiTrickle снят (disableRemap53 + рестарт MT), канарейки живут")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "state": s.dnsManagedView()})
}

// applyMTRemap - рестарт MT с верификацией факта (remap53 - единственный
// канал наполнения ipset-ов доменных групп, молчаливый рестарт недопустим):
// цепочка должна уйти (disable=true) или вернуться (false); MT рестартует
// не мгновенно - до трёх попыток с паузой. Отката yaml не делаем: повторная
// пара «правка+рестарт» через тот же PUT приводит состояние.
func (s *Server) applyMTRemap(disable bool) error {
	if err := s.backend.RestartMagitrickle(); err != nil {
		return fmt.Errorf("рестарт MagiTrickle: %w", err)
	}
	want := !disable
	var last error
	for i := 0; i < 3; i++ {
		if i > 0 {
			time.Sleep(time.Second)
		}
		if v := s.dnsManagedView(); v.Remap53 == want {
			return nil
		}
		if want {
			last = fmt.Errorf("цепочка перехвата MT_DNSOR не поднялась после рестарта MagiTrickle - проверьте его статус")
		} else {
			last = fmt.Errorf("цепочка перехвата MT_DNSOR жива после рестарта MagiTrickle - проверьте его статус и повторите переключение")
		}
	}
	return last
}

// dnsManagedGate - PUT-ы DNS-раздела в режиме «не лезть»: править
// каскад/зоны/политику/dnsmasq/ndm нельзя (115-док). false = ответ уже
// записан (409).
func (s *Server) dnsManagedGate(w http.ResponseWriter) bool {
	if v := s.store.DnsManaged(); v != nil && !*v {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "DNS управляется вручную (режим «mawg не лезет»): правки раздела DNS отключены - включите «DNS управляется mawg» в карточке DNS",
		})
		return false
	}
	return true
}

func boolPtr(v bool) *bool { return &v }
