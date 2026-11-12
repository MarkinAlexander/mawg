package web

// Хендлеры/логика групп MagiTrickle (из server.go).

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"mawg/internal/magitrickle"
	"mawg/internal/store"
)

func (s *Server) mtClient() *magitrickle.Client {
	if s.mt != nil {
		return s.mt
	}
	return magitrickle.New("http://localhost:8080")
}

type mtSnapshot struct {
	at      time.Time
	rawHash [sha256.Size]byte
	groups  []magitrickle.Group
	titles  map[string]string
	err     error
	// bindReconciled - хеш снапшота, по которому реестр устройств уже
	// сверён (97-док): сверка не должна дёргать config.json на каждый
	// поллинг без изменений.
	bindReconciled [sha256.Size]byte
	bindDone       bool
}

// mtSnapshotCached - свежие группы + заголовки слотов. Сырой JSON групп
// получаем на каждый вызов (дёшево), декод - только если хеш изменился.
// titles отдаётся копией: обработчики мутируют карту, шарить нельзя.
func (s *Server) mtSnapshotCached() (groups []magitrickle.Group, titles map[string]string, err error) {
	s.mtCacheMu.Lock()
	defer s.mtCacheMu.Unlock()
	raw, rerr := s.mtClient().GroupsRaw(context.Background())
	if s.mtCache == nil {
		s.mtCache = &mtSnapshot{}
	}
	if rerr == nil {
		sum := sha256.Sum256(raw)
		if s.mtCache.err != nil || s.mtCache.rawHash != sum {
			if decoded, derr := magitrickle.DecodeGroups(raw); derr == nil {
				s.mtCache.rawHash, s.mtCache.groups, s.mtCache.err = sum, decoded, nil
			} else {
				s.mtCache.err = derr
			}
		}
	} else {
		s.mtCache.err = rerr
	}
	s.mtCache.at = time.Now()
	s.mtCache.titles = s.mtInterfaceTitles()
	return s.mtCache.groups, copyTitles(s.mtCache.titles), s.mtCache.err
}

func copyTitles(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func (s *Server) mtInterfaceTitles() map[string]string {
	titles := map[string]string{}
	for _, p := range s.store.Pools() {
		titles[p.DeviceName()] = p.Name
	}
	if slots, err := s.backend.Slots(); err == nil {
		for _, sl := range slots {
			if sl.Description == "" || sl.Device == "" {
				continue
			}
			if _, exists := titles[sl.Device]; exists {
				continue
			}
			if s.store.IfaceModes()[sl.Device] == store.IfaceExternal {
				titles[sl.Device] = sl.Description
			}
		}
	}
	return titles
}

// mtDuplicates: дубликаты правил по ключу тип:паттерн, как их считает
// сам magitrickle (совпадение в разных группах или два включенных в одной).
func (s *Server) mtDuplicates(w http.ResponseWriter, r *http.Request) {
	groups, err := s.mtClient().GroupsWithRules(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	type ref struct {
		GroupID   string `json:"groupId"`
		GroupName string `json:"groupName"`
		RuleID    string `json:"ruleId"`
		RuleType  string `json:"ruleType"`
	}
	// ключ - только паттерн: domain и namespace на один домен для владельца -
	// тот же дубликат. Выключенные правила/группы не участвуют (в
	// маршрутизации их нет). Служебные группы (dnszone-*, включая
	// dnszone-included-* форка, и каскадные «Служебная: ») тоже: ими
	// управляет mawg, а адрес DNS-сервера зоны обязан жить и в группе
	// пути, и в группе форка (живая жалоба: треугольник висел на обеих).
	byKey := map[string][]ref{}
	for _, g := range groups {
		if !g.Enable || isServiceGroup(g.Name) {
			continue
		}
		for _, rl := range g.Rules {
			if !rl.Enable || strings.TrimSpace(rl.Rule) == "" {
				continue
			}
			key := strings.ToLower(strings.TrimSpace(rl.Rule))
			byKey[key] = append(byKey[key], ref{GroupID: g.ID, GroupName: g.Name, RuleID: rl.ID, RuleType: rl.Type})
		}
	}
	out := map[string][]ref{}
	for key, refs := range byKey {
		if len(refs) < 2 {
			continue
		}
		groupsOf := map[string]bool{}
		for _, ref := range refs {
			groupsOf[ref.GroupID] = true
		}
		if len(groupsOf) < 2 {
			continue
		}
		out[key] = refs
	}
	writeJSON(w, http.StatusOK, map[string]any{"duplicates": out})
}

func (s *Server) mtGetGroups(w http.ResponseWriter, r *http.Request) {
	// активный черновик (114-док) отдаётся вместо снапшота: панель видит
	// правки до применения; MT не дёргаем - правки живут локально
	if groups, active := s.draftRead(); active {
		s.mtGroupsView(w, r, groups, s.mtInterfaceTitles())
		return
	}
	groups, titles, err := s.mtSnapshotCached()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "error": err.Error(),
			"autoSave": s.autoSaveOn(), "draft": s.draftStatus()})
		return
	}
	// сверка реестра устройств со снапшотом (97-док) - только по
	// изменившемуся снапшоту, чтобы не дёргать config.json каждым поллингом
	s.mtCacheMu.Lock()
	if s.mtCache != nil && (!s.mtCache.bindDone || s.mtCache.bindReconciled != s.mtCache.rawHash) {
		s.mtCache.bindReconciled, s.mtCache.bindDone = s.mtCache.rawHash, true
		s.mtCacheMu.Unlock()
		s.reconcileDeviceBindings(groups)
	} else {
		s.mtCacheMu.Unlock()
	}
	s.mtGroupsView(w, r, groups, titles)
}

// mtGroupsView - groupView-обёртка списка групп (снапшот или черновик,
// 114-док) в общем ответе mtGetGroups.
func (s *Server) mtGroupsView(w http.ResponseWriter, r *http.Request, groups []magitrickle.Group, titles map[string]string) {
	type groupView struct {
		magitrickle.Group
		InterfaceTitle string              `json:"interfaceTitle,omitempty"`
		Policy         *store.GroupPolicy  `json:"policy,omitempty"`
		Cascade        *store.CascadeEntry `json:"cascade,omitempty"`
		// DnsZone/DnsTunnel - служебные группы DNS (путь зоны / туннель
		// общего DNS-каскада): управляются вкладкой DNS, редактировать
		// руками бессмысленно - применение перезапишет.
		DnsZone   bool   `json:"dnsZone,omitempty"`
		DnsTunnel bool   `json:"dnsTunnel,omitempty"`
		Degraded  string `json:"degraded,omitempty"`
	}
	policies := map[string]store.GroupPolicy{}
	for id, p := range s.store.GroupPoliciesAll() {
		policies[id] = p
	}
	cascades := map[string]store.CascadeEntry{}
	for _, c := range s.store.Cascades() {
		cascades[c.Group] = c
	}
	degraded := s.store.DegradedGroups()
	out := make([]groupView, 0, len(groups))
	seen := map[string]bool{}
	for _, g := range groups {
		gv := groupView{Group: g}
		if t, ok := titles[g.Interface]; ok && t != g.Interface {
			gv.InterfaceTitle = t
		}
		if p, ok := policies[g.ID]; ok {
			gv.Policy = &p
		}
		if c, ok := cascades[g.ID]; ok {
			gv.Cascade = &c
		}
		gv.DnsZone = strings.HasPrefix(g.Name, dnsZoneGroupPrefix)
		gv.DnsTunnel = g.Name == "dns-tunnel"
		if d, ok := degraded[g.ID]; ok {
			gv.Degraded = d.Mode
		}
		seen[g.Interface] = true
		out = append(out, gv)
	}
	wrapper := false
	flavor := ""
	if c := s.casc(); c != nil {
		wrapper = c.WrapperDetected()
		flavor = string(c.BackendFlavor())
	}
	writeJSON(w, http.StatusOK, map[string]any{"available": true, "groups": out, "glitchy": s.store.MTGlitchy(), "wrapper": wrapper,
		"sources": s.mtSourcesCapable(r.Context()), "backendFlavor": flavor,
		"autoSave": s.autoSaveOn(), "draft": s.draftStatus()})
}

func (s *Server) mtGetInterfaces(w http.ResponseWriter, r *http.Request) {
	_, titles, err := s.mtSnapshotCached()
	if err != nil {
		titles = map[string]string{}
	}
	// выбираемы ВСЕ интерфейсы системы, не только занятые группами MT
	// (сильный баг, слово владельца 11.2026): туннель чужого софта -
	// не пул и не слот Keenetic, раньше попадал в селект групп правил,
	// только если его уже использовала какая-то группа MT. Новый
	// пользователь с единственным туннелем получал ПУСТОЙ список -
	// группу создать не на что. allIfaces покрывает слоты, системные
	// туннели, пулы и интерфейсы существующих групп MT разом.
	for _, sl := range s.allIfaces(r.Context()) {
		if sl.Device == "" || sl.Device == "blackhole" {
			continue
		}
		if _, ok := titles[sl.Device]; !ok {
			titles[sl.Device] = ""
		}
	}
	type ifaceView struct {
		Device string `json:"device"`
		Title  string `json:"title,omitempty"`
	}
	out := []ifaceView{}
	for dev, title := range titles {
		out = append(out, ifaceView{Device: dev, Title: title})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Device < out[j].Device })
	writeJSON(w, http.StatusOK, map[string]any{"interfaces": out})
}

func (s *Server) mtGetPresets(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"presets":    magitrickle.Presets,
		"sourceUrl":  magitrickle.AllowDomainsURL,
		"sourceNote": magitrickle.SourceNote(),
	})
}

func normalizeSources(srcs []string) ([]string, error) {
	out := make([]string, 0, len(srcs))
	for _, src := range srcs {
		src = strings.TrimSpace(src)
		if src == "" {
			continue
		}
		if _, _, err := net.ParseCIDR(src); err != nil && net.ParseIP(src) == nil {
			return nil, fmt.Errorf("источник не похож на IP или CIDR: %s", src)
		}
		out = append(out, src)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func (s *Server) mtCreateGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string   `json:"name"`
		Interface string   `json:"interface"`
		Color     string   `json:"color"`
		Sources   []string `json:"sources"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" || req.Interface == "" {
		writeErr(w, fmt.Errorf("нужны name и interface"))
		return
	}
	sources, err := normalizeSources(req.Sources)
	if err != nil {
		writeErr(w, err)
		return
	}
	color := req.Color
	if color == "" {
		color = "#4a9eff"
	}
	created := magitrickle.Group{Name: req.Name, Interface: req.Interface, Color: color, Enable: true, Sources: sources}
	if !s.autoSaveOn() {
		// 114-док: правка копится в черновике, MT не дёргаем
		if err := s.draftMutate(r.Context(), func(groups []magitrickle.Group) []magitrickle.Group {
			created.ID = draftLocalID(groups)
			return append(groups, created)
		}); err != nil {
			writeErr(w, err)
			return
		}
		s.store.LogEvent("rules", "magitrickle", "создана группа "+req.Name+" на "+req.Interface)
		s.draftResponse(w, http.StatusOK, created)
		return
	}
	client := s.mtClient()
	if err := client.EnsureHealthy(r.Context()); err != nil {
		writeErr(w, err)
		return
	}
	g, err := client.CreateGroup(r.Context(), created)
	if err != nil {
		writeErr(w, err)
		return
	}
	client.RefreshShadow(r.Context())
	s.store.LogEvent("rules", "magitrickle", "создана группа "+req.Name+" на "+req.Interface)
	writeJSON(w, http.StatusOK, g)
}

// magitrickle 0.8.2 не пересоздаёт iptables-цепочку при одиночном PUT
// группы, поэтому все правки идут массовым сохранением всего списка.
func (s *Server) mtApplyGroups(ctx context.Context, mutate func(groups []magitrickle.Group) bool) error {
	// веб-записи через тот же самолечащийся путь, что и движок: 500-й на
	// bulk-PUT иначе терял группы (кейс владельца с правкой telega)
	return s.engine.RetryMTWrite("panel", func() error {
		return s.mtClient().MutateGroups(ctx, mutate)
	})
}

func (s *Server) mtUpdateGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string    `json:"name"`
		Color     string    `json:"color"`
		Interface string    `json:"interface"`
		Sources   *[]string `json:"sources"`
		Confirm   bool      `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	var sources []string
	if req.Sources != nil {
		norm, err := normalizeSources(*req.Sources)
		if err != nil {
			writeErr(w, err)
			return
		}
		sources = norm
	}
	id := r.PathValue("id")
	if req.Interface != "" {
		if _, isCasc := s.store.CascadeByGroup(id); isCasc {
			writeErr(w, fmt.Errorf("интерфейс каскадной группы задается при создании каскада"))
			return
		}
	}
	if !s.autoSaveOn() {
		// 114-док: правка копится в черновике; коллизии адресов (97-док)
		// проверяются по черновику, зеркало реестра обновится после
		// применения сверкой снапшота
		if req.Sources != nil {
			_, dgroups, err := s.draftEnsure(r.Context())
			if err != nil {
				writeErr(w, err)
				return
			}
			for _, g := range dgroups {
				if g.ID != id || !deviceGroupOf(g) {
					continue
				}
				iface := g.Interface
				if req.Interface != "" {
					iface = req.Interface
				}
				if pool := s.poolNameByDevice(iface); pool != "" {
					conflicts := findBindingConflicts(s.store.DeviceBindings(), s.store.Excludes(), pool, sources)
					if len(conflicts) > 0 && !req.Confirm {
						bindingConflicts409(w, conflicts)
						return
					}
					if len(conflicts) > 0 {
						s.store.LogEvent("rules", "device-bindings", fmt.Sprintf(
							"коллизия адресов подтверждена вручную (правка %q): %s", g.Name, strings.Join(sources, ", ")))
					}
				}
				break
			}
		}
		found := false
		err := s.draftMutate(r.Context(), func(groups []magitrickle.Group) []magitrickle.Group {
			for i := range groups {
				g := &groups[i]
				if g.ID != id {
					continue
				}
				if req.Name != "" {
					g.Name = req.Name
				}
				if req.Color != "" {
					g.Color = req.Color
				}
				if req.Interface != "" {
					g.Interface = req.Interface
				}
				if req.Sources != nil {
					g.Sources = sources
				}
				found = true
				break
			}
			return groups
		})
		if err != nil {
			writeErr(w, err)
			return
		}
		if !found {
			http.NotFound(w, r)
			return
		}
		s.store.LogEvent("rules", "magitrickle", "изменена группа "+req.Name)
		s.draftResponse(w, http.StatusOK, map[string]string{"ok": "updated"})
		return
	}
	// правка источников группы устройства - те же коллизии, что при
	// создании (97-док): 409 + подтверждение, свою запись не считаем
	if req.Sources != nil {
		groups, err := s.mtClient().GroupsWithRules(r.Context())
		if err != nil {
			writeErr(w, err)
			return
		}
		for _, g := range groups {
			if g.ID != id || !deviceGroupOf(g) {
				continue
			}
			iface := g.Interface
			if req.Interface != "" {
				iface = req.Interface
			}
			if pool := s.poolNameByDevice(iface); pool != "" {
				conflicts := findBindingConflicts(s.store.DeviceBindings(), s.store.Excludes(), pool, sources)
				if len(conflicts) > 0 && !req.Confirm {
					bindingConflicts409(w, conflicts)
					return
				}
				if len(conflicts) > 0 {
					s.store.LogEvent("rules", "device-bindings", fmt.Sprintf(
						"коллизия адресов подтверждена вручную (правка %q): %s", g.Name, strings.Join(sources, ", ")))
				}
			}
			break
		}
	}
	found := false
	err := s.mtApplyGroups(r.Context(), func(groups []magitrickle.Group) bool {
		for i := range groups {
			g := &groups[i]
			if g.ID != id {
				continue
			}
			if req.Name != "" {
				g.Name = req.Name
			}
			if req.Color != "" {
				g.Color = req.Color
			}
			if req.Interface != "" {
				g.Interface = req.Interface
			}
			if req.Sources != nil {
				g.Sources = sources
			}
			found = true
			return true
		}
		return false
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	// зеркало связки вслед за правкой (97-док): состав/интерфейс могли
	// измениться; не-device или неизвестный пул - записи быть не должно
	if req.Sources != nil || req.Interface != "" {
		if groups, err := s.mtClient().GroupsWithRules(r.Context()); err == nil {
			for _, g := range groups {
				if g.ID != id {
					continue
				}
				if pool := s.poolNameByDevice(g.Interface); deviceGroupOf(g) && pool != "" {
					s.deviceBindingUpsert(pool, g.Name, g.Sources)
				} else {
					s.deviceBindingRemove(g.Name)
				}
				break
			}
		}
	}
	s.store.LogEvent("rules", "magitrickle", "изменена группа "+req.Name)
	writeJSON(w, http.StatusOK, map[string]string{"ok": "updated"})
}

func reorderIDs[T any](items []T, idOf func(T) string, ids []string) ([]T, bool) {
	if len(ids) != len(items) {
		return nil, false
	}
	byID := map[string]T{}
	for _, it := range items {
		byID[idOf(it)] = it
	}
	out := make([]T, 0, len(items))
	for _, id := range ids {
		it, ok := byID[id]
		if !ok {
			return nil, false
		}
		out = append(out, it)
		delete(byID, id)
	}
	if len(byID) > 0 {
		return nil, false
	}
	return out, true
}

func (s *Server) mtGroupsOrder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	if !s.autoSaveOn() {
		// 114-док: порядок копится в черновике
		ordered := false
		if err := s.draftMutate(r.Context(), func(groups []magitrickle.Group) []magitrickle.Group {
			out, ok := reorderIDs(groups, func(g magitrickle.Group) string { return g.ID }, req.IDs)
			ordered = ok
			if !ok {
				return groups
			}
			return out
		}); err != nil {
			writeErr(w, err)
			return
		}
		if !ordered {
			writeErr(w, fmt.Errorf("список ids не совпадает с текущими группами"))
			return
		}
		s.store.LogEvent("rules", "magitrickle", "изменён порядок групп")
		s.draftResponse(w, http.StatusOK, map[string]string{"ok": "reordered"})
		return
	}
	client := s.mtClient()
	groups, err := client.GroupsWithRules(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	ordered, ok := reorderIDs(groups, func(g magitrickle.Group) string { return g.ID }, req.IDs)
	if !ok {
		writeErr(w, fmt.Errorf("список ids не совпадает с текущими группами"))
		return
	}
	if err := client.UpdateGroups(r.Context(), ordered, true); err != nil {
		writeErr(w, err)
		return
	}
	s.store.LogEvent("rules", "magitrickle", "изменён порядок групп")
	writeJSON(w, http.StatusOK, map[string]string{"ok": "reordered"})
}

func (s *Server) mtRulesOrder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	id := r.PathValue("id")
	found, mismatch := false, false
	err := s.mtApplyGroups(r.Context(), func(groups []magitrickle.Group) bool {
		for i := range groups {
			if groups[i].ID != id {
				continue
			}
			ordered, ok := reorderIDs(groups[i].Rules, func(rl magitrickle.Rule) string { return rl.ID }, req.IDs)
			if !ok {
				mismatch = true
				return false
			}
			groups[i].Rules = ordered
			found = true
			return true
		}
		return false
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	if mismatch {
		writeErr(w, fmt.Errorf("список ids не совпадает с правилами группы"))
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	s.store.LogEvent("rules", "magitrickle", "изменён порядок правил в группе")
	writeJSON(w, http.StatusOK, map[string]string{"ok": "reordered"})
}

func (s *Server) mtToggleGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enable bool `json:"enable"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	id := r.PathValue("id")
	if c := s.casc(); c != nil {
		if _, isCasc := s.store.CascadeByGroup(id); isCasc {
			if err := c.SetEnabled(r.Context(), id, req.Enable); err != nil {
				writeErr(w, err)
				return
			}
			// тумблер служебной группы каскада меняет и автомат умного
			// каскада - держим зеркало-стейт в согласии с MT (86-док)
			s.engine.SmartCascadeSyncToggle(id, req.Enable)
			writeJSON(w, http.StatusOK, map[string]string{"ok": "ok"})
			return
		}
	}
	found := false
	if !s.autoSaveOn() {
		// 114-док: тумблер копится в черновике
		if err := s.draftMutate(r.Context(), func(groups []magitrickle.Group) []magitrickle.Group {
			for i := range groups {
				if groups[i].ID != id {
					continue
				}
				found = true
				groups[i].Enable = req.Enable
				break
			}
			return groups
		}); err != nil {
			writeErr(w, err)
			return
		}
		if !found {
			http.NotFound(w, r)
			return
		}
		s.draftResponse(w, http.StatusOK, map[string]string{"ok": "ok"})
		return
	}
	err := s.mtApplyGroups(r.Context(), func(groups []magitrickle.Group) bool {
		for i := range groups {
			if groups[i].ID != id {
				continue
			}
			found = true
			if groups[i].Enable == req.Enable {
				return false
			}
			groups[i].Enable = req.Enable
			return true
		}
		return false
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "ok"})
}

func (s *Server) mtDeleteGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if c := s.casc(); c != nil {
		if _, isCasc := s.store.CascadeByGroup(id); isCasc {
			if err := c.Delete(r.Context(), r.PathValue("id")); err != nil {
				writeErr(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"ok": "deleted"})
			return
		}
	}
	if !s.autoSaveOn() {
		// 114-док: удаление копится в черновике; рантайм-следы группы
		// (хук, политика, реестр) не трогаем - группа ещё жива в MT
		removed := false
		if err := s.draftMutate(r.Context(), func(groups []magitrickle.Group) []magitrickle.Group {
			for i := range groups {
				if groups[i].ID != id {
					continue
				}
				removed = true
				return append(groups[:i], groups[i+1:]...)
			}
			return groups
		}); err != nil {
			writeErr(w, err)
			return
		}
		if !removed {
			http.NotFound(w, r)
			return
		}
		s.store.LogEvent("rules", "magitrickle", "удалена группа "+id)
		s.draftResponse(w, http.StatusOK, map[string]string{"ok": "deleted"})
		return
	}
	client := s.mtClient()
	if err := client.DeleteGroup(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	client.RefreshShadow(r.Context())
	// роутерный OUTPUT-хук и политика - рантайм-след группы, чистим вслед
	if c := s.casc(); c != nil {
		_ = c.HookRemove(id)
	}
	_ = s.store.SetGroupPolicy(id, nil)
	// запись реестра устройств - тоже след группы (97-док): реестр хранит
	// имена, id разрешаем по кэшу снапшота; мимо кэша снимет сверка
	s.deviceBindingRemoveByID(id)
	s.store.LogEvent("rules", "magitrickle", "удалена группа "+id)
	writeJSON(w, http.StatusOK, map[string]string{"ok": "deleted"})
}
