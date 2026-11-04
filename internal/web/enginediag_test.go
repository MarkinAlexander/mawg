package web

// Диагностика движков (112-док 3.7): маскировка секретов в бандле и сам
// бандл через HTTP.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mawg/internal/magitrickle"
	"mawg/internal/platform/fake"
	"mawg/internal/rotator"
	"mawg/internal/store"
)

func TestMaskSecrets(t *testing.T) {
	in := `"uuid": "a1b2c3d4-1111-2222-3333-444455556666",
"password": "hunter2",
private_key: "q9Xk1AbCdefGHIjklMNOpqrsTUVwxyz0123456789stu="
"public_key": "X25519pbkValue1234567890abcdefghijklmnopqrstuv",
"server": "203.0.113.10"`
	out := maskSecrets(in)
	for _, leak := range []string{"a1b2c3d4", "hunter2", "q9Xk1AbCdef", "X25519pbkValue"} {
		if strings.Contains(out, leak) {
			t.Fatalf("секрет %q утёк: %s", leak, out)
		}
	}
	// не-секреты остаются (нужны для разбора)
	for _, keep := range []string{"203.0.113.10", "uuid", "public_key"} {
		if !strings.Contains(out, keep) {
			t.Fatalf("«%s» замаскирован зря: %s", keep, out)
		}
	}
}

func TestGetDiagnosticsBundle(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fb := fake.New()
	e := rotator.New(st, fb, magitrickle.New("http://127.0.0.1:1"))
	srv := New(st, e, fb, nil, "test-ver", nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/system/diagnostics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("code=%d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Fatalf("content-type: %s", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	s := string(body)
	for _, sect := range []string{"===== mawg =====", "===== движки =====", "===== движковые пулы =====", "===== события"} {
		if !strings.Contains(s, sect) {
			t.Fatalf("в бандле нет секции %q:\n%s", sect, s)
		}
	}
	if !strings.Contains(s, "test-ver") {
		t.Fatal("версия mawg не попала в бандл")
	}
}
