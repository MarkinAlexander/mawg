//go:build linux

package singbox

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"
)

type Manager struct {
	mu        sync.Mutex
	eng       *Engine
	Dir       string
	MixedPort int
	ClashPort int
	pid       int
}

func NewManager(dir string) (*Manager, error) {
	eng, err := Detect()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Manager{eng: eng, Dir: dir, MixedPort: 2281, ClashPort: 2291}, nil
}

func (m *Manager) Info() Engine {
	return *m.eng
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

func (m *Manager) writeConfig(pools []PoolSpec) ([]string, error) {
	data, skipped, err := BuildConfig(pools, Params{MixedPort: m.MixedPort, ClashPort: m.ClashPort, LX: m.eng.LX})
	if err != nil {
		return skipped, err
	}
	tmp := m.cfgPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return skipped, err
	}
	if err := m.eng.Check(tmp); err != nil {
		os.Remove(tmp)
		return skipped, err
	}
	if err := os.Rename(tmp, m.cfgPath()); err != nil {
		return skipped, err
	}
	return skipped, nil
}

// Apply приводит экземпляр к списку пулов: пустой список = процесс остановлен
func (m *Manager) Apply(pools []PoolSpec) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(pools) == 0 {
		m.stop()
		return nil, nil
	}
	skipped, err := m.writeConfig(pools)
	if err != nil {
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
	return skipped, nil
}

func (m *Manager) waitClash(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	url := fmt.Sprintf("http://127.0.0.1:%d/version", m.ClashPort)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
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
