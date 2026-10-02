package web

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"mawg/internal/magitrickle"
	"mawg/internal/platform"
	"mawg/internal/platform/keenetic"
	"mawg/internal/provision"
	"mawg/internal/rotator"
	"mawg/internal/store"
	"mawg/internal/wgconf"
)

//go:embed ui/*
var uiFS embed.FS

type Server struct {
	store   *store.Store
	engine  *rotator.Engine
	backend platform.Backend
	mt      *magitrickle.Client
	version string
}

func New(st *store.Store, e *rotator.Engine, b platform.Backend, mt *magitrickle.Client, version string) *Server {
	return &Server{store: st, engine: e, backend: b, mt: mt, version: version}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/status", s.getStatus)
	mux.HandleFunc("GET /api/v1/slots", s.getSlots)
	mux.HandleFunc("GET /api/v1/ifaces", s.getIfaces)
	mux.HandleFunc("POST /api/v1/ifaces/{device}/mode", s.setIfaceMode)
	mux.HandleFunc("PUT /api/v1/ifaces/{device}/probe", s.setIfaceProbe)
	mux.HandleFunc("GET /api/v1/wanprobe", s.getWANProbe)
	mux.HandleFunc("PUT /api/v1/wanprobe", s.putWANProbe)
	mux.HandleFunc("POST /api/v1/pools/{name}/rename", s.renamePool)
	mux.HandleFunc("GET /api/v1/events", s.getEvents)
	mux.HandleFunc("POST /api/v1/pools", s.createPool)
	mux.HandleFunc("PUT /api/v1/pools/{name}", s.updatePool)
	mux.HandleFunc("DELETE /api/v1/pools/{name}", s.deletePool)
	mux.HandleFunc("POST /api/v1/pools/{name}/configs", s.uploadConfigs)
	mux.HandleFunc("DELETE /api/v1/pools/{name}/configs/{file}", s.deleteConfig)
	mux.HandleFunc("POST /api/v1/pools/{name}/configs/{file}/enable", s.enableConfig)
	mux.HandleFunc("POST /api/v1/pools/{name}/configs/{file}/move", s.moveConfig)
	mux.HandleFunc("POST /api/v1/pools/{name}/rotate", s.postRotate)
	mux.HandleFunc("POST /api/v1/pools/{name}/check", s.postCheck)
	mux.HandleFunc("POST /api/v1/pools/{name}/enable", s.postEnable)
	mux.HandleFunc("POST /api/v1/pools/{name}/disable", s.postDisable)
	mux.HandleFunc("POST /api/v1/pools/{name}/activate", s.postActivate)
	mux.HandleFunc("GET /api/v1/magitrickle", s.getMagitrickle)
	mux.HandleFunc("GET /api/v1/system/check", s.getSystemCheck)
	mux.HandleFunc("POST /api/v1/system/install", s.postSystemInstall)

	mux.HandleFunc("GET /api/v1/bundles", s.getBundles)
	mux.HandleFunc("POST /api/v1/bundles", s.createBundle)
	mux.HandleFunc("PUT /api/v1/bundles/{name}", s.updateBundle)
	mux.HandleFunc("DELETE /api/v1/bundles/{name}", s.deleteBundle)

	mux.HandleFunc("GET /api/v1/mt/groups", s.mtGetGroups)
	mux.HandleFunc("GET /api/v1/mt/interfaces", s.mtGetInterfaces)
	mux.HandleFunc("GET /api/v1/mt/presets", s.mtGetPresets)
	mux.HandleFunc("POST /api/v1/mt/groups", s.mtCreateGroup)
	mux.HandleFunc("PUT /api/v1/mt/groups/{id}", s.mtUpdateGroup)
	mux.HandleFunc("POST /api/v1/mt/groups/order", s.mtGroupsOrder)
	mux.HandleFunc("POST /api/v1/mt/groups/{id}/rules/order", s.mtRulesOrder)
	mux.HandleFunc("POST /api/v1/mt/groups/{id}/enable", s.mtToggleGroup)
	mux.HandleFunc("DELETE /api/v1/mt/groups/{id}", s.mtDeleteGroup)
	mux.HandleFunc("POST /api/v1/mt/groups/{id}/rules", s.mtCreateRule)
	mux.HandleFunc("PUT /api/v1/mt/groups/{id}/rules/{rid}", s.mtUpdateRule)
	mux.HandleFunc("POST /api/v1/mt/groups/{id}/rules/{rid}/enable", s.mtToggleRule)
	mux.HandleFunc("DELETE /api/v1/mt/groups/{id}/rules/{rid}", s.mtDeleteRule)
	mux.HandleFunc("POST /api/v1/mt/presets/{id}/apply", s.mtApplyPreset)

	sub, err := fs.Sub(uiFS, "ui")
	if err != nil {
		panic(err)
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))
	return mux
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
}

func (s *Server) getStatus(w http.ResponseWriter, r *http.Request) {
	pools := s.store.Pools()
	type poolView struct {
		Name           string             `json:"name"`
		Platform       string             `json:"platform"`
		Device         string             `json:"device"`
		Slot           string             `json:"slot,omitempty"`
		Mode           string             `json:"mode"`
		ActiveFile     string             `json:"activeFile"`
		ActiveEndpoint string             `json:"activeEndpoint"`
		LastResult     string             `json:"lastResult"`
		LastError      string             `json:"lastError"`
		Rotations      int                `json:"rotations"`
		Disabled       bool               `json:"disabled"`
		ConsecFails    int                `json:"consecFails"`
		Settings       store.PoolSettings `json:"settings"`
		Configs        []configView       `json:"configs"`
	}
	out := struct {
		Version     string     `json:"version"`
		Platform    string     `json:"platform"`
		BackendName string     `json:"backendName"`
		Pools       []poolView `json:"pools"`
	}{Version: s.version, Platform: s.backend.Name(), BackendName: s.backend.Name(), Pools: []poolView{}}

	for _, p := range pools {
		st := s.store.State(p.Name)
		view := poolView{
			Name: p.Name, Platform: p.Settings.Platform, Device: p.DeviceName(),
			Slot: p.Settings.KeeneticSlot, Mode: st.Mode, ActiveFile: st.ActiveFile,
			LastResult: st.LastResult, LastError: st.LastError,
			Rotations: st.Rotations, Disabled: p.Disabled, ConsecFails: st.ConsecFails, Settings: p.Settings,
			Configs: []configView{},
		}
		now := time.Now().Unix()
		for _, c := range p.Configs {
			cv := configView{File: c.File, Original: c.Original, Endpoint: c.Endpoint, Enabled: c.Enabled}
			if until, ok := st.Cooldowns[c.File]; ok {
				cv.CoolFor = until - now
			}
			if c.File == st.ActiveFile {
				view.ActiveEndpoint = c.Endpoint
			}
			view.Configs = append(view.Configs, cv)
		}
		out.Pools = append(out.Pools, view)
	}
	writeJSON(w, http.StatusOK, out)
}

type configView struct {
	File     string `json:"file"`
	Original string `json:"original"`
	Endpoint string `json:"endpoint"`
	Enabled  bool   `json:"enabled"`
	CoolFor  int64  `json:"coolForSec,omitempty"`
}

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
	Device       string            `json:"device"`
	Slot         string            `json:"slot,omitempty"`
	Description  string            `json:"description,omitempty"`
	Mode         string            `json:"mode"`
	Pool         string            `json:"pool,omitempty"`
	LinkUp       bool              `json:"linkUp"`
	Connected    bool              `json:"connected"`
	HandshakeAgo int               `json:"handshakeAgo"`
	Probe        *store.ProbeConfig `json:"probe,omitempty"`
	ProbeStatus  string            `json:"probeStatus,omitempty"`
}

func (s *Server) allIfaces(ctx context.Context) []platform.SlotInfo {
	slots, err := s.backend.Slots()
	if err != nil {
		return nil
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
	slots := s.allIfaces(r.Context())
	if slots == nil {
		writeErr(w, fmt.Errorf("не удалось получить список интерфейсов"))
		return
	}
	poolByDevice := map[string]string{}
	for _, p := range s.store.Pools() {
		poolByDevice[p.DeviceName()] = p.Name
	}
	modes := s.store.IfaceModes()
	out := make([]ifaceView, 0, len(slots))
	for _, sl := range slots {
		v := ifaceView{
			Device: sl.Device, Slot: sl.ID, Description: sl.Description,
			LinkUp: sl.LinkUp, Connected: sl.Connected, HandshakeAgo: -1,
		}
		if pool, ok := poolByDevice[sl.Device]; ok {
			v.Mode = store.IfaceManaged
			v.Pool = pool
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

	var wg sync.WaitGroup
	statuses := make([]string, len(out))
	for i := range out {
		if out[i].Mode != store.IfaceExternal || out[i].Probe == nil {
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			statuses[i] = s.engine.DeviceProbeStatus(out[i].Device)
		}(i)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(4 * time.Second):
	}
	for i := range out {
		out[i].ProbeStatus = statuses[i]
	}
	writeJSON(w, http.StatusOK, map[string]any{"interfaces": out})
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

func (s *Server) renamePool(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeErr(w, fmt.Errorf("нужно имя"))
		return
	}
	old := r.PathValue("name")
	if s.backend.Name() != store.PlatformKeenetic {
		writeErr(w, fmt.Errorf("переименование на этой платформе не поддерживается (имя = имя системного интерфейса)"))
		return
	}
	pool, ok := s.store.Pool(old)
	if !ok {
		http.NotFound(w, r)
		return
	}
	renamed, err := s.store.RenamePool(old, req.Name)
	if err != nil {
		writeErr(w, err)
		return
	}
	if kb, ok := s.backend.(*keenetic.Backend); ok && pool.Settings.KeeneticSlot != "" {
		kb.Ndmc("interface " + pool.Settings.KeeneticSlot + " description " + renamed.Name)
		kb.Ndmc("system configuration save")
	}
	s.store.LogEvent(renamed.Name, "rename", "пул переименован из "+old)
	s.engine.CheckNow(renamed.Name)
	writeJSON(w, http.StatusOK, renamed)
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
}

func (s *Server) bundleToView(b store.Bundle) bundleView {
	v := bundleView{Bundle: b, MemberDevices: map[string]string{}, CurrentMember: s.store.BundleState(b.Name).Member}
	devices, err := s.bundleMembersResolve(b.Members)
	if err == nil {
		v.MemberDevices = devices
	}
	return v
}

func (s *Server) getBundles(w http.ResponseWriter, r *http.Request) {
	out := []bundleView{}
	for _, b := range s.store.Bundles() {
		out = append(out, s.bundleToView(b))
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
	writeJSON(w, http.StatusOK, s.bundleToView(b))
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
	writeJSON(w, http.StatusOK, s.bundleToView(b))
}

func (s *Server) deleteBundle(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteBundle(r.PathValue("name")); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "deleted"})
}

func (s *Server) validFallback(name, fallback string) error {
	if fallback == "" || fallback == store.FallbackDirect || fallback == store.FallbackHold {
		return nil
	}
	fbName, ok := store.FallbackPoolName(fallback)
	if !ok {
		return fmt.Errorf("fallback must be direct, hold or pool:<имя>")
	}
	if fbName == name {
		return fmt.Errorf("фоллбек на самого себя")
	}
	seen := map[string]bool{name: true}
	cur := fbName
	for i := 0; i < 16; i++ {
		if seen[cur] {
			return fmt.Errorf("цикл фоллбеков через пул %s", cur)
		}
		seen[cur] = true
		p, ok := s.store.Pool(cur)
		if !ok {
			return fmt.Errorf("пул %q не найден", cur)
		}
		next, ok := store.FallbackPoolName(p.Settings.Fallback)
		if !ok {
			return nil
		}
		cur = next
	}
	return fmt.Errorf("слишком длинная цепочка фоллбеков")
}

func (s *Server) createPool(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name         string `json:"name"`
		KeeneticSlot string `json:"keeneticSlot"`
		OpenwrtProto string `json:"openwrtProto"`
		Fallback     string `json:"fallback"`
		ProbeHost    string `json:"probeHost"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	settings := store.PoolSettings{
		Platform:     s.backend.Name(),
		KeeneticSlot: req.KeeneticSlot,
		Fallback:     req.Fallback,
		ProbeHost:    req.ProbeHost,
	}
	if s.backend.Name() == store.PlatformOpenwrt {
		settings.OpenwrtProto = req.OpenwrtProto
		if settings.OpenwrtProto == "" {
			settings.OpenwrtProto = "wireguard"
		}
	}
	if err := s.validFallback(req.Name, settings.Fallback); err != nil {
		writeErr(w, err)
		return
	}
	pool, err := s.store.CreatePool(req.Name, settings)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pool)
}

func (s *Server) updatePool(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	pool, ok := s.store.Pool(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	var patch store.PoolSettings
	var maxRtt *int
	{
		var raw struct {
			store.PoolSettings
			MaxRTTms *int `json:"maxRttMs"`
		}
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			writeErr(w, err)
			return
		}
		patch = raw.PoolSettings
		maxRtt = raw.MaxRTTms
	}
	merged := pool.Settings
	if patch.ProbeHost != "" {
		merged.ProbeHost = patch.ProbeHost
	}
	if patch.CheckIntervalSec > 0 {
		merged.CheckIntervalSec = patch.CheckIntervalSec
	}
	if patch.FailThreshold > 0 {
		merged.FailThreshold = patch.FailThreshold
	}
	if patch.CooldownMin > 0 {
		merged.CooldownMin = patch.CooldownMin
	}
	if patch.Fallback != "" {
		merged.Fallback = patch.Fallback
	}
	if patch.Keepalive > 0 {
		merged.Keepalive = patch.Keepalive
	}
	if maxRtt != nil {
		merged.MaxRTTms = *maxRtt
	}
	if patch.MagitrickleGroupID != "" {
		merged.MagitrickleGroupID = patch.MagitrickleGroupID
	}
	if err := s.validFallback(name, merged.Fallback); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.store.UpdatePool(name, merged); err != nil {
		writeErr(w, err)
		return
	}
	s.engine.CheckNow(name)
	writeJSON(w, http.StatusOK, map[string]string{"ok": "updated"})
}

func (s *Server) deletePool(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := s.engine.PoolDown(name); err == nil {
		// interface left down, slot content removed below
	}
	if err := s.store.DeletePool(name); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "deleted"})
}

func (s *Server) uploadConfigs(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, ok := s.store.Pool(name); !ok {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeErr(w, err)
		return
	}
	var all []wgconf.NamedConfig
	var errors []string
	for _, headers := range r.MultipartForm.File {
		for _, fh := range headers {
			f, err := fh.Open()
			if err != nil {
				errors = append(errors, fh.Filename+": "+err.Error())
				continue
			}
			data, _ := io.ReadAll(f)
			f.Close()
			parsed, err := wgconf.IngestFile(fh.Filename, data)
			if err != nil {
				errors = append(errors, err.Error())
				continue
			}
			all = append(all, parsed...)
		}
	}
	unique, dupes := wgconf.Dedupe(all)
	added, storeDupes, err := s.store.AddConfigs(name, unique)
	if err != nil {
		writeErr(w, err)
		return
	}
	dupes = append(dupes, storeDupes...)
	s.engine.CheckNow(name)
	writeJSON(w, http.StatusOK, map[string]any{"added": added, "duplicates": dupes, "errors": errors})
}

func (s *Server) deleteConfig(w http.ResponseWriter, r *http.Request) {
	if err := s.store.RemoveConfig(r.PathValue("name"), r.PathValue("file")); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "deleted"})
}

func (s *Server) enableConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if err := s.store.SetConfigEnabled(r.PathValue("name"), r.PathValue("file"), req.Enabled); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "ok"})
}

func (s *Server) moveConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Delta int `json:"delta"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.Delta != -1 && req.Delta != 1 {
		writeErr(w, fmt.Errorf("delta must be -1 or 1"))
		return
	}
	if err := s.store.MoveConfig(r.PathValue("name"), r.PathValue("file"), req.Delta); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "moved"})
}

func (s *Server) postRotate(w http.ResponseWriter, r *http.Request) {
	if err := s.engine.RotateNow(r.PathValue("name")); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "rotating"})
}

func (s *Server) postCheck(w http.ResponseWriter, r *http.Request) {
	s.engine.CheckNow(r.PathValue("name"))
	writeJSON(w, http.StatusOK, map[string]string{"ok": "checking"})
}

func (s *Server) postEnable(w http.ResponseWriter, r *http.Request) {
	if err := s.engine.EnablePool(r.PathValue("name")); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "enabled"})
}

func (s *Server) postDisable(w http.ResponseWriter, r *http.Request) {
	if err := s.engine.DisablePool(r.PathValue("name")); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "disabled"})
}

func (s *Server) postActivate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		File string `json:"file"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.File == "" {
		writeErr(w, err)
		return
	}
	if err := s.engine.SetActive(r.PathValue("name"), req.File); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "activated"})
}

func (s *Server) getMagitrickle(w http.ResponseWriter, r *http.Request) {
	if s.mt == nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false})
		return
	}
	groups, err := s.mt.Groups(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"available": true, "groups": groups})
}

func (s *Server) systemCheck() provision.Result {
	if s.backend.Name() == store.PlatformKeenetic {
		if kb, ok := s.backend.(*keenetic.Backend); ok {
			return provision.CheckKeenetic(kb.Ndmc)
		}
	}
	return provision.CheckOpenwrt()
}

func (s *Server) getSystemCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.systemCheck())
}

func (s *Server) postSystemInstall(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		writeErr(w, fmt.Errorf("missing id"))
		return
	}
	check := s.systemCheck()
	for _, item := range check.Items {
		if item.ID != req.ID {
			continue
		}
		if item.Installed || item.Action == "" {
			writeErr(w, fmt.Errorf("ничего не требуется для %s", item.ID))
			return
		}
		out, err := provision.Install(item.Action)
		s.store.LogEvent("system", "install", item.ID+": "+firstLine(out))
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error(), "output": out})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "output": out})
		return
	}
	writeErr(w, fmt.Errorf("компонент %s не найден", req.ID))
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i > 0 {
		return s[:i]
	}
	return s
}

func (s *Server) mtClient() *magitrickle.Client {
	if s.mt != nil {
		return s.mt
	}
	return magitrickle.New("http://127.0.0.1:8080")
}

func (s *Server) mtInterfaceTitles() map[string]string {
	titles := map[string]string{}
	for _, p := range s.store.Pools() {
		titles[p.DeviceName()] = p.Name
	}
	if slots, err := s.backend.Slots(); err == nil {
		for _, sl := range slots {
			if sl.Description == "" || sl.Device == "" {
				continue
			}
			if _, exists := titles[sl.Device]; exists {
				continue
			}
			if s.store.IfaceModes()[sl.Device] == store.IfaceExternal {
				titles[sl.Device] = sl.Description
			}
		}
	}
	return titles
}

func (s *Server) mtGetGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.mtClient().GroupsWithRules(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "error": err.Error()})
		return
	}
	titles := s.mtInterfaceTitles()
	type groupView struct {
		magitrickle.Group
		InterfaceTitle string `json:"interfaceTitle,omitempty"`
	}
	out := make([]groupView, 0, len(groups))
	seen := map[string]bool{}
	for _, g := range groups {
		gv := groupView{Group: g}
		if t, ok := titles[g.Interface]; ok && t != g.Interface {
			gv.InterfaceTitle = t
		}
		seen[g.Interface] = true
		out = append(out, gv)
	}
	writeJSON(w, http.StatusOK, map[string]any{"available": true, "groups": out})
}

func (s *Server) mtGetInterfaces(w http.ResponseWriter, r *http.Request) {
	titles := s.mtInterfaceTitles()
	groups, err := s.mtClient().GroupsWithRules(r.Context())
	if err == nil {
		for _, g := range groups {
			if _, ok := titles[g.Interface]; !ok {
				titles[g.Interface] = ""
			}
		}
	}
	type ifaceView struct {
		Device string `json:"device"`
		Title  string `json:"title,omitempty"`
	}
	out := []ifaceView{}
	for dev, title := range titles {
		out = append(out, ifaceView{Device: dev, Title: title})
	}
	writeJSON(w, http.StatusOK, map[string]any{"interfaces": out})
}

func (s *Server) mtGetPresets(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"presets": magitrickle.Presets})
}

func (s *Server) mtCreateGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string `json:"name"`
		Interface string `json:"interface"`
		Color     string `json:"color"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" || req.Interface == "" {
		writeErr(w, fmt.Errorf("нужны name и interface"))
		return
	}
	color := req.Color
	if color == "" {
		color = "#4a9eff"
	}
	g, err := s.mtClient().CreateGroup(r.Context(), magitrickle.Group{
		Name: req.Name, Interface: req.Interface, Color: color, Enable: true,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.store.LogEvent("rules", "magitrickle", "создана группа "+req.Name+" на "+req.Interface)
	writeJSON(w, http.StatusOK, g)
}

// magitrickle 0.8.2 не пересоздаёт iptables-цепочку при одиночном PUT
// группы, поэтому все правки идут массовым сохранением всего списка.
func (s *Server) mtApplyGroups(ctx context.Context, mutate func(groups []magitrickle.Group) bool) error {
	return s.mtClient().MutateGroups(ctx, mutate)
}

func (s *Server) mtUpdateGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string `json:"name"`
		Color     string `json:"color"`
		Interface string `json:"interface"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	id := r.PathValue("id")
	found := false
	err := s.mtApplyGroups(r.Context(), func(groups []magitrickle.Group) bool {
		for i := range groups {
			g := &groups[i]
			if g.ID != id {
				continue
			}
			if req.Name != "" {
				g.Name = req.Name
			}
			if req.Color != "" {
				g.Color = req.Color
			}
			if req.Interface != "" {
				g.Interface = req.Interface
			}
			found = true
			return true
		}
		return false
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	s.store.LogEvent("rules", "magitrickle", "изменена группа "+req.Name)
	writeJSON(w, http.StatusOK, map[string]string{"ok": "updated"})
}

func reorderIDs[T any](items []T, idOf func(T) string, ids []string) ([]T, bool) {
	if len(ids) != len(items) {
		return nil, false
	}
	byID := map[string]T{}
	for _, it := range items {
		byID[idOf(it)] = it
	}
	out := make([]T, 0, len(items))
	for _, id := range ids {
		it, ok := byID[id]
		if !ok {
			return nil, false
		}
		out = append(out, it)
		delete(byID, id)
	}
	if len(byID) > 0 {
		return nil, false
	}
	return out, true
}

func (s *Server) mtGroupsOrder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	client := s.mtClient()
	groups, err := client.GroupsWithRules(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	ordered, ok := reorderIDs(groups, func(g magitrickle.Group) string { return g.ID }, req.IDs)
	if !ok {
		writeErr(w, fmt.Errorf("список ids не совпадает с текущими группами"))
		return
	}
	if err := client.UpdateGroups(r.Context(), ordered, true); err != nil {
		writeErr(w, err)
		return
	}
	s.store.LogEvent("rules", "magitrickle", "изменён порядок групп")
	writeJSON(w, http.StatusOK, map[string]string{"ok": "reordered"})
}

func (s *Server) mtRulesOrder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	id := r.PathValue("id")
	found, mismatch := false, false
	err := s.mtApplyGroups(r.Context(), func(groups []magitrickle.Group) bool {
		for i := range groups {
			if groups[i].ID != id {
				continue
			}
			ordered, ok := reorderIDs(groups[i].Rules, func(rl magitrickle.Rule) string { return rl.ID }, req.IDs)
			if !ok {
				mismatch = true
				return false
			}
			groups[i].Rules = ordered
			found = true
			return true
		}
		return false
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	if mismatch {
		writeErr(w, fmt.Errorf("список ids не совпадает с правилами группы"))
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	s.store.LogEvent("rules", "magitrickle", "изменён порядок правил в группе")
	writeJSON(w, http.StatusOK, map[string]string{"ok": "reordered"})
}

func (s *Server) mtToggleGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enable bool `json:"enable"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	id := r.PathValue("id")
	found := false
	err := s.mtApplyGroups(r.Context(), func(groups []magitrickle.Group) bool {
		for i := range groups {
			if groups[i].ID != id {
				continue
			}
			found = true
			if groups[i].Enable == req.Enable {
				return false
			}
			groups[i].Enable = req.Enable
			return true
		}
		return false
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "ok"})
}

func (s *Server) mtDeleteGroup(w http.ResponseWriter, r *http.Request) {
	if err := s.mtClient().DeleteGroup(r.Context(), r.PathValue("id")); err != nil {
		writeErr(w, err)
		return
	}
	s.store.LogEvent("rules", "magitrickle", "удалена группа "+r.PathValue("id"))
	writeJSON(w, http.StatusOK, map[string]string{"ok": "deleted"})
}

var ruleTypes = map[string]bool{
	"domain": true, "namespace": true, "wildcard": true, "regex": true, "subnet": true,
}

func (s *Server) mtCreateRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type string `json:"type"`
		Rule string `json:"rule"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !ruleTypes[req.Type] || req.Rule == "" {
		writeErr(w, fmt.Errorf("нужны type (domain/namespace/wildcard/regex/subnet) и rule"))
		return
	}
	rule, err := s.mtClient().CreateRule(r.Context(), r.PathValue("id"), magitrickle.Rule{
		Type: req.Type, Rule: req.Rule, Name: req.Name, Enable: true,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

func (s *Server) mtUpdateRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type string `json:"type"`
		Rule string `json:"rule"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !ruleTypes[req.Type] || req.Rule == "" {
		writeErr(w, fmt.Errorf("нужны type (domain/namespace/wildcard/regex/subnet) и rule"))
		return
	}
	client := s.mtClient()
	g, err := client.GroupByID(r.Context(), r.PathValue("id"), true)
	if err != nil {
		writeErr(w, err)
		return
	}
	ruleID := r.PathValue("rid")
	for _, rl := range g.Rules {
		if rl.ID != ruleID {
			continue
		}
		rl.Type = req.Type
		rl.Rule = req.Rule
		rl.Name = req.Name
		if err := client.UpdateRule(r.Context(), g.ID, rl); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"ok": "updated"})
		return
	}
	http.NotFound(w, r)
}

func (s *Server) mtToggleRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enable bool `json:"enable"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	client := s.mtClient()
	groups, err := client.GroupsWithRules(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	groupID, ruleID := r.PathValue("id"), r.PathValue("rid")
	for _, g := range groups {
		if g.ID != groupID {
			continue
		}
		for _, rl := range g.Rules {
			if rl.ID != ruleID {
				continue
			}
			rl.Enable = req.Enable
			if err := client.UpdateRule(r.Context(), groupID, rl); err != nil {
				writeErr(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"ok": "ok"})
			return
		}
	}
	http.NotFound(w, r)
}

func (s *Server) mtDeleteRule(w http.ResponseWriter, r *http.Request) {
	if err := s.mtClient().DeleteRule(r.Context(), r.PathValue("id"), r.PathValue("rid")); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "deleted"})
}

func (s *Server) mtApplyPreset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Interface string `json:"interface"`
		Name      string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Interface == "" {
		writeErr(w, fmt.Errorf("нужен interface"))
		return
	}
	preset, ok := magitrickle.PresetByID(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	name := req.Name
	if name == "" {
		name = preset.ID
	}
	client := s.mtClient()
	g, err := client.CreateGroup(r.Context(), magitrickle.Group{
		Name: name, Interface: req.Interface, Enable: true,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	for _, rl := range preset.Rules {
		if _, err := client.CreateRule(r.Context(), g.ID, rl); err != nil {
			writeErr(w, fmt.Errorf("группа создана, правило %s не добавилось: %v", rl.Rule, err))
			return
		}
	}
	s.store.LogEvent("rules", "magitrickle", "применён шаблон "+preset.ID+" на "+req.Interface)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "groupId": g.ID, "rules": len(preset.Rules)})
}
