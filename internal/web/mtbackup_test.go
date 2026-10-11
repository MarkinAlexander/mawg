package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"mawg/internal/magitrickle"
	"mawg/internal/platform/fake"
	"mawg/internal/rotator"
	"mawg/internal/store"
)

type restoreMTState struct {
	mu     sync.Mutex
	groups []magitrickle.Group
}

func restoreMTServer(t *testing.T) (*httptest.Server, *restoreMTState) {
	t.Helper()
	st := &restoreMTState{groups: []magitrickle.Group{
		{ID: "old1", Name: "старая", Enable: true},
	}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		defer st.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/groups":
			json.NewEncoder(w).Encode(map[string]any{"groups": st.groups})
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/groups":
			var req struct {
				Groups []magitrickle.Group `json:"groups"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(400)
				return
			}
			st.groups = req.Groups
			w.WriteHeader(200)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, st
}

func TestMTBackupAndRestoreShadow(t *testing.T) {
	magitrickle.SetShadowStorage(nil, nil)
	mtSrv, mtState := restoreMTServer(t)

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// "прошлый жизнь": shadow с группами, которые надо спасти
	dump := map[string]any{"groups": []map[string]any{{
		"id": "saved1", "name": "спасённая", "enable": true,
		"rules": []map[string]any{{"id": "r1", "type": "domain", "rule": "example.com", "enable": true}},
	}}}
	raw, _ := json.Marshal(dump)
	if err := os.WriteFile(filepath.Join(st.Base(), "magitrickle-shadow.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	fb := fake.New()
	e := rotator.New(st, fb, magitrickle.New(mtSrv.URL))
	srv := New(st, e, fb, magitrickle.New(mtSrv.URL), "test", nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// ручной бэкап из панели
	resp, err := http.Post(ts.URL+"/api/v1/mt/backup", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("backup: status %d", resp.StatusCode)
	}
	entries, _ := os.ReadDir(filepath.Join(st.Base(), "backups"))
	var name string
	for _, en := range entries {
		if strings.HasPrefix(en.Name(), "mt-shadow-") {
			name = en.Name()
		}
	}
	if name == "" {
		t.Fatal("mt-shadow бэкап не появился")
	}

	// список и скачивание
	resp, err = http.Get(ts.URL + "/api/v1/mt/backups")
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Backups []struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"backups"`
	}
	json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if len(list.Backups) != 1 || list.Backups[0].Kind != "shadow" {
		t.Fatalf("список бэкапов: %+v", list.Backups)
	}
	resp, err = http.Get(ts.URL + "/api/v1/mt/backups/" + name)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(got), "спасённая") {
		t.Fatalf("скачивание: status %d, body %q", resp.StatusCode, got)
	}

	// рантайм MT "потерял" группы (усечение) - восстанавливаем из бэкапа
	mtState.mu.Lock()
	mtState.groups = nil
	mtState.mu.Unlock()
	resp, err = http.Post(ts.URL+"/api/v1/mt/restore", "application/json", strings.NewReader(`{"file":"`+name+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("restore: status %d, body %s", resp.StatusCode, body)
	}
	mtState.mu.Lock()
	defer mtState.mu.Unlock()
	if len(mtState.groups) != 1 || mtState.groups[0].ID != "saved1" {
		raw, _ := json.Marshal(mtState.groups)
		t.Fatalf("рантайм после рестора: %s", raw)
	}
	if len(mtState.groups[0].Rules) != 1 {
		t.Fatal("правила рестора потерялись")
	}
}

func TestMTRestoreRejectsForeignFile(t *testing.T) {
	magitrickle.SetShadowStorage(nil, nil)
	mtSrv, _ := restoreMTServer(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fb := fake.New()
	e := rotator.New(st, fb, magitrickle.New(mtSrv.URL))
	srv := New(st, e, fb, magitrickle.New(mtSrv.URL), "test", nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	for _, file := range []string{"../../etc/passwd", "config.yaml", "mt-shadow-x.conf", ""} {
		resp, err := http.Post(ts.URL+"/api/v1/mt/restore", "application/json",
			strings.NewReader(`{"file":"`+file+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Fatalf("чужое имя %q прошло: status %d", file, resp.StatusCode)
		}
	}
	// скачивание чужого имени тоже закрыто
	resp, err := http.Get(ts.URL + "/api/v1/mt/backups/..%2Fconfig.json")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("traversal в скачивании прошёл: status %d", resp.StatusCode)
	}
}
