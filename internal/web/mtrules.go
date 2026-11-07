package web

// Хендлеры/логика правил MagiTrickle (из server.go).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"mawg/internal/magitrickle"
)

var ruleTypes = map[string]bool{
	"domain": true, "namespace": true, "wildcard": true, "regex": true, "subnet": true,
}

func (s *Server) mtImportRules(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text     string `json:"text"`
		Type     string `json:"type"`
		ToSecond bool   `json:"toSecond"`
		Enable   *bool  `json:"enable"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Text) == "" {
		writeErr(w, fmt.Errorf("пустой список"))
		return
	}
	if req.Type != "auto" && !ruleTypes[req.Type] {
		writeErr(w, fmt.Errorf("неизвестный тип правила"))
		return
	}
	enabled := true
	if req.Enable != nil {
		enabled = *req.Enable
	}
	parsed, badCount, reasons := magitrickle.ParseImport(req.Text, req.Type, true, req.ToSecond)
	if len(parsed) == 0 {
		msg := "в списке не нашлось правил"
		if len(reasons) > 0 {
			msg += ": " + strings.Join(reasons, "; ")
		}
		writeErr(w, fmt.Errorf("%s", msg))
		return
	}
	for i := range parsed {
		parsed[i].Enable = enabled
	}
	id := r.PathValue("id")
	added, dup, found := 0, 0, false
	groupName := id
	err := s.mtApplyGroups(r.Context(), func(groups []magitrickle.Group) bool {
		for i := range groups {
			g := &groups[i]
			if g.ID != id {
				continue
			}
			found = true
			groupName = g.Name
			existing := map[string]bool{}
			for _, rl := range g.Rules {
				existing[rl.Type+" "+rl.Rule] = true
			}
			for _, rl := range parsed {
				if existing[rl.Type+" "+rl.Rule] {
					dup++
					continue
				}
				g.Rules = append(g.Rules, rl)
				added++
			}
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
	if added > 0 {
		s.store.LogEvent("rules", "magitrickle", fmt.Sprintf("в группу %s импортировано правил: %d", groupName, added))
	}
	writeJSON(w, http.StatusOK, map[string]any{"added": added, "skipped": dup + badCount, "bad": reasons})
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
	client := s.mtClient()
	if err := client.EnsureHealthy(r.Context()); err != nil {
		writeErr(w, err)
		return
	}
	rule := magitrickle.Rule{Type: req.Type, Rule: req.Rule, Name: req.Name, Enable: true}
	if req.Type != "regex" {
		norm, ok := magitrickle.NormalizeRule(req.Type, req.Rule, false)
		if !ok {
			writeErr(w, fmt.Errorf("значение не похоже на %s", req.Type))
			return
		}
		norm.Name, norm.Enable = req.Name, true
		rule = norm
	} else if strings.Contains(req.Rule, "://") {
		writeErr(w, fmt.Errorf("похоже на URL, а не на регулярку"))
		return
	}
	if _, err := client.CreateRule(r.Context(), r.PathValue("id"), rule); err != nil {
		writeErr(w, err)
		return
	}
	client.RefreshShadow(r.Context())
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
	if err := client.EnsureHealthy(r.Context()); err != nil {
		writeErr(w, err)
		return
	}
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
		client.RefreshShadow(r.Context())
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
	if err := client.EnsureHealthy(r.Context()); err != nil {
		writeErr(w, err)
		return
	}
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
			client.RefreshShadow(r.Context())
			writeJSON(w, http.StatusOK, map[string]string{"ok": "ok"})
			return
		}
	}
	http.NotFound(w, r)
}

// mtRulesEnableAll включает/выключает ВСЕ правила группы одним PUT группы
// целиком (частичный PUT затирает поля - шлём группу как есть, меняя только
// Enable правил). Слово владельца: 465 правил по одному не нащёлкаешь.
func (s *Server) mtRulesEnableAll(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enable bool `json:"enable"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	client := s.mtClient()
	if err := client.EnsureHealthy(r.Context()); err != nil {
		writeErr(w, err)
		return
	}
	groups, err := client.GroupsWithRules(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	groupID := r.PathValue("id")
	for i := range groups {
		g := &groups[i]
		if g.ID != groupID {
			continue
		}
		changed := 0
		for j := range g.Rules {
			if g.Rules[j].Enable != req.Enable {
				g.Rules[j].Enable = req.Enable
				changed++
			}
		}
		if changed == 0 {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "total": len(g.Rules), "changed": 0})
			return
		}
		if err := client.UpdateGroup(r.Context(), *g, true); err != nil {
			writeErr(w, err)
			return
		}
		client.RefreshShadow(r.Context())
		action := map[bool]string{true: "включены", false: "выключены"}[req.Enable]
		s.store.LogEvent("rules", "magitrickle",
			fmt.Sprintf("в группе %s %s все правила (%d из %d)", g.Name, action, changed, len(g.Rules)))
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "total": len(g.Rules), "changed": changed})
		return
	}
	writeErr(w, fmt.Errorf("группа не найдена"))
}

func (s *Server) mtDeleteRule(w http.ResponseWriter, r *http.Request) {
	client := s.mtClient()
	if err := client.EnsureHealthy(r.Context()); err != nil {
		writeErr(w, err)
		return
	}
	if err := client.DeleteRule(r.Context(), r.PathValue("id"), r.PathValue("rid")); err != nil {
		writeErr(w, err)
		return
	}
	client.RefreshShadow(r.Context())
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
	if err := client.EnsureHealthy(r.Context()); err != nil {
		writeErr(w, err)
		return
	}
	created := magitrickle.Group{Name: name, Interface: req.Interface, Enable: true, Rules: preset.Rules}
	if err := client.AddGroup(r.Context(), created); err != nil {
		writeErr(w, fmt.Errorf("группа не создана: %v", err))
		return
	}
	gid := ""
	if groups, err := client.GroupsWithRules(r.Context()); err == nil {
		for _, g := range groups {
			if g.Name == name && g.Interface == req.Interface {
				gid = g.ID
			}
		}
	}
	s.store.LogEvent("rules", "magitrickle", "применён шаблон "+preset.ID+" на "+req.Interface)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "groupId": gid, "rules": len(preset.Rules)})
}
