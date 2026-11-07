package rotator

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"mawg/internal/cascade"
	"mawg/internal/magitrickle"
	"mawg/internal/platform"
	"mawg/internal/premium"
	"mawg/internal/store"
)

const settleSeconds = 15
const minApplyInterval = 20 * time.Second

type Engine struct {
	PremiumClient *premium.Client
	store         *store.Store
	backend       platform.Backend
	mt            *magitrickle.Client

	Clock func() time.Time

	WANProbeFn func() bool

	wake        chan string
	stop        context.CancelFunc
	mu          sync.Mutex
	actMu       sync.Mutex
	lastApply   map[string]time.Time
	extFails    map[string]int
	extSamples  map[string]*probeCacheEntry
	wanCache    wanCacheEntry
	mtHealAt    time.Time
	mtEverAlive bool
	mtRestarts  []time.Time // автоперезапуски за скользящий час (бюджет)
	mtSyncAt    time.Time
	// lastProbeVersion - версия MT на момент последней пробы совместимости
	// (перепроверка при смене версии: пользователь обновил MT - липкий
	// флаг глючности с прошлого старта панели должен сняться сам).
	lastProbeVersion string

	ifaceAt time.Time

	probeMu     sync.Mutex
	probeCache  map[string]*probeCacheEntry
	tunnelAddrs map[string]string
	tunnelAt    time.Time

	casc       *cascade.Manager
	tickN      int
	polFails   map[string]int
	polOks     map[string]int
	polSamples map[string]*probeCacheEntry

	lane     map[string]time.Time // фоновые пробы в полёте: пул -> старт
	laneMu   sync.Mutex
	startDue map[string]time.Time // стартовый разброс первых проб: пул -> не раньше

	// smartPause - пауза между двумя прогонами пробы умного каскада
	// (хендшейк через Via/напрямую требует секунд; тесты сжимают)
	smartPause time.Duration
}

// ifaceTouched отмечает момент нашей операции с интерфейсами: кинетик после
// этого перезагружает свой файрвол (хук netfilter.d), и iptables-restore
// магитрикла в эти секунды падает. записи в магитрикл ждут паузу.
func (e *Engine) ifaceTouched() {
	e.mu.Lock()
	e.ifaceAt = e.now()
	e.mu.Unlock()
	e.probeMu.Lock()
	e.tunnelAt = time.Time{}
	e.probeMu.Unlock()
}

func (e *Engine) settleAfterIfaceOp() {
	e.mu.Lock()
	at := e.ifaceAt
	e.mu.Unlock()
	if at.IsZero() {
		return
	}
	if d := 3*time.Second - e.now().Sub(at); d > 0 {
		time.Sleep(d)
	}
}

func New(st *store.Store, b platform.Backend, mt *magitrickle.Client) *Engine {
	e := &Engine{
		PremiumClient: premium.NewClient(),
		store:         st,
		backend:       b,
		mt:            mt,
		wake:          make(chan string, 32),
		lastApply:     map[string]time.Time{},
		extFails:      map[string]int{},
		extSamples:    map[string]*probeCacheEntry{},
		probeCache:    map[string]*probeCacheEntry{},
		tunnelAddrs:   map[string]string{},
		polFails:      map[string]int{},
		polOks:        map[string]int{},
		polSamples:    map[string]*probeCacheEntry{},
		lane:          map[string]time.Time{},
		startDue:      map[string]time.Time{},
		smartPause:    5 * time.Second,
	}
	if mt != nil {
		e.casc = cascade.New(st, mt)
		e.casc.Retry = e.RetryMTWrite
		// BeforeWrite - окно мутации на клиенте: фактическая запись групп
		// (движок, панель, рестор, импорт) снимает OUTPUT-хуки и возвращает
		// их сразу после записи (AfterMutate -> SyncHooks); no-op-мутация
		// без PUT хуки не трогает. settle идёт под groupsMu клиента - все
		// писатели и так сериализуются на нём.
		mt.BeforeWrite = func() {
			e.settleAfterIfaceOp()
			e.casc.BeforeMutate(nil)
		}
		mt.AfterWrite = e.casc.AfterMutate
		// iface-источники каскадов: авторезолв эндпоинтов роутерных WG
		if ep, ok := b.(platform.Endpointer); ok {
			e.casc.EP = ep
		}
	}
	return e
}

// Cascade - менеджер служебных групп-каскадов (nil без magitrickle).
func (e *Engine) Cascade() *cascade.Manager { return e.casc }

// SyncExclusions - немедленно применить исключения из обработки
// (PUT-хендлеры исключений); тихо без magitrickle.
func (e *Engine) SyncExclusions() {
	if e.casc == nil {
		return
	}
	e.casc.SyncExclusions()
}

func (e *Engine) applyAllowed(pool string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.lastApply[pool]
	return !ok || e.now().Sub(t) >= minApplyInterval
}

func (e *Engine) markApply(pool string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lastApply[pool] = e.now()
}

func (e *Engine) now() time.Time {
	if e.Clock != nil {
		return e.Clock()
	}
	return time.Now()
}

func (e *Engine) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	e.stop = cancel
	go e.loop(ctx)
	go e.probeMagitrickleCompatibility(ctx)
}

func (e *Engine) Stop() {
	if e.stop != nil {
		e.stop()
	}
}

func (e *Engine) Wake(pool string) {
	select {
	case e.wake <- pool:
	default:
	}
}

func (e *Engine) loop(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	bundleTicker := time.NewTicker(30 * time.Second)
	defer bundleTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case pool := <-e.wake:
			// ручная «проверить сейчас» идёт мимо полосы - как раньше
			e.checkPool(pool)
		case <-bundleTicker.C:
			e.tickN++
			e.runHousework()
		case <-ticker.C:
			e.tickProbes(e.now())
		}
	}
}

func (e *Engine) CheckNow(pool string) {
	e.Wake(pool)
}

func (e *Engine) RotateNow(pool string) error {
	e.actMu.Lock()
	defer e.actMu.Unlock()
	p, ok := e.store.Pool(pool)
	if !ok {
		return fmt.Errorf("пул %q не найден", pool)
	}
	if p.Disabled {
		return fmt.Errorf("пул %q выключен, сначала включите его", pool)
	}
	e.rotate(p, e.store.State(pool), "")
	e.saveRouterConfig(pool)
	return nil
}

func (e *Engine) SetActive(pool, file string) error {
	e.actMu.Lock()
	defer e.actMu.Unlock()
	p, ok := e.store.Pool(pool)
	if !ok {
		return fmt.Errorf("пул %q не найден", pool)
	}
	if p.Disabled {
		return fmt.Errorf("пул %q выключен, сначала включите его", pool)
	}
	if _, ok := p.ConfigByFile(file); !ok {
		return fmt.Errorf("конфиг %q не найден в пуле %q", file, pool)
	}
	e.store.MutateState(pool, func(s *store.PoolState) {
		delete(s.Cooldowns, file)
	})
	if err := e.applyConfig(p, file); err != nil {
		return err
	}
	e.saveRouterConfig(pool)
	return nil
}

func (e *Engine) DisablePool(pool string) error {
	e.actMu.Lock()
	defer e.actMu.Unlock()
	p, ok := e.store.Pool(pool)
	if !ok {
		return fmt.Errorf("пул %q не найден", pool)
	}
	if p.Disabled {
		return nil
	}
	if err := e.store.SetPoolDisabled(pool, true); err != nil {
		return err
	}
	e.ifaceTouched()
	if err := e.backend.Down(p); err != nil {
		e.store.LogEvent(pool, "disable", "интерфейс не выключился: "+err.Error())
	}
	e.suspendGroups(p)
	e.store.MutateState(pool, func(s *store.PoolState) {
		s.Mode = store.ModeFallback
		s.LastResult = "выключен вручную"
		s.ProbeOK, s.ProbeMs = false, 0
		s.ConsecFails = 0
	})
	e.store.LogEvent(pool, "disable", "пул выключен вручную, ротация остановлена")
	e.mu.Lock()
	delete(e.lastApply, pool)
	e.mu.Unlock()
	e.saveRouterConfig(pool)
	return nil
}

// saveRouterConfig сбрасывает running в сохраненный конфиг роутера: веб
// Keenetic показывает тумблеры из сохраненного. Только ручные действия -
// при ротациях флеш не дергаем.
func (e *Engine) saveRouterConfig(pool string) {
	if err := e.backend.SaveConfig(); err != nil {
		e.store.LogEvent(pool, "save-config", "не сохранился: "+err.Error())
	}
}

func (e *Engine) EnablePool(pool string) error {
	e.actMu.Lock()
	defer e.actMu.Unlock()
	p, ok := e.store.Pool(pool)
	if !ok {
		return fmt.Errorf("пул %q не найден", pool)
	}
	if !p.Disabled {
		return nil
	}
	if err := e.store.SetPoolDisabled(pool, false); err != nil {
		return err
	}
	e.store.LogEvent(pool, "enable", "пул включён вручную")
	st := e.store.State(pool)
	eligible := e.eligible(p, st)
	if st.ActiveFile != "" {
		if _, ok := p.ConfigByFile(st.ActiveFile); ok {
			delete(st.Cooldowns, st.ActiveFile)
			e.store.MutateState(pool, func(s *store.PoolState) {
				delete(s.Cooldowns, st.ActiveFile)
			})
			return e.applyConfig(p, st.ActiveFile)
		}
	}
	if len(eligible) > 0 {
		if err := e.applyConfig(p, eligible[0].File); err != nil {
			return err
		}
		e.saveRouterConfig(pool)
		return nil
	}
	e.Wake(pool)
	return nil
}

func (e *Engine) PoolUp(pool string) error {
	e.actMu.Lock()
	defer e.actMu.Unlock()
	p, ok := e.store.Pool(pool)
	if !ok {
		return fmt.Errorf("пул %q не найден", pool)
	}
	if p.Disabled {
		return fmt.Errorf("пул %q выключен, сначала включите его", pool)
	}
	st := e.store.State(pool)
	if st.ActiveFile == "" {
		eligible := e.eligible(p, st)
		if len(eligible) == 0 {
			return fmt.Errorf("в пуле %q нет конфигов, пригодных для ротации", pool)
		}
		return e.applyConfig(p, eligible[0].File)
	}
	e.ifaceTouched()
	if err := e.backend.Up(p); err != nil {
		return err
	}
	restored := false
	e.store.MutateState(pool, func(s *store.PoolState) {
		if s.Mode == store.ModeFallback {
			s.Mode = store.ModeUp
			restored = true
		}
	})
	if restored {
		e.restoreGroups(p, e.store.State(pool))
		e.restoreReboundGroups(p)
		e.restoreDegraded(p.DeviceName())
	}
	return nil
}

func (e *Engine) PoolDown(pool string) error {
	e.actMu.Lock()
	defer e.actMu.Unlock()
	p, ok := e.store.Pool(pool)
	if !ok {
		return fmt.Errorf("пул %q не найден", pool)
	}
	if p.Disabled {
		return fmt.Errorf("пул %q выключен, сначала включите его", pool)
	}
	e.ifaceTouched()
	if err := e.backend.Down(p); err != nil {
		return err
	}
	e.store.MutateState(pool, func(s *store.PoolState) {
		s.Mode = store.ModeFallback
		s.Since = e.now()
		s.LastResult = "manual down"
		s.ProbeOK, s.ProbeMs = false, 0
	})
	return nil
}

func (e *Engine) eligible(p store.Pool, st *store.PoolState) []store.ManagedConfig {
	now := e.now().Unix()
	own := p.DeviceName()
	var out []store.ManagedConfig
	for _, c := range p.Configs {
		if !c.Enabled {
			continue
		}
		if until, ok := st.Cooldowns[c.File]; ok && now < until {
			continue
		}
		if e.addressTaken(p.Name, own, c) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// addressTaken: внутренние адреса конфига держит чужой поднятый
// интерфейс - ndm такой конфиг не поднимет, из ротации исключаем.
func (e *Engine) addressTaken(poolName, ownDevice string, c store.ManagedConfig) bool {
	addrs := c.Addresses
	if len(addrs) == 0 {
		parsed, err := e.store.LoadConfigFile(poolName, c.File)
		if err != nil {
			return false
		}
		addrs = parsed.Addresses
		if len(addrs) > 0 {
			e.store.SetConfigAddresses(poolName, c.File, addrs)
		}
	}
	for addr, dev := range e.TunnelAddresses() {
		if dev == ownDevice {
			continue
		}
		for _, a := range addrs {
			if normAddr(a) == normAddr(addr) {
				return true
			}
		}
	}
	return false
}

func normAddr(a string) string {
	return strings.TrimSpace(strings.SplitN(strings.TrimSpace(a), "/", 2)[0])
}

func (e *Engine) rotate(p store.Pool, st *store.PoolState, failedActive string) {
	if failedActive != "" {
		e.store.MutateState(p.Name, func(s *store.PoolState) {
			s.Cooldowns[failedActive] = e.now().Add(time.Duration(p.Settings.CooldownMin) * time.Minute).Unix()
		})
	}
	if !e.applyAllowed(p.Name) {
		return
	}
	for {
		pool, _ := e.store.Pool(p.Name)
		state := e.store.State(p.Name)
		startIdx := pool.IndexByFile(state.ActiveFile)
		chosen := -1
		for k := 0; k < len(pool.Configs); k++ {
			idx := (startIdx + 1 + k) % len(pool.Configs)
			c := pool.Configs[idx]
			if !c.Enabled {
				continue
			}
			if until, ok := state.Cooldowns[c.File]; ok && e.now().Unix() < until {
				continue
			}
			if e.addressTaken(p.Name, p.DeviceName(), c) {
				continue
			}
			chosen = idx
			break
		}
		if chosen < 0 {
			// умный каскад (86-док): спасает пул - туннель продолжает
			// жить через Via-пул, группы остаются при своём IP. В раннем
			// режиме (fails) попытки уже шли на каждом конфиге - тут
			// только обычный фоллбэк
			if e.smartTriggerFails(pool) == 0 && e.smartRescue(pool) {
				return
			}
			e.enterFallback(pool, state)
			return
		}
		if err := e.applyConfig(pool, pool.Configs[chosen].File); err == nil {
			return
		}
	}
}

func (e *Engine) applyConfig(p store.Pool, file string) error {
	cfg, err := e.store.LoadConfigFile(p.Name, file)
	if err != nil {
		if p.Premium {
			err = fmt.Errorf("Premium configuration could not be loaded")
		}
		e.store.MutateState(p.Name, func(s *store.PoolState) {
			s.LastError = "load: " + err.Error()
			s.Cooldowns[file] = e.now().Add(time.Duration(p.Settings.CooldownMin) * time.Minute).Unix()
		})
		return err
	}
	e.ifaceTouched()
	if err := e.backend.Apply(p, cfg); err != nil {
		if p.Premium {
			err = fmt.Errorf("Premium interface apply failed")
		}
		e.store.MutateState(p.Name, func(s *store.PoolState) {
			s.LastError = "apply: " + err.Error()
			s.Cooldowns[file] = e.now().Add(time.Duration(p.Settings.CooldownMin) * time.Minute).Unix()
		})
		e.store.LogEvent(p.Name, "apply-failed", file+": "+err.Error())
		return err
	}
	return e.recordApplied(p, file, cfg.Endpoint())
}

func (e *Engine) recordApplied(p store.Pool, file, endpoint string) error {
	e.markApply(p.Name)
	restored := false
	err := e.store.MutateState(p.Name, func(s *store.PoolState) {
		s.ActiveFile = file
		s.ConsecFails = 0
		s.Rotations++
		s.GraceUntil = e.now().Add(settleSeconds * time.Second).Unix()
		s.LastResult = "applied, settling"
		s.ProbeOK, s.ProbeMs = false, 0
		s.LastError = ""
		if s.Mode == store.ModeFallback {
			s.Mode = store.ModeUp
			restored = true
		}
	})
	if restored {
		e.restoreGroups(p, e.store.State(p.Name))
		e.restoreReboundGroups(p)
		e.restoreDegraded(p.DeviceName())
	}
	e.store.LogEvent(p.Name, "applied", file+" -> "+endpoint)
	if e.casc != nil {
		go e.casc.SyncPoolRules(context.Background(), "pool:"+p.Name)
	}
	if err != nil {
		return fmt.Errorf("configuration applied but active state could not be saved")
	}
	return nil
}

func (e *Engine) enterFallback(p store.Pool, st *store.PoolState) {
	was := st.Mode
	e.store.MutateState(p.Name, func(s *store.PoolState) {
		s.Mode = store.ModeFallback
		s.Since = e.now()
		s.LastResult = "all configs failed"
		s.ProbeOK, s.ProbeMs = false, 0
	})
	e.store.LogEvent(p.Name, "fallback", "all configs failed, mode "+p.Settings.Fallback)
	if was == store.ModeFallback {
		return
	}
	if fbDev, ok := store.FallbackIfaceName(p.Settings.Fallback); ok {
		exists := false
		if slots, err := e.backend.Slots(); err == nil {
			for _, sl := range slots {
				if sl.Device == fbDev {
					exists = true
					break
				}
			}
		}
		e.ifaceTouched()
		if err := e.backend.Down(p); err != nil {
			log.Printf("pool %s: down failed: %v", p.Name, err)
		}
		if !exists {
			e.store.LogEvent(p.Name, "fallback", "fallback iface "+fbDev+" не найден, группы идут напрямую")
			e.suspendGroups(p)
			return
		}
		e.store.LogEvent(p.Name, "fallback", "группы переведены на интерфейс "+fbDev)
		e.rebindGroups(p, fbDev)
		return
	}
	if fbName, ok := store.FallbackPoolName(p.Settings.Fallback); ok {
		fbPool, exists := e.store.Pool(fbName)
		if !exists || fbPool.Disabled {
			e.store.LogEvent(p.Name, "fallback", "fallback pool "+fbName+" unavailable, going direct")
			e.ifaceTouched()
			if err := e.backend.Down(p); err != nil {
				log.Printf("pool %s: down failed: %v", p.Name, err)
			}
			e.suspendGroups(p)
			return
		}
		e.ifaceTouched()
		if err := e.backend.Down(p); err != nil {
			log.Printf("pool %s: down failed: %v", p.Name, err)
		}
		e.rebindGroups(p, fbPool.DeviceName())
		return
	}
	if p.Settings.Fallback == store.FallbackDirect {
		e.ifaceTouched()
		if err := e.backend.Down(p); err != nil {
			log.Printf("pool %s: down failed: %v", p.Name, err)
		}
		e.suspendGroups(p)
	}
}

func (e *Engine) tryRecover(p store.Pool, st *store.PoolState) {
	pool, _ := e.store.Pool(p.Name)
	state := e.store.State(p.Name)
	eligible := e.eligible(pool, state)
	if len(eligible) == 0 {
		e.store.MutateState(p.Name, func(s *store.PoolState) {
			s.LastCheck = e.now()
			s.LastResult = "fallback, waiting for cooldown"
			s.ProbeOK, s.ProbeMs = false, 0
		})
		return
	}
	e.rotate(pool, state, "")
}

// BackupMagitrickleState снимает копии дискового конфига MT и нашей
// shadow-копии в base/backups. Вызывается перед каждым фактическим PUT
// групп (через magitrickle.PreSave) и вручную из панели. Инцидент
// 2026-10-10: сорванный массовый PUT усёк рантайм, 7 юзерских групп
// потеряны - дисковый конфиг остался цел и спас рестартом; теперь
// спасательная верёвка делается кодом автоматически.
func (e *Engine) BackupMagitrickleState() {
	if data, err := os.ReadFile(filepath.Join(e.store.Base(), "magitrickle-shadow.json")); err == nil {
		e.store.SaveBackupRotated("mt-shadow", "conf.gz", string(data))
	}
	if path := e.backend.MTConfigPath(); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			e.store.SaveBackupRotated("mt-config", "conf.gz", string(data))
		}
	}
}

// BackupFull собирает полный бэкап панели одним tar.gz: config.json,
// state.json, premium/free, shadow-правила, журнал, папка пулов, конфиг
// MT. Архив пишет сама Go (archive/tar + gzip) - системный tar на роутере
// не нужен. Вызывается вручную; автокопии перед записями MT остаются
// частичными (mt-shadow/mt-config) - они про усечение рантайма.
func (e *Engine) BackupFull() error {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	add := func(name string, data []byte) error {
		hdr := &tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), ModTime: time.Now()}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		_, err := tw.Write(data)
		return err
	}
	// файлы базы; config.json на свежей установке ещё не писался - из памяти
	configData, err := os.ReadFile(filepath.Join(e.store.Base(), "config.json"))
	if err != nil {
		configData, err = e.store.ConfigJSON()
		if err != nil {
			return err
		}
	}
	if err := add("mawg/config.json", configData); err != nil {
		return err
	}
	for _, f := range []string{"state.json", "premium.json", "free.json", "magitrickle-shadow.json", "events.json"} {
		if data, err := os.ReadFile(filepath.Join(e.store.Base(), f)); err == nil {
			if err := add("mawg/"+f, data); err != nil {
				return err
			}
		}
	}
	// папка пулов рекурсивно (wireguard-конфиги и метаданные)
	root := filepath.Join(e.store.Base(), "pools")
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil // нет каталога - нечего класть
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		return add("mawg/pools/"+filepath.ToSlash(rel), data)
	})
	// конфиг MT - не в базе mawg, путь знает платформа
	if path := e.backend.MTConfigPath(); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			if err := add("mt/"+filepath.Base(path), data); err != nil {
				return err
			}
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	_, err = e.store.SaveBackupRotated("mawg-full", "tar.gz", buf.String())
	return err
}
