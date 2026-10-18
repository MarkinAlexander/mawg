package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"mawg/internal/dnsprobe"
	"mawg/internal/store"
)

// convertProbeServer: канонизация адресов по типу, схема обязана
// совпадать с типом (слово владельца: «чётко понимать что куда
// добавляет»)
func TestConvertProbeServer(t *testing.T) {
	ok := []struct {
		in   store.DnsProbeServer
		want dnsprobe.Level
	}{
		{store.DnsProbeServer{Kind: "udp", Server: "8.8.8.8"},
			dnsprobe.Level{Level: "8.8.8.8:53", Kind: "udp", Server: "8.8.8.8:53"}},
		{store.DnsProbeServer{Name: "домашний", Kind: "udp", Server: "192.0.2.1:5353"},
			dnsprobe.Level{Level: "домашний", Kind: "udp", Server: "192.0.2.1:5353"}},
		{store.DnsProbeServer{Kind: "dot", Server: "tls://dns.adguard.com"},
			dnsprobe.Level{Level: "DoT dns.adguard.com", Kind: "dot", Server: "dns.adguard.com:853", SNI: "dns.adguard.com"}},
		{store.DnsProbeServer{Name: "мой DoH", Kind: "doh", Server: "https://dns.google"},
			dnsprobe.Level{Level: "мой DoH", Kind: "doh", Server: "https://dns.google:443/dns-query", SNI: "dns.google"}},
	}
	for _, c := range ok {
		got, err := convertProbeServer(c.in)
		if err != nil {
			t.Fatalf("%+v: %v", c.in, err)
		}
		if got.Level != c.want.Level || got.Kind != c.want.Kind || got.Server != c.want.Server || got.SNI != c.want.SNI {
			t.Fatalf("%+v: got %+v, want %+v", c.in, got, c.want)
		}
	}
	bad := []store.DnsProbeServer{
		{Name: "x", Kind: "udp", Server: "https://dns.google"},   // схема не к udp
		{Name: "x", Kind: "dot", Server: "8.8.8.8"},              // без tls://
		{Name: "x", Kind: "doh", Server: "tls://8.8.8.8"},        // не https://
		{Name: "x", Kind: "smtp", Server: "8.8.8.8"},             // неизвестный тип
		{Name: "x", Kind: "udp", Server: ""},                     // пустой адрес
	}
	for _, in := range bad {
		if _, err := convertProbeServer(in); err == nil {
			t.Errorf("жду отказ на %+v", in)
		}
	}
}

// PUT /dns/probe/servers: сохранение с валидацией, GET отдаёт как есть;
// кривой сервер - 400 без сохранения
func TestDnsProbeServersAPI(t *testing.T) {
	srv, _ := policyServer(t)

	put := func(body string) (int, map[string]any) {
		req, err := http.NewRequest(http.MethodPut, srv.URL+"/api/v1/dns/probe/servers", strings.NewReader(body))
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

	code, body := put(`{"servers":[{"name":"домашний","kind":"udp","server":"192.0.2.1:5353"},{"kind":"doh","server":"https://dns.google"}]}`)
	if code != http.StatusOK {
		t.Fatalf("PUT: %d %v", code, body)
	}
	servers, _ := body["servers"].([]any)
	if len(servers) != 2 {
		t.Fatalf("servers: %v", body)
	}

	resp, err := http.Get(srv.URL + "/api/v1/dns/probe/servers")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got struct {
		Servers []store.DnsProbeServer `json:"servers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Servers) != 2 || got.Servers[0].Name != "домашний" || got.Servers[1].Kind != "doh" {
		t.Fatalf("GET вернул %+v", got.Servers)
	}

	// кривой тип - отказ, список не портится
	code, _ = put(`{"servers":[{"kind":"smtp","server":"8.8.8.8"}]}`)
	if code != http.StatusBadRequest {
		t.Fatalf("кривой тип: %d", code)
	}
	resp2, err := http.Get(srv.URL + "/api/v1/dns/probe/servers")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if err := json.NewDecoder(resp2.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Servers) != 2 {
		t.Fatalf("после отказа список изменился: %+v", got.Servers)
	}

	// пустой список - очистка
	code, _ = put(`{"servers":[]}`)
	if code != http.StatusOK {
		t.Fatalf("очистка: %d", code)
	}
}
