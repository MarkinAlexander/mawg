package rotator

// Умный каскад пулов - фейк-парк (86-док): блокировка пула -> включение
// каскада через Via с сохранением ModeUp, перебор Via по здоровью,
// возврат по расписанию/интервалу (часы Engine.Clock), антифлап,
// ручные кнопки, ensure служебной группы при сохранении настроек.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"mawg/internal/magitrickle"
	"mawg/internal/platform/fake"
	"mawg/internal/store"
	"mawg/internal/wgconf"
)

// mtState - живой фейк MT: группы с мутациями и выдачей ID.
type mtState struct {
	mu      sync.Mutex
	groups  []magitrickle.Group
	nextID  int
	baseURL string
	// onGroup - колбэк «у группы появился ID» (фейк iptables заводит
	// цепочку MT_<id>, как это делает живой MT при записи группы)
	onGroup func(id string)
}

func newMTFake(t *testing.T) *mtState {
	t.Helper()
	magitrickle.SetShadowStorage(nil, nil)
	s := &mtState{nextID: 100}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/groups":
			json.NewEncoder(w).Encode(map[string]any{"groups": s.groups})
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/groups":
			var req struct {
				Groups []magitrickle.Group `json:"groups"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			// живой MT выдаёт id сам на bulk-PUT (AddGroup шлёт без id)
			for i := range req.Groups {
				if req.Groups[i].ID == "" {
					s.nextID++
					req.Groups[i].ID = fmt.Sprintf("g%d", s.nextID)
					id := req.Groups[i].ID
					if s.onGroup != nil {
						s.onGroup(id)
					}
				}
			}
			s.groups = req.Groups
			w.WriteHeader(200)
		case r.Method == http.MethodDelete && len(r.URL.Path) > len("/api/v1/groups/"):
			id := r.URL.Path[len("/api/v1/groups/"):]
			out := s.groups[:0]
			for _, g := range s.groups {
				if g.ID != id {
					out = append(out, g)
				}
			}
			s.groups = out
			w.WriteHeader(200)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { magitrickle.SetShadowStorage(nil, nil) })
	s.baseURL = srv.URL
	return s
}

type fakeSmartIpt struct {
	mu     sync.Mutex
	output []string
	chains map[string]bool
}

func (f *fakeSmartIpt) run(args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	join := args
	_ = join
	switch {
	case len(args) == 4 && args[0] == "-t" && args[1] == "mangle" && args[2] == "-S" && args[3] == "OUTPUT":
		out := ""
		for _, c := range f.output {
			out += "-A OUTPUT -j " + c + "\n"
		}
		return out, nil
	case len(args) == 6 && args[2] == "-D" && args[3] == "OUTPUT":
		for i, c := range f.output {
			if c == args[5] {
				f.output = append(f.output[:i], f.output[i+1:]...)
				break
			}
		}
		return "", nil
	case len(args) == 6 && args[2] == "-A" && args[3] == "OUTPUT":
		for _, c := range f.output {
			if c == args[5] {
				return "", nil
			}
		}
		f.output = append(f.output, args[5])
		return "", nil
	case len(args) == 5 && args[2] == "-L":
		if f.chains[args[3]] {
			return "Chain " + args[3], nil
		}
		return "", fmt.Errorf("no chain %s", args[3])
	}
	return "", nil
}

func (f *fakeSmartIpt) has(chain string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.output {
		if c == chain {
			return true
		}
	}
	return false
}

type smartPark struct {
	st *store.Store
	fb *fake.Fake
	cl *clock
	e  *Engine
	mt *mtState
	ipt *fakeSmartIpt
}

func ipConf(priv, ep string) []byte {
	return []byte("[Interface]\nPrivateKey = " + priv + "\nAddress = 10.2.0.2/32\n" +
		"[Peer]\nPublicKey = " + peer + "\nEndpoint = " + ep + "\nAllowedIPs = 0.0.0.0/0\n")
}

// smartSetup: proton (2 конфига) с умным каскадом через warp (warp жив),
// движок на фейках MT/iptables с управляемыми часами.
func smartSetup(t *testing.T, viaMode string) *smartPark {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct {
		name string
		slot string
	}{
		{"proton", "Wireguard2"},
		{"warp", "Wireguard3"},
		{"other", "Wireguard4"},
	} {
		if _, err := st.CreatePool(p.name, store.PoolSettings{
			Platform: store.PlatformKeenetic, KeeneticSlot: p.slot,
			Fallback: store.FallbackDirect, CheckIntervalSec: 30, FailThreshold: 2, CooldownMin: 5,
		}); err != nil {
			t.Fatal(err)
		}
	}
	ncs := []wgconf.NamedConfig{
		{OriginalName: "p1.conf", Raw: ipConf(keyA, "203.0.113.10:51820"), Config: mustParse(t, ipConf(keyA, "203.0.113.10:51820"))},
		{OriginalName: "p2.conf", Raw: ipConf(keyB, "203.0.113.11:51820"), Config: mustParse(t, ipConf(keyB, "203.0.113.11:51820"))},
	}
	if _, _, err := st.AddConfigs("proton", ncs); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.AddConfigs("warp", []wgconf.NamedConfig{
		{OriginalName: "w1.conf", Raw: ipConf(keyC, "198.51.100.10:2408"), Config: mustParse(t, ipConf(keyC, "198.51.100.10:2408"))},
	}); err != nil {
		t.Fatal(err)
	}
	if viaMode != "" {
		via := []string{"warp"}
		if viaMode == "two" {
			via = []string{"other", "warp"}
		}
		p, _ := st.Pool("proton")
		settings := p.Settings
		settings.SmartCascade = &store.SmartCascadeSettings{
			Via:         via,
			Return:      store.SmartCascadeReturn{Mode: "interval", IntervalH: 6},
			MinDwellMin: 10,
		}
		if err := st.UpdatePool("proton", settings); err != nil {
			t.Fatal(err)
		}
	}
	fb := fake.New()
	fb.SetProbe("proton", true)
	fb.SetProbe("warp", true)
	fb.SetProbe("other", true)
	cl := &clock{t: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	mt := newMTFake(t)
	e := New(st, fb, magitrickle.New(mt.baseURL))
	e.Clock = cl.Now
	e.smartPause = time.Millisecond
	ipt := &fakeSmartIpt{chains: map[string]bool{}}
	if e.Cascade() != nil {
		e.Cascade().SetIptRun(ipt.run)
	}
	mt.onGroup = func(id string) { ipt.chains["MT_"+id] = true }
	return &smartPark{st: st, fb: fb, cl: cl, e: e, mt: mt, ipt: ipt}
}

// exhaust - честное доведение пула до исчерпания конфигов: отказы ->
// ротация на второй конфиг (гейт minApplyInterval 20с) -> отказы ->
// rotate видит все конфиги в cooldown. probeHeals - как только каскад
// создан, туннель «оживает через Via» (переворачивает пробу).
func (park *smartPark) exhaust(t *testing.T, probeHeals bool) {
	t.Helper()
	park.e.checkPool("proton") // конфиг1 применён (грейс 15с)
	park.fb.SetProbe("proton", false)
	park.cl.Add(20 * time.Second) // грейс истёк, applyAllowed открыт
	park.e.checkPool("proton")    // отказ 1
	park.e.checkPool("proton")    // отказ 2 -> rotate -> конфиг2 применён
	park.cl.Add(20 * time.Second)
	if probeHeals {
		// каскад создастся внутри следующего checkPool - с этого момента
		// туннель proton жив через Via (как в жизни: handshake через warp)
		park.mt.onGroup = func(id string) {
			park.ipt.chains["MT_"+id] = true
			park.fb.SetProbe("proton", true)
		}
	}
	park.e.checkPool("proton") // отказ 1 конфига2
	park.e.checkPool("proton") // отказ 2 -> оба конфига в cooldown -> rescue
}

// блокировка всех конфигов proton -> исчерпание -> каскад включён,
// пул остаётся ModeUp со своим IP; хук стоит
func TestSmartCascadeRescueOnExhaustion(t *testing.T) {
	park := smartSetup(t, "one")
	park.e.checkPool("warp")
	park.exhaust(t, true)

	st := park.st.State("proton")
	if st.Smart == nil || !st.Smart.On || st.Smart.Via != "warp" {
		t.Fatalf("smart-стейт: %+v", st.Smart)
	}
	if st.Mode != store.ModeUp {
		t.Fatalf("пул ушёл в fallback (%s), каскад обязан держать ModeUp", st.Mode)
	}
	if len(park.st.Cascades()) != 1 {
		t.Fatalf("записей каскада %d, хочу 1", len(park.st.Cascades()))
	}
	entry := park.st.Cascades()[0]
	if entry.Source != "pool:proton" || entry.Via != "pool:warp" {
		t.Fatalf("каскад: %+v", entry)
	}
	if !park.ipt.has("MT_" + entry.Group) {
		t.Fatal("OUTPUT-хук каскада не стоит")
	}
	// группа каскада включена в MT
	for _, g := range park.mt.snapshot() {
		if g.ID == entry.Group && !g.Enable {
			t.Fatal("группа каскада выключена в MT")
		}
	}
	// расписание возврата назначено
	if st.Smart.NextCheck.IsZero() {
		t.Fatal("NextCheck не назначен (interval 6ч)")
	}
}

// лежачий первый Via -> перебор на второй живой
func TestSmartCascadeViaFailover(t *testing.T) {
	park := smartSetup(t, "two") // Via: [other, warp]
	// other применён и жив, warp лежит (fallback)
	park.e.checkPool("other")
	park.fb.SetProbe("warp", false)
	park.st.MutateState("warp", func(s *store.PoolState) { s.Mode = store.ModeFallback })

	park.exhaust(t, true)

	st := park.st.State("proton")
	if st.Smart == nil || !st.Smart.On || st.Smart.Via != "other" {
		t.Fatalf("ожидался каскад через other (warp лежит): %+v", st.Smart)
	}
}

// все Via лежат -> каскад не включается, обычный fallback
func TestSmartCascadeAllViaDown(t *testing.T) {
	park := smartSetup(t, "one")
	park.st.MutateState("warp", func(s *store.PoolState) { s.Mode = store.ModeFallback })

	park.exhaust(t, false)

	st := park.st.State("proton")
	if st.Smart != nil && st.Smart.On {
		t.Fatal("каскад включился через лежачий Via")
	}
	if st.Mode != store.ModeFallback {
		t.Fatalf("ожидался обычный fallback, mode=%s", st.Mode)
	}
}

// возврат по интервалу: расписание истекло, прямой жив -> каскад снят,
// хук ушёл; раньше MinDwell - не дёргается
func TestSmartCascadeReturnByInterval(t *testing.T) {
	park := smartSetup(t, "one")
	park.e.checkPool("warp")
	park.exhaust(t, true)
	entry := park.st.Cascades()[0]

	// раньше dwell (10 мин) - домашка не трогает
	park.e.checkSmartCascades()
	if !park.st.State("proton").Smart.On {
		t.Fatal("антифлап не удержал каскад до MinDwell")
	}

	// dwell истёк, но NextCheck через 6ч - рано; двигаем часы туда
	park.cl.Add(11 * time.Minute)
	park.e.checkSmartCascades()
	if !park.st.State("proton").Smart.On {
		t.Fatal("NextCheck ещё не наступил - каскад снят преждевременно")
	}
	park.cl.Add(6 * time.Hour)
	park.fb.SetProbe("proton", true) // прямой жив
	park.e.checkSmartCascades()

	st := park.st.State("proton")
	if st.Smart.On {
		t.Fatal("каскад не снят при живом прямом пути")
	}
	if park.ipt.has("MT_" + entry.Group) {
		t.Fatal("хук каскада не снят")
	}
	if st.Smart.NextCheck.IsZero() {
		t.Fatal("следующая проверка не назначена")
	}
}

// прямой ещё мёртв - каскад остаётся, хук возвращается после окна
func TestSmartCascadeReturnStillBlocked(t *testing.T) {
	park := smartSetup(t, "one")
	park.e.checkPool("warp")
	park.exhaust(t, true)
	entry := park.st.Cascades()[0]

	park.cl.Add(7 * time.Hour) // dwell (10м) и NextCheck (6ч) истекли
	park.fb.SetProbe("proton", false) // прямой всё ещё заблокирован
	park.e.checkSmartCascades()

	st := park.st.State("proton")
	if !st.Smart.On {
		t.Fatal("каскад снят при мёртвом прямом пути")
	}
	if !park.ipt.has("MT_" + entry.Group) {
		t.Fatal("хук не возвращен после окна пробы")
	}
}

// ручная «проверить прямой сейчас» минует dwell
func TestSmartCascadeCheckNowBypassesDwell(t *testing.T) {
	park := smartSetup(t, "one")
	park.e.checkPool("warp")
	park.exhaust(t, true)

	// сразу, без 10 минут dwell; прямой жив (проба уже true после exhaust)
	if err := park.e.SmartCascadeCheckNow("proton"); err != nil {
		t.Fatal(err)
	}
	if park.st.State("proton").Smart.On {
		t.Fatal("ручная проверка не сняла каскад при живом прямом")
	}
}

// ensure: сохранение настроек создаёт служебную группу выключенной
func TestSmartCascadeEnsureCreatesDisabled(t *testing.T) {
	park := smartSetup(t, "one")
	park.e.EnsureSmartCascade("proton")

	casc := park.st.Cascades()
	if len(casc) != 1 || casc[0].Source != "pool:proton" || casc[0].Via != "pool:warp" {
		t.Fatalf("каскады: %+v", casc)
	}
	st := park.st.State("proton")
	if st.Smart == nil || st.Smart.On {
		t.Fatalf("автомат не должен быть включён: %+v", st.Smart)
	}
	for _, g := range park.mt.snapshot() {
		if g.ID == casc[0].Group && g.Enable {
			t.Fatal("служебная группа создана включённой - обязана ждать триггера")
		}
	}
	// повторный ensure идемпотентен
	park.e.EnsureSmartCascade("proton")
	if len(park.st.Cascades()) != 1 {
		t.Fatal("ensure плодит каскады")
	}
}

// расписание: возврат в 03:00 - следующее наступающее время
func TestSmartCascadeScheduleNextTime(t *testing.T) {
	park := smartSetup(t, "one")
	p, _ := park.st.Pool("proton")
	settings := p.Settings
	settings.SmartCascade.Return = store.SmartCascadeReturn{Mode: "schedule", TimeOfDay: []string{"03:00", "15:30"}}
	if err := park.st.UpdatePool("proton", settings); err != nil {
		t.Fatal(err)
	}
	// часы парка: 2026-10-01 12:00 -> следующий 15:30
	next := nextTimeOfDay(park.e.now(), []string{"03:00", "15:30"})
	if next.Hour() != 15 || next.Minute() != 30 || next.Day() != 1 {
		t.Fatalf("next = %v, хочу 2026-10-01 15:30", next)
	}
	after := next.Add(time.Minute)
	if n2 := nextTimeOfDay(after, []string{"03:00", "15:30"}); n2.Hour() != 3 || n2.Day() != 2 {
		t.Fatalf("после 15:31 следующий - 03:00 завтра, got %v", n2)
	}
}

func (s *mtState) snapshot() []magitrickle.Group {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]magitrickle.Group(nil), s.groups...)
}
