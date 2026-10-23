package platform

import (
	"time"

	"mawg/internal/store"
	"mawg/internal/wgconf"
)

type TunnelStatus struct {
	LinkUp       bool
	Connected    bool
	HandshakeAgo int
}

type SlotInfo struct {
	ID          string `json:"id"`
	Device      string `json:"device"`
	Description string `json:"description"`
	LinkUp      bool   `json:"linkUp"`
	Connected   bool   `json:"connected"`
	Managed     bool   `json:"managed"`
	Address     string `json:"address,omitempty"`
}

type Backend interface {
	Name() string
	Detect() error
	Slots() ([]SlotInfo, error)
	SysTunnels() ([]SlotInfo, error)
	Apply(pool store.Pool, cfg wgconf.Config) error
	Up(pool store.Pool) error
	Down(pool store.Pool) error
	// Destroy вычищает интерфейс роутера при удалении пула: Down гасит
	// линк/адрес, но сам слот/uci-секция остаются призраками в вебе роутера
	Destroy(pool store.Pool) error
	Status(pool store.Pool) (TunnelStatus, error)
	Probe(pool store.Pool, host string) (ok bool, rttMs int, err error)
	IfaceHandshake(device string) int
	ProbeDevice(device, target string) (ok bool, rttMs int)
	RestartMagitrickle() error
	// MTConfigPath - дисковый конфиг MagiTrickle на этой платформе
	// (цел при усечённом рантайме, спасает группы рестартом демона);
	// пусто = платформа не знает пути, бэкапится только shadow.
	MTConfigPath() string
	SaveConfig() error
}

// Endpointer - опциональная возможность платформы: адреса (без портов)
// удалённых пиров роутерного WG-интерфейса. Нужно каскадам с
// iface-источником (баг владельца: панель требовала адреса от руки,
// хотя Кинетик отдаёт их в RCI/ndmc).
type Endpointer interface {
	IfaceEndpoints(device string) ([]string, error)
}

// ShellRunner - опциональная возможность платформы: ограниченный шелл.
// Нужен DNS-менеджеру: состояние dnsmasq/remap53 и uci-мутации
// (46-док §3, срез OpenWrt 64-док).
type ShellRunner interface {
	RunShell(script string, timeout time.Duration) (string, error)
}

// SlotCreator - опциональная возможность платформы: создание нового
// WG-слота (Keenetic). Генератору WARP нужно оно при первом warp-пуле
// на роутере, где слотов ещё нет (85-док: авто-слот в мастере).
type SlotCreator interface {
	CreateSlot() (string, error)
}
