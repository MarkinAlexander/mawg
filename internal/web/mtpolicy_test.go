package web

// режимы «кто ходит в группу»: политика sources + OUTPUT-хук роутера.
// Эффективный список (юзер + адреса роутера) считает каскадный менеджер,
// адреса роутера в тестах подменяются.

import (
	"encoding/json"
	"testing"

	"mawg/internal/cascade"
	"mawg/internal/magitrickle"
	"mawg/internal/store"
)

func withRouterAddrs(t *testing.T, addrs []string) {
	t.Helper()
	old := cascade.RouterAddrs
	cascade.RouterAddrs = func() []string { return addrs }
	t.Cleanup(func() { cascade.RouterAddrs = old })
}

type groupViewTest struct {
	magitrickle.Group
	Policy *store.GroupPolicy `json:"policy"`
}

func getView(t *testing.T, base, id string) groupViewTest {
	t.Helper()
	code, raw := httpDo(t, base, "GET", "/api/v1/mt/groups", "")
	if code != 200 {
		t.Fatalf("get groups: %d", code)
	}
	var list struct {
		Groups []groupViewTest `json:"groups"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	for _, g := range list.Groups {
		if g.ID == id {
			return g
		}
	}
	t.Fatalf("group %s not found", id)
	return groupViewTest{}
}

func TestMTGroupSourcesPolicies(t *testing.T) {
	withRouterAddrs(t, []string{"192.168.0.62", "10.8.0.2"})
	ts, _, _ := newImportTestServer(t)

	code, raw := httpDo(t, ts.URL, "POST", "/api/v1/mt/groups", `{"name":"pol","interface":"awg0"}`)
	if code != 200 {
		t.Fatalf("create: %d %s", code, raw)
	}
	var created magitrickle.Group
	json.Unmarshal(raw, &created)

	// list + роутер: эффективный список = юзер + адреса роутера
	code, raw = httpDo(t, ts.URL, "PUT", "/api/v1/mt/groups/"+created.ID+"/policy",
		`{"sourcesMode":"list","sources":["10.0.0.5"],"sourcesRouter":true}`)
	if code != 200 {
		t.Fatalf("policy list+router: %d %s", code, raw)
	}
	g := getView(t, ts.URL, created.ID)
	// порядок: сначала список юзера, затем адреса роутера
	if len(g.Sources) != 3 || g.Sources[0] != "10.0.0.5" || g.Sources[1] != "192.168.0.62" || g.Sources[2] != "10.8.0.2" {
		t.Fatalf("effective sources: %v", g.Sources)
	}
	if g.Policy == nil || g.Policy.SourcesMode != "list" || !g.Policy.SourcesRouter {
		t.Fatalf("policy echo: %+v", g.Policy)
	}

	// all+router: guard не ставится (список пуст), хук - по политике
	code, _ = httpDo(t, ts.URL, "PUT", "/api/v1/mt/groups/"+created.ID+"/policy", `{"sourcesMode":"all+router"}`)
	if code != 200 {
		t.Fatalf("policy all+router: %d", code)
	}
	g = getView(t, ts.URL, created.ID)
	if len(g.Sources) != 0 {
		t.Fatalf("all+router must clear sources, got %v", g.Sources)
	}
	if g.Policy == nil || g.Policy.SourcesMode != "all+router" {
		t.Fatalf("policy echo: %+v", g.Policy)
	}

	// пустой список без роутера - отказ
	code, _ = httpDo(t, ts.URL, "PUT", "/api/v1/mt/groups/"+created.ID+"/policy", `{"sourcesMode":"list"}`)
	if code != 400 {
		t.Fatalf("empty list must be 400, got %d", code)
	}
	// кривой IP - отказ
	code, _ = httpDo(t, ts.URL, "PUT", "/api/v1/mt/groups/"+created.ID+"/policy", `{"sourcesMode":"list","sources":["junk"]}`)
	if code != 400 {
		t.Fatalf("junk source must be 400, got %d", code)
	}

	// all - политика источников сбрасывается (запись целиком уходит)
	code, _ = httpDo(t, ts.URL, "PUT", "/api/v1/mt/groups/"+created.ID+"/policy", `{"sourcesMode":"all"}`)
	if code != 200 {
		t.Fatalf("policy all: %d", code)
	}
	g = getView(t, ts.URL, created.ID)
	if g.Policy != nil && g.Policy.SourcesMode != "" {
		t.Fatalf("sourcesMode must be gone, got %+v", g.Policy)
	}

	// отвал + источники в одной политике живут вместе
	code, _ = httpDo(t, ts.URL, "PUT", "/api/v1/mt/groups/"+created.ID+"/policy",
		`{"onDead":"direct","sourcesMode":"list","sources":["10.0.0.9"]}`)
	if code != 200 {
		t.Fatalf("combined policy: %d", code)
	}
	g = getView(t, ts.URL, created.ID)
	if g.Policy == nil || g.Policy.OnDead != "direct" || g.Policy.SourcesMode != "list" {
		t.Fatalf("combined policy echo: %+v", g.Policy)
	}
}

// capability-детект sources: форк с /api/v1/version - флаг в payload и
// режимы принимаются; старый бинарь (404, в синтетике config.yaml нет -
// фолбэк тоже мимо) - флаг false, режимы отвергаются, «all» работает
func TestMTSourcesCapability(t *testing.T) {
	withRouterAddrs(t, []string{"192.168.0.62"})
	ts, mtState, _ := newImportTestServer(t)

	sourcesFlag := func() bool {
		t.Helper()
		code, raw := httpDo(t, ts.URL, "GET", "/api/v1/mt/groups", "")
		if code != 200 {
			t.Fatalf("groups: %d", code)
		}
		var out struct {
			Sources bool `json:"sources"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return out.Sources
	}

	if !sourcesFlag() {
		t.Fatal("capable MT must report sources:true")
	}
	code, raw := httpDo(t, ts.URL, "POST", "/api/v1/mt/groups", `{"name":"cap","interface":"awg0"}`)
	if code != 200 {
		t.Fatalf("create: %d %s", code, raw)
	}
	var created magitrickle.Group
	json.Unmarshal(raw, &created)
	code, _ = httpDo(t, ts.URL, "PUT", "/api/v1/mt/groups/"+created.ID+"/policy",
		`{"sourcesMode":"list","sources":["10.0.0.5"],"sourcesRouter":true}`)
	if code != 200 {
		t.Fatalf("policy on capable MT: %d", code)
	}

	mtState.mu.Lock()
	mtState.mtVersion = ""
	mtState.mu.Unlock()
	if sourcesFlag() {
		t.Fatal("old MT must report sources:false")
	}
	code, raw = httpDo(t, ts.URL, "PUT", "/api/v1/mt/groups/"+created.ID+"/policy",
		`{"sourcesMode":"list","sources":["10.0.0.5"],"sourcesRouter":true}`)
	if code != 400 {
		t.Fatalf("policy on old MT must be 400: %d %s", code, raw)
	}
	code, _ = httpDo(t, ts.URL, "PUT", "/api/v1/mt/groups/"+created.ID+"/policy", `{"sourcesMode":"all"}`)
	if code != 200 {
		t.Fatalf("«all» on old MT must work: %d", code)
	}
}

// пресет «DNS через туннель»: применение через панель доезжает до MT
// как группа с subnet-правилами (матч по адресу назначения без DNS)
func TestMTPresetDNSTunnel(t *testing.T) {
	ts, mtState, _ := newImportTestServer(t)

	code, raw := httpDo(t, ts.URL, "POST", "/api/v1/mt/presets/dns-tunnel/apply", `{"interface":"awg0","name":"dns-check"}`)
	if code != 200 {
		t.Fatalf("apply: %d %s", code, raw)
	}
	var out struct {
		OK    bool   `json:"ok"`
		Rules int    `json:"rules"`
		Group string `json:"groupId"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if !out.OK || out.Rules != 14 || out.Group == "" {
		t.Fatalf("отчёт пресета: %+v", out)
	}
	mtState.mu.Lock()
	defer mtState.mu.Unlock()
	var created *magitrickle.Group
	for i := range mtState.groups {
		if mtState.groups[i].Name == "dns-check" {
			created = &mtState.groups[i]
		}
	}
	if created == nil || len(created.Rules) != 14 {
		t.Fatalf("группа не дошла до MT: %+v", created)
	}
	if created.Rules[0].Type != "subnet" || created.Rules[0].Rule != "8.8.8.8/32" || !created.Rules[0].Enable {
		t.Fatalf("первое правило: %+v", created.Rules[0])
	}
}

// дрейф: роутер сменил адрес (ppp-переподключение) - повторная синхронизация
// пересобирает эффективный список (в живой системе это 30-секундный цикл)
func TestMTGroupSourcesDrift(t *testing.T) {
	withRouterAddrs(t, []string{"192.168.0.62"})
	ts, _, cm := newImportTestServer(t)

	code, raw := httpDo(t, ts.URL, "POST", "/api/v1/mt/groups", `{"name":"drift","interface":"awg0"}`)
	if code != 200 {
		t.Fatalf("create: %d %s", code, raw)
	}
	var created magitrickle.Group
	json.Unmarshal(raw, &created)

	code, _ = httpDo(t, ts.URL, "PUT", "/api/v1/mt/groups/"+created.ID+"/policy",
		`{"sourcesMode":"list","sources":[],"sourcesRouter":true}`)
	if code != 200 {
		t.Fatalf("policy: %d", code)
	}
	g := getView(t, ts.URL, created.ID)
	if len(g.Sources) != 1 || g.Sources[0] != "192.168.0.62" {
		t.Fatalf("initial: %v", g.Sources)
	}

	// адреса роутера сменились - ApplyGroupSources (тот же код, что и в
	// 30-секундном SyncHooks) подхватывает новый адрес
	cascade.RouterAddrs = func() []string { return []string{"10.9.9.9"} }
	pol := store.GroupPolicy{SourcesMode: "list", SourcesRouter: true}
	if err := cm.ApplyGroupSources(created.ID, pol); err != nil {
		t.Fatalf("resync: %v", err)
	}
	g = getView(t, ts.URL, created.ID)
	if len(g.Sources) != 1 || g.Sources[0] != "10.9.9.9" {
		t.Fatalf("after drift: %v", g.Sources)
	}
}
