package rotator

// Подтверждение туннеля после применения: до ответа пробы пул не получает
// чужой трафик - ни свои группы, ни группы политик. Иначе нерабочий туннель
// после каждого кулдауна ненадолго забирает их тем же циклом, что и набор.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"mawg/internal/magitrickle"
	"mawg/internal/platform"
	"mawg/internal/store"
)

// groupsMT - фейковый magitrickle: один список групп на чтение и запись
type groupsMT struct {
	mu     sync.Mutex
	groups []magitrickle.Group
}

func newGroupsMT(t *testing.T, groups ...magitrickle.Group) (*groupsMT, string) {
	t.Helper()
	g := &groupsMT{groups: groups}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/groups":
			json.NewEncoder(w).Encode(map[string]any{"groups": g.groups})
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/groups":
			var req struct {
				Groups []magitrickle.Group `json:"groups"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			g.groups = req.Groups
			w.WriteHeader(200)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return g, srv.URL
}

func (g *groupsMT) group(id string) magitrickle.Group {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, gr := range g.groups {
		if gr.ID == id {
			return gr
		}
	}
	return magitrickle.Group{}
}

func TestPoolGroupsReturnOnlyAfterConfirmation(t *testing.T) {
	mt, url := newGroupsMT(t, magitrickle.Group{ID: "g1", Name: "test", Interface: "nwg2", Enable: true})
	st, fb, cl := setup(t, store.FallbackDirect)
	e := newEngine(st.Store, fb, cl, url)
	check := func() {
		cl.Add(20 * time.Second)
		e.checkPool("proton")
	}

	e.checkPool("proton")
	fb.SetProbe("proton", false)
	for i := 0; i < 3; i++ {
		check()
		check()
	}
	if st.State("proton").Mode != store.ModeFallback || mt.group("g1").Enable {
		t.Fatalf("фоллбэк: mode=%q группа=%+v", st.State("proton").Mode, mt.group("g1"))
	}

	cl.Add(6 * time.Minute)
	e.checkPool("proton")
	if st := st.State("proton"); st.Mode != store.ModeUp || !st.RestorePending {
		t.Fatalf("восстановление: mode=%q pending=%v", st.Mode, st.RestorePending)
	}

	// рукопожатие при включении прошло, ответа на пробу нет
	fb.ProbeOK["proton"] = false
	fb.StatusMap["proton"] = platform.TunnelStatus{LinkUp: true, Connected: true, HandshakeAgo: 30}
	check()
	check()
	if mt.group("g1").Enable {
		t.Fatal("рукопожатие без ответа включило группу на мёртвый туннель")
	}

	fb.SetProbe("proton", true)
	check()
	if !mt.group("g1").Enable {
		t.Fatal("ответ пробы не вернул группу на пул")
	}
	if st.State("proton").RestorePending {
		t.Fatal("возврат групп выполнен, флаг не снят")
	}
}

// группа с политикой «при отвале - на запасной интерфейс» возвращается на
// пул после подтверждения, а не по статусу «включён»
func TestPolicyRestoreWaitsForPoolConfirmation(t *testing.T) {
	mt, url := newGroupsMT(t, magitrickle.Group{ID: "g1", Name: "test", Interface: "nwg2", Enable: true})
	st, fb, cl := setup(t, store.FallbackDirect)
	pol := store.GroupPolicy{OnDead: store.PolicyIface, Iface: "tun9"}
	if err := st.SetGroupPolicy("g1", &pol); err != nil {
		t.Fatal(err)
	}
	e := newEngine(st.Store, fb, cl, url)
	check := func() {
		cl.Add(20 * time.Second)
		e.checkPool("proton")
	}

	e.checkPool("proton")
	check()
	fb.SetProbe("proton", false)
	for i := 0; i < 3; i++ {
		check()
		check()
	}
	if got := mt.group("g1").Interface; got != "tun9" {
		t.Fatalf("отвал: группа на %q, хочу tun9", got)
	}

	cl.Add(6 * time.Minute)
	e.checkPool("proton")
	e.checkGroupPolicies()
	e.checkGroupPolicies()
	if got := mt.group("g1").Interface; got != "tun9" {
		t.Fatalf("неподтверждённый пул вернул на себя группу политик: %q", got)
	}

	fb.SetProbe("proton", true)
	check()
	if got := mt.group("g1").Interface; got != "nwg2" {
		t.Fatalf("после подтверждения группа на %q, хочу nwg2", got)
	}
}

// первая проверка после применения - сразу по окончании settle, а не
// когда выпадет по интервалу: до неё пул не подтверждён
func TestFirstCheckAfterApplyAtSettleEnd(t *testing.T) {
	st, fb, cl := setup(t, store.FallbackDirect)
	if err := st.UpdatePool("proton", store.PoolSettings{
		Platform: store.PlatformKeenetic, KeeneticSlot: "Wireguard2",
		Fallback: store.FallbackDirect, CheckIntervalSec: 30, FailThreshold: 2, CooldownMin: 5,
	}); err != nil {
		t.Fatal(err)
	}
	e := newEngine(st.Store, fb, cl, "")
	e.checkPool("proton")
	t0 := cl.t
	cl.Add(time.Second)
	e.checkPool("proton") // тик в период settle - только фиксирует LastCheck
	if got := e.firstDue(t0.Add(14 * time.Second)); got != "" {
		t.Fatalf("в период settle пул не должен быть due: %q", got)
	}
	if got := e.firstDue(t0.Add(settleSeconds * time.Second)); got != "proton" {
		t.Fatalf("по окончании settle пул должен быть due: %q", got)
	}
}

// после рестарта mawg пул с рукопожатием без ответа подтверждается первой
// проверкой: туннель давний, а не только что включённый
func TestRestartDoesNotStrandLossyPool(t *testing.T) {
	st, fb, cl := setup(t, store.FallbackDirect)
	e := newEngine(st.Store, fb, cl, "")
	e.checkPool("proton")
	cl.Add(20 * time.Second)
	e.checkPool("proton")

	fb.ProbeOK["proton"] = false
	fb.StatusMap["proton"] = platform.TunnelStatus{LinkUp: true, Connected: true, HandshakeAgo: 40}
	st.MutateState("proton", func(s *store.PoolState) {
		s.AppliedAt = time.Time{}
		s.Confirmed = false
	})
	restarted := newEngine(st.Store, fb, cl, "")
	cl.Add(20 * time.Second)
	restarted.checkPool("proton")
	if !st.State("proton").Confirmed {
		t.Fatalf("после рестарта lossy-пул не подтверждён: %+v", st.State("proton").LastResult)
	}
}
