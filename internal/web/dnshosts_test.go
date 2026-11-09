package web

// Карточка «Локальные записи» (115-док): GET отдаёт записи роутера
// (/etc/hosts OpenWrt + файл mawg) и свой список; PUT применяет свой
// список (OpenWrt: файл /etc/hosts.d/mawg + restart dnsmasq) и пишет стор.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mawg/internal/magitrickle"
	"mawg/internal/platform/fake"
	"mawg/internal/rotator"
	"mawg/internal/store"
)

func dnsHostsTestEnv(t *testing.T) (*httptest.Server, *store.Store, *[]string) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var scripts []string
	fb := fake.New()
	fb.ShellFn = func(script string) (string, error) {
		scripts = append(scripts, script)
		switch {
		case strings.Contains(script, "cat /etc/hosts.d/mawg"):
			return "10.0.0.5 dzen.test\n", nil
		case strings.Contains(script, "cat /etc/hosts"):
			return "127.0.0.1 localhost\n192.168.1.10 nas.home\n", nil
		}
		return "", nil
	}
	magitrickle.SetShadowStorage(nil, nil)
	e := rotator.New(st, fb, magitrickle.New("http://127.0.0.1:1"))
	srv := New(st, e, fb, magitrickle.New("http://127.0.0.1:1"), "test", nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, st, &scripts
}

func TestDNSHostsGetAndSet(t *testing.T) {
	ts, st, scripts := dnsHostsTestEnv(t)

	// GET: записи роутера с источниками + свой список
	got := httpGetJSON(t, ts.URL+"/api/v1/dns/hosts")
	routerJSON, _ := got["router"].([]any)
	if len(routerJSON) != 3 {
		t.Fatalf("router записей %d, want 3 (localhost, nas.home, dzen.test): %v", len(routerJSON), got["router"])
	}
	own0, _ := got["own"].([]any)
	if len(own0) != 0 {
		t.Fatalf("свой список изначально пуст: %v", own0)
	}
	if got["v6Capable"] != true {
		t.Fatalf("OpenWrt: v6Capable true, got %v", got["v6Capable"])
	}

	// PUT своего списка: файл mawg + привязка addnhosts + restart dnsmasq
	resp, err := httpPut(ts.URL+"/api/v1/dns/hosts",
		`{"hosts":[{"id":"h1","name":"Dzen.Test.","ipv4":"10.0.0.9"},{"id":"h2","name":"nas6.home","ipv6":"fd00::9"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT hosts: status %d, body %s", resp.StatusCode, body)
	}
	joined := strings.Join(*scripts, "\n")
	if !strings.Contains(joined, "cat > /etc/hosts.d/mawg") ||
		!strings.Contains(joined, "10.0.0.9 dzen.test") ||
		!strings.Contains(joined, "fd00::9 nas6.home") {
		t.Fatalf("файл mawg не записан: %s", joined)
	}
	if !strings.Contains(joined, "addnhosts='/etc/hosts.d/mawg'") || !strings.Contains(joined, "/etc/init.d/dnsmasq restart") {
		t.Fatalf("addnhosts не привязан: %s", joined)
	}
	saved := st.DnsHosts()
	if len(saved) != 2 || saved[0].Name != "dzen.test" || saved[0].IPv4 != "10.0.0.9" || saved[1].IPv6 != "fd00::9" {
		t.Fatalf("стор: %+v", saved)
	}

	// GET после PUT: own=true у своей записи в выборке роутера
	got = httpGetJSON(t, ts.URL+"/api/v1/dns/hosts")
	routerJSON, _ = got["router"].([]any)
	var dzenSeen, dzenOwn bool
	for _, r := range routerJSON {
		m, _ := r.(map[string]any)
		if m["name"] == "dzen.test" {
			dzenSeen = true
			dzenOwn, _ = m["own"].(bool)
		}
	}
	if !dzenSeen || !dzenOwn {
		t.Fatalf("dzen.test не помечен своим: seen=%v own=%v", dzenSeen, dzenOwn)
	}
	own1, _ := got["own"].([]any)
	if len(own1) != 2 {
		t.Fatalf("свой список после PUT: %d, want 2", len(own1))
	}

	// валидация: кривой IP - 400
	resp, err = httpPut(ts.URL+"/api/v1/dns/hosts", `{"hosts":[{"name":"x.test","ipv4":"не ip"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	body = readBody(resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "кривой IP") {
		t.Fatalf("валидация: status %d, body %s", resp.StatusCode, body)
	}
}
