package rotator

// Наборы интерфейсов: участник жив, пока это подтверждено трафиком через
// туннель, а не статусом «включён»: иначе пул с одним нерабочим конфигом
// после каждого кулдауна переприменяется и забирает группы набора.

import (
	"testing"
	"time"

	"mawg/internal/platform"
	"mawg/internal/platform/fake"
	"mawg/internal/store"
	"mawg/internal/wgconf"
)

func addTestPool(t *testing.T, st *store.Store, name, slot, host string) {
	t.Helper()
	if _, err := st.CreatePool(name, store.PoolSettings{
		Platform: store.PlatformKeenetic, KeeneticSlot: slot, Fallback: store.FallbackDirect,
		CheckIntervalSec: 1, FailThreshold: 2, CooldownMin: 5,
	}); err != nil {
		t.Fatal(err)
	}
	body := confBody(keyA, host)
	if _, _, err := st.AddConfigs(name, []wgconf.NamedConfig{{OriginalName: host + ".conf", Raw: body, Config: mustParse(t, body)}}); err != nil {
		t.Fatal(err)
	}
}

// bundleCase - набор chain [primary (nwg7), backup (nwg5)]:
// приоритетный пул с одним конфигом и запасной.
type bundleCase struct {
	t  *testing.T
	st *store.Store
	fb *fake.Fake
	cl *clock
	e  *Engine
}

func newBundleCase(t *testing.T, backend func(*fake.Fake) platform.Backend) *bundleCase {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	addTestPool(t, st, "primary", "Wireguard7", "primary.example.net")
	addTestPool(t, st, "backup", "Wireguard5", "backup.example.net")
	if _, err := st.CreateBundle(store.Bundle{Name: "chain", Members: []string{"primary", "backup"}, Groups: []string{"g1"}}); err != nil {
		t.Fatal(err)
	}
	fb := fake.New()
	fb.SetProbe("primary", true)
	fb.SetProbe("backup", true)
	cl := &clock{t: time.Date(2026, 11, 23, 2, 0, 0, 0, time.UTC)}
	e := New(st, backend(fb), nil)
	e.Clock = cl.Now
	return &bundleCase{t: t, st: st, fb: fb, cl: cl, e: e}
}

func (c *bundleCase) checkPool(name string) {
	c.t.Helper()
	c.cl.Add(20 * time.Second)
	c.e.checkPool(name)
}

// confirm - применить конфиг и подтвердить пул первой проверкой
func (c *bundleCase) confirm(name string) {
	c.t.Helper()
	c.e.checkPool(name)
	c.checkPool(name)
}

func (c *bundleCase) tick() {
	c.t.Helper()
	c.cl.Add(time.Minute)
	c.e.checkBundles(true)
}

func (c *bundleCase) member(want string) {
	c.t.Helper()
	if got := c.st.BundleState("chain").Member; got != want {
		c.t.Fatalf("набор на %q, хочу %q", got, want)
	}
}

// нерабочий пул после кулдауна переприменяет тот же конфиг: рукопожатие
// при включении проходит, данных нет
func TestBundleStaysAwayFromPoolThatOnlyHandshakes(t *testing.T) {
	c := newBundleCase(t, func(fb *fake.Fake) platform.Backend { return fb })
	c.confirm("primary")
	c.confirm("backup")
	c.tick()
	c.member("nwg7")

	c.fb.SetProbe("primary", false)
	for i := 0; i < 3; i++ {
		c.checkPool("primary")
	}
	if got := c.st.State("primary").Mode; got != store.ModeFallback {
		t.Fatalf("режим приоритетного пула = %q, хочу fallback", got)
	}
	c.tick()
	c.member("nwg5")

	c.cl.Add(6 * time.Minute)
	c.checkPool("primary")
	if st := c.st.State("primary"); st.Mode != store.ModeUp || st.Confirmed {
		t.Fatalf("после переприменения: mode=%q confirmed=%v", st.Mode, st.Confirmed)
	}

	c.fb.ProbeOK["primary"] = false
	c.fb.StatusMap["primary"] = platform.TunnelStatus{LinkUp: true, Connected: true, HandshakeAgo: 40}
	for i := 0; i < 2; i++ {
		c.checkPool("primary")
	}
	c.tick()
	c.tick()
	c.tick()
	c.member("nwg5")

	// по-настоящему ожил: ответ пробы, потом три хорошие проверки набора
	c.fb.SetProbe("primary", true)
	c.checkPool("primary")
	if !c.st.State("primary").Confirmed {
		t.Fatal("ответ пробы не подтвердил пул")
	}
	c.tick()
	c.tick()
	c.member("nwg5")
	c.tick()
	c.member("nwg7")
}

// рукопожатие внешнего туннеля без пробы само по себе - не оживание:
// доверие возвращается, только когда оно обновилось
func TestBundleHandshakeNeedsRenewal(t *testing.T) {
	c := newBundleCase(t, func(fb *fake.Fake) platform.Backend {
		return &handshakeBackend{Fake: fb, age: 9999}
	})
	if err := c.st.UpdateBundle("chain", store.Bundle{Name: "chain", Members: []string{"nwg9", "backup"}, Groups: []string{"g1"}}); err != nil {
		t.Fatal(err)
	}
	c.confirm("backup")
	hs := c.e.backend.(*handshakeBackend)
	c.tick()
	c.tick()
	c.member("nwg5")

	hs.age = 10
	for _, age := range []int{10, 70, 130} { // одно рукопожатие стареет
		hs.age = age
		c.tick()
		c.member("nwg5")
	}

	for _, age := range []int{10, 70, 10, 70, 10} { // обновляется каждые две проверки
		hs.age = age
		c.tick()
	}
	c.tick()
	c.member("nwg9")

	// рестарт mawg: счётчики пусты, туннель видят впервые - одно
	// рукопожатие не должно возвращать на него
	c.st.MutateBundleState("chain", func(s *store.BundleState) { s.Member = "nwg5" })
	restarted := New(c.st, c.e.backend, nil)
	restarted.Clock = c.cl.Now
	c.tickOn(restarted)
	c.member("nwg5")
}

func (c *bundleCase) tickOn(e *Engine) {
	c.cl.Add(time.Minute)
	e.checkBundles(true)
}

type handshakeBackend struct {
	*fake.Fake
	age int
}

func (b *handshakeBackend) Slots() ([]platform.SlotInfo, error) {
	return []platform.SlotInfo{{ID: "Wireguard9", Device: "nwg9", LinkUp: true}}, nil
}

func (b *handshakeBackend) IfaceHandshake(device string) int {
	if device == "nwg9" {
		return b.age
	}
	return -1
}

func TestBundleRulesFromSettings(t *testing.T) {
	c := newBundleCase(t, func(fb *fake.Fake) platform.Backend { return fb })
	st := c.st.Settings()
	st.BundleFailCycles, st.BundleRestoreCycles = 3, 1
	if err := c.st.SetSettings(st); err != nil {
		t.Fatal(err)
	}
	c.confirm("primary")
	c.confirm("backup")
	c.tick()
	c.member("nwg7")

	unconfirmed := func() {
		c.cl.Add(time.Minute)
		c.st.MutateState("primary", func(s *store.PoolState) { s.Confirmed = false })
		c.e.checkBundles(true)
	}
	unconfirmed()
	c.member("nwg7")
	unconfirmed()
	c.member("nwg7")
	unconfirmed()
	c.member("nwg5")

	c.cl.Add(time.Minute)
	c.st.MutateState("primary", func(s *store.PoolState) { s.Confirmed = true })
	c.e.checkBundles(true)
	c.member("nwg7")
}

// режим «state» - как до подтверждения пробой: включённый пул жив
func TestBundleStateModeTrustsEnabledPool(t *testing.T) {
	c := newBundleCase(t, func(fb *fake.Fake) platform.Backend { return fb })
	st := c.st.Settings()
	st.HealthSource = store.HealthState
	if err := c.st.SetSettings(st); err != nil {
		t.Fatal(err)
	}
	c.confirm("backup")
	c.tick()
	c.member("nwg7")
}

func TestBundleMemberStates(t *testing.T) {
	c := newBundleCase(t, func(fb *fake.Fake) platform.Backend { return fb })
	c.confirm("backup")
	c.fb.SetProbe("primary", false)
	c.checkPool("primary")
	c.checkPool("primary")
	c.checkPool("primary")
	states := c.e.BundleMemberStates([]string{"primary", "backup", "nwg99"})
	if states["primary"].OK || states["primary"].Note != "пул в фоллбэке" {
		t.Fatalf("primary: %+v", states["primary"])
	}
	if !states["backup"].OK || states["backup"].Device != "nwg5" {
		t.Fatalf("backup: %+v", states["backup"])
	}
	if states["nwg99"].OK || states["nwg99"].Note != "интерфейс не найден" {
		t.Fatalf("nwg99: %+v", states["nwg99"])
	}
}

// Чужой туннель (живёт в SysTunnels, не в слотах) как внешний член набора:
// здоровье по пробе, а не «не найден в слотах = мёртв» (кейс владельца
// 12.11: tun9 wdttc основной, proton запасной)
func TestMemberStateForeignTunnel(t *testing.T) {
	st, fb, cl := setup(t, store.FallbackDirect)
	if err := st.SetIfaceMode("tun9", store.IfaceExternal); err != nil {
		t.Fatal(err)
	}
	probe := store.ProbeConfig{Target: "8.8.8.8", MaxRTTms: 500}
	if err := st.SetIfaceProbe("tun9", &probe); err != nil {
		t.Fatal(err)
	}
	e := newEngine(st.Store, fb, cl, "")
	fb.TunnelsOverride = []platform.SlotInfo{{Device: "tun9", LinkUp: true}}

	fb.DeviceProbe["tun9"] = fake.DevProbe{OK: true, RTTms: 100}
	if got := e.memberState("tun9", fb.TunnelsOverride, e.deviceProbe); !got.OK || got.Device != "tun9" {
		t.Fatalf("живой чужой туннель: %+v", got)
	}

	// TTL кэша проб (30с): без сдвига фейковых часов вернётся закэшированный ок
	cl.Add(31 * time.Second)
	fb.DeviceProbe["tun9"] = fake.DevProbe{OK: false}
	if got := e.memberState("tun9", fb.TunnelsOverride, e.deviceProbe); got.OK {
		t.Fatalf("упавшая проба чужого туннеля: %+v", got)
	}
}

// Гистерезис доверия внешнего члена набора: одиночные «ок» флапающего
// туннеля не обнуляют счётчик провалов и не возвращают набор (кейс
// владельца 12.11: tun9 wdttc флапает, ТГ рвётся в мёртвых окнах).
func TestBundleForeignTunnelTrustHysteresis(t *testing.T) {
	st, fb, cl := setup(t, store.FallbackDirect)
	if err := st.SetIfaceMode("tun9", store.IfaceExternal); err != nil {
		t.Fatal(err)
	}
	probe := store.ProbeConfig{Target: "8.8.8.8", MaxRTTms: 500}
	if err := st.SetIfaceProbe("tun9", &probe); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateBundle(store.Bundle{Name: "tg", Members: []string{"tun9", "proton"}, Groups: []string{"g1"}}); err != nil {
		t.Fatal(err)
	}
	e := newEngine(st.Store, fb, cl, "")
	fb.TunnelsOverride = []platform.SlotInfo{{Device: "tun9", LinkUp: true}}
	state := func() string { return st.BundleState("tg").Member }
	setProbe := func(ok bool) {
		cl.Add(31 * time.Second) // свежий сэмпл пробы (TTL кэша 30с)
		fb.DeviceProbe["tun9"] = fake.DevProbe{OK: ok, RTTms: 100}
	}
	e.checkPool("proton")
	cl.Add(20 * time.Second)
	e.checkPool("proton") // запасной подтверждён пробой

	setProbe(true)
	e.checkBundles(true)
	if state() != "tun9" {
		t.Fatalf("живой tun9 - набор на tun9, state=%s", state())
	}

	setProbe(false) // первая плохая - текущего не дёргаем
	e.checkBundles(true)
	if state() != "tun9" {
		t.Fatalf("после первой плохой пробы текущий держится: %s", state())
	}

	setProbe(false) // вторая подряд - уводим на proton (его устройство nwg2 в фейке)
	e.checkBundles(true)
	if state() != "nwg2" {
		t.Fatalf("две плохие подряд - набор на proton: %s", state())
	}

	setProbe(true) // одиночное ок - доверия нет, остаёмся на proton
	e.checkBundles(true)
	if state() != "nwg2" {
		t.Fatalf("одиночное ок не должно возвращать на tun9: %s", state())
	}

	setProbe(true)
	e.checkBundles(true)
	setProbe(true) // третий успех подряд - доверие восстановлено
	e.checkBundles(true)
	if state() != "tun9" {
		t.Fatalf("три ок подряд - возврат на tun9: %s", state())
	}
}
