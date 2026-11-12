package web

// «Сделать внешним» срабатывало только со второго нажатия (живой кейс
// владельца 11.2026, tun9 чужого софта): POST писал режим, но GET /ifaces
// тянул кэш на 10с - панель видела старую строку. Смена режима обязана
// сбрасывать кэш, чтобы следующий опрос отвечал свежим состоянием.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mawg/internal/magitrickle"
	"mawg/internal/platform/fake"
	"mawg/internal/rotator"
	"mawg/internal/store"
)

func ifacesOf(t *testing.T, ts *httptest.Server) map[string]string {
	t.Helper()
	resp, err := http.Get(ts.URL + "/api/v1/ifaces")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Interfaces []struct {
			Device string `json:"device"`
			Mode   string `json:"mode"`
		} `json:"interfaces"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	modes := map[string]string{}
	for _, i := range out.Interfaces {
		modes[i.Device] = i.Mode
	}
	return modes
}

func TestSetIfaceModeInvalidatesCache(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fb := fake.New()
	e := rotator.New(st, fb, magitrickle.New("http://127.0.0.1:1"))
	srv := New(st, e, fb, nil, "test", nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// прогреваем кэш: nwg2 без пула - hidden
	if m := ifacesOf(t, ts); m["nwg2"] != "hidden" {
		t.Fatalf("до смены: %+v", m)
	}
	// сразу (внутри TTL) меняем режим - кэш обязан быть сброшен
	resp, err := http.Post(ts.URL+"/api/v1/ifaces/nwg2/mode",
		"application/json", strings.NewReader(`{"mode":"external"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mode: %d", resp.StatusCode)
	}
	if m := ifacesOf(t, ts); m["nwg2"] != "external" {
		t.Fatalf("после смены ждём external сразу, без второго нажатия: %+v", m)
	}

	// и обратно, тем же правилом
	resp, err = http.Post(ts.URL+"/api/v1/ifaces/nwg2/mode",
		"application/json", strings.NewReader(`{"mode":"hidden"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if m := ifacesOf(t, ts); m["nwg2"] != "hidden" {
		t.Fatalf("обратно hidden: %+v", m)
	}
}
