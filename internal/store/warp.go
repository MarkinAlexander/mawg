package store

import (
	"errors"
	"path/filepath"
	"time"
)

// WarpDevice - запись о зарегистрированном устройстве Cloudflare WARP.
// Приватные ключи тут НЕ хранятся: они живут только в .conf файлов
// пулов (0600). Токен устройства - на будущие операции (deactivate и
// пр.), сейчас используется для диагностики и учёта регистраций.
type WarpDevice struct {
	ID        string    `json:"id"`
	Token     string    `json:"token"`
	Pool      string    `json:"pool"`
	Endpoint  string    `json:"endpoint"`
	AddressV4 string    `json:"addressV4,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// WarpDevices - копия реестра регистраций WARP.
func (s *Store) WarpDevices() []WarpDevice {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]WarpDevice, len(s.warp))
	copy(out, s.warp)
	return out
}

// AddWarpDevice - запоминает регистрацию (файл warp.json, 0600, по
// образцу free.json).
func (s *Store) AddWarpDevice(d WarpDevice) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	root := append(append([]WarpDevice{}, s.warp...), d)
	if err := s.saveLocked(filepath.Join(s.base, "warp.json"), root); err != nil {
		return errors.New("не удалось сохранить реестр WARP")
	}
	s.warp = root
	return nil
}
