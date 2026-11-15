package wgconf

import (
	"strings"
	"testing"
)

// protonConf - структурная копия живого Proton-конфига владельца
// (local-docs/fixtures-proton, 15.11): Jc/Jmin/Jmax, статичный I1-блоб,
// Address = v4 + v6, DNS = 10.2.0.1 + v6. Ключи и эндпоинт синтетические
// (gitleaks), форма - как у провайдера.
const protonConf = `[Interface]
PrivateKey = QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE=
Address = 10.2.0.2/32, 2a07:b944::2:2/128
DNS = 10.2.0.1, 2a07:b944::2:1
MTU = 1420
S1 = 0
S2 = 0
S3 = 0
S4 = 0
Jc = 3
Jmin = 1
Jmax = 3
H1 = 1
H2 = 2
H3 = 3
H4 = 4
I1 = <b 0xce000000010897a297ecc34cd6dd000044d0ec2e2e1ea2991f467ace4222129b5>

[Peer]
# Server: NL-FREE#15
PublicKey = QkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkI=
Endpoint = 198.51.100.15:51820
AllowedIPs = 0.0.0.0/0, ::/0
`

func mustParse(t *testing.T, raw string) Config {
	t.Helper()
	cfg, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return cfg
}

func TestDetectProvider(t *testing.T) {
	p := DetectProvider(mustParse(t, protonConf))
	if p != ProviderProton {
		t.Fatalf("protonConf: %q, want proton", p)
	}
	// Подсеть без DNS провайдера - не вердикт: 10.2.0.0/24 - обычный
	// приватный диапазон, чужой провайдер опознаваться не должен
	noDNS := strings.Replace(protonConf, "DNS = 10.2.0.1, 2a07:b944::2:1", "DNS = 1.1.1.1", 1)
	if got := DetectProvider(mustParse(t, noDNS)); got != "" {
		t.Fatalf("10.2.0.x без DNS 10.2.0.1: %q, want пусто", got)
	}
	warp, err := Parse([]byte("[Interface]\nPrivateKey = QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE=\nAddress = 172.16.0.5/32\n\n[Peer]\nPublicKey = QkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkI=\nEndpoint = engage.cloudflareclient.com:2408\nAllowedIPs = 0.0.0.0/0\n"))
	if err != nil {
		t.Fatalf("warp Parse: %v", err)
	}
	if got := DetectProvider(warp); got != ProviderWARP {
		t.Fatalf("warp: %q, want warp", got)
	}
	amnezia, err := Parse([]byte("[Interface]\nPrivateKey = QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE=\nAddress = 10.9.0.2/32\nJc = 3\n\n[Peer]\nPublicKey = QkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkI=\nEndpoint = free.example.net:51820\nAllowedIPs = 0.0.0.0/0\n"))
	if err != nil {
		t.Fatalf("amnezia Parse: %v", err)
	}
	if got := DetectProvider(amnezia); got != "" {
		t.Fatalf("amnezia 10.9.0.x: %q, want пусто", got)
	}
}

func TestSetAddressProton(t *testing.T) {
	raw, err := SetAddress([]byte(protonConf), "10.2.0.3")
	if err != nil {
		t.Fatalf("SetAddress: %v", err)
	}
	cfg := mustParse(t, string(raw))
	if got := cfg.FirstIPv4(); got != "10.2.0.3/32" {
		t.Fatalf("v4: %q, want 10.2.0.3/32", got)
	}
	// v6 синхронно с новым v4-хвостом - иначе два пула несут один v6
	if len(cfg.Addresses) != 2 || cfg.Addresses[1] != "2a07:b944::2:3/128" {
		t.Fatalf("v6: %v, want [10.2.0.3/32 2a07:b944::2:3/128]", cfg.Addresses)
	}
	// Остальное - байт-в-байт: ключи, DNS, обфускация, I1
	for _, want := range []string{
		"PrivateKey = QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE=",
		"DNS = 10.2.0.1, 2a07:b944::2:1",
		"Jc = 3", "Jmin = 1", "Jmax = 3", "MTU = 1420",
		"I1 = <b 0xce000000010897a297ecc34cd6dd000044d0ec2e2e1ea2991f467ace4222129b5>",
		"Endpoint = 198.51.100.15:51820",
	} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("потеряна/искажена строка %q в:\n%s", want, raw)
		}
	}
	if strings.Contains(string(raw), "10.2.0.2") {
		t.Fatalf("старый v4 остался:\n%s", raw)
	}
}

func TestSetAddressPrefix(t *testing.T) {
	// Без префикса сохраняется прежний (/32); с явным - новый
	raw, err := SetAddress([]byte(protonConf), "10.2.0.30/24")
	if err != nil {
		t.Fatalf("SetAddress: %v", err)
	}
	if got := mustParse(t, string(raw)).FirstIPv4(); got != "10.2.0.30/24" {
		t.Fatalf("явный префикс: %q", got)
	}
	raw, err = SetAddress([]byte(protonConf), "10.2.0.30")
	if err != nil {
		t.Fatalf("SetAddress: %v", err)
	}
	if got := mustParse(t, string(raw)).FirstIPv4(); got != "10.2.0.30/32" {
		t.Fatalf("сохранение префикса: %q, want 10.2.0.30/32", got)
	}
}

func TestSetAddressWARP(t *testing.T) {
	conf := "[Interface]\nPrivateKey = QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE=\nAddress = 172.16.0.2/32\nMTU = 1280\n\n[Peer]\nPublicKey = QkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkI=\nEndpoint = engage.cloudflareclient.com:2408\nAllowedIPs = 0.0.0.0/0\n"
	raw, err := SetAddress([]byte(conf), "172.16.0.5")
	if err != nil {
		t.Fatalf("SetAddress: %v", err)
	}
	cfg := mustParse(t, string(raw))
	if cfg.FirstIPv4() != "172.16.0.5/32" {
		t.Fatalf("v4: %q", cfg.FirstIPv4())
	}
	if cfg.MTU != 1280 {
		t.Fatalf("MTU потерян: %d", cfg.MTU)
	}
}

func TestSetAddressErrors(t *testing.T) {
	if _, err := SetAddress([]byte(protonConf), "not-an-ip"); err == nil {
		t.Fatal("не-IPv4 принят")
	}
	v6only := strings.Replace(protonConf, "Address = 10.2.0.2/32, ", "Address = ", 1)
	if _, err := SetAddress([]byte(v6only), "10.2.0.3"); err == nil {
		t.Fatal("конфиг без v4-Address принят")
	}
}

func TestIncV4(t *testing.T) {
	for _, tc := range []struct {
		addr string
		n    int
		want string
		fail bool
	}{
		{"10.2.0.2", 0, "10.2.0.2", false},
		{"10.2.0.2/32", 2, "10.2.0.4", false},
		{"172.16.0.254", 1, "172.16.0.255", false},
		{"10.2.0.254", 2, "", true},
		{"bogus", 1, "", true},
	} {
		got, err := IncV4(tc.addr, tc.n)
		if tc.fail {
			if err == nil {
				t.Fatalf("IncV4(%s,%d): нет ошибки", tc.addr, tc.n)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Fatalf("IncV4(%s,%d): %q %v, want %q", tc.addr, tc.n, got, err, tc.want)
		}
	}
}
