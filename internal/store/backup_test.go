package store

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSaveBackupRotatedKeepsLastN(t *testing.T) {
	base := t.TempDir()
	st, err := Open(base)
	if err != nil {
		t.Fatal(err)
	}
	// ретеншен из настроек: выставили 3 - из 13 записей останутся 3
	settings := st.Settings()
	settings.BackupKeep = 3
	if err := st.SetSettings(settings); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 13; i++ {
		if _, err := st.SaveBackupRotated("mt-shadow", "conf.gz", "payload"); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(base, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "mt-shadow-") {
			n++
		}
	}
	if n != 3 {
		t.Fatalf("после ротации копий %d, want 3", n)
	}
	// чужие файлы ротация не трогает
	if err := os.WriteFile(filepath.Join(base, "backups", "handmade.conf"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveBackupRotated("mt-shadow", "conf.gz", "payload"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(base, "backups", "handmade.conf")); err != nil {
		t.Fatal("ротация снесла чужой файл")
	}
}

// дефолт без настройки - 10; EnforceBackupRetention чистит сразу
func TestBackupKeepDefaultAndEnforce(t *testing.T) {
	base := t.TempDir()
	st, err := Open(base)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.BackupKeep(); got != 10 {
		t.Fatalf("дефолт BackupKeep = %d, want 10", got)
	}
	dir := filepath.Join(base, "backups")
	for i := 0; i < 5; i++ {
		name := "mt-config-20260101-00000" + string(rune('0'+i)) + ".conf.gz"
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	settings := st.Settings()
	settings.BackupKeep = 2
	if err := st.SetSettings(settings); err != nil {
		t.Fatal(err)
	}
	st.EnforceBackupRetention()
	entries, _ := os.ReadDir(dir)
	n := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "mt-config-") {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("после EnforceRetention копий %d, want 2", n)
	}
}

// conf.gz пишется сжатым: содержимое gzip'ится, читается gunzip'ом
func TestBackupGzipContent(t *testing.T) {
	base := t.TempDir()
	st, err := Open(base)
	if err != nil {
		t.Fatal(err)
	}
	path, err := st.SaveBackupRotated("mt-shadow", "conf.gz", `{"groups":[{"name":"группа"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, ".conf.gz") {
		t.Fatalf("имя файла: %s", path)
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("запись не gzip: %v", err)
	}
	var out bytes.Buffer
	if _, err := out.ReadFrom(zr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"группа"`) {
		t.Fatalf("распакованное содержимое: %s", out.String())
	}
	// сжатие реально уменьшает повторяющийся JSON
	big := strings.Repeat(`{"groups":[{"name":"abc","rules":[{"type":"domain","rule":"x.com"}]}},`, 500)
	path2, err := st.SaveBackupRotated("mt-shadow", "conf.gz", big)
	if err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(path2)
	if fi.Size() >= int64(len(big)) {
		t.Fatalf("gzip не сжал: %d из %d", fi.Size(), len(big))
	}
}

// миграция при старте: старые несжатые .conf упаковываются в .conf.gz
// с сохранением содержимого и mtime; tar.gz и чужие файлы не трогаются
func TestMigrateBackupCompression(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-48 * time.Hour)
	files := map[string]string{
		"mt-shadow-20261001-120000.conf":    `{"groups":[1]}`,
		"mt-config-20261001-120100.conf":    "interface Wireguard1",
		"mawg-full-20261001-120200.tar.gz":  "fake-archive",
		"handmade-20261001-120300.txt":      "не наш",
		"mt-shadow-20261001-120400.conf.gz": "уже сжат",
	}
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, oldTime, oldTime); err != nil {
			t.Fatal(err)
		}
	}

	st, err := Open(base)
	if err != nil {
		t.Fatal(err)
	}

	// несжатые .conf ушли, появились .conf.gz с тем же содержимым
	for _, name := range []string{"mt-shadow-20261001-120000", "mt-config-20261001-120100"} {
		if _, err := os.Stat(filepath.Join(dir, name+".conf")); !os.IsNotExist(err) {
			t.Fatalf("%s.conf должен быть упакован", name)
		}
		raw, err := os.ReadFile(filepath.Join(dir, name+".conf.gz"))
		if err != nil {
			t.Fatal(err)
		}
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("%s.conf.gz не gzip: %v", name, err)
		}
		var out bytes.Buffer
		out.ReadFrom(zr)
		if out.String() != files[name+".conf"] {
			t.Fatalf("%s: содержимое изменилось: %s", name, out.String())
		}
		if fi, _ := os.Stat(filepath.Join(dir, name+".conf.gz")); fi.ModTime().Sub(oldTime) > time.Minute {
			t.Fatalf("%s: mtime не сохранён", name)
		}
	}
	// прочие файлы нетронуты
	for _, name := range []string{"mawg-full-20261001-120200.tar.gz", "handmade-20261001-120300.txt", "mt-shadow-20261001-120400.conf.gz"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s не должен был тронуться: %v", name, err)
		}
	}
	// событие о миграции в журнале
	found := false
	for _, ev := range st.Events() {
		if ev.Pool == "backup" && ev.Kind == "migrate" {
			found = true
		}
	}
	if !found {
		t.Fatal("событие миграции не записано")
	}

	// повторный старт идемпотентен
	if _, err := Open(base); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".conf") {
			t.Fatalf("остался несжатый бэкап: %s", e.Name())
		}
	}
}
