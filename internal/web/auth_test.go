package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mawg/internal/auth"
	"mawg/internal/magitrickle"
	"mawg/internal/platform/fake"
	"mawg/internal/rotator"
	"mawg/internal/store"
)

func newAuthedServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	base := t.TempDir()
	a := auth.New(base, true)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fb := fake.New()
	e := rotator.New(st, fb, magitrickle.New("http://127.0.0.1:1"))
	srv := New(st, e, fb, nil, "test", a)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	data, err := os.ReadFile(filepath.Join(base, "first-auth.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var pass string
	for _, ln := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(ln, "пароль: ") {
			pass = strings.TrimPrefix(ln, "пароль: ")
		}
	}
	if pass == "" {
		t.Fatal("нет пароля первого входа")
	}
	return ts, pass
}

func TestAuthFlow(t *testing.T) {
	ts, pass := newAuthedServer(t)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	// статика открыта без сессии
	resp, err := client.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("статика: %d", resp.StatusCode)
	}

	// API без сессии - 401
	resp, err = client.Get(ts.URL + "/api/v1/status")
	if err != nil {
		t.Fatal(err)
	}
	var e1 struct {
		Error string `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&e1)
	resp.Body.Close()
	if resp.StatusCode != 401 || e1.Error != "требуется вход" {
		t.Fatalf("без сессии: %d %q", resp.StatusCode, e1.Error)
	}

	// неверный логин
	resp, err = client.Post(ts.URL+"/api/v1/auth/login", "application/json",
		strings.NewReader(`{"login":"admin","password":"nope"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("неверный логин: %d", resp.StatusCode)
	}

	// верный вход
	resp, err = client.Post(ts.URL+"/api/v1/auth/login", "application/json",
		strings.NewReader(`{"login":"admin","password":"`+pass+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("вход: %d", resp.StatusCode)
	}

	// API с сессией - 200
	resp, err = client.Get(ts.URL + "/api/v1/status")
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		AuthEnabled bool `json:"authEnabled"`
	}
	json.NewDecoder(resp.Body).Decode(&st)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("статус с сессией: %d", resp.StatusCode)
	}
	if !st.AuthEnabled {
		t.Fatal("authEnabled=false при включённой авторизации")
	}

	// rcitoken не отдаёт токен
	resp, err = client.Get(ts.URL + "/api/v1/rcitoken")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || strings.Contains(string(raw), `"token"`) {
		t.Fatalf("rcitoken: %d %s", resp.StatusCode, raw)
	}

	// выход -> снова 401
	resp, err = client.Post(ts.URL+"/api/v1/auth/logout", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	resp, err = client.Get(ts.URL + "/api/v1/status")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("после выхода: %d", resp.StatusCode)
	}
}

func TestAuthDisabledOpensAPI(t *testing.T) {
	base := t.TempDir()
	a := auth.New(base, false)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fb := fake.New()
	e := rotator.New(st, fb, magitrickle.New("http://127.0.0.1:1"))
	srv := New(st, e, fb, nil, "test", a)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("с -no-auth API должен быть открыт: %d", resp.StatusCode)
	}
}

func TestBodyLimit(t *testing.T) {
	ts, pass := newAuthedServer(t)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	resp, err := client.Post(ts.URL+"/api/v1/auth/login", "application/json",
		strings.NewReader(`{"login":"admin","password":"`+pass+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// 2МБ JSON превышает лимит 1МБ
	big := `{"name":"` + strings.Repeat("x", 2<<20) + `"}`
	resp, err = client.Post(ts.URL+"/api/v1/pools", "application/json", strings.NewReader(big))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == 200 {
		t.Fatal("2МБ тело принято при лимите 1МБ")
	}
}
