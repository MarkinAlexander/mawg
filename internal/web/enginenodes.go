package web

// Узлы движкового пула (60-док): список + задержки из clash_api движка,
// активный узел, ручной выбор в selector-режиме и перемер urltest.
// sing-box сам меряет пинги (urltest по ProbeTarget) - «пинг до цели
// пробы», не ICMP до эндпоинта; UI подписывает это честно.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"mawg/internal/singbox"
)

// engineGroupTag - тег группы пула в конфиге движка (build.go).
const engineGroupTag = "mawg-"

// engineGroup - группа пула в живом движке: менеджер + весь /proxies +
// группа mawg-<name>. Недижковый пул/остановленный движок/отсутствующая
// группа - честные ошибки, ответ уже записан.
func (s *Server) engineGroup(w http.ResponseWriter, r *http.Request, name string) (*singbox.Manager, map[string]singbox.ClashProxy, singbox.ClashProxy, bool) {
	pool, ok := s.store.Pool(name)
	if !ok {
		http.NotFound(w, r)
		return nil, nil, singbox.ClashProxy{}, false
	}
	if pool.Settings.EngineMode != engineMode {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "пул не движковый (узлы не в tun-движке)"})
		return nil, nil, singbox.ClashProxy{}, false
	}
	mgr, err := s.sb()
	if err != nil {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": err.Error()})
		return nil, nil, singbox.ClashProxy{}, false
	}
	proxies, err := mgr.ClashProxies(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return nil, nil, singbox.ClashProxy{}, false
	}
	group, ok := proxies[engineGroupTag+name]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "группы пула нет в движке (пул выключен или движок не пересобран)"})
		return nil, nil, singbox.ClashProxy{}, false
	}
	return mgr, proxies, group, true
}

// getPoolEngine - GET /api/v1/pools/{name}/engine: режим группы,
// активный узел, узлы с последним замером задержки.
func (s *Server) getPoolEngine(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	_, proxies, group, ok := s.engineGroup(w, r, name)
	if !ok {
		return
	}
	type node struct {
		Tag   string `json:"tag"`
		Delay int    `json:"delay"`
	}
	nodes := make([]node, 0, len(group.All))
	for _, tag := range group.All {
		nodes = append(nodes, node{Tag: tag, Delay: singbox.LastDelay(proxies[tag])})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"mode":  strings.ToLower(group.Type), // URLTest/Selector -> urltest/selector
		"now":   group.Now,
		"nodes": nodes,
	})
}

// putPoolEngineSelect - PUT /api/v1/pools/{name}/engine/select {"node":
// тег}: ручной выбор узла (только selector-режим, GOTCHAS 60-док: в
// urltest движок сам держит лучший пинг).
func (s *Server) putPoolEngineSelect(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	mgr, _, group, ok := s.engineGroup(w, r, name)
	if !ok {
		return
	}
	if strings.ToLower(group.Type) != "selector" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ручной выбор - только в режиме selector (сейчас urltest: движок сам держит лучший пинг)"})
		return
	}
	var req struct {
		Node string `json:"node"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.Node == "" {
		writeErr(w, errors.New("нужен узел: {node: тег}"))
		return
	}
	member := false
	for _, tag := range group.All {
		if tag == req.Node {
			member = true
			break
		}
	}
	if !member {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "узла нет в пуле"})
		return
	}
	if err := mgr.ClashSelect(r.Context(), engineGroupTag+name, req.Node); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	s.store.LogEvent(name, "engine-select", "ручной выбор узла движка: "+req.Node)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "now": req.Node})
}

// postPoolEngineRetest - POST /api/v1/pools/{name}/engine/retest:
// перемерить urltest-группу сейчас (цель - проба пула, как в конфиге
// движка; для Free - фиксированная).
func (s *Server) postPoolEngineRetest(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	mgr, _, group, ok := s.engineGroup(w, r, name)
	if !ok {
		return
	}
	pool, _ := s.store.Pool(name)
	testURL := pool.Settings.ProbeHost
	if pool.Free || testURL == "" {
		testURL = FreeProbeURL
	}
	// та же нормализация, что у менеджера при сборке конфига (752)
	if !strings.HasPrefix(testURL, "http://") && !strings.HasPrefix(testURL, "https://") {
		testURL = "http://www.gstatic.com/generate_204"
	}
	if strings.ToLower(group.Type) != "urltest" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "перемер - только для urltest (в selector выберите узел вручную)"})
		return
	}
	delays, err := mgr.ClashGroupDelay(r.Context(), engineGroupTag+name, testURL, 5*time.Second)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "delays": delays})
}
