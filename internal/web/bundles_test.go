package web

// Бандлы принимают ЧУЖИЕ туннели (SysTunnels) в члены: фоллбек «tun9
// постороннего софта основной, пул запасной» - кейс владельца 12.11.
// Раньше резолвер знал только слоты mawg - «участник tun9 не найден»,
// хотя UI-пикер (берёт /ifaces) tun9 показывал.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mawg/internal/magitrickle"
	"mawg/internal/platform"
	"mawg/internal/platform/fake"
	"mawg/internal/rotator"
	"mawg/internal/store"
)

func TestBundleAcceptsForeignTunnelMember(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fb := fake.New()
	fb.TunnelsOverride = []platform.SlotInfo{{Device: "tun9", LinkUp: true}}
	e := rotator.New(st, fb, magitrickle.New("http://127.0.0.1:1"))
	srv := New(st, e, fb, nil, "test", nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp, err := http.Post(ts.URL+"/api/v1/bundles", "application/json",
		strings.NewReader(`{"name":"tg","members":["tun9"],"groups":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("создание бандла с чужим туннелем: status %d, body %s", resp.StatusCode, body)
	}

	// несуществующее имя по-прежнему отвергается
	resp, err = http.Post(ts.URL+"/api/v1/bundles", "application/json",
		strings.NewReader(`{"name":"bad","members":["tun99"],"groups":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode == http.StatusOK {
		t.Fatal("несуществующий участник не должен приниматься")
	}
}
