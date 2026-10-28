package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"mawg/internal/mihomo"
	"mawg/internal/singbox"
	"mawg/internal/store"
)

// cores - менеджмент ядер VPN («Система → Ядра VPN», просьба
// пользователя: включать/выключать совсем, снимать с автозагрузки,
// устанавливать/останавливать/удалять). Ядра: sing-box и mihomo (88-док);
// список структурный - новое ядро = ещё один элемент, UI рендерит по списку.

type coreCaps struct {
	Start     bool `json:"start"`
	Stop      bool `json:"stop"`
	Enable    bool `json:"enable"`
	Disable   bool `json:"disable"`
	Uninstall bool `json:"uninstall"`
	Install   bool `json:"install"` // можно поставить из релиза (не установлен)
}

type coreView struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Installed     bool     `json:"installed"`
	Version       string   `json:"version,omitempty"`
	LX            bool     `json:"lx,omitempty"`
	Extended      bool     `json:"extended,omitempty"`
	Source        string   `json:"source,omitempty"` // источник сборки: lx | extended | upstream
	Mode          string   `json:"mode,omitempty"`  // own | shared
	Running       bool     `json:"running"`
	Autostart     bool     `json:"autostart"`               // сервисная автозагрузка (shared)
	AutostartNote string   `json:"autostartNote,omitempty"` // own: стартует вместе с mawg
	ManagedByMawg bool     `json:"managedByMawg"`
	Disabled      bool     `json:"disabled"`
	Note          string   `json:"note,omitempty"`
	Caps          coreCaps `json:"caps"`
}

// кэш детекта ядра: запуск <bin> version - не бесплатный, а /cores
// ходит опросом из панели
var (
	coreDetectMu sync.Mutex
	coreDetectAt time.Time
	coreDetect   *singbox.Engine
	coreDetectOK bool
)

func (s *Server) detectCore() (*singbox.Engine, bool) {
	coreDetectMu.Lock()
	defer coreDetectMu.Unlock()
	if !coreDetectAt.IsZero() && time.Since(coreDetectAt) < time.Minute {
		return coreDetect, coreDetectOK
	}
	coreDetect, coreDetectOK = func() (*singbox.Engine, bool) {
		e, err := singbox.Detect()
		return e, err == nil
	}()
	coreDetectAt = time.Now()
	return coreDetect, coreDetectOK
}

func (s *Server) coreDetectReset() {
	coreDetectMu.Lock()
	coreDetectAt = time.Time{}
	coreDetectMu.Unlock()
	mhDetectMu.Lock()
	mhDetectAt = time.Time{}
	mhDetectMu.Unlock()
}

// отдельный кэш детекта mihomo (тот же паттерн: -v не бесплатный, а /cores
// ходит опросом)
var (
	mhDetectMu sync.Mutex
	mhDetectAt time.Time
	mhDetect   *mihomo.Engine
	mhDetectOK bool
)

func (s *Server) detectMihomo() (*mihomo.Engine, bool) {
	mhDetectMu.Lock()
	defer mhDetectMu.Unlock()
	if !mhDetectAt.IsZero() && time.Since(mhDetectAt) < time.Minute {
		return mhDetect, mhDetectOK
	}
	mhDetect, mhDetectOK = func() (*mihomo.Engine, bool) {
		e, err := mihomo.Detect()
		return e, err == nil
	}()
	mhDetectAt = time.Now()
	return mhDetect, mhDetectOK
}

// mihomoCoreView - строка ядра mihomo: только own-режим (свой процесс
// mawg, 88-док), сервисной автозагрузки нет - стартует вместе с пулами.
func (s *Server) mihomoCoreView() coreView {
	v := coreView{ID: "mihomo", Title: "mihomo (Clash.Meta)"}
	eng, ok := s.detectMihomo()
	if !ok {
		v.Note = "не установлен (кнопка «Установить» ставит из релиза MetaCubeX)"
		v.Caps.Install = true
		v.Disabled = s.store.CoreDisabled("mihomo")
		return v
	}
	v.Installed, v.Version = true, eng.Version
	v.Mode = "own"
	v.ManagedByMawg = mihomo.ManagedByMawg(mihomo.TargetForPlatform(s.backend.Name()))
	v.Disabled = s.store.CoreDisabled("mihomo")
	if mgr, err := s.mh(); err == nil {
		v.Running = mgr.Status().Running
	}
	v.AutostartNote = "own-режим: ядро стартует вместе с движковыми пулами mawg"
	v.Caps.Start = v.Disabled || !v.Running
	v.Caps.Stop = !v.Disabled
	v.Caps.Uninstall = v.ManagedByMawg
	return v
}

// coreAutostart - сервисная автозагрузка (shared): OpenWrt - симлинк
// /etc/rc.d от procd, Keenetic/Entware - S-префикс имени скрипта.
func (s *Server) coreAutostart() bool {
	_, init := s.sharedPaths()
	if init == "" || s.store.SingboxMode() != "shared" {
		return false
	}
	rcDir := ""
	if s.backend.Name() != store.PlatformKeenetic {
		rcDir = "/etc/rc.d"
	}
	return singbox.AutostartEnabled(init, rcDir)
}

func (s *Server) singboxCoreView() coreView {
	v := coreView{ID: "singbox", Title: "sing-box (движок туннелей)"}
	eng, ok := s.detectCore()
	if !ok {
		v.Note = "не установлен (пункт sing-box-lx в таблице зависимостей выше)"
		return v
	}
	v.Installed, v.Version, v.LX = true, eng.Version, eng.LX
	v.Extended, v.Source = eng.Extended, eng.SourceID
	v.Mode = s.store.SingboxMode()
	v.ManagedByMawg = singbox.ManagedByMawg(singbox.LXTargetForPlatform(s.backend.Name()))
	v.Disabled = s.store.EngineDisabled()
	if mgr, err := s.sb(); err == nil {
		v.Running = mgr.Status().Running
	}
	if v.Mode == "shared" {
		v.Autostart = s.coreAutostart()
		v.Caps.Enable = !v.Autostart
		v.Caps.Disable = v.Autostart
	} else {
		// own-режим: ядро живёт внутри mawg - отдельной автозагрузки нет
		v.AutostartNote = "own-режим: ядро стартует вместе с движковыми пулами mawg"
	}
	v.Caps.Start = v.Disabled || !v.Running
	v.Caps.Stop = !v.Disabled
	v.Caps.Uninstall = v.ManagedByMawg
	return v
}

func (s *Server) getCores(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"cores": []coreView{s.singboxCoreView(), s.mihomoCoreView()},
	})
}

func (s *Server) postCorePower(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id != "singbox" && id != "mihomo" {
		writeErr(w, fmt.Errorf("неизвестное ядро %q", id))
		return
	}
	var req struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Action == "" {
		writeErr(w, fmt.Errorf("нужно действие: start|stop|restart|enable|disable|uninstall"))
		return
	}
	if id == "mihomo" {
		s.postMihomoPower(w, r, req.Action)
		return
	}
	ctx := r.Context()
	keenetic := s.backend.Name() == store.PlatformKeenetic
	tgt := singbox.LXTargetForPlatform(s.backend.Name())
	_, init := s.sharedPaths()
	log := func(msg string) { s.store.LogEvent("singbox", "power", msg) }
	switch req.Action {
	case "start":
		if err := s.store.SetEngineDisabled(false); err != nil {
			writeErr(w, err)
			return
		}
		if _, err := s.applyEngine(); err != nil {
			writeErr(w, err)
			return
		}
		log("движок включён, конфиг применён")
	case "stop":
		if err := s.stopCoreEngine(ctx); err != nil {
			writeErr(w, err)
			return
		}
		log("движок выключен: процесс остановлен, автозапуск пулов не применяется")
	case "restart":
		if err := s.stopCoreEngine(ctx); err != nil {
			writeErr(w, err)
			return
		}
		_ = s.store.SetEngineDisabled(false)
		if _, err := s.applyEngine(); err != nil {
			writeErr(w, err)
			return
		}
		log("движок перезапущен")
	case "enable", "disable":
		enable := req.Action == "enable"
		var err error
		if keenetic {
			err = singbox.SetKeeneticAutostart(init, enable)
		} else {
			err = singbox.SetOpenwrtAutostart(ctx, init, enable, nil)
		}
		if err != nil {
			writeErr(w, err)
			return
		}
		state := "выключена"
		if enable {
			state = "включена"
		}
		log("автозагрузка сервиса sing-box: " + state)
	case "uninstall":
		if !singbox.ManagedByMawg(tgt) {
			writeErr(w, fmt.Errorf("ядро ставили не из mawg - удаление из панели запрещено"))
			return
		}
		_ = s.stopCoreEngine(ctx)
		restored, err := singbox.UninstallLX(tgt)
		if err != nil {
			writeErr(w, err)
			return
		}
		s.resetSB()
		s.sysCache.reset()
		if restored {
			log("ядро lx удалено, прежний (чужой) sing-box восстановлен из бэкапа")
		} else {
			log("ядро lx удалено")
		}
	default:
		writeErr(w, fmt.Errorf("неизвестное действие %q", req.Action))
		return
	}
	s.coreDetectReset()
	writeJSON(w, http.StatusOK, map[string]any{"core": s.singboxCoreView()})
}

// postMihomoPower - старт/стоп/удаление ядра mihomo (own-режим: без
// сервисной автозагрузки, стартует вместе с движковыми пулами mawg).
func (s *Server) postMihomoPower(w http.ResponseWriter, r *http.Request, action string) {
	log := func(msg string) { s.store.LogEvent("mihomo", "power", msg) }
	switch action {
	case "start":
		if err := s.store.SetCoreDisabled("mihomo", false); err != nil {
			writeErr(w, err)
			return
		}
		if _, err := s.applyEngine(); err != nil {
			writeErr(w, err)
			return
		}
		log("ядро включено, конфиг применён")
	case "stop":
		if err := s.stopMihomoEngine(); err != nil {
			writeErr(w, err)
			return
		}
		log("ядро выключено: процесс остановлен, автозапуск пулов не применяется")
	case "restart":
		if err := s.stopMihomoEngine(); err != nil {
			writeErr(w, err)
			return
		}
		_ = s.store.SetCoreDisabled("mihomo", false)
		if _, err := s.applyEngine(); err != nil {
			writeErr(w, err)
			return
		}
		log("ядро перезапущено")
	case "enable", "disable":
		writeErr(w, fmt.Errorf("у mihomo нет сервисной автозагрузки: own-ядро стартует вместе с движковыми пулами mawg"))
		return
	case "uninstall":
		tgt := mihomo.TargetForPlatform(s.backend.Name())
		if !mihomo.ManagedByMawg(tgt) {
			writeErr(w, fmt.Errorf("ядро ставили не из mawg - удаление из панели запрещено"))
			return
		}
		_ = s.stopMihomoEngine()
		restored, err := mihomo.UninstallCore(tgt)
		if err != nil {
			writeErr(w, err)
			return
		}
		s.resetMH()
		s.sysCache.reset()
		if restored {
			log("mihomo удалён, прежний (чужой) бинарь восстановлен из бэкапа")
		} else {
			log("mihomo удалён")
		}
	default:
		writeErr(w, fmt.Errorf("неизвестное действие %q", action))
		return
	}
	s.coreDetectReset()
	writeJSON(w, http.StatusOK, map[string]any{"core": s.mihomoCoreView()})
}

// stopMihomoEngine - выключить ядро mihomo совсем: свой мастер-флаг ядра
// (CoreDisabled) + остановить процесс.
func (s *Server) stopMihomoEngine() error {
	if err := s.store.SetCoreDisabled("mihomo", true); err != nil {
		return err
	}
	if mgr, err := s.mh(); err == nil {
		mgr.StopOwn()
	}
	return nil
}

// stopCoreEngine - выключить движок совсем: мастер-флаг + остановить
// свой процесс / чужой сервис. Фрагмент из чужого конфига снимаем, чтобы
// при ручном старте сервиса не поднимались мёртвые mawg-туннели.
func (s *Server) stopCoreEngine(ctx context.Context) error {
	if err := s.store.SetEngineDisabled(true); err != nil {
		return err
	}
	_, init := s.sharedPaths()
	if s.store.SingboxMode() == "shared" {
		if mgr, err := s.sb(); err == nil {
			mgr.ClearSharedFragment()
		}
		if init != "" {
			if err := singbox.ServiceCmd(ctx, init, "stop", nil); err != nil {
				return fmt.Errorf("сервис sing-box не остановился: %v", err)
			}
		}
		return nil
	}
	if mgr, err := s.sb(); err == nil {
		mgr.StopOwn()
	}
	return nil
}
