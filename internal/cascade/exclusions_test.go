package cascade

// Исключения из обработки (94-док): RETURN-правила в цепочках MT_<id>,
// синк идемпотентен, сокращение списка удаляет лишнее, несуществующие
// цепочки пропускаются, пуловые списки объединяются с глобальным.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"mawg/internal/magitrickle"
	"mawg/internal/store"
)

// exclIpt - состояние правил цепочек mangle: -S/-I/-D.
type exclIpt struct {
	mu     sync.Mutex
	chains map[string][]string // цепочка -> спеки правил (без "-A <chain>")
}

func (f *exclIpt) run(args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(args) < 3 || args[0] != "-t" || args[1] != "mangle" {
		return "", nil
	}
	chain := args[3]
	switch args[2] {
	case "-S":
		rules, ok := f.chains[chain]
		if !ok {
			return "", fmt.Errorf("no chain named %s", chain)
		}
		out := ""
		for _, r := range rules {
			out += "-A " + chain + " " + r + "\n"
		}
		return out, nil
	case "-I":
		spec := args[4:] // позиция "1" отбрасывается
		if len(spec) > 0 && spec[0] == "1" {
			spec = spec[1:]
		}
		f.chains[chain] = append([]string{strings.Join(spec, " ")}, f.chains[chain]...)
		return "", nil
	case "-D":
		spec := strings.Join(args[4:], " ")
		rules := f.chains[chain]
		for i, r := range rules {
			if r == spec {
				f.chains[chain] = append(rules[:i], rules[i+1:]...)
				return "", nil
			}
		}
		return "", fmt.Errorf("no such rule in %s", chain)
	}
	return "", nil
}

func (f *exclIpt) tagged(chain string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, r := range f.chains[chain] {
		if strings.Contains(r, "--comment "+exclComment) {
			out = append(out, r)
		}
	}
	return out
}

func exclFixture(t *testing.T, ipt *exclIpt, groups []magitrickle.Group) *Manager {
	t.Helper()
	magitrickle.SetShadowStorage(nil, nil)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/groups" {
			json.NewEncoder(w).Encode(map[string]any{"groups": groups})
			return
		}
		w.WriteHeader(404)
	}))
	t.Cleanup(srv.Close)
	m := New(st, magitrickle.New(srv.URL))
	m.iptRun = ipt.run
	return m
}

func TestSyncExclusions(t *testing.T) {
	ipt := &exclIpt{chains: map[string][]string{
		// форкownские правила уже есть - синк их не трогает
		"MT_g1": {"-m conntrack --ctdir REPLY -j RETURN"},
		"MT_g2": {},
	}}
	// g1: Interface warp (пул warp с пуловым исключением), g2 - нет пула
	m := exclFixture(t, ipt, []magitrickle.Group{
		{ID: "g1", Name: "warp", Interface: "warp", Enable: true},
		{ID: "g2", Name: "другая", Interface: "awg0", Enable: true},
	})
	if err := m.Store.SetExcludes([]string{"192.0.2.1/32", "198.51.100.0/24"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Store.CreatePool("warp", store.PoolSettings{
		EngineMode: "singbox", TunName: "warp", Excludes: []string{"203.0.113.5"},
	}); err != nil {
		t.Fatal(err)
	}

	m.SyncExclusions()

	// g1: 2 глобальных + 1 пуловой, оба направления; g2: только глобальные
	got := ipt.tagged("MT_g1")
	if len(got) != 6 {
		t.Fatalf("MT_g1: ждали 6 помеченных правил, есть %v", got)
	}
	got = ipt.tagged("MT_g2")
	if len(got) != 4 {
		t.Fatalf("MT_g2: ждали 4 помеченных правила, есть %v", got)
	}
	if ipt.chains["MT_g1"][len(ipt.chains["MT_g1"])-1] != "-m conntrack --ctdir REPLY -j RETURN" {
		t.Fatalf("чужие правила сдвинуты: %v", ipt.chains["MT_g1"])
	}

	// идемпотентность: повторный синк ничего не меняет
	before := len(ipt.chains["MT_g1"])
	m.SyncExclusions()
	if len(ipt.chains["MT_g1"]) != before {
		t.Fatalf("повторный синк задублировал правила: %v", ipt.chains["MT_g1"])
	}

	// сокращение списка: лишнее удаляется, нужное живо
	if err := m.Store.SetExcludes([]string{"192.0.2.1"}); err != nil {
		t.Fatal(err)
	}
	m.SyncExclusions()
	for _, r := range ipt.tagged("MT_g1") {
		if strings.Contains(r, "198.51.100.0/24") {
			t.Fatalf("сокращённая запись осталась: %v", ipt.tagged("MT_g1"))
		}
	}
	if n := len(ipt.tagged("MT_g1")); n != 4 { // 1 глобальный×2 + 1 пуловый×2
		t.Fatalf("после сокращения ждали 4 правила, есть %d: %v", n, ipt.tagged("MT_g1"))
	}

	// пустые списки: помеченные правила вычищаются
	if err := m.Store.SetExcludes(nil); err != nil {
		t.Fatal(err)
	}
	if err := m.Store.UpdatePool("warp", store.PoolSettings{
		EngineMode: "singbox", TunName: "warp",
	}); err != nil {
		t.Fatal(err)
	}
	m.SyncExclusions()
	if got := ipt.tagged("MT_g1"); len(got) != 0 {
		t.Fatalf("пустые списки не вычистили правила: %v", got)
	}
}

// SyncHooks не должен терять исключения на раннем выходе «нет каскадов»:
// роутер без единого каскада тоже исключает адреса.
func TestSyncHooksCoversExclusionsWithoutCascades(t *testing.T) {
	ipt := &exclIpt{chains: map[string][]string{"MT_g1": {}}}
	m := exclFixture(t, ipt, []magitrickle.Group{{ID: "g1", Name: "nwg1", Interface: "nwg1", Enable: true}})
	if err := m.Store.SetExcludes([]string{"192.0.2.9/32"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Store.CreatePool("nwg1", store.PoolSettings{
		Platform: store.PlatformOpenwrt, Excludes: []string{"198.51.100.7"},
	}); err != nil {
		t.Fatal(err)
	}
	m.SyncHooks()
	got := ipt.tagged("MT_g1")
	if len(got) != 4 {
		t.Fatalf("SyncHooks не применил исключения: %v", got)
	}
	joined := strings.Join(got, "; ")
	for _, want := range []string{"-s 192.0.2.9/32", "-d 192.0.2.9/32", "-s 198.51.100.7/32", "-d 198.51.100.7/32"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("нет правила %s: %v", want, got)
		}
	}
}

// Флаг выключенного функционала (95-док, слово владельца 30.10):
// excludesPresent()=false при непустых списках - SyncHooks не вставляет
// mawg-excl, SyncExclusions спит; после снятия флага всё возвращается.
func TestExcludesDisabledSleeps(t *testing.T) {
	ipt := &exclIpt{chains: map[string][]string{"MT_g1": {}}}
	m := exclFixture(t, ipt, []magitrickle.Group{{ID: "g1", Name: "nwg1", Interface: "nwg1", Enable: true}})
	if err := m.Store.SetExcludes([]string{"192.0.2.9/32"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Store.CreatePool("nwg1", store.PoolSettings{
		Platform: store.PlatformOpenwrt, Excludes: []string{"198.51.100.7"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.Store.SetExcludesDisabled(true); err != nil {
		t.Fatal(err)
	}

	if m.excludesPresent() {
		t.Fatal("при флаге excludesPresent()=true")
	}
	m.SyncHooks()
	m.SyncExclusions()
	if got := ipt.tagged("MT_g1"); len(got) != 0 {
		t.Fatalf("спящая фича вставила правила: %v", got)
	}

	// снятие флага - SyncHooks снова покрывает исключения
	if err := m.Store.SetExcludesDisabled(false); err != nil {
		t.Fatal(err)
	}
	m.SyncHooks()
	if got := ipt.tagged("MT_g1"); len(got) != 4 {
		t.Fatalf("после включения фичи исключения не вернулись: %v", got)
	}
}

// Нет цепочки (группа без правил) - тихий skip, без паник и ошибок.
func TestSyncExclusionsSkipsMissingChain(t *testing.T) {
	ipt := &exclIpt{chains: map[string][]string{}}
	m := exclFixture(t, ipt, []magitrickle.Group{{ID: "gone", Name: "x", Interface: "x", Enable: true}})
	if err := m.Store.SetExcludes([]string{"192.0.2.1"}); err != nil {
		t.Fatal(err)
	}
	m.SyncExclusions() // не должно паниковать
	if got := ipt.tagged("MT_gone"); len(got) != 0 {
		t.Fatalf("несуществующая цепочка получила правила: %v", got)
	}
}
