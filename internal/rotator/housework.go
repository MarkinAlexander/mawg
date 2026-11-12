package rotator

import (
	"context"
	"fmt"
	"time"

	"mawg/internal/magitrickle"
	"mawg/internal/platform"
	"mawg/internal/store"
)

// runHousework - 30-с домашка, разнесённая по чередующимся тикам, чтобы
// её залпы не складывались в один всплеск CPU: чётные тики - бандлы
// (monitorMagitrickle внутри) и фоновые пробы внешних интерфейсов,
// нечётные - сверка OUTPUT-хуков каскадов и политики групп. Каждый шаг
// выполняется раз в минуту вместо 30с - осознанный размен по слову
// владельца «пробы по очереди либо с интервалом».
func (e *Engine) runHousework() {
	if houseworkBundles(e.tickN) {
		e.checkBundles()
		e.refreshProbedExternals()
		return
	}
	e.syncCascadeHooks()
	e.checkGroupPolicies()
	e.checkSmartCascades()
	e.maybeRecheckGlitchy()
}

// houseworkBundles - чётные 30-с тики отданы бандлам, нечётные хукам и политикам.
func houseworkBundles(tickN int) bool { return tickN%2 == 0 }

// houseworkRefreshDNS - пере-резолюция доменов каскадов раз в 300с и
// только на нечётных (политических) тиках: 5,15,25... - иначе она
// пропадала бы на чётных после разноса домашки.
func houseworkRefreshDNS(tickN int) bool { return tickN%10 == 5 }

// syncCascadeHooks - сверка OUTPUT-хуков каскадов (была в каждом 30-с
// тике): хук - runtime-состояние iptables, его снимают перезагрузка
// (порядок старта mawg/MagiTrickle не гарантирован), рестарт сети или
// сторонний iptables-restore. SyncHooks идемпотентен и молчит при
// недоступном MT - доделает на следующем цикле.
func (e *Engine) syncCascadeHooks() {
	if e.casc != nil {
		e.casc.SyncHooks()
	}
}

const extMemberFailThreshold = 2

func (e *Engine) memberDeviceHealth(member string, slots []platform.SlotInfo) (device string, healthy bool, external bool, sample *probeCacheEntry) {
	if p, ok := e.store.Pool(member); ok {
		if p.Disabled {
			return p.DeviceName(), false, false, nil
		}
		return p.DeviceName(), e.store.State(member).Mode == store.ModeUp, false, nil
	}
	for _, sl := range slots {
		if sl.Device == member {
			if !sl.LinkUp {
				return member, false, true, nil
			}
			if probe := e.store.IfaceProbe(member); probe != nil {
				c := e.deviceProbe(member, *probe)
				return member, c.healthy(), true, c
			}
			fresh := sl.Connected
			if !fresh {
				hs := e.backend.IfaceHandshake(member)
				fresh = hs >= 0 && hs < 180
			}
			return member, fresh, true, nil
		}
	}
	return member, false, true, nil
}

func (e *Engine) CheckBundlesNow() {
	e.checkBundles()
}

func (e *Engine) checkBundles() {
	e.monitorMagitrickle()
	bundles := e.store.Bundles()
	if len(bundles) == 0 {
		return
	}
	e.actMu.Lock()
	defer e.actMu.Unlock()
	slots, slotsErr := e.backend.Slots()
	if tunnels, err := e.backend.SysTunnels(); err == nil {
		// чужие туннели (tunN постороннего софта) - полноценные внешние
		// члены бандла; без merge memberDeviceHealth читал бы «не найден
		// в слотах» = всегда мёртв, и бандл залип на запасном (кейс
		// владельца 12.11: tun9 wdttc основной, proton запасной)
		seen := make(map[string]bool, len(slots))
		for _, sl := range slots {
			seen[sl.Device] = true
		}
		for _, sl := range tunnels {
			if !seen[sl.Device] {
				slots = append(slots, sl)
			}
		}
	}
	for _, b := range bundles {
		if len(b.Members) == 0 || len(b.Groups) == 0 {
			continue
		}
		cur := e.store.BundleState(b.Name).Member
		sel := ""
		for _, m := range b.Members {
			dev, healthy, external, sample := e.memberDeviceHealth(m, slots)
			if external && slotsErr == nil && !healthy {
				key := b.Name + "/" + m
				if sample == nil || e.extSamples[key] != sample {
					e.extFails[key]++
					e.extSamples[key] = sample
				}
				if e.extFails[key] < extMemberFailThreshold {
					if dev == cur {
						sel = dev
						break
					}
					continue
				}
			}
			if external && healthy {
				delete(e.extFails, b.Name+"/"+m)
				delete(e.extSamples, b.Name+"/"+m)
			}
			if healthy {
				sel = dev
				break
			}
		}
		if sel == "" {
			continue
		}
		e.switchBundle(b, sel)
	}
}

func (e *Engine) switchBundle(b store.Bundle, device string) {
	if e.mt != nil {
		inBundle := map[string]bool{}
		for _, id := range b.Groups {
			inBundle[id] = true
		}
		var moved []string
		err := e.applyGroupChanges("bundle:"+b.Name, func(groups []magitrickle.Group) bool {
			changed := false
			for i := range groups {
				if inBundle[groups[i].ID] && groups[i].Interface != device {
					groups[i].Interface = device
					moved = append(moved, groups[i].Name)
					changed = true
				}
			}
			return changed
		})
		if err == nil && len(moved) > 0 {
			e.store.LogEvent("bundle:"+b.Name, "switch", "member -> "+device+", groups: "+fmt.Sprint(moved))
		}
	}
	e.store.MutateBundleState(b.Name, func(s *store.BundleState) {
		s.Member = device
	})
}

func (e *Engine) bundleGroupIDs() map[string]bool {
	out := map[string]bool{}
	for _, b := range e.store.Bundles() {
		for _, id := range b.Groups {
			out[id] = true
		}
	}
	return out
}

// magitrickle падает сам по себе; раз в тик проверяем живость и поднимаем,
// если работал и умер. Заодно подтягиваем в тень правки из его UI, если
// список не усечён.
func (e *Engine) monitorMagitrickle() {
	if e.mt == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !e.mt.Available(ctx) {
		if e.mtEverAlive {
			e.store.LogEvent("magitrickle", "monitor", "magitrickle не отвечает")
			e.healMagitrickle("monitor", time.Minute)
		}
		return
	}
	if !e.mtEverAlive {
		e.mtEverAlive = true
		return
	}
	if e.now().Sub(e.mtSyncAt) >= time.Minute {
		e.mtSyncAt = e.now()
		// усечение рантайма MT при живом API (сорванный массовый PUT):
		// панели/каскады видят «групп нет», дисковый конфиг цел. Признак
		// «группы точно были» берем из персистентного: наши каскады в
		// сторе или непустой shadow - процесс-локального флага мало
		// (усечение могло случиться до старта mawg). Пустым чтением
		// shadow не затираем.
		groups, err := e.mt.GroupsWithRules(ctx)
		if err != nil {
			return
		}
		if len(groups) == 0 {
			if len(e.store.Cascades()) > 0 {
				e.store.LogEvent("magitrickle", "monitor", "рантайм magitrickle усечён (группы пропали при живом API, каскады есть) - перезапускаю демона, конфиг на диске цел")
				e.healMagitrickle("monitor", time.Minute)
			}
			return
		}
		e.mt.RefreshShadow(ctx)
	}
}

func (e *Engine) suspendGroups(p store.Pool) {
	if e.mt == nil {
		e.store.LogEvent(p.Name, "magitrickle", "client unavailable, groups not touched")
		return
	}
	// WAN-гейт: при мёртвом провайдере откат в direct бессмысленен,
	// Suspension переносится на тик, когда интернет вернется.
	if e.wanDown() {
		e.store.MutateState(p.Name, func(s *store.PoolState) {
			s.SuspendPending = true
		})
		e.store.LogEvent(p.Name, "fallback", "провайдер без интернета, откат групп отложен")
		return
	}
	bundled := e.bundleGroupIDs()
	dev := p.DeviceName()
	var disabled []string
	degraded := map[string]store.DegradedGroup{}
	err := e.applyGroupChanges(p.Name, func(groups []magitrickle.Group) bool {
		changed := false
		for i := range groups {
			g := &groups[i]
			if g.Interface != dev || !g.Enable || bundled[g.ID] {
				continue
			}
			pol := e.store.GroupPolicy(g.ID)
			switch {
			case pol.OnDead == store.PolicyIface && pol.Iface != "" && pol.Iface != dev:
				degraded[g.ID] = store.DegradedGroup{OrigIface: g.Interface, Mode: store.PolicyIface + ":" + pol.Iface}
				g.Interface = pol.Iface
				changed = true
			default:
				g.Enable = false
				disabled = append(disabled, g.ID)
				changed = true
			}
		}
		return changed
	})
	if err != nil || (len(disabled) == 0 && len(degraded) == 0) {
		return
	}
	e.store.MutateState(p.Name, func(s *store.PoolState) {
		s.DisabledGroups = append(s.DisabledGroups, disabled...)
		s.SuspendPending = false
	})
	if len(disabled) > 0 {
		e.store.LogEvent(p.Name, "magitrickle", "disabled groups: "+fmt.Sprint(disabled))
	}
	if len(degraded) > 0 {
		e.store.MutateDegraded(func(m map[string]store.DegradedGroup) {
			for id, d := range degraded {
				m[id] = d
			}
		})
		e.store.LogEvent(p.Name, "policy", "группы уведены по политике: "+fmt.Sprint(keysOf(degraded)))
	}
}

func keysOf(m map[string]store.DegradedGroup) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func (e *Engine) rebindGroups(p store.Pool, targetDevice string) {
	if e.mt == nil {
		e.store.LogEvent(p.Name, "magitrickle", "client unavailable, groups not rebound")
		return
	}
	dev := p.DeviceName()
	var rebound []string
	degraded := map[string]store.DegradedGroup{}
	err := e.applyGroupChanges(p.Name, func(groups []magitrickle.Group) bool {
		changed := false
		for i := range groups {
			g := &groups[i]
			if g.Interface != dev {
				continue
			}
			pol := e.store.GroupPolicy(g.ID)
			if g.Enable {
				if pol.OnDead == store.PolicyIface && pol.Iface != "" && pol.Iface != dev {
					degraded[g.ID] = store.DegradedGroup{OrigIface: g.Interface, Mode: store.PolicyIface + ":" + pol.Iface}
					g.Interface = pol.Iface
					changed = true
					continue
				}
			}
			g.Interface = targetDevice
			rebound = append(rebound, g.ID)
			changed = true
		}
		return changed
	})
	if err != nil || (len(rebound) == 0 && len(degraded) == 0) {
		return
	}
	if len(degraded) > 0 {
		e.store.MutateDegraded(func(m map[string]store.DegradedGroup) {
			for id, d := range degraded {
				m[id] = d
			}
		})
		e.store.LogEvent(p.Name, "policy", "группы уведены по политике: "+fmt.Sprint(keysOf(degraded)))
	}
	e.store.MutateState(p.Name, func(s *store.PoolState) {
		s.ReboundGroups = append(s.ReboundGroups, rebound...)
	})
	e.store.LogEvent(p.Name, "magitrickle", "groups rebound to "+targetDevice+": "+fmt.Sprint(rebound))
}

func (e *Engine) restoreReboundGroups(p store.Pool) {
	st := e.store.State(p.Name)
	if len(st.ReboundGroups) == 0 {
		return
	}
	if e.mt == nil {
		e.store.MutateState(p.Name, func(s *store.PoolState) {
			s.ReboundGroups = nil
		})
		return
	}
	dev := p.DeviceName()
	restore := map[string]bool{}
	for _, id := range st.ReboundGroups {
		restore[id] = true
	}
	var restored []string
	err := e.applyGroupChanges(p.Name, func(groups []magitrickle.Group) bool {
		changed := false
		for i := range groups {
			g := &groups[i]
			if restore[g.ID] && g.Interface != dev {
				g.Interface = dev
				restored = append(restored, g.ID)
				changed = true
			}
		}
		return changed
	})
	if err != nil {
		return
	}
	e.store.MutateState(p.Name, func(s *store.PoolState) {
		s.ReboundGroups = nil
	})
	if len(restored) > 0 {
		e.store.LogEvent(p.Name, "magitrickle", "groups rebound back to "+dev+": "+fmt.Sprint(restored))
	}
}

// checkGroupPolicies: политики групп на внешних интерфейсах и страховка
// для групп пулов (ручной down, отложенный при мёртвом WAN откат), плюс
// периодическая пере-резолюция доменов каскадов.
func (e *Engine) checkGroupPolicies() {
	if e.mt == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	for _, pool := range e.store.Pools() {
		st := e.store.State(pool.Name)
		if st.Mode == store.ModeFallback && st.SuspendPending && !e.wanDown() {
			e.store.LogEvent(pool.Name, "fallback", "провайдер ожил, продолжаю откат групп")
			e.suspendGroups(pool)
		}
	}

	// пере-резолюция доменов каскадов раз в 300с - на нечётных домашних
	// тиках (в паре с политиками, не с бандлами: houseworkRefreshDNS)
	if e.casc != nil && houseworkRefreshDNS(e.tickN) {
		e.casc.RefreshDNS(ctx)
	}

	groups, err := e.mt.GroupsWithRules(ctx)
	if err != nil {
		return
	}
	degraded := e.store.DegradedGroups()
	relevant := map[string]bool{}
	for _, g := range groups {
		if !e.store.GroupPolicy(g.ID).Default() {
			if dg, ok := degraded[g.ID]; ok {
				relevant[dg.OrigIface] = true
			} else {
				relevant[g.Interface] = true
			}
		}
	}
	health := map[string]bool{}
	samples := map[string]*probeCacheEntry{}
	for _, pool := range e.store.Pools() {
		health[pool.DeviceName()] = !pool.Disabled && e.store.State(pool.Name).Mode == store.ModeUp
	}
	if slots, err := e.backend.Slots(); err == nil {
		for _, sl := range slots {
			if !relevant[sl.Device] {
				continue
			}
			if _, ok := health[sl.Device]; ok {
				continue
			}
			ok := sl.LinkUp
			if ok {
				if probe := e.store.IfaceProbe(sl.Device); probe != nil {
					c := e.deviceProbe(sl.Device, *probe)
					ok = c.healthy()
					samples[sl.Device] = c
				} else {
					ok = sl.Connected
					if !ok {
						hs := e.backend.IfaceHandshake(sl.Device)
						ok = hs >= 0 && hs < 180
					}
				}
			}
			health[sl.Device] = ok
		}
	}

	cycles := e.store.Settings()
	pending := map[string]store.DegradedGroup{}
	type action struct {
		disable, enable bool
		iface           string
		clearDeg        bool
	}
	acts := map[string]action{}
	for i := range groups {
		g := groups[i]
		pol := e.store.GroupPolicy(g.ID)
		if pol.Default() {
			continue
		}
		dg, wasDegraded := degraded[g.ID]
		dev := g.Interface
		if wasDegraded {
			dev = dg.OrigIface
		}
		if sample := samples[dev]; sample != nil {
			if e.polSamples[g.ID] == sample && dg.Mode != "direct-pending" {
				continue
			}
			e.polSamples[g.ID] = sample
		} else {
			delete(e.polSamples, g.ID)
		}
		if wasDegraded {
			e.polFails[g.ID] = 0
			if dg.Mode == "direct-pending" {
				if !e.wanDown() && g.Enable {
					acts[g.ID] = action{disable: true}
					pending[g.ID] = store.DegradedGroup{OrigIface: dg.OrigIface, Mode: store.PolicyDirect}
				}
				continue
			}
			origHealthy, known := health[dg.OrigIface]
			if !known || !origHealthy {
				e.polOks[g.ID] = 0
			}
			// флаг degraded устарел (группа уже на исходнике): без этой ветки
			// группа ждёт "восстановления" вечно и выпадает из политик.
			// Исходник нездоров - сразу переключаем, иначе снимаем флаг.
			if dg.Mode == store.PolicyIface && g.Interface == dg.OrigIface {
				if known && !origHealthy && pol.OnDead == store.PolicyIface &&
					pol.Iface != "" && pol.Iface != g.Interface && g.Enable {
					// clearDeg нельзя: pending ниже вернёт флаг (иначе
					// потерялся бы обратный возврат на исходник)
					acts[g.ID] = action{iface: pol.Iface}
					pending[g.ID] = store.DegradedGroup{OrigIface: dg.OrigIface, Mode: store.PolicyIface}
				} else {
					acts[g.ID] = action{clearDeg: true}
				}
				continue
			}
			if !known || !origHealthy {
				continue
			}
			// возврат на исходник - с двухцикловой выдержкой, чтобы
			// лоскочущий вокруг порога интерфейс не дёргал группы
			e.polOks[g.ID]++
			if e.polOks[g.ID] < cycles.PolicyRestoreCycles {
				continue
			}
			if dg.Mode == store.PolicyDirect {
				if !g.Enable {
					acts[g.ID] = action{enable: true, clearDeg: true}
				} else {
					pending[g.ID] = store.DegradedGroup{OrigIface: dg.OrigIface, Mode: store.PolicyDirect}
				}
			} else if g.Interface != dg.OrigIface {
				acts[g.ID] = action{iface: dg.OrigIface, clearDeg: true}
			}
			continue
		}
		healthy, known := health[g.Interface]
		if !known || healthy {
			e.polFails[g.ID] = 0
			e.polOks[g.ID] = 0
			continue
		}
		e.polOks[g.ID] = 0
		e.polFails[g.ID]++
		if e.polFails[g.ID] < cycles.PolicyFailCycles {
			continue
		}
		switch pol.OnDead {
		case store.PolicyDirect:
			if e.wanDown() {
				pending[g.ID] = store.DegradedGroup{OrigIface: g.Interface, Mode: "direct-pending"}
				continue
			}
			if g.Enable {
				acts[g.ID] = action{disable: true}
				pending[g.ID] = store.DegradedGroup{OrigIface: g.Interface, Mode: store.PolicyDirect}
			}
		case store.PolicyIface:
			if pol.Iface != "" && pol.Iface != g.Interface && g.Enable {
				acts[g.ID] = action{iface: pol.Iface}
				pending[g.ID] = store.DegradedGroup{OrigIface: g.Interface, Mode: store.PolicyIface}
			}
		}
	}
	if len(acts) == 0 {
		if len(pending) > 0 {
			e.store.MutateDegraded(func(m map[string]store.DegradedGroup) {
				for id, d := range pending {
					m[id] = d
				}
			})
		}
		return
	}
	err = e.applyGroupChanges("policy", func(gs []magitrickle.Group) bool {
		changed := false
		for i := range gs {
			a, ok := acts[gs[i].ID]
			if !ok {
				continue
			}
			if a.disable && gs[i].Enable {
				gs[i].Enable = false
				changed = true
			}
			if a.enable && !gs[i].Enable {
				gs[i].Enable = true
				changed = true
			}
			if a.iface != "" && gs[i].Interface != a.iface {
				gs[i].Interface = a.iface
				changed = true
			}
		}
		return changed
	})
	if err != nil {
		return
	}
	e.store.MutateDegraded(func(m map[string]store.DegradedGroup) {
		for id, d := range pending {
			m[id] = d
		}
		for id, a := range acts {
			if a.clearDeg {
				delete(m, id)
			}
		}
	})
	for id, a := range acts {
		if a.disable {
			e.store.LogEvent("policy", "degrade", "группа "+id+" уведена в direct: основной интерфейс умер")
		}
		if a.enable {
			e.store.LogEvent("policy", "restore", "группа "+id+" вернулась в direct")
		}
		if a.iface != "" {
			e.store.LogEvent("policy", "degrade", "группа "+id+" переписана на "+a.iface)
		}
	}
}

// restoreDegraded возвращает политики-группы на исходный интерфейс,
// когда основной ожил.
func (e *Engine) restoreDegraded(dev string) {
	degraded := e.store.DegradedGroups()
	var ids []string
	for id, d := range degraded {
		if d.OrigIface == dev && d.Mode != "direct" && d.Mode != "direct-pending" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return
	}
	restore := map[string]store.DegradedGroup{}
	for _, id := range ids {
		restore[id] = degraded[id]
	}
	err := e.applyGroupChanges("policy", func(groups []magitrickle.Group) bool {
		changed := false
		for i := range groups {
			d, ok := restore[groups[i].ID]
			if !ok || groups[i].Interface == d.OrigIface {
				continue
			}
			groups[i].Interface = d.OrigIface
			changed = true
		}
		return changed
	})
	if err != nil {
		return
	}
	e.store.MutateDegraded(func(m map[string]store.DegradedGroup) {
		for _, id := range ids {
			delete(m, id)
		}
	})
	e.store.LogEvent("policy", "restore", "основной интерфейс ожил, группы вернулись на "+dev)
}

func (e *Engine) restoreGroups(p store.Pool, s *store.PoolState) {
	if len(s.DisabledGroups) == 0 {
		return
	}
	if e.mt == nil {
		e.store.MutateState(p.Name, func(s2 *store.PoolState) {
			s2.DisabledGroups = nil
		})
		return
	}
	enable := map[string]bool{}
	for _, id := range s.DisabledGroups {
		enable[id] = true
	}
	var restored []string
	err := e.applyGroupChanges(p.Name, func(groups []magitrickle.Group) bool {
		changed := false
		for i := range groups {
			g := &groups[i]
			if enable[g.ID] && !g.Enable {
				g.Enable = true
				restored = append(restored, g.ID)
				changed = true
			}
		}
		return changed
	})
	if err != nil {
		return
	}
	e.store.MutateState(p.Name, func(s2 *store.PoolState) {
		s2.DisabledGroups = nil
	})
	if len(restored) > 0 {
		e.store.LogEvent(p.Name, "magitrickle", "enabled groups: "+fmt.Sprint(restored))
	}
}
