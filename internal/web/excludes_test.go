package web

// Исключения из обработки (94-док): REST-валидация глобального списка и
// исключений пула (PUT /pools/{name}).

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func excludesGet(t *testing.T, srv *httptest.Server) []string {
	t.Helper()
	resp, err := http.Get(srv.URL + "/api/v1/excludes")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Excludes []string `json:"excludes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out.Excludes
}

func excludesPut(t *testing.T, srv *httptest.Server, body string) (int, map[string]any) {
	t.Helper()
	return zonesPut(t, srv.URL+"/api/v1/excludes", body)
}

func TestExcludesAPI(t *testing.T) {
	srv, _, st := zonesServer(t, "0.8.2-rev15-mawg-dev")

	// пусто на старте: nil-хранилище отдаётся как []
	if got := excludesGet(t, srv); len(got) != 0 {
		t.Fatalf("стартовый список не пуст: %v", got)
	}

	// канонизация + дедуп: чистый IP -> /32, CIDR как есть
	code, out := excludesPut(t, srv, `{"excludes":["192.0.2.1","192.0.2.1/32"," 198.51.100.0/24 ",""]}`)
	if code != http.StatusOK {
		t.Fatalf("код %d: %v", code, out)
	}
	want := []string{"192.0.2.1/32", "198.51.100.0/24"}
	got := excludesGet(t, srv)
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("канонизация/дедуп: %v", got)
	}
	if st.Excludes()[0] != "192.0.2.1/32" {
		t.Fatalf("в сторе не канонично: %v", st.Excludes())
	}

	// мусор -> 400 и хранилище не тронуто
	code, out = excludesPut(t, srv, `{"excludes":["192.0.2.1","не адрес"]}`)
	if code != http.StatusBadRequest {
		t.Fatalf("мусор: код %d: %v", code, out)
	}
	if msg, _ := out["error"].(string); !strings.Contains(msg, "не похоже на IP") {
		t.Fatalf("текст ошибки без причины: %v", out)
	}
	if got := excludesGet(t, srv); len(got) != 2 {
		t.Fatalf("неудачный PUT затёр список: %v", got)
	}

	// IPv6 отклонён честным текстом
	code, out = excludesPut(t, srv, `{"excludes":["2001:db8::1"]}`)
	if code != http.StatusBadRequest {
		t.Fatalf("IPv6: код %d", code)
	}
	if msg, _ := out["error"].(string); !strings.Contains(msg, "IPv6") {
		t.Fatalf("текст про IPv6: %v", out)
	}

	// лимит
	many := make([]string, 129)
	for i := range many {
		many[i] = "10.1." + itoa(i/256) + "." + itoa(i%256)
	}
	body, _ := json.Marshal(map[string]any{"excludes": many})
	if code, _ := excludesPut(t, srv, string(body)); code != http.StatusBadRequest {
		t.Fatalf("лимит: код %d", code)
	}

	// очистка: [] = пустой список (не «не трогать»)
	if code, _ := excludesPut(t, srv, `{"excludes":[]}`); code != http.StatusOK {
		t.Fatal("очистка не прошла")
	}
	if got := excludesGet(t, srv); len(got) != 0 {
		t.Fatalf("очистка не сработала: %v", got)
	}
}

// Исключения пула: PUT /pools/{name} нормализует, 400 на мусор,
// [] очищает (nil - «не трогать»).
func TestPoolExcludesUpdate(t *testing.T) {
	srv, _, st := zonesServer(t, "0.8.2-rev15-mawg-dev")

	code, out := zonesPut(t, srv.URL+"/api/v1/pools/proton", `{"excludes":["203.0.113.5","203.0.113.5"]}`)
	if code != http.StatusOK {
		t.Fatalf("код %d: %v", code, out)
	}
	p, _ := st.Pool("proton")
	if len(p.Settings.Excludes) != 1 || p.Settings.Excludes[0] != "203.0.113.5/32" {
		t.Fatalf("исключения пула: %v", p.Settings.Excludes)
	}

	// без поля excludes - список не тронут
	code, out = zonesPut(t, srv.URL+"/api/v1/pools/proton", `{"checkIntervalSec":45}`)
	if code != http.StatusOK {
		t.Fatalf("код %d: %v", code, out)
	}
	p, _ = st.Pool("proton")
	if len(p.Settings.Excludes) != 1 {
		t.Fatalf("PUT без поля затёр исключения: %v", p.Settings.Excludes)
	}

	// мусор -> 400
	if code, out := zonesPut(t, srv.URL+"/api/v1/pools/proton", `{"excludes":["xxx"]}`); code != http.StatusBadRequest {
		t.Fatalf("мусор: код %d: %v", code, out)
	}

	// [] очищает
	if code, _ := zonesPut(t, srv.URL+"/api/v1/pools/proton", `{"excludes":[]}`); code != http.StatusOK {
		t.Fatal("очистка не прошла")
	}
	p, _ = st.Pool("proton")
	if len(p.Settings.Excludes) != 0 {
		t.Fatalf("очистка не сработала: %v", p.Settings.Excludes)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := ""
	for i > 0 {
		digits = string(rune('0'+i%10)) + digits
		i /= 10
	}
	return digits
}
