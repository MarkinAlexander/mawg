package keenetic

import (
	"testing"

	"mawg/internal/platform"
)

func TestParseSlotsMapShape(t *testing.T) {
	body := []byte(`{"Wireguard2":{"id":"Wireguard2","description":"warp","link":"up","connected":"yes"},
"Bridge0":{"id":"Bridge0","link":"up"},"Wireguard5":{"id":"Wireguard5"}}`)
	out, err := parseSlots(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("slots = %d, want 2", len(out))
	}
	byID := map[string]platform.SlotInfo{}
	for _, sl := range out {
		byID[sl.ID] = sl
	}
	w2, ok := byID["Wireguard2"]
	if !ok || w2.Device != "nwg2" || !w2.LinkUp || !w2.Connected {
		t.Fatalf("Wireguard2 = %+v present=%v", w2, ok)
	}
}

func TestParseSlotsArrayShape(t *testing.T) {
	body := []byte(`[{"id":"Wireguard2","description":"warp","link":"up"},{"id":"Wireguard4","id":"Wireguard4","connected":"yes"}]`)
	out, err := parseSlots(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0].Device != "nwg2" {
		t.Fatalf("slots = %+v", out)
	}
}

func TestParseSlotsCaseInsensitivePrefix(t *testing.T) {
	body := []byte(`[{"id":"WireGuard7","description":"awg"}]`)
	out, err := parseSlots(body)
	if err != nil || len(out) != 1 || out[0].Device != "nwg7" {
		t.Fatalf("slots = %+v err=%v", out, err)
	}
}

func TestParseSlotsGarbage(t *testing.T) {
	if _, err := parseSlots([]byte("<html>")); err == nil {
		t.Fatal("garbage must error")
	}
}
