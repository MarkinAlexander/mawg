//go:build linux

package mihomo

import (
	"context"
	"encoding/json"
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

	"mawg/internal/singbox"
)

// CacheDir - домашний каталог mihomo (-d): туда ядро кладёт cache.db и
// прочее состояние, поэтому ТОЛЬКО tmpfs - флешку не изнашиваем
// (прецедент SingboxCacheTmpPath).
const CacheDir = "/tmp/mihomo-mawg"

// Manager - собственный (own) экземпляр mihomo под mawg: свой конфиг
// YAML, свои порты (mixed 2381+, external-controller 2391). Пробы и
// ротация узлов - через clash API и socks-порт пула, клиентская часть
// переиспользуется из пакета singbox (clash.go).
type Manager struct {
	mu            sync.Mutex
	stmu          sync.Mutex
	eng           *Engine
	Dir           string
	MixedPort     int
	ClashPort     int
	webPort       int
	logEvent      func(pool, kind, msg string)
	lastRotate    map[string]time.Time
	pid           int
	probeLoopStop chan struct{}
	lastSpecs     []PoolSpec
	statuses      map[string]*singbox.PoolStatus
	nodeCooldown  map[string]time.Time
}

type RunConfig struct {
	WebPort  int
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
	m := &Manager{eng: eng, Dir: dir, MixedPort: 2381, ClashPort: 2391,
		webPort: cfg.WebPort, logEvent: cfg.LogEvent,
		lastRotate: map[string]time.Time{},
		statuses:   map[string]*singbox.PoolStatus{}, nodeCooldown: map[string]time.Time{}}
	m.probeLoopStop = make(chan struct{})
	go m.probeLoop()
	return m, nil
}

// Info - найденный бинарь ядра.
func (m *Manager) Info() Engine { return *m.eng }

// StopOwn - гасит процесс ядра, менеджер жив (мастер-выключатель ядра).
func (m *Manager) StopOwn() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stop()
}

// Close - остановить всё при смерти менеджера.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stop()
	if m.probeLoopStop != nil {
		close(m.probeLoopStop)
	}
}

func (m *Manager) setStatus(name string, st *singbox.PoolStatus) {
	m.stmu.Lock()
	m.statuses[name] = st
	m.stmu.Unlock()
}

func (m *Manager) PoolStatus(name string) (singbox.PoolStatus, bool) {
	m.stmu.Lock()
	defer m.stmu.Unlock()
	st, ok := m.statuses[name]
	if !ok {
		return singbox.PoolStatus{}, false
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
	st := &singbox.PoolStatus{
		Eligible: len(spec.Nodes) + len(spec.WG), MixedPort: spec.MixedPort,
		ProbeOK: err == nil, ProbeMs: ms, CheckedAt: time.Now(),
		ConsecFails: fails,
	}
	if err != nil {
		st.ProbeErr = err.Error()
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

// rotateNode переключает select-группу пула на следующий живой узел,
// пропуская узлы на cooldown (механика sing-box-менеджера).
func (m *Manager) rotateNode(spec PoolSpec) (string, error) {
	group := "mawg-" + spec.Name
	base := fmt.Sprintf("http://127.0.0.1:%d", m.ClashPort)
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
			if spec.GroupMode == "rotate" && spec.RotateSec >= 10 {
				if prev, ok := m.lastRotate[spec.Name]; !ok || now.Sub(prev) >= time.Duration(spec.RotateSec)*time.Second {
					m.lastRotate[spec.Name] = now
					m.rotateTimed(spec)
				}
			}
		}
	}
}

// rotateTimed - очередной переход таймерной ротации на следующего рабочего.
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
		if tag := mihomoNodeTag(n.Tag, spec.Name); tag != "" {
			if _, ok := delays[tag]; ok {
				order = append(order, tag)
			}
		}
	}
	if len(order) == 0 {
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

// mihomoNodeTag - тег узла в группе (mawg-<пул>|<ConfName>), как в конфиге.
func mihomoNodeTag(nodeTag, pool string) string {
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

func (m *Manager) cfgPath() string  { return filepath.Join(m.Dir, "config.yaml") }
func (m *Manager) pidPath() string  { return filepath.Join(m.Dir, "run.pid") }
func (m *Manager) lastGood() string { return filepath.Join(m.Dir, "config.last-good.yaml") }

// ClashProxies/ClashSelect/ClashGroupDelay - клиент clash API на порт
// нашего экземпляра (реализация общая с sing-box).
func (m *Manager) ClashProxies(ctx context.Context) (map[string]singbox.ClashProxy, error) {
	return singbox.ClashProxies(ctx, m.ClashPort)
}

func (m *Manager) ClashSelect(ctx context.Context, group, name string) error {
	return singbox.ClashSelect(ctx, m.ClashPort, group, name)
}

func (m *Manager) ClashGroupDelay(ctx context.Context, group, testURL string, timeout time.Duration) (map[string]int, error) {
	return singbox.ClashGroupDelay(ctx, m.ClashPort, group, testURL, timeout)
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

func (m *Manager) writeConfig(pools []PoolSpec) error {
	data, _, err := BuildConfig(pools, Params{ClashPort: m.ClashPort})
	if err != nil {
		return err
	}
	tmp := m.cfgPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	// состояние ядра (-d) в RAM: mihomo -t не должен писать на флешку
	_ = os.MkdirAll(CacheDir, 0o755)
	if err := m.eng.Check(tmp, CacheDir); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, m.cfgPath())
}

// Apply приводит ядро к списку пулов: пустой список = остановка процесса.
func (m *Manager) Apply(pools []PoolSpec) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastRotate = map[string]time.Time{}
	m.stmu.Lock()
	prevStatuses := m.statuses
	m.statuses = map[string]*singbox.PoolStatus{}
	for _, spec := range pools {
		if st, ok := prevStatuses[spec.Name]; ok {
			m.statuses[spec.Name] = st
		}
	}
	m.stmu.Unlock()
	if len(pools) == 0 {
		m.stop()
		m.lastSpecs = nil
		return nil, nil
	}
	var skipped []string
	var runnable []PoolSpec
	for i, spec := range pools {
		eligible, reasons := EligibleNodes(spec.Nodes)
		if len(eligible) == 0 && len(spec.WG) == 0 {
			m.setStatus(spec.Name, &singbox.PoolStatus{Reason: "waits-core", Detail: strings.Join(reasons, "; ")})
			skipped = append(skipped, fmt.Sprintf("пул %s: ни один узел не поддерживается mihomo (%s)", spec.Name, strings.Join(reasons, "; ")))
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
		m.stop()
		m.lastSpecs = nil
		return skipped, nil
	}
	// живой выбор select-группы переживает пересборку (default-selected)
	selCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if proxies, err := m.ClashProxies(selCtx); err == nil {
		for i := range runnable {
			if g, ok := proxies["mawg-"+runnable[i].Name]; ok && g.Now != "" {
				runnable[i].Selected = g.Now
			}
		}
	}
	cancel()
	if err := m.writeConfig(runnable); err != nil {
		return skipped, err
	}
	m.stop()
	logF, err := os.OpenFile(filepath.Join(m.Dir, "mihomo.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND|os.O_TRUNC, 0o600)
	if err != nil {
		return skipped, err
	}
	defer logF.Close()
	if err := os.MkdirAll(CacheDir, 0o755); err != nil {
		return skipped, err
	}
	cmd := exec.Command(m.eng.Bin, "-d", CacheDir, "-f", m.cfgPath())
	cmd.Stdout = logF
	cmd.Stderr = logF
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return skipped, err
	}
	m.pid = cmd.Process.Pid
	go cmd.Process.Wait()
	os.WriteFile(m.pidPath(), []byte(strconv.Itoa(m.pid)), 0o600)
	if err := m.waitClash(30 * time.Second); err != nil {
		return skipped, fmt.Errorf("mihomo стартовал, но API не отвечает: %v (лог: %s/mihomo.log)", err, m.Dir)
	}
	if cp, err := os.ReadFile(m.cfgPath()); err == nil {
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
	probe := fmt.Sprintf("http://127.0.0.1:%d/version", m.ClashPort)
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
	Running bool   `json:"running"`
	API     bool   `json:"api"`
	PID     int    `json:"pid,omitempty"`
	Version string `json:"version,omitempty"`
	Bin     string `json:"bin,omitempty"`
	Mixed   int    `json:"mixedPort"`
	Clash   int    `json:"clashPort"`
	Mode    string `json:"mode"`
	Cache   string `json:"cacheFile,omitempty"`
}

func (m *Manager) Status() Status {
	st := Status{Version: m.eng.Version, Bin: m.eng.Bin, Mixed: m.MixedPort, Clash: m.ClashPort, Mode: "own", Cache: filepath.Join(CacheDir, "cache.db")}
	st.Running = m.alive()
	if st.Running {
		st.PID = m.pid
		cl := &http.Client{Timeout: 750 * time.Millisecond}
		if resp, err := cl.Get(fmt.Sprintf("http://127.0.0.1:%d/version", m.ClashPort)); err == nil {
			resp.Body.Close()
			st.API = resp.StatusCode == http.StatusOK
		}
	}
	return st
}
