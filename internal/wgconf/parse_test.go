package wgconf

import (
	"os"
	"strings"
	"testing"
)

func loadConf(t *testing.T, name string) Config {
	t.Helper()
	data, err := os.ReadFile("../../testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Parse(data)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return cfg
}

func TestParsePlainWG(t *testing.T) {
	cfg := loadConf(t, "plain-wg.conf")
	if cfg.AWG.Present() {
		t.Fatal("plain config must not have awg params")
	}
	if cfg.FirstIPv4() != "10.2.0.2/32" {
		t.Fatalf("FirstIPv4 = %q", cfg.FirstIPv4())
	}
	if cfg.Endpoint() != "se-01.protonvpn.net:51820" {
		t.Fatalf("Endpoint = %q", cfg.Endpoint())
	}
	if cfg.Peer.PersistentKeepalive != 0 {
		t.Fatalf("keepalive = %d", cfg.Peer.PersistentKeepalive)
	}
}

func TestParseAWGClassic(t *testing.T) {
	cfg := loadConf(t, "awg-proton.conf")
	if !cfg.AWG.Present() {
		t.Fatal("awg params not parsed")
	}
	if cfg.AWG.HasExtended() {
		t.Fatal("no extended params expected")
	}
	got := cfg.AWG.AscArgs()
	want := []string{"4", "40", "70", "15", "16", "1", "2", "3", "4"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("classic[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if cfg.Peer.PresharedKey == "" {
		t.Fatal("preshared key lost")
	}
	if len(cfg.DNS) != 1 || cfg.DNS[0] != "10.2.0.1" {
		t.Fatalf("DNS = %v", cfg.DNS)
	}
}

func TestParseAWGExtended(t *testing.T) {
	cfg := loadConf(t, "awg-warp-extended.conf")
	if !cfg.AWG.HasExtended() {
		t.Fatal("extended params not parsed")
	}
	got := cfg.AWG.AscArgs()
	want := []string{"3", "50", "1000", "100", "110", "1", "96", "99", "99", "30", "39", "\"15\"", "\"16\"", "\"17\"", "\"18\"", "\"19\""}
	if len(got) != len(want) {
		t.Fatalf("AscArgs len = %d (%v)", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("AscArgs[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if cfg.MTU != 1280 {
		t.Fatalf("MTU = %d", cfg.MTU)
	}
	if len(cfg.Addresses) != 2 || cfg.FirstIPv4() != "172.16.0.2/32" {
		t.Fatalf("Addresses = %v", cfg.Addresses)
	}
	if cfg.Endpoint() != "engage.cloudflareclient.com:2408" {
		t.Fatalf("Endpoint = %q", cfg.Endpoint())
	}
}

func TestParseInitPacketBlobs(t *testing.T) {
	body := "[Interface]\nPrivateKey = QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE=\nAddress = 10.2.0.2/32\n" +
		"Jc = 3\nJmin = 1\nJmax = 3\nS1 = 0\nS2 = 0\nS3 = 0\nS4 = 0\n" +
		"H1 = 1\nH2 = 2\nH3 = 3\nH4 = 4\n" +
		"I1 = <b 0xdeadbeef0102>\nI2 = <b 0x494e5649544520736970>\n" +
		"[Peer]\nPublicKey = QkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkI=\nEndpoint = 185.182.193.107:51820\nAllowedIPs = 0.0.0.0/0\n"
	cfg, err := Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AWG.I1 == nil || *cfg.AWG.I1 != "<b 0xdeadbeef0102>" {
		t.Fatalf("I1 = %v", cfg.AWG.I1)
	}
	if cfg.AWG.I2 == nil || !strings.HasPrefix(*cfg.AWG.I2, "<b 0x494e") {
		t.Fatalf("I2 = %v", cfg.AWG.I2)
	}
	if !cfg.AWG.HasExtended() {
		t.Fatal("init packets must mark extended")
	}
	args := cfg.AWG.AscArgs()
	if len(args) != 16 || args[11] != "\"<b 0xdeadbeef0102>\"" || args[12] != "\"<b 0x494e5649544520736970>\"" {
		t.Fatalf("AscArgs = %v", args)
	}
	if args[13] != "\"\"" || args[15] != "\"\"" {
		t.Fatalf("absent init packets must be empty quoted: %v", args)
	}
}

func TestParseRealServiceConfigs(t *testing.T) {
	files, err := os.ReadDir("../../testconfigs")
	if err != nil {
		t.Skip("no testconfigs dir")
	}
	for _, dir := range []string{"proton", "warp"} {
		entries, err := os.ReadDir("../../testconfigs/" + dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".conf") {
				continue
			}
			data, err := os.ReadFile("../../testconfigs/" + dir + "/" + e.Name())
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := Parse(data)
			if err != nil {
				t.Fatalf("%s/%s: %v", dir, e.Name(), err)
			}
			if !cfg.AWG.Present() {
				t.Fatalf("%s/%s: awg params missing", dir, e.Name())
			}
			t.Logf("%s/%s: endpoint=%s asc=%d args, jc=%s", dir, e.Name(), cfg.Endpoint(), len(cfg.AWG.AscArgs()), *cfg.AWG.Jc)
		}
	}
	_ = files
}

func TestParseRejectsBroken(t *testing.T) {
	cases := map[string]string{
		"no private key": "[Peer]\nPublicKey = QkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkI=\nEndpoint = h:1\nAllowedIPs = 0.0.0.0/0\n",
		"no address":     "[Interface]\nPrivateKey = QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE=\n[Peer]\nPublicKey = QkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkI=\nEndpoint = h:1\n",
		"no endpoint":    "[Interface]\nPrivateKey = QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE=\nAddress = 10.0.0.2/32\n[Peer]\nPublicKey = QkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkI=\n",
		"two peers":      "[Interface]\nPrivateKey = QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE=\nAddress = 10.0.0.2/32\n[Peer]\nPublicKey = QkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkI=\nEndpoint = h:1\n[Peer]\nPublicKey = SkpKSkpKSkpKSkpKSkpKSkpKSkpKSkpKSkpKSkpKSko=\nEndpoint = h:2\n",
		"bad key":        "[Interface]\nPrivateKey = short\nAddress = 10.0.0.2/32\n[Peer]\nPublicKey = QkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkI=\nEndpoint = h:1\n",
	}
	for name, body := range cases {
		if _, err := Parse([]byte(body)); err == nil {
			t.Fatalf("%s: expected error", name)
		}
	}
}

func TestParseEndpointForms(t *testing.T) {
	for _, c := range []struct {
		in   string
		host string
		port int
	}{
		{"host.example:51820", "host.example", 51820},
		{"192.0.2.1:443", "192.0.2.1", 443},
		{"[2001:db8::1]:2408", "[2001:db8::1]", 2408},
	} {
		h, p, err := parseEndpoint(c.in)
		if err != nil || h != c.host || p != c.port {
			t.Fatalf("parseEndpoint(%q) = %q,%d,%v", c.in, h, p, err)
		}
	}
}
