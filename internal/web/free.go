package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

func (s *Server) createFreePool(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string `json:"name"`
		Fallback  string `json:"fallback"`
		ProbeHost string `json:"probeHost"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(&req) != nil || req.Name == "" || d.Decode(new(any)) != io.EOF {
		writeErr(w, errors.New("expected a Free pool name, without credentials or region override"))
		return
	}
	settings, err := s.poolSettingsFromReq(poolSourceReq{Name: req.Name, Fallback: req.Fallback, ProbeHost: req.ProbeHost, OpenwrtProto: "amneziawg"})
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := s.validFallback(req.Name, settings.Fallback); err != nil {
		writeErr(w, err)
		return
	}
	p, err := s.engine.CreateFreePool(r.Context(), req.Name, settings)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sourcePlan{Pool: p.Name, Added: len(p.Configs), Warnings: []string{"Amnezia Free получен без аккаунта. Пул не активирован: используйте обычную кнопку активации после проверки настроек."}})
}
