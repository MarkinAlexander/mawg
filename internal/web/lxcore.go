package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"time"

	"mawg/internal/singbox"
)

// installLXCore - установка ядра sing-box по кнопке «Система -> Зависимости»
// (источник на выбор: extended/lx/upstream). Чужое ядро меняется только когда
// свежая проверка показала пункт замены (владелец подтвердил в диалоге);
// backup=false - без копии старого.
func (s *Server) installLXCore(w http.ResponseWriter, r *http.Request, flavor string, backup bool, source string) {
	if source != "" {
		if _, ok := singbox.SourceByID(source); !ok {
			writeErr(w, fmt.Errorf("неизвестный источник ядра %q", source))
			return
		}
	} else if len(singbox.Sources) > 0 {
		source = singbox.Sources[0].ID
	}
	replace := false
	for _, item := range s.systemCheck().Items {
		if item.ID == "singbox-lx" {
			replace = item.Action == "singbox-lx-replace"
			break
		}
	}
	target := singbox.LXTargetForPlatform(s.backend.Name())
	s.store.LogEvent("system", "lxcore", "установка ядра sing-box ("+orDefault(flavor, "plain")+", источник "+source+", цель "+target.Bin+", замена чужого: "+yesNo(replace)+", копия старого: "+yesNo(backup)+")")
	ctx, cancel := context.WithTimeout(r.Context(), 290*time.Second)
	defer cancel()
	res, err := singbox.InstallLXCore(ctx, singbox.LXInstallOptions{
		Flavor: flavor, Target: target, Source: source, ReplaceForeign: replace,
		DropForeignBackup: replace && !backup,
	})
	if err != nil {
		s.store.LogEvent("system", "lxcore", "установка не удалась: "+err.Error())
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error(), "output": res.Log})
		return
	}
	switch {
	case res.UpToDate:
		s.store.LogEvent("system", "lxcore", "ядро "+res.Version+" уже актуально")
	default:
		msg := "установлено ядро sing-box " + res.Version + " (" + res.Tag + ") в " + res.Bin
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
	s.coreDetectReset()
	if len(s.enginePools()) > 0 {
		go func() {
			// бинарь ядра заменён - старый процесс продолжает жить со
			// снятым образом (живой кейс Кинетика: «start» на работающем
			// сервисе no-op, движок остался на прежнем ядре). Поэтому при
			// живом сервисе - рестарт; с нуля - просто start.
			if s.store.SingboxMode() == "shared" {
				_, initScript := s.sharedPaths()
				action := "start"
				if mgr, err := s.sb(); err == nil && mgr.SharedAlive() {
					action = "restart"
				}
				out, err := exec.Command(initScript, action).CombinedOutput()
				if err != nil {
					s.store.LogEvent("system", "lxcore", "сервис sing-box не перезапущен ("+initScript+" "+action+"): "+firstLine(string(out)))
				}
			}
			skipped, err := s.applyEngine()
			if err != nil {
				s.store.LogEvent("singbox", "applied", "после установки движок не пересобран: "+err.Error())
				return
			}
			for _, sk := range skipped {
				s.store.LogEvent("singbox", "applied", sk)
			}
			s.store.LogEvent("singbox", "applied", "движок пересобран под ядро "+res.Version)
		}()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "output": res.Log, "upToDate": res.UpToDate, "version": res.Version, "backup": res.Backup,
	})
}

// singboxSourceView - источник установки для селекта диалога: живой дискавери
// с пометкой, есть ли сборка под архитектуру роутера и сжатый профиль.
type singboxSourceView struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Note      string `json:"note"`
	Latest    string `json:"latest,omitempty"`
	UPX       bool   `json:"upx"`       // есть сжатый профиль
	Available bool   `json:"available"` // есть ассет под арх роутера
	Error     string `json:"error,omitempty"`
}

// getSingboxSources - GET /api/v1/cores/singbox/sources: список источников
// ядра с их последними релизами (кэш 10 минут - GitHub API, как чекер MT).
func (s *Server) getSingboxSources(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	s.sbSrcCache.serve(w, 10*time.Minute, func() ([]byte, error) {
		arch := ""
		if a, _, err := singbox.DetectLxArch(); err == nil {
			arch = a
		}
		reports := singbox.Discovery(ctx)
		out := make([]singboxSourceView, 0, len(reports))
		for _, rep := range reports {
			v := singboxSourceView{ID: rep.Source.ID, Title: rep.Source.Title, Note: rep.Source.Note, UPX: rep.Source.CompactMarker != ""}
			if rep.Error != "" {
				v.Error = rep.Error
			} else {
				// свежий релиз, ГДЕ ЕСТЬ сборка под арх роутера (у альф
				// части платформ нет - available не должен из-за них гаснуть)
				for _, rel := range rep.Releases {
					n, ok := rel.Names[arch]
					if !ok || (n.Plain == "" && n.UPX == "") {
						continue
					}
					v.Latest, v.Available = rel.Tag, true
					break
				}
			}
			out = append(out, v)
		}
		return json.Marshal(map[string]any{"sources": out, "arch": arch})
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
