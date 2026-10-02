package keenetic

import (
	"os"
	"testing"
)

func mustFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestParseNDMCAggregateSlots(t *testing.T) {
	out := mustFixture(t, "wg2.txt") + mustFixture(t, "wg4.txt")
	ifaces := parseNDMCInterfaces(out)
	w2, ok := ifaces["Wireguard2"]
	if !ok {
		t.Fatalf("Wireguard2 missing, got %v", keys(ifaces))
	}
	if w2.Description != "warp" || w2.Link != "up" || w2.Connected != "yes" || w2.State != "up" {
		t.Fatalf("w2 = %+v", w2)
	}
	w3 := ifaces["Wireguard3"]
	if w3.Link != "down" || w3.Connected != "no" || w3.Description != "rocket_moscow" {
		t.Fatalf("w3 = %+v", w3)
	}
	slots := ndmcSlots(ifaces)
	if len(slots) != 3 {
		t.Fatalf("slots = %d, want 3", len(slots))
	}
	if slots[0].ID != "Wireguard2" || slots[0].Device != "nwg2" || !slots[0].LinkUp {
		t.Fatalf("slot0 = %+v", slots[0])
	}
	if slots[1].ID != "Wireguard3" || slots[1].LinkUp {
		t.Fatalf("slot1 = %+v", slots[1])
	}
	if slots[2].ID != "Wireguard4" || slots[2].Description != "proton" {
		t.Fatalf("slot2 = %+v", slots[2])
	}
}

func TestParseNDMCSingleWithPeer(t *testing.T) {
	out := mustFixture(t, "wg2-single.txt")
	ifaces := parseNDMCInterfaces(out)
	w2, ok := ifaces["Wireguard2"]
	if !ok {
		t.Fatalf("Wireguard2 missing")
	}
	if len(w2.Wireguard.Peers) != 1 {
		t.Fatalf("peers = %d, want 1", len(w2.Wireguard.Peers))
	}
	p := w2.Wireguard.Peers[0]
	if p.PublicKey != "bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo=" {
		t.Fatalf("public key = %q", p.PublicKey)
	}
	if !p.Enabled || !p.Online {
		t.Fatalf("peer flags: enabled=%v online=%v", p.Enabled, p.Online)
	}
	if p.LastHandshake <= 0 || p.LastHandshake > 86400 {
		t.Fatalf("last-handshake = %d", p.LastHandshake)
	}
	if w2.Description != "warp" || w2.Link != "up" {
		t.Fatalf("iface = %+v", w2)
	}
}

func keys(m map[string]rciInterface) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
