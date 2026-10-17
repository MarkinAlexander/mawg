package web

import (
	"encoding/binary"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"mawg/internal/platform/fake"
	"mawg/internal/rotator"
	"mawg/internal/store"
)

// фейковый UDP-DNS, отвечающий canned-ответом с каноническим адресом
func fakeUDPDNSCanary(t *testing.T, ip string) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 4096)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			resp := append([]byte(nil), buf[:n]...)
			binary.BigEndian.PutUint16(resp[6:8], 1)
			resp = append(resp, 0xC0, 0x0C)
			resp = binary.BigEndian.AppendUint16(resp, 1)
			resp = binary.BigEndian.AppendUint16(resp, 1)
			resp = binary.BigEndian.AppendUint32(resp, 60)
			resp = binary.BigEndian.AppendUint16(resp, 4)
			resp = append(resp, net.ParseIP(ip).To4()...)
			_, _ = pc.WriteTo(resp, addr)
		}
	}()
	return pc.LocalAddr().(*net.UDPAddr).String()
}

// GET /dns/probe?kind&server - зонд одного уровня (офлайн: фейковый UDP)
func TestDNSProbeEndpoint(t *testing.T) {
	st, _ := store.Open(t.TempDir())
	fb := fake.New()
	engine := rotator.New(st, fb, nil)
	server := httptest.NewServer(New(st, engine, fb, nil, "test", nil).Handler())
	defer server.Close()

	dns := fakeUDPDNSCanary(t, "204.8.99.146")
	resp, err := http.Get(server.URL + "/api/v1/dns/probe?kind=udp&server=" + dns)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Results []struct {
			Level     string `json:"level"`
			Kind      string `json:"kind"`
			Verdict   string `json:"verdict"`
			Canonical int    `json:"canonical"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || len(body.Results) != 1 {
		t.Fatalf("status=%d results=%d", resp.StatusCode, len(body.Results))
	}
	r := body.Results[0]
	if r.Verdict != "clean" || r.Canonical != 1 || r.Kind != "udp" {
		t.Fatalf("probe result: %+v", r)
	}
}

// без параметров - полный набор (проверяем только форму ответа, без сети:
// локальных уровней на тестовой машине нет, внешние не дёргаем)
func TestDNSProbeEndpointShape(t *testing.T) {
	st, _ := store.Open(t.TempDir())
	fb := fake.New()
	engine := rotator.New(st, fb, nil)
	server := httptest.NewServer(New(st, engine, fb, nil, "test", nil).Handler())
	defer server.Close()

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(server.URL + "/api/v1/dns/probe")
	if err != nil {
		t.Skip("полный прогон требует сети/локальных уровней:", err)
	}
	defer resp.Body.Close()
	var body struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || len(body.Results) == 0 {
		t.Fatalf("status=%d results=%d", resp.StatusCode, len(body.Results))
	}
	for _, r := range body.Results {
		if _, ok := r["verdict"]; !ok {
			t.Fatalf("уровень без вердикта: %+v", r)
		}
	}
}
