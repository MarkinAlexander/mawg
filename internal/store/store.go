package store

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"mawg/internal/wgconf"
)

type Store struct {
	mu            sync.Mutex
	base          string
	root          RootConfig
	state         StateFile
	log           []Event
	lastStateJSON string
	premium       PremiumSubscription
	free          FreeInstallation
	warp          []WarpDevice
}

const (
	eventsMemCap   = 500              // сколько событий живёт в памяти
	eventsMaxBytes = 256 << 10        // ротация дискового журнала при превышении
)

func Open(base string) (*Store, error) {
	s := &Store{base: base, state: StateFile{Pools: map[string]*PoolState{}}}
	for _, dir := range []string{base, s.poolsDir(), s.backupDir()} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
	}
	if err := s.load(filepath.Join(base, "config.json"), &s.root); err != nil {
		return nil, err
	}
	if err := s.load(filepath.Join(base, "state.json"), &s.state); err != nil {
		return nil, err
	}
	if err := s.load(filepath.Join(base, "premium.json"), &s.premium); err != nil {
		return nil, errors.New("не удалось загрузить подписку Premium")
	}
	if err := s.load(filepath.Join(base, "free.json"), &s.free); err != nil {
		return nil, errors.New("не удалось загрузить данные Amnezia Free")
	}
	if err := s.load(filepath.Join(base, "warp.json"), &s.warp); err != nil {
		return nil, errors.New("не удалось загрузить реестр WARP")
	}
	if s.state.Pools == nil {
		s.state.Pools = map[string]*PoolState{}
	}
	s.loadEvents()
	s.migrateBackupCompression()
	migratedAwg := false
	for i := range s.root.Pools {
		s.root.Pools[i].Settings = s.root.Pools[i].Settings.WithDefaults()
		if s.state.Pools[s.root.Pools[i].Name] == nil {
			s.state.Pools[s.root.Pools[i].Name] = NewPoolState()
		}
		// миграция: у конфигов, добавленных до появления поля Awg,
		// уровень обфускации считается один раз при старте
		for j := range s.root.Pools[i].Configs {
			if s.root.Pools[i].Configs[j].Awg != "" {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(s.PoolDir(s.root.Pools[i].Name), s.root.Pools[i].Configs[j].File))
			if err != nil {
				continue
			}
			s.root.Pools[i].Configs[j].Awg = wgconf.AwgLevelOf(raw)
			migratedAwg = true
		}
	}
	if migratedAwg {
		_ = s.saveLocked(filepath.Join(base, "config.json"), s.root)
	}
	s.root.Settings = s.root.Settings.WithDefaults()
	if data, err := json.Marshal(&s.state); err == nil {
		s.lastStateJSON = string(data)
	}
	return s, nil
}

func (s *Store) load(path string, v any) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func (s *Store) saveLocked(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Store) poolsDir() string  { return filepath.Join(s.base, "pools") }
func (s *Store) backupDir() string { return filepath.Join(s.base, "backups") }

// Журнал живёт в tmpfs (слово владельца 2026-10-17: флешку не
// изнашивать - журнал некритичен, теряется только при ребуте роутера,
// рестарт mawg переживает). Легаси-файлы на флеше (base/events.json
// и .old) читаются при старте один раз (апгрейд) и больше не пишутся.
// Суффикс от базового каталога: параллельные инстансы (и параллельные
// же тест-бинари go test) не должны смешивать события в один файл.
func (s *Store) eventsTmpPath() string {
	h := fnv.New64a()
	h.Write([]byte(s.base))
	return filepath.Join(os.TempDir(), fmt.Sprintf("mawg-events-%x.jsonl", h.Sum64()))
}

func (s *Store) eventsPath() string    { return filepath.Join(s.base, "events.json") }
func (s *Store) eventsOldPath() string { return s.eventsPath() + ".old" }

func (s *Store) Base() string { return s.base }

func (s *Store) PoolDir(name string) string { return filepath.Join(s.poolsDir(), name) }

func (s *Store) Settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.root.Settings
}

// SingboxMode - "own" (свой экземпляр движка, по умолчанию) | "shared"
// (общее ядро: фрагмент mawg-pools.json в чужой каталог + рестарт чужого сервиса)
func (s *Store) SingboxMode() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root.Settings.SingboxMode == "shared" {
		return "shared"
	}
	return "own"
}

func (s *Store) SetSingboxMode(mode string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if mode != "own" && mode != "shared" {
		return fmt.Errorf("неизвестный режим движка %q (own|shared)", mode)
	}
	s.root.Settings.SingboxMode = mode
	return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
}

// EngineDisabled - мастер-выключатель движка sing-box («Система → Ядра
// VPN»): выключен - mawg не поднимает ни свой процесс, ни фрагмент в
// чужом ядре, движковые пулы показывают «движок выключен».
func (s *Store) EngineDisabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.root.Settings.EngineDisabled
}

func (s *Store) SetEngineDisabled(v bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root.Settings.EngineDisabled == v {
		return nil
	}
	s.root.Settings.EngineDisabled = v
	return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
}

// CoreDisabled - выключено ли ядро по id ("singbox"|"mihomo"): у singbox
// исторический флаг EngineDisabled остаётся источником правды, прочие
// ядра живут в карте CoreDisabled (88-док: стоп/старт по ядру).
func (s *Store) CoreDisabled(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "singbox" {
		return s.root.Settings.EngineDisabled
	}
	return s.root.Settings.CoreDisabled[id]
}

func (s *Store) SetCoreDisabled(id string, v bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "singbox" {
		if s.root.Settings.EngineDisabled == v {
			return nil
		}
		s.root.Settings.EngineDisabled = v
		return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
	}
	if s.root.Settings.CoreDisabled[id] == v {
		return nil
	}
	if s.root.Settings.CoreDisabled == nil {
		s.root.Settings.CoreDisabled = map[string]bool{}
	}
	s.root.Settings.CoreDisabled[id] = v
	return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
}

// SetMTGlitchy пишет флаг «глючная сборка MT» только при смене значения -
// проба совместимости ходит при каждом старте, config.json не надо
// перезаписывать впустую.
func (s *Store) SetMTGlitchy(v bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root.Settings.MTGlitchy == v {
		return nil
	}
	s.root.Settings.MTGlitchy = v
	return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
}

func (s *Store) MTGlitchy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.root.Settings.MTGlitchy
}

func (s *Store) SetSettings(v Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.root.Settings = v.WithDefaults()
	return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
}

func (s *Store) IfaceModes() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	for _, e := range s.root.Settings.Ifaces {
		if e.Device != "" && (e.Mode == IfaceExternal || e.Mode == IfaceHidden) {
			out[e.Device] = e.Mode
		}
	}
	return out
}

func (s *Store) SetIfaceMode(device, mode string) error {
	if mode != IfaceExternal && mode != IfaceHidden {
		return fmt.Errorf("режим должен быть external (внешний) или hidden (скрытый)")
	}
	for _, p := range s.root.Pools {
		if p.DeviceName() == device {
			return fmt.Errorf("устройством %s управляет пул %q", device, p.Name)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.root.Settings.Ifaces {
		if s.root.Settings.Ifaces[i].Device == device {
			s.root.Settings.Ifaces[i].Mode = mode
			return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
		}
	}
	s.root.Settings.Ifaces = append(s.root.Settings.Ifaces, IfaceEntry{Device: device, Mode: mode})
	return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
}

// ServerSettings - сетевые настройки панели, применяются на старте демона.
func (s *Store) ServerSettings() (addr string, port int, allowed []IPAllow, authDisabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.root.Settings.WithDefaults()
	return st.ListenAddr, st.WebPort, append([]IPAllow(nil), st.AllowedIPs...), st.AuthDisabled
}

// AllowedIPList - включённые записи allowlist (для IPGate).
func (s *Store) AllowedIPList() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, e := range s.root.Settings.AllowedIPs {
		if e.On {
			out = append(out, e.Value)
		}
	}
	return out
}

// SetServerSettings валидирует и сохраняет адрес/порт/allowlist/авторизацию.
func (s *Store) SetServerSettings(addr string, port int, allowed []IPAllow, authDisabled bool) error {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		addr = "0.0.0.0"
	}
	if net.ParseIP(addr) == nil {
		return fmt.Errorf("%q не похож на IP-адрес", addr)
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("порт должен быть 1-65535")
	}
	seen := map[string]bool{}
	clean := make([]IPAllow, 0, len(allowed))
	for _, e := range allowed {
		c := strings.TrimSpace(e.Value)
		if c == "" || seen[c] {
			continue
		}
		if ip := net.ParseIP(c); ip == nil {
			if _, _, err := net.ParseCIDR(c); err != nil {
				return fmt.Errorf("%q не IP и не подсеть", c)
			}
		}
		seen[c] = true
		clean = append(clean, IPAllow{Value: c, On: e.On})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.root.Settings.ListenAddr = addr
	s.root.Settings.WebPort = port
	s.root.Settings.AllowedIPs = clean
	s.root.Settings.AuthDisabled = authDisabled
	return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
}

func (s *Store) WANProbe() ProbeConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.root.Settings.WANProbe.Normalized()
}

func (s *Store) WANProbeEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.root.Settings.WANProbe != nil && s.root.Settings.WANProbe.Target != ""
}

func (s *Store) SetWANProbe(p *ProbeConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p != nil {
		norm := p.Normalized()
		p = &norm
	}
	s.root.Settings.WANProbe = p
	return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
}

// DNSCascade - политика каскада DNS (nil = не задана).
func (s *Store) DNSCascade() *DnsCascadePolicy {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.root.Settings.DNSCascade
}

func (s *Store) SetDNSCascade(p *DnsCascadePolicy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.root.Settings.DNSCascade = p
	return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
}

// DNSProbeServers - свои серверы карточки «Проверка DNS и сети».
func (s *Store) DNSProbeServers() []DnsProbeServer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]DnsProbeServer(nil), s.root.Settings.DNSProbe...)
}

func (s *Store) SetDNSProbeServers(list []DnsProbeServer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if list == nil {
		list = []DnsProbeServer{}
	}
	s.root.Settings.DNSProbe = list
	return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
}

// DnsZones - таблица зонного DNS.
func (s *Store) DnsZones() []DnsZone {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]DnsZone(nil), s.root.Settings.DnsZones...)
}

func (s *Store) SetDnsZones(list []DnsZone) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if list == nil {
		list = []DnsZone{}
	}
	s.root.Settings.DnsZones = list
	return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
}
// Excludes - глобальный список исключений из обработки (94-док):
// каноничные IPv4/CIDR, трафик на них и с них не маркируется группами.
func (s *Store) Excludes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.root.Settings.Excludes...)
}

func (s *Store) SetExcludes(list []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if list == nil {
		list = []string{}
	}
	s.root.Settings.Excludes = list
	return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
}

// GroupPolicy - политика поведения группы при отвале интерфейса.
func (s *Store) GroupPolicy(id string) GroupPolicy {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.root.Settings.GroupPolicies[id]
}

func (s *Store) GroupPoliciesAll() map[string]GroupPolicy {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]GroupPolicy{}
	for id, p := range s.root.Settings.GroupPolicies {
		out[id] = p
	}
	return out
}

func (s *Store) SetGroupPolicy(id string, p *GroupPolicy) error {
	if p != nil && p.OnDead != "" && p.OnDead != "direct" && p.OnDead != "iface" {
		return fmt.Errorf("невалидный режим отвала %q", p.OnDead)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root.Settings.GroupPolicies == nil {
		s.root.Settings.GroupPolicies = map[string]GroupPolicy{}
	}
	// запись живёт, пока задан хоть один из разделов: отвал или источники
	if p == nil || (p.OnDead == "" && p.SourcesEmpty()) {
		delete(s.root.Settings.GroupPolicies, id)
	} else {
		s.root.Settings.GroupPolicies[id] = *p
	}
	if len(s.root.Settings.GroupPolicies) == 0 {
		s.root.Settings.GroupPolicies = nil
	}
	return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
}

func (s *Store) Cascades() []CascadeEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]CascadeEntry, len(s.root.Settings.Cascades))
	copy(out, s.root.Settings.Cascades)
	return out
}

func (s *Store) CascadeByGroup(group string) (CascadeEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.root.Settings.Cascades {
		if c.Group == group {
			return c, true
		}
	}
	return CascadeEntry{}, false
}

func (s *Store) SetCascades(c []CascadeEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.root.Settings.Cascades = c
	return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
}

// DegradedGroups - группы, у которых политика переписала интерфейс из-за
// отвала основного, плюс группы с отложенным direct при мёртвом WAN.
func (s *Store) MutateDegraded(fn func(map[string]DegradedGroup)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.DegradedGroups == nil {
		s.state.DegradedGroups = map[string]DegradedGroup{}
	}
	fn(s.state.DegradedGroups)
	data, err := json.Marshal(&s.state)
	if err != nil {
		return err
	}
	if string(data) == s.lastStateJSON {
		return nil
	}
	if err := s.saveLocked(filepath.Join(s.base, "state.json"), &s.state); err != nil {
		return err
	}
	s.lastStateJSON = string(data)
	return nil
}

func (s *Store) DegradedGroups() map[string]DegradedGroup {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]DegradedGroup{}
	for k, v := range s.state.DegradedGroups {
		out[k] = v
	}
	return out
}

// RCIToken - токен локального API Keenetic 5.2+ (X-NDMA-TKN), на 5.1 и
// старее не нужен.
func (s *Store) RCIToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.root.Settings.RCIToken
}

func (s *Store) SetRCIToken(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.root.Settings.RCIToken = strings.TrimSpace(token)
	return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
}

func (s *Store) IfaceProbe(device string) *ProbeConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.root.Settings.Ifaces {
		if e.Device == device {
			return e.Probe
		}
	}
	return nil
}

// ProbedExternals - внешние интерфейсы с настроенной пробой: их статусы
// движок держит в кэше для мгновенной отдачи в /ifaces.
func (s *Store) ProbedExternals() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, e := range s.root.Settings.Ifaces {
		if e.Device != "" && e.Mode == IfaceExternal && e.Probe != nil {
			out = append(out, e.Device)
		}
	}
	return out
}

func (s *Store) SetIfaceProbe(device string, p *ProbeConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p != nil {
		norm := p.Normalized()
		p = &norm
	}
	for i := range s.root.Settings.Ifaces {
		if s.root.Settings.Ifaces[i].Device == device {
			s.root.Settings.Ifaces[i].Probe = p
			return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
		}
	}
	return fmt.Errorf("интерфейс %s не найден в реестре", device)
}

func (s *Store) RenamePool(oldName, newName string) (Pool, error) {
	clean, err := wgconf.SanitizePoolName(newName)
	if err != nil {
		return Pool{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if clean == oldName {
		for _, p := range s.root.Pools {
			if p.Name == oldName {
				return p, nil
			}
		}
		return Pool{}, fmt.Errorf("пул %q не найден", oldName)
	}
	for _, p := range s.root.Pools {
		if p.Name == clean {
			return Pool{}, fmt.Errorf("пул %q уже существует", clean)
		}
	}
	found := false
	for i := range s.root.Pools {
		if s.root.Pools[i].Name == oldName {
			s.root.Pools[i].Name = clean
			found = true
			break
		}
	}
	if !found {
		return Pool{}, fmt.Errorf("пул %q не найден", oldName)
	}
	// ссылки на пул в каскадах (Source/Via "pool:имя") и Via умных
	// каскадов других пулов следуют за новым именем - иначе sync не
	// найдёт источник и каскад застынет со старым набором правил
	for i := range s.root.Settings.Cascades {
		if s.root.Settings.Cascades[i].Source == "pool:"+oldName {
			s.root.Settings.Cascades[i].Source = "pool:" + clean
		}
		if s.root.Settings.Cascades[i].Via == "pool:"+oldName {
			s.root.Settings.Cascades[i].Via = "pool:" + clean
		}
	}
	for i := range s.root.Pools {
		sc := s.root.Pools[i].Settings.SmartCascade
		if sc == nil {
			continue
		}
		for j, v := range sc.Via {
			if v == oldName {
				sc.Via[j] = clean
			}
		}
	}
	if st := s.state.Pools[oldName]; st != nil {
		delete(s.state.Pools, oldName)
		s.state.Pools[clean] = st
	}
	if err := os.Rename(s.PoolDir(oldName), s.PoolDir(clean)); err != nil && !os.IsNotExist(err) {
		return Pool{}, err
	}
	if err := s.saveLocked(filepath.Join(s.base, "config.json"), s.root); err != nil {
		return Pool{}, err
	}
	if err := s.saveLocked(filepath.Join(s.base, "state.json"), &s.state); err != nil {
		return Pool{}, err
	}
	for _, p := range s.root.Pools {
		if p.Name == clean {
			return p, nil
		}
	}
	return Pool{}, nil
}

func (s *Store) Pools() []Pool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Pool, len(s.root.Pools))
	copy(out, s.root.Pools)
	return out
}

func (s *Store) Pool(name string) (Pool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.root.Pools {
		if p.Name == name {
			return p, true
		}
	}
	return Pool{}, false
}

func (s *Store) State(name string) *PoolState {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.state.Pools[name]
	if st == nil {
		st = NewPoolState()
		s.state.Pools[name] = st
	}
	cp := *st
	cp.Cooldowns = make(map[string]int64, len(st.Cooldowns))
	for k, v := range st.Cooldowns {
		cp.Cooldowns[k] = v
	}
	return &cp
}

func (s *Store) CreatePool(name string, settings PoolSettings) (Pool, error) {
	return s.createPool(name, settings, nil)
}

func (s *Store) createPool(name string, settings PoolSettings, freeConfig *wgconf.NamedConfig) (Pool, error) {
	clean, err := wgconf.SanitizePoolName(name)
	if err != nil {
		return Pool{}, err
	}
	if !ValidProbeTarget(settings.WithDefaults().ProbeHost) {
		return Pool{}, fmt.Errorf("цель пробы должна быть публичным IPv4-адресом или http(s)-ссылкой, получено: %q", settings.ProbeHost)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.root.Pools {
		if freeConfig != nil && p.Free {
			return Pool{}, errors.New("Amnezia Free уже привязан к другому пулу")
		}
		if p.Name == clean {
			return Pool{}, fmt.Errorf("пул %q уже существует", clean)
		}
		if p.Settings.Platform == settings.Platform &&
			settings.Platform == PlatformKeenetic &&
			p.Settings.KeeneticSlot == settings.KeeneticSlot && settings.KeeneticSlot != "" {
			return Pool{}, fmt.Errorf("слот %s уже занят пулом %q", settings.KeeneticSlot, p.Name)
		}
		if settings.EngineMode == "singbox" && settings.TunName != "" &&
			p.Settings.EngineMode == "singbox" && p.Settings.TunName == settings.TunName {
			return Pool{}, fmt.Errorf("tun %s уже занят пулом %q", settings.TunName, p.Name)
		}
	}
	if err := os.MkdirAll(s.PoolDir(clean), 0o700); err != nil {
		return Pool{}, err
	}
	pool := Pool{Name: clean, Settings: settings.WithDefaults()}
	if freeConfig != nil {
		pool.Free = true
		pool.Disabled = true
		cfg := freeConfig.Config
		file := "amnezia-free.conf"
		path := filepath.Join(s.PoolDir(clean), file)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return Pool{}, errors.New("не удалось подготовить конфиг Amnezia Free; существующий файл сохранён")
		}
		_, writeErr := f.Write(freeConfig.Raw)
		closeErr := f.Close()
		if writeErr != nil || closeErr != nil {
			os.Remove(path)
			return Pool{}, errors.New("не удалось сохранить конфиг Amnezia Free")
		}
		pool.Configs = []ManagedConfig{{File: file, Original: "Amnezia Free", Endpoint: cfg.Endpoint(), PublicKey: cfg.Peer.PublicKey, Addresses: cfg.Addresses, Enabled: true, Awg: freeConfig.Config.AwgLevel()}}
	}
	root := s.root
	root.Pools = append(append([]Pool{}, s.root.Pools...), pool)
	if err := s.saveLocked(filepath.Join(s.base, "config.json"), root); err != nil {
		if freeConfig != nil {
			os.Remove(filepath.Join(s.PoolDir(clean), "amnezia-free.conf"))
		}
		return Pool{}, err
	}
	s.root = root
	s.state.Pools[clean] = NewPoolState()
	return pool, nil
}

func (s *Store) UpdatePool(name string, settings PoolSettings) error {
	if !ValidProbeTarget(settings.WithDefaults().ProbeHost) {
		return fmt.Errorf("цель пробы должна быть публичным IPv4-адресом или http(s)-ссылкой, получено: %q", settings.ProbeHost)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.root.Pools {
		if s.root.Pools[i].Name == name {
			p := s.root.Pools[i]
			if p.Premium {
				if settings.Source != "" {
					return errors.New("Premium работает с нативным AWG-пулом без статического источника")
				}
				// движковый режим разрешён: Keenetic-слот (AWG 2.0) конфиги
				// AWG 3.x от gateway не поднимает, им нужен движок sing-box-lx
				if settings.EngineMode != "" && settings.EngineMode != "singbox" {
					return errors.New("Premium работает с нативным AWG-пулом или движком sing-box")
				}
				if settings.EngineMode == "" && settings.Platform == PlatformOpenwrt && settings.OpenwrtProto != "amneziawg" {
					return errors.New("для Premium нужен пул с протоколом amneziawg")
				}
			}
			s.root.Pools[i].Settings = settings.WithDefaults()
			return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
		}
	}
	return fmt.Errorf("пул %q не найден", name)
}

func (s *Store) DeletePool(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, p := range s.root.Pools {
		if p.Name == name {
			s.root.Pools = append(s.root.Pools[:i], s.root.Pools[i+1:]...)
			delete(s.state.Pools, name)
			if err := s.saveLocked(filepath.Join(s.base, "config.json"), s.root); err != nil {
				return err
			}
			if err := s.saveLocked(filepath.Join(s.base, "state.json"), &s.state); err != nil {
				return err
			}
			return os.RemoveAll(s.PoolDir(name))
		}
	}
	return fmt.Errorf("пул %q не найден", name)
}

func (s *Store) AddConfigs(pool string, configs []wgconf.NamedConfig) (added int, duplicates []string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pi := -1
	for i := range s.root.Pools {
		if s.root.Pools[i].Name == pool {
			pi = i
			break
		}
	}
	if pi < 0 {
		return 0, nil, fmt.Errorf("пул %q не найден", pool)
	}
	if s.root.Pools[pi].Premium {
		return 0, nil, errors.New("пул Premium меняет страну, статические конфиги ему не нужны")
	}
	dir := s.PoolDir(pool)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, nil, err
	}
	taken := map[string]bool{}
	ids := map[string]bool{}
	for _, c := range s.root.Pools[pi].Configs {
		taken[strings.TrimSuffix(c.File, ".conf")] = true
		ids[c.PublicKey+"|"+c.Endpoint] = true
	}
	for _, nc := range configs {
		if ids[nc.Config.Peer.PublicKey+"|"+nc.Config.Endpoint()] {
			duplicates = append(duplicates, nc.OriginalName)
			continue
		}
		slug := wgconf.UniqueSlug(wgconf.Slug(nc.OriginalName), func(x string) bool { return taken[x] })
		taken[slug] = true
		ids[nc.Config.Peer.PublicKey+"|"+nc.Config.Endpoint()] = true
		file := slug + ".conf"
		if err := os.WriteFile(filepath.Join(dir, file), nc.Raw, 0o600); err != nil {
			return added, duplicates, err
		}
		s.root.Pools[pi].Configs = append(s.root.Pools[pi].Configs, ManagedConfig{
			File:      file,
			Original:  nc.OriginalName,
			Endpoint:  nc.Config.Endpoint(),
			PublicKey: nc.Config.Peer.PublicKey,
			Enabled:   true,
			Addresses: nc.Config.Addresses,
			Awg:       nc.Config.AwgLevel(),
		})
		added++
	}
	if added > 0 {
		if err := s.saveLocked(filepath.Join(s.base, "config.json"), s.root); err != nil {
			return added, duplicates, err
		}
	}
	return added, duplicates, nil
}

// SetConfigAddresses сохраняет внутренние адреса конфига в реестр: потом
// коллизии проверяются без чтения файлов.
func (s *Store) SetConfigAddresses(pool, file string, addrs []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.root.Pools {
		if s.root.Pools[i].Name != pool {
			continue
		}
		for j := range s.root.Pools[i].Configs {
			if s.root.Pools[i].Configs[j].File == file && len(s.root.Pools[i].Configs[j].Addresses) == 0 {
				s.root.Pools[i].Configs[j].Addresses = addrs
				_ = s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
				return
			}
		}
	}
}

func (s *Store) LoadConfigFile(pool, file string) (wgconf.Config, error) {
	if filepath.Base(file) != file || !strings.HasSuffix(file, ".conf") {
		return wgconf.Config{}, fmt.Errorf("недопустимое имя файла конфига: %q", file)
	}
	data, err := os.ReadFile(filepath.Join(s.PoolDir(pool), file))
	if err != nil {
		return wgconf.Config{}, err
	}
	return wgconf.Parse(data)
}

func (s *Store) RemoveConfig(pool, file string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.root.Pools {
		if s.root.Pools[i].Name != pool {
			continue
		}
		for j, c := range s.root.Pools[i].Configs {
			if c.File == file {
				if s.root.Pools[i].Premium {
					return errors.New("активный конфиг Premium нельзя удалить по отдельности - удалите пул целиком")
				}
				s.root.Pools[i].Configs = append(s.root.Pools[i].Configs[:j], s.root.Pools[i].Configs[j+1:]...)
				if err := s.saveLocked(filepath.Join(s.base, "config.json"), s.root); err != nil {
					return err
				}
				return os.Remove(filepath.Join(s.PoolDir(pool), file))
			}
		}
		return fmt.Errorf("конфиг %q не найден в пуле %q", file, pool)
	}
	return fmt.Errorf("пул %q не найден", pool)
}

func (s *Store) SetConfigEnabled(pool, file string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.root.Pools {
		if s.root.Pools[i].Name != pool {
			continue
		}
		for j := range s.root.Pools[i].Configs {
			if s.root.Pools[i].Configs[j].File == file {
				s.root.Pools[i].Configs[j].Enabled = enabled
				return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
			}
		}
		return fmt.Errorf("конфиг %q не найден", file)
	}
	return fmt.Errorf("пул %q не найден", pool)
}

func (s *Store) MoveConfig(pool, file string, delta int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.root.Pools {
		if s.root.Pools[i].Name != pool {
			continue
		}
		cfgs := s.root.Pools[i].Configs
		for j := range cfgs {
			if cfgs[j].File != file {
				continue
			}
			k := j + delta
			if k < 0 || k >= len(cfgs) {
				return fmt.Errorf("конфиг %q уже крайний в списке", file)
			}
			cfgs[j], cfgs[k] = cfgs[k], cfgs[j]
			if err := s.saveLocked(filepath.Join(s.base, "config.json"), s.root); err != nil {
				return err
			}
			return nil
		}
		return fmt.Errorf("конфиг %q не найден", file)
	}
	return fmt.Errorf("пул %q не найден", pool)
}

func (s *Store) SetPoolDisabled(pool string, disabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.root.Pools {
		if s.root.Pools[i].Name == pool {
			s.root.Pools[i].Disabled = disabled
			return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
		}
	}
	return fmt.Errorf("пул %q не найден", pool)
}

func (s *Store) MutateState(pool string, fn func(*PoolState)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.state.Pools[pool]
	if st == nil {
		st = NewPoolState()
		s.state.Pools[pool] = st
	}
	fn(st)
	data, err := json.Marshal(&s.state)
	if err != nil {
		return err
	}
	if string(data) == s.lastStateJSON {
		return nil
	}
	if err := s.saveLocked(filepath.Join(s.base, "state.json"), &s.state); err != nil {
		return err
	}
	s.lastStateJSON = string(data)
	return nil
}

func (s *Store) Bundles() []Bundle {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Bundle, len(s.root.Bundles))
	copy(out, s.root.Bundles)
	return out
}

func (s *Store) Bundle(name string) (Bundle, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range s.root.Bundles {
		if b.Name == name {
			return b, true
		}
	}
	return Bundle{}, false
}

func (s *Store) CreateBundle(b Bundle) (Bundle, error) {
	clean, err := wgconf.SanitizeBundleName(b.Name)
	if err != nil {
		return Bundle{}, err
	}
	b.Name = clean
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.root.Bundles {
		if existing.Name == clean {
			return Bundle{}, fmt.Errorf("набор %q уже существует", clean)
		}
	}
	s.root.Bundles = append(s.root.Bundles, b)
	if s.state.Bundles == nil {
		s.state.Bundles = map[string]*BundleState{}
	}
	s.state.Bundles[clean] = &BundleState{}
	if err := s.saveLocked(filepath.Join(s.base, "config.json"), s.root); err != nil {
		return Bundle{}, err
	}
	return b, nil
}

func (s *Store) UpdateBundle(name string, b Bundle) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.root.Bundles {
		if s.root.Bundles[i].Name == name {
			b.Name = name
			s.root.Bundles[i] = b
			return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
		}
	}
	return fmt.Errorf("набор %q не найден", name)
}

func (s *Store) DeleteBundle(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.root.Bundles {
		if s.root.Bundles[i].Name == name {
			s.root.Bundles = append(s.root.Bundles[:i], s.root.Bundles[i+1:]...)
			delete(s.state.Bundles, name)
			if err := s.saveLocked(filepath.Join(s.base, "config.json"), s.root); err != nil {
				return err
			}
			return s.saveLocked(filepath.Join(s.base, "state.json"), &s.state)
		}
	}
	return fmt.Errorf("набор %q не найден", name)
}

func (s *Store) BundleState(name string) BundleState {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.state.Bundles[name]
	if st == nil {
		return BundleState{}
	}
	return *st
}

func (s *Store) MutateBundleState(name string, fn func(*BundleState)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Bundles == nil {
		s.state.Bundles = map[string]*BundleState{}
	}
	st := s.state.Bundles[name]
	if st == nil {
		st = &BundleState{}
		s.state.Bundles[name] = st
	}
	fn(st)
	data, err := json.Marshal(&s.state)
	if err != nil {
		return err
	}
	if string(data) == s.lastStateJSON {
		return nil
	}
	if err := s.saveLocked(filepath.Join(s.base, "state.json"), &s.state); err != nil {
		return err
	}
	s.lastStateJSON = string(data)
	return nil
}

func (s *Store) LogEvent(pool, kind, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ev := Event{Time: time.Now(), Pool: pool, Kind: kind, Message: message}
	s.log = append(s.log, ev)
	if len(s.log) > eventsMemCap {
		s.log = s.log[len(s.log)-eventsMemCap:]
	}
	s.appendEventLocked(ev)
}

// appendEventLocked пишет событие в дисковый журнал (JSONL, append).
// События больше не умирают при рестарте mawg: при старте хвост читается
// обратно (loadEvents). Ошибки диска игнорируются - журнал не должен
// ронять работу; при переполнении файл ротируется в .old (одна копия
// истории, старшая теряется).
func (s *Store) appendEventLocked(ev Event) {
	path := s.eventsTmpPath()
	if st, err := os.Stat(path); err == nil && st.Size() >= eventsMaxBytes {
		os.Rename(path, s.eventsOldPath())
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(line, '\n'))
}

func (s *Store) loadEvents() {
	var evs []Event
	var legacyRead []string
	// журнал до уникальных имён (фиксированный путь) - хвост подхватить
	// один раз и снять, чтобы не висел мёртвым грузом
	legacyTmp := filepath.Join(os.TempDir(), "mawg-events.jsonl")
	for _, path := range []string{s.eventsTmpPath() + ".old", s.eventsTmpPath(), legacyTmp, s.eventsOldPath(), s.eventsPath()} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if strings.HasPrefix(path, s.base) || path == legacyTmp {
			// легаси-файлы: после чтения снимаем (слово владельца
			// 2026-10-17: мёртвый груз после апгрейда не должен висеть)
			legacyRead = append(legacyRead, path)
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var ev Event
			if json.Unmarshal([]byte(line), &ev) == nil {
				evs = append(evs, ev)
			}
		}
	}
	if len(evs) > eventsMemCap {
		evs = evs[len(evs)-eventsMemCap:]
	}
	s.log = evs
	for _, path := range legacyRead {
		_ = os.Remove(path)
	}
}

func (s *Store) Events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, len(s.log))
	copy(out, s.log)
	return out
}

// ConfigJSON - текущий config.json (настройки сервера: порт/доступ/RCI,
// пулы, каскады, наборы) одним JSON - для бэкапа настроек панели.
func (s *Store) ConfigJSON() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return json.MarshalIndent(s.root, "", "  ")
}

func (s *Store) SaveBackup(name, content string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveBackupLocked(name, content)
}

func (s *Store) saveBackupLocked(name, content string) (string, error) {
	return s.saveBackupLockedExt(name, "conf", content)
}

func (s *Store) saveBackupLockedExt(name, ext, content string) (string, error) {
	if err := os.MkdirAll(s.backupDir(), 0o700); err != nil {
		return "", err
	}
	// частичные бэкапы (.conf.gz) хранятся сжатыми: ~100КБ shadow
	// превращаются в ~10-15КБ (слово владельца: «сделаем сжатие и
	// хранить в сжатом виде»). tar.gz уже сжат - второй раз не греем
	if ext == "conf.gz" {
		if gz, err := gzipBytes([]byte(content)); err == nil {
			content = string(gz)
		}
	}
	base := fmt.Sprintf("%s-%s", name, time.Now().Format("20060102-150405"))
	path := filepath.Join(s.backupDir(), base+"."+ext)
	// две записи в одну секунду: не перетираем первую копию - суффикс
	if _, err := os.Stat(path); err == nil {
		for i := 1; ; i++ {
			p := filepath.Join(s.backupDir(), fmt.Sprintf("%s-%02d.%s", base, i, ext))
			if _, err := os.Stat(p); os.IsNotExist(err) {
				path = p
				break
			}
		}
	}
	return path, os.WriteFile(path, []byte(content), 0o600)
}

func gzipBytes(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// BackupKeep - сколько последних копий каждого вида хранить (настройка
// вкладки «Резервные копии», 2..10).
func (s *Store) BackupKeep() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root.Settings.BackupKeep <= 0 {
		return 10
	}
	return s.root.Settings.BackupKeep
}

// SaveBackupRotated пишет бэкап и подчищает старые копии того же имени,
// оставляя столько самых свежих, сколько задано настройкой BackupKeep
// (формат имени name-YYYYMMDD-HHMMSS.ext). Авто-бэкапы перед массовыми
// записями могут сыпаться часто - без ротации каталог распухает на флеше
// роутера.
func (s *Store) SaveBackupRotated(name, ext, content string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.saveBackupLockedExt(name, ext, content)
	if err != nil {
		return "", err
	}
	s.rotateBackupsLocked(name, s.root.Settings.BackupKeep)
	return path, nil
}

// EnforceBackupRetention - немедленная чистка старых копий по настройке:
// вызывается PUT /mt/backup-keep, эффект виден сразу, а не при
// следующем бэкапе.
func (s *Store) EnforceBackupRetention() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, kind := range []string{"mt-shadow", "mt-config", "mawg-full"} {
		s.rotateBackupsLocked(kind, s.root.Settings.BackupKeep)
	}
}

// rotateBackupsLocked - оставить keep самых свежих файлов с префиксом
// name- в любом формате (переход .conf -> .conf.gz считает обе серии
// вместе). Вызов под s.mu.
func (s *Store) rotateBackupsLocked(name string, keep int) {
	if keep <= 0 {
		keep = 10
	}
	entries, err := os.ReadDir(s.backupDir())
	if err != nil {
		return
	}
	prefix := name + "-"
	var files []string
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, prefix) {
			files = append(files, n)
		}
	}
	// свежее = лексически больше (формат метки времени сортируется сам)
	sort.Strings(files)
	for i, j := 0, len(files)-1; i < j; i, j = i+1, j-1 {
		files[i], files[j] = files[j], files[i]
	}
	if len(files) > keep {
		for _, n := range files[keep:] {
			os.Remove(filepath.Join(s.backupDir(), n))
		}
	}
}

// migrateBackupCompression - старые несжатые бэкапы (*.conf) упаковывает
// в gzip (*.conf.gz): tar.gz уже сжат и не матчится, чужие файлы не
// трогаем. Идемпотентно; mtime сохраняем, чтобы порядок в панели не
// уехал. Запускается при старте mawg (слово владельца: «чтобы mawg
// автоматом их упаковал и сжал и подготовил»).
func (s *Store) migrateBackupCompression() {
	entries, err := os.ReadDir(s.backupDir())
	if err != nil {
		return
	}
	n := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".conf") {
			continue
		}
		path := filepath.Join(s.backupDir(), name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		gz, err := gzipBytes(data)
		if err != nil {
			continue
		}
		dst := path + ".gz"
		if err := writeAtomically(dst, gz); err != nil {
			continue
		}
		if st, err := os.Stat(path); err == nil {
			_ = os.Chtimes(dst, st.ModTime(), st.ModTime())
		}
		if err := os.Remove(path); err != nil {
			continue
		}
		n++
	}
	if n > 0 {
		s.LogEvent("backup", "migrate", fmt.Sprintf("старые бэкапы упакованы в gzip: %d", n))
	}
}

// writeAtomically - tmp+rename: полфайла после сбоя питания не бывает
func writeAtomically(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
