package rotator

import (
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"time"

	"mawg/internal/store"
)

// полоса фоновых проб: не больше probeLaneMax одновременно; застрявшую
// (старше probeForceWait) не ждём - стартуем следующую параллельно.
// actMu по-прежнему сериализует сами пробы, полоса лишь раздаёт старты,
// чтобы из-за «стада» due-пулов не складывался минутный залп CPU
// (замер на прод-Кинетике, 82-док).
const probeLaneMax = 2
const probeForceWait = 15 * time.Second

const probeStatusTTL = 30 * time.Second

type probeCacheEntry struct {
	cfg  store.ProbeConfig
	done chan struct{}
	ok   bool
	rtt  int
	err  error
	at   time.Time
}

// tickProbes - один секундный тик полосы проб: стартуем максимум одну
// фоновую пробу; пока активная моложе probeForceWait - ждём её, старше -
// стартуем следующую параллельно (actMu разрулит), максимум probeLaneMax
// в полёте. Раньше все due-пулы прогонялись синхронно подряд в одном
// тике - 8 пулов давали залп на десятки секунд (рывки CPU, 82-док).
func (e *Engine) tickProbes(now time.Time) {
	switch n := e.laneCount(); {
	case n >= probeLaneMax:
		return
	case n == 1 && now.Sub(e.laneOldest()) < probeForceWait:
		return
	}
	name := e.firstDue(now)
	if name == "" {
		return
	}
	e.laneAdd(name, now)
	go e.runLaneProbe(name)
}

func (e *Engine) runLaneProbe(name string) {
	defer e.laneDone(name)
	e.checkPool(name)
}

// firstDue - первый пул (в порядке хранения), чья проба пора. Никогда не
// проверявшиеся пулы ждут стартового разброса (startDue): LastCheck не
// персистится (json:"-"), после рестарта без разбора всё «стадо» стреляло
// бы в первый же тик.
func (e *Engine) firstDue(now time.Time) string {
	e.ensureStartSpread(now)
	for _, p := range e.store.Pools() {
		if p.Disabled || p.Settings.EngineMode != "" {
			continue // checkPool всё равно no-op - полосу не занимаем
		}
		if e.laneHas(p.Name) {
			continue
		}
		st := e.store.State(p.Name)
		if st.LastCheck.IsZero() {
			if at, ok := e.startDue[p.Name]; ok && now.Before(at) {
				continue
			}
			return p.Name
		}
		if now.Sub(st.LastCheck) >= time.Duration(p.Settings.CheckIntervalSec)*time.Second {
			return p.Name
		}
	}
	return ""
}

// ensureStartSpread назначает момент первой пробы пулам, которые ещё ни
// разу не проверялись и в карту ещё не попали: по sorted-именам,
// отсрочка idx*CheckIntervalSec/N раскидывает «стадо» первого тика по
// всему интервалу. Карта живёт только в горутине loop.
func (e *Engine) ensureStartSpread(now time.Time) {
	var fresh []store.Pool
	pools := e.store.Pools()
	for _, p := range pools {
		if _, ok := e.startDue[p.Name]; ok {
			continue
		}
		if e.store.State(p.Name).LastCheck.IsZero() {
			fresh = append(fresh, p)
		}
	}
	if len(fresh) == 0 {
		return
	}
	sort.Slice(fresh, func(i, j int) bool { return fresh[i].Name < fresh[j].Name })
	n := len(pools)
	if n < 1 {
		n = 1
	}
	for _, p := range fresh {
		interval := p.Settings.CheckIntervalSec
		if interval <= 0 {
			interval = 30
		}
		e.startDue[p.Name] = now.Add(time.Duration(len(e.startDue)) * time.Duration(interval) * time.Second / time.Duration(n))
	}
}

func (e *Engine) laneCount() int {
	e.laneMu.Lock()
	defer e.laneMu.Unlock()
	return len(e.lane)
}

func (e *Engine) laneOldest() time.Time {
	e.laneMu.Lock()
	defer e.laneMu.Unlock()
	var oldest time.Time
	for _, at := range e.lane {
		if oldest.IsZero() || at.Before(oldest) {
			oldest = at
		}
	}
	return oldest
}

func (e *Engine) laneHas(name string) bool {
	e.laneMu.Lock()
	defer e.laneMu.Unlock()
	_, ok := e.lane[name]
	return ok
}

func (e *Engine) laneAdd(name string, at time.Time) {
	e.laneMu.Lock()
	e.lane[name] = at
	e.laneMu.Unlock()
}

func (e *Engine) laneDone(name string) {
	e.laneMu.Lock()
	delete(e.lane, name)
	e.laneMu.Unlock()
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
				s.ProbeOK, s.ProbeMs = false, 0
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
			s.ProbeOK, s.ProbeMs = false, 0
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
			s.ProbeOK, s.ProbeMs = false, 0
		})
		return
	}
	verdict, rtt, detail := e.probe(p)
	e.store.MutateState(p.Name, func(s *store.PoolState) {
		s.LastCheck = e.now()
		s.LastResult = detail
		s.ProbeOK = verdict == probeOK
		s.ProbeMs = rtt
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
	// ранний триггер умного каскада (86-док, слово владельца: «напрямую
	// проверили - фейл - переключились на варп и их же проверили, если и
	// там фейл - идут следующие»): после N отказов АКТИВНОГО конфига
	// сразу пробуем его же через Via; не ожил - конфиг проверен обоими
	// путями, к следующему немедленно
	if n := e.smartTriggerFails(p); n > 0 && fails >= n {
		if e.smartRescue(p) {
			e.store.MutateState(p.Name, func(s *store.PoolState) { s.ConsecFails = 0 })
			return
		}
		e.rotate(p, e.store.State(p.Name), st.ActiveFile)
		return
	}
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

func (e *Engine) probe(p store.Pool) (verdict int, rttMs int, detail string) {
	status, err := e.backend.Status(p)
	if err != nil {
		return probeFail, 0, "status error: " + err.Error()
	}
	if !status.LinkUp {
		return probeFail, 0, "link down"
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
		return probeFail, rtt, fmt.Sprintf("%s %dms выше порога %dms", kind, rtt, p.Settings.MaxRTTms)
	}
	rttSuffix := ""
	if rtt > 0 {
		rttSuffix = fmt.Sprintf(", %s %dms", kind, rtt)
	}
	if !gotReply {
		if status.HandshakeAgo >= 0 && status.HandshakeAgo <= 120 {
			if p.Settings.MaxRTTms > 0 {
				return probeLossy, 0, fmt.Sprintf("ok (handshake %ds ago, %s lossy), отказы не сброшены: задан порог %dms", status.HandshakeAgo, kind, p.Settings.MaxRTTms)
			}
			return probeOK, 0, fmt.Sprintf("ok (handshake %ds ago, %s lossy)", status.HandshakeAgo, kind)
		}
		return probeFail, 0, "probe " + target + " failed"
	}
	return probeOK, rtt, fmt.Sprintf("ok (handshake %ds ago%s)", status.HandshakeAgo, rttSuffix)
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

func (c *probeCacheEntry) healthy() bool {
	return c.ok && (c.cfg.MaxRTTms <= 0 || c.rtt <= c.cfg.MaxRTTms)
}

func (e *Engine) deviceProbe(device string, cfg store.ProbeConfig) *probeCacheEntry {
	e.probeMu.Lock()
	c := e.startDeviceProbe(device, cfg.Normalized())
	e.probeMu.Unlock()
	<-c.done
	return c
}

func (e *Engine) startDeviceProbe(device string, cfg store.ProbeConfig) *probeCacheEntry {
	if c := e.probeCache[device]; c != nil && c.cfg == cfg {
		select {
		case <-c.done:
			if e.now().Sub(c.at) < probeStatusTTL {
				return c
			}
		default:
			return c
		}
	}
	c := &probeCacheEntry{cfg: cfg, done: make(chan struct{}), at: e.now()}
	e.probeCache[device] = c
	go func() {
		if store.IsHTTPProbe(cfg.Target) {
			c.ok, c.rtt, c.err = httpProbe(device, cfg.Target, 6*time.Second)
		} else {
			c.ok, c.rtt = e.backend.ProbeDevice(device, cfg.Target)
		}
		close(c.done)
	}()
	return c
}

// статусы проб внешних интерфейсов тяжелые (ping до 2с, http до 5с), поэтому
// они живут в кэше с TTL и обновляются фоном: /ifaces отвечает мгновенно,
// а строки проб не мигают из-за того, что чья-то проба не успела в бюджет.
func (e *Engine) DeviceProbeStatus(device string) string {
	probe := e.store.IfaceProbe(device)
	if probe == nil {
		return ""
	}
	e.probeMu.Lock()
	defer e.probeMu.Unlock()
	c := e.probeCache[device]
	if c == nil || c.cfg != probe.Normalized() {
		return ""
	}
	select {
	case <-c.done:
		if e.now().Sub(c.at) < probeStatusTTL {
			return c.status()
		}
	default:
	}
	return ""
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
	probe := e.store.IfaceProbe(device)
	if probe == nil {
		return
	}
	e.probeMu.Lock()
	e.startDeviceProbe(device, probe.Normalized())
	e.probeMu.Unlock()
}

func (c *probeCacheEntry) status() string {
	if !c.ok {
		if c.err != nil {
			return "проба: нет ответа (" + c.err.Error() + ")"
		}
		return "проба: нет ответа"
	}
	if c.cfg.MaxRTTms > 0 && c.rtt > c.cfg.MaxRTTms {
		return fmt.Sprintf("проба: %dms > порога %dms", c.rtt, c.cfg.MaxRTTms)
	}
	return fmt.Sprintf("проба: ок %dms", c.rtt)
}
