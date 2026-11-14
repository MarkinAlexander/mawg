package web

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

// "auto" - синоним пустого workdir (диалог установки слал литерал 'auto',
// валидация отвергала и установка lx падала; регрессия 10.3-10.5,
// кейс владельца 14.11). Неизвестный компонент - нормализация прошла,
// ошибка не про workdir; кривое значение - отвергнуто.
func TestInstallWorkdirAutoSynonym(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fb := fake.New()
	e := rotator.New(st, fb, magitrickle.New("http://127.0.0.1:1"))
	srv := New(st, e, fb, nil, "test", nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	postInstall := func(body string) (int, map[string]any) {
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/system/install", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	code, out := postInstall(`{"id":"zzz","workdir":"auto"}`)
	body := toJSON(out)
	if strings.Contains(body, "workdir:") {
		t.Fatalf("auto должен быть синонимом пустого: %s", body)
	}
	_ = code

	code, out = postInstall(`{"id":"zzz","workdir":"флешка"}`)
	if code != http.StatusBadRequest || !strings.Contains(toJSON(out), "workdir:") {
		t.Fatalf("кривой workdir должен отвергаться: %d %s", code, toJSON(out))
	}
}

func toJSON(m map[string]any) string {
	b, _ := json.Marshal(m)
	return string(b)
}
