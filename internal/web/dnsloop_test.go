package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mawg/internal/magitrickle"
	"mawg/internal/platform/keenetic"
	"mawg/internal/rotator"
	"mawg/internal/store"
)

// dnsmasqLoopHost - петля только по адресам: udp:53 на loopback/своём IP.
func TestDnsmasqLoopHost(t *testing.T) {
	own := []string{"192.168.0.42", "10.8.0.1"}
	cases := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1", true},       // порт по умолчанию 53
		{"127.0.0.1:53", true},    // живой кейс Keenetic-петли на OpenWrt
		{"[::1]:53", true},        // v6-loopback
		{"192.168.0.42:53", true}, // собственный IP роутера
		{"192.168.0.42", true},
		{"127.0.0.1:5353", false},  // не-53 порт - не dnsmasq
		{"tls://127.0.0.1", false}, // шифрованный транспорт - не udp:53
		{"https://127.0.0.1/dns-query", false},
		{"8.8.8.8:53", false},
		{"77.88.8.8", false},
		{"dns.google", false},   // имя: петля только по адресам
		{"localhost", false},    // имя (резолвится мимо dnsmasq)
		{"", false},
		{"192.168.0.99:53", false}, // чужой адрес в LAN
	}
	for _, c := range cases {
		if got := dnsmasqLoopHost(c.addr, own); got != c.want {
			t.Errorf("dnsmasqLoopHost(%q) = %v, хочу %v", c.addr, got, c.want)
		}
	}
}

// PUT /dns/upstreams при dnsmasq.inMT: адрес dnsmasq в списке - 409 со
// списком рискованных; с confirm - проходит до форка (предупреждение +
// подтверждение, слово владельца 2026-10-18)
func TestDNSUpstreamsLoopGuard(t *testing.T) {
	fm := &fakeMT{version: "0.8.2-rev8-mawgfix1"}
	mt := httptest.NewServer(fm.handler())
	defer mt.Close()
	srv := dnsTestServerMT(t, mt.URL) // ShellFn: dnsmasq -> 127.0.0.1#3553 (inMT)

	put := func(body string) (int, map[string]any) {
		req, err := http.NewRequest(http.MethodPut, srv.URL+"/api/v1/dns/upstreams", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var m map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, m
	}

	code, body := put(`{"upstreams":["127.0.0.1:53","8.8.8.8"]}`)
	if code != http.StatusConflict {
		t.Fatalf("жду 409 на петлю, получил %d: %v", code, body)
	}
	risky, _ := body["loopRisk"].([]any)
	if len(risky) != 1 || risky[0] != "127.0.0.1:53" {
		t.Fatalf("loopRisk = %v, жду [127.0.0.1:53]", risky)
	}
	if len(fm.puts()) != 0 {
		t.Fatalf("PUT в форк не должен был уйти: %v", fm.puts())
	}

	// валидный список проходит как раньше
	code, body = put(`{"upstreams":["8.8.8.8"]}`)
	if code != http.StatusOK || body["ok"] != true {
		t.Fatalf("валидный список: %d %v", code, body)
	}

	// подтверждённый риск проходит до форка
	code, _ = put(`{"upstreams":["127.0.0.1:53","8.8.8.8"],"confirm":true}`)
	if code != http.StatusOK {
		t.Fatalf("подтверждённая петля: %d", code)
	}
	puts := fm.puts()
	if len(puts) != 2 || !strings.Contains(puts[len(puts)-1], `"127.0.0.1:53"`) {
		t.Fatalf("подтверждённый PUT не дошёл до форка: %v", puts)
	}
}

// на Keenetic 127.0.0.1:53 легален (системный резолвер, прод владельца
// живёт на нём) - правило платформенное, гард не срабатывает
func TestDNSUpstreamsLoopKeeneticExempt(t *testing.T) {
	fm := &fakeMT{version: "0.8.2-rev8-mawgfix1"}
	mt := httptest.NewServer(fm.handler())
	defer mt.Close()
	fb := keenetic.New()
	st, _ := store.Open(t.TempDir())
	engine := rotator.New(st, fb, nil)
	ts := httptest.NewServer(New(st, engine, fb, magitrickle.New(mt.URL), "test", nil).Handler())
	defer ts.Close()

	req, err := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/dns/upstreams", strings.NewReader(`{"upstreams":["127.0.0.1:53"]}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("на Кинетике 127.0.0.1:53 легален: %d", resp.StatusCode)
	}
}
