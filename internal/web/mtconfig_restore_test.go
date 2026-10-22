package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mawg/internal/magitrickle"
	"mawg/internal/platform/fake"
	"mawg/internal/rotator"
	"mawg/internal/store"
)

// фейк-платформа с НАСТОЯЩИМ путём конфига (для mt-config-рестора)
type mtConfigBackend struct {
	*fake.Fake
	path string
}

func (b *mtConfigBackend) MTConfigPath() string { return b.path }

// mt-config-рестор перезаписывает конфиг ЦЕЛИКОМ: перед этим текущий
// конфиг уходит в авто-бэкап - откат возможен без чужих бэкапов
// (кейс Кинетика: рестор старого бэкапа сбросил настройки, 84-док)
func TestMTConfigRestoreAutoBackup(t *testing.T) {
	magitrickle.SetShadowStorage(nil, nil)
	mtSrv, _ := restoreMTServer(t)

	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	original := []byte("groups:\n- id: aaaabbbb\n  old: true\n")
	if err := os.WriteFile(cfg, original, 0o600); err != nil {
		t.Fatal(err)
	}

	fb := &mtConfigBackend{Fake: fake.New(), path: cfg}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := rotator.New(st, fb, magitrickle.New(mtSrv.URL))
	srv := New(st, e, fb, magitrickle.New(mtSrv.URL), "test", nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	name := "mt-config-20261022-120000.conf"
	if err := os.MkdirAll(filepath.Join(st.Base(), "backups"), 0o700); err != nil {
		t.Fatal(err)
	}
	updated := []byte("groups:\n- id: ccccdddd\n  new: true\n")
	if err := os.WriteFile(filepath.Join(st.Base(), "backups", name), updated, 0o600); err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/mt/restore", strings.NewReader(`{"file":"`+name+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("restore: status %d", resp.StatusCode)
	}

	// на диске - конфиг из бэкапа
	got, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(updated) {
		t.Fatalf("конфиг не заменён: %q", got)
	}

	// авто-бэкап: новый mt-config-*.conf с ПРЕЖНИМ содержимым
	entries, err := os.ReadDir(filepath.Join(st.Base(), "backups"))
	if err != nil {
		t.Fatal(err)
	}
	autos := 0
	for _, en := range entries {
		if !strings.HasPrefix(en.Name(), "mt-config-") || en.Name() == name {
			continue
		}
		autos++
		b, err := os.ReadFile(filepath.Join(st.Base(), "backups", en.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != string(original) {
			t.Fatalf("авто-бэкап %s не совпал с прежним конфигом: %q", en.Name(), b)
		}
	}
	if autos != 1 {
		t.Fatalf("авто-бэкапов %d, хочу 1", autos)
	}
}
