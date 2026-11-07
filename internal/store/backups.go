package store

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (s *Store) SaveBackup(name, content string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveBackupLocked(name, content)
}

func (s *Store) saveBackupLocked(name, content string) (string, error) {
	return s.saveBackupLockedExt(name, "conf", content)
}

func (s *Store) saveBackupLockedExt(name, ext, content string) (string, error) {
	if err := os.MkdirAll(s.backupDir(), 0o700); err != nil {
		return "", err
	}
	// частичные бэкапы (.conf.gz) хранятся сжатыми: ~100КБ shadow
	// превращаются в ~10-15КБ (слово владельца: «сделаем сжатие и
	// хранить в сжатом виде»). tar.gz уже сжат - второй раз не греем
	if ext == "conf.gz" {
		if gz, err := gzipBytes([]byte(content)); err == nil {
			content = string(gz)
		}
	}
	base := fmt.Sprintf("%s-%s", name, time.Now().Format("20060102-150405"))
	path := filepath.Join(s.backupDir(), base+"."+ext)
	// две записи в одну секунду: не перетираем первую копию - суффикс
	if _, err := os.Stat(path); err == nil {
		for i := 1; ; i++ {
			p := filepath.Join(s.backupDir(), fmt.Sprintf("%s-%02d.%s", base, i, ext))
			if _, err := os.Stat(p); os.IsNotExist(err) {
				path = p
				break
			}
		}
	}
	return path, os.WriteFile(path, []byte(content), 0o600)
}

func gzipBytes(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// BackupKeep - сколько последних копий каждого вида хранить (настройка
// вкладки «Резервные копии», 2..10).
func (s *Store) BackupKeep() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root.Settings.BackupKeep <= 0 {
		return 10
	}
	return s.root.Settings.BackupKeep
}

// SaveBackupRotated пишет бэкап (name-YYYYMMDD-HHMMSS.ext) и подчищает
// старые копии того же имени, оставляя BackupKeep свежих: авто-бэкапы
// перед массовыми записями сыплются часто - без ротации каталог
// распухает на флеше роутера.
func (s *Store) SaveBackupRotated(name, ext, content string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.saveBackupLockedExt(name, ext, content)
	if err != nil {
		return "", err
	}
	s.rotateBackupsLocked(name, s.root.Settings.BackupKeep)
	return path, nil
}

// EnforceBackupRetention - немедленная чистка старых копий по настройке:
// вызывается PUT /mt/backup-keep, эффект виден сразу, а не при
// следующем бэкапе.
func (s *Store) EnforceBackupRetention() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, kind := range []string{"mt-shadow", "mt-config", "mawg-full"} {
		s.rotateBackupsLocked(kind, s.root.Settings.BackupKeep)
	}
}

// rotateBackupsLocked - оставить keep самых свежих файлов с префиксом
// name- в любом формате (переход .conf -> .conf.gz считает обе серии
// вместе). Вызов под s.mu.
func (s *Store) rotateBackupsLocked(name string, keep int) {
	if keep <= 0 {
		keep = 10
	}
	entries, err := os.ReadDir(s.backupDir())
	if err != nil {
		return
	}
	prefix := name + "-"
	var files []string
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, prefix) {
			files = append(files, n)
		}
	}
	// свежее = лексически больше (формат метки времени сортируется сам)
	sort.Strings(files)
	for i, j := 0, len(files)-1; i < j; i, j = i+1, j-1 {
		files[i], files[j] = files[j], files[i]
	}
	if len(files) > keep {
		for _, n := range files[keep:] {
			os.Remove(filepath.Join(s.backupDir(), n))
		}
	}
}

// migrateBackupCompression - старые несжатые бэкапы (*.conf) упаковывает
// в gzip (*.conf.gz): tar.gz уже сжат и не матчится, чужие файлы не
// трогаем. Идемпотентно; mtime сохраняем, чтобы порядок в панели не
// уехал. Запускается при старте mawg (слово владельца: «чтобы mawg
// автоматом их упаковал и сжал и подготовил»).
func (s *Store) migrateBackupCompression() {
	entries, err := os.ReadDir(s.backupDir())
	if err != nil {
		return
	}
	n := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".conf") {
			continue
		}
		path := filepath.Join(s.backupDir(), name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		gz, err := gzipBytes(data)
		if err != nil {
			continue
		}
		dst := path + ".gz"
		if err := writeAtomically(dst, gz); err != nil {
			continue
		}
		if st, err := os.Stat(path); err == nil {
			_ = os.Chtimes(dst, st.ModTime(), st.ModTime())
		}
		if err := os.Remove(path); err != nil {
			continue
		}
		n++
	}
	if n > 0 {
		s.LogEvent("backup", "migrate", fmt.Sprintf("старые бэкапы упакованы в gzip: %d", n))
	}
}
