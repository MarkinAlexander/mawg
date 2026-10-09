package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"mawg/internal/platform/fake"
	"mawg/internal/rotator"
	"mawg/internal/store"
	"mawg/internal/wgconf"
)

// состав пула: WG-конфиги с прототипами (awg3/awg/wg) и прокси-узлы
// с транспортами; для карточки - protos-сводка
func TestPoolMembersAndProtos(t *testing.T) {
	st, _ := store.Open(t.TempDir())
	fb := premiumOpenWrtBackend{fake.New()}
	engine := rotator.New(st, fb, nil)
	server := httptest.NewServer(New(st, engine, fb, nil, "test", nil).Handler())
	defer server.Close()

	if _, err := st.CreatePool("mixed", store.PoolSettings{Platform: store.PlatformOpenwrt, EngineMode: "singbox", OpenwrtProto: "amneziawg"}); err != nil {
		t.Fatal(err)
	}
	awg3, err := wgconf.Parse([]byte("[Interface]\nPrivateKey = uJd2xK5t0mFvqWz+YbN8cLrA3eHh7oPqQ1wZsX4dC6k=\nAddress = 10.9.0.2/32\nJc = 4\nHeaderProtectionKey = AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n[Peer]\nPublicKey = ll+gozj6agFD5WRhXQUFna6LLoC4zxi7eka5RMUfKE8=\nEndpoint = free.example.net:51820\nAllowedIPs = 0.0.0.0/0\n"))
	if err != nil {
		t.Fatal(err)
	}
	awg, err := wgconf.Parse([]byte("[Interface]\nPrivateKey = uJd2xK5t0mFvqWz+YbN8cLrA3eHh7oPqQ1wZsX4dC6k=\nAddress = 10.9.1.2/32\nJc = 4\n[Peer]\nPublicKey = ll+gozj6agFD5WRhXQUFna6LLoC4zxi7eka5RMUfKE8=\nEndpoint = awg.example.net:51820\nAllowedIPs = 0.0.0.0/0\n"))
	if err != nil {
		t.Fatal(err)
	}
	awg3Raw := "[Interface]\nPrivateKey = uJd2xK5t0mFvqWz+YbN8cLrA3eHh7oPqQ1wZsX4dC6k=\nAddress = 10.9.0.2/32\nJc = 4\nHeaderProtectionKey = AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n[Peer]\nPublicKey = ll+gozj6agFD5WRhXQUFna6LLoC4zxi7eka5RMUfKE8=\nEndpoint = free.example.net:51820\nAllowedIPs = 0.0.0.0/0\n"
	awgRaw := "[Interface]\nPrivateKey = uJd2xK5t0mFvqWz+YbN8cLrA3eHh7oPqQ1wZsX4dC6k=\nAddress = 10.9.1.2/32\nJc = 4\n[Peer]\nPublicKey = ll+gozj6agFD5WRhXQUFna6LLoC4zxi7eka5RMUfKE8=\nEndpoint = awg.example.net:51820\nAllowedIPs = 0.0.0.0/0\n"
	ncs := []wgconf.NamedConfig{
		{OriginalName: "free", Raw: []byte(awg3Raw), Config: awg3},
		{OriginalName: "awg-two", Raw: []byte(awgRaw), Config: awg},
	}
	if _, _, err := st.AddConfigs("mixed", ncs); err != nil {
		t.Fatal(err)
	}
	nodes := []map[string]any{
		{"type": "vless", "tag": "ella", "host": "ella.example.net", "port": 443, "transport": "ws"},
		{"type": "vless", "tag": "fin", "host": "fin.example.net", "port": 443, "transport": "grpc"},
	}
	nb, _ := json.Marshal(nodes)
	os.WriteFile(filepath.Join(st.PoolDir("mixed"), "nodes.json"), nb, 0o600)

	resp, err := http.Get(server.URL + "/api/v1/pools/mixed/members")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Members []memberView `json:"members"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, m := range out.Members {
		joined += m.Proto + "|" + m.Detail + "|" + m.Endpoint + " "
	}
	for _, want := range []string{"AmneziaWG 3.x||free.example.net:51820", "AmneziaWG||awg.example.net:51820"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("в составе нет %q: %s", want, joined)
		}
	}

	// protos-сводка в статусе
	sresp, err := http.Get(server.URL + "/api/v1/status")
	if err != nil {
		t.Fatal(err)
	}
	defer sresp.Body.Close()
	var status struct {
		Pools []struct {
			Name   string       `json:"name"`
			Protos []protoBadge `json:"protos"`
		} `json:"pools"`
	}
	if err := json.NewDecoder(sresp.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	badges := ""
	for _, p := range status.Pools {
		if p.Name != "mixed" {
			continue
		}
		for _, b := range p.Protos {
			badges += b.Label + "x" + strconv.Itoa(b.Count) + " "
		}
	}
	for _, want := range []string{"AmneziaWG 3.xx1", "AmneziaWGx1"} {
		if !strings.Contains(badges, want) {
			t.Fatalf("в protos нет %q: %s", want, badges)
		}
	}
}
