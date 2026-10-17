package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"mawg/internal/platform/fake"
	"mawg/internal/rotator"
	"mawg/internal/store"
)

// узлы движкового пула (60-док): гарды эндпоинта - неизвестный пул и
// недижковый пул; живой проход через clash_api покрывают тесты пакета
// singbox (клиент) и живая приёмка на стенде.
func TestPoolEngineGuards(t *testing.T) {
	fb := fake.New()
	st, _ := store.Open(t.TempDir())
	if _, err := st.CreatePool("native", store.PoolSettings{Platform: store.PlatformOpenwrt, OpenwrtProto: "wireguard"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreatePool("eng", store.PoolSettings{Platform: store.PlatformOpenwrt, EngineMode: "singbox", TunName: "tun1"}); err != nil {
		t.Fatal(err)
	}
	engine := rotator.New(st, fb, nil)
	srv := httptest.NewServer(New(st, engine, fb, nil, "test", nil).Handler())
	t.Cleanup(srv.Close)

	// неизвестный пул - 404
	resp, err := http.Get(srv.URL + "/api/v1/pools/nope/engine")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("неизвестный пул: status=%d", resp.StatusCode)
	}

	// недижковый пул - 400 с честным текстом
	resp2, err := http.Get(srv.URL + "/api/v1/pools/native/engine")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	var body struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if resp2.StatusCode != http.StatusBadRequest || body.Error == "" {
		t.Fatalf("недижковый пул: status=%d body=%+v", resp2.StatusCode, body)
	}
}
