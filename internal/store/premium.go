package store

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"mawg/internal/premium"
	"mawg/internal/wgconf"
)

type PremiumSubscription struct {
	Key            string            `json:"key"`
	UUID           string            `json:"installationUUID"`
	UserCountry    string            `json:"userCountry,omitempty"`
	Country        string            `json:"country,omitempty"`
	Countries      []premium.Country `json:"countries,omitempty"`
	PendingPrivate string            `json:"pendingPrivate,omitempty"`
	PendingCountry string            `json:"pendingCountry,omitempty"`
}

type PremiumView struct {
	Imported  bool              `json:"imported"`
	Country   string            `json:"country"`
	Countries []premium.Country `json:"countries"`
	Pending   bool              `json:"pending"`
}

func (s *Store) Premium() PremiumSubscription {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.premium
	out.Countries = append([]premium.Country{}, out.Countries...)
	return out
}

func (s *Store) PremiumView(pool string) PremiumView {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.root.Pools {
		if p.Name == pool && p.Premium {
			return PremiumView{Imported: true, Country: s.premium.Country, Countries: append([]premium.Country{}, s.premium.Countries...), Pending: s.premium.PendingCountry != ""}
		}
	}
	return PremiumView{Countries: []premium.Country{}}
}

func (s *Store) SavePremium(v PremiumSubscription) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.savePremiumLocked(v)
}

func (s *Store) savePremiumLocked(v PremiumSubscription) error {
	if err := s.saveLocked(filepath.Join(s.base, "premium.json"), v); err != nil {
		return errors.New("could not persist Premium subscription")
	}
	s.premium = v
	return nil
}

func (s *Store) ImportPremium(pool, key, userCountry string) (PremiumSubscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pi := -1
	for i, p := range s.root.Pools {
		if p.Premium && p.Name != pool {
			return PremiumSubscription{}, errors.New("Premium subscription already belongs to another pool")
		}
		if p.Name == pool {
			pi = i
		}
	}
	if pi < 0 {
		return PremiumSubscription{}, errors.New("Premium pool not found")
	}
	p := s.root.Pools[pi]
	if p.Settings.EngineMode != "" {
		return PremiumSubscription{}, errors.New("Premium requires a native AWG pool")
	}
	if !p.Premium && len(p.Configs) > 0 {
		return PremiumSubscription{}, errors.New("import Premium into an empty AWG pool")
	}
	if p.Settings.Platform == PlatformOpenwrt && p.Settings.OpenwrtProto != "amneziawg" {
		return PremiumSubscription{}, errors.New("Premium requires an amneziawg pool")
	}
	v := s.premium
	if v.Key != "" && v.Key != key {
		return PremiumSubscription{}, errors.New("another Premium subscription is already stored")
	}
	if v.UUID == "" {
		id, err := premium.UUID()
		if err != nil {
			return PremiumSubscription{}, errors.New("could not create Premium identity")
		}
		v.UUID = id
	}
	v.Key, v.UserCountry = key, userCountry
	if err := s.savePremiumLocked(v); err != nil {
		return PremiumSubscription{}, err
	}
	root := s.root
	root.Pools = append([]Pool{}, s.root.Pools...)
	root.Pools[pi].Premium = true
	root.Pools[pi].Settings.Source = ""
	root.Pools[pi].Settings.Amnezia = nil
	if err := s.saveLocked(filepath.Join(s.base, "config.json"), root); err != nil {
		return PremiumSubscription{}, errors.New("could not bind Premium pool")
	}
	s.root = root
	return v, nil
}

func (s *Store) ReplacePremiumConfig(pool string, raw []byte) (string, error) {
	cfg, err := wgconf.Parse(raw)
	if err != nil {
		return "", errors.New("invalid Premium configuration")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pi := -1
	for i, p := range s.root.Pools {
		if p.Name == pool && p.Premium {
			pi = i
			break
		}
	}
	if pi < 0 {
		return "", errors.New("Premium pool not found")
	}
	hash := sha256.Sum256(raw)
	file := fmt.Sprintf("premium-%x.conf", hash[:8])
	path := filepath.Join(s.PoolDir(pool), file)
	if err := os.WriteFile(path+".tmp", raw, 0600); err != nil {
		return "", errors.New("could not stage Premium configuration")
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		return "", errors.New("could not persist Premium configuration")
	}
	root := s.root
	root.Pools = append([]Pool{}, s.root.Pools...)
	old := root.Pools[pi].Configs
	root.Pools[pi].Configs = []ManagedConfig{{File: file, Original: "Amnezia Premium", Endpoint: cfg.Endpoint(), PublicKey: cfg.Peer.PublicKey, Addresses: cfg.Addresses, Enabled: true}}
	if err := s.saveLocked(filepath.Join(s.base, "config.json"), root); err != nil {
		retained := false
		for _, c := range old {
			if c.File == file {
				retained = true
			}
		}
		if !retained {
			os.Remove(path)
		}
		return "", errors.New("could not select Premium configuration")
	}
	s.root = root
	for _, c := range old {
		if c.File != file {
			os.Remove(filepath.Join(s.PoolDir(pool), c.File))
		}
	}
	return file, nil
}
