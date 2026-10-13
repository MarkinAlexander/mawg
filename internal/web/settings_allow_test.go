package web

// защита от потери доступа: PUT /settings/server со списком, который не
// покрывает адрес клиента, НЕ отклоняется - адрес клиента дописывается
// в список сам (кейс владельца: вручную добавил чужой IP, сам остался
// за бортом после перезагрузки). Покрытый список проходит без добавок.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mawg/internal/magitrickle"
	"mawg/internal/provision"
	"mawg/internal/store"
)

func putAllow(t *testing.T, s *Server, clientIP, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings/server", strings.NewReader(body))
	req.RemoteAddr = clientIP + ":51000"
	w := httptest.NewRecorder()
	s.putServerSettings(w, req)
	return w
}

func TestPutServerSettingsAutoAppendsClient(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{store: st}

	w := putAllow(t, s, "192.168.10.99", `{"allowedIps":[{"v":"192.168.0.81","on":true}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT отклонён: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		AllowedIPs []store.IPAllow `json:"allowedIps"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	var has81, hasClient bool
	for _, e := range out.AllowedIPs {
		if e.Value == "192.168.0.81" && e.On {
			has81 = true
		}
		if e.Value == "192.168.10.99" && e.On {
			hasClient = true
		}
	}
	if !has81 || !hasClient {
		t.Fatalf("адрес клиента не дописан: %+v", out.AllowedIPs)
	}
	// и в сторе то же самое (после перезагрузки страницы не пропадёт)
	_, _, allowed, _ := st.ServerSettings()
	if len(allowed) != 2 {
		t.Fatalf("в сторе %+v", allowed)
	}
}

func TestPutServerSettingsCoveredListUntouched(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{store: st}

	// подсеть покрывает адрес клиента - дописывать нечего
	w := putAllow(t, s, "192.168.10.99", `{"allowedIps":[{"v":"192.168.10.0/24","on":true}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT отклонён: %d %s", w.Code, w.Body.String())
	}
	_, _, allowed, _ := st.ServerSettings()
	if len(allowed) != 1 || allowed[0].Value != "192.168.10.0/24" {
		t.Fatalf("лишние записи: %+v", allowed)
	}
}

// системная проверка: у MagiTrickle показывается ревизия РАБОТАЮЩЕГО
// бинаря, а не opkg-пакета (наш форк ставится поверх пакета 0.8.2-1)
func TestMagitrickleVersionEnriched(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{store: st}
	mtSrv, mtState := restoreMTServer(t) // mtVersion 0.8.2-rev4-mawgfix2
	defer mtSrv.Close()
	s.mt = magitrickle.New(mtSrv.URL)

	items := []provision.Item{{ID: "magitrickle", Title: "MagiTrickle (Entware)", Installed: true, Version: "0.8.2-1"}}
	s.enrichMagitrickleVersion(items)
	if items[0].Version != "0.8.2-rev4-mawgfix2 (пакет 0.8.2-1)" {
		t.Fatalf("версия не обогащена: %q", items[0].Version)
	}

	// ревизию определить не удалось (старый бинарь без /version, конфига
	// в синтетике нет) - честная пометка, версия пакета не врёт
	mtState.mu.Lock()
	mtState.mtVersion = ""
	mtState.mu.Unlock()
	items = []provision.Item{{ID: "magitrickle", Installed: true, Version: "0.8.2-1"}}
	s.enrichMagitrickleVersion(items)
	if items[0].Version != "0.8.2-1" || items[0].Note == "" {
		t.Fatalf("ожидали пакет+пометку: %+v", items[0])
	}
}
