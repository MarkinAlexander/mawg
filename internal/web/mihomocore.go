package web

import (
	"context"
	"net/http"
	"time"

	"mawg/internal/mihomo"
)

// seam для тестов (живая установка качает релиз с github)
var mihomoInstallCore = mihomo.InstallCore

// installMihomoCore - установка ядра mihomo по кнопке в строке ядра
// («Система → Ядра VPN», 88-док). Чужой бинарь меняется только с явным
// replace (подтверждение в панели); backup=false - без копии старого.
func (s *Server) installMihomoCore(w http.ResponseWriter, r *http.Request, replace, backup bool) {
	target := mihomo.TargetForPlatform(s.backend.Name())
	s.store.LogEvent("system", "mihomo", "установка mihomo (цель "+target.Bin+", замена чужого: "+yesNo(replace)+", копия старого: "+yesNo(backup)+")")
	ctx, cancel := context.WithTimeout(r.Context(), 290*time.Second)
	defer cancel()
	res, err := mihomoInstallCore(ctx, mihomo.InstallOptions{
		Target: target, ReplaceForeign: replace,
		DropForeignBackup: replace && !backup,
	})
	if err != nil {
		s.store.LogEvent("system", "mihomo", "установка не удалась: "+err.Error())
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error(), "output": res.Log})
		return
	}
	switch {
	case res.UpToDate:
		s.store.LogEvent("system", "mihomo", "mihomo "+res.Version+" уже актуален")
	default:
		msg := "установлен mihomo " + res.Version + " (" + res.Tag + ") в " + res.Bin
		if res.Previous != "" {
			msg += ", было " + res.Previous
		}
		if res.Backup != "" {
			msg += ", старое сохранено: " + res.Backup
		}
		s.store.LogEvent("system", "mihomo", msg)
	}
	s.resetMH()
	s.sysCache.reset()
	if len(s.enginePoolsOfCore(engineCoreMihomo)) > 0 {
		go func() {
			skipped, err := s.applyEngine()
			if err != nil {
				s.store.LogEvent("mihomo", "applied", "после установки ядро не пересобрано: "+err.Error())
				return
			}
			for _, sk := range skipped {
				s.store.LogEvent("mihomo", "applied", sk)
			}
			s.store.LogEvent("mihomo", "applied", "ядро пересобрано под mihomo "+res.Version)
		}()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "output": res.Log, "upToDate": res.UpToDate, "version": res.Version, "backup": res.Backup,
	})
}
