//go:build !linux

package singbox

import (
	"fmt"
	"time"
)

type Manager struct{}

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
	return nil, fmt.Errorf("движок sing-box доступен только на роутере")
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

func (m *Manager) Info() Engine { return Engine{} }
func (m *Manager) Close()       {}
func (m *Manager) Apply(pools []PoolSpec) ([]string, error) {
	return nil, fmt.Errorf("движок sing-box доступен только на роутере")
}
func (m *Manager) Status() Status { return Status{} }

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

func (m *Manager) PoolStatus(name string) (PoolStatus, bool) { return PoolStatus{}, false }
