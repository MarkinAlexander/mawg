package rotator

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"mawg/internal/cascade"
	"mawg/internal/magitrickle"
	"mawg/internal/platform"
	"mawg/internal/premium"
	"mawg/internal/store"
)

const settleSeconds = 15
const minApplyInterval = 20 * time.Second

type Engine struct {
	PremiumClient *premium.Client
	store         *store.Store
	backend       platform.Backend
	mt            *magitrickle.Client

	Clock func() time.Time

	WANProbeFn func() bool

	wake        chan string
	stop        context.CancelFunc
	mu          sync.Mutex
	actMu       sync.Mutex
	lastApply   map[string]time.Time
	extFails    map[string]int
	wanCache    wanCacheEntry
	mtHealAt    time.Time
	mtEverAlive bool
	mtSyncAt    time.Time

	ifaceAt time.Time

	probeMu     sync.Mutex
	probeCache  map[string]probeCacheEntry
	probeBusy   map[string]bool
	tunnelAddrs map[string]string
	tunnelAt    time.Time

	casc     *cascade.Manager
	tickN    int
	polFails map[string]int
	polOks   map[string]int
}

const probeStatusTTL = 30 * time.Second

type probeCacheEntry struct {
	status string
	at     time.Time
}

// ifaceTouched отмечает момент нашей операции с интерфейсами: кинетик после
// этого перезагружает свой файрвол (хук netfilter.d), и iptables-restore
// магитрикла в эти секунды падает. записи в магитрикл ждут паузу.
func (e *Engine) ifaceTouched() {
	e.mu.Lock()
	e.ifaceAt = e.now()
	e.mu.Unlock()
	e.probeMu.Lock()
	e.tunnelAt = time.Time{}
	e.probeMu.Unlock()
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
	e := &Engine{
		PremiumClient: premium.NewClient(),
		store:         st,
		backend:       b,
		mt:            mt,
		wake:          make(chan string, 32),
		lastApply:     map[string]time.Time{},
		extFails:      map[string]int{},
		probeCache:    map[string]probeCacheEntry{},
		probeBusy:     map[string]bool{},
		tunnelAddrs:   map[string]string{},
		polFails:      map[string]int{},
		polOks:        map[string]int{},
	}
	if mt != nil {
		e.casc = cascade.New(st, mt)
	}
	return e
}

// Cascade - менеджер служебных групп-каскадов (nil без magitrickle).
func (e *Engine) Cascade() *cascade.Manager { return e.casc }

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
			e.refreshProbedExternals()
			e.tickN++
			e.checkGroupPolicies()
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
	e.monitorMagitrickle()
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
		return fmt.Errorf("пул %q не найден", pool)
	}
	if p.Disabled {
		return fmt.Errorf("пул %q выключен, сначала включите его", pool)
	}
	e.rotate(p, e.store.State(pool), "")
	e.saveRouterConfig(pool)
	return nil
}

func (e *Engine) SetActive(pool, file string) error {
	e.actMu.Lock()
	defer e.actMu.Unlock()
	p, ok := e.store.Pool(pool)
	if !ok {
		return fmt.Errorf("пул %q не найден", pool)
	}
	if p.Disabled {
		return fmt.Errorf("пул %q выключен, сначала включите его", pool)
	}
	if _, ok := p.ConfigByFile(file); !ok {
		return fmt.Errorf("конфиг %q не найден в пуле %q", file, pool)
	}
	e.store.MutateState(pool, func(s *store.PoolState) {
		delete(s.Cooldowns, file)
	})
	if err := e.applyConfig(p, file); err != nil {
		return err
	}
	e.saveRouterConfig(pool)
	return nil
}

func (e *Engine) DisablePool(pool string) error {
	e.actMu.Lock()
	defer e.actMu.Unlock()
	p, ok := e.store.Pool(pool)
	if !ok {
		return fmt.Errorf("пул %q не найден", pool)
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
	e.saveRouterConfig(pool)
	return nil
}

// saveRouterConfig сбрасывает running в сохраненный конфиг роутера: веб
// Keenetic показывает тумблеры из сохраненного. Только ручные действия -
// при ротациях флеш не дергаем.
func (e *Engine) saveRouterConfig(pool string) {
	if err := e.backend.SaveConfig(); err != nil {
		e.store.LogEvent(pool, "save-config", "не сохранился: "+err.Error())
	}
}

func (e *Engine) EnablePool(pool string) error {
	e.actMu.Lock()
	defer e.actMu.Unlock()
	p, ok := e.store.Pool(pool)
	if !ok {
		return fmt.Errorf("пул %q не найден", pool)
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
		if err := e.applyConfig(p, eligible[0].File); err != nil {
			return err
		}
		e.saveRouterConfig(pool)
		return nil
	}
	e.Wake(pool)
	return nil
}

func (e *Engine) PoolUp(pool string) error {
	e.actMu.Lock()
	defer e.actMu.Unlock()
	p, ok := e.store.Pool(pool)
	if !ok {
		return fmt.Errorf("пул %q не найден", pool)
	}
	if p.Disabled {
		return fmt.Errorf("пул %q выключен, сначала включите его", pool)
	}
	st := e.store.State(pool)
	if st.ActiveFile == "" {
		eligible := e.eligible(p, st)
		if len(eligible) == 0 {
			return fmt.Errorf("в пуле %q нет конфигов, пригодных для ротации", pool)
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
		e.restoreDegraded(p.DeviceName())
	}
	return nil
}

func (e *Engine) PoolDown(pool string) error {
	e.actMu.Lock()
	defer e.actMu.Unlock()
	p, ok := e.store.Pool(pool)
	if !ok {
		return fmt.Errorf("пул %q не найден", pool)
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
	if !ok || p.Disabled || p.Settings.EngineMode != "" {
		return
	}
	st := e.store.State(name)
	if len(p.EligibleConfigs()) == 0 {
		if st.ActiveFile != "" {
			// конфигов не осталось (удалили последний / выключили
			// все): туннель надо погасить - интерфейс роутера иначе
			// продолжает работать с последним применённым конфигом,
			// которого в пуле уже нет, и пул выглядит «работает»
			if err := e.backend.Down(p); err != nil {
				e.store.LogEvent(name, "rotate", "конфигов нет, интерфейс не выключился: "+err.Error())
			} else {
				e.ifaceTouched()
			}
			e.store.MutateState(name, func(s *store.PoolState) {
				s.LastCheck = e.now()
				s.LastResult = "конфигов нет - туннель остановлен"
				s.ActiveFile = ""
				s.Mode = store.ModeFallback
				s.ConsecFails = 0
			})
			e.store.LogEvent(name, "rotate", "конфигов не осталось - туннель остановлен")
			e.saveRouterConfig(name)
			return
		}
		e.store.MutateState(name, func(s *store.PoolState) {
			s.LastCheck = e.now()
			s.LastResult = "конфигов для ротации нет"
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

// статусы проб внешних интерфейсов тяжелые (ping до 2с, http до 5с), поэтому
// они живут в кэше с TTL и обновляются фоном: /ifaces отвечает мгновенно,
// а строки проб не мигают из-за того, что чья-то проба не успела в бюджет.
func (e *Engine) DeviceProbeStatus(device string) string {
	e.probeMu.Lock()
	defer e.probeMu.Unlock()
	return e.probeCache[device].status
}

func (e *Engine) InvalidateProbeStatus(device string) {
	e.probeMu.Lock()
	delete(e.probeCache, device)
	e.probeMu.Unlock()
	e.refreshProbeStatus(device)
}

// TunnelAddresses: адрес -> устройство, кэш 30с. Для проверки конфигов
// на коллизии без живых вызовов на каждый запрос.
func (e *Engine) TunnelAddresses() map[string]string {
	e.probeMu.Lock()
	if e.tunnelAt.IsZero() || e.now().Sub(e.tunnelAt) >= 30*time.Second {
		e.probeMu.Unlock()
		m := map[string]string{}
		if tunnels, err := e.backend.SysTunnels(); err == nil {
			for _, sl := range tunnels {
				if sl.Address != "" && sl.LinkUp {
					m[normAddr(sl.Address)] = sl.Device
				}
			}
		}
		if slots, err := e.backend.Slots(); err == nil {
			for _, sl := range slots {
				if sl.Address != "" && sl.LinkUp {
					if _, exists := m[normAddr(sl.Address)]; !exists {
						m[normAddr(sl.Address)] = sl.Device
					}
				}
			}
		}
		e.probeMu.Lock()
		e.tunnelAddrs = m
		e.tunnelAt = e.now()
		e.probeMu.Unlock()
		return m
	}
	defer e.probeMu.Unlock()
	return e.tunnelAddrs
}

func (e *Engine) refreshProbedExternals() {
	for _, dev := range e.store.ProbedExternals() {
		e.refreshProbeStatus(dev)
	}
}

func (e *Engine) refreshProbeStatus(device string) {
	e.probeMu.Lock()
	if c, ok := e.probeCache[device]; ok && e.now().Sub(c.at) < probeStatusTTL {
		e.probeMu.Unlock()
		return
	}
	if e.probeBusy[device] {
		e.probeMu.Unlock()
		return
	}
	e.probeBusy[device] = true
	e.probeMu.Unlock()
	go func() {
		status := e.probeStatus(device)
		e.probeMu.Lock()
		e.probeCache[device] = probeCacheEntry{status: status, at: e.now()}
		delete(e.probeBusy, device)
		e.probeMu.Unlock()
	}()
}

func (e *Engine) probeStatus(device string) string {
	probe := e.store.IfaceProbe(device)
	if probe == nil {
		return ""
	}
	cfg := probe.Normalized()
	var ok bool
	var rtt int
	var perr error
	if store.IsHTTPProbe(cfg.Target) {
		ok, rtt, perr = httpProbe(device, cfg.Target, 5*time.Second)
	} else {
		ok, rtt = e.backend.ProbeDevice(device, cfg.Target)
	}
	if !ok {
		if perr != nil {
			return "проба: нет ответа (" + perr.Error() + ")"
		}
		return "проба: нет ответа"
	}
	if cfg.MaxRTTms > 0 && rtt > cfg.MaxRTTms {
		return fmt.Sprintf("проба: %dms > порога %dms", rtt, cfg.MaxRTTms)
	}
	return fmt.Sprintf("проба: ок %dms", rtt)
}

func (e *Engine) eligible(p store.Pool, st *store.PoolState) []store.ManagedConfig {
	now := e.now().Unix()
	own := p.DeviceName()
	var out []store.ManagedConfig
	for _, c := range p.Configs {
		if !c.Enabled {
			continue
		}
		if until, ok := st.Cooldowns[c.File]; ok && now < until {
			continue
		}
		if e.addressTaken(p.Name, own, c) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// addressTaken: внутренние адреса конфига держит чужой поднятый
// интерфейс - ndm такой конфиг не поднимет, из ротации исключаем.
func (e *Engine) addressTaken(poolName, ownDevice string, c store.ManagedConfig) bool {
	addrs := c.Addresses
	if len(addrs) == 0 {
		parsed, err := e.store.LoadConfigFile(poolName, c.File)
		if err != nil {
			return false
		}
		addrs = parsed.Addresses
		if len(addrs) > 0 {
			e.store.SetConfigAddresses(poolName, c.File, addrs)
		}
	}
	for addr, dev := range e.TunnelAddresses() {
		if dev == ownDevice {
			continue
		}
		for _, a := range addrs {
			if normAddr(a) == normAddr(addr) {
				return true
			}
		}
	}
	return false
}

func normAddr(a string) string {
	return strings.TrimSpace(strings.SplitN(strings.TrimSpace(a), "/", 2)[0])
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
			if e.addressTaken(p.Name, p.DeviceName(), c) {
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
		if p.Premium {
			err = fmt.Errorf("Premium configuration could not be loaded")
		}
		e.store.MutateState(p.Name, func(s *store.PoolState) {
			s.LastError = "load: " + err.Error()
			s.Cooldowns[file] = e.now().Add(time.Duration(p.Settings.CooldownMin) * time.Minute).Unix()
		})
		return err
	}
	e.ifaceTouched()
	if err := e.backend.Apply(p, cfg); err != nil {
		if p.Premium {
			err = fmt.Errorf("Premium interface apply failed")
		}
		e.store.MutateState(p.Name, func(s *store.PoolState) {
			s.LastError = "apply: " + err.Error()
			s.Cooldowns[file] = e.now().Add(time.Duration(p.Settings.CooldownMin) * time.Minute).Unix()
		})
		e.store.LogEvent(p.Name, "apply-failed", file+": "+err.Error())
		return err
	}
	return e.recordApplied(p, file, cfg.Endpoint())
}

func (e *Engine) recordApplied(p store.Pool, file, endpoint string) error {
	e.markApply(p.Name)
	restored := false
	err := e.store.MutateState(p.Name, func(s *store.PoolState) {
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
		e.restoreDegraded(p.DeviceName())
	}
	e.store.LogEvent(p.Name, "applied", file+" -> "+endpoint)
	if e.casc != nil {
		go e.casc.SyncSourceEndpoints(context.Background(), "pool:"+p.Name)
	}
	if err != nil {
		return fmt.Errorf("configuration applied but active state could not be saved")
	}
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
	if fbDev, ok := store.FallbackIfaceName(p.Settings.Fallback); ok {
		exists := false
		if slots, err := e.backend.Slots(); err == nil {
			for _, sl := range slots {
				if sl.Device == fbDev {
					exists = true
					break
				}
			}
		}
		e.ifaceTouched()
		if err := e.backend.Down(p); err != nil {
			log.Printf("pool %s: down failed: %v", p.Name, err)
		}
		if !exists {
			e.store.LogEvent(p.Name, "fallback", "fallback iface "+fbDev+" не найден, группы идут напрямую")
			e.suspendGroups(p)
			return
		}
		e.store.LogEvent(p.Name, "fallback", "группы переведены на интерфейс "+fbDev)
		e.rebindGroups(p, fbDev)
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
	if e.casc != nil {
		e.casc.BeforeMutate(mutate)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	err := e.mt.MutateGroups(ctx, mutate)
	if e.casc != nil {
		e.casc.AfterMutate()
	}
	if err == nil {
		e.mtEverAlive = true
		return nil
	}
	e.store.LogEvent(key, "magitrickle", "groups save failed: "+err.Error())
	e.healMagitrickle(key, 10*time.Minute)
	return err
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
		e.mt.RefreshShadow(ctx)
	}
}

// сбой массового PUT оставляет рантайм magitrickle усечённым, но конфиг
// на диске цел: рестарт демона собирает группы обратно. мертвому демону
// рестартим часто, живому после сбоя записи - редко.
func (e *Engine) healMagitrickle(key string, cooldown time.Duration) {
	if e.now().Sub(e.mtHealAt) < cooldown {
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

	if e.casc != nil && e.tickN%10 == 0 {
		e.casc.RefreshDNS(ctx)
	}

	groups, err := e.mt.GroupsWithRules(ctx)
	if err != nil {
		return
	}
	health := map[string]bool{}
	for _, pool := range e.store.Pools() {
		health[pool.DeviceName()] = !pool.Disabled && e.store.State(pool.Name).Mode == store.ModeUp
	}
	if slots, err := e.backend.Slots(); err == nil {
		for _, sl := range slots {
			if _, ok := health[sl.Device]; ok {
				continue
			}
			ok := sl.LinkUp
			if ok {
				if probe := e.store.IfaceProbe(sl.Device); probe != nil {
					ok = e.deviceProbeOK(sl.Device, *probe)
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

	degraded := e.store.DegradedGroups()
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
