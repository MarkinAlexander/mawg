package fake

import (
	"fmt"
	"sync"

	"mawg/internal/platform"
	"mawg/internal/store"
	"mawg/internal/wgconf"
)

type Fake struct {
	mu sync.Mutex

	AppliedEndpoints []string
	DownCalled       []string
	DestroyCalled    []string
	UpCalled         []string

	ProbeOK     map[string]bool
	ProbeRTT    map[string]int
	StatusMap   map[string]platform.TunnelStatus
	ApplyErrOn  map[string]error
	DeviceProbe map[string]DevProbe
}

type DevProbe struct {
	OK    bool
	RTTms int
}

func New() *Fake {
	return &Fake{
		ProbeOK:     map[string]bool{},
		ProbeRTT:    map[string]int{},
		StatusMap:   map[string]platform.TunnelStatus{},
		ApplyErrOn:  map[string]error{},
		DeviceProbe: map[string]DevProbe{},
	}
}

func (f *Fake) Name() string  { return "fake" }
func (f *Fake) Detect() error { return nil }

func (f *Fake) Slots() ([]platform.SlotInfo, error) {
	return []platform.SlotInfo{{ID: "Wireguard2", Device: "nwg2", Managed: true}}, nil
}

func (f *Fake) Apply(pool store.Pool, cfg wgconf.Config) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.ApplyErrOn[cfg.Endpoint()]; ok {
		return err
	}
	f.AppliedEndpoints = append(f.AppliedEndpoints, cfg.Endpoint())
	return nil
}

func (f *Fake) Up(pool store.Pool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.UpCalled = append(f.UpCalled, pool.Name)
	return nil
}

func (f *Fake) Down(pool store.Pool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.DownCalled = append(f.DownCalled, pool.Name)
	return nil
}

func (f *Fake) Destroy(pool store.Pool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.DestroyCalled = append(f.DestroyCalled, pool.Name)
	return nil
}

func (f *Fake) Status(pool store.Pool) (platform.TunnelStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.StatusMap[pool.Name]
	if !ok {
		return platform.TunnelStatus{}, fmt.Errorf("no status for %s", pool.Name)
	}
	return st, nil
}

func (f *Fake) Probe(pool store.Pool, host string) (bool, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ok, set := f.ProbeOK[pool.Name]
	return set && ok, f.ProbeRTT[pool.Name], nil
}

func (f *Fake) IfaceHandshake(device string) int { return -1 }

func (f *Fake) SysTunnels() ([]platform.SlotInfo, error) {
	return nil, nil
}

func (f *Fake) ProbeDevice(device, target string) (bool, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, set := f.DeviceProbe[device]
	return set && st.OK, st.RTTms
}

func (f *Fake) SetProbe(pool string, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ProbeOK[pool] = ok
	st := f.StatusMap[pool]
	st.LinkUp = ok
	st.Connected = ok
	f.StatusMap[pool] = st
}

func (f *Fake) Ups() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.UpCalled)
}

func (f *Fake) Downs() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.DownCalled)
}

func (f *Fake) Applied() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.AppliedEndpoints))
	copy(out, f.AppliedEndpoints)
	return out
}

func (f *Fake) RestartMagitrickle() error { return nil }

func (f *Fake) SaveConfig() error { return nil }

func (f *Fake) Destroys() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.DestroyCalled)
}
