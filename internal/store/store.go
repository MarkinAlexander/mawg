package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
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
}

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
	if s.state.Pools == nil {
		s.state.Pools = map[string]*PoolState{}
	}
	for i := range s.root.Pools {
		s.root.Pools[i].Settings = s.root.Pools[i].Settings.WithDefaults()
		if s.state.Pools[s.root.Pools[i].Name] == nil {
			s.state.Pools[s.root.Pools[i].Name] = NewPoolState()
		}
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

func (s *Store) PoolDir(name string) string { return filepath.Join(s.poolsDir(), name) }

func (s *Store) Settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.root.Settings
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root.Settings.GroupPolicies == nil {
		s.root.Settings.GroupPolicies = map[string]GroupPolicy{}
	}
	if p == nil || p.OnDead == "" || (p.OnDead != "direct" && p.OnDead != "iface") {
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
	s.lastStateJSON = string(data)
	return s.saveLocked(filepath.Join(s.base, "state.json"), &s.state)
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
		if p.Name == clean {
			return Pool{}, fmt.Errorf("пул %q уже существует", clean)
		}
		if p.Settings.Platform == settings.Platform &&
			settings.Platform == PlatformKeenetic &&
			p.Settings.KeeneticSlot == settings.KeeneticSlot && settings.KeeneticSlot != "" {
			return Pool{}, fmt.Errorf("слот %s уже занят пулом %q", settings.KeeneticSlot, p.Name)
		}
	}
	if err := os.MkdirAll(s.PoolDir(clean), 0o700); err != nil {
		return Pool{}, err
	}
	pool := Pool{Name: clean, Settings: settings.WithDefaults()}
	s.root.Pools = append(s.root.Pools, pool)
	s.state.Pools[clean] = NewPoolState()
	if err := s.saveLocked(filepath.Join(s.base, "config.json"), s.root); err != nil {
		return Pool{}, err
	}
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
	s.lastStateJSON = string(data)
	return s.saveLocked(filepath.Join(s.base, "state.json"), &s.state)
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
	s.lastStateJSON = string(data)
	return s.saveLocked(filepath.Join(s.base, "state.json"), &s.state)
}

func (s *Store) LogEvent(pool, kind, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log = append(s.log, Event{Time: time.Now(), Pool: pool, Kind: kind, Message: message})
	if len(s.log) > 500 {
		s.log = s.log[len(s.log)-500:]
	}
}

func (s *Store) Events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, len(s.log))
	copy(out, s.log)
	return out
}

func (s *Store) SaveBackup(name, content string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.backupDir(), 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(s.backupDir(), fmt.Sprintf("%s-%s.conf", name, time.Now().Format("20060102-150405")))
	return path, os.WriteFile(path, []byte(content), 0o600)
}
