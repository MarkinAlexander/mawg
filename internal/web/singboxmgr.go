package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"mawg/internal/links"
	"mawg/internal/singbox"
	"mawg/internal/store"
)

const engineMode = "singbox"

var sbMu sync.Mutex
var sbMgr *singbox.Manager
var sbMode string

// sb отдаёт менеджер движка под текущий режим из настроек (own/shared);
// при смене режима старый менеджер закрывается: свой процесс гасится,
// у чужого ядра снимается наш фрагмент.
func (s *Server) sb() (*singbox.Manager, error) {
	sbMu.Lock()
	defer sbMu.Unlock()
	mode := s.store.SingboxMode()
	if sbMgr != nil && sbMode == mode {
		return sbMgr, nil
	}
	cfg := singbox.RunConfig{Mode: mode}
	if mode == "shared" {
		cfg.SharedDir, cfg.SharedInit = s.sharedPaths()
	}
	m, err := singbox.NewManagerConfig(filepath.Join(s.store.Base(), "singbox"), cfg)
	if err != nil {
		return nil, err
	}
	if sbMgr != nil {
		sbMgr.Close()
	}
	sbMgr, sbMode = m, mode
	return sbMgr, nil
}

func (s *Server) sharedPaths() (dir, init string) {
	if s.backend.Name() == store.PlatformKeenetic {
		return "/opt/etc/sing-box", "/opt/etc/init.d/S99sing-box"
	}
	return "/etc/sing-box", "/etc/init.d/sing-box"
}

// postSingboxMode переключает режим движка (own|shared): сохраняется в
// настройки, менеджер пересоздаётся (старый закрыт: свой процесс погашен /
// у чужого ядра снят фрагмент), затем движок применяется в новом режиме.
func (s *Server) postSingboxMode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	if req.Mode != "own" && req.Mode != "shared" {
		writeErr(w, fmt.Errorf("режим должен быть own или shared"))
		return
	}
	if err := s.store.SetSingboxMode(req.Mode); err != nil {
		writeErr(w, err)
		return
	}
	out := map[string]any{"ok": "saved", "mode": req.Mode}
	if _, err := s.sb(); err != nil {
		out["warning"] = err.Error()
		writeJSON(w, http.StatusOK, out)
		return
	}
	skipped, err := s.applyEngine()
	out["skipped"] = skipped
	if err != nil {
		out["warning"] = err.Error()
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) enginePools() []store.Pool {
	var out []store.Pool
	for _, p := range s.store.Pools() {
		if p.Settings.EngineMode == engineMode {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *Server) allocTun() string {
	taken := map[string]bool{}
	for _, p := range s.enginePools() {
		taken[p.Settings.TunName] = true
	}
	for i := 1; ; i++ {
		name := singbox.TuneName(i)
		if !taken[name] {
			return name
		}
	}
}

// claimTun выделяет свободный tun и сразу создаёт пул под блокировкой:
// два параллельных «Создать» не должны получить одно имя (задвоение tun2).
// Отдельно задвоение запрещает store.CreatePool; отключённые и ждущие-lx
// пулы имя тоже занимают.
func (s *Server) claimTun(settings store.PoolSettings, name string) (store.Pool, error) {
	s.tunMu.Lock()
	defer s.tunMu.Unlock()
	settings.TunName = s.allocTun()
	pool, err := s.store.CreatePool(name, settings)
	if err != nil {
		return store.Pool{}, err
	}
	return pool, nil
}

func (s *Server) applyEngine() ([]string, error) {
	var specs []singbox.PoolSpec
	for _, p := range s.enginePools() {
		if p.Disabled {
			continue
		}
		nodes, err := s.readPoolNodes(p.Name)
		if err != nil {
			return nil, fmt.Errorf("пул %s: %v", p.Name, err)
		}
		idx := 1
		if n, err := strconv.Atoi(strings.TrimPrefix(p.Settings.TunName, "tun")); err == nil && n > 0 {
			idx = n
		}
		specs = append(specs, singbox.PoolSpec{
			Name: p.Name, Tun: p.Settings.TunName, TunIP: singbox.TuneIP(idx), Nodes: nodes,
			ProbeTarget:      p.Settings.ProbeHost,
			CheckIntervalSec: p.Settings.CheckIntervalSec,
			FailThreshold:    p.Settings.FailThreshold,
			CooldownMin:      p.Settings.CooldownMin,
			MaxRTTms:         p.Settings.MaxRTTms,
		})
	}
	seen := map[string]string{}
	for _, spec := range specs {
		if prev, dup := seen[spec.Tun]; dup {
			return nil, fmt.Errorf("tun %s назначен пулам %s и %s - исправьте настройки одного из них", spec.Tun, prev, spec.Name)
		}
		seen[spec.Tun] = spec.Name
	}
	mgr, err := s.sb()
	if err != nil {
		return nil, err
	}
	return mgr.Apply(specs)
}

func (s *Server) readPoolNodes(name string) ([]links.Node, error) {
	data, err := os.ReadFile(filepath.Join(s.store.Base(), "pools", name, "nodes.json"))
	if err != nil {
		return nil, err
	}
	var nodes []links.Node
	if err := json.Unmarshal(data, &nodes); err != nil {
		return nil, err
	}
	return nodes, nil
}

func (s *Server) writePoolNodes(name string, nodes []links.Node) error {
	data, err := json.MarshalIndent(nodes, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Join(s.store.Base(), "pools", name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "nodes.json"), data, 0o600)
}

func (s *Server) engineEnabled(r *http.Request) bool {
	name := r.PathValue("name")
	p, ok := s.store.Pool(name)
	return ok && p.Settings.EngineMode == engineMode
}

func (s *Server) postEnableEngine(w http.ResponseWriter, r *http.Request) {
	if err := s.store.SetPoolDisabled(r.PathValue("name"), false); err != nil {
		writeErr(w, err)
		return
	}
	if _, err := s.applyEngine(); err != nil {
		writeErr(w, err)
		return
	}
	s.store.LogEvent(r.PathValue("name"), "applied", "движок: включен")
	writeJSON(w, http.StatusOK, map[string]string{"ok": "enabled"})
}

func (s *Server) postDisableEngine(w http.ResponseWriter, r *http.Request) {
	if err := s.store.SetPoolDisabled(r.PathValue("name"), true); err != nil {
		writeErr(w, err)
		return
	}
	if _, err := s.applyEngine(); err != nil {
		writeErr(w, err)
		return
	}
	s.store.LogEvent(r.PathValue("name"), "applied", "движок: выключен")
	writeJSON(w, http.StatusOK, map[string]string{"ok": "disabled"})
}

func (s *Server) refreshSource(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	pool, ok := s.store.Pool(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	var req struct {
		Source string `json:"source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	src := strings.TrimSpace(req.Source)
	if src == "" {
		src = pool.Settings.Source
	}
	if src == "" {
		writeErr(w, fmt.Errorf("у пула нет сохранённого источника - вставьте ссылку"))
		return
	}
	res, err := resolveSource(r.Context(), src)
	if err != nil {
		writeErr(w, err)
		return
	}
	plan := sourcePlan{Warnings: res.Warnings}
	if src != pool.Settings.Source {
		st := pool.Settings
		st.Source = src
		if err := s.store.UpdatePool(name, st); err != nil {
			writeErr(w, err)
			return
		}
	}
	if pool.Settings.EngineMode == engineMode {
		if len(res.Nodes) == 0 {
			writeErr(w, fmt.Errorf("в источнике нет узлов"))
			return
		}
		if err := s.writePoolNodes(name, res.Nodes); err != nil {
			writeErr(w, err)
			return
		}
		skipped, err := s.applyEngine()
		plan.Skipped = skipped
		if err != nil {
			plan.Warnings = append(plan.Warnings, err.Error())
		}
		plan.Pool = name
		writeJSON(w, http.StatusOK, plan)
		return
	}
	for _, c := range pool.Configs {
		if err := s.store.RemoveConfig(name, c.File); err != nil {
			writeErr(w, err)
			return
		}
	}
	ncs, engineNodes, skipped := buildNativeConfigs(res.Nodes)
	plan.Skipped = append(plan.Skipped, skipped...)
	if len(ncs) > 0 {
		added, dupes, err := s.store.AddConfigs(name, ncs)
		if err != nil {
			writeErr(w, err)
			return
		}
		plan.Pool, plan.Added, plan.Duplicates = name, added, dupes
		plan.Engine = engineNodes
		if p, ok := s.store.Pool(name); ok && len(p.Configs) > 0 {
			if err := s.engine.SetActive(name, p.Configs[0].File); err != nil {
				plan.Warnings = append(plan.Warnings, "не активирован: "+err.Error())
			}
		}
		s.engine.CheckNow(name)
	}
	writeJSON(w, http.StatusOK, plan)
}
