package web

import (
	"encoding/json"
	"net/http"
)

// Кноб «Уровень лога MT» (кейс Вячеслава 12.11: WRN dns-proxy забивал
// syslog OpenWrt): GET отдаёт текущий уровень + capable; PUT передаёт имя
// уровня форку (он применяет глобально и пишет в config.yaml, переживает
// рестарт). Ошибка форка пробрасывается с её текстом - панель показывает,
// что уровень не применился, а не молчит.

// mtLogLevelGet - GET /mt/loglevel: {level, mtCapable}; level пуст на
// старой ревизии форка или недоступном MT.
func (s *Server) mtLogLevelGet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !s.mtLogLevelCapable(ctx) {
		writeJSON(w, http.StatusOK, map[string]any{"level": "", "mtCapable": false})
		return
	}
	level, err := s.mtClient().MtLogLevel(ctx)
	if err != nil {
		// MT жив (version ответила), а уровень прочесть не удалось -
		// контрол показываем, значение честно пустое
		writeJSON(w, http.StatusOK, map[string]any{"level": "", "mtCapable": true})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"level": level, "mtCapable": true})
}

// mtLogLevelSet - PUT /mt/loglevel {"level":"debug"}.
func (s *Server) mtLogLevelSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Level string `json:"level"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Level == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "нужен JSON {\"level\": \"...\"}"})
		return
	}
	if err := s.mtClient().SetMtLogLevel(r.Context(), req.Level); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"level": req.Level, "mtCapable": true})
}
