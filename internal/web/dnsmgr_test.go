package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mawg/internal/platform/fake"
	"mawg/internal/rotator"
	"mawg/internal/store"
)

// canned-выводы шелла для состояния DNS (fake.ShellRunner)
func dnsShellStub(serverList string) func(string) (string, error) {
	return func(script string) (string, error) {
		switch {
		case strings.Contains(script, "dhcp.@dnsmasq[0].server") && !strings.Contains(script, "mawg_server_bak"):
			return serverList, nil
		case strings.Contains(script, "mawg_server_bak"):
			return "", nil
		case strings.Contains(script, "noresolv"):
			return "0\n", nil
		case strings.Contains(script, "cachesize"):
			return "1000\n", nil
		case strings.Contains(script, "MT_DNSOR"):
			return "2\n", nil
		}
		return "", nil
	}
}

func dnsTestServer(t *testing.T, fb *fake.Fake) *httptest.Server {
	t.Helper()
	st, _ := store.Open(t.TempDir())
	engine := rotator.New(st, fb, nil)
	srv := httptest.NewServer(New(st, engine, fb, nil, "test", nil).Handler())
	t.Cleanup(srv.Close)
	return srv
}

// GET /dns/state: состояние из canned-шелла (dnsmasq в MT, remap53 жив)
func TestDNSStateEndpoint(t *testing.T) {
	fb := fake.New()
	fb.ShellFn = dnsShellStub("dhcp.@dnsmasq[0].server='127.0.0.1#3553'\n")
	srv := dnsTestServer(t, fb)

	resp, err := http.Get(srv.URL + "/api/v1/dns/state")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var body struct {
		Dnsmasq struct {
			Servers []string `json:"servers"`
			InMT    bool     `json:"inMT"`
			Strict  bool     `json:"strict"`
		} `json:"dnsmasq"`
		MT struct {
			Remap53   bool     `json:"remap53"`
			Upstreams []string `json:"upstreams"`
		} `json:"mt"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Dnsmasq.InMT || body.Dnsmasq.Strict || len(body.Dnsmasq.Servers) != 1 {
		t.Fatalf("dnsmasq: %+v", body.Dnsmasq)
	}
	if !body.MT.Remap53 {
		t.Fatalf("remap53: %+v", body.MT)
	}
}

// POST /dns/dnsmasq: mode=mt выполняет uci-batch и возвращает новое состояние
func TestDNSDnsmasqEndpoint(t *testing.T) {
	fb := fake.New()
	var applied string
	fb.ShellFn = func(script string) (string, error) {
		if applied == "" && strings.Contains(script, "uci -q batch") {
			applied = script
		}
		return dnsShellStub("dhcp.@dnsmasq[0].server='192.168.0.1'\n")(script)
	}
	srv := dnsTestServer(t, fb)

	resp, err := http.Post(srv.URL+"/api/v1/dns/dnsmasq", "application/json", strings.NewReader(`{"mode":"mt"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if !strings.Contains(applied, "add_list dhcp.@dnsmasq[0].server='127.0.0.1#3553'") ||
		!strings.Contains(applied, "mawg_server_bak='192.168.0.1'") {
		t.Fatalf("batch не содержит нужных строк: %q", applied)
	}
	var body struct {
		OK    bool `json:"ok"`
		State struct {
			Dnsmasq struct {
				Servers []string `json:"servers"`
			} `json:"dnsmasq"`
		} `json:"state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || len(body.State.Dnsmasq.Servers) == 0 {
		t.Fatalf("ответ: %+v", body)
	}

	// кривой mode - 400
	resp2, err := http.Post(srv.URL+"/api/v1/dns/dnsmasq", "application/json", strings.NewReader(`{"mode":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("кривой mode: status=%d", resp2.StatusCode)
	}
}
