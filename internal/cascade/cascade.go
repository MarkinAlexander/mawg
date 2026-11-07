package cascade

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"

	"mawg/internal/magitrickle"
	"mawg/internal/store"
)

const serviceColor = "#8a8f98"

type Manager struct {
	Store *store.Store
	MT    *magitrickle.Client

	// Retry подключает самолечение записей (Engine.RetryMTWrite): сбой
	// bulk-PUT -> рестарт MT -> одна повторная попытка. Каскадные ручки
	// без него теряли группу каскада при первом же 500-м (кейс владельца
	// с созданием каскада 2026-10-11).
	Retry func(key string, attempt func() error) error

	// EP (опционально) - авторезолв эндпоинтов роутерных WG-интерфейсов
	// для iface-источников (platform.Endpointer; баг владельца: каскад с
	// nwg7 на Кинетике требовал адреса от руки).
	EP interface {
		IfaceEndpoints(device string) ([]string, error)
	}

	mu       sync.Mutex
	wrapper  bool                                 // в mangle OUTPUT видны чужие MR_*-цепочки (обёртка юзера)
	mutating int                                  // глубина окна мутации: SyncHooks в нём молчит (см. beginMutate)
	exclActive bool                               // исключения могли остаться в цепочках - синк продолжает вычищать (exclusions.go)
	flavor   magitrickle.MtFlavor                 // flavor бэкенда MT (проталкивает rotator после пробы версии)
	iptRun   func(args ...string) (string, error) // подменяется в тестах
}

func (m *Manager) retry(key string, attempt func() error) error {
	if m.Retry == nil {
		return attempt()
	}
	return m.Retry(key, attempt)
}

// RouterAddrs - адреса самого роутера для источников групп; переопределяется
// в тестах (на живом роутере это все не-loopback и не link-local адреса
// интерфейсов: LAN-адрес, WAN-адрес ppp-туннеля и т.д. - трафик роутера
// проходит guard группы в OUTPUT-пути по собственным адресам).
var RouterAddrs = func() []string {
	ifaces, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range ifaces {
		ipn, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipn.IP
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			continue
		}
		out = append(out, ip.String())
	}
	return out
}

// ApplyGroupSources - применить sources-политику к группе: эффективный
// список (юзер + адреса роутера) перезаписывается в MagiTrickle при
// расхождении, OUTPUT-хук приводится по RouterHook.
func (m *Manager) ApplyGroupSources(id string, p store.GroupPolicy) error {
	want := p.EffectiveSources(RouterAddrs())
	if err := m.syncGroupSources(id, want); err != nil {
		return err
	}
	m.SyncHooks()
	return nil
}

// syncGroupSources перезаписывает sources группы, если фактический список
// разошёлся с ожидаемым (смена политики, новый WAN-адрес роутера).
func (m *Manager) syncGroupSources(id string, want []string) error {
	groups, err := m.MT.GroupsWithRules(context.Background())
	if err != nil {
		return err
	}
	drift := false
	found := false
	for _, g := range groups {
		if g.ID != id {
			continue
		}
		found = true
		drift = !equalStrings(g.Sources, want)
		break
	}
	if !found || !drift {
		return nil
	}
	// sources-правка пересобирает цепочку группы форком, а на группе может
	// висеть её же роутерный хук - пишем в том же защитном окне, что и все
	// записи (сняли хуки, проверили, записали, вернули)
	m.beginMutate()
	m.reportStuck(m.sweepOutputHooks())
	err = m.retry("sources", func() error {
		return m.MT.MutateGroups(context.Background(), func(groups []magitrickle.Group) bool {
			for i := range groups {
				if groups[i].ID != id {
					continue
				}
				groups[i].Sources = want
				return true
			}
			return false
		})
	})
	m.endMutate()
	if err != nil {
		return err
	}
	m.log("policy", "sources", "источники группы "+id+" обновлены: "+strings.Join(want, ", "))
	return nil
}

func equalStrings(a, b []string) bool {
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

func New(st *store.Store, mt *magitrickle.Client) *Manager {
	return &Manager{Store: st, MT: mt}
}

func (m *Manager) log(pool, kind, msg string) {
	m.Store.LogEvent(pool, kind, msg)
}

func (m *Manager) Create(ctx context.Context, source, via string, hosts []string) (magitrickle.Group, error) {
	srcKind, srcName, err := parseSource(source)
	if err != nil {
		return magitrickle.Group{}, err
	}
	if source == via {
		return magitrickle.Group{}, fmt.Errorf("источник и путь совпадают")
	}
	device, viaTitle, err := viaDevice(m, via)
	if err != nil {
		return magitrickle.Group{}, err
	}
	if device == "blackhole" {
		return magitrickle.Group{}, fmt.Errorf("каскад через blackhole не имеет смысла")
	}

	var srcTitle string
	ruleHosts := hosts
	var domains []string
	switch srcKind {
	case "pool":
		p, ok := m.Store.Pool(srcName)
		if !ok {
			return magitrickle.Group{}, fmt.Errorf("пул %q не найден", srcName)
		}
		srcTitle = srcName
		poolHosts, poolDomains := m.poolEndpoints(p)
		if len(poolHosts)+len(poolDomains) == 0 {
			return magitrickle.Group{}, fmt.Errorf("у пула %q нет конфигов с эндпоинтами", srcName)
		}
		ruleHosts = append(ruleHosts, poolHosts...)
		domains = poolDomains
		ruleHosts = append(ruleHosts, poolDomains...)
	case "iface":
		// ruleHosts уже скопирован из hosts - дополняем его же
		if len(ruleHosts) == 0 && m.EP != nil {
			// авторезолв эндпоинтов роутерного WG-интерфейса (Кинетик RCI/
			// ndmc отдаёт remote-endpoint-address пиров)
			if resolved, epErr := m.EP.IfaceEndpoints(srcName); epErr == nil && len(resolved) > 0 {
				ruleHosts = resolved
			}
		}
		if len(ruleHosts) == 0 {
			if m.EP != nil {
				if _, epErr := m.EP.IfaceEndpoints(srcName); epErr != nil {
					return magitrickle.Group{}, fmt.Errorf("не удалось определить эндпоинты интерфейса %q (%v) - перечислите адреса вручную", srcName, epErr)
				}
			}
			return magitrickle.Group{}, fmt.Errorf("для интерфейса-источника перечислите адреса или домены его эндпоинтов")
		}
		srcTitle = srcName
	}

	resolutions := map[string][]string{}
	for _, d := range domains {
		resolutions[d] = m.resolve(ctx, d)
	}
	rules := BuildRules(ruleHosts, resolutions)
	if len(rules) == 0 {
		return magitrickle.Group{}, fmt.Errorf("не получилось собрать правила каскада")
	}

	name := "Служебная: " + srcTitle + " через " + viaTitle
	// дубли каскадов запрещены: второй ipset-to-link на тот же интерфейс
	// роняет iptables-restore всего батча (живой кейс 2026-10-11 - 500 и
	// деградация рантайма до ручного восстановления)
	for _, c := range m.Store.Cascades() {
		if c.Source == source && c.Via == via {
			return magitrickle.Group{}, fmt.Errorf("каскад %s -> %s уже есть", srcTitle, viaTitle)
		}
	}
	if err := m.guardLoop(source, via, true); err != nil {
		return magitrickle.Group{}, err
	}
	g := magitrickle.Group{Name: name, Color: serviceColor, Interface: device, Enable: true, Rules: rules}
	if err := m.retry("cascade", func() error {
		if err := m.MT.EnsureHealthy(ctx); err != nil {
			return err
		}
		// группа с таким именем уже есть в MT (в т.ч. вне стора) - дубль
		// уронит батч на ipset-to-link
		groups, err := m.MT.GroupsWithRules(ctx)
		if err != nil {
			return err
		}
		for _, x := range groups {
			if x.Name == name {
				return fmt.Errorf("группа %q уже есть в magitrickle, дубли каскадов запрещены", name)
			}
		}
		return m.MT.AddGroup(ctx, g)
	}); err != nil {
		return magitrickle.Group{}, err
	}
	groups, err := m.MT.GroupsWithRules(ctx)
	if err != nil {
		return magitrickle.Group{}, err
	}
	created := magitrickle.Group{}
	for _, x := range groups {
		if x.Name == name {
			created = x
		}
	}
	if created.ID == "" {
		return magitrickle.Group{}, fmt.Errorf("magitrickle не вернул созданную группу")
	}
	entry := store.CascadeEntry{Group: created.ID, Name: name, Source: source, Via: via, Domains: domains}
	casc := m.Store.Cascades()
	casc = append(casc, entry)
	if err := m.Store.SetCascades(casc); err != nil {
		return created, err
	}
	if err := m.HookAdd(created.ID); err != nil {
		m.log("cascade", "hook", "каскад создан, но хук не встал: "+err.Error())
	}
	m.Store.LogEvent("cascade", "create", name+" ("+strconv.Itoa(len(rules))+" правил)")
	m.RefreshShadowSafe(ctx)
	return created, nil
}

// guardLoop не даёт включить встречные каскады - зациклят инкапсуляцию.
func (m *Manager) guardLoop(source, via string, enabling bool) error {
	if !enabling {
		return nil
	}
	for _, c := range m.Store.Cascades() {
		if c.Source != via || c.Via != source {
			continue
		}
		groups, err := m.groups(context.Background())
		if err != nil {
			return fmt.Errorf("не проверить каскады: %v", err)
		}
		for _, g := range groups {
			if g.ID == c.Group && g.Enable {
				return fmt.Errorf("встречный каскад: уже включен %q (%s через %s), одновременно нельзя - зациклится", c.Name, c.Source, c.Via)
			}
		}
	}
	return nil
}

func (m *Manager) groups(ctx context.Context) ([]magitrickle.Group, error) {
	return m.MT.GroupsWithRules(ctx)
}

func (m *Manager) RefreshShadowSafe(ctx context.Context) {
	m.MT.RefreshShadow(ctx)
}

func (m *Manager) Delete(ctx context.Context, groupID string) error {
	if _, ok := m.Store.CascadeByGroup(groupID); !ok {
		return fmt.Errorf("каскад %s не найден", groupID)
	}
	m.beginMutate()
	defer m.endMutate()
	if err := m.HookRemove(groupID); err != nil {
		return err
	}
	if err := m.retry("cascade", func() error { return m.MT.EnsureHealthy(ctx) }); err != nil {
		return err
	}
	if err := m.MT.DeleteGroup(ctx, groupID); err != nil {
		return err
	}
	casc := m.Store.Cascades()
	out := casc[:0]
	var entry store.CascadeEntry
	for _, c := range casc {
		if c.Group == groupID {
			entry = c
			continue
		}
		out = append(out, c)
	}
	if err := m.Store.SetCascades(out); err != nil {
		return err
	}
	m.MT.RefreshShadow(ctx)
	m.Store.LogEvent("cascade", "delete", "удален каскад "+entry.Name)
	return nil
}

// SetEnabled: хук снимается ДО выключения и ставится ПОСЛЕ включения группы.
func (m *Manager) SetEnabled(ctx context.Context, groupID string, enable bool) error {
	entry, ok := m.Store.CascadeByGroup(groupID)
	if !ok {
		return fmt.Errorf("каскад %s не найден", groupID)
	}
	if err := m.retry("cascade", func() error { return m.MT.EnsureHealthy(ctx) }); err != nil {
		return err
	}
	if enable {
		if err := m.guardLoop(entry.Source, entry.Via, true); err != nil {
			return err
		}
		if err := m.toggle(ctx, groupID, true); err != nil {
			return err
		}
		if err := m.HookAdd(groupID); err != nil {
			return err
		}
		m.Store.LogEvent("cascade", "toggle", entry.Name+" включен")
		return nil
	}
	if err := m.HookRemove(groupID); err != nil {
		return err
	}
	if err := m.toggle(ctx, groupID, false); err != nil {
		return err
	}
	m.FlushConns(m.cascadeHosts(ctx, entry))
	m.Store.LogEvent("cascade", "toggle", entry.Name+" выключен")
	return nil
}

func (m *Manager) toggle(ctx context.Context, groupID string, enable bool) error {
	return m.retry("cascade", func() error {
		return m.MT.MutateGroups(ctx, func(groups []magitrickle.Group) bool {
			for i := range groups {
				if groups[i].ID == groupID {
					if groups[i].Enable == enable {
						return false
					}
					groups[i].Enable = enable
					return true
				}
			}
			return false
		})
	})
}

func (m *Manager) cascadeHosts(ctx context.Context, entry store.CascadeEntry) []string {
	var hosts []string
	kind, name, err := parseSource(entry.Source)
	if err == nil && kind == "pool" {
		if p, ok := m.Store.Pool(name); ok {
			h, d := m.poolEndpoints(p)
			hosts = append(hosts, h...)
			for _, dom := range d {
				hosts = append(hosts, m.resolve(ctx, dom)...)
			}
		}
	}
	// iface-источник: эндпоинты роутерного WG-интерфейса перечитываем так
	// же, как у пулов - «адреса добавляются сами» работает и тут (админ
	// роутера сменил endpoint пира - каскад догоняет)
	if err == nil && kind == "iface" && m.EP != nil {
		if resolved, epErr := m.EP.IfaceEndpoints(name); epErr == nil {
			hosts = append(hosts, resolved...)
		}
	}
	hosts = append(hosts, entry.Domains...)
	return hosts
}

// SyncPoolRules - пересборка правил каскадов ИСТОЧНИКА по текущему
// составу (жалобы пользователей: новые конфиги не попадали в каскад,
// адреса удалённых/выключенных оставались навсегда). Пул без включённых
// конфигов каскаду смысла не даёт - каскад выключаем с событием.
func (m *Manager) SyncPoolRules(ctx context.Context, source string) {
	for _, entry := range m.Store.Cascades() {
		if entry.Source != source {
			continue
		}
		kind, name, err := parseSource(source)
		if err != nil {
			continue
		}
		if kind == "pool" {
			p, ok := m.Store.Pool(name)
			if !ok {
				// пул удалён: каскад с его адресами остался бы висеть включённым
				m.disableIfOn(ctx, entry, "пул-источник удалён - каскад выключен")
				continue
			}
			poolHosts, poolDomains := m.poolEndpoints(p)
			if len(poolHosts)+len(poolDomains) == 0 {
				m.disableIfOn(ctx, entry, "у пула-источника не осталось включённых конфигов - каскад выключен")
				continue
			}
			if entry.AutoDisabled {
				m.reEnableAuto(ctx, entry)
			}
			m.syncRules(ctx, entry, append(poolHosts, poolDomains...), nil)
			continue
		}
		if kind == "iface" && m.EP != nil {
			if resolved, epErr := m.EP.IfaceEndpoints(name); epErr == nil && len(resolved) > 0 {
				m.syncRules(ctx, entry, resolved, nil)
			}
		}
	}
}

// disableIfOn - гасит каскад с событием и меткой AutoDisabled (по ней
// sync вернёт его при оживании пула). Уже выключенный (или исчезла
// группа) не трогаем - иначе каждый sync сыпал бы одно и то же событие;
// ручное выключение пользователя метки не получает.
func (m *Manager) disableIfOn(ctx context.Context, entry store.CascadeEntry, reason string) {
	groups, err := m.MT.GroupsWithRules(ctx)
	if err != nil {
		return
	}
	for _, g := range groups {
		if g.ID != entry.Group {
			continue
		}
		if g.Enable {
			if err := m.SetEnabled(ctx, entry.Group, false); err == nil {
				m.updateCascade(entry.Group, func(e *store.CascadeEntry) { e.AutoDisabled = true })
				m.Store.LogEvent("cascade", "sync", entry.Name+": "+reason)
			}
		}
		return
	}
}

// reEnableAuto - возврат каскада, погашенного самим sync (флаг
// AutoDisabled). Живую группу в MT не дёргаем - её мог включить умный
// каскад, тогда просто снимаем флаг.
func (m *Manager) reEnableAuto(ctx context.Context, entry store.CascadeEntry) {
	clear := func() { m.updateCascade(entry.Group, func(e *store.CascadeEntry) { e.AutoDisabled = false }) }
	groups, err := m.MT.GroupsWithRules(ctx)
	if err != nil {
		return
	}
	for _, g := range groups {
		if g.ID != entry.Group {
			continue
		}
		if !g.Enable {
			if err := m.SetEnabled(ctx, entry.Group, true); err != nil {
				m.log("cascade", "sync", entry.Name+": пул-источник ожил, но каскад не включился: "+err.Error())
				return
			}
			m.Store.LogEvent("cascade", "sync", entry.Name+": пул-источник снова с конфигами - каскад включён")
		}
		clear()
		return
	}
	clear() // группа исчезла - метка больше не имеет смысла
}

func (m *Manager) updateCascade(group string, fn func(*store.CascadeEntry)) {
	cs := m.Store.Cascades()
	for i := range cs {
		if cs[i].Group == group {
			fn(&cs[i])
			_ = m.Store.SetCascades(cs)
			return
		}
	}
}

func (m *Manager) RefreshDNS(ctx context.Context) {
	for _, entry := range m.Store.Cascades() {
		if len(entry.Domains) == 0 {
			continue
		}
		var ips []string
		for _, d := range entry.Domains {
			ips = append(ips, m.resolve(ctx, d)...)
		}
		m.syncRules(ctx, entry, ips, nil)
	}
}

// syncRules - ПРИВОДИТ правила группы каскада к нужному набору: было
// add-only, адреса удалённых/выключенных конфигов оставались навсегда
// (живые жалобы); теперь полная замена с diff-логом. Домены пула
// резолвятся и дублируются A-записями - как при Create.
func (m *Manager) syncRules(ctx context.Context, entry store.CascadeEntry, hosts, _ []string) {
	if len(hosts) == 0 {
		return
	}
	var domains []string
	for _, h := range hosts {
		if net.ParseIP(h) == nil {
			if _, _, err := net.ParseCIDR(h); err != nil {
				domains = append(domains, h)
			}
		}
	}
	resolutions := map[string][]string{}
	for _, d := range domains {
		resolutions[d] = m.resolve(ctx, d)
	}
	need := BuildRules(hosts, resolutions)
	groups, err := m.MT.GroupsWithRules(ctx)
	if err != nil {
		return
	}
	existing := map[string]bool{}
	var target *magitrickle.Group
	for i := range groups {
		if groups[i].ID == entry.Group {
			target = &groups[i]
			for _, r := range groups[i].Rules {
				existing[r.Type+" "+r.Rule] = true
			}
		}
	}
	if target == nil {
		m.log("cascade", "sync", "группа каскада исчезла: "+entry.Name)
		return
	}
	needSet := map[string]bool{}
	added, removed := 0, 0
	for _, r := range need {
		k := r.Type + " " + r.Rule
		needSet[k] = true
		if !existing[k] {
			added++
		}
	}
	for k := range existing {
		if !needSet[k] {
			removed++
		}
	}
	if added == 0 && removed == 0 {
		return
	}
	kept := make([]magitrickle.Rule, 0, len(need))
	for _, r := range target.Rules {
		if needSet[r.Type+" "+r.Rule] {
			kept = append(kept, r) // сохраняем id включённых правил
		}
	}
	for _, r := range need {
		if !existing[r.Type+" "+r.Rule] {
			kept = append(kept, r)
		}
	}
	err = m.retry("cascade", func() error {
		return m.MT.MutateGroups(ctx, func(groups []magitrickle.Group) bool {
			for i := range groups {
				if groups[i].ID != entry.Group {
					continue
				}
				groups[i].Rules = kept
				return true
			}
			return false
		})
	})
	if err != nil {
		m.log("cascade", "sync", "не пересобрал правила "+entry.Name+": "+err.Error())
		return
	}
	m.Store.LogEvent("cascade", "sync", entry.Name+": правила пересобраны (+"+strconv.Itoa(added)+"/-"+strconv.Itoa(removed)+")")
}

type Info struct {
	store.CascadeEntry
	Enabled bool   `json:"enabled"`
	Hook    bool   `json:"hook"`
	Rules   int    `json:"rules"`
	Err     string `json:"err,omitempty"`
}

func (m *Manager) Infos(ctx context.Context) []Info {
	casc := m.Store.Cascades()
	out := make([]Info, 0, len(casc))
	groups, err := m.MT.GroupsWithRules(ctx)
	if err != nil {
		for _, c := range casc {
			out = append(out, Info{CascadeEntry: c, Err: err.Error()})
		}
		return out
	}
	byID := map[string]magitrickle.Group{}
	for _, g := range groups {
		byID[g.ID] = g
	}
	for _, c := range casc {
		info := Info{CascadeEntry: c}
		if g, ok := byID[c.Group]; ok {
			info.Enabled = g.Enable
			info.Rules = len(g.Rules)
			info.Hook = m.HookPresent(c.Group)
		} else {
			info.Err = "группа удалена из magitrickle"
		}
		out = append(out, info)
	}
	return out
}
