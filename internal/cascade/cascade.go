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
	iptRun   func(args ...string) (string, error) // подменяется в тестах
}

// retry применяет самолечение, если оно подключено, иначе прямую запись.
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
	casc := m.Store.Cascades()
	routerPolicies := m.routerHookPolicies()
	if len(casc) == 0 && len(routerPolicies) == 0 {
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
}

// routerHookPolicies - политики групп, которым нужен OUTPUT-хук.
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

// viaEndpointIPs - фактические адреса туннеля via из конфигов пула
// (домены резолвятся с коротким таймаутом: SyncHooks ходит каждые 30с,
// мёртвый DNS не должен тормозить цикл).
func (m *Manager) viaEndpointIPs(via string) []string {
	kind, name, err := parseSource(via)
	if err != nil || kind != "pool" {
		return nil
	}
	p, ok := m.Store.Pool(name)
	if !ok {
		return nil
	}
	hosts, domains := m.poolEndpoints(p)
	ips := append([]string(nil), hosts...)
	for _, d := range domains {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		ips = append(ips, m.resolve(ctx, d)...)
		cancel()
	}
	return ips
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

// IfaceEndpoints - адреса пиров роутерного WG-интерфейса для префилла
// диалога каскадов; source = "iface:<device>".
func (m *Manager) IfaceEndpoints(source string) ([]string, error) {
	kind, name, err := parseSource(source)
	if err != nil {
		return nil, err
	}
	if kind != "iface" {
		return nil, fmt.Errorf("автоподстановка - только для интерфейса-источника")
	}
	if m.EP == nil {
		return nil, fmt.Errorf("платформа не умеет определять эндпоинты интерфейсов - перечислите вручную")
	}
	return m.EP.IfaceEndpoints(name)
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

func (m *Manager) SyncSourceEndpoints(ctx context.Context, source string) {
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
				continue
			}
			poolHosts, poolDomains := m.poolEndpoints(p)
			m.syncRules(ctx, entry, poolHosts, poolDomains)
			continue
		}
		if kind == "iface" && m.EP != nil {
			if resolved, epErr := m.EP.IfaceEndpoints(name); epErr == nil && len(resolved) > 0 {
				m.syncRules(ctx, entry, resolved, nil)
			}
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
	err = m.retry("cascade", func() error {
		return m.MT.MutateGroups(ctx, func(groups []magitrickle.Group) bool {
			for i := range groups {
				if groups[i].ID != entry.Group {
					continue
				}
				groups[i].Rules = append(groups[i].Rules, added...)
				return true
			}
			return false
		})
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
