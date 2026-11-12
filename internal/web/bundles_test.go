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

// Мёртвые ссылки набора видны пользователю: группа пересоздана (id
// сменился) или участник удалён - набор молча не переключает; issues в
// /bundles - причина прямо в карточке (кейс владельца 12.11: 2ip-test
// ссылался на несуществующую группу и никогда не переключался)
func TestBundleIssuesReported(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fb := fake.New()
	fb.TunnelsOverride = []platform.SlotInfo{{Device: "tun9", LinkUp: true}}
	f := &fakeMT{version: "0.8.2-rev22", groups: []magitrickle.Group{
		{ID: "g1", Name: "telegram", Interface: "tun9"},
	}}
	mt := httptest.NewServer(f.handler())
	t.Cleanup(mt.Close)
	e := rotator.New(st, fb, magitrickle.New("http://127.0.0.1:1"))
	srv := New(st, e, fb, magitrickle.New(mt.URL), "test", nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// через store напрямую: API-создание отвергло бы мёртвого участника,
	// а стейл-ссылки появляются ПОСЛЕ создания (пересоздание группы,
	// удаление пула) - ровно как в жизни
	if _, err := st.CreateBundle(store.Bundle{
		Name:    "tg",
		Members: []string{"tun9", "nwg99"},
		Groups:  []string{"g1", "dead-id"},
	}); err != nil {
		t.Fatal(err)
	}
	got := httpGetJSON(t, ts.URL+"/api/v1/bundles")
	list, _ := got["bundles"].([]any)
	first, _ := list[0].(map[string]any)
	issues, _ := first["issues"].([]any)
	joined := ""
	for _, i := range issues {
		joined += i.(string) + "\n"
	}
	if !strings.Contains(joined, "группа dead-id не существует") {
		t.Fatalf("мёртвая группа не показана: %q", joined)
	}
	if !strings.Contains(joined, "участник nwg99 не найден") {
		t.Fatalf("мёртвый участник не показан: %q", joined)
	}
	if strings.Contains(joined, "tun9") || strings.Contains(joined, "g1") {
		t.Fatalf("живые ссылки не должны помечаться: %q", joined)
	}
}
