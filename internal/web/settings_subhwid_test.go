package web

// Идентификация установки для подписок с лимитом устройств (Remnawave
// x-hwid): свой стабильный hwid, ручной из панели, валидация формата.

import (
	"strings"
	"testing"
)

func TestSubHwidEndpoints(t *testing.T) {
	ts, _, _ := newImportTestServer(t)

	code, raw := httpDo(t, ts.URL, "GET", "/api/v1/settings/sub-hwid", "")
	if code != 200 || !strings.Contains(string(raw), `"effective":"mawg-`) {
		t.Fatalf("get: %d %s", code, raw)
	}
	// мусорный формат панели не проходит валидатор - отказ, а не тихая
	// регистрация неработающего устройства
	code, raw = httpDo(t, ts.URL, "PUT", "/api/v1/settings/sub-hwid", `{"hwid":"короткий"}`)
	if code == 200 {
		t.Fatalf("мусорный hwid принят: %d %s", code, raw)
	}
	// ручной приоритетнее своего
	code, raw = httpDo(t, ts.URL, "PUT", "/api/v1/settings/sub-hwid", `{"hwid":"myhappdevice123"}`)
	if code != 200 {
		t.Fatalf("put: %d %s", code, raw)
	}
	code, raw = httpDo(t, ts.URL, "GET", "/api/v1/settings/sub-hwid", "")
	if code != 200 || !strings.Contains(string(raw), `"manual":"myhappdevice123"`) ||
		!strings.Contains(string(raw), `"effective":"myhappdevice123"`) {
		t.Fatalf("после put: %d %s", code, raw)
	}
	// очистка возвращает к своему (без ротации последнего)
	code, raw = httpDo(t, ts.URL, "PUT", "/api/v1/settings/sub-hwid", `{"hwid":""}`)
	if code != 200 {
		t.Fatalf("clear: %d %s", code, raw)
	}
	code, raw = httpDo(t, ts.URL, "GET", "/api/v1/settings/sub-hwid", "")
	if code != 200 || strings.Contains(string(raw), `"manual":"myhappdevice123"`) ||
		!strings.Contains(string(raw), `"effective":"mawg-`) {
		t.Fatalf("после clear: %d %s", code, raw)
	}
}
