package store

import (
	"time"

	"mawg/internal/links"
)

type Sub struct {
	Name        string        `json:"name"`
	Source      string        `json:"source"`
	AddedAt     time.Time     `json:"addedAt"`
	RefreshedAt time.Time     `json:"refreshedAt,omitempty"`
	Info        links.SubInfo `json:"info,omitempty"`
	Nodes       []links.Node  `json:"nodes,omitempty"`
	Warnings    []string      `json:"warnings,omitempty"`
	Error       string        `json:"error,omitempty"`
}

func (s *Store) subsPath() string { return s.base + "/subs.json" }

func (s *Store) Subs() []Sub {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.subsLocked()
}

func (s *Store) subsLocked() []Sub {
	var out []Sub
	if err := s.load(s.subsPath(), &out); err != nil {
		return nil
	}
	return out
}

func (s *Store) SubByName(name string) (Sub, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sub := range s.subsLocked() {
		if sub.Name == name {
			return sub, true
		}
	}
	return Sub{}, false
}

func (s *Store) SaveSub(sub Sub) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	subs := s.subsLocked()
	for i := range subs {
		if subs[i].Name == sub.Name {
			subs[i] = sub
			return s.saveLocked(s.subsPath(), subs)
		}
	}
	subs = append(subs, sub)
	return s.saveLocked(s.subsPath(), subs)
}

func (s *Store) DeleteSub(name string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	subs := s.subsLocked()
	for i := range subs {
		if subs[i].Name == name {
			subs = append(subs[:i], subs[i+1:]...)
			return true, s.saveLocked(s.subsPath(), subs)
		}
	}
	return false, nil
}
