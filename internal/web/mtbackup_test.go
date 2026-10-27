package web

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
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
	n      int // счётчик ID для групп без ID - как настоящий MT
	// mtVersion имитирует /api/v1/version форка (rev5+); пусто = старый
	// бинарь без эндпоинта (404) - тестирует configVersion-фолбэк
	mtVersion string
}

func restoreMTServer(t *testing.T) (*httptest.Server, *restoreMTState) {
	t.Helper()
	st := &restoreMTState{groups: []magitrickle.Group{
		{ID: "old1", Name: "старая", Enable: true},
	}, mtVersion: "0.8.2-rev4-mawgfix2"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		defer st.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/version":
			if st.mtVersion == "" {
				w.WriteHeader(404)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"version": st.mtVersion})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/groups":
			json.NewEncoder(w).Encode(map[string]any{"groups": st.groups})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/groups":
			var req magitrickle.Group
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(400)
				return
			}
			if req.ID == "" {
				st.n++
				req.ID = fmt.Sprintf("mig%d", st.n)
			}
			st.groups = append(st.groups, req)
			json.NewEncoder(w).Encode(req)
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/groups":
			var req struct {
				Groups []magitrickle.Group `json:"groups"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(400)
				return
			}
			// настоящий MT назначает ID новым группам (как в моке каскадов)
			for i := range req.Groups {
				if req.Groups[i].ID == "" {
					st.n++
					req.Groups[i].ID = fmt.Sprintf("mig%d", st.n)
				}
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
	// ручная кнопка теперь кладёт и полный архив - shadow-запись ищем по kind
	var shadowKind string
	for _, b := range list.Backups {
		if strings.HasPrefix(b.Name, "mt-shadow-") {
			shadowKind = b.Kind
		}
	}
	if shadowKind != "shadow" {
		t.Fatalf("список бэкапов: %+v", list.Backups)
	}
	resp, err = http.Get(ts.URL + "/api/v1/mt/backups/" + name)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	// скачивается сжатый .conf.gz - распаковываем и сверяем содержимое
	if !strings.HasSuffix(name, ".conf.gz") {
		t.Fatalf("новый бэкап должен быть сжат: %s", name)
	}
	zr, err := gzip.NewReader(bytes.NewReader(got))
	if err != nil {
		t.Fatalf("скачивание: не gzip: %v", err)
	}
	unpacked, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || !strings.Contains(string(unpacked), "спасённая") {
		t.Fatalf("скачивание: status %d, body %q", resp.StatusCode, unpacked)
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

func TestMTBackupFullAndRestore(t *testing.T) {
	magitrickle.SetShadowStorage(nil, nil)
	// рестарт панели: подменяем init-скрипт на пустой файл в temp
	script := filepath.Join(t.TempDir(), "init-mawg")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	origScript := mawgServiceScript
	mawgServiceScript = func(bool) string { return script }
	t.Cleanup(func() { mawgServiceScript = origScript })

	mtSrv, _ := restoreMTServer(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// config.json "прошлой жизни" с другим портом + пул с файлом
	oldConfig := map[string]any{"settings": map[string]any{"webPort": 9999, "listenAddr": "127.0.0.1"}}
	raw, _ := json.Marshal(oldConfig)
	if err := os.WriteFile(filepath.Join(st.Base(), "config.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	poolDir := filepath.Join(st.Base(), "pools", "proton")
	if err := os.MkdirAll(poolDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(poolDir, "proton.conf"), []byte("[Interface]"), 0o600); err != nil {
		t.Fatal(err)
	}

	fb := fake.New()
	e := rotator.New(st, fb, magitrickle.New(mtSrv.URL))
	srv := New(st, e, fb, magitrickle.New(mtSrv.URL), "test", nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// ручной бэкап: полный архив появился, в списке kind full
	resp, err := http.Post(ts.URL+"/api/v1/mt/backup", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("backup: status %d", resp.StatusCode)
	}
	entries, _ := os.ReadDir(filepath.Join(st.Base(), "backups"))
	var full string
	for _, en := range entries {
		if strings.HasPrefix(en.Name(), "mawg-full-") {
			full = en.Name()
		}
	}
	if full == "" {
		t.Fatal("mawg-full архив не появился")
	}
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
	var fullKind string
	for _, b := range list.Backups {
		if b.Name == full {
			fullKind = b.Kind
		}
	}
	if fullKind != "full" {
		t.Fatalf("kind полного бэкапа: %q", fullKind)
	}

	// config.json "испортили" (порт вернулся к дефолту) - рестор вернёт 9999
	if err := os.WriteFile(filepath.Join(st.Base(), "config.json"),
		[]byte(`{"settings":{"webPort":8090}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	resp, err = http.Post(ts.URL+"/api/v1/mt/restore", "application/json",
		strings.NewReader(`{"file":"`+full+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "restarting") {
		t.Fatalf("restore: status %d, body %s", resp.StatusCode, body)
	}
	got, err := os.ReadFile(filepath.Join(st.Base(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "9999") {
		t.Fatalf("config.json после рестора: %s", got)
	}
	gotPool, err := os.ReadFile(filepath.Join(st.Base(), "pools", "proton", "proton.conf"))
	if err != nil || string(gotPool) != "[Interface]" {
		t.Fatalf("пул после рестора: %s, err %v", gotPool, err)
	}

	// мусорный архив под видом бэкапа - отказ, состояние не тронуто
	junk := filepath.Join(st.Base(), "backups", "mawg-full-20990101-000000.tar.gz")
	if err := os.WriteFile(junk, []byte("not a gzip"), 0o600); err != nil {
		t.Fatal(err)
	}
	resp, err = http.Post(ts.URL+"/api/v1/mt/restore", "application/json",
		strings.NewReader(`{"file":"`+filepath.Base(junk)+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("мусорный архив прошёл: status %d", resp.StatusCode)
	}
	got, _ = os.ReadFile(filepath.Join(st.Base(), "config.json"))
	if !strings.Contains(string(got), "9999") {
		t.Fatal("мусорный архив испортил config.json")
	}
}

// старые несжатые .conf-бэкапы (до сжатия, в т.ч. скачанные на ПК)
// восстанавливаются как раньше; новые .conf.gz - распаковываются
func TestMTRestorePlainAndGzipBackups(t *testing.T) {
	mtSrv, mtState := restoreMTServer(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fb := fake.New()
	e := rotator.New(st, fb, magitrickle.New(mtSrv.URL))
	srv := New(st, e, fb, magitrickle.New(mtSrv.URL), "test", nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	groupsJSON := func(name string) string {
		return `{"groups":[{"name":"` + name + `","enable":true,"interface":"nwg1"}]}`
	}
	plain := "mt-shadow-20261001-120000.conf"
	gzName := "mt-shadow-20261002-120000.conf.gz"
	if err := os.WriteFile(filepath.Join(st.Base(), "backups", plain), []byte(groupsJSON("из-старого")), 0o600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write([]byte(groupsJSON("из-сжатого")))
	zw.Close()
	if err := os.WriteFile(filepath.Join(st.Base(), "backups", gzName), buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	// старый несжатый
	resp, err := http.Post(ts.URL+"/api/v1/mt/restore", "application/json", strings.NewReader(`{"file":"`+plain+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("рестор старого .conf: %d %s", resp.StatusCode, body)
	}
	mtState.mu.Lock()
	restored := len(mtState.groups) == 1 && mtState.groups[0].Name == "из-старого"
	mtState.mu.Unlock()
	if !restored {
		t.Fatalf("старый бэкап не восстановился: %+v", mtState.groups)
	}

	// новый сжатый
	resp, err = http.Post(ts.URL+"/api/v1/mt/restore", "application/json", strings.NewReader(`{"file":"`+gzName+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("рестор .conf.gz: %d %s", resp.StatusCode, body)
	}
	mtState.mu.Lock()
	restored = len(mtState.groups) == 1 && mtState.groups[0].Name == "из-сжатого"
	mtState.mu.Unlock()
	if !restored {
		t.Fatalf("сжатый бэкап не восстановился: %+v", mtState.groups)
	}
}

// ретеншен: PUT вне диапазона отбивается, в диапазоне применяется
// сразу (лишние копии чистятся немедленно)
func TestBackupKeepEndpoints(t *testing.T) {
	mtSrv, _ := restoreMTServer(t)
	base := t.TempDir()
	st, err := store.Open(base)
	if err != nil {
		t.Fatal(err)
	}
	fb := fake.New()
	e := rotator.New(st, fb, magitrickle.New(mtSrv.URL))
	srv := New(st, e, fb, magitrickle.New(mtSrv.URL), "test", nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// дефолт
	resp, _ := http.Get(ts.URL + "/api/v1/mt/backup-keep")
	var got struct {
		Keep int `json:"keep"`
	}
	json.NewDecoder(resp.Body).Decode(&got)
	resp.Body.Close()
	if got.Keep != 10 {
		t.Fatalf("дефолт keep = %d, want 10", got.Keep)
	}

	// вне диапазона 2..10 - отказ
	r1, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/mt/backup-keep", strings.NewReader(`{"keep":1}`))
	resp1, err := http.DefaultClient.Do(r1)
	if err != nil {
		t.Fatal(err)
	}
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusBadRequest {
		t.Fatalf("keep=1 должен отбиваться 400, получил %d", resp1.StatusCode)
	}

	// 5 старых копий на диске; ставим keep=2 - лишние чистятся сразу
	dir := filepath.Join(base, "backups")
	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("mt-config-20261001-12000%d.conf.gz", i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r2, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/mt/backup-keep", strings.NewReader(`{"keep":2}`))
	resp2, err := http.DefaultClient.Do(r2)
	if err != nil {
		t.Fatal(err)
	}
	json.NewDecoder(resp2.Body).Decode(&got)
	resp2.Body.Close()
	if resp2.StatusCode != 200 || got.Keep != 2 {
		t.Fatalf("keep=2: %d %+v", resp2.StatusCode, got)
	}
	entries, _ := os.ReadDir(dir)
	n := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "mt-config-") {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("после keep=2 копий %d, want 2", n)
	}
}
