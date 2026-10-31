package web

import (
	"encoding/json"
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

// «DNS самого роутера» на Keenetic (98-док): GET/PUT /dns/keenetic/dns,
// 409-confirm mt-режима, вычистка петлевых адресов из «Резервных»,
// петлезащита PUT /dns/upstreams в mt-режиме, 501 не-Кинетик.

// ndmFake - эмулятор ndm/Entware-шелла: запоминает ip name-server,
// running-config отражает их (верификация чтением - суть механики).
type ndmFake struct {
	mu        sync.Mutex
	servers   map[string]bool
	intercept bool
}

func newNdmsFake() *ndmFake { return &ndmFake{servers: map[string]bool{}} }

func (n *ndmFake) shell(script string) (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if strings.Contains(script, "MT_DNSOR") {
		return "2\n", nil
	}
	for _, seg := range strings.Split(script, " ; ") {
		seg = strings.TrimSpace(seg)
		if !strings.HasPrefix(seg, "ndmc -c '") || !strings.HasSuffix(seg, "'") {
			continue
		}
		cmd := strings.TrimSuffix(strings.TrimPrefix(seg, "ndmc -c '"), "'")
		switch {
		case cmd == "show running-config":
			var sb strings.Builder
			sb.WriteString("dns-proxy\n")
			if n.intercept {
				sb.WriteString("    intercept enable\n")
			}
			sb.WriteString("!\n")
			for a := range n.servers {
				sb.WriteString("ip name-server " + a + "\n")
			}
			sb.WriteString("ip no name-servers\n")
			return sb.String(), nil
		case cmd == "dns-proxy intercept enable":
			n.intercept = true
		case cmd == "dns-proxy no intercept enable":
			n.intercept = false
		case strings.HasPrefix(cmd, "ip name-server "):
			n.servers[strings.TrimSpace(strings.TrimPrefix(cmd, "ip name-server "))] = true
		case strings.HasPrefix(cmd, "no ip name-server "):
			delete(n.servers, strings.TrimSpace(strings.TrimPrefix(cmd, "no ip name-server ")))
		}
	}
	return "", nil
}

func (n *ndmFake) has(args string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.servers[args]
}

func keenDNSServer(t *testing.T, fm *fakeMT, ndm *ndmFake) *httptest.Server {
	t.Helper()
	magitrickle.SetShadowStorage(nil, nil)
	mt := httptest.NewServer(fm.handler())
	t.Cleanup(mt.Close)
	fb := fake.New()
	fb.NameOverride = "keenetic"
	fb.ShellFn = ndm.shell
	st, _ := store.Open(t.TempDir())
	engine := rotator.New(st, fb, nil)
	srv := httptest.NewServer(New(st, engine, fb, magitrickle.New(mt.URL), "test", nil).Handler())
	t.Cleanup(srv.Close)
	return srv
}

func keenDNSPut(t *testing.T, url, payload string) (int, map[string]any) {
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
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestKeeneticDNSNotKeenetic(t *testing.T) {
	ndm := newNdmsFake()
	fb := fake.New() // имя "fake" - ветвление keenetic не срабатывает
	fb.ShellFn = ndm.shell
	st, _ := store.Open(t.TempDir())
	engine := rotator.New(st, fb, nil)
	srv := httptest.NewServer(New(st, engine, fb, nil, "test", nil).Handler())
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/api/v1/dns/keenetic/dns")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("GET не-Кинетик: status=%d, хочу 501", resp.StatusCode)
	}
	if code, _ := keenDNSPut(t, srv.URL+"/api/v1/dns/keenetic/dns", `{"mode":"spare","spare":"8.8.8.8"}`); code != http.StatusNotImplemented {
		t.Fatalf("PUT не-Кинетик: status=%d, хочу 501", code)
	}
}

func TestKeeneticDNSSpare(t *testing.T) {
	ndm := newNdmsFake()
	srv := keenDNSServer(t, &fakeMT{version: "0.8.2-rev16-mawg-dev"}, ndm)

	// старт: режим пуст
	resp, err := http.Get(srv.URL + "/api/v1/dns/keenetic/dns")
	if err != nil {
		t.Fatal(err)
	}
	var start struct {
		Mode string `json:"mode"`
		Live bool   `json:"live"`
	}
	json.NewDecoder(resp.Body).Decode(&start)
	resp.Body.Close()
	if start.Mode != "" || start.Live {
		t.Fatalf("стартовое состояние: %+v", start)
	}

	// страховочный сервер с привязкой к подключению
	code, body := keenDNSPut(t, srv.URL+"/api/v1/dns/keenetic/dns", `{"mode":"spare","spare":"8.8.8.8","iface":"ISP"}`)
	if code != http.StatusOK {
		t.Fatalf("PUT spare: status=%d body=%v", code, body)
	}
	st := body["state"].(map[string]any)
	if st["mode"] != "spare" || st["applied"] != `8.8.8.8 "" on ISP` || st["live"] != true {
		t.Fatalf("стейт после spare: %v", st)
	}
	if !ndm.has(`8.8.8.8 "" on ISP`) {
		t.Fatalf("запись не встала: %v", ndm.servers)
	}

	// GET подтверждает живость чтением running-config
	resp, err = http.Get(srv.URL + "/api/v1/dns/keenetic/dns")
	if err != nil {
		t.Fatal(err)
	}
	var after struct {
		Mode    string `json:"mode"`
		Applied string `json:"applied"`
		Live    bool   `json:"live"`
	}
	json.NewDecoder(resp.Body).Decode(&after)
	resp.Body.Close()
	if after.Mode != "spare" || after.Applied != `8.8.8.8 "" on ISP` || !after.Live {
		t.Fatalf("GET после spare: %+v", after)
	}

	// петлевой страховочный сервер - честный 400
	if code, _ := keenDNSPut(t, srv.URL+"/api/v1/dns/keenetic/dns", `{"mode":"spare","spare":"127.0.0.1"}`); code != http.StatusBadRequest {
		t.Fatalf("loopback-spare: status=%d, хочу 400", code)
	}

	// off снимает ровно нашу запись
	code, body = keenDNSPut(t, srv.URL+"/api/v1/dns/keenetic/dns", `{"mode":"off"}`)
	if code != http.StatusOK {
		t.Fatalf("PUT off: status=%d body=%v", code, body)
	}
	st = body["state"].(map[string]any)
	if st["mode"] != "off" || st["applied"] != nil || st["live"] != false {
		t.Fatalf("стейт после off: %v", st)
	}
	if ndm.has(`8.8.8.8 "" on ISP`) {
		t.Fatal("запись не снята")
	}
}

func TestKeeneticDNSMTConfirmAndLoopClean(t *testing.T) {
	ndm := newNdmsFake()
	fm := &fakeMT{version: "0.8.2-rev16-mawg-dev"}
	srv := keenDNSServer(t, fm, ndm)

	// mt без confirm - 409 (честное предупреждение)
	code, body := keenDNSPut(t, srv.URL+"/api/v1/dns/keenetic/dns", `{"mode":"mt"}`)
	if code != http.StatusConflict {
		t.Fatalf("mt без confirm: status=%d body=%v", code, body)
	}
	if body["needConfirm"] != true {
		t.Fatalf("нет needConfirm: %v", body)
	}
	if ndm.has("127.0.0.1 3553") {
		t.Fatal("запись встала без confirm")
	}

	// mt с confirm: запись ставится
	code, body = keenDNSPut(t, srv.URL+"/api/v1/dns/keenetic/dns", `{"mode":"mt","confirm":true}`)
	if code != http.StatusOK {
		t.Fatalf("mt с confirm: status=%d body=%v", code, body)
	}
	st := body["state"].(map[string]any)
	if st["applied"] != "127.0.0.1 3553" || st["live"] != true {
		t.Fatalf("стейт после mt: %v", st)
	}

	// петлезащита PUT /dns/upstreams теперь действует и на Кинетике
	code, body = keenDNSPut(t, srv.URL+"/api/v1/dns/upstreams", `{"upstreams":["8.8.8.8:53","127.0.0.1:53"]}`)
	if code != http.StatusConflict {
		t.Fatalf("петля в mt-режиме: status=%d body=%v", code, body)
	}
	if code, _ := keenDNSPut(t, srv.URL+"/api/v1/dns/upstreams", `{"upstreams":["8.8.8.8:53","127.0.0.1:53"],"confirm":true}`); code != http.StatusOK {
		t.Fatalf("петля с confirm: status=%d", code)
	}
}

func TestKeeneticDNSMTCleansUpstreams(t *testing.T) {
	ndm := newNdmsFake()
	fm := &fakeMT{version: "0.8.2-rev16-mawg-dev", loopUpstream: true}
	srv := keenDNSServer(t, fm, ndm)

	// mt с confirm: 127.0.0.1:53 вычищен из «Резервных» до включения
	code, body := keenDNSPut(t, srv.URL+"/api/v1/dns/keenetic/dns", `{"mode":"mt","confirm":true}`)
	if code != http.StatusOK {
		t.Fatalf("mt с петлёй в списке: status=%d body=%v", code, body)
	}
	puts := fm.puts()
	if len(puts) != 1 || strings.Contains(puts[0], "127.0.0.1:53") || !strings.Contains(puts[0], "8.8.8.8:53") {
		t.Fatalf("список MT не вычищен: %q", puts)
	}
	if !ndm.has("127.0.0.1 3553") {
		t.Fatal("запись ndm не встала")
	}
}

func TestKeeneticDNSMTDeadGate(t *testing.T) {
	ndm := newNdmsFake()
	// MT не поднимаем (mtClient смотрит в пустоту) - mt-режим честно 502
	srv := keenDNSServer(t, &fakeMT{version: "0.8.2-rev16-mawg-dev"}, ndm)
	// подсунуть недоступный MT: пересобрать сервер без MT-фейка
	fb := fake.New()
	fb.NameOverride = "keenetic"
	fb.ShellFn = ndm.shell
	st, _ := store.Open(t.TempDir())
	engine := rotator.New(st, fb, nil)
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer dead.Close()
	srv2 := httptest.NewServer(New(st, engine, fb, magitrickle.New(dead.URL), "test", nil).Handler())
	t.Cleanup(srv2.Close)
	_ = srv

	code, _ := keenDNSPut(t, srv2.URL+"/api/v1/dns/keenetic/dns", `{"mode":"mt","confirm":true}`)
	if code != http.StatusBadGateway {
		t.Fatalf("mt при мёртвом MT: status=%d, хочу 502", code)
	}
	if ndm.has("127.0.0.1 3553") {
		t.Fatal("запись встала при мёртвом MT")
	}
}

func TestKeeneticInterceptToggle(t *testing.T) {
	ndm := newNdmsFake()
	srv := keenDNSServer(t, &fakeMT{version: "0.8.2-rev16-mawg-dev"}, ndm)

	// без confirm - 409
	code, body := keenDNSPut(t, srv.URL+"/api/v1/dns/keenetic/dns", `{"intercept":true}`)
	if code != http.StatusConflict || body["needConfirm"] != true {
		t.Fatalf("intercept без confirm: status=%d body=%v", code, body)
	}
	// с confirm - включён, верифицирован чтением
	code, body = keenDNSPut(t, srv.URL+"/api/v1/dns/keenetic/dns", `{"intercept":true,"confirm":true}`)
	if code != http.StatusOK {
		t.Fatalf("intercept с confirm: status=%d body=%v", code, body)
	}
	st := body["state"].(map[string]any)
	if st["intercept"] != true {
		t.Fatalf("стейт после включения: %v", st)
	}
	ndm.mu.Lock()
	on := ndm.intercept
	ndm.mu.Unlock()
	if !on {
		t.Fatal("в ndm-фейке не включён")
	}
	// выключение - безопасно, без confirm
	code, body = keenDNSPut(t, srv.URL+"/api/v1/dns/keenetic/dns", `{"intercept":false}`)
	if code != http.StatusOK {
		t.Fatalf("intercept выключение: status=%d body=%v", code, body)
	}
	st = body["state"].(map[string]any)
	if st["intercept"] != false {
		t.Fatalf("стейт после выключения: %v", st)
	}
}

func TestKeeneticInterceptMTLoopGuard(t *testing.T) {
	ndm := newNdmsFake()
	// снапшот fakeMT содержит простой 8.8.8.8:53 - с перехватом+mt это петля
	srv := keenDNSServer(t, &fakeMT{version: "0.8.2-rev16-mawg-dev"}, ndm)
	if code, body := keenDNSPut(t, srv.URL+"/api/v1/dns/keenetic/dns", `{"mode":"mt","confirm":true}`); code != http.StatusOK {
		t.Fatalf("mt: status=%d body=%v", code, body)
	}
	code, body := keenDNSPut(t, srv.URL+"/api/v1/dns/keenetic/dns", `{"intercept":true,"confirm":true}`)
	if code != http.StatusBadRequest {
		t.Fatalf("петля-гард: status=%d body=%v", code, body)
	}
	if !strings.Contains(body["error"].(string), "петлю") {
		t.Fatalf("текст гарда: %v", body)
	}
}
