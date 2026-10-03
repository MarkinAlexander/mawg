package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func filePassword(t *testing.T, base string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(base, "first-auth.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, ln := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(ln, "пароль: ") {
			return strings.TrimPrefix(ln, "пароль: ")
		}
	}
	t.Fatal("в first-auth.txt нет пароля")
	return ""
}

func TestFirstRunGeneratesCredentials(t *testing.T) {
	base := t.TempDir()
	a := New(base, true)
	if !a.FirstRun {
		t.Fatal("FirstRun не выставлен")
	}
	pass := filePassword(t, base)
	if len(pass) < 8 {
		t.Fatalf("пароль слишком короткий: %q", pass)
	}
	if _, err := os.Stat(filepath.Join(base, "auth.json")); err != nil {
		t.Fatalf("auth.json не создан: %v", err)
	}
	if err := os.Chmod(filepath.Join(base, "auth.json"), 0); err == nil {
		os.Chmod(filepath.Join(base, "auth.json"), 0o600)
	}
	// повторный New не должен считать это первым запуском
	a2 := New(base, true)
	if a2.FirstRun {
		t.Fatal("повторный запуск посчитался первым")
	}
}

func TestVerifyAndSessions(t *testing.T) {
	base := t.TempDir()
	a := New(base, true)
	pass := filePassword(t, base)

	ts := httptest.NewServer(http.HandlerFunc(a.HandleLogin))
	defer ts.Close()

	// неверный пароль
	resp, _ := http.Post(ts.URL, "application/json", strings.NewReader(`{"login":"admin","password":"wrong"}`))
	if resp.StatusCode != 401 {
		t.Fatalf("неверный пароль: %d", resp.StatusCode)
	}
	resp.Body.Close()

	// верный - cookie и токен
	resp, _ = http.Post(ts.URL, "application/json",
		strings.NewReader(`{"login":"admin","password":"`+pass+`"}`))
	if resp.StatusCode != 200 {
		t.Fatalf("верный пароль: %d", resp.StatusCode)
	}
	var out struct {
		Token string `json:"token"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if out.Token == "" {
		t.Fatal("токен пустой")
	}

	// доступ с токеном
	req, _ := http.NewRequest("GET", "http://x/", nil)
	req.Header.Set("Authorization", "Bearer "+out.Token)
	if !a.valid(req) {
		t.Fatal("Bearer-токен не принят")
	}
	// без токена
	if a.valid(&http.Request{Header: http.Header{}}) {
		t.Fatal("пустой токен принят")
	}
}

func TestLoginLocksAfterFiveFails(t *testing.T) {
	base := t.TempDir()
	a := New(base, true)
	ts := httptest.NewServer(http.HandlerFunc(a.HandleLogin))
	defer ts.Close()

	for i := 0; i < 5; i++ {
		resp, _ := http.Post(ts.URL, "application/json", strings.NewReader(`{"login":"admin","password":"bad"}`))
		resp.Body.Close()
	}
	resp, _ := http.Post(ts.URL, "application/json", strings.NewReader(`{"login":"admin","password":"bad"}`))
	if resp != nil {
		defer resp.Body.Close()
		if resp.StatusCode != 429 {
			t.Fatalf("после 5 неудач ждали 429, получили %d", resp.StatusCode)
		}
	} else {
		t.Fatal("6-й запрос не выполнен")
	}
}

func TestPasswordChangeFlow(t *testing.T) {
	base := t.TempDir()
	a := New(base, true)
	pass := filePassword(t, base)

	// смена через API: войти, сменить
	ts := httptest.NewServer(http.HandlerFunc(a.HandleLogin))
	defer ts.Close()
	resp, _ := http.Post(ts.URL, "application/json",
		strings.NewReader(`{"login":"admin","password":"`+pass+`"}`))
	var out struct {
		Token string `json:"token"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()

	pts := httptest.NewServer(http.HandlerFunc(a.HandlePassword))
	defer pts.Close()
	client := &http.Client{}
	req, _ := http.NewRequest("POST", pts.URL, strings.NewReader(`{"old":"bad","password":"newpass123"}`))
	req.Header.Set("Authorization", "Bearer "+out.Token)
	resp2, _ := client.Do(req)
	if resp2.StatusCode != 401 {
		t.Fatalf("смена с неверным старым: %d", resp2.StatusCode)
	}
	resp2.Body.Close()

	req, _ = http.NewRequest("POST", pts.URL, strings.NewReader(`{"old":"`+pass+`","password":"newpass123"}`))
	req.Header.Set("Authorization", "Bearer "+out.Token)
	resp2, _ = client.Do(req)
	if resp2.StatusCode != 200 {
		t.Fatalf("смена с верным старым: %d", resp2.StatusCode)
	}
	resp2.Body.Close()

	if _, err := os.Stat(filepath.Join(base, "first-auth.txt")); !os.IsNotExist(err) {
		t.Fatal("first-auth.txt не удалён после смены пароля")
	}

	// короткий пароль отклоняется
	req, _ = http.NewRequest("POST", pts.URL, strings.NewReader(`{"old":"newpass123","password":"123"}`))
	req.Header.Set("Authorization", "Bearer "+out.Token)
	resp2, _ = client.Do(req)
	resp2.Body.Close()
	if resp2.StatusCode != 400 {
		t.Fatalf("короткий пароль: %d", resp2.StatusCode)
	}
}

func TestResetPasswordCLI(t *testing.T) {
	base := t.TempDir()
	a := New(base, true)
	if err := a.ResetPassword("freshpass9"); err != nil {
		t.Fatal(err)
	}
	a2 := New(base, true)
	if !verifyHash(a2.hash, "freshpass9") {
		t.Fatal("сброшенный пароль не работает")
	}
	if err := a.ResetPassword("short"); err == nil {
		t.Fatal("короткий пароль принят")
	}
}

func TestLoginPicksUpExternalPasswordChange(t *testing.T) {
	base := t.TempDir()
	a := New(base, true)
	_ = filePassword(t, base)
	ts := httptest.NewServer(http.HandlerFunc(a.HandleLogin))
	defer ts.Close()

	// другой инстанс (как CLI -reset-auth) меняет файл
	Open(base).ResetPassword("external-pass-1")

	resp, err := http.Post(ts.URL, "application/json",
		strings.NewReader(`{"login":"admin","password":"external-pass-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("логин со свежим файлом: %d (ждём 200 без рестарта)", resp.StatusCode)
	}
}
