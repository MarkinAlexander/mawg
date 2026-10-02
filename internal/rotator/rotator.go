package rotator

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"regexp"
	"strconv"
	"sync"
	"time"

	"mawg/internal/magitrickle"
	"mawg/internal/platform"
	"mawg/internal/store"
)

const settleSeconds = 15
const minApplyInterval = 20 * time.Second

type Engine struct {
	store   *store.Store
	backend platform.Backend
	mt      *magitrickle.Client

	Clock func() time.Time

	WANProbeFn func() bool

	wake      chan string
	stop      context.CancelFunc
	mu        sync.Mutex
	actMu     sync.Mutex
	lastApply map[string]time.Time
	extFails  map[string]int
	wanCache  wanCacheEntry
	mtHealAt  time.Time

	ifaceAt time.Time
}

// ifaceTouched отмечает момент нашей операции с интерфейсами: кинетик после
// этого перезагружает свой файрвол (хук netfilter.d), и iptables-restore
// магитрикла в эти секунды падает. записи в магитрикл ждут паузу.
func (e *Engine) ifaceTouched() {
	e.mu.Lock()
	e.ifaceAt = e.now()
	e.mu.Unlock()
}

func (e *Engine) settleAfterIfaceOp() {
	e.mu.Lock()
	at := e.ifaceAt
	e.mu.Unlock()
	if at.IsZero() {
		return
	}
	if d := 3*time.Second - e.now().Sub(at); d > 0 {
		time.Sleep(d)
	}
}

func New(st *store.Store, b platform.Backend, mt *magitrickle.Client) *Engine {
	return &Engine{
		store:     st,
		backend:   b,
		mt:        mt,
		wake:      make(chan string, 32),
		lastApply: map[string]time.Time{},
		extFails:  map[string]int{},
	}
}

func (e *Engine) applyAllowed(pool string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.lastApply[pool]
	return !ok || e.now().Sub(t) >= minApplyInterval
}

func (e *Engine) markApply(pool string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lastApply[pool] = e.now()
}

func (e *Engine) now() time.Time {
	if e.Clock != nil {
		return e.Clock()
	}
	return time.Now()
}

func (e *Engine) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	e.stop = cancel
	go e.loop(ctx)
}

func (e *Engine) Stop() {
	if e.stop != nil {
		e.stop()
	}
}

func (e *Engine) Wake(pool string) {
	select {
	case e.wake <- pool:
	default:
	}
}

func (e *Engine) loop(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	bundleTicker := time.NewTicker(30 * time.Second)
	defer bundleTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case pool := <-e.wake:
			e.checkPool(pool)
		case <-bundleTicker.C:
			e.checkBundles()
		case <-ticker.C:
			for _, pool := range e.store.Pools() {
				st := e.store.State(pool.Name)
				due := st.LastCheck.IsZero() || e.now().Sub(st.LastCheck) >= time.Duration(pool.Settings.CheckIntervalSec)*time.Second
				if due {
					e.checkPool(pool.Name)
				}
			}
		}
	}
}

func (e *Engine) CheckNow(pool string) {
	e.Wake(pool)
}

const extMemberFailThreshold = 2

func (e *Engine) memberDeviceHealth(member string, slots []platform.SlotInfo) (device string, healthy bool, external bool) {
	if p, ok := e.store.Pool(member); ok {
		if p.Disabled {
			return p.DeviceName(), false, false
		}
		return p.DeviceName(), e.store.State(member).Mode == store.ModeUp, false
	}
	for _, sl := range slots {
		if sl.Device == member {
			if !sl.LinkUp {
				return member, false, true
			}
			if probe := e.store.IfaceProbe(member); probe != nil {
				return member, e.deviceProbeOK(member, *probe), true
			}
			fresh := sl.Connected
			if !fresh {
				hs := e.backend.IfaceHandshake(member)
				fresh = hs >= 0 && hs < 180
			}
			return member, fresh, true
		}
	}
	return member, false, true
}

func (e *Engine) CheckBundlesNow() {
	e.checkBundles()
}

func (e *Engine) checkBundles() {
	bundles := e.store.Bundles()
	if len(bundles) == 0 {
		return
	}
	e.actMu.Lock()
	defer e.actMu.Unlock()
	slots, slotsErr := e.backend.Slots()
	for _, b := range bundles {
		if len(b.Members) == 0 || len(b.Groups) == 0 {
			continue
		}
		cur := e.store.BundleState(b.Name).Member
		sel := ""
		for _, m := range b.Members {
			dev, healthy, external := e.memberDeviceHealth(m, slots)
			if external && slotsErr == nil && !healthy {
				key := b.Name + "/" + m
				e.extFails[key]++
				if e.extFails[key] < extMemberFailThreshold {
					if dev == cur {
						sel = dev
						break
					}
					continue
				}
			}
			if external {
				delete(e.extFails, b.Name+"/"+m)
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

func (e *Engine) RotateNow(pool string) error {
	e.actMu.Lock()
	defer e.actMu.Unlock()
	p, ok := e.store.Pool(pool)
	if !ok {
		return fmt.Errorf("pool %q not found", pool)
	}
	if p.Disabled {
		return fmt.Errorf("пул %q выключен, сначала включите его", pool)
	}
	e.rotate(p, e.store.State(pool), "")
	return nil
}

func (e *Engine) SetActive(pool, file string) error {
	e.actMu.Lock()
	defer e.actMu.Unlock()
	p, ok := e.store.Pool(pool)
	if !ok {
		return fmt.Errorf("pool %q not found", pool)
	}
	if p.Disabled {
		return fmt.Errorf("пул %q выключен, сначала включите его", pool)
	}
	if _, ok := p.ConfigByFile(file); !ok {
		return fmt.Errorf("config %q not found in pool %q", file, pool)
	}
	e.store.MutateState(pool, func(s *store.PoolState) {
		delete(s.Cooldowns, file)
	})
	return e.applyConfig(p, file)
}

func (e *Engine) DisablePool(pool string) error {
	e.actMu.Lock()
	defer e.actMu.Unlock()
	p, ok := e.store.Pool(pool)
	if !ok {
		return fmt.Errorf("pool %q not found", pool)
	}
	if p.Disabled {
		return nil
	}
	if err := e.store.SetPoolDisabled(pool, true); err != nil {
		return err
	}
	e.ifaceTouched()
	if err := e.backend.Down(p); err != nil {
		e.store.LogEvent(pool, "disable", "интерфейс не выключился: "+err.Error())
	}
	e.suspendGroups(p)
	e.store.MutateState(pool, func(s *store.PoolState) {
		s.Mode = store.ModeFallback
		s.LastResult = "выключен вручную"
		s.ConsecFails = 0
	})
	e.store.LogEvent(pool, "disable", "пул выключен вручную, ротация остановлена")
	e.mu.Lock()
	delete(e.lastApply, pool)
	e.mu.Unlock()
	return nil
}

func (e *Engine) EnablePool(pool string) error {
	e.actMu.Lock()
	defer e.actMu.Unlock()
	p, ok := e.store.Pool(pool)
	if !ok {
		return fmt.Errorf("pool %q not found", pool)
	}
	if !p.Disabled {
		return nil
	}
	if err := e.store.SetPoolDisabled(pool, false); err != nil {
		return err
	}
	e.store.LogEvent(pool, "enable", "пул включён вручную")
	st := e.store.State(pool)
	eligible := e.eligible(p, st)
	if st.ActiveFile != "" {
		if _, ok := p.ConfigByFile(st.ActiveFile); ok {
			delete(st.Cooldowns, st.ActiveFile)
			e.store.MutateState(pool, func(s *store.PoolState) {
				delete(s.Cooldowns, st.ActiveFile)
			})
			return e.applyConfig(p, st.ActiveFile)
		}
	}
	if len(eligible) > 0 {
		return e.applyConfig(p, eligible[0].File)
	}
	e.Wake(pool)
	return nil
}

func (e *Engine) PoolUp(pool string) error {
	e.actMu.Lock()
	defer e.actMu.Unlock()
	p, ok := e.store.Pool(pool)
	if !ok {
		return fmt.Errorf("pool %q not found", pool)
	}
	if p.Disabled {
		return fmt.Errorf("пул %q выключен, сначала включите его", pool)
	}
	st := e.store.State(pool)
	if st.ActiveFile == "" {
		eligible := e.eligible(p, st)
		if len(eligible) == 0 {
			return fmt.Errorf("pool %q has no eligible configs", pool)
		}
		return e.applyConfig(p, eligible[0].File)
	}
	e.ifaceTouched()
	if err := e.backend.Up(p); err != nil {
		return err
	}
	restored := false
	e.store.MutateState(pool, func(s *store.PoolState) {
		if s.Mode == store.ModeFallback {
			s.Mode = store.ModeUp
			restored = true
		}
	})
	if restored {
		e.restoreGroups(p, e.store.State(pool))
		e.restoreReboundGroups(p)
	}
	return nil
}

func (e *Engine) PoolDown(pool string) error {
	e.actMu.Lock()
	defer e.actMu.Unlock()
	p, ok := e.store.Pool(pool)
	if !ok {
		return fmt.Errorf("pool %q not found", pool)
	}
	if p.Disabled {
		return fmt.Errorf("пул %q выключен, сначала включите его", pool)
	}
	e.ifaceTouched()
	if err := e.backend.Down(p); err != nil {
		return err
	}
	e.store.MutateState(pool, func(s *store.PoolState) {
		s.Mode = store.ModeFallback
		s.Since = e.now()
		s.LastResult = "manual down"
	})
	return nil
}

func (e *Engine) checkPool(name string) {
	e.actMu.Lock()
	defer e.actMu.Unlock()
	p, ok := e.store.Pool(name)
	if !ok || p.Disabled {
		return
	}
	st := e.store.State(name)
	if len(p.EligibleConfigs()) == 0 && st.ActiveFile == "" {
		e.store.MutateState(name, func(s *store.PoolState) {
			s.LastCheck = e.now()
			s.LastResult = "no eligible configs"
		})
		return
	}
	if st.Mode == store.ModeFallback {
		e.tryRecover(p, st)
		return
	}
	if st.ActiveFile == "" {
		e.rotate(p, st, "")
		return
	}
	e.probeAndMaybeRotate(p, st)
}

func (e *Engine) probeAndMaybeRotate(p store.Pool, st *store.PoolState) {
	if e.now().Unix() < st.GraceUntil {
		e.store.MutateState(p.Name, func(s *store.PoolState) {
			s.LastCheck = e.now()
			s.LastResult = "settling after apply"
		})
		return
	}
	verdict, detail := e.probe(p)
	e.store.MutateState(p.Name, func(s *store.PoolState) {
		s.LastCheck = e.now()
		s.LastResult = detail
	})
	if verdict != probeFail {
		if st.ConsecFails > 0 && verdict == probeOK {
			e.store.MutateState(p.Name, func(s *store.PoolState) { s.ConsecFails = 0 })
		}
		return
	}
	if e.wanDown() {
		e.store.MutateState(p.Name, func(s *store.PoolState) {
			s.LastResult = detail + ", канал провайдера недоступен, отказ не засчитан"
		})
		return
	}
	fails := st.ConsecFails + 1
	e.store.MutateState(p.Name, func(s *store.PoolState) { s.ConsecFails = fails })
	if fails < p.Settings.FailThreshold {
		return
	}
	e.store.LogEvent(p.Name, "rotate", fmt.Sprintf("config %s failed %d checks (%s), rotating", st.ActiveFile, fails, detail))
	e.rotate(p, e.store.State(p.Name), st.ActiveFile)
}

const (
	probeFail  = 0
	probeOK    = 1
	probeLossy = 2
)

func (e *Engine) probe(p store.Pool) (int, string) {
	status, err := e.backend.Status(p)
	if err != nil {
		return probeFail, "status error: " + err.Error()
	}
	if !status.LinkUp {
		return probeFail, "link down"
	}
	target := p.Settings.ProbeHost
	var gotReply bool
	var rtt int
	var kind string
	if store.IsHTTPProbe(target) {
		kind = "http"
		gotReply, rtt, _ = httpProbe(p.DeviceName(), target, 8*time.Second)
	} else {
		kind = "icmp"
		gotReply, rtt, _ = e.backend.Probe(p, target)
	}
	if gotReply && rtt > 0 && p.Settings.MaxRTTms > 0 && rtt > p.Settings.MaxRTTms {
		return probeFail, fmt.Sprintf("%s %dms выше порога %dms", kind, rtt, p.Settings.MaxRTTms)
	}
	rttSuffix := ""
	if rtt > 0 {
		rttSuffix = fmt.Sprintf(", %s %dms", kind, rtt)
	}
	if !gotReply {
		if status.HandshakeAgo >= 0 && status.HandshakeAgo <= 120 {
			if p.Settings.MaxRTTms > 0 {
				return probeLossy, fmt.Sprintf("ok (handshake %ds ago, %s lossy), отказы не сброшены: задан порог %dms", status.HandshakeAgo, kind, p.Settings.MaxRTTms)
			}
			return probeOK, fmt.Sprintf("ok (handshake %ds ago, %s lossy)", status.HandshakeAgo, kind)
		}
		return probeFail, "probe " + target + " failed"
	}
	return probeOK, fmt.Sprintf("ok (handshake %ds ago%s)", status.HandshakeAgo, rttSuffix)
}

type wanCacheEntry struct {
	at   time.Time
	down bool
}

func (e *Engine) wanDown() bool {
	if !e.store.WANProbeEnabled() {
		return false
	}
	if e.WANProbeFn != nil {
		return e.WANProbeFn()
	}
	e.mu.Lock()
	c := e.wanCache
	e.mu.Unlock()
	if !c.at.IsZero() && e.now().Sub(c.at) < 15*time.Second {
		return c.down
	}
	cfg := e.store.WANProbe()
	var ok bool
	var rtt int
	if store.IsHTTPProbe(cfg.Target) {
		ok, rtt, _ = httpProbe("", cfg.Target, 6*time.Second)
	} else {
		ok, rtt = pingNoBind(cfg.Target)
	}
	down := !ok || (cfg.MaxRTTms > 0 && rtt > cfg.MaxRTTms)
	e.mu.Lock()
	e.wanCache = wanCacheEntry{at: e.now(), down: down}
	e.mu.Unlock()
	return down
}

var wanPingRTTRe = regexp.MustCompile(`(?:rtt|round-trip) min/avg/max(?:/(?:mdev|stddev))? = [\d.]+/([\d.]+)/`)

func pingNoBind(target string) (bool, int) {
	cmd := exec.Command("ping", "-c", "2", "-W", "3", target)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, 0
	}
	if m := wanPingRTTRe.FindStringSubmatch(string(out)); m != nil {
		if ms, err := strconv.ParseFloat(m[1], 64); err == nil {
			return true, int(ms)
		}
	}
	return true, 0
}

func (e *Engine) deviceProbeOK(device string, cfg store.ProbeConfig) bool {
	var ok bool
	var rtt int
	if store.IsHTTPProbe(cfg.Target) {
		ok, rtt, _ = httpProbe(device, cfg.Target, 6*time.Second)
	} else {
		ok, rtt = e.backend.ProbeDevice(device, cfg.Target)
	}
	if !ok {
		return false
	}
	return cfg.MaxRTTms <= 0 || rtt <= cfg.MaxRTTms
}

func (e *Engine) DeviceProbeStatus(device string) string {
	probe := e.store.IfaceProbe(device)
	if probe == nil {
		return ""
	}
	cfg := probe.Normalized()
	var ok bool
	var rtt int
	if store.IsHTTPProbe(cfg.Target) {
		ok, rtt, _ = httpProbe(device, cfg.Target, 5*time.Second)
	} else {
		ok, rtt = e.backend.ProbeDevice(device, cfg.Target)
	}
	if !ok {
		return "проба: нет ответа"
	}
	if cfg.MaxRTTms > 0 && rtt > cfg.MaxRTTms {
		return fmt.Sprintf("проба: %dms > порога %dms", rtt, cfg.MaxRTTms)
	}
	return fmt.Sprintf("проба: ок %dms", rtt)
}

func (e *Engine) eligible(p store.Pool, st *store.PoolState) []store.ManagedConfig {
	now := e.now().Unix()
	var out []store.ManagedConfig
	for _, c := range p.Configs {
		if !c.Enabled {
			continue
		}
		if until, ok := st.Cooldowns[c.File]; ok && now < until {
			continue
		}
		out = append(out, c)
	}
	return out
}

func (e *Engine) rotate(p store.Pool, st *store.PoolState, failedActive string) {
	if failedActive != "" {
		e.store.MutateState(p.Name, func(s *store.PoolState) {
			s.Cooldowns[failedActive] = e.now().Add(time.Duration(p.Settings.CooldownMin) * time.Minute).Unix()
		})
	}
	if !e.applyAllowed(p.Name) {
		return
	}
	for {
		pool, _ := e.store.Pool(p.Name)
		state := e.store.State(p.Name)
		startIdx := pool.IndexByFile(state.ActiveFile)
		chosen := -1
		for k := 0; k < len(pool.Configs); k++ {
			idx := (startIdx + 1 + k) % len(pool.Configs)
			c := pool.Configs[idx]
			if !c.Enabled {
				continue
			}
			if until, ok := state.Cooldowns[c.File]; ok && e.now().Unix() < until {
				continue
			}
			chosen = idx
			break
		}
		if chosen < 0 {
			e.enterFallback(pool, state)
			return
		}
		if err := e.applyConfig(pool, pool.Configs[chosen].File); err == nil {
			return
		}
	}
}

func (e *Engine) applyConfig(p store.Pool, file string) error {
	cfg, err := e.store.LoadConfigFile(p.Name, file)
	if err != nil {
		e.store.MutateState(p.Name, func(s *store.PoolState) {
			s.LastError = "load: " + err.Error()
			s.Cooldowns[file] = e.now().Add(time.Duration(p.Settings.CooldownMin) * time.Minute).Unix()
		})
		return err
	}
	e.ifaceTouched()
	if err := e.backend.Apply(p, cfg); err != nil {
		e.store.MutateState(p.Name, func(s *store.PoolState) {
			s.LastError = "apply: " + err.Error()
			s.Cooldowns[file] = e.now().Add(time.Duration(p.Settings.CooldownMin) * time.Minute).Unix()
		})
		e.store.LogEvent(p.Name, "apply-failed", file+": "+err.Error())
		return err
	}
	e.markApply(p.Name)
	restored := false
	e.store.MutateState(p.Name, func(s *store.PoolState) {
		s.ActiveFile = file
		s.ConsecFails = 0
		s.Rotations++
		s.GraceUntil = e.now().Add(settleSeconds * time.Second).Unix()
		s.LastResult = "applied, settling"
		s.LastError = ""
		if s.Mode == store.ModeFallback {
			s.Mode = store.ModeUp
			restored = true
		}
	})
	if restored {
		e.restoreGroups(p, e.store.State(p.Name))
		e.restoreReboundGroups(p)
	}
	e.store.LogEvent(p.Name, "applied", file+" -> "+cfg.Endpoint())
	return nil
}

func (e *Engine) enterFallback(p store.Pool, st *store.PoolState) {
	was := st.Mode
	e.store.MutateState(p.Name, func(s *store.PoolState) {
		s.Mode = store.ModeFallback
		s.Since = e.now()
		s.LastResult = "all configs failed"
	})
	e.store.LogEvent(p.Name, "fallback", "all configs failed, mode "+p.Settings.Fallback)
	if was == store.ModeFallback {
		return
	}
	if fbName, ok := store.FallbackPoolName(p.Settings.Fallback); ok {
		fbPool, exists := e.store.Pool(fbName)
		if !exists || fbPool.Disabled {
			e.store.LogEvent(p.Name, "fallback", "fallback pool "+fbName+" unavailable, going direct")
			e.ifaceTouched()
	if err := e.backend.Down(p); err != nil {
				log.Printf("pool %s: down failed: %v", p.Name, err)
			}
			e.suspendGroups(p)
			return
		}
		e.ifaceTouched()
	if err := e.backend.Down(p); err != nil {
			log.Printf("pool %s: down failed: %v", p.Name, err)
		}
		e.rebindGroups(p, fbPool.DeviceName())
		return
	}
	if p.Settings.Fallback == store.FallbackDirect {
		e.ifaceTouched()
	if err := e.backend.Down(p); err != nil {
			log.Printf("pool %s: down failed: %v", p.Name, err)
		}
		e.suspendGroups(p)
	}
}

func (e *Engine) tryRecover(p store.Pool, st *store.PoolState) {
	pool, _ := e.store.Pool(p.Name)
	state := e.store.State(p.Name)
	eligible := e.eligible(pool, state)
	if len(eligible) == 0 {
		e.store.MutateState(p.Name, func(s *store.PoolState) {
			s.LastCheck = e.now()
			s.LastResult = "fallback, waiting for cooldown"
		})
		return
	}
	e.rotate(pool, state, "")
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

// magitrickle 0.8.2: single-group PUT не пересоздаёт iptables-цепочку, если
// рантайм группы уже выключен; пересборку даёт только массовый PUT списка.
func (e *Engine) applyGroupChanges(key string, mutate func(groups []magitrickle.Group) bool) error {
	e.settleAfterIfaceOp()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	err := e.mt.MutateGroups(ctx, mutate)
	if err == nil {
		return nil
	}
	e.store.LogEvent(key, "magitrickle", "groups save failed: "+err.Error())
	e.healMagitrickle(key)
	return err
}

// сбой массового PUT оставляет рантайм magitrickle усечённым, но конфиг
// на диске цел: рестарт демона собирает группы обратно.
func (e *Engine) healMagitrickle(key string) {
	if e.now().Sub(e.mtHealAt) < 10*time.Minute {
		return
	}
	e.mtHealAt = e.now()
	if err := e.backend.RestartMagitrickle(); err != nil {
		e.store.LogEvent(key, "magitrickle", "автоперезапуск не удался: "+err.Error())
		return
	}
	e.store.LogEvent(key, "magitrickle", "автоперезапуск после сбоя сохранения")
}

func (e *Engine) suspendGroups(p store.Pool) {
	if e.mt == nil {
		e.store.LogEvent(p.Name, "magitrickle", "client unavailable, groups not touched")
		return
	}
	bundled := e.bundleGroupIDs()
	dev := p.DeviceName()
	var disabled []string
	err := e.applyGroupChanges(p.Name, func(groups []magitrickle.Group) bool {
		changed := false
		for i := range groups {
			g := &groups[i]
			if g.Interface == dev && g.Enable && !bundled[g.ID] {
				g.Enable = false
				disabled = append(disabled, g.ID)
				changed = true
			}
		}
		return changed
	})
	if err != nil || len(disabled) == 0 {
		return
	}
	e.store.MutateState(p.Name, func(s *store.PoolState) {
		s.DisabledGroups = append(s.DisabledGroups, disabled...)
	})
	e.store.LogEvent(p.Name, "magitrickle", "disabled groups: "+fmt.Sprint(disabled))
}

func (e *Engine) rebindGroups(p store.Pool, targetDevice string) {
	if e.mt == nil {
		e.store.LogEvent(p.Name, "magitrickle", "client unavailable, groups not rebound")
		return
	}
	dev := p.DeviceName()
	var rebound []string
	err := e.applyGroupChanges(p.Name, func(groups []magitrickle.Group) bool {
		changed := false
		for i := range groups {
			if groups[i].Interface == dev {
				groups[i].Interface = targetDevice
				rebound = append(rebound, groups[i].ID)
				changed = true
			}
		}
		return changed
	})
	if err != nil || len(rebound) == 0 {
		return
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
