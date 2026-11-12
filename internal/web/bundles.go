package web

// Хендлеры/логика наборов интерфейсов (bundles) (из server.go).

import (
	"encoding/json"
	"fmt"
	"net/http"

	"mawg/internal/store"
)

// memberKnown - участник существует: пул, слот mawg или чужой туннель
// (SysTunnels - в слотах его нет, кейс владельца 12.11: tun9 wdttc).
func (s *Server) memberKnown(m string) bool {
	if _, ok := s.store.Pool(m); ok {
		return true
	}
	known := false
	if slots, err := s.backend.Slots(); err == nil {
		for _, sl := range slots {
			if sl.Device == m {
				known = true
				break
			}
		}
	}
	if !known {
		if tunnels, err := s.backend.SysTunnels(); err == nil {
			for _, sl := range tunnels {
				if sl.Device == m {
					known = true
					break
				}
			}
		}
	}
	return known
}

func (s *Server) bundleMembersResolve(members []string) (map[string]string, error) {
	out := map[string]string{}
	for _, m := range members {
		if _, ok := out[m]; ok {
			return nil, fmt.Errorf("участник %s повторяется", m)
		}
		if p, ok := s.store.Pool(m); ok {
			out[m] = p.DeviceName()
			continue
		}
		if !s.memberKnown(m) {
			return nil, fmt.Errorf("участник %s не найден (нет такого пула или интерфейса)", m)
		}
		out[m] = m
	}
	return out, nil
}

type bundleView struct {
	store.Bundle
	CurrentMember string            `json:"currentMember,omitempty"`
	MemberDevices map[string]string `json:"memberDevices"`
	// Issues - мёртвые ссылки набора: участник/группа, которых больше
	// нет. Набор с мёртвой группой молча не переключает её (applyGroup
	// Changes не находит id), пользователь должен видеть почему.
	Issues []string `json:"issues,omitempty"`
}

// bundleLiveGroups - id живых MT-групп; nil = MT недоступен (проверку
// групп не делаем, чтобы не обвинять ссылки в смерти при лежащем MT).
func (s *Server) bundleLiveGroups() map[string]string {
	groups, _, err := s.mtSnapshotCached()
	if err != nil {
		return nil
	}
	live := make(map[string]string, len(groups))
	for _, g := range groups {
		live[g.ID] = g.Name
	}
	return live
}

func (s *Server) bundleToView(b store.Bundle, liveGroups map[string]string) bundleView {
	v := bundleView{Bundle: b, MemberDevices: map[string]string{}, CurrentMember: s.store.BundleState(b.Name).Member}
	devices, err := s.bundleMembersResolve(b.Members)
	if err == nil {
		v.MemberDevices = devices
	}
	for _, m := range b.Members {
		if !s.memberKnown(m) {
			v.Issues = append(v.Issues, "участник "+m+" не найден - нет такого пула или интерфейса")
		}
	}
	if liveGroups != nil {
		for _, g := range b.Groups {
			if _, ok := liveGroups[g]; !ok {
				v.Issues = append(v.Issues, "группа "+g+" не существует (удалена или пересоздана с новым id) - набор её не переключает")
			}
		}
	}
	return v
}

func (s *Server) getBundles(w http.ResponseWriter, r *http.Request) {
	live := s.bundleLiveGroups()
	out := []bundleView{}
	for _, b := range s.store.Bundles() {
		out = append(out, s.bundleToView(b, live))
	}
	writeJSON(w, http.StatusOK, map[string]any{"bundles": out})
}

func (s *Server) createBundle(w http.ResponseWriter, r *http.Request) {
	var req store.Bundle
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeErr(w, fmt.Errorf("нужно имя"))
		return
	}
	if _, err := s.bundleMembersResolve(req.Members); err != nil {
		writeErr(w, err)
		return
	}
	b, err := s.store.CreateBundle(req)
	if err != nil {
		writeErr(w, err)
		return
	}
	s.store.LogEvent("bundle:"+b.Name, "create", "создан набор, участников: "+fmt.Sprint(len(b.Members)))
	s.engine.CheckBundlesNow()
	writeJSON(w, http.StatusOK, s.bundleToView(b, s.bundleLiveGroups()))
}

func (s *Server) updateBundle(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req store.Bundle
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	if _, err := s.bundleMembersResolve(req.Members); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.store.UpdateBundle(name, req); err != nil {
		writeErr(w, err)
		return
	}
	b, _ := s.store.Bundle(name)
	s.store.LogEvent("bundle:"+name, "update", "изменён набор")
	s.engine.CheckBundlesNow()
	writeJSON(w, http.StatusOK, s.bundleToView(b, s.bundleLiveGroups()))
}

func (s *Server) deleteBundle(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteBundle(r.PathValue("name")); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "deleted"})
}
