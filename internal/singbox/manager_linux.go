//go:build linux

package singbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"mawg/internal/platform"
)

type Manager struct {
	mu              sync.Mutex
	stmu            sync.Mutex
	eng             *Engine
	Dir             string
	MixedPort       int
	ClashPort       int
	Mode            string
	SharedDir       string
	SharedInit      string
	MarkerDir       string // каталог маркера .installed-by-mawg (RunConfig): разрешает старт сервиса
	webPort         int
	sharedClash     int
	resolverTag     string // тег нашего dns-сервера во фрагменте (свободный у чужого конфига)
	sharedCacheFile string
	patchNote       string // что mawg поправил в чужом config.json при последнем Apply
	logEvent        func(pool, kind, msg string)
	lastRotate      map[string]time.Time // пул -> прошлый таймерный переход (rotate-режим)
	pid             int
	probeLoopStop   chan struct{}
	lastSpecs       []PoolSpec
	statuses        map[string]*PoolStatus
	nodeCooldown    map[string]time.Time
	engineLog       string // лог движка (rotated): причина старта и хвост диагностики
	lastStartErr    string // последняя неудача старта (под stmu): «не запущен» с причиной
}

type RunConfig struct {
	Mode        string
	SharedDir   string
	SharedInit  string
	MarkerDir   string // каталог маркера установки ядра (.installed-by-mawg)
	SharedClash int
	WebPort     int // панель mawg: её порт тоже занят и не отдается чужим inbound'ам
	// LogEvent - журнал событий панели (pool/kind/message): менеджер
	// пишет туда события таймерной ротации узлов из своей горутины
	LogEvent func(pool, kind, msg string)
}

func NewManager(dir string) (*Manager, error) {
	return NewManagerConfig(dir, RunConfig{})
}

func NewManagerConfig(dir string, cfg RunConfig) (*Manager, error) {
	eng, err := Detect()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	mode := cfg.Mode
	if mode != "shared" {
		mode = "own"
	}
	m := &Manager{eng: eng, Dir: dir, MixedPort: 2281, ClashPort: 2291,
		Mode: mode, SharedDir: cfg.SharedDir, SharedInit: cfg.SharedInit,
		MarkerDir: cfg.MarkerDir, sharedClash: cfg.SharedClash, webPort: cfg.WebPort, logEvent: cfg.LogEvent,
		engineLog:  platform.EngineLogPath(dir, "singbox"),
		lastRotate: map[string]time.Time{},
		statuses:   map[string]*PoolStatus{}, nodeCooldown: map[string]time.Time{}}
	m.probeLoopStop = make(chan struct{})
	go m.probeLoop()
	return m, nil
}

// Close приводит чужое ядро и собственный процесс в состояние,
// соответствующее отсутствию менеджера: свой экземпляр гасится, в
// shared-режиме чужому ядру возвращается чистый конфиг (фрагмент снят).
func (m *Manager) Close() {
	m.mu.Lock()
	mode := m.Mode
	m.mu.Unlock()
	if mode == "shared" {
		if err := m.removeFragment(); err == nil {
			_ = m.restartShared(20 * time.Second)
		}
		return
	}
	m.stop()
}

func (m *Manager) Info() Engine {
	return *m.eng
}

// EngineLog - путь лога движка (ротация при каждом старте): диагностика
// «не запущен» и хвост для бандла (112-док 3.7).
func (m *Manager) EngineLog() string { return m.engineLog }

// LastError - причина последнего неудачного старта движка: карточка ядра
// показывает её вместо голого «не запущен» (кейс 04.11, 112-док 3.6).
func (m *Manager) LastError() string {
	m.stmu.Lock()
	defer m.stmu.Unlock()
	return m.lastStartErr
}

// setStartErr - зафиксировать исход старта (вызывается под m.mu из Apply;
// поле под stmu - его читает probeSpec без m.mu). Неудача пишет событие
// в журнал панели: раньше FATAL ядра терялся в stdout init-скрипта.
func (m *Manager) setStartErr(msg string) {
	m.stmu.Lock()
	m.lastStartErr = msg
	m.stmu.Unlock()
	if msg != "" && m.logEvent != nil {
		m.logEvent("singbox", "start-fail", msg)
	}
}

// StopOwn - гасит собственный процесс ядра (own-режим), менеджер жив
// (следующий Apply поднимет ядро). Мастер-выключатель движка (Система → Ядра VPN).
func (m *Manager) StopOwn() {
	m.mu.Lock()
	mode := m.Mode
	m.mu.Unlock()
	if mode == "shared" {
		return
	}
	m.stop()
}

// ClearSharedFragment - снимает наш фрагмент из чужого конфига, НЕ
// перезапуская сервис (uninstall/выключение: сервис остановят следом).
func (m *Manager) ClearSharedFragment() {
	m.mu.Lock()
	mode := m.Mode
	m.mu.Unlock()
	if mode != "shared" {
		return
	}
	_ = m.removeFragment()
}

type PoolStatus struct {
	Eligible    int       `json:"eligible"`
	MixedPort   int       `json:"mixedPort"`
	ProbeOK     bool      `json:"probeOk"`
	ProbeMs     int       `json:"probeMs"`
	ProbeErr    string    `json:"probeErr,omitempty"`
	ConsecFails int       `json:"consecFails"`
	CheckedAt   time.Time `json:"checkedAt,omitempty"`
	Reason      string    `json:"reason,omitempty"`
	Detail      string    `json:"detail,omitempty"`
}

func (m *Manager) setStatus(name string, st *PoolStatus) {
	m.stmu.Lock()
	m.statuses[name] = st
	m.stmu.Unlock()
}

func (m *Manager) PoolStatus(name string) (PoolStatus, bool) {
	m.stmu.Lock()
	defer m.stmu.Unlock()
	st, ok := m.statuses[name]
	if !ok {
		return PoolStatus{}, false
	}
	cp := *st
	return cp, true
}

func (m *Manager) probePool(port int, target string) (int, error) {
	pu, err := url.Parse(fmt.Sprintf("socks5://127.0.0.1:%d", port))
	if err != nil {
		return 0, err
	}
	client := &http.Client{
		Timeout: 8 * time.Second,
		// редиректы не следуем: 1.1.1.1 по http даёт 301 на https, а Free
		// 443 не пускает - проба проверяет «канал жив», а не конечный контент
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{Proxy: http.ProxyURL(pu)},
	}
	start := time.Now()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	ms := int(time.Since(start).Milliseconds())
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return ms, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return ms, nil
}

func (m *Manager) probeSpec(spec PoolSpec) {
	ms, err := m.probePool(spec.MixedPort, spec.ProbeTarget)
	m.stmu.Lock()
	prev := m.statuses[spec.Name]
	fails := 0
	if prev != nil && !prev.ProbeOK {
		fails = prev.ConsecFails
	}
	m.stmu.Unlock()
	if err == nil && spec.MaxRTTms > 0 && ms > spec.MaxRTTms {
		err = fmt.Errorf("RTT %dms выше порога %dms", ms, spec.MaxRTTms)
	}
	st := &PoolStatus{
		Eligible: len(spec.Nodes) + len(spec.WG), MixedPort: spec.MixedPort,
		ProbeOK: err == nil, ProbeMs: ms, CheckedAt: time.Now(),
		ConsecFails: fails,
	}
	if err != nil {
		st.ProbeErr = err.Error()
		// к отказу пробы - первая строка ошибки из лога движка, когда ядро
		// само в отказе (старт-ошибка или socks-порт мёртв); у живого ядра
		// мёртвый узел старый лог не тащит (112-док 3.7г)
		m.stmu.Lock()
		startErr := m.lastStartErr
		m.stmu.Unlock()
		if startErr != "" || strings.Contains(st.ProbeErr, "connection refused") {
			if first := platform.LogFirstError(m.engineLog); first != "" {
				st.ProbeErr += " | ядро: " + first
			}
		}
		st.ConsecFails = fails + 1
	}
	m.setStatus(spec.Name, st)
	if err == nil {
		return
	}
	threshold := spec.FailThreshold
	if threshold <= 0 {
		threshold = 3
	}
	if st.ConsecFails >= threshold {
		if next, rErr := m.rotateNode(spec); rErr == nil && next != "" {
			st.Detail = fmt.Sprintf("после %d неудач переключено на %s", st.ConsecFails, next)
			m.setStatus(spec.Name, st)
		}
	}
}

// rotateNode переключает selector-группу пула на следующий живой узел,
// пропуская узлы на cooldown; возвращает выбранный тег ("" - некуда)
func (m *Manager) rotateNode(spec PoolSpec) (string, error) {
	group := "mawg-" + spec.Name
	base := fmt.Sprintf("http://127.0.0.1:%d", m.clashBase())
	resp, err := http.Get(base + "/proxies/" + url.PathEscape(group))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var g struct {
		Now string   `json:"now"`
		All []string `json:"all"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&g); err != nil {
		return "", err
	}
	if len(g.All) == 0 {
		return "", nil
	}
	m.stmu.Lock()
	defer m.stmu.Unlock()
	cur := 0
	for i, t := range g.All {
		if t == g.Now {
			cur = i
			break
		}
	}
	cooldown := time.Duration(spec.CooldownMin) * time.Minute
	if cooldown <= 0 {
		cooldown = 10 * time.Minute
	}
	nowT := time.Now()
	var pick string
	for step := 1; step <= len(g.All); step++ {
		cand := g.All[(cur+step)%len(g.All)]
		if cand == g.Now {
			continue
		}
		if until, bad := m.nodeCooldown[cand]; bad && nowT.Before(until) {
			continue
		}
		pick = cand
		break
	}
	if pick == "" {
		return "", nil
	}
	m.nodeCooldown[g.Now] = nowT.Add(cooldown)
	body := fmt.Sprintf(`{"name":%q}`, pick)
	req, err := http.NewRequest(http.MethodPut, base+"/proxies/"+url.PathEscape(group), strings.NewReader(body))
	if err != nil {
		return "", err
	}
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	resp2.Body.Close()
	return pick, nil
}

func (m *Manager) probeLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.probeLoopStop:
			return
		case <-ticker.C:
		}
		m.mu.Lock()
		specs := m.lastSpecs
		m.mu.Unlock()
		now := time.Now()
		for _, spec := range specs {
			if spec.ProbeTarget == "" {
				continue
			}
			interval := time.Duration(spec.CheckIntervalSec) * time.Second
			if interval < 10*time.Second {
				interval = 60 * time.Second
			}
			m.stmu.Lock()
			st, ok := m.statuses[spec.Name]
			due := !ok || st.CheckedAt.IsZero() || now.Sub(st.CheckedAt) >= interval
			m.stmu.Unlock()
			if due {
				m.probeSpec(spec)
			}
			// ротация по таймеру (rotate-режим): строго на узлы, ответившие
			// замеру, следующей по кругу после текущего (слово владельца
			// 2026-10-17)
			if spec.GroupMode == "rotate" && spec.RotateSec >= 10 {
				if prev, ok := m.lastRotate[spec.Name]; !ok || now.Sub(prev) >= time.Duration(spec.RotateSec)*time.Second {
					m.lastRotate[spec.Name] = now
					m.rotateTimed(spec)
				}
			}
		}
	}
}

// rotateTimed - очередной переход таймерной ротации: замерить группу
// (ответившие = рабочие), взять следующего по кругу после текущего,
// переключить selector-группу. Никто не ответил - остаёмся, событие в журнал.
func (m *Manager) rotateTimed(spec PoolSpec) {
	group := "mawg-" + spec.Name
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	delays, err := m.ClashGroupDelay(ctx, group, spec.ProbeTarget, 5*time.Second)
	if err != nil {
		m.note(spec.Name, "ротация не удалась: "+err.Error())
		return
	}
	if len(delays) == 0 {
		m.note(spec.Name, "ротация: живых узлов нет, остаёмся на текущем")
		return
	}
	var order []string
	for _, n := range spec.Nodes {
		if _, ok := delays[groupPrefix(n.Tag, spec.Name)]; ok {
			order = append(order, groupPrefix(n.Tag, spec.Name))
		}
	}
	if len(order) == 0 { // перестраховка: замер есть, тег не совпал
		for tag := range delays {
			order = append(order, tag)
		}
	}
	cur := ""
	if proxies, err := m.ClashProxies(ctx); err == nil {
		cur = proxies[group].Now
	}
	pick := order[0]
	for i, t := range order {
		if t == cur && i+1 < len(order) {
			pick = order[i+1]
			break
		}
	}
	if pick == cur {
		m.note(spec.Name, "ротация: единственный рабочий узел - текущий")
		return
	}
	if err := m.ClashSelect(ctx, group, pick); err != nil {
		m.note(spec.Name, "ротация не удалась: "+err.Error())
		return
	}
	m.note(spec.Name, "ротация по таймеру: "+cur+" -> "+pick)
}

// groupPrefix - тег узла в группе движка (build.go: mawg-<пул>|<ConfName>).
func groupPrefix(nodeTag, pool string) string {
	name := nodeTag
	if _, after, ok := strings.Cut(nodeTag, "|"); ok && after != "" {
		name = after
	}
	return "mawg-" + pool + "|" + name
}

func (m *Manager) note(pool, msg string) {
	if m.logEvent != nil {
		m.logEvent(pool, "rotate", msg)
	}
}

func (m *Manager) cfgPath() string  { return m.Dir + "/config.json" }
func (m *Manager) pidPath() string  { return m.Dir + "/run.pid" }
func (m *Manager) lastGood() string { return m.Dir + "/config.last-good.json" }

func (m *Manager) clashBase() int {
	if m.Mode == "shared" && m.sharedClash > 0 {
		return m.sharedClash
	}
	return m.ClashPort
}

// ClashProxies - все прокси и группы движка (clash_api, порт с учётом
// shared-режима); узлы/задержки пула читает веб-слой (60-док).
func (m *Manager) ClashProxies(ctx context.Context) (map[string]ClashProxy, error) {
	return ClashProxies(ctx, m.clashBase())
}

func (m *Manager) ClashSelect(ctx context.Context, group, name string) error {
	return ClashSelect(ctx, m.clashBase(), group, name)
}

func (m *Manager) ClashGroupDelay(ctx context.Context, group, testURL string, timeout time.Duration) (map[string]int, error) {
	return ClashGroupDelay(ctx, m.clashBase(), group, testURL, timeout)
}

func (m *Manager) fragmentPath() string { return filepath.Join(m.SharedDir, FragmentName) }

// FragmentMTime - время последней записи применённого конфига движка
// (фрагмент shared-режима или собственный config.json). Веб-слой
// сравнивает с ним свежесть nodes.json пулов: узлы новее конфига -
// движок их ещё не применял.
func (m *Manager) FragmentMTime() time.Time {
	path := m.cfgPath()
	if m.Mode == "shared" {
		path = m.fragmentPath()
	}
	if fi, err := os.Stat(path); err == nil {
		return fi.ModTime()
	}
	return time.Time{}
}

func (m *Manager) alive() bool {
	if m.pid == 0 {
		data, err := os.ReadFile(m.pidPath())
		if err != nil {
			return false
		}
		m.pid, _ = strconv.Atoi(string(data))
	}
	if m.pid <= 0 {
		return false
	}
	return syscall.Kill(m.pid, 0) == nil
}

func (m *Manager) stop() {
	if m.pid == 0 {
		return
	}
	syscall.Kill(m.pid, syscall.SIGTERM)
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(m.pid, 0) == nil && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
	}
	if syscall.Kill(m.pid, 0) == nil {
		syscall.Kill(m.pid, syscall.SIGKILL)
	}
	os.Remove(m.pidPath())
	m.pid = 0
}

// sweepOrphanEngines гасит осиротевшие процессы нашего экземпляра
// sing-box: comm sing-box + наш конфиг в cmdline, pid не наш (потерян
// прошлым mawg при рестарте). Чужой сервис (свой конфиг) не трогается.
// Возвращает число погашенных.
func (m *Manager) sweepOrphanEngines() int {
	orphans := OurEngineProcs("sing-box", m.cfgPath(), m.pid)
	if len(orphans) == 0 {
		return 0
	}
	for _, p := range orphans {
		syscall.Kill(p.PID, syscall.SIGTERM)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		alive := 0
		for _, p := range orphans {
			if syscall.Kill(p.PID, 0) == nil {
				alive++
			}
		}
		if alive == 0 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	forced := 0
	for _, p := range orphans {
		if syscall.Kill(p.PID, 0) == nil {
			syscall.Kill(p.PID, syscall.SIGKILL)
			forced++
		}
	}
	if m.logEvent != nil {
		msg := "погашен осиротевший процесс sing-box: " + DescribeProcs(orphans)
		if forced > 0 {
			msg += fmt.Sprintf(" (%d не ушёл по SIGTERM, добит SIGKILL)", forced)
		}
		m.logEvent("singbox", "orphan", msg)
	}
	return len(orphans)
}

// startOwn - поднять свой процесс ядра (конфиг уже записан и проверен,
// прошлые процессы погашены). Ошибка старта фиксируется с причиной из
// лога ядра (112-док 3.6).
func (m *Manager) startOwn() error {
	// ротация перед O_TRUNC: хвост прошлой сессии ядра живёт в .old
	platform.RotateIfLarge(m.engineLog, platform.EngineLogMaxBytes)
	logF, err := os.OpenFile(m.engineLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer logF.Close()
	cmd := exec.Command(m.eng.Bin, "run", "-c", m.cfgPath())
	cmd.Stdout = logF
	cmd.Stderr = logF
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		m.setStartErr(fmt.Sprintf("процесс не стартовал: %v", err))
		return err
	}
	m.pid = cmd.Process.Pid
	go cmd.Process.Wait()
	os.WriteFile(m.pidPath(), []byte(strconv.Itoa(m.pid)), 0o600)
	if err := m.waitClash(30 * time.Second); err != nil {
		// причина - в первых строках лога (FATAL ядра: занятый tun,
		// порт, битый конфиг), а не только таймаут clash_api
		msg := fmt.Sprintf("движок стартовал, но clash_api не отвечает: %v", err)
		if head := platform.LogHead(m.engineLog, 4); len(head) > 0 {
			msg += "; ядро пишет: " + strings.Join(head, " ⏎ ")
		}
		if others := TunHolderProcs(m.pid); len(others) > 0 {
			msg += "; /dev/net/tun держат: " + DescribeProcs(others)
		}
		msg += " (лог: " + m.engineLog + ")"
		m.setStartErr(msg)
		return errors.New(msg)
	}
	m.setStartErr("")
	return nil
}

func (m *Manager) writeConfig(pools []PoolSpec) error {
	merged := m.Mode == "shared"
	data, _, err := BuildConfig(pools, Params{ClashPort: m.ClashPort, Profile: m.eng.SourceID, Caps: m.eng.Caps(), Merged: merged, ResolverTag: m.resolverTag})
	if err != nil {
		return err
	}
	if !merged {
		tmp := m.cfgPath() + ".tmp"
		if err := os.WriteFile(tmp, data, 0o600); err != nil {
			return err
		}
		if err := m.eng.Check(tmp); err != nil {
			os.Remove(tmp)
			return err
		}
		return os.Rename(tmp, m.cfgPath())
	}
	return m.writeFragment(data)
}

// Apply приводит ядро к списку пулов: пустой список = пустой конфиг.
// В режиме own гасится собственный процесс, в shared снимается фрагмент
// с чужого ядра. Пулы, которые текущий профиль не умеет, не запускаются
// со статусом waits-lx.
func (m *Manager) Apply(pools []PoolSpec) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.patchNote = ""
	m.lastRotate = map[string]time.Time{}
	m.stmu.Lock()
	prevStatuses := m.statuses
	m.statuses = map[string]*PoolStatus{}
	for _, spec := range pools {
		if st, ok := prevStatuses[spec.Name]; ok {
			m.statuses[spec.Name] = st
		}
	}
	m.stmu.Unlock()
	if len(pools) == 0 {
		if m.Mode == "shared" {
			if err := m.removeFragment(); err == nil && m.sharedAlive() {
				_ = m.restartShared(20 * time.Second)
			}
		} else {
			m.stop()
		}
		m.lastSpecs = nil
		return nil, nil
	}
	var skipped []string
	var runnable []PoolSpec
	for i, spec := range pools {
		eligible, reasons := EligibleNodes(spec.Nodes, m.eng.Caps())
		// WG-конфиги пула - полноценные члены (wireguard-эндпоинты): пул
		// без vless-узлов, но с конфигами runnable. AWG-поля умеют только
		// расширенные ядра (lx/extended) - на upstream такие конфиги ждут.
		wgOK := len(spec.WG) > 0
		if wgOK && !m.eng.Caps().AWG {
			// апстрим не знает amnezia-полей: один AWG-конфиг в сборке -
			// и ядро отвергает ВЕСЬ конфиг, вместе с рабочими пулами.
			// Выфильтровываем конфиги, пул живёт на остальных узлах.
			var wgReasons []string
			spec.WG, wgReasons = EligibleWG(spec.WG, m.eng.Caps())
			reasons = append(reasons, wgReasons...)
			wgOK = len(spec.WG) > 0
		}
		if len(eligible) == 0 && !wgOK {
			m.setStatus(spec.Name, &PoolStatus{Reason: "waits-lx", Detail: strings.Join(reasons, "; ")})
			skipped = append(skipped, fmt.Sprintf("пул %s: ни один узел не поддерживается текущим движком (%s)", spec.Name, strings.Join(reasons, "; ")))
			continue
		}
		spec.Nodes = eligible
		spec.MixedPort = m.MixedPort + 1 + i
		if spec.ProbeTarget == "" ||
			(!strings.HasPrefix(spec.ProbeTarget, "http://") && !strings.HasPrefix(spec.ProbeTarget, "https://")) {
			spec.ProbeTarget = "http://www.gstatic.com/generate_204"
		}
		if spec.CheckIntervalSec <= 0 {
			spec.CheckIntervalSec = 60
		}
		runnable = append(runnable, spec)
	}
	if len(runnable) == 0 {
		if m.Mode == "shared" {
			if err := m.removeFragment(); err == nil && m.sharedAlive() {
				_ = m.restartShared(20 * time.Second)
			}
		} else {
			m.stop()
		}
		m.lastSpecs = nil
		return skipped, nil
	}
	// живой выбор selector-групп переживает пересборку (иначе hot-reload
	// бросал бы на первый узел - слово владельца 2026-10-17)
	selCtx, selCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if proxies, err := m.ClashProxies(selCtx); err == nil {
		for i := range runnable {
			if g, ok := proxies["mawg-"+runnable[i].Name]; ok && g.Now != "" {
				runnable[i].Selected = g.Now
			}
		}
	}
	selCancel()
	if m.Mode == "shared" {
		return m.applyShared(runnable, skipped)
	}
	prevCfg, prevCfgErr := os.ReadFile(m.cfgPath())
	if err := m.writeConfig(runnable); err != nil {
		return skipped, err
	}
	// конфиг не изменился и процесс жив - рестарт холостой (рвёт сессии
	// пулов на этом же экземпляре ядра), достаточно перепробовать пулы
	if cur, rerr := os.ReadFile(m.cfgPath()); rerr == nil && prevCfgErr == nil &&
		bytes.Equal(prevCfg, cur) && m.alive() {
		m.lastSpecs = runnable
		for _, spec := range runnable {
			m.probeSpec(spec)
			if st, ok := m.PoolStatus(spec.Name); ok && !st.ProbeOK {
				time.Sleep(2 * time.Second)
				m.probeSpec(spec)
			}
		}
		return skipped, nil
	}
	m.stop()
	// осиротевшие процессы нашего экземпляра (pid потерян прошлым mawg)
	// держат tun/порты со старым конфигом - гасим ДО старта (кейс 04.11)
	m.sweepOrphanEngines()
	if err := m.startOwn(); err != nil {
		// повтор один раз: за время неудачного старта мог вскрыться ещё
		// один сирота (гонка sweep/старта) - погасили, пробуем снова
		if m.sweepOrphanEngines() == 0 {
			return skipped, err
		}
		if retryErr := m.startOwn(); retryErr != nil {
			return skipped, retryErr
		}
	}
	cp, err := os.ReadFile(m.cfgPath())
	if err == nil {
		os.WriteFile(m.lastGood(), cp, 0o600)
	}
	m.lastSpecs = runnable
	for _, spec := range runnable {
		m.probeSpec(spec)
		if st, ok := m.PoolStatus(spec.Name); ok && !st.ProbeOK {
			time.Sleep(2 * time.Second)
			m.probeSpec(spec)
		}
	}
	return skipped, nil
}

func (m *Manager) waitClash(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	probe := fmt.Sprintf("http://127.0.0.1:%d/version", m.clashBase())
	for time.Now().Before(deadline) {
		resp, err := http.Get(probe)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("таймаут")
}

type Status struct {
	Running   bool   `json:"running"`
	API       bool   `json:"api"` // clash-эндпоинт отвечает (слово владельца 2026-10-17: отличать «не запущено» от «не отвечает»)
	PID       int    `json:"pid,omitempty"`
	Version   string `json:"version,omitempty"`
	LX        bool   `json:"lx"`
	Extended  bool   `json:"extended,omitempty"`
	SourceID  string `json:"source,omitempty"` // lx | extended | upstream
	Bin       string `json:"bin,omitempty"`
	Mixed     int    `json:"mixedPort"`
	Clash     int    `json:"clashPort"`
	Mode      string `json:"mode"`
	Fragment  string `json:"fragment,omitempty"`
	CacheFile string `json:"cacheFile,omitempty"`
}

func (m *Manager) Status() Status {
	st := Status{Version: m.eng.Version, LX: m.eng.LX, Extended: m.eng.Extended, SourceID: m.eng.SourceID, Bin: m.eng.Bin, Mixed: m.MixedPort, Clash: m.clashBase(), Mode: m.Mode, CacheFile: m.sharedCacheFile}
	if m.Mode == "shared" {
		st.Fragment = m.fragmentPath()
		if m.sharedAlive() {
			st.Running = true
		}
	} else {
		st.Running = m.alive()
		if st.Running {
			st.PID = m.pid
		}
	}
	// жив ли clash-эндпоинт: «процесс есть, но не отвечает» и «не
	// запущено» - разные диагнозы для пользователя
	if st.Running {
		cl := &http.Client{Timeout: 750 * time.Millisecond}
		if resp, err := cl.Get(fmt.Sprintf("http://127.0.0.1:%d/version", m.clashBase())); err == nil {
			resp.Body.Close()
			st.API = resp.StatusCode == http.StatusOK
		}
	}
	return st
}
