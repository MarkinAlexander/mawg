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
	mt := httptest.NewServer((&fakeMT{version: "0.8.2-rev17-mawg-dev"}).handler())
	t.Cleanup(mt.Close)
	fb := fake.New()
	fb.NameOverride = "keenetic"
	fb.ShellFn = ndm.shell
	st, _ := store.Open(t.TempDir())
	engine := rotator.New(st, fb, nil)
	srv := httptest.NewServer(New(st, engine, fb, magitrickle.New(mt.URL), "test", nil).Handler())
	t.Cleanup(srv.Close)

	// режимы записи в ndm выведены: spare - честный 400
	code, body := keenDNSPut(t, srv.URL+"/api/v1/dns/keenetic/dns", `{"mode":"spare","spare":"8.8.8.8","iface":"ISP"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("PUT spare: status=%d body=%v", code, body)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "выведена") {
		t.Fatalf("текст без объяснения: %v", body)
	}
	if ndm.has(`8.8.8.8 "" on ISP`) {
		t.Fatal("запись встала при снятом режиме")
	}

	// легаси: в хранилище осталась старая запись - первый GET снимает её
	// из ndm и возвращает панель на дефолт
	if err := st.SetKeeneticDNS(store.KeeneticDNS{Mode: "spare", Spare: "8.8.8.8", Iface: "ISP", Applied: `8.8.8.8 "" on ISP`}); err != nil {
		t.Fatal(err)
	}
	ndm.mu.Lock()
	ndm.servers[`8.8.8.8 "" on ISP`] = true
	ndm.mu.Unlock()
	resp, err := http.Get(srv.URL + "/api/v1/dns/keenetic/dns")
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
	if after.Mode != "" || after.Applied != "" || after.Live {
		t.Fatalf("легаси-автосброс: %+v", after)
	}
	if ndm.has(`8.8.8.8 "" on ISP`) {
		t.Fatal("легаси-запись не снята из ndm")
	}

	// петлевой страховочный сервер тоже просто 400 (гвард раньше)
	if code, _ := keenDNSPut(t, srv.URL+"/api/v1/dns/keenetic/dns", `{"mode":"spare","spare":"127.0.0.1"}`); code != http.StatusBadRequest {
		t.Fatalf("loopback-spare: status=%d, хочу 400", code)
	}
}

func TestKeeneticDNSMTRemoved(t *testing.T) {
	ndm := newNdmsFake()
	fm := &fakeMT{version: "0.8.2-rev17-mawg-dev"}
	srv := keenDNSServer(t, fm, ndm)

	// режим mt снят: 400 с честным текстом, без confirm-танцев и без
	// записи в ndm (инцидент 01.11: ndm не принимает 127.0.0.1 как
	// апстрим, портов нет - MT:3553 через name-server невыразим)
	code, body := keenDNSPut(t, srv.URL+"/api/v1/dns/keenetic/dns", `{"mode":"mt","confirm":true}`)
	if code != http.StatusBadRequest {
		t.Fatalf("mt: status=%d body=%v", code, body)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "снят") {
		t.Fatalf("текст ошибки без объяснения: %v", body)
	}
	if ndm.has("127.0.0.1 3553") {
		t.Fatal("запись ndm встала при снятом режиме")
	}

	// петлезащита upstreams на Кинетике больше не привязана к mt:
	// ndm не кормится от MT - loop-адрес в «Резервных» не петля
	code, _ = keenDNSPut(t, srv.URL+"/api/v1/dns/upstreams", `{"upstreams":["8.8.8.8:53","127.0.0.1:53"]}`)
	if code != http.StatusOK {
		t.Fatalf("loop-адреса на Keenetic без mt-режима легальны: status=%d", code)
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

func TestKeeneticInterceptWithoutMTMode(t *testing.T) {
	ndm := newNdmsFake()
	srv := keenDNSServer(t, &fakeMT{version: "0.8.2-rev17-mawg-dev"}, ndm)
	// режим mt снят - перехват ndm больше не связан с «Резервными» MT:
	// простой :53 в списке петли не даёт (ndm не направляется в MT)
	code, body := keenDNSPut(t, srv.URL+"/api/v1/dns/keenetic/dns", `{"intercept":true,"confirm":true}`)
	if code != http.StatusOK {
		t.Fatalf("intercept с confirm: status=%d body=%v", code, body)
	}
	st := body["state"].(map[string]any)
	if st["intercept"] != true {
		t.Fatalf("перехват не включился: %v", st)
	}
	// и обратно выключается
	code, _ = keenDNSPut(t, srv.URL+"/api/v1/dns/keenetic/dns", `{"intercept":false,"confirm":true}`)
	if code != http.StatusOK {
		t.Fatalf("intercept off: status=%d", code)
	}
}
