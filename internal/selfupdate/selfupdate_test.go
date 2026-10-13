package selfupdate

import "testing"

// дев-сборки (vX.Y.Z-devN) не предлагаются обновляться на релизы: суффикс
// существует, чтобы релиз случайно не затёр дев-фичи (слово владельца).
func TestNewerDevSuffix(t *testing.T) {
	cases := []struct {
		cur, latest string
		want        bool
	}{
		{"v0.6.10-dev8", "v0.6.10", false},
		{"v0.6.10-dev8", "v0.6.11", false},
		{"v0.6.10-dev1", "v0.7.0", false},
		{"v0.6.10", "v0.6.11", true},
		{"v0.6.9", "v0.6.10", true},
		{"v0.6.10", "v0.6.10", false},
		{"dev", "v0.6.10", false},
		{"", "v0.6.10", false},
	}
	for _, c := range cases {
		if got := newer(c.cur, c.latest); got != c.want {
			t.Errorf("newer(%q, %q) = %v, want %v", c.cur, c.latest, got, c.want)
		}
	}
}
