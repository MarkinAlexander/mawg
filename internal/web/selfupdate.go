package web

// Хендлеры/логика самообновления и перезапусков (из server.go).

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"

	"mawg/internal/selfupdate"
	"mawg/internal/store"
)

func (s *Server) updateCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, selfupdate.Check(r.Context(), s.version))
}

func (s *Server) updateRun(w http.ResponseWriter, r *http.Request) {
	info := selfupdate.Check(r.Context(), s.version)
	if info.Error != "" {
		writeErr(w, fmt.Errorf("%s", info.Error))
		return
	}
	if !info.Update {
		writeErr(w, fmt.Errorf("обновлений нет (текущая %s, последняя %s)", info.Current, info.Latest))
		return
	}
	s.store.LogEvent("system", "update", "запуск обновления до "+info.Latest)
	go func() {
		if err := selfupdate.Run(context.Background()); err != nil {
			s.store.LogEvent("system", "update", "обновление не удалось: "+err.Error())
		}
	}()
	writeJSON(w, http.StatusOK, map[string]string{"ok": "started", "to": info.Latest})
}

// restartPanel - рестарт в отложенной горутине (ответ уходит раньше);
// ошибка - только отсутствующий init-скрипт: тогда вызывающий не трогает
// состояние.
func restartPanel(s *Server) error {
	script := mawgServiceScript(s.backend.Name() == store.PlatformKeenetic)
	if _, err := os.Stat(script); err != nil {
		return fmt.Errorf("init-скрипт не найден: %s", script)
	}
	go func() {
		time.Sleep(700 * time.Millisecond)
		cmd := exec.Command(script, "restart")
		cmd.Stdout = nil
		cmd.Stderr = nil
		cmd.Run()
	}()
	return nil
}

// postSystemRestart перезапускает сервис: ответ уходит раньше, чем
// демон снимется.
func (s *Server) postSystemRestart(w http.ResponseWriter, r *http.Request) {
	if err := restartPanel(s); err != nil {
		writeErr(w, err)
		return
	}
	s.store.LogEvent("settings", "server", "перезапуск панели из веб-интерфейса")
	writeJSON(w, http.StatusOK, map[string]string{"ok": "restarting"})
}

// postMTRestart - рестарт активного MagiTrickle (платформенно:
// Кинетик S98mtc/S99magitrickle, OpenWrt mtc/magitrickle).
// Слово владельца 03.11: кнопка на вкладке Система - геоблок удобнее
// проверять перезапуском MT прямо из панели.
func (s *Server) postMTRestart(w http.ResponseWriter, r *http.Request) {
	if err := s.backend.RestartMagitrickle(); err != nil {
		writeErr(w, err)
		return
	}
	s.store.LogEvent("settings", "server", "перезапуск MagiTrickle из веб-интерфейса")
	writeJSON(w, http.StatusOK, map[string]string{"ok": "restarting"})
}

// postMTrecheck - ручная перепроверка глючной сборки MT (кнопка панели,
// 114-док): та же проба совместимости, чистая запись снимает липкий флаг.
// Async: результат в журнале и в /status.
func (s *Server) postMTrecheck(w http.ResponseWriter, r *http.Request) {
	if s.engine == nil {
		writeErr(w, fmt.Errorf("движок не запущен"))
		return
	}
	if !s.engine.RecheckMagitrickle() {
		writeErr(w, fmt.Errorf("MT не отвечает"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "recheck started"})
}
