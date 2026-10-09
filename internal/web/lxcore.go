package web

import (
	"context"
	"net/http"
	"os/exec"
	"time"

	"mawg/internal/singbox"
)

// installLXCore - установка lx-ядра по кнопке «Система -> Зависимости».
// Чужое ядро меняется только когда свежая проверка показала пункт замены
// (владелец подтвердил в диалоге); backup=false - без копии старого.
func (s *Server) installLXCore(w http.ResponseWriter, r *http.Request, flavor string, backup bool) {
	replace := false
	for _, item := range s.systemCheck().Items {
		if item.ID == "singbox-lx" {
			replace = item.Action == "singbox-lx-replace"
			break
		}
	}
	target := singbox.LXTargetForPlatform(s.backend.Name())
	s.store.LogEvent("system", "lxcore", "установка lx-ядра ("+orDefault(flavor, "plain")+", цель "+target.Bin+", замена чужого: "+yesNo(replace)+", копия старого: "+yesNo(backup)+")")
	ctx, cancel := context.WithTimeout(r.Context(), 290*time.Second)
	defer cancel()
	res, err := singbox.InstallLXCore(ctx, singbox.LXInstallOptions{
		Flavor: flavor, Target: target, ReplaceForeign: replace,
		DropForeignBackup: replace && !backup,
	})
	if err != nil {
		s.store.LogEvent("system", "lxcore", "установка не удалась: "+err.Error())
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error(), "output": res.Log})
		return
	}
	switch {
	case res.UpToDate:
		s.store.LogEvent("system", "lxcore", "lx-ядро "+res.Version+" уже актуально")
	default:
		msg := "установлено lx-ядро " + res.Version + " (" + res.Tag + ") в " + res.Bin
		if res.Previous != "" {
			msg += ", было " + res.Previous
		}
		if res.Backup != "" {
			msg += ", старое сохранено: " + res.Backup
		}
		s.store.LogEvent("system", "lxcore", msg)
	}
	// движок пересобирается в фоне: в shared это рестарт чужого сервиса,
	// ответ клиенту его не ждёт
	s.resetSB()
	s.sysCache.reset()
	if len(s.enginePools()) > 0 {
		go func() {
			// после установки с нуля сервиса нет; start идемпотентен
			if s.store.SingboxMode() == "shared" {
				_, initScript := s.sharedPaths()
				out, err := exec.Command(initScript, "start").CombinedOutput()
				if err != nil {
					s.store.LogEvent("system", "lxcore", "сервис sing-box не стартовал ("+initScript+"): "+firstLine(string(out)))
				}
			}
			skipped, err := s.applyEngine()
			if err != nil {
				s.store.LogEvent("singbox", "applied", "после установки lx движок не пересобран: "+err.Error())
				return
			}
			for _, sk := range skipped {
				s.store.LogEvent("singbox", "applied", sk)
			}
			s.store.LogEvent("singbox", "applied", "движок пересобран под lx-ядро "+res.Version)
		}()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "output": res.Log, "upToDate": res.UpToDate, "version": res.Version, "backup": res.Backup,
	})
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func yesNo(b bool) string {
	if b {
		return "да"
	}
	return "нет"
}
