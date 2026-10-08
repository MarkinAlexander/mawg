package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"mawg/internal/links"
	"mawg/internal/singbox"
	"mawg/internal/store"
	"mawg/internal/wgconf"
)

type sourcePlan struct {
	Pool       string            `json:"pool,omitempty"`
	Tun        string            `json:"tun,omitempty"`
	Applied    int               `json:"applied,omitempty"`
	Added      int               `json:"added,omitempty"`
	ProbeOK    *bool             `json:"probeOk,omitempty"`
	ProbeMs    int               `json:"probeMs,omitempty"`
	Duplicates []string          `json:"duplicates,omitempty"`
	Engine     []sourceEngineOne `json:"engine,omitempty"`
	Skipped    []string          `json:"skipped,omitempty"`
	Warnings   []string          `json:"warnings,omitempty"`
	Amnezia    *amneziaPlanMeta  `json:"amnezia,omitempty"`
}

// amneziaPlanMeta - что вернул gateway при обмене ключа: протокол, локация,
// доступные локации и счётчики устройств подписки.
type amneziaPlanMeta struct {
	Protocol           string                 `json:"protocol,omitempty"`
	ServerCountry      string                 `json:"serverCountry,omitempty"`
	ServerCountryName  string                 `json:"serverCountryName,omitempty"`
	AvailableCountries []store.GatewayCountry `json:"availableCountries,omitempty"`
	ActiveDevices      int                    `json:"activeDevices,omitempty"`
	MaxDevices         int                    `json:"maxDevices,omitempty"`
	IssuedConfigs      int                    `json:"issuedConfigs,omitempty"`
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
	Exchange     bool   `json:"exchange"`
	Via          string `json:"via"`
	Country      string `json:"country"`
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

func buildNativeConfigs(nodes []links.Node) (ncs []wgconf.NamedConfig, engine []sourceEngineOne, skipped []string) {
	for _, node := range nodes {
		if node.Type != "wireguard" && node.Type != "amneziawg" {
			engine = append(engine, sourceEngineOne{Tag: node.Tag, Type: node.Type})
			continue
		}
		confText, err := node.ConfText()
		if err != nil {
			skipped = append(skipped, err.Error())
			continue
		}
		cfg, err := wgconf.Parse([]byte(confText))
		if err != nil {
			skipped = append(skipped, node.Tag+": "+err.Error())
			continue
		}
		ncs = append(ncs, wgconf.NamedConfig{OriginalName: node.ConfName(), Raw: []byte(confText), Config: cfg})
	}
	return ncs, engine, skipped
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
	if key, ok := links.IsAmneziaKey(req.Source); ok {
		if !req.Exchange {
			writeJSON(w, http.StatusOK, sourcePlan{
				Warnings: []string{fmt.Sprintf(
					"Это ключ Amnezia %s API (не конфиг). Нажмите «Запросить конфиг», чтобы mawg обменял его у gateway Амнезии на пул (AWG или VLESS).",
					strings.TrimPrefix(key.ServiceType, "amnezia-"))},
			})
			return
		}
		via := strings.TrimPrefix(strings.TrimSpace(req.Via), "socks5://")
		xr, xerr := links.ExchangeAmneziaKey(r.Context(), req.Source, links.ExchangeOptions{
			Socks5: via, ServerCountryCode: strings.TrimSpace(req.Country),
		})
		if xerr != nil {
			writeErr(w, xerr)
			return
		}
		meta := amneziaFromExchange(&xr)
		res := store.Sub{Source: req.Source, Nodes: xr.Nodes, RefreshedAt: time.Now(),
			Warnings: []string{"ключ Amnezia обменян на конфиг у gateway"}}
		s.createPoolFromNodes(w, r, req, res, meta)
		return
	}
	res, err := resolveSource(r.Context(), req.Source)
	if err != nil {
		writeErr(w, err)
		return
	}
	s.createPoolFromNodes(w, r, req, res, nil)
}

func amneziaFromExchange(xr *links.ExchangeResult) *amneziaPlanMeta {
	if xr == nil {
		return nil
	}
	countries := make([]store.GatewayCountry, 0, len(xr.AvailableCountries))
	for _, c := range xr.AvailableCountries {
		countries = append(countries, store.GatewayCountry{Code: c.Code, Name: c.Name, Protocols: c.Protocols})
	}
	return &amneziaPlanMeta{
		Protocol: xr.Protocol, ServerCountry: xr.ServerCountry, ServerCountryName: xr.ServerCountryName,
		AvailableCountries: countries,
		ActiveDevices:      xr.ActiveDevices, MaxDevices: xr.MaxDevices, IssuedConfigs: xr.IssuedConfigs,
	}
}

func storeAmnezia(meta *amneziaPlanMeta) *store.AmneziaMeta {
	if meta == nil {
		return nil
	}
	return &store.AmneziaMeta{Protocol: meta.Protocol, Country: meta.ServerCountry, Countries: meta.AvailableCountries}
}

func (s *Server) createPoolFromNodes(w http.ResponseWriter, r *http.Request, req poolSourceReq, res store.Sub, amnezia *amneziaPlanMeta) {
	plan := sourcePlan{Warnings: res.Warnings, Amnezia: amnezia}
	if res.Error != "" {
		plan.Skipped = append(plan.Skipped, res.Error)
	}
	ncs, engineNodes, skipped := buildNativeConfigs(res.Nodes)
	plan.Skipped = append(plan.Skipped, skipped...)

	if len(ncs) > 0 {
		settings, err := s.poolSettingsFromReq(req)
		if err != nil {
			writeErr(w, err)
			return
		}
		settings.Amnezia = storeAmnezia(amnezia)
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
		plan.Engine = engineNodes
		s.store.LogEvent(pool.Name, "applied", fmt.Sprintf("пул из источника: %d конфигов", added))
		for _, e := range plan.Engine {
			s.store.LogEvent(pool.Name, "engine", fmt.Sprintf("узел %s (%s) ждёт движок sing-box", e.Tag, e.Type))
		}
		if p, ok := s.store.Pool(pool.Name); ok && len(p.Configs) > 0 {
			if err := s.engine.SetActive(pool.Name, p.Configs[0].File); err != nil {
				plan.Warnings = append(plan.Warnings, "не активирован: "+err.Error())
			}
		}
		s.engine.CheckNow(pool.Name)
		writeJSON(w, http.StatusOK, plan)
		return
	}

	if len(engineNodes) > 0 {
		lx := false
		if mgr, err := s.sb(); err == nil {
			lx = mgr.Info().LX
		}
		eligible, reasons := singbox.EligibleNodes(res.Nodes, lx)
		if len(eligible) == 0 {
			plan.Engine = engineNodes
			plan.Skipped = append(plan.Skipped, reasons...)
			plan.Warnings = append(plan.Warnings,
				"Пул не создан: ни один узел не поддерживается текущим движком sing-box. Нужен lx-профиль ядра - после его установки добавьте ссылку заново.")
			writeJSON(w, http.StatusOK, plan)
			return
		}
		settings := store.PoolSettings{
			Platform: s.backend.Name(), Fallback: req.Fallback, ProbeHost: req.ProbeHost,
			Source: req.Source, EngineMode: engineMode, Amnezia: storeAmnezia(amnezia),
		}
		if err := s.validFallback(req.Name, settings.Fallback); err != nil {
			writeErr(w, err)
			return
		}
		pool, err := s.claimTun(settings, req.Name)
		if err != nil {
			writeErr(w, err)
			return
		}
		if err := s.writePoolNodes(pool.Name, res.Nodes); err != nil {
			writeErr(w, err)
			return
		}
		plan.Pool = pool.Name
		plan.Tun = pool.Settings.TunName
		eligSet := map[string]bool{}
		for _, n := range eligible {
			eligSet[n.Tag] = true
		}
		for _, e := range engineNodes {
			if !eligSet[e.Tag] {
				plan.Engine = append(plan.Engine, e)
			}
		}
		s.store.LogEvent(pool.Name, "applied", fmt.Sprintf("пул из источника: %d узлов в tun (%s)", len(eligible), pool.Settings.TunName))
		plan.Applied = len(eligible)
		skipped, err := s.applyEngine()
		plan.Skipped = append(plan.Skipped, skipped...)
		if err != nil {
			plan.Warnings = append(plan.Warnings, "движок не применил конфиг: "+err.Error())
		} else if mgr, mgrErr := s.sb(); mgrErr == nil {
			if ps, ok := mgr.PoolStatus(pool.Name); ok && !ps.CheckedAt.IsZero() {
				ok := ps.ProbeOK
				plan.ProbeOK = &ok
				plan.ProbeMs = ps.ProbeMs
				if !ps.ProbeOK {
					plan.Warnings = append(plan.Warnings, "проба не прошла: "+ps.ProbeErr)
				}
			}
		}
		writeJSON(w, http.StatusOK, plan)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}
