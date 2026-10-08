package links

import (
	"strings"
	"testing"

	"mawg/internal/wgconf"
)

func TestConfTextRoundtrip(t *testing.T) {
	node, err := ParseLink("s4", amneziaWGLink)
	if err != nil {
		t.Fatal(err)
	}
	text, err := node.ConfText()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := wgconf.Parse([]byte(text))
	if err != nil {
		t.Fatalf("сгенерированный conf не парсится: %v\n%s", err, text)
	}
	if parsed.PrivateKey != node.PrivateKey || parsed.Peer.PublicKey != node.PublicKey ||
		parsed.Peer.PresharedKey != node.PresharedKey {
		t.Fatalf("ключи потеряны:\n%s", text)
	}
	if parsed.Peer.EndpointHost != node.Host || parsed.Peer.EndpointPort != node.Port {
		t.Fatalf("endpoint: %+v", parsed.Peer)
	}
	if parsed.MTU != node.MTU || parsed.Peer.PersistentKeepalive != node.Keepalive {
		t.Fatalf("mtu/keepalive:\n%s", text)
	}
	if parsed.AWG.I1 == nil || *parsed.AWG.I1 != fakeCPS {
		t.Fatalf("I1 CPS потерян:\n%s", text)
	}
	if parsed.AWG.H1 == nil || *parsed.AWG.H1 != "24294-85973" {
		t.Fatalf("H1 диапазон потерян:\n%s", text)
	}
}

func TestConfTextPlainWG(t *testing.T) {
	node, err := ParseLink("s4", wireguardLink)
	if err != nil {
		t.Fatal(err)
	}
	text, err := node.ConfText()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "Jc") || strings.Contains(text, "H1") {
		t.Fatalf("plain wg не должен иметь awg-полей:\n%s", text)
	}
	if _, err := wgconf.Parse([]byte(text)); err != nil {
		t.Fatal(err)
	}
}

func TestConfTextRejectsProxyNode(t *testing.T) {
	node, err := ParseLink("s4", vlessRealityTCP)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := node.ConfText(); err == nil {
		t.Fatal("vless не должен превращаться в .conf")
	}
}
