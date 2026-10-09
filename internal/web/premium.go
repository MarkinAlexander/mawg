package web

import (
	"encoding/json"
	"errors"
	"net/http"

	"mawg/internal/premium"
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
	if err := s.validFallback(req.Name, settings.Fallback); err != nil {
		writeErr(w, err)
		return
	}
	p, err := s.store.CreatePool(req.Name, settings)
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
	if err := s.engine.SwitchPremium(r.Context(), r.PathValue("name"), req.Country); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.store.PremiumView(r.PathValue("name")))
}
