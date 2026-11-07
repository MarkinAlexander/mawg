package store

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	eventsMemCap   = 500       // сколько событий живёт в памяти
	eventsMaxBytes = 256 << 10 // ротация дискового журнала при превышении
)

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
