//go:build !linux

package mihomo

import (
	"context"
	"fmt"
	"time"

	"mawg/internal/singbox"
)

// Стабы для разработки на Windows: менеджер mihomo живёт только на
// роутере (build tag linux), как и у sing-box.

type Manager struct{}

type RunConfig struct {
	WebPort  int
	LogEvent func(pool, kind, msg string)
}

func NewManager(dir string) (*Manager, error) {
	return NewManagerConfig(dir, RunConfig{})
}

func NewManagerConfig(dir string, cfg RunConfig) (*Manager, error) {
	return nil, fmt.Errorf("ядро mihomo доступно только на роутере")
}

func (m *Manager) StopOwn() {}
func (m *Manager) Close()   {}

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

// CacheDir - каталог состояния ядра на роутере (tmpfs, см. manager_linux).
const CacheDir = "/tmp/mihomo-mawg"

func (m *Manager) Info() Engine { return Engine{} }
func (m *Manager) Apply(pools []PoolSpec) ([]string, error) {
	return nil, fmt.Errorf("ядро mihomo доступно только на роутере")
}
func (m *Manager) Status() Status { return Status{} }

func (m *Manager) PoolStatus(name string) (singbox.PoolStatus, bool) {
	return singbox.PoolStatus{}, false
}

func (m *Manager) ClashProxies(ctx context.Context) (map[string]singbox.ClashProxy, error) {
	return nil, fmt.Errorf("ядро mihomo доступно только на роутере")
}

func (m *Manager) ClashSelect(ctx context.Context, group, name string) error {
	return fmt.Errorf("ядро mihomo доступно только на роутере")
}

func (m *Manager) ClashGroupDelay(ctx context.Context, group, testURL string, timeout time.Duration) (map[string]int, error) {
	return nil, fmt.Errorf("ядро mihomo доступно только на роутере")
}
