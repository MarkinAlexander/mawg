package web

import (
	"context"
	"net/http"
	"time"

	"mawg/internal/mtinstall"
)

// mtInstall - точка подмены для тестов (реальная установка ходит в GitHub
// и трогает менеджер пакетов роутера).
var mtInstall = mtinstall.Install

// mtVersionWait - сколько ждать живой /api/v1/version после старта службы.
var mtVersionWait = 15 * time.Second

// installMagitrickle - установка/обновление MagiTrickle пакетом из релиза
// mawg-форка: пункт «MagiTrickle» в Системе (маркер действия mt-fork).
// После установки ждём живой /api/v1/version - служба стартует не мгновенно.
func (s *Server) installMagitrickle(w http.ResponseWriter, r *http.Request) {
	s.store.LogEvent("system", "mtinstall", "установка MagiTrickle из релиза форка "+mtinstall.Repo)
	ctx, cancel := context.WithTimeout(r.Context(), 290*time.Second)
	defer cancel()
	res, err := mtInstall(ctx, mtinstall.Options{Platform: s.backend.Name()})
	if err != nil {
		s.store.LogEvent("system", "mtinstall", "установка не удалась: "+err.Error())
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error(), "output": res.Log})
		return
	}
	s.sysCache.reset()

	// служба стартует секунды: опрашиваем версию, чтобы ответить живой, а не пакетной
	version := ""
	deadline := time.Now().Add(mtVersionWait)
	for time.Now().Before(deadline) {
		if v, verr := s.mtClient().GetVersion(ctx); verr == nil && v != "" {
			version = v
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	msg := "установлен MagiTrickle " + res.Tag + " (" + res.Asset + ")"
	if version != "" {
		msg += ", служба отвечает: " + version
	} else {
		msg += "; версия службы пока не прочитана (проверка зависимостей обновится)"
	}
	s.store.LogEvent("system", "mtinstall", msg)
	out := map[string]any{"ok": true, "output": res.Log, "tag": res.Tag, "asset": res.Asset, "version": version}
	// хуки OUTPUT-групп вернутся штатным 30-секундным SyncHooks
	writeJSON(w, http.StatusOK, out)
}
