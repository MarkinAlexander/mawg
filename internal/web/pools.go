package web

// Хендлеры/логика пулов и их конфигов (из server.go).

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"mawg/internal/platform/keenetic"
	"mawg/internal/platform/openwrt"
	"mawg/internal/store"
	"mawg/internal/wgconf"
)

func normAddr(a string) string {
	return strings.TrimSpace(strings.SplitN(strings.TrimSpace(a), "/", 2)[0])
}

// configConflict: первый занятый адрес конфига (не считая собственного
// интерфейса пула) с описанием, пусто если конфликтов нет.
func (s *Server) configConflict(poolName, device string, addrs []string) string {
	if len(addrs) == 0 {
		return ""
	}
	for addr, dev := range s.engine.TunnelAddresses() {
		if dev == device || dev == "" {
			continue
		}
		for _, a := range addrs {
			if normAddr(a) == normAddr(addr) {
				return fmt.Sprintf("адрес %s уже занят интерфейсом %s", a, dev)
			}
		}
	}
	return ""
}

// poolConfigAddrs: адреса конфига из реестра, старые записи добираются из
// файла и запоминаются.
func (s *Server) poolConfigAddrs(poolName string, cfg store.ManagedConfig) []string {
	if len(cfg.Addresses) > 0 {
		return cfg.Addresses
	}
	parsed, err := s.store.LoadConfigFile(poolName, cfg.File)
	if err != nil {
		return nil
	}
	s.store.SetConfigAddresses(poolName, cfg.File, parsed.Addresses)
	return parsed.Addresses
}

// liveAddressConflict: конфликт по свежим адресам интерфейсов (для ручных
// действий: включение тумблером, активация).
func (s *Server) liveAddressConflict(poolName, file string) error {
	pool, ok := s.store.Pool(poolName)
	if !ok {
		return nil
	}
	cfg, err := s.store.LoadConfigFile(poolName, file)
	if err != nil {
		return nil
	}
	tunnels, err := s.backend.SysTunnels()
	if err != nil {
		return nil
	}
	own := pool.DeviceName()
	for _, a := range cfg.Addresses {
		for _, sl := range tunnels {
			// выключенный интерфейс адрес не держит: выключенный в UI
			// Кинетика WARP не должен мешать включить конфиг с тем же
			// адресом - мешают только два одновременно поднятых
			if sl.Device == own || sl.Address == "" || !sl.LinkUp {
				continue
			}
			if normAddr(sl.Address) == normAddr(a) {
				return fmt.Errorf("адрес %s из конфига уже занят интерфейсом %s: отключите его или выберите другой конфиг", a, sl.Device)
			}
		}
	}
	return nil
}

func (s *Server) renamePool(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeErr(w, fmt.Errorf("нужно имя"))
		return
	}
	old := r.PathValue("name")
	if s.backend.Name() != store.PlatformKeenetic {
		writeErr(w, fmt.Errorf("переименование на этой платформе не поддерживается (имя = имя системного интерфейса)"))
		return
	}
	pool, ok := s.store.Pool(old)
	if !ok {
		http.NotFound(w, r)
		return
	}
	renamed, err := s.engine.RenamePool(old, req.Name)
	if err != nil {
		writeErr(w, err)
		return
	}
	if kb, ok := s.backend.(*keenetic.Backend); ok && pool.Settings.KeeneticSlot != "" {
		kb.Ndmc("interface " + pool.Settings.KeeneticSlot + " description " + renamed.Name)
		kb.Ndmc("system configuration save")
	}
	s.store.LogEvent(renamed.Name, "rename", "пул переименован из "+old)
	s.engine.CheckNow(renamed.Name)
	writeJSON(w, http.StatusOK, renamed)
}

func (s *Server) createPool(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name         string `json:"name"`
		KeeneticSlot string `json:"keeneticSlot"`
		OpenwrtProto string `json:"openwrtProto"`
		Fallback     string `json:"fallback"`
		ProbeHost    string `json:"probeHost"`
		// Пустой пул сразу на движке (слово владельца 30.10): конфиги
		// можно лить после создания - движок крутит их и AWG 3.x.
		EngineMode string `json:"engineMode"`
		EngineCore string `json:"engineCore"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	settings := store.PoolSettings{
		Platform:     s.backend.Name(),
		KeeneticSlot: req.KeeneticSlot,
		Fallback:     req.Fallback,
		ProbeHost:    req.ProbeHost,
	}
	if req.EngineMode != "" {
		if req.EngineMode != engineMode {
			writeErr(w, fmt.Errorf("engineMode: пусто или %q", engineMode))
			return
		}
		core, _, err := s.awg3CorePick(req.EngineCore)
		if err != nil {
			writeErr(w, err)
			return
		}
		if core != "" {
			settings.EngineCore = core
		}
		settings.EngineMode = engineMode
	}
	if s.backend.Name() == store.PlatformOpenwrt {
		settings.OpenwrtProto = req.OpenwrtProto
		if settings.OpenwrtProto == "" {
			settings.OpenwrtProto = "wireguard"
		}
	}
	if req.EngineMode != "" {
		// движковому пулу слот Wireguard не нужен - сразу tun
		if err := s.validFallback(req.Name, settings.Fallback); err != nil {
			writeErr(w, err)
			return
		}
		pool, err := s.claimTun(settings, req.Name)
		if err != nil {
			writeErr(w, err)
			return
		}
		s.store.LogEvent(pool.Name, "applied", "создан пустой пул на движке "+map[bool]string{true: "mihomo", false: "sing-box"}[pool.Settings.EngineCore == engineCoreMihomo])
		writeJSON(w, http.StatusOK, pool)
		return
	}
	slot, err := s.resolveKeeneticSlot(req.KeeneticSlot)
	if err != nil {
		writeErr(w, err)
		return
	}
	settings.KeeneticSlot = slot
	if err := s.validFallback(req.Name, settings.Fallback); err != nil {
		writeErr(w, err)
		return
	}
	pool, err := s.store.CreatePool(req.Name, settings)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pool)
}

func (s *Server) updatePool(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	pool, ok := s.store.Pool(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	var patch store.PoolSettings
	var maxRtt *int
	var updateIntervalH *float64
	{
		var raw struct {
			store.PoolSettings
			MaxRTTms        *int     `json:"maxRttMs"`
			UpdateIntervalH *float64 `json:"updateIntervalH"`
		}
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			writeErr(w, err)
			return
		}
		patch = raw.PoolSettings
		maxRtt = raw.MaxRTTms
		updateIntervalH = raw.UpdateIntervalH
	}
	merged := pool.Settings
	if patch.ProbeHost != "" {
		merged.ProbeHost = patch.ProbeHost
	}
	if patch.CheckIntervalSec > 0 {
		merged.CheckIntervalSec = patch.CheckIntervalSec
	}
	if patch.FailThreshold > 0 {
		merged.FailThreshold = patch.FailThreshold
	}
	if patch.CooldownMin > 0 {
		merged.CooldownMin = patch.CooldownMin
	}
	if patch.Fallback != "" {
		merged.Fallback = patch.Fallback
	}
	if patch.Keepalive > 0 {
		merged.Keepalive = patch.Keepalive
	}
	if maxRtt != nil {
		merged.MaxRTTms = *maxRtt
	}
	if updateIntervalH != nil {
		if *updateIntervalH < 0 || *updateIntervalH > 8760 {
			writeErr(w, fmt.Errorf("интервал автообновления должен быть 0 (из подписки) или 1-8760 часов"))
			return
		}
		merged.UpdateIntervalH = *updateIntervalH
	}
	if patch.MagitrickleGroupID != "" {
		merged.MagitrickleGroupID = patch.MagitrickleGroupID
	}
	if patch.GroupMode != "" {
		if patch.GroupMode != "urltest" && patch.GroupMode != "selector" && patch.GroupMode != "rotate" {
			writeErr(w, fmt.Errorf("groupMode: urltest|selector|rotate"))
			return
		}
		merged.GroupMode = patch.GroupMode
	}
	if patch.RotateSec != 0 {
		if patch.RotateSec < 10 || patch.RotateSec > 86400 {
			writeErr(w, fmt.Errorf("интервал ротации: 10-86400 секунд"))
			return
		}
		merged.RotateSec = patch.RotateSec
	}
	// Ядро движкового пула (88-док): "" и "singbox" - одно и то же
	// (нормализуем в ""), "mihomo" - второй движок; переключение живого
	// пула пересобирает конфиги обоих ядер (engineRebuild ниже).
	if patch.EngineCore != "" {
		if patch.EngineCore != "singbox" && patch.EngineCore != "mihomo" {
			writeErr(w, fmt.Errorf("engineCore: singbox|mihomo"))
			return
		}
		if pool.Settings.EngineMode != engineMode {
			writeErr(w, fmt.Errorf("ядро движка выбирается только у движкового пула"))
			return
		}
		merged.EngineCore = patch.EngineCore
		if merged.EngineCore == "singbox" {
			merged.EngineCore = ""
		}
	}
	// Исключения пула (94-док): nil = не трогалось, [] = очистить.
	// При выключенном функционале (95-док) поле отклоняется - nil
	// («не трогать») остаётся легальным.
	if patch.Excludes != nil && s.store.ExcludesDisabled() {
		writeErr(w, errors.New(exclDisabledMsg))
		return
	}
	if patch.Excludes != nil {
		list, err := poolExcludesValid(patch.Excludes)
		if err != nil {
			writeErr(w, err)
			return
		}
		merged.Excludes = list
	}
	// Каскад движка (detour) владельцем отвергнут: всё, заведённое через
	// ядро, умирает вместе с ядром, а на OpenWrt без sing-box юзер вообще
	// ничего не получит. Поле EngineDetour оставлено в схеме про запас -
	// к конфигу движка не применяется, из UI убрано.
	_ = patch.EngineDetour
	if patch.SmartCascade != nil {
		sc, err := s.validSmartCascade(pool, patch.SmartCascade)
		if err != nil {
			writeErr(w, err)
			return
		}
		if sc == nil && pool.Settings.SmartCascade != nil {
			// настройки сняты при живом автомате - честно выключить каскад,
			// иначе он остался бы включённым навсегда без расписания
			if st := s.store.State(name); st.Smart != nil && st.Smart.On {
				if err := s.engine.SmartCascadeSetManual(name, false); err != nil {
					s.store.LogEvent(name, "smart-cascade", "не выключил каскад при снятии настроек: "+err.Error())
				}
			}
		}
		merged.SmartCascade = sc
	}
	if pool.Free {
		merged.ProbeHost = FreeProbeURL
	}
	if err := s.validFallback(name, merged.Fallback); err != nil {
		writeErr(w, err)
		return
	}
	engineRebuild := pool.Settings.EngineMode == engineMode
	if err := s.store.UpdatePool(name, merged); err != nil {
		writeErr(w, err)
		return
	}
	if patch.Excludes != nil {
		if len(merged.Excludes) == 0 {
			s.store.LogEvent(name, "excludes", "исключения пула очищены")
		} else {
			s.store.LogEvent(name, "excludes", "исключения пула применены ("+fmt.Sprint(len(merged.Excludes))+"): "+strings.Join(merged.Excludes, ", "))
		}
		s.syncExclusionsNow()
	}
	if merged.SmartCascade != nil {
		// служебная группа каскада - сразу при сохранении настроек,
		// выключена до триггера (слово владельца, 86-док); при недоступном
		// MT долечит домашка (EnsureErr в стейте виден в панели)
		s.engine.EnsureSmartCascade(name)
	}
	if engineRebuild {
		if _, err := s.applyEngine(); err != nil {
			s.store.LogEvent(name, "applied", "движок не пересобран после смены настроек: "+err.Error())
		}
		writeJSON(w, http.StatusOK, map[string]string{"ok": "updated"})
		return
	}
	s.engine.CheckNow(name)
	writeJSON(w, http.StatusOK, map[string]string{"ok": "updated"})
}

func (s *Server) deletePool(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	// судьба групп устройств пула (97-док, слово владельца 30.10):
	// «удаление пула должно удалять группу, а если пользователь не
	// удаляет - перевести на директ». Дефолт direct: выключенная группа
	// не маркирует - устройства идут напрямую, а не в мёртвый интерфейс.
	devices := r.URL.Query().Get("devices")
	switch devices {
	case "", "delete", "direct":
	default:
		writeErr(w, fmt.Errorf("неизвестный режим устройств %q (delete|direct)", devices))
		return
	}
	wasEngine := s.engineEnabled(r)
	var err error
	if wasEngine {
		err = s.store.DeletePool(name)
	} else {
		err = s.engine.DeletePool(name)
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	if wasEngine {
		if _, err := s.applyEngine(); err != nil {
			s.store.LogEvent(name, "applied", "движок не пересобран после удаления: "+err.Error())
		}
	}
	s.cascSyncPool(name) // каскады с этим источником погасятся
	warn := s.deletePoolDeviceGroups(r.Context(), name, devices)
	if warn != nil {
		writeJSON(w, http.StatusOK, map[string]string{"ok": "deleted", "warning": warn.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "deleted"})
}

func (s *Server) uploadConfigs(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, ok := s.store.Pool(name); !ok {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeErr(w, err)
		return
	}
	var all []wgconf.NamedConfig
	var errors []string
	for _, headers := range r.MultipartForm.File {
		for _, fh := range headers {
			f, err := fh.Open()
			if err != nil {
				errors = append(errors, fh.Filename+": "+err.Error())
				continue
			}
			data, _ := io.ReadAll(f)
			f.Close()
			parsed, err := wgconf.IngestFile(fh.Filename, data)
			if err != nil {
				errors = append(errors, err.Error())
				continue
			}
			all = append(all, parsed...)
		}
	}
	unique, dupes := wgconf.Dedupe(all)
	// Некорректная комбинация 3.x (116-док): HP-ключ при S1-S4 < 12
	// отвергают и прошивка 5.2, и ядро lx - сохранять нельзя, конфиг
	// мёрт на любом пути (натив и движок).
	if len(unique) > 0 {
		var keep []wgconf.NamedConfig
		for _, nc := range unique {
			if issue := nc.Config.AWG.HPIssue(); issue != "" {
				errors = append(errors, nc.OriginalName+": "+issue)
				continue
			}
			keep = append(keep, nc)
		}
		unique = keep
	}
	// Гейт нежизнеспособных конфигов (слово владельца 30.10: «не должно
	// даже пытаться создавать пулы, а то ерунда выходит»): в нативный
	// пул конфиг, требующий движка (AWG 3.x / <r>-случайность на старом
	// kmod), НЕ сохраняется - иначе пул живёт с вечным «apply failed».
	// Текст называет установленные движки (sing-box любого флейвора,
	// mihomo) - не только sing-box-lx.
	if pool, ok := s.store.Pool(name); ok && pool.Settings.EngineMode == "" {
		rNeedEngine := false
		if ob, ok := s.backend.(*openwrt.Backend); ok && !ob.SupportsNativeAWG3() {
			rNeedEngine = true
		}
		// Keenetic 5.2+ / OpenWrt kmod 3.x поднимают 3.x нативно
		// (asc-хвост из 31 аргумента) - гейт их не трогает
		native3 := s.nativeAWG3()
		sbOK, mhOK := s.awg3Cores()
		installed := installedEngineNames(sbOK, mhOK)
		var keep []wgconf.NamedConfig
		for _, nc := range unique {
			if !((nc.Config.NeedsEngine() && !native3) || (rNeedEngine && nc.Config.AWG.HasRandomInit())) {
				keep = append(keep, nc)
				continue
			}
			if installed == "" {
				errors = append(errors, nc.OriginalName+": конфиг AmneziaWG 3.x не поднимется: прошивка ниже 5.2 нативно его не применяет, ни sing-box, ни mihomo не установлены - установите движок в «Система -> Зависимости» или создайте пул на движке после установки")
				continue
			}
			errors = append(errors, nc.OriginalName+": конфиг AmneziaWG 3.x в нативном пуле не поднимется - создайте движковый пул («Новый интерфейс -> конфиги .conf / .zip», ядро: "+installed+")")
		}
		if len(keep) < len(unique) {
			unique = keep
		}
		// AWG-конфиг в нативном пуле OpenWrt живёт на proto amneziawg
		// (Apply выбирает его по конфигу сам); чиним и настройку пула,
		// чтобы статус пула читался инструментом awg, а не wg
		if s.backend.Name() == store.PlatformOpenwrt && pool.Settings.OpenwrtProto != "amneziawg" {
			for _, nc := range unique {
				if nc.Config.AWG.Present() || nc.Config.AWG.AWG3() {
					pool.Settings.OpenwrtProto = "amneziawg"
					if err := s.store.UpdatePool(name, pool.Settings); err == nil {
						s.store.LogEvent(name, "applied", "пул переведён на proto amneziawg: загружен AWG-конфиг")
					}
					break
				}
			}
		}
	}
	var added int
	if len(unique) > 0 {
		var storeDupes []string
		var err error
		added, storeDupes, err = s.store.AddConfigs(name, unique)
		if err != nil {
			writeErr(w, err)
			return
		}
		dupes = append(dupes, storeDupes...)
	}
	// помечаем конфликтные: адрес занят другим интерфейсом сейчас ->
	// в ротацию не попадает, пока конфликт не исчезнет
	var warnings []string
	if pool, ok := s.store.Pool(name); ok {
		for _, c := range pool.Configs {
			if !c.Enabled {
				continue
			}
			if msg := s.configConflict(name, pool.DeviceName(), s.poolConfigAddrs(name, c)); msg != "" {
				_ = s.store.SetConfigEnabled(name, c.File, false)
				warnings = append(warnings, c.Original+": "+msg+", исключен из ротации")
			}
		}
	}
	// Конфликтные адреса помечает configConflict ниже; гейт движковых
	// конфигов отработал ДО записи - «конфиг сохранён» тут больше не
	// бывает (95-док, слово владельца 30.10).
	for _, w := range warnings {
		s.store.LogEvent(name, "conflict", w)
	}
	if added == 0 && len(unique) == 0 && len(errors) > 0 {
		writeErr(w, fmt.Errorf("%s", errors[len(errors)-1]))
		return
	}
	// Движковый пул: конфиги WG/AWG едут wireguard-эндпоинтами движка
	// (engineSpecs) - пересобираем сразу, чтобы конфиг появился живьём.
	if pool, ok := s.store.Pool(name); ok && pool.Settings.EngineMode == engineMode && added > 0 {
		if st := s.store.State(name); st.ActiveFile == "" {
			for _, c := range pool.Configs {
				if c.Enabled {
					s.store.MutateState(name, func(state *store.PoolState) {
						state.ActiveFile = c.File
					})
					break
				}
			}
		}
		if _, err := s.applyEngine(); err != nil {
			warnings = append(warnings, "движок не пересобран после загрузки: "+err.Error())
		}
	}
	s.engine.CheckNow(name)
	s.cascSyncPool(name)
	writeJSON(w, http.StatusOK, map[string]any{"added": added, "duplicates": dupes, "errors": errors, "warnings": warnings})
}

// installedEngineNames - человеческий перечень установленных движков
// для сообщений гейта AWG 3.x ("" = ни одного).
func installedEngineNames(sbOK, mhOK bool) string {
	var names []string
	if sbOK {
		names = append(names, "sing-box")
	}
	if mhOK {
		names = append(names, "mihomo")
	}
	return strings.Join(names, " / ")
}

func (s *Server) deleteConfig(w http.ResponseWriter, r *http.Request) {
	if err := s.store.RemoveConfig(r.PathValue("name"), r.PathValue("file")); err != nil {
		writeErr(w, err)
		return
	}
	// удаление последнего/активного конфига должно сразу отразиться на
	// туннеле, а не ждать очередного цикла с «работающим» интерфейсом
	if pool, ok := s.store.Pool(r.PathValue("name")); ok && pool.Settings.EngineMode == engineMode {
		if _, err := s.applyEngine(); err != nil {
			s.store.LogEvent(r.PathValue("name"), "applied", "движок не пересобран после удаления конфига: "+err.Error())
		}
	} else {
		s.engine.CheckNow(r.PathValue("name"))
	}
	s.cascSyncPool(r.PathValue("name"))
	writeJSON(w, http.StatusOK, map[string]string{"ok": "deleted"})
}

func (s *Server) enableConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.Enabled {
		if err := s.liveAddressConflict(r.PathValue("name"), r.PathValue("file")); err != nil {
			writeErr(w, err)
			return
		}
	}
	if err := s.store.SetConfigEnabled(r.PathValue("name"), r.PathValue("file"), req.Enabled); err != nil {
		writeErr(w, err)
		return
	}
	// Движковый пул: включённость конфига = состав spec.WG - пересборка.
	if pool, ok := s.store.Pool(r.PathValue("name")); ok && pool.Settings.EngineMode == engineMode {
		if _, err := s.applyEngine(); err != nil {
			s.store.LogEvent(r.PathValue("name"), "applied", "движок не пересобран после переключения конфига: "+err.Error())
		}
	}
	// выключение последнего активного конфига - тот же случай: без
	// немедленной перепроверки туннель продолжит работать
	if !req.Enabled {
		s.engine.CheckNow(r.PathValue("name"))
	}
	s.cascSyncPool(r.PathValue("name"))
	writeJSON(w, http.StatusOK, map[string]string{"ok": "ok"})
}

func (s *Server) moveConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Delta int `json:"delta"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.Delta != -1 && req.Delta != 1 {
		writeErr(w, fmt.Errorf("смещение должно быть -1 или 1"))
		return
	}
	if err := s.store.MoveConfig(r.PathValue("name"), r.PathValue("file"), req.Delta); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "moved"})
}

func (s *Server) postRotate(w http.ResponseWriter, r *http.Request) {
	if s.engineEnabled(r) {
		writeErr(w, fmt.Errorf("пул работает через движок: узлы переключаются в его группе, ротация не нужна"))
		return
	}
	if err := s.engine.RotateNow(r.PathValue("name")); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "rotating"})
}

func (s *Server) postCheck(w http.ResponseWriter, r *http.Request) {
	if s.engineEnabled(r) {
		writeErr(w, fmt.Errorf("пул работает через движок: проверка выполняется автоматически"))
		return
	}
	s.engine.CheckNow(r.PathValue("name"))
	// строка интерфейса показывает ту же пробу - после «Проверить» пусть
	// обновится сразу, а не через TTL кэша /ifaces
	s.ifaceCache.reset()
	writeJSON(w, http.StatusOK, map[string]string{"ok": "checking"})
}

func (s *Server) postEnable(w http.ResponseWriter, r *http.Request) {
	if s.engineEnabled(r) {
		s.postEnableEngine(w, r)
		return
	}
	if err := s.engine.EnablePool(r.PathValue("name")); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "enabled"})
}

func (s *Server) postDisable(w http.ResponseWriter, r *http.Request) {
	if s.engineEnabled(r) {
		s.postDisableEngine(w, r)
		return
	}
	if err := s.engine.DisablePool(r.PathValue("name")); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "disabled"})
}

func (s *Server) postActivate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		File string `json:"file"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.File == "" {
		writeErr(w, err)
		return
	}
	if err := s.liveAddressConflict(r.PathValue("name"), req.File); err != nil {
		writeErr(w, err)
		return
	}
	// Движковый пул: rotator.SetActive недопустим (его applyConfig идёт
	// в нативный backend.Apply) - меняем активный конфиг в сторе и
	// пересобираем движок (95-док, слово владельца 30.10).
	if pool, ok := s.store.Pool(r.PathValue("name")); ok && pool.Settings.EngineMode == engineMode {
		if err := s.enginePoolActivate(r.PathValue("name"), req.File); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"ok": "activated"})
		return
	}
	if err := s.engine.SetActive(r.PathValue("name"), req.File); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "activated"})
}
