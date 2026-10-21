package web

// Зонный DNS (80-док, форк rev13): доменная зона -> упорядоченный список
// своих DNS-серверов; путь до них - через конкретный пул (группа MT
// dnszone-<зона> с subnet-правилами адресов серверов; форк rev7+ сам
// маркирует OUTPUT, так что запросы dns-proxy к серверам зоны уходят в
// туннель, а лежачий пул = пакет в несуществующий интерфейс = честный
// fail-closed). Каскад и health серверов зоны - машина форка («первый
// живой», liveness без контент-канарейки); полный отвал зоны - SERVFAIL,
// FallbackOpen - сознательная деградация в общий список.

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"strings"
	"time"

	"mawg/internal/dnsprobe"
	"mawg/internal/magitrickle"
	"mawg/internal/store"
)

// dnsZoneGroupPrefix - префикс имён служебных групп пути зон; всё с этим
// префиксом принадлежит зонному DNS и синхронизируется применением.
const dnsZoneGroupPrefix = "dnszone-"

// maxDnsZones - потолок таблицы (форк держит тот же).
const maxDnsZones = 32

// normalizeDnsZoneName - канонизация имени зоны: lowercase, trim, без
// хвостовых точек; кривой ввод - ошибка (в форке та же нормализация).
func normalizeDnsZoneName(z string) (string, error) {
	t := strings.ToLower(strings.TrimSpace(z))
	t = strings.TrimRight(t, ".")
	if t == "" {
		return "", fmt.Errorf("пустое имя зоны")
	}
	if strings.ContainsAny(t, " \t/\\") || strings.Contains(t, "://") || strings.Contains(t, "..") {
		return "", fmt.Errorf("кривое имя зоны %q", z)
	}
	return t, nil
}

// dnsZoneGroupName - стабильное имя группы пути зоны: префикс + слаг
// зоны + короткий хеш (слаг с заменой точек может коллизировать:
// a.b-c и a-b.c).
func dnsZoneGroupName(zone string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(zone))
	slug := strings.ReplaceAll(zone, ".", "-")
	if len(slug) > 24 {
		slug = slug[:24]
	}
	return fmt.Sprintf("%s%s-%04x", dnsZoneGroupPrefix, slug, h.Sum32()&0xffff)
}

// validateDnsZones - валидация+канонизация таблицы из редактора: имя
// зоны, адреса серверов (нормализуются), дубли зон схлопываются, пул
// должен существовать. Возвращает каноничную копию для стора.
func (s *Server) validateDnsZones(zones []store.DnsZone) ([]store.DnsZone, error) {
	if len(zones) > maxDnsZones {
		return nil, fmt.Errorf("зон больше %d", maxDnsZones)
	}
	out := make([]store.DnsZone, 0, len(zones))
	seen := map[string]struct{}{}
	pools := map[string]bool{}
	for _, z := range zones {
		name, err := normalizeDnsZoneName(z.Zone)
		if err != nil {
			return nil, err
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		if len(z.Upstreams) == 0 {
			return nil, fmt.Errorf("зона %s: список DNS-серверов пуст", name)
		}
		ups := make([]string, 0, len(z.Upstreams))
		for _, raw := range z.Upstreams {
			a, err := normalizeUpstream(raw)
			if err != nil {
				return nil, fmt.Errorf("зона %s: %w", name, err)
			}
			ups = append(ups, a)
		}
		zone := store.DnsZone{ID: z.ID, Zone: name, Upstreams: ups, FallbackOpen: z.FallbackOpen, Enable: z.Enable}
		if z.Pool != "" {
			// путь зоны: имя пула mawg ИЛИ готовый интерфейс (iface:<dev>,
			// фидбэк пользователя зон: WG настроен руками, мимо mawg)
			if strings.HasPrefix(z.Pool, "iface:") {
				dev := strings.TrimPrefix(z.Pool, "iface:")
				if dev == "" || strings.ContainsAny(dev, " /\\") {
					return nil, fmt.Errorf("зона %s: кривое имя интерфейса %q", name, dev)
				}
			} else if !pools[z.Pool] {
				if _, ok := s.store.Pool(z.Pool); !ok {
					return nil, fmt.Errorf("зона %s: пул %q не найден", name, z.Pool)
				}
				pools[z.Pool] = true
			}
			zone.Pool = z.Pool
		}
		if zone.ID == "" {
			zone.ID = fmt.Sprintf("z%08x", fnv32a(name))
		}
		out = append(out, zone)
	}
	return out, nil
}

func fnv32a(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}

// zoneGroupRules - правила группы пути зоны: subnet-статика под адреса
// серверов зоны (/32, IPv6 - /128). Пресетов нет - зона знает только свои
// серверы; форк сам маркирует OUTPUT, так что запросы dns-proxy идут
// через пул.
func zoneGroupRules(ips []string) []magitrickle.Rule {
	rules := make([]magitrickle.Rule, 0, len(ips))
	seen := map[string]struct{}{}
	for _, ip := range ips {
		typ, rule := "subnet", ip+"/32"
		if strings.Contains(ip, ":") {
			typ, rule = "subnet6", ip+"/128"
		}
		key := typ + "|" + rule
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		rules = append(rules, magitrickle.Rule{Type: typ, Rule: rule, Enable: true})
	}
	return rules
}

// applyDnsZones - применение таблицы: группы пути (создать/обновить/
// удалить осиротевшие) + один PUT таблицы зон в форк. Идемпотентно.
func (s *Server) applyDnsZones(ctx context.Context, zones []store.DnsZone) (warnings []string, err error) {
	client := s.mtClient()
	if err := client.EnsureHealthy(ctx); err != nil {
		return nil, err
	}
	groups, err := client.GroupsWithRules(ctx)
	if err != nil {
		return nil, err
	}
	byName := map[string]magitrickle.Group{}
	for _, g := range groups {
		if strings.HasPrefix(g.Name, dnsZoneGroupPrefix) {
			byName[g.Name] = g
		}
	}
	specs := make([]magitrickle.DnsZoneSpec, 0, len(zones))
	desired := map[string]struct{}{}
	for _, z := range zones {
		if !z.Enable {
			continue // выключенная зона: не в таблице форка и без группы
		}
		specs = append(specs, magitrickle.DnsZoneSpec{Zone: z.Zone, Upstreams: z.Upstreams, Fallback: z.FallbackOpen})
		if z.Pool == "" {
			continue // путь напрямую - группы не нужно
		}
		iface := ""
		if strings.HasPrefix(z.Pool, "iface:") {
			// готовый интерфейс (настроен мимо mawg) - устройство как есть
			iface = strings.TrimPrefix(z.Pool, "iface:")
		} else {
			p, ok := s.store.Pool(z.Pool)
			if !ok {
				warnings = append(warnings, fmt.Sprintf("зона %s: пул %q исчез, зона применена без группы пути", z.Zone, z.Pool))
				continue
			}
			iface = p.DeviceName()
		}
		ips, failed := upstreamHostIPs(ctx, z.Upstreams)
		for _, h := range failed {
			warnings = append(warnings, fmt.Sprintf("зона %s: адрес %s не разрезолвился для статики группы (применю без него; повторите позже)", z.Zone, h))
		}
		if len(ips) == 0 {
			warnings = append(warnings, fmt.Sprintf("зона %s: ни один адрес сервера не стал статикой группы пути", z.Zone))
		}
		name := dnsZoneGroupName(z.Zone)
		rules := zoneGroupRules(ips)
		if g, ok := byName[name]; ok {
			delete(byName, name)
			if g.Interface != iface || !rulesEqual(g.Rules, rules) {
				g.Interface = iface
				g.Rules = rules
				if err := client.UpdateGroup(ctx, g, true); err != nil {
					return warnings, fmt.Errorf("группа %s не обновлена: %w", name, err)
				}
			}
		} else {
			if err := client.AddGroup(ctx, magitrickle.Group{Name: name, Interface: iface, Enable: true, Rules: rules}); err != nil {
				return warnings, fmt.Errorf("группа %s не создана: %w", name, err)
			}
		}
		desired[name] = struct{}{}
	}
	for name, g := range byName {
		if _, want := desired[name]; want {
			continue
		}
		if err := client.DeleteGroup(ctx, g.ID); err != nil {
			warnings = append(warnings, fmt.Sprintf("осиротевшая группа %s не удалена (%v); попробуйте ещё раз", name, err))
		}
	}
	if err := client.SetDnsZones(ctx, specs); err != nil {
		return warnings, err
	}
	return warnings, nil
}

// getDnsZones - GET /api/v1/dns/zones: сохранённая таблица + capability +
// живой стейт зон из форка (alive/active по серверам каждой зоны).
func (s *Server) getDnsZones(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	res := map[string]any{"capable": s.mtDnsZonesCapable(ctx)}
	res["zones"] = s.store.DnsZones()
	if res["capable"] == true {
		if st, err := s.mtClient().DnsZones(ctx); err == nil {
			res["live"] = st
		}
	}
	writeJSON(w, http.StatusOK, res)
}

// putDnsZones - PUT /api/v1/dns/zones {zones, confirm}: сохранить и
// применить (группы пути + таблица форка). Пустой список = очистка.
func (s *Server) putDnsZones(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Zones   []store.DnsZone `json:"zones"`
		Confirm bool            `json:"confirm"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(&req); err != nil {
		writeErr(w, fmt.Errorf("кривой запрос"))
		return
	}
	zones, err := s.validateDnsZones(req.Zones)
	if err != nil {
		writeErr(w, err)
		return
	}
	// петлезащита (та же, что у политики): адрес dnsmasq среди серверов
	// зоны при dnsmasq.inMT замыкает петлю
	var addrs []string
	for _, z := range zones {
		addrs = append(addrs, z.Upstreams...)
	}
	if risky := s.loopRiskUpstreams(addrs); len(risky) > 0 && !req.Confirm {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":    "замкнёт петлю: dnsmasq сейчас направлен в MT, а среди серверов зоны есть его адрес (" + strings.Join(risky, ", ") + ")",
			"loopRisk": risky,
		})
		return
	}
	if len(zones) > 0 && !s.mtDnsZonesCapable(r.Context()) {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "зонный DNS требует MagiTrickle 0.8.2-rev13+ (обновите MT в Системе)"})
		return
	}
	warnings, err := s.applyDnsZones(r.Context(), zones)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	if err := s.store.SetDnsZones(zones); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	for _, wn := range warnings {
		s.store.LogEvent("dns", "zones", wn)
	}
	if len(zones) == 0 {
		s.store.LogEvent("dns", "zones", "таблица зон очищена")
	} else {
		s.store.LogEvent("dns", "zones", fmt.Sprintf("таблица зон применена (%d шт.): %s", len(zones), zoneListShort(zones)))
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "zones": zones, "warnings": warnings})
}

// zoneListShort - имена зон для строки журнала.
func zoneListShort(zones []store.DnsZone) string {
	names := make([]string, 0, len(zones))
	for _, z := range zones {
		names = append(names, z.Zone)
	}
	return strings.Join(names, ", ")
}

// postDnsZonesProbe - POST /api/v1/dns/zones/{id}/probe: канарейка по
// каждому серверу зоны; путь - как у клиентов dns-proxy (маркировка
// группой зоны через OUTPUT форка rev7+), так что проба честно идёт
// через пул. Два прогона (первый греет туннель), лучший по уровню.
func (s *Server) postDnsZonesProbe(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var zone *store.DnsZone
	for _, z := range s.store.DnsZones() {
		if z.ID == id {
			zc := z
			zone = &zc
			break
		}
	}
	if zone == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "зона не найдена"})
		return
	}
	levels := make([]dnsprobe.Level, 0, len(zone.Upstreams))
	for _, raw := range zone.Upstreams {
		if l, ok := policyProbeLevel("зона "+zone.Zone, raw); ok {
			levels = append(levels, l)
		}
	}
	if len(levels) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "в зоне нет ни одного корректного адреса"})
		return
	}
	// два прогона: первый греет туннель и цепочки MT (холодный handshake
	// даёт таймауты на туннельном уровне - живой кейс владельца), в
	// результат идёт лучший из двух
	first := dnsprobe.Run(levels, 3*time.Second)
	time.Sleep(2 * time.Second)
	writeJSON(w, http.StatusOK, map[string]any{"results": bestOfPasses(first, dnsprobe.Run(levels, 3*time.Second))})
}
