package web

// Переключатель «Маршрутизация DNS» (115-док, слово владельца второе):
// off без confirm = 409; off = список «Резервных» сохранён в стор,
// апстримы форка подменены на резолвер роутера (127.0.0.1:53), зоны
// приостановлены, рестарт MT НЕ нужен; гейты 409; on = восстановление.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"mawg/internal/magitrickle"
	"mawg/internal/platform/fake"
	"mawg/internal/rotator"
	"mawg/internal/store"
)

func readBody(resp *http.Response) string {
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return string(raw)
}

func httpGetJSON(t *testing.T, url string) map[string]any {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	return m
}

// fakeMTAPI - синтетический форк MT: апстримы/зоны держит в памяти
// (PUT горячо применяет, как настоящий форк), версию отдаёт rev19.
type fakeMTAPI struct {
	mu         sync.Mutex
	ups        []string
	zones      int
	upPuts     int
	zonePuts   []int // len таблицы на каждый PUT
	routerDead bool  // health-проба 127.0.0.1:53 валится (петля)
}

func (f *fakeMTAPI) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.URL.Path == "/api/v1/version":
			json.NewEncoder(w).Encode(map[string]string{"version": "0.8.2-rev19-mawg-dev"})
		case r.URL.Path == "/api/v1/groups":
			json.NewEncoder(w).Encode(map[string]any{"groups": []map[string]any{}})
		case r.URL.Path == "/api/v1/dns/upstreams" && r.Method == http.MethodGet:
			type up struct {
				Address string `json:"address"`
				Alive   bool   `json:"alive"`
				Active  bool   `json:"active"`
			}
			list := make([]up, 0, len(f.ups))
			for _, a := range f.ups {
				alive := true
				if a == "127.0.0.1:53" && f.routerDead {
					alive = false
				}
				list = append(list, up{Address: a, Alive: alive, Active: a == f.ups[0]})
			}
			json.NewEncoder(w).Encode(map[string]any{"available": true, "health": map[string]any{"enabled": true}, "upstreams": list})
		case r.URL.Path == "/api/v1/dns/upstreams" && r.Method == http.MethodPut:
			var req struct {
				Upstreams []string `json:"upstreams"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			if len(req.Upstreams) == 0 {
				w.WriteHeader(400)
				json.NewEncoder(w).Encode(map[string]string{"error": "нужен хотя бы один сервер"})
				return
			}
			f.ups = req.Upstreams
			f.upPuts++
			w.WriteHeader(200)
			json.NewEncoder(w).Encode(map[string]any{"ok": true})
		case r.URL.Path == "/api/v1/dns/zones" && r.Method == http.MethodGet:
			json.NewEncoder(w).Encode(map[string]any{"zones": make([]map[string]any, f.zones)})
		case r.URL.Path == "/api/v1/dns/zones" && r.Method == http.MethodPut:
			var req struct {
				Zones []map[string]any `json:"zones"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			f.zones = len(req.Zones)
			f.zonePuts = append(f.zonePuts, f.zones)
			w.WriteHeader(200)
			json.NewEncoder(w).Encode(map[string]any{"ok": true})
		default:
			w.WriteHeader(404)
		}
	})
}

func dnsRoutingTestEnv(t *testing.T) (*httptest.Server, *store.Store, *fakeMTAPI) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mt := &fakeMTAPI{ups: []string{"8.8.8.8:53", "77.88.8.8:53"}, zones: 1}
	mtSrv := httptest.NewServer(mt.handler())
	t.Cleanup(mtSrv.Close)
	// зона пользователя в сторе: suspend должен очистить таблицу форка
	if err := st.SetDnsZones([]store.DnsZone{{ID: "z1", Zone: "corp.example.com", Upstreams: []string{"10.0.0.53:53"}}}); err != nil {
		t.Fatal(err)
	}

	fb := fake.New()
	fb.ShellFn = func(script string) (string, error) {
		if strings.Contains(script, "MT_DNSOR") {
			return "1\n", nil // перехват жив всегда (не трогается переключателем)
		}
		return "", nil
	}
	magitrickle.SetShadowStorage(nil, nil)
	e := rotator.New(st, fb, magitrickle.New(mtSrv.URL))
	srv := New(st, e, fb, magitrickle.New(mtSrv.URL), "test", nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, st, mt
}

func TestDNSRoutingOffOn(t *testing.T) {
	ts, st, mt := dnsRoutingTestEnv(t)
	url := ts.URL

	// off без confirm - честный 409
	resp, err := httpPut(url+"/api/v1/dns/managed", `{"routing":false}`)
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(resp)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(body, "needConfirm") {
		t.Fatalf("off без confirm: status %d, body %s", resp.StatusCode, body)
	}

	// off с confirm: апстримы -> резолвер роутера, список сохранён
	resp, err = httpPut(url+"/api/v1/dns/managed", `{"routing":false,"confirm":true}`)
	if err != nil {
		t.Fatal(err)
	}
	body = readBody(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("off с confirm: status %d, body %s", resp.StatusCode, body)
	}
	mt.mu.Lock()
	upPuts, liveUps, zonePuts := mt.upPuts, append([]string(nil), mt.ups...), append([]int(nil), mt.zonePuts...)
	mt.mu.Unlock()
	if upPuts != 1 || len(liveUps) != 1 || liveUps[0] != "127.0.0.1:53" {
		t.Fatalf("форк не подменён: puts %d, ups %v", upPuts, liveUps)
	}
	if len(zonePuts) != 1 || zonePuts[0] != 0 {
		t.Fatalf("зоны не приостановлены: %v", zonePuts)
	}
	// стор с зонами цел - рестор при включении
	if len(st.DnsZones()) != 1 {
		t.Fatalf("зоны стора пострадали: %v", st.DnsZones())
	}
	saved := st.DnsSavedUpstreams()
	if len(saved) != 2 || saved[0] != "8.8.8.8:53" || saved[1] != "77.88.8.8:53" {
		t.Fatalf("список не сохранён: %v", saved)
	}
	if v := st.DnsRouting(); v == nil || *v {
		t.Fatalf("флаг в сторе: %v", v)
	}

	// GET показывает режим и живой перехват (не тронут)
	got := httpGetJSON(t, url+"/api/v1/dns/managed")
	if r, ok := got["routing"].(bool); !ok || r {
		t.Fatalf("GET routing: %v", got["routing"])
	}
	if got["remap53"] != true {
		t.Fatalf("GET remap53: %v (перехват должен жить)", got["remap53"])
	}

	// гейты: правки DNS при выключенной маршрутизации - 409
	resp, err = http.Post(url+"/api/v1/dns/dnsmasq", "application/json", strings.NewReader(`{"mode":"mt"}`))
	if err != nil {
		t.Fatal(err)
	}
	body = readBody(resp)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(body, "маршрутизация DNS выключена") {
		t.Fatalf("гейт dnsmasq: status %d, body %s", resp.StatusCode, body)
	}
	for _, ep := range []string{"/api/v1/dns/upstreams", "/api/v1/dns/policy", "/api/v1/dns/zones"} {
		resp, err := httpPut(url+ep, `{"mode":"mt"}`)
		if err != nil {
			t.Fatal(err)
		}
		body = readBody(resp)
		if resp.StatusCode != http.StatusConflict || !strings.Contains(body, "маршрутизация DNS выключена") {
			t.Fatalf("гейт %s: status %d, body %s", ep, resp.StatusCode, body)
		}
	}

	// on: список вернулся в форк, флаг nil, сохранённое очищено
	resp, err = httpPut(url+"/api/v1/dns/managed", `{"routing":true}`)
	if err != nil {
		t.Fatal(err)
	}
	body = readBody(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("on: status %d, body %s", resp.StatusCode, body)
	}
	mt.mu.Lock()
	liveUps = append([]string(nil), mt.ups...)
	mt.mu.Unlock()
	if len(liveUps) != 2 || liveUps[0] != "8.8.8.8:53" {
		t.Fatalf("список не вернулся: %v", liveUps)
	}
	if v := st.DnsRouting(); v != nil {
		t.Fatalf("флаг после on: %v, want nil", v)
	}
	if len(st.DnsSavedUpstreams()) != 0 {
		t.Fatalf("сохранённое не очищено: %v", st.DnsSavedUpstreams())
	}
}

func TestDNSRoutingOnNoop(t *testing.T) {
	ts, _, mt := dnsRoutingTestEnv(t)
	resp, err := httpPut(ts.URL+"/api/v1/dns/managed", `{"routing":true}`)
	if err != nil {
		t.Fatal(err)
	}
	readBody(resp)
	mt.mu.Lock()
	defer mt.mu.Unlock()
	if mt.upPuts != 0 {
		t.Fatalf("noop on трогает форк: puts %d", mt.upPuts)
	}
}

// Гард петли (живой кейс 0.62 09.11): MT отвечает через резолвер роутера,
// health-проба валится - направить dnsmasq в MT нельзя, иначе
// dnsmasq → MT → dnsmasq роняет DNS устройств.
func TestDNSDnsmasqLoopGuard(t *testing.T) {
	ts, _, mt := dnsRoutingTestEnv(t)
	mt.mu.Lock()
	mt.ups = []string{"127.0.0.1:53"}
	mt.routerDead = true
	mt.mu.Unlock()

	resp, err := http.Post(ts.URL+"/api/v1/dns/dnsmasq", "application/json", strings.NewReader(`{"mode":"mt"}`))
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(resp)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(body, "замкнёт петлю") {
		t.Fatalf("гард петли: status %d, body %s", resp.StatusCode, body)
	}

	// живой апстрим роутера - историческая форма, не блокируем
	mt.mu.Lock()
	mt.routerDead = false
	mt.mu.Unlock()
	resp, err = http.Post(ts.URL+"/api/v1/dns/dnsmasq", "application/json", strings.NewReader(`{"mode":"mt"}`))
	if err != nil {
		t.Fatal(err)
	}
	readBody(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("живой апстрим роутера не должен блокироваться: status %d", resp.StatusCode)
	}
}
