package singbox

import "testing"

// FallbackPick - вся семантика режима "первый живой по порядку":
// текущий держится через одну плохую пробу, старший возвращается только
// после трёх подряд успехов, мёртвый текущий не удерживает никого
func TestFallbackPick(t *testing.T) {
	order := []string{"mawg-p|a", "mawg-p|b", "mawg-p|c"}
	cases := []struct {
		name    string
		alive   map[string]bool
		current string
		streak  int
		pick    string
		sw      bool
		nstreak int
	}{
		{
			name:    "старший жив, он и текущий - на месте, стрик растёт",
			alive:   map[string]bool{"mawg-p|a": true},
			current: "mawg-p|a", streak: 1,
			pick: "mawg-p|a", sw: false, nstreak: 2,
		},
		{
			name:    "все живы, текущий второй - остаёмся (первый и так старший жив)",
			alive:   map[string]bool{"mawg-p|a": true, "mawg-p|b": true},
			current: "mawg-p|b",
			pick:    "mawg-p|b", sw: false, nstreak: 1,
		},
		{
			name:    "старший умер - немедленно на первого живого",
			alive:   map[string]bool{"mawg-p|b": true, "mawg-p|c": true},
			current: "mawg-p|a",
			pick:    "mawg-p|b", sw: true, nstreak: 0,
		},
		{
			name:    "старший умер, уже на следующем - без переключения",
			alive:   map[string]bool{"mawg-p|b": true},
			current: "mawg-p|b",
			pick:    "mawg-p|b", sw: false, nstreak: 0,
		},
		{
			name:    "старший ожил, стрик меньше порога - ждём на текущем живом",
			alive:   map[string]bool{"mawg-p|a": true, "mawg-p|b": true},
			current: "mawg-p|b", streak: 1,
			pick: "mawg-p|b", sw: false, nstreak: 2,
		},
		{
			name:    "старший жив, текущий мёртв - немедленный возврат без доверия",
			alive:   map[string]bool{"mawg-p|a": true},
			current: "mawg-p|b",
			pick:    "mawg-p|a", sw: true, nstreak: 0,
		},
		{
			name:    "стрик достиг порога - возврат на старшего",
			alive:   map[string]bool{"mawg-p|a": true, "mawg-p|b": true},
			current: "mawg-p|b", streak: 2,
			pick: "mawg-p|a", sw: true, nstreak: 0,
		},
		{
			name:    "никто не жив - остаёмся, стрик сброшен",
			alive:   map[string]bool{},
			current: "mawg-p|b",
			pick:    "mawg-p|b", sw: false, nstreak: 0,
		},
	}
	for _, c := range cases {
		pick, sw, streak := FallbackPick(order, c.alive, c.current, c.streak)
		if pick != c.pick || sw != c.sw || streak != c.nstreak {
			t.Errorf("%s: got pick=%s sw=%v streak=%d, want pick=%s sw=%v streak=%d",
				c.name, pick, sw, streak, c.pick, c.sw, c.nstreak)
		}
	}
	// пустой порядок - но-оп
	pick, sw, streak := FallbackPick(nil, map[string]bool{"x": true}, "x", 5)
	if pick != "x" || sw || streak != 5 {
		t.Errorf("пустой порядок: pick=%s sw=%v streak=%d", pick, sw, streak)
	}
}
