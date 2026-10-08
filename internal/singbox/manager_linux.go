//go:build linux

package singbox

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Manager struct {
	mu        sync.Mutex
	stmu      sync.Mutex
	eng       *Engine
	Dir       string
	MixedPort int
	ClashPort int
	pid       int
	lastSpecs []PoolSpec
	statuses  map[string]*PoolStatus
}

func NewManager(dir string) (*Manager, error) {
	eng, err := Detect()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	m := &Manager{eng: eng, Dir: dir, MixedPort: 2281, ClashPort: 2291, statuses: map[string]*PoolStatus{}}
	go m.probeLoop()
	return m, nil
}

func (m *Manager) Info() Engine {
	return *m.eng
}

type PoolStatus struct {
	Eligible  int       `json:"eligible"`
	MixedPort int       `json:"mixedPort"`
	ProbeOK   bool      `json:"probeOk"`
	ProbeMs   int       `json:"probeMs"`
	ProbeErr  string    `json:"probeErr,omitempty"`
	CheckedAt time.Time `json:"checkedAt,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	Detail    string    `json:"detail,omitempty"`
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
	st := &PoolStatus{
		Eligible: len(spec.Nodes), MixedPort: spec.MixedPort,
		ProbeOK: err == nil, ProbeMs: ms, CheckedAt: time.Now(),
	}
	if err != nil {
		st.ProbeErr = err.Error()
	}
	m.setStatus(spec.Name, st)
}

func (m *Manager) probeLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		m.mu.Lock()
		specs := m.lastSpecs
		m.mu.Unlock()
		for _, spec := range specs {
			if spec.ProbeTarget == "" {
				continue
			}
			m.probeSpec(spec)
		}
	}
}

func (m *Manager) cfgPath() string  { return m.Dir + "/config.json" }
func (m *Manager) pidPath() string  { return m.Dir + "/run.pid" }
func (m *Manager) lastGood() string { return m.Dir + "/config.last-good.json" }

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
	data, _, err := BuildConfig(pools, Params{ClashPort: m.ClashPort, LX: m.eng.LX})
	if err != nil {
		return err
	}
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

// Apply приводит экземпляр к списку пулов: пустой список = процесс остановлен.
// Пулы, которые текущий профиль не умеет, не запускаются со статусом waits-lx.
func (m *Manager) Apply(pools []PoolSpec) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stmu.Lock()
	m.statuses = map[string]*PoolStatus{}
	m.stmu.Unlock()
	if len(pools) == 0 {
		m.stop()
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
		if spec.ProbeTarget == "" {
			spec.ProbeTarget = "http://www.gstatic.com/generate_204"
		}
		runnable = append(runnable, spec)
	}
	if len(runnable) == 0 {
		m.stop()
		m.lastSpecs = nil
		return skipped, nil
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
	PID     int    `json:"pid,omitempty"`
	Version string `json:"version,omitempty"`
	LX      bool   `json:"lx"`
	Bin     string `json:"bin,omitempty"`
	Mixed   int    `json:"mixedPort"`
	Clash   int    `json:"clashPort"`
}

func (m *Manager) Status() Status {
	st := Status{Version: m.eng.Version, LX: m.eng.LX, Bin: m.eng.Bin, Mixed: m.MixedPort, Clash: m.ClashPort}
	st.Running = m.alive()
	if st.Running {
		st.PID = m.pid
	}
	return st
}
