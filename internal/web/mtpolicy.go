package web

// Хендлеры политик групп MagiTrickle (из mtgroups.go): отвала/источники
// и гистерезис. Политики - фоновый писатель, черновик (114-док) их не
// гейтит.

import (
	"encoding/json"
	"fmt"
	"net/http"

	"mawg/internal/store"
)

// getPolicyCycles/putPolicyCycles - гистерезис политик групп: сколько
// циклов до переключения с исходного интерфейса и сколько до возврата.
func (s *Server) getPolicyCycles(w http.ResponseWriter, r *http.Request) {
	st := s.store.Settings()
	writeJSON(w, http.StatusOK, map[string]int{
		"failCycles":    st.PolicyFailCycles,
		"restoreCycles": st.PolicyRestoreCycles,
	})
}

func (s *Server) putPolicyCycles(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FailCycles    int `json:"failCycles"`
		RestoreCycles int `json:"restoreCycles"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	if req.FailCycles < 1 || req.FailCycles > 20 || req.RestoreCycles < 1 || req.RestoreCycles > 20 {
		writeErr(w, fmt.Errorf("циклы должны быть от 1 до 20"))
		return
	}
	st := s.store.Settings()
	st.PolicyFailCycles = req.FailCycles
	st.PolicyRestoreCycles = req.RestoreCycles
	if err := s.store.SetSettings(st); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{
		"failCycles":    st.PolicyFailCycles,
		"restoreCycles": st.PolicyRestoreCycles,
	})
}

func (s *Server) mtSetPolicy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OnDead        string   `json:"onDead"`
		Iface         string   `json:"iface"`
		SourcesMode   string   `json:"sourcesMode"` // "" | all | all+router | list
		Sources       []string `json:"sources"`
		SourcesRouter bool     `json:"sourcesRouter"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	switch req.OnDead {
	case "", store.PolicyDirect, store.PolicyIface:
	default:
		writeErr(w, fmt.Errorf("неизвестный режим %q", req.OnDead))
		return
	}
	switch req.SourcesMode {
	case "", "all", "all+router", "list":
	default:
		writeErr(w, fmt.Errorf("неизвестный режим источников %q", req.SourcesMode))
		return
	}
	var userSources []string
	if req.SourcesMode == "list" {
		var err error
		userSources, err = normalizeSources(req.Sources)
		if err != nil {
			writeErr(w, err)
			return
		}
		if len(userSources) == 0 && !req.SourcesRouter {
			writeErr(w, fmt.Errorf("пустой список: добавьте IP или включите роутер"))
			return
		}
	} else {
		userSources = nil
	}
	pol := store.GroupPolicy{
		OnDead:        req.OnDead,
		Iface:         req.Iface,
		SourcesMode:   req.SourcesMode,
		Sources:       userSources,
		SourcesRouter: req.SourcesRouter,
	}
	if pol.OnDead == store.PolicyIface && pol.Iface == "" {
		writeErr(w, fmt.Errorf("укажите запасной интерфейс"))
		return
	}
	id := r.PathValue("id")
	if req.SourcesMode != "" && req.SourcesMode != "all" {
		if _, isCasc := s.store.CascadeByGroup(id); isCasc {
			writeErr(w, fmt.Errorf("у каскадной группы источники задаются при создании каскада"))
			return
		}
	}
	if req.SourcesMode == "all+router" || req.SourcesMode == "list" {
		if !s.mtSourcesCapable(r.Context()) {
			writeErr(w, fmt.Errorf("подключённый MagiTrickle не умеет источники (нужен форк 0.8.2-rev3+)"))
			return
		}
	}
	if err := s.store.SetGroupPolicy(id, &pol); err != nil {
		writeErr(w, err)
		return
	}
	// применяем только если группа ещё жива: сторе можно задать политику
	// и будущей группе, а писать в MT - только существующей
	applied := false
	if groups, err := s.mtClient().GroupsWithRules(r.Context()); err == nil {
		for _, g := range groups {
			if g.ID == id {
				applied = true
				break
			}
		}
	}
	if applied {
		if c := s.casc(); c != nil {
			if err := c.ApplyGroupSources(id, pol); err != nil {
				writeErr(w, err)
				return
			}
		}
	}
	switch {
	case pol.SourcesEmpty():
		s.store.LogEvent("policy", "sources", "группа "+id+": источники - вся локалка")
	case pol.SourcesMode == "all+router":
		s.store.LogEvent("policy", "sources", "группа "+id+": локалка + роутер")
	case pol.SourcesRouter:
		s.store.LogEvent("policy", "sources", fmt.Sprintf("группа %s: только выбранные IP (%d) + роутер", id, len(userSources)))
	default:
		s.store.LogEvent("policy", "sources", fmt.Sprintf("группа %s: только выбранные IP (%d)", id, len(userSources)))
	}
	if pol.OnDead != "" {
		s.store.LogEvent("policy", "set", "группа "+id+": при отвале - "+pol.OnDead)
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "ok"})
}
