package web

// Селект интерфейсов в группах правил обязан содержать ВСЕ туннели системы
// (сильный баг, слово владельца 11.2026): раньше список собирался из групп
// MT + пулов/слотов - туннель чужого софта (tun9, без слота и пула) не
// попадал, пока его не выберет какая-то группа; новому пользователю с
// единственным туннелем создавать группу было не на что.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"mawg/internal/magitrickle"
	"mawg/internal/platform"
	"mawg/internal/platform/fake"
	"mawg/internal/rotator"
	"mawg/internal/store"
)

func mtInterfacesOf(t *testing.T, ts *httptest.Server) map[string]bool {
	t.Helper()
	resp, err := http.Get(ts.URL + "/api/v1/mt/interfaces")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Interfaces []struct {
			Device string `json:"device"`
		} `json:"interfaces"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	m := map[string]bool{}
	for _, i := range out.Interfaces {
		m[i.Device] = true
	}
	return m
}

func TestMtInterfacesIncludeForeignTunnels(t *testing.T) {
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

	m := mtInterfacesOf(t, ts)
	// MT недоступен (127.0.0.1:1) - но системные туннели и слоты выбираемы
	if !m["tun9"] {
		t.Fatalf("туннель чужого софта tun9 обязан быть в селекте групп: %+v", m)
	}
	if !m["nwg2"] {
		t.Fatalf("слот nwg2 (fake Slots) тоже должен быть: %+v", m)
	}
	if m["blackhole"] {
		t.Fatalf("blackhole - служебное имя, UI добавляет его сам: %+v", m)
	}
}
