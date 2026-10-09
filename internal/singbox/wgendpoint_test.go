package singbox

import (
	"encoding/json"
	"strings"
	"testing"

	"mawg/internal/wgconf"
)

// конфиг вида Amnezia Free: AWG 3.x (защита заголовка, диапазоны,
// I-блобы со случайными вставками, диапазонный keepalive)
const awg3Conf = `[Interface]
Address = 100.81.189.224/32
DNS = 100.64.0.1, 8.8.4.4
PrivateKey = uJd2xK5t0mFvqWz+YbN8cLrA3eHh7oPqQ1wZsX4dC6k=
Jc = 6
Jmin = 10
Jmax = 80
S1 = 31
S2 = 1101
S3 = 1240
S4 = 12
H1 = 1
H2 = 2
H3 = 3
H4 = 4
HeaderProtectionKey = c3ludGhldGljLWhwLWtleQ==
RekeyAfterTime = 100-120
RekeyTimeout = 3-8
RejectAfterTime = 150-180
KeepaliveTimeout = 7-13
MaxHandshakeAttempts = 15-20
ContentPaddingAddition = 10-100
I1 = <b 0xc70000000108><r 640><b 0xe78ab395ff2f><r 64>
[Peer]
PublicKey = ll+gozj6agFD5WRhXQUFna6LLoC4zxi7eka5RMUfKE8=
PresharedKey = kLKh8p7664kHCd8rKtcfHWgmaWqzv1mTUAQxtCf/GVU=
AllowedIPs = 100.64.0.1/32, 8.8.8.8/32
Endpoint = 84.54.51.212:1545
PersistentKeepalive = 25-35
`

func TestWGEndpointAWG3(t *testing.T) {
	cfg, err := wgconf.Parse([]byte(awg3Conf))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.NeedsEngine() {
		t.Fatal("AWG 3.x конфиг должен требовать движок")
	}
	ep, err := wgEndpoint(cfg, "mawg-free|1")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(ep)
	s := string(b)
	for _, want := range []string{
		`"type":"wireguard"`, `"private_key":"uJd2`, `"address":["100.81.189.224/32"]`,
		`"header_protection_key":"c3ludGhldGljLWhwLWtleQ=="`,
		`"rekey_after_time":"100-120"`, `"content_padding_addition":"10-100"`,
		`"keepalive_timeout":"7-13"`, `"max_handshake_attempts":"15-20"`,
		`"persistent_keepalive_interval":"25-35"`,
		`"pre_shared_key":"kLKh`, `"port":1545`,
		`"jc":6`, `"s2":1101`,
		`"allowed_ips":["100.64.0.1/32","8.8.8.8/32"]`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("в эндпоинте нет %s: %s", want, s)
		}
	}
	// i1-блоб: json.Marshal экранирует <> как u003c, сверяем после разбора
	var back map[string]any
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if got, _ := back["i1"].(string); got != "<b 0xc70000000108><r 640><b 0xe78ab395ff2f><r 64>" {
		t.Fatalf("i1: %q", got)
	}
}

// классический AWG 2.0 тоже собирается эндпоинтом (движок не обязан)
func TestWGEndpointClassic(t *testing.T) {
	cfg, err := wgconf.Parse([]byte("[Interface]\nPrivateKey = uJd2xK5t0mFvqWz+YbN8cLrA3eHh7oPqQ1wZsX4dC6k=\nAddress = 10.2.0.2/32\nJc = 3\nH1 = 1\n[Peer]\nPublicKey = ll+gozj6agFD5WRhXQUFna6LLoC4zxi7eka5RMUfKE8=\nEndpoint = free.example.net:51820\nAllowedIPs = 0.0.0.0/0\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NeedsEngine() {
		t.Fatal("классика не требует движок")
	}
	ep, err := wgEndpoint(cfg, "ep")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(ep)
	if !strings.Contains(string(b), `"address":"free.example.net"`) || !strings.Contains(string(b), `"h1":"1"`) {
		t.Fatalf("эндпоинт: %s", b)
	}
}

// пул только с WG-конфигами собирает конфиг движка с endpoints-секцией
func TestBuildConfigWGOnly(t *testing.T) {
	cfg, err := wgconf.Parse([]byte("[Interface]\nPrivateKey = uJd2xK5t0mFvqWz+YbN8cLrA3eHh7oPqQ1wZsX4dC6k=\nAddress = 10.9.0.2/32\nJc = 4\n[Peer]\nPublicKey = ll+gozj6agFD5WRhXQUFna6LLoC4zxi7eka5RMUfKE8=\nEndpoint = 192.0.2.1:30565\nAllowedIPs = 0.0.0.0/0\n"))
	if err != nil {
		t.Fatal(err)
	}
	data, _, err := BuildConfig([]PoolSpec{{
		Name: "free", Tun: "tun1", TunIP: TuneIP(1), MixedPort: 2281,
		ProbeTarget: "http://www.gstatic.com/generate_204", WG: []wgconf.Config{cfg},
	}}, Params{ClashPort: 2291, LX: true})
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{`"endpoints"`, `"type": "wireguard"`, `"mawg-free"`, `"urltest"`} {
		if !strings.Contains(s, want) {
			t.Fatalf("нет %s в:\n%s", want, s)
		}
	}
}
