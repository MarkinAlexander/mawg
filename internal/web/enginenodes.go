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

	"mawg/internal/links"
	"mawg/internal/singbox"
)

// engineGroupTag - тег группы пула в конфиге движка (build.go).
const engineGroupTag = "mawg-"

// engineGroup - группа пула в живом движке (менеджер выбирается ядром
// пула: sing-box | mihomo): менеджер + весь /proxies + группа
// mawg-<name>. Недижковый пул/остановленный движок/отсутствующая группа -
// честные ошибки, ответ уже записан.
func (s *Server) engineGroup(w http.ResponseWriter, r *http.Request, name string) (engineManager, map[string]singbox.ClashProxy, singbox.ClashProxy, bool) {
	pool, ok := s.store.Pool(name)
	if !ok {
		http.NotFound(w, r)
		return nil, nil, singbox.ClashProxy{}, false
	}
	if pool.Settings.EngineMode != engineMode {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "пул не движковый (узлы не в tun-движке)"})
		return nil, nil, singbox.ClashProxy{}, false
	}
	mgr, err := s.engineMgr(pool)
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
// активный узел, узлы с последним замером задержки. Каждому узлу -
// его тип и транспорт (слово владельца 2026-10-17: у подключений
// разнобой - vless по tcp/grpc/xhttp, показывать, что это, не только пинг).
func (s *Server) getPoolEngine(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	_, proxies, group, ok := s.engineGroup(w, r, name)
	if !ok {
		return
	}
	type node struct {
		Tag       string `json:"tag"`
		Delay     int    `json:"delay"`
		Type      string `json:"type,omitempty"`
		Transport string `json:"transport,omitempty"`
		Off       bool   `json:"off,omitempty"`
	}
	byName := map[string]links.Node{}
	if ns, err := s.readPoolNodes(name); err == nil {
		for _, n := range ns {
			byName[n.ConfName()] = n
		}
	}
	nodes := make([]node, 0, len(group.All))
	for _, tag := range group.All {
		nd := node{Tag: tag, Delay: singbox.LastDelay(proxies[tag])}
		if n, found := byName[strings.TrimPrefix(tag, engineGroupTag+name+"|")]; found {
			nd.Type = strings.ToUpper(n.Type)
			nd.Transport = strings.TrimPrefix(transportBadgeOf(n), "/")
		}
		nodes = append(nodes, nd)
	}
	// выключенные узлы видны в конце списка с пометкой (в конфиг движка
	// они не попадают) - слово владельца 2026-10-17: исключать, как у
	// конфигов, но не терять из виду
	if pool, ok := s.store.Pool(name); ok {
		for cn, n := range byName {
			if !pool.Settings.NodeOff[cn] {
				continue
			}
			nodes = append(nodes, node{
				Tag: engineGroupTag + name + "|" + cn, Delay: 0,
				Type: strings.ToUpper(n.Type), Transport: strings.TrimPrefix(transportBadgeOf(n), "/"),
				Off: true,
			})
		}
	}
	// groupMode - режим по настройкам mawg (rotate - это selector-группа,
	// которой управляет таймер mawg; группе движка он не виден)
	gm := ""
	if pool, ok := s.store.Pool(name); ok {
		gm = pool.Settings.GroupMode
	}
	if gm == "" {
		gm = "urltest"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"mode":      strings.ToLower(group.Type), // URLTest/Selector -> urltest/selector
		"groupMode": gm,
		"now":       group.Now,
		"nodes":     nodes,
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
	mgr, _, _, ok := s.engineGroup(w, r, name)
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
	delays, err := mgr.ClashGroupDelay(r.Context(), engineGroupTag+name, testURL, 5*time.Second)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "delays": delays})
}

// postPoolEngineToggle - POST /api/v1/pools/{name}/engine/toggle {"node":
// тег}: выключить/вернуть узел подписки. Исключённые не попадают в конфиг
// движка, но остаются в пуле и видны в списке с пометкой (слово владельца
// 2026-10-17: позволить исключать, как у конфигов).
func (s *Server) postPoolEngineToggle(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	pool, ok := s.store.Pool(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if pool.Settings.EngineMode != engineMode {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "пул не движковый"})
		return
	}
	var req struct {
		Node string `json:"node"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.Node == "" {
		writeErr(w, errors.New("нужен узел: {node: тег}"))
		return
	}
	cn := strings.TrimPrefix(req.Node, engineGroupTag+name+"|")
	nodes, err := s.readPoolNodes(name)
	if err != nil {
		writeErr(w, err)
		return
	}
	known := false
	for _, n := range nodes {
		if n.ConfName() == cn {
			known = true
			break
		}
	}
	if !known {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "узла нет в пуле"})
		return
	}
	st := pool.Settings
	if st.NodeOff[cn] {
		delete(st.NodeOff, cn)
	} else {
		enabled := 0
		for _, n := range nodes {
			if !st.NodeOff[n.ConfName()] {
				enabled++
			}
		}
		if enabled <= 1 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "нельзя выключить последний узел пула"})
			return
		}
		if st.NodeOff == nil {
			st.NodeOff = map[string]bool{}
		}
		st.NodeOff[cn] = true
	}
	if err := s.store.UpdatePool(name, st); err != nil {
		writeErr(w, err)
		return
	}
	if _, err := s.applyEngine(); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	verb := "возвращён в пул"
	if st.NodeOff[cn] {
		verb = "исключён из пула"
	}
	s.store.LogEvent(name, "engine-select", "узел "+cn+": "+verb)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "off": st.NodeOff[cn]})
}
