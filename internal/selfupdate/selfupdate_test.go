package selfupdate

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

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
		// косметические 4-частные релизы: пользователям 0.7.3 видно 0.7.3.1,
		// обратно - нет; следующий нормальный релиз поверх 0.7.3.1 тоже виден
		{"v0.7.3", "v0.7.3.1", true},
		{"v0.7.3.1", "v0.7.3", false},
		{"v0.7.3.1", "v0.7.4", true},
		{"v0.8.0", "v0.7.3.1", false},
		{"v0.7.3.1", "v0.7.3.2", true},
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

// api.github.com бьётся rate-limit'ом по IP роутера (живой кейс 11.2026:
// релиз вышел, а панель молчала «обновлений нет») - тег добирается
// веб-редиректом /releases/latest, который лимитов не знает
func TestLatestFallsBackToWebRedirect(t *testing.T) {
	resp := func(code int, loc string) *http.Response {
		h := http.Header{}
		if loc != "" {
			h.Set("Location", loc)
		}
		return &http.Response{StatusCode: code, Header: h, Body: io.NopCloser(strings.NewReader(""))}
	}
	oldGet, oldNoRedir := httpGet, httpGetNoRedirect
	t.Cleanup(func() { httpGet, httpGetNoRedirect = oldGet, oldNoRedir })

	httpGet = func(ctx context.Context, url string) (*http.Response, error) {
		return resp(403, ""), nil // rate limit
	}
	httpGetNoRedirect = func(ctx context.Context, url string) (*http.Response, error) {
		return resp(302, "https://github.com/MarkinAlexander/mawg/releases/tag/v0.8.10.3"), nil
	}
	rel, err := Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rel.Tag != "v0.8.10.3" {
		t.Fatalf("тег из редиректа: %+v", rel)
	}

	// API недоступен и вовсе (сеть), редирект жив - тоже работает
	httpGet = func(ctx context.Context, url string) (*http.Response, error) {
		return nil, fmt.Errorf("timeout")
	}
	if rel, err = Latest(context.Background()); err != nil || rel.Tag != "v0.8.10.3" {
		t.Fatalf("фолбэк при недоступном API: %+v %v", rel, err)
	}

	// оба пути мертвы - ошибка API доносится
	httpGetNoRedirect = func(ctx context.Context, url string) (*http.Response, error) {
		return nil, fmt.Errorf("нет сети")
	}
	if _, err = Latest(context.Background()); err == nil || !strings.Contains(err.Error(), "github недоступен") {
		t.Fatalf("ожидалась ошибка недоступности: %v", err)
	}
}
