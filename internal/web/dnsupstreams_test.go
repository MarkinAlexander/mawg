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
	mu           sync.Mutex
	lastPuts     []string
	getCounter   int
	groups       []magitrickle.Group
	groupSeq     int      // выдача id группам без id (как живой MT)
	groupPuts    []string // тела bulk/single PUT групп
	groupsDelete []string // id удалённых групп
	zonePuts     []string // тела PUT /dns/zones
}

func (f *fakeMT) snapshot() map[string]any {
	return map[string]any{
		"available": true,
		"health": map[string]any{
			"enabled": true, "interval": "10s", "timeout": "2s",
			"failCycles": 3, "restoreCycles": 2, "restoreGap": "5m0s",
		},
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
		f.mu.Lock()
		f.lastPuts = append(f.lastPuts, string(buf[:n]))
		f.mu.Unlock()
		json.NewEncoder(w).Encode(f.snapshot())
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
	mux.HandleFunc("GET /api/v1/system/interfaces", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"interfaces": []map[string]any{
			{"id": "blackhole"},
			{"id": "wg9", "name": "ручной WG товарища"},
			{"id": "nwg0", "name": "пул proton"},
		}})
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
