package cascade

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"mawg/internal/magitrickle"
	"mawg/internal/store"
)

const serviceColor = "#8a8f98"

type Manager struct {
	Store *store.Store
	MT    *magitrickle.Client

	mu sync.Mutex
}

func New(st *store.Store, mt *magitrickle.Client) *Manager {
	return &Manager{Store: st, MT: mt}
}

func (m *Manager) log(pool, kind, msg string) {
	m.Store.LogEvent(pool, kind, msg)
}

func (m *Manager) chainName(groupID string) string {
	return "MT_" + groupID
}

func (m *Manager) ipt(args ...string) (string, error) {
	cmd := exec.Command("iptables", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

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
	if !m.HookPresent(groupID) {
		return nil
	}
	_, err := m.ipt("-t", "mangle", "-D", "OUTPUT", "-j", m.chainName(groupID))
	if err == nil {
		m.log("cascade", "hook", "OUTPUT-хук снят: "+m.chainName(groupID))
	}
	return err
}

// BeforeMutate снимает хуки групп, которые запись удалит или выключит:
// атомарный restore magitrickle роняет батч об удаляемую цепочку.
func (m *Manager) BeforeMutate(mutate func([]magitrickle.Group) bool) {
	casc := m.Store.Cascades()
	if len(casc) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	groups, err := m.MT.GroupsWithRules(ctx)
	if err != nil {
		for _, c := range casc {
			if err := m.HookRemove(c.Group); err != nil {
				m.log("cascade", "hook", "не снял хук перед записью: "+err.Error())
			}
		}
		return
	}
	before := map[string]magitrickle.Group{}
	for _, g := range groups {
		before[g.ID] = g
	}
	draft := make([]magitrickle.Group, len(groups))
	for i, g := range groups {
		g.Rules = append([]magitrickle.Rule(nil), g.Rules...)
		draft[i] = g
	}
	mutate(draft)
	after := map[string]magitrickle.Group{}
	for _, g := range draft {
		after[g.ID] = g
	}
	for _, c := range casc {
		was, okBefore := before[c.Group]
		now, okAfter := after[c.Group]
		if !okBefore {
			continue
		}
		if !okAfter || (was.Enable && !now.Enable) || was.Interface != now.Interface {
			if err := m.HookRemove(c.Group); err != nil {
				m.log("cascade", "hook", "не снял хук перед записью: "+err.Error())
			}
		}
	}
}

func (m *Manager) AfterMutate() {
	m.SyncHooks()
}

func (m *Manager) SyncHooks() {
	m.mu.Lock()
	defer m.mu.Unlock()
	casc := m.Store.Cascades()
	if len(casc) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	groups, err := m.MT.GroupsWithRules(ctx)
	if err != nil {
		return
	}
	enabled := map[string]bool{}
	for _, g := range groups {
		enabled[g.ID] = g.Enable
	}
	for _, c := range casc {
		if enabled[c.Group] {
			if err := m.HookAdd(c.Group); err != nil {
				m.log("cascade", "hook", "не поставил хук: "+err.Error())
			}
		} else if err := m.HookRemove(c.Group); err != nil {
			m.log("cascade", "hook", "не снял хук: "+err.Error())
		}
	}
}

// FlushConns чистит conntrack: залипший connmark тащит поток в мёртвый путь.
func (m *Manager) FlushConns(hosts []string) {
	if _, err := exec.LookPath("conntrack"); err != nil {
		return
	}
	for _, h := range hosts {
		if net.ParseIP(h) == nil {
			continue
		}
		cmd := exec.Command("conntrack", "-D", "-p", "udp", "-d", h)
		cmd.Run()
	}
	m.log("cascade", "conntrack", "сброшены соединения каскадируемых эндпоинтов")
}

func parseSource(ref string) (kind, name string, err error) {
	parts := strings.SplitN(ref, ":", 2)
	if len(parts) != 2 || parts[1] == "" || (parts[0] != "pool" && parts[0] != "iface") {
		return "", "", fmt.Errorf("неверный адрес источника %q", ref)
	}
	return parts[0], parts[1], nil
}

func HostOf(endpoint string) string {
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		return strings.TrimSpace(endpoint)
	}
	return host
}

func normalizeHost(h string) string {
	h = strings.TrimSpace(h)
	if h == "" || net.ParseIP(h) != nil {
		return h
	}
	if _, _, err := net.ParseCIDR(h); err == nil {
		return h
	}
	if host, port, err := net.SplitHostPort(h); err == nil && port != "" {
		return strings.ToLower(host)
	}
	return strings.ToLower(h)
}

// BuildRules: домены дублируются A-записями - трафик туннеля идёт мимо DNS.
func BuildRules(hosts []string, resolutions map[string][]string) []magitrickle.Rule {
	seen := map[string]bool{}
	var rules []magitrickle.Rule
	add := func(t, v string) {
		key := t + " " + v
		if seen[key] {
			return
		}
		seen[key] = true
		rules = append(rules, magitrickle.Rule{Type: t, Rule: v, Enable: true})
	}
	for _, raw := range hosts {
		h := normalizeHost(raw)
		if h == "" {
			continue
		}
		if net.ParseIP(h) != nil {
			add("subnet", h+"/32")
			continue
		}
		if _, _, err := net.ParseCIDR(h); err == nil {
			add("subnet", h)
			continue
		}
		add("domain", h)
		for _, ip := range resolutions[h] {
			add("subnet", ip+"/32")
		}
	}
	return rules
}

func (m *Manager) resolve(ctx context.Context, host string) []string {
	rctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupHost(rctx, host)
	if err != nil {
		return nil
	}
	var ips []string
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil && ip.To4() != nil {
			ips = append(ips, ip.String())
		}
	}
	return ips
}

func (m *Manager) poolEndpoints(pool store.Pool) (hosts, domains []string) {
	seen := map[string]bool{}
	for _, c := range pool.Configs {
		h := HostOf(c.Endpoint)
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		if net.ParseIP(h) == nil {
			domains = append(domains, strings.ToLower(h))
		} else {
			hosts = append(hosts, h)
		}
	}
	return hosts, domains
}

func viaDevice(m *Manager, via string) (device string, title string, err error) {
	kind, name, err := parseSource(via)
	if err != nil {
		return "", "", err
	}
	if kind == "iface" {
		return name, name, nil
	}
	p, ok := m.Store.Pool(name)
	if !ok {
		return "", "", fmt.Errorf("пул %q не найден", name)
	}
	return p.DeviceName(), name, nil
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
		if len(hosts) == 0 {
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
	if err := m.guardLoop(source, via, true); err != nil {
		return magitrickle.Group{}, err
	}
	g := magitrickle.Group{Name: name, Color: serviceColor, Interface: device, Enable: true, Rules: rules}
	if err := m.MT.EnsureHealthy(ctx); err != nil {
		return magitrickle.Group{}, err
	}
	if err := m.MT.AddGroup(ctx, g); err != nil {
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
	if err := m.HookRemove(groupID); err != nil {
		return err
	}
	if err := m.MT.EnsureHealthy(ctx); err != nil {
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
	if err := m.MT.DeleteGroup(ctx, groupID); err != nil {
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
	if err := m.MT.EnsureHealthy(ctx); err != nil {
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
}

func (m *Manager) cascadeHosts(ctx context.Context, entry store.CascadeEntry) []string {
	var hosts []string
	if kind, name, err := parseSource(entry.Source); err == nil && kind == "pool" {
		if p, ok := m.Store.Pool(name); ok {
			h, d := m.poolEndpoints(p)
			hosts = append(hosts, h...)
			for _, dom := range d {
				hosts = append(hosts, m.resolve(ctx, dom)...)
			}
		}
	}
	hosts = append(hosts, entry.Domains...)
	return hosts
}

func (m *Manager) SyncSourceEndpoints(ctx context.Context, source string) {
	for _, entry := range m.Store.Cascades() {
		if entry.Source != source {
			continue
		}
		kind, name, err := parseSource(source)
		if err != nil || kind != "pool" {
			continue
		}
		p, ok := m.Store.Pool(name)
		if !ok {
			continue
		}
		poolHosts, poolDomains := m.poolEndpoints(p)
		m.syncRules(ctx, entry, poolHosts, poolDomains)
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

func (m *Manager) syncRules(ctx context.Context, entry store.CascadeEntry, hosts, domains []string) {
	if len(hosts) == 0 && len(domains) == 0 {
		return
	}
	need := BuildRules(hosts, nil)
	_ = domains
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
	var added []magitrickle.Rule
	for _, r := range need {
		if !existing[r.Type+" "+r.Rule] {
			added = append(added, r)
		}
	}
	if len(added) == 0 {
		return
	}
	err = m.MT.MutateGroups(ctx, func(groups []magitrickle.Group) bool {
		for i := range groups {
			if groups[i].ID != entry.Group {
				continue
			}
			groups[i].Rules = append(groups[i].Rules, added...)
			return true
		}
		return false
	})
	if err != nil {
		m.log("cascade", "sync", "не добавил адреса в "+entry.Name+": "+err.Error())
		return
	}
	m.Store.LogEvent("cascade", "sync", entry.Name+": добавлено адресов "+strconv.Itoa(len(added)))
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
