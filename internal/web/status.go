package web

// Хендлеры/логика статуса и проб пула (из server.go).

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"mawg/internal/store"
)

func (s *Server) getStatus(w http.ResponseWriter, r *http.Request) {
	pools := s.store.Pools()
	known := s.knownDevices()
	type poolView struct {
		Name           string `json:"name"`
		Platform       string `json:"platform"`
		Device         string `json:"device"`
		Slot           string `json:"slot,omitempty"`
		Mode           string `json:"mode"`
		ActiveFile     string `json:"activeFile"`
		ActiveEndpoint string `json:"activeEndpoint"`
		LastResult     string `json:"lastResult"`
		LastError      string `json:"lastError"`
		// ProbeOk/ProbeMs/ProbeAt - структурированный итог последней
		// пробы и её время (unix-сек): единый источник для карточки
		// пула и строки интерфейса; nil/0 - пробы ещё не было
		ProbeOk        *bool                    `json:"probeOk,omitempty"`
		ProbeMs        int                      `json:"probeMs,omitempty"`
		ProbeAt        int64                    `json:"probeAt,omitempty"`
		Rotations      int                      `json:"rotations"`
		Disabled       bool                     `json:"disabled"`
		ConsecFails    int                      `json:"consecFails"`
		RefreshFails   int                      `json:"refreshFails,omitempty"`
		LastRefreshErr string                   `json:"lastRefreshErr,omitempty"`
		SubRefreshAt   time.Time                `json:"subRefreshAt,omitempty"`
		Smart          *store.SmartCascadeState `json:"smart,omitempty"`
		Settings       store.PoolSettings       `json:"settings"`
		Configs        []configView             `json:"configs"`
		Premium        store.PremiumView        `json:"premium"`
		Protos         []protoBadge             `json:"protos,omitempty"`
		Free           bool                     `json:"free,omitempty"`
		// FallbackDead - цель фоллбека «pool:имя»/«iface:устройство» не
		// существует (пул удалён или выключен): при отказе этого пула
		// группы уйдут direct, а не туда, куда думает владелец (аудит
		// мёртвых ссылок, 12.11)
		FallbackDead bool `json:"fallbackDead,omitempty"`
	}
	out := struct {
		Version     string `json:"version"`
		Platform    string `json:"platform"`
		BackendName string `json:"backendName"`
		AuthEnabled bool   `json:"authEnabled"`
		// NativeAWG3 - роутер поднимает AWG 3.x нативно (OpenWrt c kmod
		// amneziawg 3.x, Keenetic 5.2+): только там нативный Premium.
		// Панели по флагу прячут блок «Amnezia Premium vpn://» в карточке
		// пула - на Keenetic до 5.2 слот AWG 2.0 HP-ключ не применит.
		NativeAWG3 bool `json:"nativeAWG3"`
		// MtSources - подключённый MT понимает поле sources (форк rev3+).
		// Детект и гейтинг - см. mtversion.go.
		MtSources bool `json:"mtSources"`
		// ExcludesDisabled - функционал исключений выключен (95-док,
		// слово владельца 30.10): UI гасит блоки исключений.
		ExcludesDisabled bool           `json:"excludesDisabled"`
		Pools            []poolView     `json:"pools"`
		Engine           map[string]any `json:"engine,omitempty"`
		// Engines - состояние по ядрам (88-док): singbox (копия Engine
		// для совместимости) и mihomo. available:false - ядро не установлено.
		Engines map[string]map[string]any `json:"engines,omitempty"`
	}{Version: s.version, Platform: s.backend.Name(), BackendName: s.backend.Name(),
		AuthEnabled: s.auth == nil || s.auth.Enabled, NativeAWG3: s.nativeAWG3(), MtSources: s.mtSourcesCapable(r.Context()),
		ExcludesDisabled: s.store.ExcludesDisabled(), Pools: []poolView{}}

	for _, p := range pools {
		st := s.store.State(p.Name)
		view := poolView{
			Name: p.Name, Platform: p.Settings.Platform, Device: p.DeviceName(),
			Slot: p.Settings.KeeneticSlot, Mode: st.Mode, ActiveFile: st.ActiveFile,
			LastResult: st.LastResult, LastError: st.LastError,
			Rotations: st.Rotations, Disabled: p.Disabled, ConsecFails: st.ConsecFails, Settings: p.Settings,
			RefreshFails: st.RefreshFails, LastRefreshErr: st.LastRefreshErr, SubRefreshAt: st.SubRefreshAt,
			Smart:   st.Smart,
			Configs: []configView{},
			Premium: s.store.PremiumView(p.Name),
			Protos:  s.poolProtoBadges(p),
			Free:    p.Free,
		}
		switch fb := p.Settings.Fallback; {
		case strings.HasPrefix(fb, store.FallbackPoolPrefix):
			if name, ok := store.FallbackPoolName(fb); ok {
				fbPool, exists := s.store.Pool(name)
				view.FallbackDead = !exists || fbPool.Disabled
			}
		case strings.HasPrefix(fb, store.FallbackIfacePrefix):
			if dev, ok := store.FallbackIfaceName(fb); ok {
				view.FallbackDead = !known[dev]
			}
		}
		if p.Settings.EngineMode == engineMode {
			view.ProbeOk, view.ProbeMs, view.ProbeAt, view.Mode, view.LastResult = s.poolProbeInfo(p)
		} else if !st.LastCheck.IsZero() {
			v := st.ProbeOK
			view.ProbeOk, view.ProbeMs, view.ProbeAt = &v, st.ProbeMs, st.LastCheck.Unix()
		}
		now := time.Now().Unix()
		for _, c := range p.Configs {
			cv := configView{File: c.File, Original: c.Original, Endpoint: c.Endpoint, Enabled: c.Enabled, Awg: c.Awg}
			if until, ok := st.Cooldowns[c.File]; ok {
				cv.CoolFor = until - now
			}
			cv.Conflict = s.configConflict(p.Name, p.DeviceName(), s.poolConfigAddrs(p.Name, c))
			if c.File == st.ActiveFile {
				view.ActiveEndpoint = c.Endpoint
			}
			view.Configs = append(view.Configs, cv)
		}
		out.Pools = append(out.Pools, view)
	}
	if mgr, err := s.sb(); err == nil {
		st := mgr.Status()
		var tuns []map[string]any
		for _, p := range s.enginePoolsOfCore("singbox") {
			nodes := 0
			if ns, err := s.readPoolNodes(p.Name); err == nil {
				nodes = len(ns)
			}
			entry := map[string]any{
				"pool": p.Name, "tun": p.Settings.TunName,
				"disabled": p.Disabled, "nodes": nodes,
			}
			if ps, ok := mgr.PoolStatus(p.Name); ok {
				// порт сокс-пробы - из фактической спецификации пула;
				// номер туна тут ни при чём (порты идут по порядку пулов)
				entry["probePort"] = ps.MixedPort
				entry["probeOk"] = ps.ProbeOK
				entry["probeMs"] = ps.ProbeMs
				entry["probeErr"] = ps.ProbeErr
				entry["reason"] = ps.Reason
				// причина waits-lx/waits-core из матрицы возможностей
				// (112-док 2.3): варнинг в карточке пула
				entry["detail"] = ps.Detail
			}
			tuns = append(tuns, entry)
		}
		out.Engine = map[string]any{
			"available": true, "running": st.Running, "api": st.API, "version": st.Version,
			"lx": st.LX, "extended": st.Extended, "source": st.SourceID,
			"bin": st.Bin, "mixedPort": st.Mixed, "clashPort": st.Clash,
			"mode": st.Mode, "fragment": st.Fragment, "cacheFile": st.CacheFile, "tuns": tuns,
			"disabled": s.store.EngineDisabled(), "autostart": s.coreAutostart(),
		}
	}
	out.Engines = map[string]map[string]any{}
	if out.Engine != nil {
		out.Engines["singbox"] = out.Engine
	} else {
		out.Engines["singbox"] = map[string]any{"available": false, "disabled": s.store.CoreDisabled("singbox")}
	}
	if mmgr, err := s.mh(); err == nil {
		st := mmgr.Status()
		var tuns []map[string]any
		for _, p := range s.enginePoolsOfCore(engineCoreMihomo) {
			nodes := 0
			if ns, err := s.readPoolNodes(p.Name); err == nil {
				nodes = len(ns)
			}
			entry := map[string]any{
				"pool": p.Name, "tun": p.Settings.TunName,
				"disabled": p.Disabled, "nodes": nodes,
			}
			if ps, ok := mmgr.PoolStatus(p.Name); ok {
				entry["probePort"] = ps.MixedPort
				entry["probeOk"] = ps.ProbeOK
				entry["probeMs"] = ps.ProbeMs
				entry["probeErr"] = ps.ProbeErr
				entry["reason"] = ps.Reason
				entry["detail"] = ps.Detail
			}
			tuns = append(tuns, entry)
		}
		out.Engines["mihomo"] = map[string]any{
			"available": true, "running": st.Running, "api": st.API, "version": st.Version,
			"bin": st.Bin, "mixedPort": st.Mixed, "clashPort": st.Clash,
			"mode": st.Mode, "cacheFile": st.Cache, "tuns": tuns,
			"disabled": s.store.CoreDisabled("mihomo"),
		}
	} else {
		out.Engines["mihomo"] = map[string]any{"available": false, "disabled": s.store.CoreDisabled("mihomo")}
	}
	writeJSON(w, http.StatusOK, out)
}

type configView struct {
	File     string `json:"file"`
	Original string `json:"original"`
	Endpoint string `json:"endpoint"`
	Enabled  bool   `json:"enabled"`
	CoolFor  int64  `json:"coolForSec,omitempty"`
	Conflict string `json:"conflict,omitempty"`
	// Awg - уровень обфускации (plain/awg2/awg3): панель по нему
	// показывает кнопки скачивания WG/AWG.
	Awg string `json:"awg,omitempty"`
}

// poolProbeInfo - единый источник итога пробы пула (/status и /ifaces
// читают только его, повторно проба не выполняется): нативные пулы - из
// состояния ротатора, движковые - из PoolStatus менеджера. ok=nil - пробы
// ещё не было.
func (s *Server) poolProbeInfo(p store.Pool) (ok *bool, ms int, at int64, mode, text string) {
	if p.Settings.EngineMode != engineMode {
		st := s.store.State(p.Name)
		if st.LastCheck.IsZero() {
			return nil, 0, 0, st.Mode, st.LastResult
		}
		v := st.ProbeOK
		return &v, st.ProbeMs, st.LastCheck.Unix(), st.Mode, st.LastResult
	}
	if p.Settings.EngineMode == engineMode && s.store.CoreDisabled(engineCoreOf(p)) {
		return nil, 0, 0, "fallback", "движок выключен (Система → Ядра VPN)"
	}
	mgr, err := s.engineMgr(p)
	if err != nil {
		return nil, 0, 0, "fallback", "движок недоступен"
	}
	ps, valid := mgr.PoolStatus(p.Name)
	if !valid {
		return nil, 0, 0, "fallback", "движок не применял конфиг"
	}
	switch {
	case ps.Reason == "waits-core":
		return nil, 0, 0, "fallback", "узлы не поддерживаются mihomo: " + ps.Detail
	case ps.Reason == "waits-lx":
		return nil, 0, 0, "fallback", "узлы ждут lx-ядро: " + ps.Detail
	case ps.CheckedAt.IsZero():
		return nil, 0, 0, "fallback", "проба ещё не выполнялась"
	case ps.ProbeOK:
		v := true
		return &v, ps.ProbeMs, ps.CheckedAt.Unix(), "up", fmt.Sprintf("ok (http %dms)", ps.ProbeMs)
	default:
		v := false
		return &v, ps.ProbeMs, ps.CheckedAt.Unix(), "fallback", "проба не прошла: " + ps.ProbeErr
	}
}
