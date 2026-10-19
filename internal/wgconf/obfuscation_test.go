package wgconf

import (
	"strings"
	"testing"
)

// Антиобфускация и уровни (слово владельца: голому конфигу - AWG-вариант,
// 2.0 - снутый вариант, 3.x - никаких преобразований).

const protonLikeConf = `[Interface]
PrivateKey = RRRRRRRRRRRRRRRRRRRRRRRRRRRRRRRRRRRRRRRRRRR=
Address = 10.2.0.2/32
MTU = 1280
S1 = 0
S2 = 0
Jc = 3
Jmin = 1
Jmax = 3
H1 = 1
H4 = 4
I1 = <b 0xc2ababababababababababababababababababababababababababababababababababababababababababababababababababababababababababab>

[Peer]
# Server: US-FREE#34
PublicKey = DDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD=
Endpoint = 149.102.254.89:51820
AllowedIPs = 0.0.0.0/0, ::/0
`

func TestAwgLevelOf(t *testing.T) {
	if got := AwgLevelOf([]byte(protonLikeConf)); got != Awg2 {
		t.Errorf("proton-like: %q, ожидали awg2", got)
	}
	plain := "[Interface]\nPrivateKey = " + strings.Repeat("A", 43) + "=\nAddress = 172.16.0.2/32\nMTU = 1280\n\n[Peer]\nPublicKey = " + testPeerKey() + "\nEndpoint = engage.cloudflareclient.com:500\n"
	if got := AwgLevelOf([]byte(plain)); got != AwgPlain {
		t.Errorf("простой: %q, ожидали plain", got)
	}
	fields := "Jc = 4\nI1 = <b 0x494e>\nHeaderProtectionKey = AAAA\nL1 = 5-10\n"
	awg3 := plain[:strings.Index(plain, "\n\n[Peer]")] + "\n" + fields + plain[strings.Index(plain, "\n\n[Peer]"):]
	if got := AwgLevelOf([]byte(awg3)); got != Awg3 {
		t.Errorf("3.x: %q, ожидали awg3", got)
	}
	if got := AwgLevelOf([]byte("мусор")); got != "" {
		t.Errorf("мусор: %q, ожидали пусто", got)
	}
}

// TestStripAWGParams - поля обфускации сняты, комментарии и прочее целы,
// результат разбирается как чистый WG.
func TestStripAWGParams(t *testing.T) {
	stripped := StripAWGParams([]byte(protonLikeConf))
	for _, gone := range []string{"Jc = 3", "S1 = 0", "H4 = 4", "I1 = <b"} {
		if strings.Contains(string(stripped), gone) {
			t.Errorf("поле %q осталось после снятия", gone)
		}
	}
	for _, kept := range []string{"# Server: US-FREE#34", "PrivateKey = RRRRRRRRRR", "Endpoint = 149.102.254.89:51820", "AllowedIPs = 0.0.0.0/0, ::/0"} {
		if !strings.Contains(string(stripped), kept) {
			t.Errorf("потеряли %q", kept)
		}
	}
	if _, err := Parse(stripped); err != nil {
		t.Fatalf("снутый конфиг не разбирается: %v", err)
	}
	if lvl := AwgLevelOf(stripped); lvl != AwgPlain {
		t.Errorf("после снятия уровень: %q", lvl)
	}
	// 3.x-поля тоже снимаются (L1-L25, HP-ключ)
	awg3 := string(plainConfForStrip()) + "Jc = 4\nI1 = <b 0x494e>\nHeaderProtectionKey = BBBB\nL1 = 5-10\n"
	stripped3 := StripAWGParams([]byte(awg3))
	for _, gone := range []string{"HeaderProtectionKey", "L1 = 5-10", "Jc = 4", "I1 = <b 0x494e"} {
		if strings.Contains(string(stripped3), gone) {
			t.Errorf("3.x/2.0 поле %q осталось", gone)
		}
	}
}

func plainConfForStrip() []byte {
	return []byte("[Interface]\nPrivateKey = " + strings.Repeat("A", 43) + "=\nAddress = 172.16.0.2/32\nMTU = 1280\n\n[Peer]\nPublicKey = " + testPeerKey() + "\nEndpoint = engage.cloudflareclient.com:500\nAllowedIPs = 0.0.0.0/0\n")
}

func testPeerKey() string {
	return strings.Repeat("B", 43) + "="
}
