package magitrickle

import (
	"net"
	"strings"
	"testing"
)

func TestPresetsBuilt(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range Presets {
		if p.ID == "" || p.Title == "" {
			t.Fatalf("пресет без id/title: %+v", p)
		}
		if seen[p.ID] {
			t.Fatalf("дубль id %s", p.ID)
		}
		seen[p.ID] = true
		if len(p.Rules) == 0 {
			t.Fatalf("пресет %s пустой", p.ID)
		}
		if p.Source != AllowDomainsURL {
			t.Fatalf("пресет %s: source = %q", p.ID, p.Source)
		}
		for _, rl := range p.Rules {
			if !rl.Enable {
				t.Fatalf("%s: правило %q выключено", p.ID, rl.Rule)
			}
			switch rl.Type {
			case "namespace":
				if !strings.Contains(rl.Rule, ".") || strings.Contains(rl.Rule, "/") {
					t.Fatalf("%s: кривой домен %q", p.ID, rl.Rule)
				}
			case "subnet", "subnet6":
				ip, _, err := net.ParseCIDR(rl.Rule)
				if err != nil {
					t.Fatalf("%s: кривая подсеть %q: %v", p.ID, rl.Rule, err)
				}
				isV4 := ip.To4() != nil
				if rl.Type == "subnet" && !isV4 {
					t.Fatalf("%s: %q не IPv4", p.ID, rl.Rule)
				}
				if rl.Type == "subnet6" && isV4 {
					t.Fatalf("%s: %q не IPv6", p.ID, rl.Rule)
				}
			default:
				t.Fatalf("%s: неожиданный тип %q", p.ID, rl.Type)
			}
		}
	}
	if len(Presets) != len(presetSpecs) {
		t.Fatalf("пресетов %d, ожидалось %d", len(Presets), len(presetSpecs))
	}
}

func TestTelegramPresetSubnets(t *testing.T) {
	p, ok := PresetByID("telegram")
	if !ok {
		t.Fatal("нет пресета telegram")
	}
	var v4, v6 int
	for _, rl := range p.Rules {
		switch rl.Type {
		case "subnet":
			v4++
		case "subnet6":
			v6++
		}
	}
	if v4 < 5 || v6 < 2 {
		t.Fatalf("telegram: v4=%d v6=%d, слишком мало", v4, v6)
	}
}

func TestSourceNote(t *testing.T) {
	if SourceNote() == "" {
		t.Fatal("SourceNote пустой: MANIFEST.json не попал в embed")
	}
}
