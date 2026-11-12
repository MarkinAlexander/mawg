package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

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
		// фоллбек "pool:имя" других пулов тоже следует за именем: иначе
		// после rename молча вёл в несуществующий пул (fallback уходил
		// direct с событием - семантика менялась без ведома владельца)
		if s.root.Pools[i].Settings.Fallback == FallbackPoolPrefix+oldName {
			s.root.Pools[i].Settings.Fallback = FallbackPoolPrefix + clean
		}
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
	// пути зонного DNS (зона -> пул-туннель), участники бандлов и реестр
	// привязок устройств - те же ссылки на имя пула
	for i := range s.root.Settings.DnsZones {
		if s.root.Settings.DnsZones[i].Pool == oldName {
			s.root.Settings.DnsZones[i].Pool = clean
		}
	}
	for bi := range s.root.Bundles {
		for j, m := range s.root.Bundles[bi].Members {
			if m == oldName {
				s.root.Bundles[bi].Members[j] = clean
			}
		}
	}
	for i := range s.root.Settings.DeviceBindings {
		if s.root.Settings.DeviceBindings[i].Pool == oldName {
			s.root.Settings.DeviceBindings[i].Pool = clean
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
