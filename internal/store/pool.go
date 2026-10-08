package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (p Pool) EligibleConfigs() []ManagedConfig {
	var out []ManagedConfig
	for _, c := range p.Configs {
		if c.Enabled {
			out = append(out, c)
		}
	}
	return out
}

func (p Pool) ConfigByFile(file string) (ManagedConfig, bool) {
	for _, c := range p.Configs {
		if c.File == file {
			return c, true
		}
	}
	return ManagedConfig{}, false
}

func (p Pool) IndexByFile(file string) int {
	for i, c := range p.Configs {
		if c.File == file {
			return i
		}
	}
	return -1
}

func (p Pool) DeviceName() string {
	if p.Settings.EngineMode == "singbox" && p.Settings.TunName != "" {
		return p.Settings.TunName
	}
	if p.Settings.Platform == PlatformKeenetic && p.Settings.KeeneticSlot != "" {
		return "nwg" + strings.TrimPrefix(strings.ToLower(p.Settings.KeeneticSlot), "wireguard")
	}
	return p.Name
}

// PoolSnapshot - снимок каталога пула перед заменой конфигов
// автообновлением источника: файлы + реестр + активный конфиг.
type PoolSnapshot struct {
	Dir        string
	Files      map[string][]byte
	Configs    []ManagedConfig
	ActiveFile string
}

// SnapshotPool снимает состояние каталога пула (файлы верхнего уровня и
// реестр конфигов); RestorePoolSnapshot возвращает ровно его - лишние
// файлы, появившиеся после снимка, удаляются.
func (s *Store) SnapshotPool(pool string) (*PoolSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.poolLocked(pool)
	if !ok {
		return nil, fmt.Errorf("пул %q не найден", pool)
	}
	snap := &PoolSnapshot{Dir: s.PoolDir(pool), Files: map[string][]byte{}, Configs: append([]ManagedConfig(nil), p.Configs...)}
	snap.ActiveFile = s.state.Pools[pool].ActiveFile
	entries, err := os.ReadDir(snap.Dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(snap.Dir, e.Name()))
		if err != nil {
			return nil, err
		}
		snap.Files[e.Name()] = data
	}
	return snap, nil
}

// RestorePoolSnapshot откатывает каталог и реестр к снимку.
func (s *Store) RestorePoolSnapshot(snap *PoolSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := filepath.Base(snap.Dir)
	pi := -1
	for i := range s.root.Pools {
		if s.root.Pools[i].Name == name {
			pi = i
			break
		}
	}
	if pi < 0 {
		return fmt.Errorf("пул %q не найден", name)
	}
	entries, _ := os.ReadDir(snap.Dir)
	for _, e := range entries {
		if !e.IsDir() {
			if _, keep := snap.Files[e.Name()]; !keep {
				_ = os.Remove(filepath.Join(snap.Dir, e.Name()))
			}
		}
	}
	for name, data := range snap.Files {
		if err := os.WriteFile(filepath.Join(snap.Dir, name), data, 0o600); err != nil {
			return err
		}
	}
	s.root.Pools[pi].Configs = append([]ManagedConfig(nil), snap.Configs...)
	if err := s.saveLocked(filepath.Join(s.base, "config.json"), s.root); err != nil {
		return err
	}
	if st := s.state.Pools[name]; st != nil && st.ActiveFile != snap.ActiveFile {
		st.ActiveFile = snap.ActiveFile
		return s.saveLocked(filepath.Join(s.base, "state.json"), &s.state)
	}
	return nil
}

func (s *Store) poolLocked(name string) (Pool, bool) {
	for i := range s.root.Pools {
		if s.root.Pools[i].Name == name {
			return s.root.Pools[i], true
		}
	}
	return Pool{}, false
}
