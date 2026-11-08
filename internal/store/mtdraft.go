package store

// Черновик правок MagiTrickle (114-док): при выключенном автосохранении
// правки вкладки «Группы» копятся в base/mt-draft.json и пишутся в MT
// одним массовым PUT по команде панели. Хранится сырым JSON - store не
// импортирует magitrickle.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func (s *Store) mtDraftPath() string { return filepath.Join(s.base, "mt-draft.json") }

// MTDraftGet - сырое тело черновика; false = файла нет.
func (s *Store) MTDraftGet() (json.RawMessage, bool) {
	data, err := os.ReadFile(s.mtDraftPath())
	if err != nil {
		return nil, false
	}
	return json.RawMessage(data), true
}

// MTDraftSave пишет тело черновика атомарно (tmp+rename, как saveLocked):
// полфайла после сбоя питания сделал бы черновик нечитаемым.
func (s *Store) MTDraftSave(raw json.RawMessage) error {
	if !json.Valid(raw) {
		return fmt.Errorf("черновик - не валидный JSON")
	}
	return writeAtomically(s.mtDraftPath(), raw)
}

// MTDraftClear удаляет черновик (применён или сброшен); нет файла - не ошибка.
func (s *Store) MTDraftClear() error {
	err := os.Remove(s.mtDraftPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
