package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"mawg/internal/magitrickle"
	"mawg/internal/store"
)

// devGroup - MT-группа устройства (sources + статика 0.0.0.0/0).
func devGroup(id, name, iface string, sources []string, enable bool) magitrickle.Group {
	return magitrickle.Group{
		ID: id, Name: name, Interface: iface, Enable: enable,
		Sources: sources,
		Rules:   []magitrickle.Rule{{Type: "subnet", Rule: "0.0.0.0/0", Enable: true}},
	}
}

// Реестр «пул ↔ устройство через пул» (97-док): коллизии, запись/
// снятие, сверка, удаление пула.

func TestCanonBindAddr(t *testing.T) {
	cases := map[string]string{
		"192.168.0.5":    "192.168.0.5/32",
		" 192.168.0.5 ":  "192.168.0.5/32",
		"10.0.0.0/24":    "10.0.0.0/24",
		"198.51.100.7/32": "198.51.100.7/32",
		"::1":            "",
		"fe80::/64":      "",
		"мусор":           "",
		"":               "",
	}
	for in, want := range cases {
		if got := canonBindAddr(in); got != want {
			t.Fatalf("canonBindAddr(%q) = %q, ждём %q", in, got, want)
		}
	}
}

func TestCidrsOverlap(t *testing.T) {
	yes := [][2]string{
		{"192.168.0.5/32", "192.168.0.5/32"},
		{"192.168.0.5/32", "192.168.0.0/24"},
		{"192.168.0.0/24", "192.168.0.200/32"},
		{"10.0.0.0/16", "10.0.128.0/17"},
	}
	for _, c := range yes {
		if !cidrsOverlap(c[0], c[1]) {
			t.Fatalf("%s и %s должны пересекаться", c[0], c[1])
		}
	}
	no := [][2]string{
		{"192.168.0.5/32", "192.168.0.6/32"},
		{"10.0.0.0/24", "10.0.1.0/24"},
	}
	for _, c := range no {
		if cidrsOverlap(c[0], c[1]) {
			t.Fatalf("%s и %s не должны пересекаться", c[0], c[1])
		}
	}
}

func TestFindBindingConflicts(t *testing.T) {
	bindings := []store.DeviceBinding{
		{Pool: "warp", Name: "Устройство: 192.0.2.5", IPs: []string{"192.0.2.5/32"}},
		{Pool: "warp", Name: "Сеть: lan", IPs: []string{"203.0.113.0/24"}},
	}
	got := findBindingConflicts(bindings, []string{"198.51.100.9/32"}, "proton",
		[]string{"192.0.2.5", "203.0.113.44", "198.51.100.9", "192.0.2.99"})
	if len(got) != 3 {
		t.Fatalf("ждём 3 конфликта, получили %v", got)
	}
	for _, want := range []string{"пулу warp", "глобальных исключениях"} {
		found := false
		for _, g := range got {
			if strings.Contains(g, want) {
				found = true
			}
		}
		if !found {
			t.Fatalf("нет упоминания %q: %v", want, got)
		}
	}
	// свой пул не конфликт
	if got := findBindingConflicts(bindings, nil, "warp", []string{"192.0.2.5"}); len(got) != 0 {
		t.Fatalf("свой пул не должен конфликтовать: %v", got)
	}
}

func postJSON(t *testing.T, url, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func delJSON(t *testing.T, url string) (int, map[string]any) {
	t.Helper()
	resp, err := http.DefaultClient.Do(mustReq(t, http.MethodDelete, url))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func mustReq(t *testing.T, method, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func doGet(t *testing.T, url string) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// второй пул для коллизий (первый proton создаёт zonesServer)
func addPool(t *testing.T, st *store.Store, name string) {
	t.Helper()
	if _, err := st.CreatePool(name, store.PoolSettings{Platform: store.PlatformOpenwrt, ProbeHost: "1.1.1.1"}); err != nil {
		t.Fatal(err)
	}
}

func TestDeviceBindingCreateCollision(t *testing.T) {
	srv, fm, st := zonesServer(t, "0.8.2-rev13-mawg-dev")
	addPool(t, st, "warp")
	if err := st.SetDeviceBindings([]store.DeviceBinding{
		{Pool: "warp", Name: "Устройство: 192.0.2.5", IPs: []string{"192.0.2.5/32"}},
	}); err != nil {
		t.Fatal(err)
	}

	// коллизия с чужим пулом: 409 + список, MT не тронут
	code, out := postJSON(t, srv.URL+"/api/v1/mt/device-group",
		`{"pool":"proton","ips":["192.0.2.5"]}`)
	if code != http.StatusConflict {
		t.Fatalf("коллизия: код %d: %v", code, out)
	}
	if out["conflicts"] == nil || !strings.Contains(out["error"].(string), "warp") {
		t.Fatalf("нет списка конфликтов: %v", out)
	}
	if len(fm.groups) != 0 {
		t.Fatalf("MT тронут при 409: %v", fm.groups)
	}

	// подтверждение: создаётся и запись реестра появляется
	code, out = postJSON(t, srv.URL+"/api/v1/mt/device-group",
		`{"pool":"proton","ips":["192.0.2.5"],"confirm":true}`)
	if code != http.StatusOK {
		t.Fatalf("confirm: код %d: %v", code, out)
	}
	bindings := st.DeviceBindings()
	var found bool
	for _, b := range bindings {
		if b.Pool == "proton" && b.Name == "Устройство: 192.0.2.5" && len(b.IPs) == 1 && b.IPs[0] == "192.0.2.5/32" {
			found = true
		}
	}
	if !found {
		t.Fatalf("запись proton не записана: %+v", bindings)
	}
	confirmed := false
	for _, e := range st.Events() {
		if e.Kind == "device-bindings" && strings.Contains(e.Message, "подтверждена вручную") {
			confirmed = true
		}
	}
	if !confirmed {
		t.Fatal("нет события о подтверждении коллизии")
	}

	// тот же адрес в свой пул - не конфликт (своя запись)
	code, out = postJSON(t, srv.URL+"/api/v1/mt/device-group",
		`{"pool":"warp","ips":["192.0.2.5"],"name":"дубль в warp","confirm":true}`)
	if code != http.StatusOK {
		t.Fatalf("свой адрес в свой пул: код %d: %v", code, out)
	}
}

func TestDeviceBindingExcludesCollision(t *testing.T) {
	srv, _, st := zonesServer(t, "0.8.2-rev13-mawg-dev")
	if err := st.SetExcludes([]string{"198.51.100.9"}); err != nil {
		t.Fatal(err)
	}
	code, out := postJSON(t, srv.URL+"/api/v1/mt/device-group",
		`{"pool":"proton","ips":["198.51.100.9"]}`)
	if code != http.StatusConflict {
		t.Fatalf("исключение: код %d: %v", code, out)
	}
	if !strings.Contains(out["error"].(string), "исключениях") {
		t.Fatalf("нет упоминания исключений: %v", out)
	}
	if len(st.DeviceBindings()) != 0 {
		t.Fatal("запись не должна была создаться при 409")
	}
}

func TestDeviceBindingDelete(t *testing.T) {
	srv, fm, st := zonesServer(t, "0.8.2-rev13-mawg-dev")
	code, out := postJSON(t, srv.URL+"/api/v1/mt/device-group",
		`{"pool":"proton","ips":["192.0.2.7","192.0.2.8"]}`)
	if code != http.StatusOK {
		t.Fatalf("создание: код %d: %v", code, out)
	}
	if len(st.DeviceBindings()) != 1 {
		t.Fatalf("запись не создана: %+v", st.DeviceBindings())
	}

	// посторонняя (не device) группа - отказ
	fm.mu.Lock()
	fm.groups = append(fm.groups, magitrickle.Group{ID: "g99", Name: "обычная", Interface: "proton", Enable: true})
	fm.mu.Unlock()
	if code, _ := delJSON(t, srv.URL+"/api/v1/mt/device-group/"+url.PathEscape("обычная")); code != http.StatusBadRequest {
		t.Fatalf("не-device группа: код %d", code)
	}

	code, out = delJSON(t, srv.URL+"/api/v1/mt/device-group/"+url.PathEscape("Устройство: 192.0.2.7 +1"))
	if code != http.StatusOK {
		t.Fatalf("удаление: код %d: %v", code, out)
	}
	if got := fm.deletedGroups(); len(got) != 1 || got[0] != "g1" {
		t.Fatalf("MT-группа не удалена: %v", got)
	}
	if len(st.DeviceBindings()) != 0 {
		t.Fatalf("запись не снята: %+v", st.DeviceBindings())
	}
	removed := false
	for _, e := range st.Events() {
		if e.Kind == "device-bindings" && strings.Contains(e.Message, "снята привязка") {
			removed = true
		}
	}
	if !removed {
		t.Fatal("нет события снятия привязки")
	}
	// повторно - 404, запись и так пуста
	if code, _ := delJSON(t, srv.URL+"/api/v1/mt/device-group/"+url.PathEscape("Устройство: 192.0.2.7 +1")); code != http.StatusNotFound {
		t.Fatalf("повторное удаление: код %d", code)
	}
}

func TestReconcileDeviceBindings(t *testing.T) {
	srv, fm, st := zonesServer(t, "0.8.2-rev13-mawg-dev")
	if err := st.SetDeviceBindings([]store.DeviceBinding{
		{Pool: "proton", Name: "Живая связка", IPs: []string{"192.0.2.1/32"}},
		{Pool: "proton", Name: "Гость без группы", IPs: []string{"192.0.2.2/32"}},
	}); err != nil {
		t.Fatal(err)
	}
	fm.mu.Lock()
	fm.groups = []magitrickle.Group{
		devGroup("g1", "Живая связка", "proton", []string{"192.0.2.1"}, true),
		devGroup("g2", "Новая без записи", "proton", []string{"192.0.2.3"}, true),
		devGroup("g3", "Выключенная", "proton", []string{"192.0.2.4"}, false),
		devGroup("g4", "Чужой интерфейс", "tun99", []string{"192.0.2.5"}, true),
	}
	fm.mu.Unlock()

	if code := doGet(t, srv.URL+"/api/v1/mt/groups"); code != http.StatusOK {
		t.Fatalf("mt/groups: код %d", code)
	}
	bindings := st.DeviceBindings()
	if len(bindings) != 2 {
		t.Fatalf("ждём 2 записи (живая + новая), получили %+v", bindings)
	}
	byName := map[string]store.DeviceBinding{}
	for _, b := range bindings {
		byName[b.Name] = b
	}
	b, ok := byName["Новая без записи"]
	if !ok || b.Pool != "proton" || len(b.IPs) != 1 || b.IPs[0] != "192.0.2.3/32" {
		t.Fatalf("бэкфилл не сработал: %+v", bindings)
	}
	if _, ok := byName["Гость без группы"]; ok {
		t.Fatal("отжившая запись не снята")
	}
	if _, ok := byName["Живая связка"]; !ok {
		t.Fatal("живая запись потерялась")
	}
}

func TestDeletePoolDevices(t *testing.T) {
	// direct (и дефолт): группы остаются, но выключены; записи сняты
	for _, q := range []string{"", "?devices=direct"} {
		srv, fm, st := zonesServer(t, "0.8.2-rev13-mawg-dev")
		code, out := postJSON(t, srv.URL+"/api/v1/mt/device-group",
			`{"pool":"proton","ips":["192.0.2.7"]}`)
		if code != http.StatusOK {
			t.Fatalf("создание: код %d: %v", code, out)
		}
		if code, out := delJSON(t, srv.URL+"/api/v1/pools/proton"+q); code != http.StatusOK {
			t.Fatalf("удаление пула %q: код %d: %v", q, code, out)
		}
		fm.mu.Lock()
		left := len(fm.groups)
		enabled := left > 0 && fm.groups[0].Enable
		fm.mu.Unlock()
		if left != 1 || enabled {
			t.Fatalf("direct: группа должна остаться выключенной (left=%d enabled=%v)", left, enabled)
		}
		if len(st.DeviceBindings()) != 0 {
			t.Fatalf("direct: запись не снята: %+v", st.DeviceBindings())
		}
	}

	// delete: группы устройств удаляются вместе с пулом
	srv, fm, st := zonesServer(t, "0.8.2-rev13-mawg-dev")
	if code, out := postJSON(t, srv.URL+"/api/v1/mt/device-group",
		`{"pool":"proton","ips":["192.0.2.7"]}`); code != http.StatusOK {
		t.Fatalf("создание: код %d: %v", code, out)
	}
	if code, out := delJSON(t, srv.URL+"/api/v1/pools/proton?devices=delete"); code != http.StatusOK {
		t.Fatalf("удаление пула: код %d: %v", code, out)
	}
	if got := fm.deletedGroups(); len(got) != 1 || got[0] != "g1" {
		t.Fatalf("группа устройства не удалена: %v", got)
	}
	if len(st.DeviceBindings()) != 0 {
		t.Fatalf("запись не снята: %+v", st.DeviceBindings())
	}
	// мусорный режим - отказ
	if code, _ := delJSON(t, srv.URL+"/api/v1/pools/proton?devices=ерунда"); code == http.StatusOK {
		t.Fatal("мусорный режим устройств должен отклоняться")
	}
}
