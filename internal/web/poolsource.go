package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"mawg/internal/store"
	"mawg/internal/wgconf"
)

type sourcePlan struct {
	Pool       string            `json:"pool,omitempty"`
	Added      int               `json:"added,omitempty"`
	Duplicates []string          `json:"duplicates,omitempty"`
	Engine     []sourceEngineOne `json:"engine,omitempty"`
	Skipped    []string          `json:"skipped,omitempty"`
	Warnings   []string          `json:"warnings,omitempty"`
}

type sourceEngineOne struct {
	Tag  string `json:"tag"`
	Type string `json:"type"`
}

type poolSourceReq struct {
	Name         string `json:"name"`
	Source       string `json:"source"`
	KeeneticSlot string `json:"keeneticSlot"`
	OpenwrtProto string `json:"openwrtProto"`
	Fallback     string `json:"fallback"`
	ProbeHost    string `json:"probeHost"`
}

func (s *Server) poolSettingsFromReq(req poolSourceReq) (store.PoolSettings, error) {
	settings := store.PoolSettings{
		Platform:     s.backend.Name(),
		KeeneticSlot: req.KeeneticSlot,
		Fallback:     req.Fallback,
		ProbeHost:    req.ProbeHost,
		Source:       req.Source,
	}
	if s.backend.Name() == store.PlatformOpenwrt {
		settings.OpenwrtProto = req.OpenwrtProto
		if settings.OpenwrtProto == "" {
			settings.OpenwrtProto = "wireguard"
		}
	}
	if s.backend.Name() == store.PlatformKeenetic && settings.KeeneticSlot == "" {
		return settings, fmt.Errorf("не выбран слот Keenetic: не удалось получить список слотов, повторите позже")
	}
	return settings, nil
}

func (s *Server) createPoolFromSource(w http.ResponseWriter, r *http.Request) {
	var req poolSourceReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	req.Source = strings.TrimSpace(req.Source)
	if req.Source == "" {
		writeErr(w, fmt.Errorf("пустой источник: дайте URL подписки или ссылку"))
		return
	}
	res, err := resolveSource(r.Context(), req.Source)
	if err != nil {
		writeErr(w, err)
		return
	}
	plan := sourcePlan{Warnings: res.Warnings}
	for _, warn := range []string{res.Error} {
		if warn != "" {
			plan.Skipped = append(plan.Skipped, warn)
		}
	}

	var ncs []wgconf.NamedConfig
	for _, node := range res.Nodes {
		if node.Type != "wireguard" && node.Type != "amneziawg" {
			plan.Engine = append(plan.Engine, sourceEngineOne{Tag: node.Tag, Type: node.Type})
			continue
		}
		confText, err := node.ConfText()
		if err != nil {
			plan.Skipped = append(plan.Skipped, err.Error())
			continue
		}
		cfg, err := wgconf.Parse([]byte(confText))
		if err != nil {
			plan.Skipped = append(plan.Skipped, node.Tag+": "+err.Error())
			continue
		}
		ncs = append(ncs, wgconf.NamedConfig{OriginalName: node.ConfName(), Raw: []byte(confText), Config: cfg})
	}

	if len(ncs) > 0 {
		settings, err := s.poolSettingsFromReq(req)
		if err != nil {
			writeErr(w, err)
			return
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
		added, dupes, err := s.store.AddConfigs(pool.Name, ncs)
		if err != nil {
			writeErr(w, err)
			return
		}
		plan.Pool = pool.Name
		plan.Added = added
		plan.Duplicates = dupes
		s.store.LogEvent(pool.Name, "applied", fmt.Sprintf("пул из источника: %d конфигов", added))
		for _, e := range plan.Engine {
			s.store.LogEvent(pool.Name, "engine", fmt.Sprintf("узел %s (%s) ждёт движок sing-box", e.Tag, e.Type))
		}
		s.engine.CheckNow(pool.Name)
	}
	writeJSON(w, http.StatusOK, plan)
}
