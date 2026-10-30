package cascade

import (
	"context"
	"net"
	"strconv"
	"strings"
	"time"

	"mawg/internal/magitrickle"
)

// canonExclude - каноничный адрес правила ("1.2.3.4" -> "1.2.3.4/32");
// пусто для не-адреса. Синк не доверяет хранению: config.json могли править
// руками, а сравнение правил в цепочке идёт по точной форме адреса.
func canonExclude(e string) string {
	e = strings.TrimSpace(e)
	if e == "" {
		return ""
	}
	if _, ipnet, err := net.ParseCIDR(e); err == nil {
		if strings.Contains(ipnet.String(), ":") {
			return "" // IPv6: маркировка форка - IPv4
		}
		return ipnet.String()
	}
	if ip := net.ParseIP(e); ip != nil {
		if ip.To4() == nil {
			return ""
		}
		return ip.String() + "/32"
	}
	return ""
}

// Исключения из обработки (94-док, слово владельца 2026-10-29): в каждую
// цепочку MT_<id> вставляются RETURN-правила для исключённых адресов (оба
// направления, -s и -d) - такой трафик не маркируется НИ ОДНОЙ группой и
// ходит напрямую. Механизм - обобщение каскадных RETURN-исключений
// (syncHookExclusions): форк перетирает цепочки при каждой своей записи,
// поэтому исключения восстанавливаются каждым циклом SyncHooks, а
// AfterMutate (endMutate -> SyncHooks) закрывает гэп сразу после записи.
// Правила помечены комментарием mawg-excl: чужие RETURN (форк, каскады)
// синк не трогает, при сокращении списка лишние удаляются.

const exclComment = "mawg-excl"

// excludesPresent - есть ли что исключать (глобальный список или пулы).
// Функционал выключен флагом (95-док, слово владельца 30.10) - false:
// SyncHooks (hasExcl) и SyncExclusions спят целиком, никакой активности
// фичи; вставленные ранее правила mawg-excl вымирают на ближайшей
// пересборке форка, списки в сторах остаются.
func (m *Manager) excludesPresent() bool {
	if m.Store.Settings().ExcludesDisabled {
		return false
	}
	if len(m.Store.Settings().Excludes) > 0 {
		return true
	}
	for _, p := range m.Store.Pools() {
		if len(p.Settings.Excludes) > 0 {
			return true
		}
	}
	return false
}

// poolExcludesByDevice - исключения пулов по device: действуют на все
// группы пула (основная и dns-группы пути с тем же Interface).
func (m *Manager) poolExcludesByDevice() map[string][]string {
	out := map[string][]string{}
	for _, p := range m.Store.Pools() {
		if len(p.Settings.Excludes) == 0 {
			continue
		}
		dev := p.DeviceName()
		for _, e := range p.Settings.Excludes {
			if !containsString(out[dev], e) {
				out[dev] = append(out[dev], e)
			}
		}
	}
	return out
}

// SyncExclusions - немедленное применение исключений (PUT-хендлеры
// исключений). В окне мутации молчит: правила восстановит SyncHooks по
// завершении записи.
func (m *Manager) SyncExclusions() {
	m.mu.Lock()
	inWindow := m.mutating > 0
	active := m.exclActive
	m.mu.Unlock()
	if inWindow || (!m.excludesPresent() && !active) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	groups, err := m.MT.GroupsWithRules(ctx)
	if err != nil {
		return
	}
	m.syncExclusions(groups)
}

// syncExclusions - выровнять исключения по всем группам; пуловые списки
// применяются к группам с тем же Interface (объединение с глобальными).
func (m *Manager) syncExclusions(groups []magitrickle.Group) {
	global := canonExcludeList(m.Store.Settings().Excludes)
	byDevice := map[string][]string{}
	for dev, extra := range m.poolExcludesByDevice() {
		byDevice[dev] = canonExcludeList(extra)
	}
	changed := 0
	for _, g := range groups {
		list := global
		if extra := byDevice[g.Interface]; len(extra) > 0 {
			list = append(append([]string(nil), global...), extra...)
		}
		changed += m.syncChainExclusions(m.chainName(g.ID), list)
	}
	// active держит синк живым, пока правила могли остаться в цепочках:
	// без него сокращение списка до пустого не вычистило бы старые
	// RETURN-правила (ранний выход «нечего исключать»)
	m.mu.Lock()
	m.exclActive = len(global) > 0 || len(byDevice) > 0 || changed > 0
	m.mu.Unlock()
	if changed > 0 {
		m.log("rules", "excludes", "исключения применены: обновлено правил "+strconv.Itoa(changed))
	}
}

// canonExcludeList - канонизация списка, дубликаты схлопываются.
func canonExcludeList(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, e := range in {
		c := canonExclude(e)
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out
}

// syncChainExclusions - выровнять исключения одной цепочки, возврат -
// число изменений. Нет цепочки (группа без правил) - тихий skip.
func (m *Manager) syncChainExclusions(chain string, entries []string) int {
	out, err := m.ipt("-t", "mangle", "-S", chain)
	if err != nil {
		return 0
	}
	// помеченные правила цепочки: key "<s|d>:<адрес>" -> спека для -D
	have := map[string]string{}
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimSpace(ln)
		if !strings.HasPrefix(ln, "-A "+chain+" ") || !strings.Contains(ln, "--comment "+exclComment) {
			continue
		}
		key, ok := exclKey(ln)
		if !ok {
			continue
		}
		have[key] = strings.TrimSpace(strings.TrimPrefix(ln, "-A "+chain+" "))
	}
	want := map[string]bool{}
	for _, e := range entries {
		want["s:"+e] = true
		want["d:"+e] = true
	}
	changed := 0
	for key, spec := range have {
		if want[key] {
			continue
		}
		args := append([]string{"-t", "mangle", "-D", chain}, strings.Fields(spec)...)
		if _, err := m.ipt(args...); err == nil {
			changed++
		}
	}
	for _, e := range entries {
		if strings.Contains(e, ":") {
			continue // IPv6: маркировка форка - IPv4
		}
		for _, dir := range []struct{ flag, key string }{{"-s", "s"}, {"-d", "d"}} {
			if _, ok := have[dir.key+":"+e]; ok {
				continue
			}
			if _, err := m.ipt("-t", "mangle", "-I", chain, "1", dir.flag, e, "-j", "RETURN", "-m", "comment", "--comment", exclComment); err == nil {
				changed++
			}
		}
	}
	return changed
}

// exclKey - направление и адрес помеченного правила ("s:1.2.3.4/32").
func exclKey(line string) (string, bool) {
	f := strings.Fields(line)
	for i, w := range f {
		var dir string
		switch w {
		case "-s":
			dir = "s"
		case "-d":
			dir = "d"
		default:
			continue
		}
		if i+1 < len(f) {
			return dir + ":" + f[i+1], true
		}
	}
	return "", false
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
