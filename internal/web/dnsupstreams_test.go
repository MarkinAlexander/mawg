package web

import (
	"encoding/json"
	"fmt"
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

// fakeMT - фейковый форк MT (rev8): /version, GET/PUT /dns/upstreams,
// группы (bulk PUT для пресетов); PUT-тела собираются в lastPuts.
// Зонные эндпоинты (rev13) и DELETE групп - для тестов зонного DNS.
type fakeMT struct {
	version      string
	health       map[string]any // текущая политика зондов; rev14 умеет править (частичный патч)
	mu           sync.Mutex
	lastPuts     []string
	getCounter   int
	groups       []magitrickle.Group
	groupSeq     int      // выдача id группам без id (как живой MT)
	groupPuts    []string // тела bulk/single PUT групп
	groupsDelete []string // id удалённых групп
	zonePuts     []string // тела PUT /dns/zones
}

func defaultFakeHealth() map[string]any {
	return map[string]any{
		"enabled": true, "interval": "10s", "timeout": "2s",
		"failCycles": 3, "restoreCycles": 2, "restoreGap": "5m0s",
	}
}

func (f *fakeMT) snapshot() map[string]any {
	health := f.health
	if health == nil {
		health = defaultFakeHealth()
	}
	return map[string]any{
		"available": true,
		"health":    health,
		"upstreams": []map[string]any{
			{"address": "8.8.8.8:53", "alive": true, "active": true, "fails": 0, "oks": 5, "lastSwap": "0001-01-01T00:00:00Z"},
			{"address": "77.88.8.8:53", "alive": false, "active": false, "fails": 3, "oks": 0, "lastSwap": "2026-10-16T10:00:00Z", "reason": "подмена: канонических адресов нет"},
		},
	}
}

func (f *fakeMT) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/version", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"version": f.version})
	})
	mux.HandleFunc("GET /api/v1/dns/upstreams", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.getCounter++
		f.mu.Unlock()
		json.NewEncoder(w).Encode(f.snapshot())
	})
	mux.HandleFunc("PUT /api/v1/dns/upstreams", func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		raw := string(buf[:n])
		f.mu.Lock()
		f.lastPuts = append(f.lastPuts, raw)
		// rev14 умеет health: частичный патч (только присланные поля,
		// секунды -> строки-длительности как в живом снапшоте форка);
		// старые ревизии неизвестное поле игнорируют - стейт не трогаем
		if magitrickle.DnsHealthSupported(f.version) {
			var body struct {
				Health map[string]json.RawMessage `json:"health"`
			}
			if err := json.Unmarshal([]byte(raw), &body); err == nil && body.Health != nil {
				cur := f.health
				if cur == nil {
					cur = defaultFakeHealth()
				}
				sec := func(key, dst string) {
					v, ok := body.Health[key]
					if !ok {
						return
					}
					var n float64
					if json.Unmarshal(v, &n) == nil {
						cur[dst] = fmt.Sprintf("%ds", int(n))
					}
				}
				if v, ok := body.Health["enabled"]; ok {
					var b bool
					if json.Unmarshal(v, &b) == nil {
						cur["enabled"] = b
					}
				}
				sec("intervalSec", "interval")
				sec("timeoutSec", "timeout")
				sec("restoreGapSec", "restoreGap")
				num := func(key string) {
					v, ok := body.Health[key]
					if !ok {
						return
					}
					var n float64
					if json.Unmarshal(v, &n) == nil {
						cur[key] = int(n)
					}
				}
				num("failCycles")
				num("restoreCycles")
				f.health = cur
			}
		}
		snap := f.snapshot()
		f.mu.Unlock()
		json.NewEncoder(w).Encode(snap)
	})
	// группы: список, bulk PUT (AddGroup/UpdateGroups), одиночный PUT
	mux.HandleFunc("GET /api/v1/groups", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"groups": f.groups})
	})
	mux.HandleFunc("PUT /api/v1/groups", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Groups []magitrickle.Group `json:"groups"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		if len(body.Groups) > 0 {
			last := body.Groups[len(body.Groups)-1]
			f.groupPuts = append(f.groupPuts, "bulk:"+last.Name+"@"+last.Interface)
		}
		for i := range body.Groups {
			if body.Groups[i].ID == "" {
				f.groupSeq++
				body.Groups[i].ID = fmt.Sprintf("g%d", f.groupSeq)
			}
		}
		f.groups = body.Groups
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	mux.HandleFunc("PUT /api/v1/groups/{id}", func(w http.ResponseWriter, r *http.Request) {
		var g magitrickle.Group
		json.NewDecoder(r.Body).Decode(&g)
		f.mu.Lock()
		f.groupPuts = append(f.groupPuts, "single:"+g.Name+"@"+g.Interface)
		for i := range f.groups {
			if f.groups[i].ID == r.PathValue("id") {
				f.groups[i] = g
			}
		}
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	mux.HandleFunc("DELETE /api/v1/groups/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		f.mu.Lock()
		f.groupsDelete = append(f.groupsDelete, id)
		for i := range f.groups {
			if f.groups[i].ID == id {
				f.groups = append(f.groups[:i], f.groups[i+1:]...)
				break
			}
		}
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	mux.HandleFunc("GET /api/v1/dns/zones", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"available": true, "zones": []any{}})
	})
	mux.HandleFunc("PUT /api/v1/dns/zones", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 65536))
		f.mu.Lock()
		f.zonePuts = append(f.zonePuts, string(body))
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"available": true, "zones": []any{}})
	})
	return mux
}

func (f *fakeMT) zonePutsList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.zonePuts...)
}

func (f *fakeMT) deletedGroups() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.groupsDelete...)
}

func (f *fakeMT) puts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.lastPuts...)
}

func dnsTestServerMT(t *testing.T, mtURL string) *httptest.Server {
	t.Helper()
	fb := fake.New()
	fb.ShellFn = dnsShellStub("dhcp.@dnsmasq[0].server='127.0.0.1#3553'\n")
	st, _ := store.Open(t.TempDir())
	engine := rotator.New(st, fb, nil)
	srv := httptest.NewServer(New(st, engine, fb, magitrickle.New(mtURL), "test", nil).Handler())
	t.Cleanup(srv.Close)
	return srv
}

// не-capable MT (rev7): GET честно отдаёт {capable:false}, PUT - 501
func TestDNSUpstreamsNotCapable(t *testing.T) {
	mt := httptest.NewServer((&fakeMT{version: "0.8.2-rev7-mawgfix1"}).handler())
	defer mt.Close()
	srv := dnsTestServerMT(t, mt.URL)

	resp, err := http.Get(srv.URL + "/api/v1/dns/upstreams")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Capable bool `json:"capable"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || body.Capable {
		t.Fatalf("ожидался capable=false, status=%d body=%+v", resp.StatusCode, body)
	}

	req2, err := http.NewRequest(http.MethodPut, srv.URL+"/api/v1/dns/upstreams", strings.NewReader(`{"upstreams":["8.8.8.8"]}`))
	if err != nil {
		t.Fatal(err)
	}
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotImplemented {
		t.Fatalf("PUT на rev7: status=%d", resp2.StatusCode)
	}
}

// capable MT (rev8): GET проксирует снапшот, PUT доезжает до форка
func TestDNSUpstreamsCapable(t *testing.T) {
	fm := &fakeMT{version: "0.8.2-rev8-mawgfix1"}
	mt := httptest.NewServer(fm.handler())
	defer mt.Close()
	srv := dnsTestServerMT(t, mt.URL)

	resp, err := http.Get(srv.URL + "/api/v1/dns/upstreams")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Capable bool `json:"capable"`
		State   struct {
			Available bool `json:"available"`
			Upstreams []struct {
				Address string `json:"address"`
				Alive   bool   `json:"alive"`
				Active  bool   `json:"active"`
				Reason  string `json:"reason"`
			} `json:"upstreams"`
		} `json:"state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Capable || !body.State.Available || len(body.State.Upstreams) != 2 {
		t.Fatalf("снапшот не дошёл: %+v", body)
	}
	if body.State.Upstreams[1].Address != "77.88.8.8:53" || body.State.Upstreams[1].Alive {
		t.Fatalf("мёртвый апстрим потерялся: %+v", body.State.Upstreams[1])
	}
	if !strings.Contains(body.State.Upstreams[1].Reason, "канонических") {
		t.Fatalf("причина потерялась: %q", body.State.Upstreams[1].Reason)
	}

	req2, err := http.NewRequest(http.MethodPut, srv.URL+"/api/v1/dns/upstreams", strings.NewReader(`{"upstreams":["77.88.8.8:53","8.8.8.8"]}`))
	if err != nil {
		t.Fatal(err)
	}
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	var putBody struct {
		OK bool `json:"ok"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&putBody); err != nil {
		t.Fatal(err)
	}
	if !putBody.OK || resp2.StatusCode != http.StatusOK {
		t.Fatalf("PUT: status=%d body=%+v", resp2.StatusCode, putBody)
	}
	puts := fm.puts()
	if len(puts) != 1 || !strings.Contains(puts[0], `"77.88.8.8:53"`) || !strings.Contains(puts[0], `"8.8.8.8"`) {
		t.Fatalf("тело PUT не дошло до форка: %q", puts)
	}
}

func dnsPut(t *testing.T, url, payload string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, url, strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// rev14: GET отдаёт healthCapable; health-only PUT (без upstreams) доезжает
// до форка и применяется частично; невалидный диапазон mawg режет ДО форка
func TestDNSUpstreamsHealth(t *testing.T) {
	fm := &fakeMT{version: "0.8.2-rev14-mawg-dev"}
	mt := httptest.NewServer(fm.handler())
	defer mt.Close()
	srv := dnsTestServerMT(t, mt.URL)

	resp, err := http.Get(srv.URL + "/api/v1/dns/upstreams")
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Capable       bool `json:"capable"`
		HealthCapable bool `json:"healthCapable"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !body.Capable || !body.HealthCapable {
		t.Fatalf("ожидались capable+healthCapable: %+v", body)
	}

	// health-only: в теле форку нет upstreams, поля частичные
	if code := dnsPut(t, srv.URL+"/api/v1/dns/upstreams", `{"health":{"enabled":false,"intervalSec":30,"restoreGapSec":0}}`); code != http.StatusOK {
		t.Fatalf("health-only PUT: status=%d", code)
	}
	puts := fm.puts()
	if len(puts) != 1 || strings.Contains(puts[0], `"upstreams"`) || !strings.Contains(puts[0], `"intervalSec":30`) {
		t.Fatalf("тело health-only PUT: %q", puts)
	}

	// частичное применение: присланные поля легли, остальные - дефолт
	resp, err = http.Get(srv.URL + "/api/v1/dns/upstreams")
	if err != nil {
		t.Fatal(err)
	}
	var after struct {
		State struct {
			Health struct {
				Enabled    bool   `json:"enabled"`
				Interval   string `json:"interval"`
				Timeout    string `json:"timeout"`
				RestoreGap string `json:"restoreGap"`
			} `json:"health"`
		} `json:"state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&after); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	h := after.State.Health
	if h.Enabled || h.Interval != "30s" || h.RestoreGap != "0s" || h.Timeout != "2s" {
		t.Fatalf("health после патча: %+v", h)
	}

	// невалидный диапазон: 400 от mawg, до форка не доходит
	if code := dnsPut(t, srv.URL+"/api/v1/dns/upstreams", `{"health":{"intervalSec":301}}`); code != http.StatusBadRequest {
		t.Fatalf("интервал 301: status=%d, хочу 400", code)
	}
	if code := dnsPut(t, srv.URL+"/api/v1/dns/upstreams", `{"health":{"restoreCycles":0}}`); code != http.StatusBadRequest {
		t.Fatalf("restoreCycles 0: status=%d, хочу 400", code)
	}
	if len(fm.puts()) != 1 {
		t.Fatalf("невалидный health дошёл до форка: %q", fm.puts())
	}

	// пустой запрос (ни списка, ни health) - честная ошибка
	if code := dnsPut(t, srv.URL+"/api/v1/dns/upstreams", `{}`); code == http.StatusOK {
		t.Fatal("пустой PUT не должен проходить")
	}
}

// rev13: healthCapable=false честно, PUT с health - 501, PUT со списком
// работает как раньше
func TestDNSUpstreamsHealthOnRev13(t *testing.T) {
	fm := &fakeMT{version: "0.8.2-rev13-mawg-dev"}
	mt := httptest.NewServer(fm.handler())
	defer mt.Close()
	srv := dnsTestServerMT(t, mt.URL)

	resp, err := http.Get(srv.URL + "/api/v1/dns/upstreams")
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Capable       bool `json:"capable"`
		HealthCapable bool `json:"healthCapable"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !body.Capable || body.HealthCapable {
		t.Fatalf("rev13: ожидался capable=true, healthCapable=false: %+v", body)
	}

	if code := dnsPut(t, srv.URL+"/api/v1/dns/upstreams", `{"health":{"intervalSec":30}}`); code != http.StatusNotImplemented {
		t.Fatalf("health на rev13: status=%d, хочу 501", code)
	}
	if code := dnsPut(t, srv.URL+"/api/v1/dns/upstreams", `{"upstreams":["8.8.8.8"]}`); code != http.StatusOK {
		t.Fatalf("список на rev13: status=%d, хочу 200", code)
	}
	if len(fm.puts()) != 1 || strings.Contains(fm.puts()[0], `"health"`) {
		t.Fatalf("списочный PUT на rev13 испорчен: %q", fm.puts())
	}
}

// диапазоны валидатора политики зондов (чистая функция)
func TestValidateDnsHealthPatch(t *testing.T) {
	b := func(v bool) *bool { return &v }
	u := func(v uint) *uint { return &v }
	if err := validateDnsHealthPatch(nil); err != nil {
		t.Fatalf("nil-патч валиден: %v", err)
	}
	ok := []magitrickle.DnsHealthPatch{
		{IntervalSec: u(5)}, {IntervalSec: u(300)},
		{TimeoutSec: u(1)}, {TimeoutSec: u(10)},
		{FailCycles: u(1)}, {FailCycles: u(20)},
		{RestoreCycles: u(1)}, {RestoreCycles: u(20)},
		{RestoreGapSec: u(0)}, {RestoreGapSec: u(3600)},
		{Enabled: b(false)},
	}
	for i := range ok {
		if err := validateDnsHealthPatch(&ok[i]); err != nil {
			t.Fatalf("патч %+v должен проходить: %v", ok[i], err)
		}
	}
	bad := []magitrickle.DnsHealthPatch{
		{IntervalSec: u(4)}, {IntervalSec: u(301)},
		{TimeoutSec: u(0)}, {TimeoutSec: u(11)},
		{FailCycles: u(0)}, {FailCycles: u(21)},
		{RestoreCycles: u(0)}, {RestoreCycles: u(21)},
		{RestoreGapSec: u(3601)},
	}
	for i := range bad {
		if err := validateDnsHealthPatch(&bad[i]); err == nil {
			t.Fatalf("патч %+v должен отвергаться", bad[i])
		}
	}
}
