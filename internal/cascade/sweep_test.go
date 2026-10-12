package cascade

// предзаписная зачистка хуков (кейс владельца 2026-10-12, awgfree): форк
// при bulk-PUT пересобирает цепочки не только «обречённых» групп, и любой
// висящий прыжок из OUTPUT ронял весь restore-батч - CHAIN_DEL busy, 500,
// рестарт MT, рантайм пустел. Контракт: BeforeMutate/BeforeReplace снимают
// ВСЕ хуки и верифицируют снятие чтением, SyncHooks молчит в окне мутации.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"mawg/internal/magitrickle"
	"mawg/internal/store"
)

// fakeIpt - состояние mangle OUTPUT и существования цепочек MT_*.
type fakeIpt struct {
	mu        sync.Mutex
	output    []string          // прыжки -j MT_x из OUTPUT
	chains    map[string]bool   // существующие цепочки
	breakDel  int               // сколько первых -D вернуть ошибку (упрямый хук)
	deletions []string          // лог снятых цепочек
	adds      []string          // лог поставленных прыжков
}

func (f *fakeIpt) run(args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	join := strings.Join(args, " ")
	switch {
	case join == "-t mangle -S OUTPUT":
		out := ""
		for _, c := range f.output {
			out += "-A OUTPUT -j " + c + "\n"
		}
		return out, nil
	case len(args) == 6 && join == "-t mangle -D OUTPUT -j "+args[5]:
		chain := args[5]
		if f.breakDel > 0 {
			f.breakDel--
			return "", errors.New("iptables: Resource busy")
		}
		f.deletions = append(f.deletions, chain)
		for i, c := range f.output {
			if c == chain {
				f.output = append(f.output[:i], f.output[i+1:]...)
				break
			}
		}
		return "", nil
	case len(args) == 6 && join == "-t mangle -A OUTPUT -j "+args[5]:
		chain := args[5]
		for _, c := range f.output {
			if c == chain {
				return "", nil // уже стоит (HookAdd сам не дублирует, но всё равно)
			}
		}
		f.output = append(f.output, chain)
		f.adds = append(f.adds, chain)
		return "", nil
	case len(args) == 5 && args[0] == "-t" && args[1] == "mangle" && args[2] == "-L":
		if f.chains[args[3]] {
			return "Chain " + args[3], nil
		}
		return "not found", fmt.Errorf("no chain named %s", args[3])
	case len(args) >= 4 && args[0] == "-t" && args[1] == "mangle" && args[2] == "-D":
		// -D <chain> ... (RETURN-исключения эндпоинтов) - просто ОК
		return "", nil
	}
	return "", nil
}

func (f *fakeIpt) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.output...)
}

// mockMT отдаёт фиксированный список групп - SyncHooks/GroupsWithRules.
func mockMT(t *testing.T, groups []magitrickle.Group) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/groups":
			json.NewEncoder(w).Encode(map[string]any{"groups": groups})
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/groups":
			w.WriteHeader(200)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func sweepFixture(t *testing.T) (*Manager, *fakeIpt) {
	t.Helper()
	magitrickle.SetShadowStorage(nil, nil)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetCascades([]store.CascadeEntry{{Group: "g1", Name: "каскад", Source: "pool:a", Via: "pool:b"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetGroupPolicy("g2", &store.GroupPolicy{SourcesMode: "all+router"}); err != nil {
		t.Fatal(err)
	}
	f := &fakeIpt{chains: map[string]bool{"MT_g1": true, "MT_g2": true, "MT_stale": true}}
	m := New(st, magitrickle.New(mockMT(t, []magitrickle.Group{
		{ID: "g1", Name: "каскад", Interface: "warp", Enable: true},
		{ID: "g2", Name: "роутерная", Interface: "awg0", Enable: true},
	}).URL))
	m.iptRun = f.run
	return m, f
}

// BeforeMutate снимает ВСЕ хуки (каскад + роутерный) и заодно протухший
// прыжок группы, которой в сторе уже нет; окно мутации открывается и
// AfterMutate его закрывает, вернув хуки живым группам.
func TestBeforeMutateSweepsAllHooks(t *testing.T) {
	m, f := sweepFixture(t)
	f.output = []string{"MT_g1", "MT_g2", "MT_stale"}

	m.BeforeMutate(func([]magitrickle.Group) bool { return false })
	if got := f.snapshot(); len(got) != 0 {
		t.Fatalf("после BeforeMutate остались прыжки: %v", got)
	}
	if m.mutating != 1 {
		t.Fatalf("окно мутации не открыто: %d", m.mutating)
	}

	m.AfterMutate()
	if m.mutating != 0 {
		t.Fatalf("окно мутации не закрыто: %d", m.mutating)
	}
	got := f.snapshot()
	// SyncHooks вернул хуки живым группам; протухший MT_stale не возвращается
	if len(got) != 2 || !contains(got, "MT_g1") || !contains(got, "MT_g2") {
		t.Fatalf("после AfterMutate хуки: %v, want g1+g2", got)
	}
}

// упрямый хук: -DRaw врёт «снял», но правило висит - верификация читает
// OUTPUT и дожимает; полный отказ - список stuck (запись пойдёт, самолечение
// перезапустит MT).
func TestSweepVerifiesAndReportsStuck(t *testing.T) {
	m, f := sweepFixture(t)
	f.output = []string{"MT_g1", "MT_g2", "MT_stale"}
	f.breakDel = 2 // первый -D каждого... один -D ломаем, второй проходит

	stuck := m.sweepOutputHooks()
	if got := f.snapshot(); len(got) != 0 {
		t.Fatalf("после ретраев остались прыжки: %v", got)
	}
	// g1 дожат ретраем после первой ошибки -D, g2 и stale сняты с первого раза
	if len(f.deletions) < 3 {
		t.Fatalf("верификация не дожала: deletions=%v", f.deletions)
	}
	_ = stuck
}

func TestSweepStuckReported(t *testing.T) {
	m, f := sweepFixture(t)
	f.output = []string{"MT_g1"}
	f.breakDel = 100 // -D всегда возвращает ошибку
	stuck := m.sweepOutputHooks()
	if len(stuck) == 0 || !contains(stuck, "MT_g1") {
		t.Fatalf("упрямый хук не попал в stuck: %v", stuck)
	}
	if got := f.snapshot(); len(got) != 1 {
		t.Fatalf("хук должен был остаться: %v", got)
	}
}

// SyncHooks в окне мутации молчит: 30-секундный тик не должен вернуть хук
// между снятием и записью (гонка кейса awgfree).
func TestSyncHooksSilentDuringMutate(t *testing.T) {
	m, f := sweepFixture(t)
	f.output = []string{"MT_g1"}

	m.beginMutate()
	n := len(f.adds) + len(f.deletions)
	m.SyncHooks()
	m.SyncHooks()
	if got := len(f.adds) + len(f.deletions); got != n {
		t.Fatalf("SyncHooks трогал хуки в окне мутации (%d операций)", got-n)
	}
	if got := f.snapshot(); len(got) != 1 {
		t.Fatalf("SyncHooks изменил OUTPUT в окне: %v", got)
	}
	m.endMutate() // вернёт хук g1
	if got := f.snapshot(); !contains(got, "MT_g1") {
		t.Fatalf("после окна хук не вернулся: %v", got)
	}
}

// sources-правка живой группы с её же роутерным хуком идёт в защитном
// окне: хук снят на время записи и возвращён после.
func TestSyncGroupSourcesSweepsHook(t *testing.T) {
	m, f := sweepFixture(t)
	f.output = []string{"MT_g2"} // роутерный хук группы, чьи sources меняем

	if err := m.syncGroupSources("g2", []string{"10.0.0.9"}); err != nil {
		t.Fatalf("syncGroupSources: %v", err)
	}
	if len(f.deletions) == 0 {
		t.Fatal("хук не снимался перед записью sources")
	}
	if got := f.snapshot(); !contains(got, "MT_g2") {
		t.Fatalf("хук не вернулся после записи: %v", got)
	}
	if m.mutating != 0 {
		t.Fatalf("окно мутации не закрыто: %d", m.mutating)
	}
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
