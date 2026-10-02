package platform

import (
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
}

type Backend interface {
	Name() string
	Detect() error
	Slots() ([]SlotInfo, error)
	SysTunnels() ([]SlotInfo, error)
	Apply(pool store.Pool, cfg wgconf.Config) error
	Up(pool store.Pool) error
	Down(pool store.Pool) error
	Status(pool store.Pool) (TunnelStatus, error)
	Probe(pool store.Pool, host string) (ok bool, rttMs int, err error)
	IfaceHandshake(device string) int
	ProbeDevice(device, target string) (ok bool, rttMs int)
	RestartMagitrickle() error
}
