package store

import (
	"encoding/json"
	"errors"
	"path/filepath"

	"mawg/internal/wgconf"
)

type FreeInstallation struct {
	UUID        string          `json:"installationUUID"`
	Private     string          `json:"privateKey"`
	UserCountry string          `json:"userCountry,omitempty"`
	Auth        json.RawMessage `json:"authData,omitempty"`
	Config      []byte          `json:"config,omitempty"`
}

func (s *Store) Free() FreeInstallation {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.free
	v.Auth = append(json.RawMessage(nil), v.Auth...)
	v.Config = append([]byte(nil), v.Config...)
	return v
}

func (s *Store) SaveFree(v FreeInstallation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.saveLocked(filepath.Join(s.base, "free.json"), v); err != nil {
		return errors.New("could not persist Amnezia Free installation")
	}
	s.free = v
	return nil
}

func (s *Store) CreateFreePool(name string, settings PoolSettings, cfg wgconf.NamedConfig) (Pool, error) {
	return s.createPool(name, settings, &cfg)
}
