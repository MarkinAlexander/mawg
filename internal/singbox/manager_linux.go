//go:build linux

package singbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Manager struct {
	mu            sync.Mutex
	stmu          sync.Mutex
	eng           *Engine
	Dir           string
	MixedPort     int
	ClashPort     int
	Mode          string
	SharedDir     string
	SharedInit    string
	sharedClash   int
	resolverTag   string // тег нашего dns-сервера во фрагменте (свободный у чужого конфига)
	pid           int
	probeLoopStop chan struct{}
	lastSpecs     []PoolSpec
	statuses      map[string]*PoolStatus
	nodeCooldown  map[string]time.Time
}

type RunConfig struct {
	Mode        string
	SharedDir   string
	SharedInit  string
	SharedClash int
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
		sharedClash: cfg.SharedClash, statuses: map[string]*PoolStatus{}, nodeCooldown: map[string]time.Time{}}
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
		Timeout:   8 * time.Second,
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
		Eligible: len(spec.Nodes), MixedPort: spec.MixedPort,
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
		}
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

func (m *Manager) fragmentPath() string { return filepath.Join(m.SharedDir, FragmentName) }

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
	merged := m.Mode == "shared"
	data, _, err := BuildConfig(pools, Params{ClashPort: m.ClashPort, LX: m.eng.LX, Merged: merged, ResolverTag: m.resolverTag})
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

// --- общий экземпляр (shared): фрагмент в чужой каталог + рестарт чужого сервиса ---

type sharedFacts struct {
	ClashPort int
	InTags    map[string]bool
	OutTags   map[string]bool
	Tuns      map[string]bool
	Addrs     map[string]bool
	DNSTags   map[string]bool
}

// preflightShared разбирает чужой config.json: порт clash_api для проб,
// занятые теги/tun/адреса и dns-теги (для выбора имени нашего резолвера),
// которые наш фрагмент не должен трогать.
func (m *Manager) preflightShared() (*sharedFacts, error) {
	path := filepath.Join(m.SharedDir, "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("чужой конфиг %s не читается: %v", path, err)
	}
	var cfg struct {
		Inbounds     []map[string]any `json:"inbounds"`
		Outbounds    []map[string]any `json:"outbounds"`
		DNS          struct {
			Servers []map[string]any `json:"servers"`
		} `json:"dns"`
		Experimental struct {
			ClashAPI struct {
				ExternalController string `json:"external_controller"`
			} `json:"clash_api"`
		} `json:"experimental"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("чужой config.json не разбирается: %v", err)
	}
	f := &sharedFacts{InTags: map[string]bool{}, OutTags: map[string]bool{}, Tuns: map[string]bool{}, Addrs: map[string]bool{}, DNSTags: map[string]bool{}}
	str := func(v any) string { s, _ := v.(string); return s }
	for _, in := range cfg.Inbounds {
		if t := str(in["tag"]); t != "" {
			f.InTags[t] = true
		}
		if t := str(in["interface_name"]); t != "" {
			f.Tuns[t] = true
		}
		if a, ok := in["address"].([]any); ok {
			for _, x := range a {
				if s := str(x); s != "" {
					f.Addrs[s] = true
				}
			}
		}
	}
	for _, ob := range cfg.Outbounds {
		if t := str(ob["tag"]); t != "" {
			f.OutTags[t] = true
		}
	}
	for _, s := range cfg.DNS.Servers {
		if t := str(s["tag"]); t != "" {
			f.DNSTags[t] = true
		}
	}
	if c := cfg.Experimental.ClashAPI.ExternalController; c != "" {
		if _, port, err := net.SplitHostPort(c); err == nil {
			f.ClashPort, _ = strconv.Atoi(port)
		}
	}
	if f.ClashPort == 0 {
		f.ClashPort = m.sharedClash
	}
	return f, nil
}

// resolverTag - свободный тег для нашего локального DNS-резолвера: в merged
// конфиге массивы дописываются, дубли тегов dns.servers рвут старт ядра.
func (f *sharedFacts) resolverTag() string {
	for _, tag := range []string{"local", "mawg-local", "mawg-local-2"} {
		if !f.DNSTags[tag] {
			return tag
		}
	}
	return "mawg-local"
}

func (m *Manager) writeFragment(data []byte) error {
	tmp := m.fragmentPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, m.fragmentPath()); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func (m *Manager) removeFragment() error {
	err := os.Remove(m.fragmentPath())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (m *Manager) checkShared() error {
	out, err := exec.Command(m.eng.Bin, "check", "-C", m.SharedDir).CombinedOutput()
	if err != nil {
		return fmt.Errorf("check -C %s не прошёл: %s", m.SharedDir, strings.TrimSpace(string(out)))
	}
	return nil
}

func (m *Manager) restartShared(timeout time.Duration) error {
	if m.SharedInit == "" {
		return fmt.Errorf("init-скрипт чужого ядра не задан")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, m.SharedInit, "restart").CombinedOutput()
	if err != nil {
		return fmt.Errorf("рестарт %s не удался: %s", m.SharedInit, strings.TrimSpace(string(out)))
	}
	return m.waitClash(timeout)
}

func (m *Manager) sharedAlive() bool {
	out, err := exec.Command("pidof", filepath.Base(m.eng.Bin)).Output()
	return err == nil && len(strings.Fields(string(out))) > 0
}

// backupShared сохраняет config.json владельца перед каждым apply
// (последние 5 копий в <база mawg>/singbox/shared-backups)
func (m *Manager) backupShared() {
	data, err := os.ReadFile(filepath.Join(m.SharedDir, "config.json"))
	if err != nil {
		return
	}
	bdir := filepath.Join(m.Dir, "shared-backups")
	if err := os.MkdirAll(bdir, 0o700); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(bdir, "config."+time.Now().Format("20060102-150405")+".json"), data, 0o600)
	entries, _ := os.ReadDir(bdir)
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "config.") && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for i := 0; i < len(names)-5; i++ {
		_ = os.Remove(filepath.Join(bdir, names[i]))
	}
}

func (m *Manager) restoreFragment(prev []byte, hadPrev bool) {
	if !hadPrev {
		_ = m.removeFragment()
		return
	}
	_ = m.writeFragment(prev)
}

func (m *Manager) applyShared(runnable []PoolSpec, skipped []string) ([]string, error) {
	facts, err := m.preflightShared()
	if err != nil {
		return skipped, err
	}
	m.resolverTag = facts.resolverTag()
	if facts.ClashPort == 0 {
		return skipped, fmt.Errorf("в чужом config.json нет clash_api - пробы невозможны; включите clash_api или используйте свой экземпляр движка")
	}
	for i, spec := range runnable {
		inTag := fmt.Sprintf("tun-in-%d", i+1)
		if facts.InTags[inTag] || facts.InTags["mixed-"+spec.Name] {
			return skipped, fmt.Errorf("тег inbound %s уже занят чужим конфигом", inTag)
		}
		if facts.OutTags["mawg-"+spec.Name] {
			return skipped, fmt.Errorf("тег mawg-%s уже занят чужим конфигом", spec.Name)
		}
		for _, n := range spec.Nodes {
			if facts.OutTags["mawg-"+spec.Name+"|"+n.ConfName()] {
				return skipped, fmt.Errorf("тег узла mawg-%s|%s уже занят чужим конфигом", spec.Name, n.ConfName())
			}
		}
		if facts.Tuns[spec.Tun] {
			return skipped, fmt.Errorf("интерфейс %s уже занят чужим конфигом", spec.Tun)
		}
		if facts.Addrs[spec.TunIP] {
			return skipped, fmt.Errorf("адрес %s уже занят чужим конфигом", spec.TunIP)
		}
	}
	if !m.sharedAlive() {
		return skipped, fmt.Errorf("процесс чужого ядра не запущен - не буду стартовать чужой сервис из mawg, запустите %s", m.SharedInit)
	}
	m.backupShared()
	prev, prevErr := os.ReadFile(m.fragmentPath())
	hadPrev := prevErr == nil
	if err := m.writeConfig(runnable); err != nil {
		return skipped, err
	}
	if err := m.checkShared(); err != nil {
		m.restoreFragment(prev, hadPrev)
		return skipped, err
	}
	m.sharedClash = facts.ClashPort
	if err := m.restartShared(45 * time.Second); err != nil {
		m.restoreFragment(prev, hadPrev)
		if rerr := m.checkShared(); rerr == nil {
			_ = m.restartShared(45 * time.Second)
		}
		return skipped, err
	}
	if cp, err := os.ReadFile(m.fragmentPath()); err == nil {
		_ = os.WriteFile(m.lastGood(), cp, 0o600)
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

// Apply приводит ядро к списку пулов: пустой список = пустой конфиг.
// В режиме own гасится собственный процесс, в shared снимается фрагмент
// с чужого ядра. Пулы, которые текущий профиль не умеет, не запускаются
// со статусом waits-lx.
func (m *Manager) Apply(pools []PoolSpec) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stmu.Lock()
	m.statuses = map[string]*PoolStatus{}
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
		eligible, reasons := EligibleNodes(spec.Nodes, m.eng.LX)
		if len(eligible) == 0 {
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
	if m.Mode == "shared" {
		return m.applyShared(runnable, skipped)
	}
	if err := m.writeConfig(runnable); err != nil {
		return skipped, err
	}
	m.stop()
	logF, err := os.OpenFile(m.Dir+"/singbox.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND|os.O_TRUNC, 0o600)
	if err != nil {
		return skipped, err
	}
	defer logF.Close()
	cmd := exec.Command(m.eng.Bin, "run", "-c", m.cfgPath())
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
		return skipped, fmt.Errorf("движок стартовал, но clash_api не отвечает: %v (лог: %s/singbox.log)", err, m.Dir)
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
	Running  bool   `json:"running"`
	PID      int    `json:"pid,omitempty"`
	Version  string `json:"version,omitempty"`
	LX       bool   `json:"lx"`
	Bin      string `json:"bin,omitempty"`
	Mixed    int    `json:"mixedPort"`
	Clash    int    `json:"clashPort"`
	Mode     string `json:"mode"`
	Fragment string `json:"fragment,omitempty"`
}

func (m *Manager) Status() Status {
	st := Status{Version: m.eng.Version, LX: m.eng.LX, Bin: m.eng.Bin, Mixed: m.MixedPort, Clash: m.clashBase(), Mode: m.Mode}
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
	return st
}
