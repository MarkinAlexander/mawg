package rotator

import (
	"fmt"
	"time"

	"mawg/internal/magitrickle"
	"mawg/internal/platform"
	"mawg/internal/store"
)

// Наборы интерфейсов: группы привязаны к первому живому участнику по
// порядку. Пул жив, когда подтверждён после применения (poolServing), а не
// по статусу «включён»: иначе пул с одним нерабочим конфигом после каждого
// кулдауна переприменяется и каждый раз ненадолго забирает группы набора.

// handshakeFresh - рукопожатие не старше одной сессии WireGuard: внешний
// туннель без пробы оценивается только по нему.
const handshakeFresh = 180 * time.Second

// handshakeRenewGap - настолько должно уйти вперёд рукопожатие между
// проверками, чтобы считаться обновлённым (WireGuard обновляет его раз в
// 120с). Одно рукопожатие держит туннель «живым» три проверки подряд.
const handshakeRenewGap = 10 * time.Second

type healthRules struct {
	source        string
	failCycles    int
	restoreCycles int
}

func (e *Engine) healthRules() healthRules {
	s := e.store.Settings().WithDefaults()
	return healthRules{s.HealthSource, s.BundleFailCycles, s.BundleRestoreCycles}
}

// poolServing - пул пропускает трафик: включён и подтверждён после
// последнего применения. Движковый пул узлы проверяет сам движок, режим
// «state» подтверждение выключает.
func (e *Engine) poolServing(p store.Pool, st *store.PoolState) bool {
	if p.Disabled || st.Mode != store.ModeUp {
		return false
	}
	return p.Settings.EngineMode != "" || e.healthRules().source == store.HealthState || st.Confirmed
}

// bundleTrust - гистерезис участника набора: текущего не бросаем после
// одной плохой проверки, на приоритетного возвращаемся после полосы
// хороших.
type bundleTrust struct {
	fails   int
	oks     int
	hs0     time.Time // рукопожатие в начале полосы успехов
	renewed bool      // рукопожатие в полосе обновилось
	seen    bool
}

func (t *bundleTrust) observe(ms MemberState, rules healthRules) {
	if !ms.OK {
		*t = bundleTrust{fails: t.fails + 1, seen: true}
		return
	}
	if !t.seen {
		*t = bundleTrust{seen: true}
		if ms.handshake {
			t.fails = rules.failCycles
		}
	}
	if t.oks == 0 {
		t.hs0 = ms.hsAt
	}
	t.oks++
	if ms.hsAt.Sub(t.hs0) >= handshakeRenewGap {
		t.renewed = true
	}
	if t.oks >= rules.restoreCycles && (!ms.handshake || t.renewed) {
		t.fails = 0
	}
}

// MemberState - здоровье участника набора: жив ли и почему нет.
type MemberState struct {
	Device string `json:"device"`
	OK     bool   `json:"ok"`
	Note   string `json:"note"`
	// Pending - результата пробы ещё нет (для панели: кэш пуст)
	Pending bool `json:"pending,omitempty"`
	// handshake - живость оценена по рукопожатию (внешний туннель без
	// пробы); hsAt - его время, ноль, если роутер его не отдал
	handshake bool
	hsAt      time.Time
	// down - интерфейс выключен: удерживать текущим нечего, уходим сразу
	down bool
}

func (e *Engine) memberState(member string, slots []platform.SlotInfo, probe func(string, store.ProbeConfig) *probeCacheEntry) MemberState {
	if p, ok := e.store.Pool(member); ok {
		st := e.store.State(member)
		ms := MemberState{Device: p.DeviceName()}
		ms.down = p.Disabled || st.Mode != store.ModeUp
		switch {
		case p.Disabled:
			ms.Note = "пул выключен"
		case st.Mode != store.ModeUp:
			ms.Note = "пул в фоллбэке"
		case !e.poolServing(p, st):
			ms.Note = "ждёт подтверждения пробой"
		default:
			ms.OK, ms.Note = true, st.LastResult
		}
		return ms
	}
	ms := MemberState{Device: member}
	var sl *platform.SlotInfo
	for i := range slots {
		if slots[i].Device == member {
			sl = &slots[i]
			break
		}
	}
	switch {
	case sl == nil:
		ms.Note = "интерфейс не найден"
	case !sl.LinkUp:
		ms.Note = "соединение опущено"
	default:
		if cfg := e.store.IfaceProbe(member); cfg != nil {
			if c := probe(member, *cfg); c != nil {
				ms.OK, ms.Note = c.healthy(), c.status()
			} else {
				ms.Pending, ms.Note = true, "проверяется"
			}
			break
		}
		age := e.backend.IfaceHandshake(member)
		ms.handshake = true
		switch {
		case age >= 0:
			ms.hsAt = e.now().Add(-time.Duration(age) * time.Second)
			ms.OK = age < int(handshakeFresh/time.Second)
			ms.Note = fmt.Sprintf("handshake %dс назад", age)
		case sl.Connected:
			ms.OK, ms.Note = true, "подключён, время рукопожатия неизвестно"
		default:
			ms.Note = "рукопожатия нет"
		}
	}
	return ms
}

// bundleSlots - слоты mawg и чужие туннели (tun/wg постороннего софта):
// без merge такой член читался бы «не найден в слотах» = всегда мёртв
// (кейс владельца 12.11: tun9 wdttc основной, proton запасной).
func (e *Engine) bundleSlots() []platform.SlotInfo {
	slots, _ := e.backend.Slots()
	seen := make(map[string]bool, len(slots))
	for _, sl := range slots {
		seen[sl.Device] = true
	}
	tunnels, _ := e.backend.SysTunnels()
	for _, sl := range tunnels {
		if !seen[sl.Device] {
			slots = append(slots, sl)
		}
	}
	return slots
}

// BundleMemberStates - здоровье участников для панели: пробы внешних
// туннелей берутся из кэша, панель новых не ждёт.
func (e *Engine) BundleMemberStates(members []string) map[string]MemberState {
	slots := e.bundleSlots()
	out := make(map[string]MemberState, len(members))
	for _, m := range members {
		out[m] = e.memberState(m, slots, e.cachedProbe)
	}
	return out
}

// checkBundles - проход по наборам. Счётчики гистерезиса ходят только в
// периодическом тике (раз в минуту): ручной запуск после правки набора не
// должен засчитываться туннелю как лишняя проверка.
func (e *Engine) checkBundles(tick bool) {
	e.monitorMagitrickle()
	bundles := e.store.Bundles()
	if len(bundles) == 0 {
		return
	}
	e.actMu.Lock()
	defer e.actMu.Unlock()
	slots, rules := e.bundleSlots(), e.healthRules()
	live := map[string]bool{}
	for _, b := range bundles {
		if len(b.Members) == 0 || len(b.Groups) == 0 {
			continue
		}
		if sel := e.pickMember(b, slots, rules, tick, live); sel != "" {
			e.switchBundle(b, sel)
		}
	}
	for key := range e.bundleTrust {
		if !live[key] {
			delete(e.bundleTrust, key)
		}
	}
}

// pickMember - первый доверенный по порядку; иначе текущий, пока не набрал
// порог отказов; иначе первый живой - раз текущий мёртв, лучше хоть
// какой-то туннель. Пусто - живых нет, группы не трогаем.
func (e *Engine) pickMember(b store.Bundle, slots []platform.SlotInfo, rules healthRules, tick bool, live map[string]bool) string {
	cur := e.store.BundleState(b.Name).Member
	states := make([]MemberState, len(b.Members))
	trust := make([]*bundleTrust, len(b.Members))
	for i, m := range b.Members {
		key := b.Name + "/" + m
		live[key] = true
		trust[i] = e.bundleTrust[key]
		if trust[i] == nil {
			trust[i] = &bundleTrust{}
			e.bundleTrust[key] = trust[i]
		}
		states[i] = e.memberState(m, slots, e.deviceProbe)
		if tick {
			trust[i].observe(states[i], rules)
		}
	}
	held, first := false, ""
	for i := range b.Members {
		ms, tr := states[i], trust[i]
		if ms.OK && tr.fails < rules.failCycles {
			return ms.Device
		}
		if ms.Device == cur {
			held = !ms.down && (ms.OK || tr.fails < rules.failCycles)
			if held {
				break
			}
			continue
		}
		if ms.OK && first == "" {
			first = ms.Device
		}
	}
	if held {
		return cur
	}
	return first
}

func (e *Engine) CheckBundlesNow() {
	e.checkBundles(false)
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
