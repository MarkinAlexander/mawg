package web

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"mawg/internal/magitrickle"
	"mawg/internal/store"
)

// Реестр «пул ↔ устройство через пул» (97-док, слово владельца 30.10:
// «не видно, что с чем связано... надо хранить и ловить коллизии»).
//
// Связка устройство->пул существует только как MT-группа (sources +
// статика 0.0.0.0/0); реестр хранит её зеркало в Settings.DeviceBindings:
// карточка/настройки пула показывают устройства, гейты ловят коллизии
// адресов между пулами и глобальными исключениями. Истина о составе - в
// MT: хендлеры пишут реестр сразу, сверка по снапшоту (reconcile) -
// страховка от тумблера, внешних правок и отживших записей.

// canonBindAddr - каноническая форма адреса связки: CIDR как есть,
// хост -> /32, IPv6/мусор -> "" (маркировка v4-only, 94-док).
func canonBindAddr(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if ip := net.ParseIP(s); ip != nil {
		if v4 := ip.To4(); v4 == nil {
			return ""
		}
		return ip.String() + "/32"
	}
	if _, ipnet, err := net.ParseCIDR(s); err == nil {
		if ipnet.IP.To4() == nil {
			return ""
		}
		return ipnet.String()
	}
	return ""
}

func canonicalSources(sources []string) []string {
	out := make([]string, 0, len(sources))
	for _, src := range sources {
		if c := canonBindAddr(src); c != "" {
			out = append(out, c)
		}
	}
	return out
}

// cidrsOverlap - пересекаются ли две канонические сети (для сетей
// разного размера пересечение = вхождение меньшей в большую).
func cidrsOverlap(a, b string) bool {
	_, anet, err1 := net.ParseCIDR(a)
	_, bnet, err2 := net.ParseCIDR(b)
	if err1 != nil || err2 != nil {
		return a == b
	}
	return anet.Contains(bnet.IP) || bnet.Contains(anet.IP)
}

// findBindingConflicts - по каким источникам конфликт: адрес уже связан
// с другим пулом (или накрыт его подсетью) либо сидит в глобальных
// исключениях (там трафик не маркируется - связка была бы бессмысленна).
// Записи самого пула не конфликт. Решение о применении - за владельцем:
// 409 + подтверждение, не запрет (паттерн петлезащиты DnsView).
func findBindingConflicts(bindings []store.DeviceBinding, excludes []string, pool string, sources []string) []string {
	var out []string
	for _, src := range sources {
		c := canonBindAddr(src)
		if c == "" {
			continue
		}
		for _, b := range bindings {
			if b.Pool == pool {
				continue
			}
			for _, ip := range b.IPs {
				if cidrsOverlap(c, ip) {
					out = append(out, fmt.Sprintf("%s - уже привязан к пулу %s (группа %q)", src, b.Pool, b.Name))
					break
				}
			}
		}
		for _, ex := range excludes {
			ce := canonBindAddr(ex)
			if ce == "" {
				continue
			}
			if cidrsOverlap(c, ce) {
				out = append(out, fmt.Sprintf("%s - в глобальных исключениях из обработки (там трафик не маркируется)", src))
				break
			}
		}
	}
	return out
}

// deviceBindingUpsert - зеркалит MT-группу устройства в реестре (по
// имени группы: при правке sources запись обновляется, при создании -
// добавляется).
func (s *Server) deviceBindingUpsert(pool, name string, sources []string) {
	ips := canonicalSources(sources)
	if name == "" || pool == "" || len(ips) == 0 {
		return
	}
	bindings := s.store.DeviceBindings()
	for i := range bindings {
		if bindings[i].Name != name {
			continue
		}
		if bindings[i].Pool == pool && sameStrings(bindings[i].IPs, ips) {
			return
		}
		bindings[i].Pool, bindings[i].IPs = pool, ips
		_ = s.store.SetDeviceBindings(bindings)
		return
	}
	bindings = append(bindings, store.DeviceBinding{Pool: pool, IPs: ips, Name: name, Created: time.Now()})
	_ = s.store.SetDeviceBindings(bindings)
}

func (s *Server) deviceBindingRemove(name string) {
	bindings := s.store.DeviceBindings()
	for i := range bindings {
		if bindings[i].Name != name {
			continue
		}
		bindings = append(bindings[:i], bindings[i+1:]...)
		_ = s.store.SetDeviceBindings(bindings)
		return
	}
}

// deviceBindingRemoveByID - снять запись по id MT-группы (generic
// удаление в списке групп): реестр хранит имена, id разрешается по кэшу
// снапшота; мимо кэша запись снимет сверка по следующему снапшоту.
func (s *Server) deviceBindingRemoveByID(id string) {
	s.mtCacheMu.Lock()
	var name string
	if s.mtCache != nil {
		for _, g := range s.mtCache.groups {
			if g.ID == id {
				name = g.Name
				break
			}
		}
	}
	s.mtCacheMu.Unlock()
	if name != "" {
		s.deviceBindingRemove(name)
	}
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// reconcileDeviceBindings - сверка реестра со снапшотом MT (97-док:
// «одноразовая миграция + сверка»): связка = ВКЛЮЧЕННАЯ device-группа
// на интерфейсе известного пула. Вызывается из mtGetGroups при
// изменившемся снапшоте; тем самым покрываются ручной тумблер группы,
// «оставить напрямую» при удалении пула и внешние правки. Save - только
// при изменениях (не дёргать config.json на каждый поллинг).
func (s *Server) reconcileDeviceBindings(groups []magitrickle.Group) {
	bindings := s.store.DeviceBindings()
	if len(groups) == 0 && len(bindings) == 0 {
		return
	}
	ifacePool := map[string]string{}
	for _, p := range s.store.Pools() {
		ifacePool[p.DeviceName()] = p.Name
	}
	byName := map[string]magitrickle.Group{}
	for _, g := range groups {
		byName[g.Name] = g
	}
	kept := make([]store.DeviceBinding, 0, len(bindings))
	changed := false
	for _, b := range bindings {
		g, ok := byName[b.Name]
		if ok && g.Enable && deviceGroupOf(g) && ifacePool[g.Interface] == b.Pool {
			kept = append(kept, b)
			continue
		}
		changed = true
	}
	have := make(map[string]bool, len(kept))
	for _, b := range kept {
		have[b.Name] = true
	}
	for _, g := range groups {
		if !g.Enable || !deviceGroupOf(g) {
			continue
		}
		pool, ok := ifacePool[g.Interface]
		if !ok || have[g.Name] {
			continue
		}
		ips := canonicalSources(g.Sources)
		if len(ips) == 0 {
			continue
		}
		kept = append(kept, store.DeviceBinding{Pool: pool, IPs: ips, Name: g.Name, Created: time.Now()})
		changed = true
	}
	if changed {
		_ = s.store.SetDeviceBindings(kept)
	}
}

// poolNameByDevice - имя пула по устройству его интерфейса (для привязки
// MT-группы к пулу: Interface группы = pool.DeviceName()).
func (s *Server) poolNameByDevice(iface string) string {
	for _, p := range s.store.Pools() {
		if p.DeviceName() == iface {
			return p.Name
		}
	}
	return ""
}

// mtDeleteDeviceGroup - DELETE /api/v1/mt/device-group/{name}: снять
// привязку устройства (97-док) - удалить MT-группу и запись реестра.
func (s *Server) mtDeleteDeviceGroup(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		http.NotFound(w, r)
		return
	}
	bindings := s.store.DeviceBindings()
	knownBinding := false
	for _, b := range bindings {
		if b.Name == name {
			knownBinding = true
			break
		}
	}
	client := s.mtClient()
	groups, err := client.GroupsWithRules(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	var target *magitrickle.Group
	for i := range groups {
		if groups[i].Name == name {
			target = &groups[i]
			break
		}
	}
	if target == nil {
		// группы в MT нет - запись реестра (если отжила) чистим и
		// отвечаем 404 как и раньше
		s.deviceBindingRemove(name)
		http.NotFound(w, r)
		return
	}
	if !knownBinding && !deviceGroupOf(*target) {
		writeErr(w, fmt.Errorf("группа %q не является группой устройства - снимайте её в списке групп", name))
		return
	}
	if err := s.engine.RetryMTWrite("panel", func() error {
		return client.DeleteGroup(r.Context(), target.ID)
	}); err != nil {
		writeErr(w, err)
		return
	}
	client.RefreshShadow(r.Context())
	s.deviceBindingRemove(name)
	s.store.LogEvent("rules", "device-bindings", fmt.Sprintf(
		"снята привязка устройства: группа %q удалена (%d адрес(ов) больше не идут через пул)", name, len(target.Sources)))
	writeJSON(w, http.StatusOK, map[string]string{"ok": "deleted"})
}

// deletePoolDeviceGroups - судьба MT-групп устройств удаляемого пула
// (слово владельца 30.10: «удаление пула должно удалять группу, а если
// пользователь не удаляет - перевести на директ»). Суть mawg - не давать
// лишиться интернета: выключенная группа не маркирует, устройства идут
// напрямую, а не в мёртвый интерфейс. mode: "delete" | "direct" (пусто =
// direct). Ошибки отдельных групп собираются в одну (запись таких
// связок остаётся в реестре - правда о MT).
func (s *Server) deletePoolDeviceGroups(ctx context.Context, pool, mode string) error {
	if mode == "" {
		mode = "direct"
	}
	var names []string
	for _, b := range s.store.DeviceBindings() {
		if b.Pool == pool {
			names = append(names, b.Name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	client := s.mtClient()
	groups, err := client.GroupsWithRules(ctx)
	if err != nil {
		return fmt.Errorf("группы устройств %s не разобраны (MT недоступен): %w", strings.Join(names, ", "), err)
	}
	byName := map[string]magitrickle.Group{}
	for _, g := range groups {
		byName[g.Name] = g
	}
	var failed []string
	if mode == "delete" {
		for _, name := range names {
			g, ok := byName[name]
			if !ok {
				s.deviceBindingRemove(name)
				continue
			}
			if err := s.engine.RetryMTWrite("panel", func() error {
				return client.DeleteGroup(ctx, g.ID)
			}); err != nil {
				failed = append(failed, fmt.Sprintf("%s: %v", name, err))
				continue
			}
			s.deviceBindingRemove(name)
		}
	} else {
		ids := map[string]bool{}
		for _, name := range names {
			if g, ok := byName[name]; ok && g.Enable {
				ids[g.ID] = true
			}
		}
		if len(ids) > 0 {
			err = s.mtApplyGroups(ctx, func(gs []magitrickle.Group) bool {
				found := false
				for i := range gs {
					if ids[gs[i].ID] && gs[i].Enable {
						gs[i].Enable = false
						found = true
					}
				}
				return found
			})
			if err != nil {
				failed = append(failed, fmt.Sprintf("не выключить: %v", err))
			}
		}
		// связки нет: группы остались выключенными, устройства идут напрямую
		for _, name := range names {
			s.deviceBindingRemove(name)
		}
	}
	client.RefreshShadow(ctx)
	if len(failed) > 0 {
		return fmt.Errorf("не всё получилось с группами устройств: %s", strings.Join(failed, "; "))
	}
	if mode == "delete" {
		s.store.LogEvent("rules", "device-bindings", fmt.Sprintf(
			"пул %s удалён вместе с группами устройств (%d): связки сняты", pool, len(names)))
	} else {
		s.store.LogEvent("rules", "device-bindings", fmt.Sprintf(
			"пул %s удалён: группы устройств оставлены выключенными - устройства идут напрямую", pool))
	}
	return nil
}

// getDeviceBindings - GET /api/v1/device-bindings: весь реестр (UI
// догружает лениво, как excludes).
func (s *Server) getDeviceBindings(w http.ResponseWriter, r *http.Request) {
	bindings := s.store.DeviceBindings()
	if bindings == nil {
		bindings = []store.DeviceBinding{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"bindings": bindings})
}

// bindingConflicts409 - ответ 409 со списком конфликтов (без confirm).
func bindingConflicts409(w http.ResponseWriter, conflicts []string) {
	writeJSON(w, http.StatusConflict, map[string]any{
		"error":     "адреса уже заняты: " + strings.Join(conflicts, "; ") + ". Применить всё равно?",
		"conflicts": conflicts,
	})
}
