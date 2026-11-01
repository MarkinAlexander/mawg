package cascade

// Гейт flavor-aware (107-док): при бэкенде mt-c (FlavorMtC) каскад не
// пишет ни одной записи в чужие iptables-цепочки - ни OUTPUT-прыжков,
// ни свипа, ни mawg-excl; одна честная строка в журнал событий при
// переходе flavor туда и обратно.

import (
	"strings"
	"sync"
	"testing"

	"mawg/internal/magitrickle"
)

// writeCountIpt - обёртка над fake-iptables, считающая записи (-A/-I/-D).
type writeCountIpt struct {
	inner  func(args ...string) (string, error)
	mu     sync.Mutex
	writes int
}

func (f *writeCountIpt) run(args ...string) (string, error) {
	f.mu.Lock()
	if len(args) >= 3 {
		switch args[2] {
		case "-A", "-I", "-D", "-N", "-X", "-F", "-R":
			f.writes++
		}
	}
	f.mu.Unlock()
	return f.inner(args...)
}

func (f *writeCountIpt) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes
}

func mtcGateFixture(t *testing.T, ipt *exclIpt) (*Manager, *writeCountIpt) {
	t.Helper()
	m := exclFixture(t, ipt, []magitrickle.Group{{ID: "g1", Name: "nwg1", Interface: "nwg1", Enable: true}})
	wc := &writeCountIpt{inner: ipt.run}
	m.SetIptRun(wc.run)
	return m, wc
}

func TestMtcGateBlocksCascadeWrites(t *testing.T) {
	ipt := &exclIpt{chains: map[string][]string{"MT_g1": {}}}
	m, wc := mtcGateFixture(t, ipt)
	if err := m.Store.SetExcludes([]string{"192.0.2.9/32"}); err != nil {
		t.Fatal(err)
	}

	// до гейта: SyncHooks применяет исключения (записи идут)
	m.SyncHooks()
	if wc.count() == 0 {
		t.Fatal("SyncHooks без гейта не делал записей - счётчик не работает")
	}
	wc.mu.Lock()
	wc.writes = 0
	wc.mu.Unlock()
	ipt.mu.Lock()
	ipt.chains["MT_g1"] = nil // правила до-гейта в цепочках легитимны - чистим для фазы гейта
	ipt.mu.Unlock()

	// flavor mt-c: гейт блокирует всё
	m.SetBackendFlavor(magitrickle.FlavorMtC)
	m.SyncHooks()
	m.SyncExclusions()
	m.BeforeMutate(nil)
	m.AfterMutate()
	m.BeforeReplace(nil)
	m.AfterMutate()
	if n := wc.count(); n != 0 {
		t.Fatalf("гейт mt-c пропустил %d записей в чужие цепочки", n)
	}
	if got := ipt.tagged("MT_g1"); len(got) != 0 {
		t.Fatalf("mawg-excl правила просочились при mt-c: %v", got)
	}

	// честные события: одна строка на переход в mt-c (первые SyncHooks
	// до гейта событий mtc не пишут)
	mtcEvents := 0
	for _, ev := range m.Store.Events() {
		if ev.Pool == "cascade" && ev.Kind == "mtc-backend" {
			mtcEvents++
		}
	}
	if mtcEvents != 1 {
		t.Fatalf("ждали 1 событие mtc-backend, есть %d", mtcEvents)
	}

	// возврат на Go-форк: хуки/исключения снова пишутся
	m.SetBackendFlavor(magitrickle.FlavorGoFork)
	m.SyncHooks()
	if wc.count() == 0 {
		t.Fatal("после возврата на Go-форк SyncHooks не пишет исключения")
	}
	mtcEvents = 0
	for _, ev := range m.Store.Events() {
		if ev.Pool == "cascade" && ev.Kind == "mtc-backend" {
			mtcEvents++
		}
	}
	if mtcEvents != 2 {
		t.Fatalf("ждали 2 события mtc-backend (вход+выход), есть %d", mtcEvents)
	}
}

func TestMtcGateAllowsReads(t *testing.T) {
	ipt := &exclIpt{chains: map[string][]string{"MT_g1": {}}}
	m, wc := mtcGateFixture(t, ipt)
	m.SetBackendFlavor(magitrickle.FlavorMtC)
	// чтения живы: HookPresent/ChainExists ходят в -S, гейт их не глушит
	if m.HookPresent("g1") {
		t.Fatal("HookPresent на пустой цепочке должен быть false")
	}
	if wc.count() != 0 {
		t.Fatalf("чтения посчитались как записи: %d", wc.count())
	}
	if m.BackendFlavor() != magitrickle.FlavorMtC {
		t.Fatalf("BackendFlavor = %q", m.BackendFlavor())
	}
}

// FlavorOf-переход через rotator-пробу: строка версии mt-c даёт FlavorMtC.
func TestMtcFlavorFromRealVersionStrings(t *testing.T) {
	if got := magitrickle.FlavorOf("0.8.2-rev3-mawg-c-kn"); got != magitrickle.FlavorMtC {
		t.Fatalf("KN-версия mt-c не детектится: %q", got)
	}
	if !strings.HasPrefix("0.8.2-rev3-mawg-c-kn", "0.8.2") {
		t.Fatal("санити формата версии")
	}
}
