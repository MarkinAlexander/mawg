package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"mawg/internal/links"
	"mawg/internal/mihomo"
	"mawg/internal/platform/openwrt"
	"mawg/internal/singbox"
	"mawg/internal/store"
	"mawg/internal/wgconf"
)

const engineMode = "singbox"

// engineCoreMihomo - второй движок туннелей (88-док); "" = sing-box.
const engineCoreMihomo = "mihomo"

// FreeProbeURL - фиксированная цель пробы Amnezia Free (см. web/free.go).
const FreeProbeURL = "http://1.1.1.1/"

var sbMu sync.Mutex
var sbMgr *singbox.Manager
var sbMode string

// sb отдаёт менеджер движка под текущий режим из настроек (own/shared);
// при смене режима старый менеджер закрывается: свой процесс гасится,
// у чужого ядра снимается наш фрагмент.
func (s *Server) sb() (*singbox.Manager, error) {
	sbMu.Lock()
	defer sbMu.Unlock()
	mode := s.store.SingboxMode()
	if sbMgr != nil && sbMode == mode {
		return sbMgr, nil
	}
	cfg := singbox.RunConfig{Mode: mode, WebPort: s.store.Settings().WebPort,
		// события таймерной ротации узлов - в журнал панели
		LogEvent: func(pool, kind, msg string) { s.store.LogEvent(pool, kind, msg) }}
	if mode == "shared" {
		cfg.SharedDir, cfg.SharedInit = s.sharedPaths()
		// маркер установки ядра: разрешает mawg стартовать остановленный
		// сервис в Apply (старт-if-managed, слово владельца 2026-10-29)
		cfg.MarkerDir = singbox.LXTargetForPlatform(s.backend.Name()).MarkerDir
	}
	m, err := singbox.NewManagerConfig(filepath.Join(s.store.Base(), "singbox"), cfg)
	if err != nil {
		return nil, err
	}
	if sbMgr != nil {
		sbMgr.Close()
	}
	sbMgr, sbMode = m, mode
	return sbMgr, nil
}

// resetSB сбрасывает кэш менеджера движка (после подмены бинаря следующий
// sb() пересоздаёт его со свежим детектом). Свой процесс гасим - иначе
// осиротеет с занятыми портами; shared-сервис не наш, просто забываем.
func (s *Server) resetSB() {
	sbMu.Lock()
	defer sbMu.Unlock()
	if sbMgr != nil && s.store.SingboxMode() != "shared" {
		sbMgr.Close()
	}
	sbMgr = nil
}

var mhMu sync.Mutex
var mhMgr *mihomo.Manager

// mh - менеджер ядра mihomo (только own-режим, 88-док): свой процесс со
// своим конфигом/портами (mixed 2381+, контроллер 2391).
func (s *Server) mh() (*mihomo.Manager, error) {
	mhMu.Lock()
	defer mhMu.Unlock()
	if mhMgr != nil {
		return mhMgr, nil
	}
	m, err := mihomo.NewManagerConfig(filepath.Join(s.store.Base(), "mihomo"), mihomo.RunConfig{
		WebPort: s.store.Settings().WebPort,
		// события таймерной ротации узлов - в журнал панели
		LogEvent: func(pool, kind, msg string) { s.store.LogEvent(pool, kind, msg) }})
	if err != nil {
		return nil, err
	}
	mhMgr = m
	return mhMgr, nil
}

// resetMH сбрасывает кэш менеджера mihomo (подмена бинаря/выключение):
// свой процесс гасится, чтобы не осиротел с занятыми портами.
func (s *Server) resetMH() {
	mhMu.Lock()
	defer mhMu.Unlock()
	if mhMgr != nil {
		mhMgr.Close()
	}
	mhMgr = nil
}

// engineManager - общий срез менеджеров ядер: clash-клиент + статусы
// пулов (у mihomo API тот же - он и есть эталонная реализация clash API).
type engineManager interface {
	ClashProxies(ctx context.Context) (map[string]singbox.ClashProxy, error)
	ClashSelect(ctx context.Context, group, name string) error
	ClashGroupDelay(ctx context.Context, group, testURL string, timeout time.Duration) (map[string]int, error)
	PoolStatus(name string) (singbox.PoolStatus, bool)
}

// engineMgr - менеджер ядра, которым крутится пул.
func (s *Server) engineMgr(p store.Pool) (engineManager, error) {
	if p.Settings.EngineCore == engineCoreMihomo {
		return s.mh()
	}
	return s.sb()
}

// engineCoreOf - id ядра пула для мастер-выключателей ("singbox"|"mihomo").
func engineCoreOf(p store.Pool) string {
	if p.Settings.EngineCore == engineCoreMihomo {
		return engineCoreMihomo
	}
	return "singbox"
}

// enginePoolActivate - сменить активный конфиг движкового пула: только
// запись стора + пересборка движка (rotator.SetActive недопустим - его
// applyConfig идёт в нативный backend.Apply). Слово владельца 30.10:
// движку подкладывается один конфиг, ротация = смена активного.
func (s *Server) enginePoolActivate(name, file string) error {
	p, ok := s.store.Pool(name)
	if !ok {
		return fmt.Errorf("пул %q не найден", name)
	}
	c, ok := p.ConfigByFile(file)
	if !ok || !c.Enabled {
		return fmt.Errorf("конфиг %q не найден или выключен", file)
	}
	s.store.MutateState(name, func(st *store.PoolState) {
		delete(st.Cooldowns, file)
	})
	if _, err := s.applyEngine(); err != nil {
		return err
	}
	s.store.MutateState(name, func(st *store.PoolState) {
		st.ActiveFile = file
		st.LastError = ""
	})
	s.store.LogEvent(name, "applied", "движковый пул: активный конфиг → "+file)
	return nil
}

// EngineConfigRotateLoop - автоповорот конфигов движковых пулов: движок
// пробует цель пула сам (PoolStatus), при устойчивых неудачах mawg
// подкидывает следующий конфиг и пересобирает движок - как ротация
// нативных пулов (слово владельца 30.10).
func (s *Server) EngineConfigRotateLoop(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	fails := map[string]int{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.rotateEngineConfigs(fails)
		}
	}
}

func (s *Server) rotateEngineConfigs(fails map[string]int) {
	for _, p := range s.enginePools() {
		if p.Disabled || len(p.Configs) < 2 {
			continue
		}
		mgr, err := s.engineMgr(p)
		if err != nil {
			continue
		}
		ps, ok := mgr.PoolStatus(p.Name)
		if !ok {
			continue
		}
		if ps.ProbeOK {
			delete(fails, p.Name)
			continue
		}
		threshold := p.Settings.FailThreshold
		if threshold <= 0 {
			threshold = 3
		}
		fails[p.Name]++
		if fails[p.Name] < threshold {
			continue
		}
		fails[p.Name] = 0
		// следующий включённый конфиг после активного, не в cooldown
		st := s.store.State(p.Name)
		start := p.IndexByFile(st.ActiveFile)
		next := ""
		for k := 1; k <= len(p.Configs); k++ {
			c := p.Configs[(start+k)%len(p.Configs)]
			if !c.Enabled {
				continue
			}
			if until, ok := st.Cooldowns[c.File]; ok && time.Now().Unix() < until {
				continue
			}
			next = c.File
			break
		}
		if next == "" || next == st.ActiveFile {
			continue
		}
		s.store.MutateState(p.Name, func(state *store.PoolState) {
			if state.ActiveFile != "" {
				state.Cooldowns[state.ActiveFile] = time.Now().Add(time.Duration(p.Settings.CooldownMin) * time.Minute).Unix()
			}
		})
		if err := s.enginePoolActivate(p.Name, next); err != nil {
			s.store.LogEvent(p.Name, "rotate", "движковый пул: ротация не удалась: "+err.Error())
		}
	}
}

// awg3Cores - какие ядра сейчас способны крутить конфиг, требующий
// движка (AWG 3.x / <r>-случайность на старом kmod): sing-box любого
// флейвора (lx несёт AWG-поля в корне, extended - объектом amnezia;
// билдер строит оба формата) и mihomo (amnezia-wg-option, Version 3).
// Учитываем мастер-выключатели ядер («Система -> Ядра VPN»). Слово
// владельца 30.10: гейты не должны затыкаться на sing-box-lx - движков
// может быть несколько, и движковый пул можно создать под любой из них.
func (s *Server) awg3Cores() (singboxOK, mihomoOK bool) {
	if !s.store.CoreDisabled("singbox") {
		if _, err := s.sb(); err == nil {
			singboxOK = true
		}
	}
	if !s.store.CoreDisabled("mihomo") {
		if _, err := s.mh(); err == nil {
			mihomoOK = true
		}
	}
	return
}

// awg3CorePick - выбрать ядро для движкового AWG-пула: явный выбор
// пользователя валидируется, пустой - автопик (sing-box, если жив;
// иначе mihomo). Возвращает нормализованное ядро ("" = singbox) и
// человеческое имя для сообщений.
func (s *Server) awg3CorePick(core string) (string, string, error) {
	sbOK, mhOK := s.awg3Cores()
	switch core {
	case "", "singbox":
		if sbOK {
			return "", "sing-box", nil
		}
		if core == "" && mhOK {
			return engineCoreMihomo, "mihomo", nil
		}
	case engineCoreMihomo:
		if mhOK {
			return engineCoreMihomo, "mihomo", nil
		}
	default:
		return "", "", fmt.Errorf("engineCore: singbox|mihomo")
	}
	if !sbOK && !mhOK {
		return "", "", fmt.Errorf("конфиг требует движок, но ни sing-box, ни mihomo не установлены - установите ядро в «Система -> Зависимости»")
	}
	if core == engineCoreMihomo {
		return "", "", fmt.Errorf("ядро mihomo не установлено (или выключено) - выберите sing-box или установите mihomo в «Система -> Зависимости»")
	}
	return "", "", fmt.Errorf("ядро sing-box не установлено (или выключено) - установите его в «Система -> Зависимости» или выберите mihomo")
}

func (s *Server) sharedPaths() (dir, init string) {
	if s.backend.Name() == store.PlatformKeenetic {
		return "/opt/etc/sing-box", "/opt/etc/init.d/S99sing-box"
	}
	return "/etc/sing-box", "/etc/init.d/sing-box"
}

// postSingboxMode переключает режим движка (own|shared): сохраняется в
// настройки, менеджер пересоздаётся (старый закрыт: свой процесс погашен /
// у чужого ядра снят фрагмент), затем движок применяется в новом режиме.
func (s *Server) postSingboxMode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	if req.Mode != "own" && req.Mode != "shared" {
		writeErr(w, fmt.Errorf("режим должен быть own или shared"))
		return
	}
	if err := s.store.SetSingboxMode(req.Mode); err != nil {
		writeErr(w, err)
		return
	}
	out := map[string]any{"ok": "saved", "mode": req.Mode}
	if _, err := s.sb(); err != nil {
		out["warning"] = err.Error()
		writeJSON(w, http.StatusOK, out)
		return
	}
	skipped, err := s.applyEngine()
	out["skipped"] = skipped
	if err != nil {
		out["warning"] = err.Error()
	}
	writeJSON(w, http.StatusOK, out)
}

// postCacheFile выключает (mode "off") или переносит в /tmp (mode "tmp")
// clash cache_file общего ядра: база пишет себя через mmap постоянно
// и изнашивает флешку.
func (s *Server) postCacheFile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	if req.Mode != singbox.CacheModeOff && req.Mode != singbox.CacheModeTmp {
		writeErr(w, fmt.Errorf("mode должен быть off или tmp"))
		return
	}
	mgr, err := s.sb()
	if err != nil {
		writeErr(w, err)
		return
	}
	if _, err := mgr.SetCacheFile(req.Mode); err != nil {
		s.store.LogEvent("singbox", "cachefile", "управление cache_file не удалось: "+err.Error())
		writeErr(w, err)
		return
	}
	verb := map[string]string{singbox.CacheModeOff: "выключен (запись ядра на флешку прекращена)",
		singbox.CacheModeTmp: "перенесён в " + singbox.CacheTmpPath}[req.Mode]
	s.store.LogEvent("singbox", "cachefile", "clash cache_file "+verb)
	writeJSON(w, http.StatusOK, map[string]string{"ok": req.Mode})
}

// RestoreEngine при старте mawg приводит ядра к текущим пулам: без этого
// после рестарта mawg пулы висят в «не применял конфиг» без проб (в shared
// фрагмент на диске уже верный, но статусы и пробы пустые). Выключенные
// ядра («Система → Ядра VPN») пропускаются с событием в журнал.
func (s *Server) RestoreEngine() {
	specs, err := s.engineSpecs()
	if err != nil {
		s.store.LogEvent("singbox", "applied", "восстановление движка при старте: "+err.Error())
		return
	}
	sbN, mhN := 0, 0
	for _, es := range specs {
		if es.core == engineCoreMihomo {
			mhN++
		} else {
			sbN++
		}
	}
	if sbN == 0 && mhN == 0 {
		return
	}
	if sbN > 0 && s.store.CoreDisabled("singbox") {
		s.store.LogEvent("singbox", "power", "движок sing-box выключен («Система → Ядра VPN») - его пулы не восстановлены")
	}
	if mhN > 0 && s.store.CoreDisabled("mihomo") {
		s.store.LogEvent("mihomo", "power", "ядро mihomo выключено («Система → Ядра VPN») - его пулы не восстановлены")
	}
	if (sbN > 0 && !s.store.CoreDisabled("singbox")) || (mhN > 0 && !s.store.CoreDisabled("mihomo")) {
		if _, err := s.applyEngine(); err != nil {
			s.store.LogEvent("singbox", "applied", "восстановление движков при старте: "+err.Error())
		}
	}
}

func (s *Server) enginePools() []store.Pool {
	var out []store.Pool
	for _, p := range s.store.Pools() {
		if p.Settings.EngineMode == engineMode {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// enginePoolsOfCore - движковые пулы конкретного ядра.
func (s *Server) enginePoolsOfCore(core string) []store.Pool {
	var out []store.Pool
	for _, p := range s.enginePools() {
		if core == engineCoreMihomo && p.Settings.EngineCore == engineCoreMihomo ||
			core != engineCoreMihomo && p.Settings.EngineCore != engineCoreMihomo {
			out = append(out, p)
		}
	}
	return out
}

func (s *Server) allocTun() string {
	taken := map[string]bool{}
	for _, p := range s.enginePools() {
		taken[p.Settings.TunName] = true
	}
	for i := 1; ; i++ {
		name := singbox.TuneName(i)
		if !taken[name] {
			return name
		}
	}
}

// claimTun выделяет свободный tun и сразу создаёт пул под блокировкой:
// два параллельных «Создать» не должны получить одно имя (задвоение tun2).
// Отдельно задвоение запрещает store.CreatePool; отключённые и ждущие-lx
// пулы имя тоже занимают.
func (s *Server) claimTun(settings store.PoolSettings, name string) (store.Pool, error) {
	s.tunMu.Lock()
	defer s.tunMu.Unlock()
	settings.TunName = s.allocTun()
	pool, err := s.store.CreatePool(name, settings)
	if err != nil {
		return store.Pool{}, err
	}
	return pool, nil
}

// applyEngineWarn - применить конфиг движка, приложив проблемы к плану.
func (s *Server) applyEngineWarn(plan *sourcePlan) {
	skipped, err := s.applyEngine()
	plan.Skipped = append(plan.Skipped, skipped...)
	if err != nil {
		plan.Warnings = append(plan.Warnings, "движок не применил конфиг: "+err.Error())
	}
}

// engineSpec - спецификация движкового пула + ядро, которое его крутит.
type engineSpec struct {
	core string // "" = sing-box | "mihomo"
	spec singbox.PoolSpec
}

// engineSpecs собирает спецификации ВСЕХ движковых пулов (оба ядра) -
// общая сборка, ядро определяет EngineCore пула. Задвоение tun ловится
// здесь же, до каких-либо менеджеров.
func (s *Server) engineSpecs() ([]engineSpec, error) {
	var specs []engineSpec
	for _, p := range s.enginePools() {
		if p.Disabled {
			continue
		}
		idx := 1
		if n, err := strconv.Atoi(strings.TrimPrefix(p.Settings.TunName, "tun")); err == nil && n > 0 {
			idx = n
		}
		// Каскад движка (detour у wireguard-эндпоинта) - идея ПРО ЗАПАС,
		// владельцем отвергнута: ядро - точка отказа для всего, что через
		// него заведено; каскады правильнее делать нативными средствами
		// роутера (интерфейсы + маршруты). Реализация wgEndpoint(detour)
		// сохранена в build.go, сюда не подключена.
		probe := p.Settings.ProbeHost
		if p.Free {
			// старые пулы могли сохранить цель без схемы (например «1.1.1.1») -
			// менеджер такое подменяет на gstatic; для Free цель фиксирована
			probe = FreeProbeURL
		}
		spec := singbox.PoolSpec{
			Name: p.Name, Tun: p.Settings.TunName, TunIP: singbox.TuneIP(idx),
			ProbeTarget:      probe,
			CheckIntervalSec: p.Settings.CheckIntervalSec,
			FailThreshold:    p.Settings.FailThreshold,
			CooldownMin:      p.Settings.CooldownMin,
			MaxRTTms:         p.Settings.MaxRTTms,
			GroupMode:        p.Settings.GroupMode,
			RotateSec:        p.Settings.RotateSec,
		}
		nodes, err := s.readPoolNodes(p.Name)
		switch {
		case err == nil && (len(nodes) > 0 || len(p.Configs) == 0):
			spec.Nodes = nodes
			// выключенные узлы в конфиг движка не попадают (слово
			// владельца 2026-10-17: позволить исключать, как у конфигов)
			if len(p.Settings.NodeOff) > 0 {
				kept := make([]links.Node, 0, len(spec.Nodes))
				for _, n := range spec.Nodes {
					if !p.Settings.NodeOff[n.ConfName()] {
						kept = append(kept, n)
					}
				}
				spec.Nodes = kept
			}
		case os.IsNotExist(err) && len(p.Configs) > 0:
			// пул без nodes.json, но с конфигами WG/AWG (Amnezia Free и
			// AWG 3.x-импорт) - движку подкладывается ОДИН активный конфиг
			// (95-док, слово владельца 30.10: у однотипных конфигов
			// одинаковый внутренний IP - все сразу создали бы коллизию;
			// ротация = смена активного + пересборка, как у нативных)
			st := s.store.State(p.Name)
			var active *store.ManagedConfig
			for i := range p.Configs {
				c := p.Configs[i]
				if !c.Enabled {
					continue
				}
				if c.File == st.ActiveFile {
					active = &p.Configs[i]
					break
				}
				if active == nil {
					active = &p.Configs[i]
				}
			}
			if active == nil {
				return nil, fmt.Errorf("пул %s: нет включённых конфигов", p.Name)
			}
			cfg, cerr := s.store.LoadConfigFile(p.Name, active.File)
			if cerr != nil {
				return nil, fmt.Errorf("пул %s: конфиг %s: %v", p.Name, active.File, cerr)
			}
			spec.WG = append(spec.WG, cfg)
		default:
			return nil, fmt.Errorf("пул %s: %v", p.Name, err)
		}
		specs = append(specs, engineSpec{core: p.Settings.EngineCore, spec: spec})
	}
	seen := map[string]string{}
	for _, es := range specs {
		if prev, dup := seen[es.spec.Tun]; dup {
			return nil, fmt.Errorf("tun %s назначен пулам %s и %s - исправьте настройки одного из них", es.spec.Tun, prev, es.spec.Name)
		}
		seen[es.spec.Tun] = es.spec.Name
	}
	return specs, nil
}

// applyEngine применяет конфиги ВСЕХ ядер: пулы раскладываются по
// EngineCore, каждое ядро получает свой список. Выключенное ядро
// («Система → Ядра VPN») пропускается; сбой одного ядра не валит
// другое - ошибки суммируются.
func (s *Server) applyEngine() ([]string, error) {
	specs, err := s.engineSpecs()
	if err != nil {
		return nil, err
	}
	var sbList, mhList []singbox.PoolSpec
	for _, es := range specs {
		if es.core == engineCoreMihomo {
			mhList = append(mhList, es.spec)
		} else {
			sbList = append(sbList, es.spec)
		}
	}
	var skipped []string
	var errs []string
	// sing-box: мастер-флаг EngineDisabled/CoreDisabled - конфиг не
	// строим, процесс не поднимаем (пулы показывают «движок выключен»)
	if !s.store.CoreDisabled("singbox") {
		sbMu.Lock()
		haveMgr := sbMgr != nil
		sbMu.Unlock()
		if len(sbList) > 0 || haveMgr {
			if mgr, err := s.sb(); err != nil {
				errs = append(errs, "sing-box: "+err.Error())
			} else {
				sk, aerr := mgr.Apply(sbList)
				// что mawg поправил в чужом config.json (clash_api, занятые
				// порты) - в журнал независимо от исхода Apply
				if note := mgr.PatchNote(); note != "" {
					s.store.LogEvent("singbox", "applied", note)
				}
				skipped = append(skipped, sk...)
				if aerr != nil {
					errs = append(errs, "sing-box: "+aerr.Error())
				}
			}
		}
	}
	if !s.store.CoreDisabled("mihomo") {
		if len(mhList) > 0 {
			mgr, err := s.mh()
			if err != nil {
				errs = append(errs, "mihomo: "+err.Error())
			} else if sk, aerr := mgr.Apply(mhList); aerr != nil {
				errs = append(errs, "mihomo: "+aerr.Error())
				skipped = append(skipped, sk...)
			} else {
				skipped = append(skipped, sk...)
			}
		} else {
			// пулов mihomo не осталось - погасить живой процесс
			s.resetMH()
		}
	}
	if len(errs) > 0 {
		return skipped, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return skipped, nil
}

func (s *Server) readPoolNodes(name string) ([]links.Node, error) {
	data, err := os.ReadFile(filepath.Join(s.store.Base(), "pools", name, "nodes.json"))
	if err != nil {
		return nil, err
	}
	var nodes []links.Node
	if err := json.Unmarshal(data, &nodes); err != nil {
		return nil, err
	}
	return nodes, nil
}

func (s *Server) writePoolNodes(name string, nodes []links.Node) error {
	data, err := json.MarshalIndent(nodes, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Join(s.store.Base(), "pools", name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "nodes.json"), data, 0o600)
}

func (s *Server) engineEnabled(r *http.Request) bool {
	name := r.PathValue("name")
	p, ok := s.store.Pool(name)
	return ok && p.Settings.EngineMode == engineMode
}

func (s *Server) postEnableEngine(w http.ResponseWriter, r *http.Request) {
	if err := s.store.SetPoolDisabled(r.PathValue("name"), false); err != nil {
		writeErr(w, err)
		return
	}
	if _, err := s.applyEngine(); err != nil {
		writeErr(w, err)
		return
	}
	s.store.LogEvent(r.PathValue("name"), "applied", "движок: включен")
	writeJSON(w, http.StatusOK, map[string]string{"ok": "enabled"})
}

func (s *Server) postDisableEngine(w http.ResponseWriter, r *http.Request) {
	if err := s.store.SetPoolDisabled(r.PathValue("name"), true); err != nil {
		writeErr(w, err)
		return
	}
	if _, err := s.applyEngine(); err != nil {
		writeErr(w, err)
		return
	}
	s.store.LogEvent(r.PathValue("name"), "applied", "движок: выключен")
	writeJSON(w, http.StatusOK, map[string]string{"ok": "disabled"})
}

func (s *Server) refreshSource(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	pool, ok := s.store.Pool(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	var req struct {
		Source  string `json:"source"`
		Country string `json:"country"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	src := strings.TrimSpace(req.Source)
	if src == "" {
		src = pool.Settings.Source
	}
	if src == "" {
		writeErr(w, fmt.Errorf("у пула нет сохранённого источника - вставьте ссылку"))
		return
	}
	if _, busy := subRefreshBusy.LoadOrStore(name, struct{}{}); busy {
		writeErr(w, fmt.Errorf("обновление уже выполняется"))
		return
	}
	defer subRefreshBusy.Delete(name)
	res, err := s.resolvePoolSource(r.Context(), pool, src, req.Country)
	if err != nil {
		writeErr(w, err)
		return
	}
	plan := sourcePlan{Warnings: res.sub.Warnings, Amnezia: res.amnezia}
	if src != pool.Settings.Source || res.amnezia != nil {
		st := pool.Settings
		st.Source = src
		if res.amnezia != nil {
			st.Amnezia = storeAmnezia(res.amnezia)
		}
		if err := s.store.UpdatePool(name, st); err != nil {
			writeErr(w, err)
			return
		}
	}
	if pool.Settings.EngineMode == engineMode {
		if len(res.sub.Nodes) == 0 {
			writeErr(w, fmt.Errorf("в источнике нет узлов"))
			return
		}
		if err := s.writePoolNodes(name, res.sub.Nodes); err != nil {
			writeErr(w, err)
			return
		}
		skipped, err := s.applyEngine()
		plan.Skipped = skipped
		if err != nil {
			plan.Warnings = append(plan.Warnings, err.Error())
		}
		plan.Pool = name
		s.recordSourceRefresh(name, res.bodyHash, res.intervalH)
		writeJSON(w, http.StatusOK, plan)
		return
	}
	for _, c := range pool.Configs {
		if err := s.store.RemoveConfig(name, c.File); err != nil {
			writeErr(w, err)
			return
		}
	}
	ncs, engineNodes, skipped := buildNativeConfigs(res.sub.Nodes)
	// Гейт движковых конфигов (как в uploadConfigs, слово владельца
	// 30.10): в нативный пул AWG 3.x не попадает - не resurrectить
	// нежизнеспособные конфиги обновлением из источника.
	if pool, ok := s.store.Pool(name); ok && pool.Settings.EngineMode == "" {
		rNeedEngine := false
		if ob, ok := s.backend.(*openwrt.Backend); ok && !ob.SupportsNativeAWG3() {
			rNeedEngine = true
		}
		native3 := s.nativeAWG3()
		installed := ""
		if sbOK, mhOK := s.awg3Cores(); sbOK || mhOK {
			installed = installedEngineNames(sbOK, mhOK)
		}
		var keep []wgconf.NamedConfig
		for _, nc := range ncs {
			if (nc.Config.NeedsEngine() && !native3) || (rNeedEngine && nc.Config.AWG.HasRandomInit()) {
				if installed == "" {
					plan.Skipped = append(plan.Skipped, nc.OriginalName+": конфиг AmneziaWG 3.x не поднимется - ни sing-box, ни mihomo не установлены («Система -> Зависимости»)")
				} else {
					plan.Skipped = append(plan.Skipped, nc.OriginalName+": конфиг AmneziaWG 3.x в нативном пуле не поднимется (движок "+installed+" установлен - нужен движковый пул)")
				}
				continue
			}
			keep = append(keep, nc)
		}
		ncs = keep
	}
	plan.Skipped = append(plan.Skipped, skipped...)
	if len(ncs) > 0 {
		added, dupes, err := s.store.AddConfigs(name, ncs)
		if err != nil {
			writeErr(w, err)
			return
		}
		plan.Pool, plan.Added, plan.Duplicates = name, added, dupes
		plan.Engine = engineNodes
		if p, ok := s.store.Pool(name); ok && len(p.Configs) > 0 {
			if err := s.engine.SetActive(name, p.Configs[0].File); err != nil {
				plan.Warnings = append(plan.Warnings, "не активирован: "+err.Error())
			}
		}
		s.engine.CheckNow(name)
	}
	s.recordSourceRefresh(name, res.bodyHash, res.intervalH)
	writeJSON(w, http.StatusOK, plan)
}
