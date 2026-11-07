package web

// Хендлеры/логика системных зависимостей (из server.go).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"mawg/internal/platform/keenetic"
	"mawg/internal/provision"
	"mawg/internal/store"
)

func (s *Server) getMagitrickle(w http.ResponseWriter, r *http.Request) {
	if s.mt == nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false})
		return
	}
	groups, err := s.mt.Groups(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"available": true, "groups": groups})
}

func (s *Server) systemCheck() provision.Result {
	var res provision.Result
	if s.backend.Name() == store.PlatformKeenetic {
		if kb, ok := s.backend.(*keenetic.Backend); ok {
			res = provision.CheckKeenetic(kb.Ndmc)
		}
	}
	if res.Items == nil {
		res = provision.CheckOpenwrt()
	}
	// MagiTrickle: opkg-версия пакета != ревизии нашего бинаря (форк
	// ставится поверх пакета) - показываем реальную
	s.enrichMagitrickleVersion(res.Items)
	return res
}

func (s *Server) getSystemCheck(w http.ResponseWriter, r *http.Request) {
	// панель опрашивает проверку зависимостей каждые 60с, а каждая проверка
	// гоняет opkg/apk-пайплайны и version-запуски - на слабом роутере это
	// постоянная фоновая нагрузка; кэш 3 минуты, сбрасывается после установки
	s.sysCache.serve(w, 3*time.Minute, func() ([]byte, error) {
		return json.Marshal(s.systemCheck())
	})
}

func (s *Server) postSystemInstall(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID      string `json:"id"`
		Flavor  string `json:"flavor"`
		Source  string `json:"source"` // источник sing-box: extended|lx|upstream
		Backup  *bool  `json:"backup"`
		Replace bool   `json:"replace"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		writeErr(w, fmt.Errorf("не указан id компонента"))
		return
	}
	if req.ID == "singbox-lx" {
		backup := true
		if req.Backup != nil {
			backup = *req.Backup
		}
		s.installLXCore(w, r, req.Flavor, backup, req.Source)
		return
	}
	if req.ID == "mihomo" {
		backup := true
		if req.Backup != nil {
			backup = *req.Backup
		}
		s.installMihomoCore(w, r, req.Replace, backup)
		return
	}
	// MagiTrickle ставим кодом из релиза mawg-форка (детект таргета, пакет
	// под opkg/apk) - шелл-скрипт bin.magitrickle.dev больше не предлагаем
	if req.ID == "magitrickle" {
		s.installMagitrickle(w, r)
		return
	}
	check := s.systemCheck()
	for _, item := range check.Items {
		if item.ID != req.ID {
			continue
		}
		if item.Installed || item.Action == "" {
			writeErr(w, fmt.Errorf("ничего не требуется для %s", item.ID))
			return
		}
		out, err := provision.Install(item.Action)
		s.store.LogEvent("system", "install", item.ID+": "+firstLine(out))
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error(), "output": out})
			return
		}
		s.sysCache.reset()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "output": out})
		return
	}
	writeErr(w, fmt.Errorf("компонент %s не найден", req.ID))
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i > 0 {
		return s[:i]
	}
	return s
}
