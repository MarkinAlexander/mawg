package cascade

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"mawg/internal/magitrickle"
	"mawg/internal/store"
)

func (m *Manager) chainName(groupID string) string {
	return "MT_" + groupID
}

func (m *Manager) ipt(args ...string) (string, error) {
	if m.iptRun != nil {
		return m.iptRun(args...)
	}
	cmd := exec.Command("iptables", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// SetIptRun - подмена iptables-раннера (тесты; живой прогон зовёт
// бинарь iptables).
func (m *Manager) SetIptRun(fn func(args ...string) (string, error)) { m.iptRun = fn }

func (m *Manager) HookPresent(groupID string) bool {
	out, err := m.ipt("-t", "mangle", "-S", "OUTPUT")
	if err != nil {
		return false
	}
	return strings.Contains(out, "-j "+m.chainName(groupID)+"\n") || strings.HasSuffix(strings.TrimSpace(out), "-j "+m.chainName(groupID))
}

func (m *Manager) ChainExists(groupID string) bool {
	_, err := m.ipt("-t", "mangle", "-L", m.chainName(groupID), "-n")
	return err == nil
}

func (m *Manager) HookAdd(groupID string) error {
	if m.HookPresent(groupID) {
		return nil
	}
	if !m.ChainExists(groupID) {
		return fmt.Errorf("цепочка %s не существует, группа выключена?", m.chainName(groupID))
	}
	if _, err := m.ipt("-t", "mangle", "-A", "OUTPUT", "-j", m.chainName(groupID)); err != nil {
		return err
	}
	m.log("cascade", "hook", "OUTPUT-хук поставлен: "+m.chainName(groupID))
	return nil
}

func (m *Manager) HookRemove(groupID string) error {
	// RETURN-исключения эндпоинтов жили внутри цепочки группы; цепочка
	// могла уже быть снесена MT - ошибки игнорируем, снимаем только у
	// живой цепочки и известного каскада
	if entry, ok := m.Store.CascadeByGroup(groupID); ok && m.ChainExists(groupID) {
		chain := m.chainName(groupID)
		for _, ip := range m.viaEndpointIPs(entry.Via) {
			m.ipt("-t", "mangle", "-D", chain, "-d", ip, "-j", "RETURN")
		}
	}
	if !m.HookPresent(groupID) {
		return nil
	}
	_, err := m.ipt("-t", "mangle", "-D", "OUTPUT", "-j", m.chainName(groupID))
	if err == nil {
		m.log("cascade", "hook", "OUTPUT-хук снят: "+m.chainName(groupID))
	}
	return err
}

// beginMutate/endMutate - окно мутации. Внутри него SyncHooks (30-секундный
// тик, ApplyGroupSources) молчит: если тик вернёт хук между снятием и
// записью, restore MT снова упрётся в CHAIN_DEL busy (кейс awgfree
// 2026-10-12: параллельные policy/repair-записи и тик сражались за хуки).
func (m *Manager) beginMutate() {
	m.mu.Lock()
	m.mutating++
	m.mu.Unlock()
}

func (m *Manager) endMutate() {
	m.mu.Lock()
	if m.mutating > 0 {
		m.mutating--
	}
	m.mu.Unlock()
	m.SyncHooks()
}

// outputMTJumps - какие MT_-цепочки сейчас прыгаются из mangle OUTPUT.
func (m *Manager) outputMTJumps() []string {
	out, err := m.ipt("-t", "mangle", "-S", "OUTPUT")
	if err != nil {
		return nil
	}
	var refs []string
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimSpace(ln)
		if !strings.HasPrefix(ln, "-A OUTPUT") {
			continue
		}
		for _, f := range strings.Fields(ln) {
			if strings.HasPrefix(f, "MT_") {
				refs = append(refs, f)
			}
		}
	}
	return refs
}

// removeHookVerified снимает хук и читает состояние обратно, пока хук
// реально не уйдёт (грубая верификация: ошибка -D не значит «снят» и не
// значит «не снят» - факт узнаём свежим чтением OUTPUT).
func (m *Manager) removeHookVerified(id string) {
	for attempt := 0; attempt < 3 && m.HookPresent(id); attempt++ {
		if err := m.HookRemove(id); err != nil {
			m.log("cascade", "hook", "не снял хук перед записью: "+err.Error())
		}
	}
}

// sweepOutputHooks - предзаписная проверка (слово владельца 2026-10-12:
// «все проверки перед тем как править что-то связанное с каскадом»).
// Форк при bulk-PUT пересобирает цепочки не только удаляемых групп, и
// ЛЮБОЙ висящий прыжок из OUTPUT роняет весь restore-батч: CHAIN_DEL busy
// -> 500 -> рестарт MT -> рантайм пустеет (кейс awgfree 2026-10-12).
// Снимаем все свои хуки (каскадные и роутерные) и протухшие MT_-прыжки,
// потом читаем OUTPUT до нуля. Возврат - что снять не удалось (запись
// всё равно пойдёт: RetryMTWrite при сбое перезапустит MT, чей старт
// сносит чужие -j MT_; но теперь это только для реально упрямых).
func (m *Manager) sweepOutputHooks() []string {
	for _, c := range m.Store.Cascades() {
		m.removeHookVerified(c.Group)
	}
	for id := range m.routerHookPolicies() {
		m.removeHookVerified(id)
	}
	var stuck []string
	for attempt := 0; attempt < 3; attempt++ {
		refs := m.outputMTJumps()
		if len(refs) == 0 {
			return nil
		}
		stuck = refs
		for _, chain := range refs {
			if _, err := m.ipt("-t", "mangle", "-D", "OUTPUT", "-j", chain); err == nil {
				m.log("cascade", "hook", "OUTPUT-хук снят: "+chain)
			}
		}
	}
	return stuck
}

func (m *Manager) reportStuck(stuck []string) {
	if len(stuck) > 0 {
		m.log("cascade", "hook", "ПЕРЕД ЗАПИСЬЮ не снялись хуки: "+strings.Join(stuck, ", ")+" - запись может упасть, самолечение перезапустит MT")
	}
}

// BeforeMutate снимает ВСЕ наши OUTPUT-хуки перед записью и верифицирует
// снятие чтением. Предугадывать, какие цепочки форк пересоберёт при
// bulk-PUT (пресет, правка правил, repair, смена политики), нельзя - кейс
// 2026-10-12: снимали только «обречённые», а форк пересобрал соседние с
// живыми хуками. Короткое окно без хуков (каскад/роутер на секунды идут
// мимо группы) дешевле 500-шторма с рестартом MT; AfterMutate возвращает
// хуки по SyncHooks.
func (m *Manager) BeforeMutate(_ func([]magitrickle.Group) bool) {
	m.beginMutate()
	if m.mtcBackend() {
		return
	}
	m.reportStuck(m.sweepOutputHooks())
}

// AfterMutate закрывает окно мутации и возвращает хуки по SyncHooks.
func (m *Manager) AfterMutate() {
	m.endMutate()
}

// BeforeReplace - BeforeMutate для записи, заменяющей ВЕСЬ список групп
// (восстановление из бэкапа, импорт «заменить всё»): там пересобираются
// вообще все цепочки, так что снимать нужно все хуки без исключений.
func (m *Manager) BeforeReplace(_ []magitrickle.Group) {
	m.beginMutate()
	if m.mtcBackend() {
		return
	}
	m.reportStuck(m.sweepOutputHooks())
}

func (m *Manager) SyncHooks() {
	m.mu.Lock()
	inWindow := m.mutating > 0
	m.mu.Unlock()
	if inWindow {
		// окно мутации: хуки поставит AfterMutate по завершении записи,
		// иначе тик вернёт хук между снятием и записью - CHAIN_DEL busy
		return
	}
	if m.mtcBackend() {
		// бэкенд mt-c: ни прыжков, ни исключений в его цепочки - весь
		// роутер-путь он держит сам (SetBackendFlavor)
		return
	}
	casc := m.Store.Cascades()
	routerPolicies := m.routerHookPolicies()
	// исключения из обработки синхронятся независимо от каскадов
	// (роутер без единого каскада тоже имеет право на исключения)
	hasExcl := m.excludesPresent()
	m.mu.Lock()
	exclActive := m.exclActive
	m.mu.Unlock()
	if len(casc) == 0 && len(routerPolicies) == 0 && !hasExcl && !exclActive {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	groups, err := m.MT.GroupsWithRules(ctx)
	if err != nil {
		return
	}
	enabled := map[string]bool{}
	byID := map[string]magitrickle.Group{}
	for _, g := range groups {
		enabled[g.ID] = g.Enable
		byID[g.ID] = g
	}
	// обёртка юзера (гайд: подмена PATH в procd-ините MT) сама выводит
	// ВСЕ группы в OUTPUT через MR_*-цепочки - второй прыжок дублировал
	// бы обработку, свои хуки в её присутствии не ставим
	wrapper := m.outputWrapperPresent()
	m.noteWrapper(wrapper)
	for _, c := range casc {
		if !enabled[c.Group] {
			if err := m.HookRemove(c.Group); err != nil {
				m.log("cascade", "hook", "не снял хук: "+err.Error())
			}
			continue
		}
		if wrapper {
			continue
		}
		if err := m.HookAdd(c.Group); err != nil {
			m.log("cascade", "hook", "не поставил хук: "+err.Error())
			continue
		}
		m.syncHookExclusions(c)
	}
	// группы с роутером в источниках: тот же хук, что у каскадов, плюс
	// дрейф эффективных источников - роутер сменил адрес (ppp-переподключение),
	// список надо пересобрать, иначе роутер перестаёт проходить guard
	for id, p := range routerPolicies {
		if !enabled[id] {
			if err := m.HookRemove(id); err != nil {
				m.log("policy", "hook", "не снял роутерный хук: "+err.Error())
			}
			continue
		}
		if wrapper {
			continue
		}
		if err := m.HookAdd(id); err != nil {
			m.log("policy", "hook", "не поставил роутерный хук: "+err.Error())
			continue
		}
		want := p.EffectiveSources(RouterAddrs())
		if g, ok := byID[id]; ok && !equalStrings(g.Sources, want) {
			if err := m.syncGroupSources(id, want); err != nil {
				m.log("policy", "sources", "не пересобрал источники "+id+": "+err.Error())
			}
		}
	}
	if hasExcl {
		m.syncExclusions(groups)
	}
}

func (m *Manager) routerHookPolicies() map[string]store.GroupPolicy {
	out := map[string]store.GroupPolicy{}
	for id, p := range m.Store.GroupPoliciesAll() {
		if p.RouterHook() {
			out[id] = p
		}
	}
	return out
}

// outputWrapperPresent ищет в mangle OUTPUT ссылки на чужие MR_*-цепочки.
func (m *Manager) outputWrapperPresent() bool {
	out, err := m.ipt("-t", "mangle", "-S", "OUTPUT")
	if err != nil {
		return false
	}
	return strings.Contains(out, "-j MR_") || strings.Contains(out, "-g MR_")
}

func (m *Manager) noteWrapper(v bool) {
	if m.wrapper == v {
		return
	}
	m.wrapper = v
	if v {
		m.log("cascade", "hook", "обнаружена обёртка юзера (MR_* в mangle OUTPUT): каскадные OUTPUT-хуки не ставлю - обёртка выводит группы в OUTPUT сама")
	} else {
		m.log("cascade", "hook", "обёртка юзера исчезла - возвращаю свои каскадные OUTPUT-хуки")
	}
}

// WrapperDetected - для статуса каскадов в панели.
func (m *Manager) WrapperDetected() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.wrapper
}

// SetBackendFlavor - flavor бэкенда MT (проталкивает rotator из пробы
// версии, 107-док). Бэкенд mt-c сам обслуживает роутер-путь через свои
// policy-цепочки и делает батч-пересборки iptables-restore: ЛЮБАЯ наша
// запись между его коммитами фаз роняет весь restore -> шторм -> голодание
// DNS-прокси :3553 (аварии 01.11). Поэтому при FlavorMtC каскад не пишет
// в чужие цепочки вовсе: хуки, свип и исключения молчат, честная строка -
// в журнал событий.
func (m *Manager) SetBackendFlavor(f magitrickle.MtFlavor) {
	m.mu.Lock()
	wasMtc := m.flavor == magitrickle.FlavorMtC
	m.flavor = f
	m.mu.Unlock()
	isMtc := f == magitrickle.FlavorMtC
	switch {
	case isMtc && !wasMtc:
		m.log("cascade", "mtc-backend", "OUTPUT-хуки отключены: бэкенд mt-c")
	case !isMtc && wasMtc:
		m.log("cascade", "mtc-backend", "бэкенд mt-c сменился ("+string(f)+") - каскадные OUTPUT-хуки возвращаются")
	}
}

// BackendFlavor - flavor бэкенда MT для панели.
func (m *Manager) BackendFlavor() magitrickle.MtFlavor {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.flavor
}

// mtcBackend - активен ли гейт «не писать в чужие цепочки».
func (m *Manager) mtcBackend() bool {
	return m.BackendFlavor() == magitrickle.FlavorMtC
}

// syncHookExclusions ставит RETURN-исключения для эндпоинтов туннеля, в
// который ведёт каскад: пакеты самого туннеля не должны маркироваться
// группой, ведущей в этот же туннель (антицикл). Правила живут внутри
// цепочки MT_<id> - точечно для этой группы, чужие группы не трогаем.
// MT перетирает цепочку при каждом refresh своих правил - SyncHooks
// восстанавливает исключения каждый цикл.
func (m *Manager) syncHookExclusions(entry store.CascadeEntry) {
	ips := m.viaEndpointIPs(entry.Via)
	if len(ips) == 0 {
		return
	}
	chain := m.chainName(entry.Group)
	for _, ip := range ips {
		if _, err := m.ipt("-t", "mangle", "-C", chain, "-d", ip, "-j", "RETURN"); err == nil {
			continue
		}
		if _, err := m.ipt("-t", "mangle", "-I", chain, "1", "-d", ip, "-j", "RETURN"); err == nil {
			m.log("cascade", "hook", "RETURN-исключение эндпоинта "+ip+" в "+chain)
		}
	}
}
