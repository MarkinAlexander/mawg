//go:build !linux

package singbox

import "fmt"

type Manager struct{}

func NewManager(dir string) (*Manager, error) {
	return nil, fmt.Errorf("движок sing-box доступен только на роутере")
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

func (m *Manager) Info() Engine { return Engine{} }
func (m *Manager) Apply(pools []PoolSpec) ([]string, error) {
	return nil, fmt.Errorf("движок sing-box доступен только на роутере")
}
func (m *Manager) Status() Status { return Status{} }
