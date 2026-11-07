package web

// Хендлеры/логика каскадов (из server.go).

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// ifaceEndpointsHint - авторезолв эндпоинтов роутерного WG-интерфейса
// для префилла диалога каскадов (баг владельца: каскад с nwg7 требовал
// адреса от руки, хотя Кинетик их отдаёт в RCI).
func (s *Server) ifaceEndpointsHint(w http.ResponseWriter, r *http.Request) {
	c := s.casc()
	if c == nil {
		writeErr(w, fmt.Errorf("magitrickle недоступен"))
		return
	}
	hosts, err := c.IfaceEndpoints(r.URL.Query().Get("source"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hosts": hosts})
}

func (s *Server) cascList(w http.ResponseWriter, r *http.Request) {
	c := s.casc()
	if c == nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"available": true, "cascades": c.Infos(r.Context()), "wrapper": c.WrapperDetected(),
		"backendFlavor": string(c.BackendFlavor())})
}

func (s *Server) cascCreate(w http.ResponseWriter, r *http.Request) {
	c := s.casc()
	if c == nil {
		writeErr(w, fmt.Errorf("magitrickle недоступен"))
		return
	}
	var req struct {
		Source string   `json:"source"`
		Via    string   `json:"via"`
		Hosts  []string `json:"hosts"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Source == "" || req.Via == "" {
		writeErr(w, fmt.Errorf("нужны source и via"))
		return
	}
	g, err := c.Create(r.Context(), req.Source, req.Via, req.Hosts)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) cascDelete(w http.ResponseWriter, r *http.Request) {
	c := s.casc()
	if c == nil {
		writeErr(w, fmt.Errorf("magitrickle недоступен"))
		return
	}
	if err := c.Delete(r.Context(), r.PathValue("group")); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "deleted"})
}
