package web

// Умный каскад пулов - web-слой (86-док): валидация настроек в PATCH
// пула, ручные кнопки (проверить прямой сейчас / завернуть / вернуть),
// состояние автомата в /status.

import (
	"errors"
	"fmt"
	"net/http"

	"mawg/internal/store"
)

// validSmartCascade - проверка настроек умного каскада из PATCH пула.
// Пустой список Via = «выключить» (nil).
func (s *Server) validSmartCascade(p store.Pool, sc *store.SmartCascadeSettings) (*store.SmartCascadeSettings, error) {
	if len(sc.Via) == 0 {
		return nil, nil
	}
	if s.casc() == nil {
		return nil, errors.New("умный каскад требует MagiTrickle (форк)")
	}
	if p.Settings.EngineMode != "" {
		return nil, errors.New("умный каскад поддерживается нативными пулами, не движковыми")
	}
	seen := map[string]bool{}
	via := make([]string, 0, len(sc.Via))
	for _, v := range sc.Via {
		if v == p.Name {
			return nil, errors.New("пул не может быть запасным путём для самого себя")
		}
		if seen[v] {
			continue
		}
		vp, ok := s.store.Pool(v)
		if !ok {
			return nil, fmt.Errorf("запасной пул %q не найден", v)
		}
		if vp.Disabled {
			return nil, fmt.Errorf("запасной пул %q выключен", v)
		}
		seen[v] = true
		via = append(via, v)
	}
	switch sc.Return.Mode {
	case "", "interval", "schedule", "manual":
	default:
		return nil, errors.New("режим возврата: interval | schedule | manual")
	}
	switch sc.TriggerMode {
	case "", "exhaust", "fails":
	default:
		return nil, errors.New("когда включать каскад: exhaust | fails")
	}
	if sc.TriggerFails < 0 || sc.TriggerFails > 10 {
		return nil, errors.New("отказов до каскада: 0-10 (0 = 3)")
	}
	if sc.Return.IntervalH < 0 || sc.Return.IntervalH > 168 {
		return nil, errors.New("интервал возврата: 0-168 часов (0 = 6)")
	}
	for _, t := range sc.Return.TimeOfDay {
		if _, _, ok := store.ParseTimeOfDay(t); !ok {
			return nil, fmt.Errorf("время расписания %q не в форме ЧЧ:ММ", t)
		}
	}
	if sc.MinDwellMin < 0 || sc.MinDwellMin > 1440 {
		return nil, errors.New("антифлап: 0-1440 минут (0 = 10)")
	}
	out := *sc
	out.Via = via
	return &out, nil
}

func (s *Server) writeSmartState(w http.ResponseWriter, name string) {
	st := s.store.State(name)
	writeJSON(w, http.StatusOK, map[string]any{"smart": st.Smart})
}

// smartCascadeCheckNow - кнопка «проверить прямой сейчас»: гейт dwell и
// расписание минуются, wanDown честно переносит проверку.
func (s *Server) smartCascadeCheckNow(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := s.engine.SmartCascadeCheckNow(name); err != nil {
		writeErr(w, err)
		return
	}
	s.writeSmartState(w, name)
}

func (s *Server) smartCascadeOn(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := s.engine.SmartCascadeSetManual(name, true); err != nil {
		writeErr(w, err)
		return
	}
	s.writeSmartState(w, name)
}

func (s *Server) smartCascadeOff(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := s.engine.SmartCascadeSetManual(name, false); err != nil {
		writeErr(w, err)
		return
	}
	s.writeSmartState(w, name)
}
