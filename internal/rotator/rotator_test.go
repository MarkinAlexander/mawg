package rotator

import (
	"encoding/json"
	"strings"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"mawg/internal/magitrickle"
	"mawg/internal/platform"
	"mawg/internal/platform/fake"
	"mawg/internal/store"
	"mawg/internal/wgconf"
)

type clock struct{ t time.Time }

func (c *clock) Now() time.Time      { return c.t }
func (c *clock) Add(d time.Duration) { c.t = c.t.Add(d) }

const (
	keyA = "qELLdGEcuWN4Cw04dU6EcD6nSuVPWHFISHlWFPjlyl0="
	keyB = "qELLdGEcuWN4Cw04dU6EcD6nSuVPWHFISHlWFPjlyl1="
	keyC = "qELLdGEcuWN4Cw04dU6EcD6nSuVPWHFISHlWFPjlyl2="
	peer = "a7cbHcmzL3FwW7+Fg6Xo9FCdIYnDhWQeWm2c1A2B3C4="
)

func confBody(priv, host string) []byte {
	body := "[Interface]\nPrivateKey = " + priv + "\nAddress = 10.2.0.2/32\n"
	body += "[Peer]\nPublicKey = " + peer + "\nEndpoint = " + host + ":51820\nAllowedIPs = 0.0.0.0/0\n"
	return []byte(body)
}

func setup(t *testing.T, fallback string) (*Store2, *fake.Fake, *clock) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := st.CreatePool("proton", store.PoolSettings{
		Platform:         store.PlatformKeenetic,
		KeeneticSlot:     "Wireguard2",
		Fallback:         fallback,
		CheckIntervalSec: 1,
		FailThreshold:    2,
		CooldownMin:      5,
	})
	if err != nil {
		t.Fatal(err)
	}
	in := []wgconf.NamedConfig{
		{OriginalName: "AT-1.conf", Raw: confBody(keyA, "at1.example.net"), Config: mustParse(t, confBody(keyA, "at1.example.net"))},
		{OriginalName: "AT-2.conf", Raw: confBody(keyB, "at2.example.net"), Config: mustParse(t, confBody(keyB, "at2.example.net"))},
		{OriginalName: "AT-3.conf", Raw: confBody(keyC, "at3.example.net"), Config: mustParse(t, confBody(keyC, "at3.example.net"))},
	}
	if _, _, err := st.AddConfigs(pool.Name, in); err != nil {
		t.Fatal(err)
	}
	fb := fake.New()
	fb.SetProbe("proton", true)
	cl := &clock{t: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	return &Store2{st}, fb, cl
}

type Store2 struct{ *store.Store }

func mustParse(t *testing.T, b []byte) wgconf.Config {
	t.Helper()
	cfg, err := wgconf.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func newEngine(st *store.Store, fb *fake.Fake, cl *clock, mtURL string) *Engine {
	var mt *magitrickle.Client
	if mtURL != "" {
		mt = magitrickle.New(mtURL)
	}
	e := New(st, fb, mt)
	e.Clock = cl.Now
	return e
}

func TestFirstApplyPicksFirstConfig(t *testing.T) {
	st, fb, cl := setup(t, store.FallbackDirect)
	e := newEngine(st.Store, fb, cl, "")
	e.checkPool("proton")
	applied := fb.Applied()
	if len(applied) != 1 || applied[0] != "at1.example.net:51820" {
		t.Fatalf("applied = %v", applied)
	}
	if st.State("proton").ActiveFile == "" {
		t.Fatal("no active file")
	}
}

func TestRotationOnFailures(t *testing.T) {
	st, fb, cl := setup(t, store.FallbackDirect)
	e := newEngine(st.Store, fb, cl, "")
	e.checkPool("proton")
	fb.SetProbe("proton", false)

	e.checkPool("proton")
	e.checkPool("proton")
	applied := fb.Applied()
	if len(applied) != 1 {
		t.Fatalf("premature rotation: %v", applied)
	}

	cl.Add(20 * time.Second)
	e.checkPool("proton")
	e.checkPool("proton")
	applied = fb.Applied()
	if len(applied) != 2 || applied[1] != "at2.example.net:51820" {
		t.Fatalf("after threshold applied = %v", applied)
	}
	if st.State("proton").ConsecFails != 0 {
		t.Fatal("fails not reset")
	}
}

func TestCooldownSkipsToNext(t *testing.T) {
	st, fb, cl := setup(t, store.FallbackDirect)
	e := newEngine(st.Store, fb, cl, "")
	e.checkPool("proton")
	fb.SetProbe("proton", false)

	cl.Add(20 * time.Second)
	e.checkPool("proton")
	e.checkPool("proton")

	cl.Add(20 * time.Second)
	e.checkPool("proton")
	e.checkPool("proton")

	applied := fb.Applied()
	if len(applied) != 3 {
		t.Fatalf("applied = %v", applied)
	}
	if applied[1] != "at2.example.net:51820" || applied[2] != "at3.example.net:51820" {
		t.Fatalf("rotation order broken: %v", applied)
	}
}

func TestFallbackDirectDisablesAndRecovers(t *testing.T) {
	var mu sync.Mutex
	groups := []magitrickle.Group{{ID: "g1", Name: "test", Interface: "nwg2", Enable: true}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/groups":
			json.NewEncoder(w).Encode(map[string]any{"groups": groups})
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/groups/g1":
			var g magitrickle.Group
			json.NewDecoder(r.Body).Decode(&g)
			g.ID = "g1"
			groups[0] = g
			w.WriteHeader(200)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	st, fb, cl := setup(t, store.FallbackDirect)
	e := newEngine(st.Store, fb, cl, srv.URL)
	e.checkPool("proton")
	fb.SetProbe("proton", false)

	cl.Add(20 * time.Second)
	e.checkPool("proton")
	e.checkPool("proton")

	cl.Add(20 * time.Second)
	e.checkPool("proton")
	e.checkPool("proton")

	cl.Add(20 * time.Second)
	e.checkPool("proton")
	e.checkPool("proton")

	if fb.Downs() == 0 {
		t.Fatal("interface was not brought down on fallback")
	}
	state := st.State("proton")
	if state.Mode != store.ModeFallback {
		t.Fatalf("mode = %s", state.Mode)
	}
	mu.Lock()
	enabled := groups[0].Enable
	mu.Unlock()
	if enabled {
		t.Fatal("magitrickle group not disabled")
	}

	fb.SetProbe("proton", true)
	cl.Add(6 * time.Minute)
	e.checkPool("proton")

	state = st.State("proton")
	if state.Mode != store.ModeUp {
		t.Fatalf("did not recover, mode = %s, result = %s", state.Mode, state.LastResult)
	}
	mu.Lock()
	enabled = groups[0].Enable
	mu.Unlock()
	if !enabled {
		t.Fatal("magitrickle group not re-enabled")
	}
}

func TestFallbackHoldKeepsInterface(t *testing.T) {
	st, fb, cl := setup(t, store.FallbackHold)
	e := newEngine(st.Store, fb, cl, "")
	e.checkPool("proton")
	fb.SetProbe("proton", false)

	cl.Add(20 * time.Second)
	e.checkPool("proton")
	e.checkPool("proton")
	cl.Add(40 * time.Second)
	e.checkPool("proton")
	e.checkPool("proton")
	cl.Add(40 * time.Second)
	e.checkPool("proton")
	e.checkPool("proton")

	if fb.Downs() != 0 {
		t.Fatal("hold mode must not bring interface down")
	}
	if st.State("proton").Mode != store.ModeFallback {
		t.Fatal("expected fallback state")
	}
}

func TestApplyErrorCooldownsConfig(t *testing.T) {
	st, fb, cl := setup(t, store.FallbackDirect)
	fb.ApplyErrOn["at1.example.net:51820"] = os.ErrDeadlineExceeded
	e := newEngine(st.Store, fb, cl, "")
	e.checkPool("proton")
	applied := fb.Applied()
	if len(applied) != 1 || applied[0] != "at2.example.net:51820" {
		t.Fatalf("apply error must skip config: %v", applied)
	}
}

func TestEventsPersisted(t *testing.T) {
	st, fb, cl := setup(t, store.FallbackDirect)
	e := newEngine(st.Store, fb, cl, "")
	e.checkPool("proton")
	events := st.Events()
	if len(events) == 0 {
		t.Fatal("no events logged")
	}
}

func TestStorePersistsAcrossOpen(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	st.CreatePool("warp", store.PoolSettings{Platform: store.PlatformOpenwrt})
	cfg := mustParse(t, confBody(keyA, "warp.example.net"))
	st.AddConfigs("warp", []wgconf.NamedConfig{{OriginalName: "warp.conf", Raw: confBody(keyA, "warp.example.net"), Config: cfg}})
	st.MutateState("warp", func(s *store.PoolState) { s.ActiveFile = "warp-1.conf" })

	st2, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	pool, ok := st2.Pool("warp")
	if !ok || len(pool.Configs) != 1 {
		t.Fatalf("pool after reopen: %+v", pool)
	}
	if _, err := st2.LoadConfigFile("warp", pool.Configs[0].File); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "state.json")); err != nil {
		t.Fatal("state file missing")
	}
	if st2.State("warp").ActiveFile != "warp-1.conf" {
		t.Fatalf("active file not persisted: %q", st2.State("warp").ActiveFile)
	}
	_ = platform.SlotInfo{}
}

func TestManualDisableStopsRotation(t *testing.T) {
	st, fb, cl := setup(t, store.FallbackDirect)
	e := newEngine(st.Store, fb, cl, "")
	e.checkPool("proton")
	fb.SetProbe("proton", false)

	if err := e.DisablePool("proton"); err != nil {
		t.Fatal(err)
	}
	pool, _ := st.Pool("proton")
	if !pool.Disabled {
		t.Fatal("pool must be marked disabled")
	}
	if fb.Downs() != 1 {
		t.Fatalf("interface down calls = %d", fb.Downs())
	}

	cl.Add(10 * time.Minute)
	e.checkPool("proton")
	e.checkPool("proton")
	e.checkPool("proton")
	if got := fb.Applied(); len(got) != 1 {
		t.Fatalf("no rotations expected while disabled: %v", got)
	}
	if got := fb.Downs(); got != 1 {
		t.Fatalf("repeated downs while disabled: %d", got)
	}
	if err := e.RotateNow("proton"); err == nil {
		t.Fatal("rotate must be refused for disabled pool")
	}

	fb.SetProbe("proton", true)
	if err := e.EnablePool("proton"); err != nil {
		t.Fatal(err)
	}
	pool, _ = st.Pool("proton")
	if pool.Disabled {
		t.Fatal("pool must be enabled back")
	}
	if got := fb.Applied(); len(got) != 2 {
		t.Fatalf("enable must re-apply config: %v", got)
	}
	if st.State("proton").Mode != store.ModeUp {
		t.Fatalf("mode after enable = %s", st.State("proton").Mode)
	}
}

func TestFallbackPoolRebindsGroups(t *testing.T) {
	var mu sync.Mutex
	groups := []magitrickle.Group{{ID: "g1", Name: "test", Interface: "nwg2", Enable: true}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/groups":
			q := r.URL.Query().Get("with_rules")
			_ = q
			json.NewEncoder(w).Encode(map[string]any{"groups": groups})
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/groups/g1":
			var g magitrickle.Group
			json.NewDecoder(r.Body).Decode(&g)
			g.ID = "g1"
			groups[0] = g
			w.WriteHeader(200)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	st, fb, cl := setup(t, "pool:warp")
	if _, err := st.CreatePool("warp", store.PoolSettings{
		Platform: store.PlatformKeenetic, KeeneticSlot: "Wireguard4",
	}); err != nil {
		t.Fatal(err)
	}
	e := newEngine(st.Store, fb, cl, srv.URL)
	e.checkPool("proton")
	fb.SetProbe("proton", false)

	for i := 0; i < 3; i++ {
		cl.Add(20 * time.Second)
		e.checkPool("proton")
		e.checkPool("proton")
	}

	if fb.Downs() == 0 {
		t.Fatal("interface was not brought down on fallback")
	}
	mu.Lock()
	iface := groups[0].Interface
	enabled := groups[0].Enable
	mu.Unlock()
	if iface != "nwg4" {
		t.Fatalf("group not rebound to fallback pool, interface = %s", iface)
	}
	if !enabled {
		t.Fatal("group must stay enabled when rebound")
	}
	if got := st.State("proton").ReboundGroups; len(got) != 1 || got[0] != "g1" {
		t.Fatalf("reboundGroups = %v", got)
	}

	fb.SetProbe("proton", true)
	cl.Add(6 * time.Minute)
	e.checkPool("proton")

	if st.State("proton").Mode != store.ModeUp {
		t.Fatal("did not recover")
	}
	mu.Lock()
	iface = groups[0].Interface
	mu.Unlock()
	if iface != "nwg2" {
		t.Fatalf("group not rebound back, interface = %s", iface)
	}
	if got := st.State("proton").ReboundGroups; len(got) != 0 {
		t.Fatalf("reboundGroups not cleared: %v", got)
	}
}

func TestRTTThresholdRotates(t *testing.T) {
	st, fb, cl := setup(t, store.FallbackDirect)
	fb.DeviceProbe["proton"] = fake.DevProbe{}
	fb.ProbeRTT = map[string]int{"proton": 800}
	e := newEngine(st.Store, fb, cl, "")
	st.UpdatePool("proton", store.PoolSettings{
		Platform: store.PlatformKeenetic, KeeneticSlot: "Wireguard2",
		Fallback: store.FallbackDirect, CheckIntervalSec: 1, FailThreshold: 2, CooldownMin: 5,
		ProbeHost: "1.1.1.1", MaxRTTms: 700,
	})
	e.checkPool("proton")
	fb.SetProbe("proton", true)
	cl.Add(20 * time.Second)
	e.checkPool("proton")
	cl.Add(20 * time.Second)
	e.checkPool("proton")
	applied := fb.Applied()
	if len(applied) != 2 {
		t.Fatalf("rtt threshold did not rotate, applied = %v", applied)
	}
	found := false
	for _, ev := range st.Events() {
		if ev.Kind == "rotate" && strings.Contains(ev.Message, "800ms") {
			found = true
		}
	}
	if !found {
		t.Fatal("rotate event lacks rtt detail")
	}
}

func TestWANDownSkipsFailures(t *testing.T) {
	st, fb, cl := setup(t, store.FallbackDirect)
	st.SetWANProbe(&store.ProbeConfig{Type: store.ProbeTypeICMP, Target: "8.8.8.8"})
	e := newEngine(st.Store, fb, cl, "")
	e.WANProbeFn = func() bool { return true }
	e.checkPool("proton")
	fb.SetProbe("proton", false)
	for i := 0; i < 6; i++ {
		cl.Add(20 * time.Second)
		e.checkPool("proton")
		e.checkPool("proton")
	}
	state := st.State("proton")
	if state.ConsecFails != 0 {
		t.Fatalf("failures counted while wan down: %d", state.ConsecFails)
	}
	if !strings.Contains(state.LastResult, "канал провайдера") {
		t.Fatalf("detail = %q", state.LastResult)
	}
	if state.Mode != store.ModeUp {
		t.Fatalf("mode = %s, must stay up", state.Mode)
	}

	e.WANProbeFn = func() bool { return false }
	cl.Add(20 * time.Second)
	e.checkPool("proton")
	cl.Add(20 * time.Second)
	e.checkPool("proton")
	if len(fb.Applied()) != 2 {
		t.Fatal("rotation did not start after wan recovered")
	}
	if strings.Contains(st.State("proton").LastResult, "канал провайдера") {
		t.Fatalf("detail = %q", st.State("proton").LastResult)
	}
}
