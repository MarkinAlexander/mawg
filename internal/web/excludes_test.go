package web

// Исключения из обработки (94-док): REST-валидация глобального списка и
// исключений пула (PUT /pools/{name}).

import (
	"encoding/json"
	"io"
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

// Гейт выключенного функционала (95-док, слово владельца 30.10):
// PUT /excludes -> 503 с текстом владельца, GET отдаёт список как было;
// excludes в PUT /pools/{name} -> 400 (без поля - ок); переключатель
// пишет флаг и событие; /status несёт флаг для UI.
func TestExcludesDisabledGate(t *testing.T) {
	srv, _, st := zonesServer(t, "0.8.2-rev15-mawg-dev")

	if code, out := excludesPut(t, srv, `{"excludes":["192.0.2.7"]}`); code != http.StatusOK {
		t.Fatalf("код %d: %v", code, out)
	}
	if err := st.SetExcludesDisabled(true); err != nil {
		t.Fatal(err)
	}

	// PUT глобального списка -> 503 с текстом гейта
	req, err := http.NewRequest(http.MethodPut, srv.URL+"/api/v1/excludes", strings.NewReader(`{"excludes":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("код %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "ростом нагрузки на CPU") {
		t.Fatalf("текст гейта: %s", body)
	}
	if got := excludesGet(t, srv); len(got) != 1 || got[0] != "192.0.2.7/32" {
		t.Fatalf("отказанный PUT затёр список: %v", got)
	}

	// excludes в PUT пула -> 400, список пула не тронут; без поля - ок
	code, out := zonesPut(t, srv.URL+"/api/v1/pools/proton", `{"excludes":["203.0.113.9"]}`)
	if code != http.StatusBadRequest {
		t.Fatalf("пул: код %d: %v", code, out)
	}
	if msg, _ := out["error"].(string); msg != "" && !strings.Contains(msg, "CPU") {
		t.Fatalf("текст в JSON: %v", out)
	}
	if p, _ := st.Pool("proton"); len(p.Settings.Excludes) != 0 {
		t.Fatalf("пуловые исключения записались: %v", p.Settings.Excludes)
	}
	if code, out = zonesPut(t, srv.URL+"/api/v1/pools/proton", `{"checkIntervalSec":41}`); code != http.StatusOK {
		t.Fatalf("PUT без excludes: код %d: %v", code, out)
	}

	// /status несёт флаг
	resp2, err := http.Get(srv.URL + "/api/v1/status")
	if err != nil {
		t.Fatal(err)
	}
	var status struct {
		ExcludesDisabled bool `json:"excludesDisabled"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if !status.ExcludesDisabled {
		t.Fatal("/status не отдал excludesDisabled")
	}

	// переключатель: событие в журнал + обратное включение
	code, out = zonesPut(t, srv.URL+"/api/v1/excludes/disabled", `{"disabled":false}`)
	if code != http.StatusOK {
		t.Fatalf("переключатель: код %d: %v", code, out)
	}
	if st.ExcludesDisabled() {
		t.Fatal("флаг не снялся")
	}
	found := false
	for _, e := range st.Events() {
		if e.Pool == "settings" && e.Kind == "excludes" && strings.Contains(e.Message, "снова включены") {
			found = true
		}
	}
	if !found {
		t.Fatal("нет события о выключателе")
	}

	// после снятия флага PUT снова работает
	if code, out := excludesPut(t, srv, `{"excludes":["192.0.2.8"]}`); code != http.StatusOK {
		t.Fatalf("код %d: %v", code, out)
	}
	if got := excludesGet(t, srv); len(got) != 1 || got[0] != "192.0.2.8/32" {
		t.Fatalf("после включения фичи список не применился: %v", got)
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
