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
		if p.Source != AllowDomainsURL && p.Source != "встроенный список" {
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
	// allow-domains спеки + встроенный dns-tunnel
	if len(Presets) != len(presetSpecs)+1 {
		t.Fatalf("пресетов %d, ожидалось %d", len(Presets), len(presetSpecs)+1)
	}
}

func TestDNSTunnelPreset(t *testing.T) {
	p, ok := PresetByID("dns-tunnel")
	if !ok {
		t.Fatal("нет пресета dns-tunnel")
	}
	var v4, v6 int
	hasGoogle := false
	for _, rl := range p.Rules {
		switch rl.Type {
		case "subnet":
			v4++
		case "subnet6":
			v6++
		default:
			t.Fatalf("dns-tunnel: неожиданный тип %q у %q", rl.Type, rl.Rule)
		}
		if rl.Rule == "8.8.8.8/32" {
			hasGoogle = true
		}
	}
	if v4 != 8 || v6 != 6 {
		t.Fatalf("dns-tunnel: v4=%d v6=%d, ожидалось 8+6", v4, v6)
	}
	if !hasGoogle {
		t.Fatal("dns-tunnel: нет 8.8.8.8/32")
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

// сплит Meta по сервисам (85-док): домены честно по своим, подсети общие -
// WhatsApp несёт их с предупреждением, meta-subnets - весь Meta без доменов
func TestMetaSplitPresets(t *testing.T) {
	presetRules := func(id string) map[string][]string {
		p, ok := PresetByID(id)
		if !ok {
			t.Fatalf("нет пресета %s", id)
		}
		out := map[string][]string{}
		for _, rl := range p.Rules {
			out[rl.Type] = append(out[rl.Type], rl.Rule)
		}
		return out
	}
	has := func(rules []string, v string) bool {
		for _, r := range rules {
			if r == v {
				return true
			}
		}
		return false
	}

	// WhatsApp: домены владельца (включая whatsapp.org, которого в общей
	// meta.lst нет) + его 12 подсетей (включая гуглевую 108.177.8.0/21)
	wa := presetRules("whatsapp")
	for _, d := range []string{"whatsapp.com", "whatsapp.net", "whatsapp.org", "wa.me"} {
		if !has(wa["namespace"], d) {
			t.Errorf("whatsapp: нет домена %s", d)
		}
	}
	for _, s := range []string{"108.177.8.0/21", "157.240.0.0/16", "31.13.24.0/21", "185.60.216.0/22", "173.252.64.0/18"} {
		if !has(wa["subnet"], s) {
			t.Errorf("whatsapp: нет подсети %s", s)
		}
	}
	if len(wa["subnet"]) != 12 {
		t.Errorf("whatsapp: подсетей %d, ожидалось 12 (список владельца)", len(wa["subnet"]))
	}
	if len(wa["subnet6"]) != 0 {
		t.Errorf("whatsapp: v6-подсетей не должно быть: %v", wa["subnet6"])
	}
	p, _ := PresetByID("whatsapp")
	if p.Note == "" {
		t.Error("whatsapp: без предупреждения об общих подсетях Meta")
	}

	// сервисные пресеты - только домены, без чужих
	for id, doms := range map[string][]string{
		"instagram": {"instagram.com", "cdninstagram.com"},
		"facebook":  {"facebook.com", "fbcdn.net", "fb.com"},
		"threads":   {"threads.net", "threads.com"},
	} {
		r := presetRules(id)
		for _, d := range doms {
			if !has(r["namespace"], d) {
				t.Errorf("%s: нет домена %s", id, d)
			}
		}
		if len(r["subnet"]) != 0 || len(r["subnet6"]) != 0 {
			t.Errorf("%s: подсети не должны попадать (общие у Meta): %v %v", id, r["subnet"], r["subnet6"])
		}
	}

	// meta-subnets: только подсети (v4+v6 из общей meta.lst), без доменов
	ms := presetRules("meta-subnets")
	if len(ms["namespace"]) != 0 {
		t.Errorf("meta-subnets: домены не нужны: %v", ms["namespace"])
	}
	if len(ms["subnet"]) < 70 || len(ms["subnet6"]) < 30 {
		t.Errorf("meta-subnets: v4=%d v6=%d - список общей меты не весь", len(ms["subnet"]), len(ms["subnet6"]))
	}
	if !has(ms["subnet"], "157.240.0.0/17") {
		t.Error("meta-subnets: нет 157.240.0.0/17 из общей meta.lst")
	}

	// старый «meta» остался всем сразу
	m := presetRules("meta")
	if !has(m["namespace"], "whatsapp.com") || !has(m["namespace"], "instagram.com") || !has(m["namespace"], "threads.net") {
		t.Error("meta: домены сервисов пропали")
	}
	if len(m["subnet"]) == 0 || len(m["subnet6"]) == 0 {
		t.Error("meta: подсети пропали")
	}
}
