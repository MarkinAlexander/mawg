package web

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"

	"mawg/internal/magitrickle"
	"mawg/internal/store"
)

func devicePool() store.Pool {
	return store.Pool{Name: "warp", Settings: store.PoolSettings{
		Platform: "openwrt", OpenwrtProto: "wireguard", KeeneticSlot: "Wireguard3",
	}}
}

func TestBuildDeviceGroup(t *testing.T) {
	g := buildDeviceGroup(devicePool(), "Устройство: 192.168.1.50", []string{"192.168.1.50", "192.168.1.0/24"})
	if g.Interface == "" {
		t.Fatal("интерфейс пула должен подставляться")
	}
	if !g.Enable {
		t.Fatal("группа создаётся включённой")
	}
	if len(g.Sources) != 2 {
		t.Fatalf("sources: %v", g.Sources)
	}
	if len(g.Rules) != 1 || g.Rules[0].Type != "subnet" || g.Rules[0].Rule != "0.0.0.0/0" || !g.Rules[0].Enable {
		t.Fatalf("правило «весь трафик»: %+v", g.Rules)
	}
}

func TestDeviceGroupName(t *testing.T) {
	if got := deviceGroupName("192.168.1.50", 1); got != "Устройство: 192.168.1.50" {
		t.Fatalf("имя: %q", got)
	}
	if got := deviceGroupName("192.168.1.50", 3); got != "Устройство: 192.168.1.50 +2" {
		t.Fatalf("имя с несколькими: %q", got)
	}
}

func TestDeviceGroupOf(t *testing.T) {
	full := magitrickle.Group{Sources: []string{"192.168.1.50"},
		Rules: []magitrickle.Rule{{Type: "subnet", Rule: "0.0.0.0/0", Enable: true}}}
	if !deviceGroupOf(full) {
		t.Fatal("группа устройства должна детектиться")
	}
	partial := magitrickle.Group{Sources: []string{"192.168.1.50"},
		Rules: []magitrickle.Rule{{Type: "subnet", Rule: "8.8.8.8/32", Enable: true}}}
	if deviceGroupOf(partial) {
		t.Fatal("статика не «весь трафик» - не группа устройства")
	}
	noSrc := magitrickle.Group{Rules: []magitrickle.Rule{{Type: "subnet", Rule: "0.0.0.0/0", Enable: true}}}
	if deviceGroupOf(noSrc) {
		t.Fatal("без источников - обычная группа (весь трафик локалки), не устройство")
	}
}

// адрес роутера в источниках = петля туннеля (живой кейс 0.62)
func TestRouterAddrConflicts(t *testing.T) {
	router := []net.IP{net.IP{192, 168, 0, 62}.To4(), net.IP{127, 0, 0, 1}.To4()}
	for _, src := range []string{"192.168.0.62", "127.0.0.1", "192.168.0.0/24", "0.0.0.0/0"} {
		if !routerAddrConflicts(src, router) {
			t.Fatalf("%s должен конфликтовать с адресами роутера", src)
		}
	}
	for _, src := range []string{"192.168.0.50", "192.168.0.199", "192.168.1.0/24", "10.5.0.7"} {
		if routerAddrConflicts(src, router) {
			t.Fatalf("%s не должен конфликтовать", src)
		}
	}
}

// guards до похода в MT (на Windows тест-сервер MT недоступен)
func TestDeviceGroupEndpointGuards(t *testing.T) {
	ts := newTestServer(t)

	resp, err := http.Post(ts.URL+"/api/v1/mt/device-group", "application/json",
		strings.NewReader(`{"pool":"nope","ips":["192.168.1.50"]}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("неизвестный пул: код %d", resp.StatusCode)
	}

	// валидный пул, но кривые IP - честный отказ до записи в MT
	if resp, err = http.Post(ts.URL+"/api/v1/pools", "application/json",
		strings.NewReader(`{"name":"warp","fallback":"direct"}`)); err != nil || resp.StatusCode != 200 {
		t.Fatalf("пул не создался: %v %d", err, resp.StatusCode)
	} else {
		resp.Body.Close()
	}
	for _, payload := range []string{
		`{"pool":"warp","ips":[]}`,
		`{"pool":"warp","ips":["абракадабра"]}`,
		`{"pool":""}`,
	} {
		resp, err = http.Post(ts.URL+"/api/v1/mt/device-group", "application/json", strings.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		var out struct {
			Error string `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest || out.Error == "" {
			t.Fatalf("guard %s: код %d err %q", payload, resp.StatusCode, out.Error)
		}
	}
}

// Цель «внешний интерфейс» (слово владельца 11.2026): для tun чужого
// софта - сразу указать устройства, правило создаётся без пула. Внешний
// должен быть помечен «Сделать внешним», иначе отказ с подсказкой.
// Цель «внешний интерфейс» (слово владельца 11.2026): для tun чужого
// софта - сразу указать устройства, правило создаётся без пула. Внешний
// должен быть помечен «Сделать внешним», иначе отказ с подсказкой.
func TestCreateDeviceGroupOnExternalIface(t *testing.T) {
	srv, fm, st := zonesServer(t, "0.8.2-rev13-mawg-dev")

	// ещё не внешний - отказ с подсказкой, MT не тронут
	code, out := postJSON(t, srv.URL+"/api/v1/mt/device-group",
		`{"iface":"tun9","ips":["192.0.2.50"]}`)
	if code != http.StatusBadRequest || !strings.Contains(out["error"].(string), "Сделать внешним") {
		t.Fatalf("не-внешний: %d %v", code, out)
	}
	if err := st.SetIfaceMode("tun9", store.IfaceExternal); err != nil {
		t.Fatal(err)
	}
	code, out = postJSON(t, srv.URL+"/api/v1/mt/device-group",
		`{"iface":"tun9","ips":["192.0.2.50"]}`)
	if code != http.StatusOK {
		t.Fatalf("внешний: %d %v", code, out)
	}
	// группа в MT на интерфейсе tun9; реестр привязок - только про пулы
	if len(fm.groups) != 1 || fm.groups[0].Interface != "tun9" || len(fm.groups[0].Sources) != 1 {
		t.Fatalf("группа на внешнем: %+v", fm.groups)
	}
	if len(st.DeviceBindings()) != 0 {
		t.Fatalf("реестр привязок не для внешних: %+v", st.DeviceBindings())
	}
}
