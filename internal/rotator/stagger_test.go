package rotator

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"mawg/internal/platform/fake"
	"mawg/internal/store"
)

// пустой пул: firstDue/tickProbes ходят, checkPool пишет LastCheck
// «конфигов для ротации нет» и не трогает туннели
func addEmptyPool(t *testing.T, st *store.Store, name, slot string) {
	t.Helper()
	_, err := st.CreatePool(name, store.PoolSettings{
		Platform:         store.PlatformKeenetic,
		KeeneticSlot:     slot,
		CheckIntervalSec: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
}

// только пустые пулы с равными интервалами - чистая математика разброса
func spreadEngine(t *testing.T, names ...string) (*store.Store, *Engine, *clock) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range names {
		addEmptyPool(t, st, name, "Wireguard"+strconv.Itoa(2+i))
	}
	cl := &clock{t: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	e := New(st, fake.New(), nil)
	e.Clock = cl.Now
	return st, e, cl
}

// стартовый разброс: никогда не проверявшиеся пулы назначаются на разные
// моменты (шаг CheckIntervalSec/N по sorted-именам), «стада» первого тика нет
func TestStartSpreadStaggersFirstChecks(t *testing.T) {
	st, e, cl := spreadEngine(t, "alpha", "bravo", "charlie")
	t0 := cl.t
	// 3 пула x 30с: шаг 10с; sorted: alpha t0, bravo t0+10, charlie t0+20
	if got := e.firstDue(t0); got != "alpha" {
		t.Fatalf("первый due = %q, хочу alpha (sorted первая)", got)
	}
	e.laneAdd("alpha", t0) // alpha «в полёте»
	if got := e.firstDue(t0); got != "" {
		t.Fatalf("t0: due = %q, хочу \"\" (разброс держит остальных)", got)
	}
	if got := e.firstDue(t0.Add(9 * time.Second)); got != "" {
		t.Fatalf("t0+9s: due = %q, хочу \"\"", got)
	}
	if got := e.firstDue(t0.Add(10 * time.Second)); got != "bravo" {
		t.Fatalf("t0+10s: due = %q, хочу bravo", got)
	}
	e.laneAdd("bravo", t0.Add(10 * time.Second))
	if got := e.firstDue(t0.Add(19 * time.Second)); got != "" {
		t.Fatalf("t0+19s: due = %q, хочу \"\"", got)
	}
	if got := e.firstDue(t0.Add(20 * time.Second)); got != "charlie" {
		t.Fatalf("t0+20s: due = %q, хочу charlie", got)
	}
	_ = st
}

// полоса: одна проба за раз; застрявшую дольше probeForceWait не ждём -
// стартуем вторую параллельно; максимум probeLaneMax в полёте
func TestProbeLaneOneAtATimeAndForce(t *testing.T) {
	st, fb, cl := setup(t, store.FallbackDirect)
	addEmptyPool(t, st.Store, "alpha", "Wireguard3")
	e := newEngine(st.Store, fb, cl, "")
	t0 := cl.t
	// actMu наш: горутина пробы виснет в очереди, полоса остаётся занятой
	func() {
		e.actMu.Lock()
		defer e.actMu.Unlock()
		e.tickProbes(t0)
		if got := e.laneCount(); got != 1 {
			t.Fatalf("после первого тика в полёте %d пробы, хочу 1", got)
		}
		if got := e.laneOldest(); got != t0 {
			t.Fatalf("laneOldest = %v, хочу %v", got, t0)
		}
		e.tickProbes(t0.Add(time.Second))
		if got := e.laneCount(); got != 1 {
			t.Fatalf("молодая проба не должна пускать вторую, в полёте %d", got)
		}
		e.tickProbes(t0.Add(14 * time.Second))
		if got := e.laneCount(); got != 1 {
			t.Fatalf("14с - ещё ждём, в полёте %d", got)
		}
		e.tickProbes(t0.Add(probeForceWait))
		if got := e.laneCount(); got != 2 {
			t.Fatalf("после форса в полёте %d, хочу 2", got)
		}
		e.tickProbes(t0.Add(probeForceWait + time.Second))
		if got := e.laneCount(); got != 2 {
			t.Fatalf("предел %d превышен: %d", probeLaneMax, got)
		}
	}()

	// отпускаем actMu - обе пробы доделываются, полоса опустевает
	deadline := time.Now().Add(2 * time.Second)
	for e.laneCount() > 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if e.laneCount() != 0 {
		t.Fatal("полоса не опустела после проб")
	}
	if st.Store.State("alpha").LastCheck.IsZero() {
		t.Fatalf("alpha не проверен: стейт %+v", st.Store.State("alpha"))
	}
	// proton с конфигами уходит в rotate: применяется первый конфиг и
	// ставится грейс; LastCheck допишет следующий тик (settling-ветка) -
	// здесь важно, что проба дошла до движка
	if st.Store.State("proton").ActiveFile == "" {
		t.Fatalf("proton не ротирован: стейт %+v", st.Store.State("proton"))
	}
}

// при молодой активной пробе других не стартует, при старой - следующий due
func TestProbeLaneHoldsWhileBusy(t *testing.T) {
	_, e, cl := spreadEngine(t, "alpha", "bravo")
	t0 := cl.t
	e.laneAdd("alpha", t0) // «активная» проба; база разброса - первый firstDue (t0+5с)
	if got := e.firstDue(t0.Add(5 * time.Second)); got != "" {
		t.Fatalf("молодая проба: firstDue = %q, хочу \"\"", got)
	}
	if got := e.firstDue(t0.Add(20 * time.Second)); got != "bravo" {
		t.Fatalf("старая проба: firstDue = %q, хочу bravo", got)
	}
}

// разнос домашки: чётные тики ходят в MT как monitor (без with_rules),
// нечётные - хуки/политики (with_rules)
func TestHouseworkAlternates(t *testing.T) {
	var mu sync.Mutex
	monitorReads, policyReads := 0, 0
	mt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/groups" {
			if r.URL.Query().Get("with_rules") == "true" {
				policyReads++
			} else {
				monitorReads++
			}
			w.Write([]byte(`{"groups":[]}`))
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer mt.Close()
	st, fb, cl := setup(t, store.FallbackDirect)
	e := newEngine(st.Store, fb, cl, mt.URL)
	e.mtEverAlive = true
	e.mtSyncAt = cl.t // минутную сверку групп внутри monitorMagitrickle выключаем

	e.tickN = 2
	e.runHousework() // чётный: бандлы (monitor) + внешние
	e.tickN = 3
	e.runHousework() // нечётный: хуки + политики

	mu.Lock()
	defer mu.Unlock()
	if monitorReads == 0 {
		t.Fatal("чётный тик не сходил в MT (monitorMagitrickle)")
	}
	if policyReads == 0 {
		t.Fatal("нечётный тик не сверил группы (SyncHooks/политики)")
	}
}

// RefreshDNS после разноса домашки обязан попадать только на нечётные
// (политические) тики и строго раз в 300с
func TestHouseworkRefreshDNSParity(t *testing.T) {
	var hits []int
	for n := 1; n <= 40; n++ {
		if !houseworkRefreshDNS(n) {
			continue
		}
		if houseworkBundles(n) {
			t.Fatalf("tickN=%d: RefreshDNS на бандловом тике", n)
		}
		if n%10 != 5 {
			t.Fatalf("tickN=%d: неожиданный тик RefreshDNS", n)
		}
		hits = append(hits, n)
	}
	if len(hits) != 4 || hits[0] != 5 {
		t.Fatalf("тики RefreshDNS = %v, хочу [5 15 25 35]", hits)
	}
}
