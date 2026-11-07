package web

// Диагностика движков (112-док 3.7, слово владельца 04.11: «не хватает в
// логах диагностики, собирать все диагностические выводы по сингбоксам
// михомо и мт»): сводка живости/версий/ошибок по ядрам + текстовый бандл
// для разбора (конфиги с маскировкой секретов, хвосты логов, события).

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"mawg/internal/platform"
)

type engineDiag struct {
	ID        string   `json:"id"`
	Running   bool     `json:"running"`
	Version   string   `json:"version,omitempty"`
	PID       int      `json:"pid,omitempty"`
	StartedAt string   `json:"startedAt,omitempty"` // старт по mtime run.pid
	LastError string   `json:"lastError,omitempty"`
	LogPath   string   `json:"logPath,omitempty"`
	LogTail   []string `json:"logTail,omitempty"`
	Note      string   `json:"note,omitempty"`
}

// mtLogCandidates - где живой MT-форк может вести лог (Кинетик logread
// отсутствует, stdout init-скрипта теряется).
var mtLogCandidates = []string{
	"/opt/var/log/magitrickle.log",
	"/var/log/magitrickle.log",
	"/opt/var/lib/magitrickle/magitrickle.log",
	"/var/lib/magitrickle/magitrickle.log",
}

// getEnginesDiag - GET /api/v1/system/engines: живость/версии/хвосты
// логов всех движков + MT.
func (s *Server) getEnginesDiag(w http.ResponseWriter, r *http.Request) {
	out := struct {
		Engines []engineDiag `json:"engines"`
		MT      engineDiag   `json:"mt"`
	}{Engines: []engineDiag{}}

	if mgr, err := s.sb(); err == nil {
		d := engineDiag{ID: "singbox", LogPath: mgr.EngineLog(), LastError: mgr.LastError()}
		st := mgr.Status()
		d.Running, d.PID, d.Version = st.Running, st.PID, st.Version
		d.StartedAt = pidStartedAt(filepath.Join(s.store.Base(), "singbox", "run.pid"))
		d.LogTail = platform.LogTail(d.LogPath, 20)
		out.Engines = append(out.Engines, d)
	} else {
		out.Engines = append(out.Engines, engineDiag{ID: "singbox", Note: err.Error()})
	}
	if mgr, err := s.mh(); err == nil {
		d := engineDiag{ID: "mihomo", LogPath: mgr.EngineLog(), LastError: mgr.LastError()}
		st := mgr.Status()
		d.Running, d.PID, d.Version = st.Running, st.PID, st.Version
		d.StartedAt = pidStartedAt(filepath.Join(s.store.Base(), "mihomo", "run.pid"))
		d.LogTail = platform.LogTail(d.LogPath, 20)
		out.Engines = append(out.Engines, d)
	} else {
		out.Engines = append(out.Engines, engineDiag{ID: "mihomo", Note: err.Error()})
	}

	mt := engineDiag{ID: "magitrickle"}
	if v := s.mtRealVersion(r.Context()); v != "" {
		mt.Version = v
	} else {
		mt.Note = "версия недоступна (демон не отвечает?)"
	}
	for _, p := range mtLogCandidates {
		if _, err := os.Stat(p); err == nil {
			mt.LogPath = p
			mt.LogTail = platform.LogTail(p, 20)
			break
		}
	}
	if mt.LogPath == "" {
		mt.Note += "; файл лога MT не найден в известных местах"
	}
	out.MT = mt
	writeJSON(w, http.StatusOK, out)
}

// pidStartedAt - время старта по mtime run.pid (менеджер пишет его при
// каждом старте процесса ядра).
func pidStartedAt(pidFile string) string {
	st, err := os.Stat(pidFile)
	if err != nil {
		return ""
	}
	return st.ModTime().Format(time.RFC3339)
}

var (
	reUUID     = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	reSecretKV = regexp.MustCompile(`(?i)(password|passwd|secret|private[_-]?key|auth)["']?\s*[:=]\s*("[^"]*"|\S+)`)
	// base64-ключи WG/REALITY pbk (43+ значащих символов, возможен =)
	reB64Key = regexp.MustCompile(`[A-Za-z0-9+/_-]{43,}={0,2}`)
)

// maskSecrets - заменить uuid/пароли/приватные ключи/base64-ключи на ***:
// бандл диагностики уходит в чаты, живые креды там не нужны.
func maskSecrets(s string) string {
	s = reSecretKV.ReplaceAllString(s, "$1=***")
	s = reUUID.ReplaceAllString(s, "***")
	s = reB64Key.ReplaceAllString(s, "***")
	return s
}

// getDiagnostics - GET /api/v1/system/diagnostics: текстовый бандл
// (версии, конфиги движков с маскировкой, статусы пулов, хвосты логов,
// последние события) - скачивается одним файлом.
func (s *Server) getDiagnostics(w http.ResponseWriter, r *http.Request) {
	var b strings.Builder
	line := func(format string, a ...any) {
		fmt.Fprintf(&b, format+"\n", a...)
	}
	sect := func(name string) { b.WriteString("\n===== " + name + " =====\n") }

	sect("mawg")
	line("mawg: %s", s.version)
	line("платформа: %s", s.backend.Name())
	line("время сбора: %s", time.Now().Format(time.RFC3339))

	sect("движки")
	if mgr, err := s.sb(); err == nil {
		st := mgr.Status()
		line("sing-box: %s running=%v pid=%d mode=%s", st.Version, st.Running, st.PID, st.Mode)
		if e := mgr.LastError(); e != "" {
			line("последняя ошибка старта: %s", e)
		}
		if tail := platform.LogTail(mgr.EngineLog(), 15); len(tail) > 0 {
			line("лог (%s), последние строки:", mgr.EngineLog())
			for _, l := range tail {
				line("  " + l)
			}
		}
	} else {
		line("sing-box: менеджер недоступен: %v", err)
	}
	if mgr, err := s.mh(); err == nil {
		st := mgr.Status()
		line("mihomo: %s running=%v pid=%d", st.Version, st.Running, st.PID)
		if e := mgr.LastError(); e != "" {
			line("последняя ошибка старта: %s", e)
		}
		if tail := platform.LogTail(mgr.EngineLog(), 15); len(tail) > 0 {
			line("лог (%s), последние строки:", mgr.EngineLog())
			for _, l := range tail {
				line("  " + l)
			}
		}
	} else {
		line("mihomo: менеджер недоступен: %v", err)
	}
	if v := s.mtRealVersion(r.Context()); v != "" {
		line("MagiTrickle: %s", v)
	} else {
		line("MagiTrickle: версия недоступна")
	}

	sect("движковые пулы")
	for _, p := range s.enginePools() {
		mgr, err := s.engineMgr(p)
		if err != nil {
			line("%s (%s): статуса нет: %v", p.Name, p.Settings.EngineCore, err)
			continue
		}
		if st, ok := mgr.PoolStatus(p.Name); ok {
			state := "ok"
			if !st.ProbeOK {
				state = "fail"
			}
			if st.Reason != "" {
				state += " (" + st.Reason + ")"
			}
			line("%s (%s): %s %dms consecFails=%d eligible=%d port=%d %s",
				p.Name, p.Settings.EngineCore, state, st.ProbeMs, st.ConsecFails, st.Eligible, st.MixedPort, st.ProbeErr)
		} else {
			line("%s (%s): статус не собран", p.Name, p.Settings.EngineCore)
		}
	}

	sect("конфиг sing-box (секреты замаскированы)")
	if data, err := os.ReadFile(filepath.Join(s.store.Base(), "singbox", "config.json")); err == nil {
		b.WriteString(maskSecrets(string(data)))
		b.WriteString("\n")
	} else {
		line("не читается: %v", err)
	}
	sect("конфиг mihomo (секреты замаскированы)")
	if data, err := os.ReadFile(filepath.Join(s.store.Base(), "mihomo", "config.yaml")); err == nil {
		b.WriteString(maskSecrets(string(data)))
		b.WriteString("\n")
	} else {
		line("не читается: %v", err)
	}

	sect("события (последние 40)")
	evs := s.store.Events()
	if len(evs) > 40 {
		evs = evs[len(evs)-40:]
	}
	for _, ev := range evs {
		line("%s [%s/%s] %s", ev.Time.Format("02.01 15:04:05"), ev.Pool, ev.Kind, ev.Message)
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=mawg-diagnostics.txt")
	w.Write([]byte(b.String()))
}
