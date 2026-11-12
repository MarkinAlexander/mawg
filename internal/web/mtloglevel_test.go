package web

// Кноб «Уровень лога MT» (кейс Вячеслава 12.11): GET отдаёт уровень +
// capable по ревизии форка; PUT проксирует имя уровня, ошибка форка -
// с её текстом (панель показывает, что не применилось).

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"mawg/internal/magitrickle"
	"mawg/internal/platform/fake"
	"mawg/internal/rotator"
	"mawg/internal/store"
)

func mtLogLevelEnv(t *testing.T, mtVersion, level string, putStatus int) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var puts []string
	mt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/version":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"version":"` + mtVersion + `"}`))
		case r.URL.Path == "/api/v1/app/loglevel" && r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"level":"` + level + `"}`))
		case r.URL.Path == "/api/v1/app/loglevel" && r.Method == http.MethodPut:
			buf := make([]byte, 512)
			n, _ := r.Body.Read(buf)
			mu.Lock()
			puts = append(puts, string(buf[:n]))
			mu.Unlock()
			if putStatus != http.StatusOK {
				w.WriteHeader(putStatus)
				_, _ = w.Write([]byte(`{"error":"log level invalid: \"loud\""}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"level":"` + level + `"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(mt.Close)

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fb := fake.New()
	e := rotator.New(st, fb, magitrickle.New("http://127.0.0.1:1"))
	srv := New(st, e, fb, magitrickle.New(mt.URL), "test", nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, &puts
}

func TestMtLogLevelGetAndSet(t *testing.T) {
	ts, puts := mtLogLevelEnv(t, "0.8.2-rev22", "info", http.StatusOK)

	got := httpGetJSON(t, ts.URL+"/api/v1/mt/loglevel")
	if got["mtCapable"] != true || got["level"] != "info" {
		t.Fatalf("GET: want capable=true level=info, got %v", got)
	}

	resp, err := httpPut(ts.URL+"/api/v1/mt/loglevel", `{"level":"debug"}`)
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT: status %d, body %s", resp.StatusCode, body)
	}
	if len(*puts) != 1 || !strings.Contains((*puts)[0], `"level":"debug"`) {
		t.Fatalf("форк не получил level: %v", *puts)
	}
}

func TestMtLogLevelForkErrorPassthrough(t *testing.T) {
	ts, _ := mtLogLevelEnv(t, "0.8.2-rev22", "info", http.StatusBadRequest)

	resp, err := httpPut(ts.URL+"/api/v1/mt/loglevel", `{"level":"loud"}`)
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(resp)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("кривой уровень: want 502, got %d", resp.StatusCode)
	}
	if !strings.Contains(body, "log level invalid") {
		t.Fatalf("текст ошибки форка потерян: %s", body)
	}
}

func TestMtLogLevelOldForkNotCapable(t *testing.T) {
	ts, _ := mtLogLevelEnv(t, "0.8.2-rev21", "info", http.StatusOK)

	got := httpGetJSON(t, ts.URL+"/api/v1/mt/loglevel")
	if got["mtCapable"] != false {
		t.Fatalf("rev21: capable=false, got %v", got)
	}
	if got["level"] != "" {
		t.Fatalf("rev21: уровень пустой, got %v", got["level"])
	}
}
