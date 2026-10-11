package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveBackupRotatedKeepsLastN(t *testing.T) {
	base := t.TempDir()
	st, err := Open(base)
	if err != nil {
		t.Fatal(err)
	}
	// тринадцать записей с разными метками - ротация обязана оставить 10
	for i := 0; i < 13; i++ {
		if _, err := st.SaveBackupRotated("mt-shadow", "payload", 10); err != nil {
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
	if n != 10 {
		t.Fatalf("после ротации копий %d, want 10", n)
	}
	// чужие файлы ротация не трогает
	if err := os.WriteFile(filepath.Join(base, "backups", "handmade.conf"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveBackupRotated("mt-shadow", "payload", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(base, "backups", "handmade.conf")); err != nil {
		t.Fatal("ротация снесла чужой файл")
	}
}
