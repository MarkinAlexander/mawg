package web

import (
	"encoding/json"
	"errors"
	"net/http"

	"mawg/internal/premium"
	"mawg/internal/store"
)

func (s *Server) createPremiumPoolFromSource(w http.ResponseWriter, r *http.Request, req poolSourceReq) {
	if _, _, err := premium.ImportKey(req.Source); err != nil {
		writeErr(w, err)
		return
	}
	for _, p := range s.store.Pools() {
		if p.Premium {
			writeErr(w, errors.New("Premium уже привязан к пулу; используйте его панель смены страны"))
			return
		}
	}
	key := req.Source
	req.Source = ""
	req.OpenwrtProto = "amneziawg"
	settings, err := s.poolSettingsFromReq(req)
	if err != nil {
		writeErr(w, err)
		return
	}
	// Keenetic: конфиг gateway - AWG 3.x (защита заголовка и диапазоны),
	// слот 5.1 (AWG 2.0) его не поднимает - пул сразу в режиме движка
	// sing-box-lx. OpenWrt - нативно (kmod AWG 3.x).
	if s.backend.Name() == store.PlatformKeenetic {
		settings.KeeneticSlot = ""
		settings.EngineMode = engineMode
		// проба Premium - google-204: gstatic/gstatic-like домены отдают
		// AAAA и резолвятся мимо туннеля (v6 у Premium нет), 1.1.1.1 режет
		// сам сервер Premium; google-204 стабилен и в списке
		settings.ProbeHost = "https://www.google.com/generate_204"
	}
	var p store.Pool
	if settings.EngineMode == engineMode {
		p, err = s.claimTun(settings, req.Name)
	} else {
		p, err = s.store.CreatePool(req.Name, settings)
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	plan := sourcePlan{Pool: p.Name, Premium: true}
	if err := s.engine.ImportPremium(r.Context(), p.Name, key); err != nil {
		if !s.store.PremiumView(p.Name).Imported {
			if cleanupErr := s.store.DeletePool(p.Name); cleanupErr != nil {
				writeErr(w, errors.New("импорт Premium не завершён; пустой пул не удалось удалить"))
				return
			}
			writeErr(w, err)
			return
		}
		plan.Warnings = []string{err.Error() + ". Ключ сохранён; повторите импорт в карточке пула, чтобы загрузить список стран."}
	}
	writeJSON(w, http.StatusOK, plan)
}

func (s *Server) getPremium(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.store.Pool(r.PathValue("name")); !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, s.store.PremiumView(r.PathValue("name")))
}

func (s *Server) importPremium(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key string `json:"key"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.Key == "" {
		writeErr(w, errors.New("вставьте ключ Premium (vpn://)"))
		return
	}
	if err := s.engine.ImportPremium(r.Context(), r.PathValue("name"), req.Key); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.store.PremiumView(r.PathValue("name")))
}

func (s *Server) switchPremium(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Country string `json:"country"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.Country == "" {
		writeErr(w, errors.New("выберите страну для Premium"))
		return
	}
	name := r.PathValue("name")
	// пул, созданный до движкового пути (нативный на Keenetic), мигрирует
	// при первой же смене страны: gateway выдаёт AWG 3.x, слот 5.1 его
	// не поднимает
	if p, ok := s.store.Pool(name); ok && p.Premium && p.Settings.EngineMode == "" && s.backend.Name() == store.PlatformKeenetic {
		merged := p.Settings
		merged.EngineMode = engineMode
		merged.TunName = s.allocTun()
		if err := s.store.UpdatePool(name, merged); err == nil {
			s.store.LogEvent(name, "premium", "миграция в режим движка (конфиги gateway - AWG 3.x, слот AWG 2.0 их не поднимает)")
		}
	}
	if err := s.engine.SwitchPremium(r.Context(), name, req.Country); err != nil {
		writeErr(w, err)
		return
	}
	// движковый пул: конфиг сохранён, применяем движком
	if p, ok := s.store.Pool(name); ok && p.Settings.EngineMode == engineMode {
		if _, err := s.applyEngine(); err != nil {
			s.store.LogEvent(name, "premium", "движок не пересобран после смены страны: "+err.Error())
		}
	}
	writeJSON(w, http.StatusOK, s.store.PremiumView(name))
}
