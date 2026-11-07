package web

// Хендлеры/логика интерфейсов и проб (из server.go).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"mawg/internal/platform"
	"mawg/internal/store"
)

func (s *Server) getSlots(w http.ResponseWriter, r *http.Request) {
	slots, err := s.backend.Slots()
	if err != nil {
		writeErr(w, err)
		return
	}
	managed := map[string]bool{}
	for _, p := range s.store.Pools() {
		managed[p.DeviceName()] = true
	}
	for i := range slots {
		slots[i].Managed = managed[slots[i].Device]
	}
	writeJSON(w, http.StatusOK, map[string]any{"slots": slots})
}

func (s *Server) getEvents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"events": s.store.Events()})
}

type ifaceView struct {
	Device       string             `json:"device"`
	Slot         string             `json:"slot,omitempty"`
	Description  string             `json:"description,omitempty"`
	Mode         string             `json:"mode"`
	Pool         string             `json:"pool,omitempty"`
	LinkUp       bool               `json:"linkUp"`
	Connected    bool               `json:"connected"`
	HandshakeAgo int                `json:"handshakeAgo"`
	Probe        *store.ProbeConfig `json:"probe,omitempty"`
	ProbeStatus  string             `json:"probeStatus,omitempty"`
	// ProbeOk/ProbeMs/ProbeAt - итог пробы ПУЛА для managed-строки: те же
	// данные, что в карточке пула (/status), проба не повторяется
	ProbeOk *bool `json:"probeOk,omitempty"`
	ProbeMs int   `json:"probeMs,omitempty"`
	ProbeAt int64 `json:"probeAt,omitempty"`
}

func (s *Server) allIfaces(ctx context.Context) []platform.SlotInfo {
	slots, err := s.backend.Slots()
	if err != nil {
		// RCI может не ответить: панель не должна пустовать, добираем
		// интерфейсы из туннелей системы, реестра и групп magitrickle
		slots = nil
	}
	seen := map[string]bool{}
	for _, sl := range slots {
		seen[sl.Device] = true
	}
	if tunnels, err := s.backend.SysTunnels(); err == nil {
		for _, sl := range tunnels {
			if !seen[sl.Device] {
				slots = append(slots, sl)
				seen[sl.Device] = true
			}
		}
	}
	for _, p := range s.store.Pools() {
		dev := p.DeviceName()
		if dev != "" && !seen[dev] {
			slots = append(slots, platform.SlotInfo{Device: dev, Description: p.Name})
			seen[dev] = true
		}
	}
	if groups, err := s.mtClient().GroupsWithRules(ctx); err == nil {
		for _, g := range groups {
			if g.Interface == "" || seen[g.Interface] {
				continue
			}
			slots = append(slots, platform.SlotInfo{Device: g.Interface})
			seen[g.Interface] = true
		}
	}
	return slots
}

func (s *Server) getIfaces(w http.ResponseWriter, r *http.Request) {
	s.ifaceCache.serve(w, 10*time.Second, func() ([]byte, error) { return s.buildIfaces(r) })
}

func (s *Server) buildIfaces(r *http.Request) ([]byte, error) {
	slots := s.allIfaces(r.Context())
	if slots == nil {
		return nil, fmt.Errorf("не удалось получить список интерфейсов")
	}
	poolByDevice := map[string]store.Pool{}
	for _, p := range s.store.Pools() {
		poolByDevice[p.DeviceName()] = p
	}
	modes := s.store.IfaceModes()
	out := make([]ifaceView, 0, len(slots))
	for _, sl := range slots {
		v := ifaceView{
			Device: sl.Device, Slot: sl.ID, Description: sl.Description,
			LinkUp: sl.LinkUp, Connected: sl.Connected, HandshakeAgo: -1,
		}
		if p, ok := poolByDevice[sl.Device]; ok {
			v.Mode = store.IfaceManaged
			v.Pool = p.Name
			// проба пула тем же единственным источником, что и карточка
			// пула: запрос не дублируется, время пробы переиспользуется
			v.ProbeOk, v.ProbeMs, v.ProbeAt, _, v.ProbeStatus = s.poolProbeInfo(p)
		} else if m := modes[sl.Device]; m == store.IfaceExternal {
			v.Mode = store.IfaceExternal
		} else {
			v.Mode = store.IfaceHidden
		}
		if v.Mode != store.IfaceManaged {
			v.HandshakeAgo = s.backend.IfaceHandshake(sl.Device)
			v.Probe = s.store.IfaceProbe(sl.Device)
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Device < out[j].Device })

	for i := range out {
		if out[i].Mode == store.IfaceExternal && out[i].Probe != nil {
			out[i].ProbeStatus = s.engine.DeviceProbeStatus(out[i].Device)
		}
	}
	return json.Marshal(map[string]any{"interfaces": out})
}

func (s *Server) setIfaceMode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	device := r.PathValue("device")
	known := false
	for _, sl := range s.allIfaces(r.Context()) {
		if sl.Device == device {
			known = true
			break
		}
	}
	if !known {
		writeErr(w, fmt.Errorf("интерфейс %s не найден в системе", device))
		return
	}
	if err := s.store.SetIfaceMode(device, req.Mode); err != nil {
		writeErr(w, err)
		return
	}
	s.store.LogEvent("ifaces", "mode", device+" -> "+req.Mode)
	writeJSON(w, http.StatusOK, map[string]string{"ok": "set"})
}

func parseProbeBody(r *http.Request) (*store.ProbeConfig, error) {
	var req store.ProbeConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return nil, err
	}
	if req.Target == "" {
		return nil, nil
	}
	if !store.ValidProbeTarget(req.Target) {
		return nil, fmt.Errorf("цель пробы должна быть публичным IPv4 или http(s) URL")
	}
	norm := req.Normalized()
	return &norm, nil
}

func (s *Server) setIfaceProbe(w http.ResponseWriter, r *http.Request) {
	probe, err := parseProbeBody(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	device := r.PathValue("device")
	if probe != nil {
		for _, pool := range s.store.Pools() {
			if pool.DeviceName() == device {
				writeErr(w, fmt.Errorf("интерфейс %s управляется пулом %q, проба настраивается в настройках пула", device, pool.Name))
				return
			}
		}
	}
	if err := s.store.SetIfaceProbe(device, probe); err != nil {
		writeErr(w, err)
		return
	}
	s.store.LogEvent("ifaces", "probe", device+" probe "+map[bool]string{true: "set", false: "cleared"}[probe != nil])
	if probe != nil {
		s.engine.InvalidateProbeStatus(device)
	}
	s.engine.CheckBundlesNow()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "probe": probe})
}

func (s *Server) getWANProbe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": s.store.WANProbeEnabled(),
		"probe":   s.store.WANProbe(),
	})
}

func (s *Server) putWANProbe(w http.ResponseWriter, r *http.Request) {
	probe, err := parseProbeBody(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := s.store.SetWANProbe(probe); err != nil {
		writeErr(w, err)
		return
	}
	s.store.LogEvent("system", "wanprobe", "проверка канала провайдера "+map[bool]string{true: "включена", false: "выключена"}[probe != nil])
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "enabled": probe != nil})
}
