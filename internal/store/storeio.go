package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"mawg/internal/wgconf"
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

func (s *Store) Base() string { return s.base }

func (s *Store) PoolDir(name string) string { return filepath.Join(s.poolsDir(), name) }

// ConfigJSON - текущий config.json (настройки сервера: порт/доступ/RCI,
// пулы, каскады, наборы) одним JSON - для бэкапа настроек панели.
func (s *Store) ConfigJSON() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return json.MarshalIndent(s.root, "", "  ")
}

// writeAtomically - tmp+rename: полфайла после сбоя питания не бывает
func writeAtomically(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
