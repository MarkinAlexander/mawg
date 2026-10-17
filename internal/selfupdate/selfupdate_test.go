package selfupdate

import "testing"

// дев-сборки (vX.Y.Z-devN) обновляются на релиз СТРОГО новее своей базы:
// владелец проверяет механизм автообновления именно так (слово 2026-10-18:
// «мы специально ставили себе дев версию тем же номером что сейчас релиз»);
// равный релиз не предлагается - дев свеже своего релиза.
func TestNewerDevSuffix(t *testing.T) {
	cases := []struct {
		cur, latest string
		want        bool
	}{
		{"v0.6.11-dev39", "v0.6.11", false}, // равная база: дев свежее релиза
		{"v0.6.11-dev39", "v0.6.12", true},  // как владелец и проверял
		{"v0.6.11-dev39", "v0.7.0", true},
		{"v0.6.10-dev8", "v0.6.11", true},
		{"0.6.12-49-g0b35b33", "v0.7.0", true},  // git-describe без v
		{"0.6.12-49-g0b35b33", "v0.6.12", false}, // свой релиз не предлагается
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

func TestVerBase(t *testing.T) {
	cases := []struct{ in, want string }{
		{"0.6.11-dev39", "0.6.11"},
		{"0.6.12-49-g0b35b33", "0.6.12"},
		{"0.7.0", "0.7.0"},
		{"", ""},
		{"dev", "dev"},
	}
	for _, c := range cases {
		if got := verBase(c.in); got != c.want {
			t.Errorf("verBase(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
