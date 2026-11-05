package web

import (
	"os"
	"os/exec"

	"mawg/internal/mtinstall"
)

// Лог MagiTrickle: демон пишет в него каждый DNS-запрос сети, на
// Entware/Keenetic /opt/var/log живёт на USB-флешке - это постоянный
// износ (часовой монитор на живом Кинетике, слово владельца 2026-11-05:
// 300+КБ/час, единственный пишущий на флешку). Переносим в RAM с
// ограничением размера: роутеры бывают со 128МБ памяти, мегабайты лога
// там недопустимы. На OpenWrt /var и так tmpfs - там ничего не делаем.
const (
	mtLogCapBytes  = 256 << 10 // активный лог в RAM выше - ротация
	mtLogKeepBytes = 128 << 10 // хвост, уезжающий в .old при ротации
)

var (
	mtLogFlashPath = "/opt/var/log/magitrickle-go.log"
	mtLogRAMPath   = "/tmp/magitrickle-go.log"
)

// mtLogQuietEnsure - идемпотентный перенос лога MT с флешки в RAM:
// хвост переезжает в /tmp, на месте флеш-файла остаётся symlink (init-
// скрипт демона пишет по пути >> - ядро следует ссылке). Открытый fd
// работающего демона смотрит на старый inode на флешке, поэтому перенос
// завершается одним рестартом MT (разово, при первом срабатывании).
func (s *Server) mtLogQuietEnsure() {
	if mtEntwareAbsent() {
		return
	}
	if fi, err := os.Lstat(mtLogFlashPath); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return // уже перенесено
		}
		if err := mtLogMigrate(mtLogFlashPath, mtLogRAMPath, mtLogKeepBytes); err != nil {
			s.store.LogEvent("magitrickle", "power", "перенос лога MT в RAM не удался: "+err.Error())
			return
		}
		s.store.LogEvent("magitrickle", "power",
			"лог MT перенесён с флешки в RAM ("+mtLogRAMPath+"), демон перезапущен; флешка больше не пишется журналом DNS")
		if init, _, _ := mtinstall.ServicePaths(s.backend.Name()); init != "" {
			_ = exec.Command(init, "restart").Run()
		}
		return
	}
	// лога ещё нет (MT вот-вот поставят) - symlink на будущее
	_ = os.Symlink(mtLogRAMPath, mtLogFlashPath)
}

// mtLogNanny - RAM-лог MT не должен раздувать память: сверх cap хвост
// уезжает в .old, активный файл усекается на месте (copytruncate: fd
// демона открыт с O_APPEND и продолжает писать в тот же inode).
func (s *Server) mtLogNanny() {
	if _, err := mtLogRotate(mtLogRAMPath, mtLogCapBytes, mtLogKeepBytes); err != nil {
		// ротация не критична - молча ждём следующего тика
		_ = err
	}
}

func mtEntwareAbsent() bool {
	if _, err := os.Stat("/opt/bin/magitrickled"); err != nil {
		return true
	}
	_, err := os.Stat("/opt/var/log")
	return err != nil
}

// mtLogMigrate - хвост флеш-файла в RAM, на месте флеш-пути symlink.
func mtLogMigrate(flash, ram string, keep int64) error {
	tail, err := readTail(flash, keep)
	if err != nil {
		return err
	}
	if err := os.WriteFile(ram, tail, 0o644); err != nil {
		return err
	}
	if err := os.Remove(flash); err != nil {
		return err
	}
	return os.Symlink(ram, flash)
}

// mtLogRotate - copytruncate-ротация: хвост -> .old, файл усекается.
// rotated=false - файл меньше cap или недоступен.
func mtLogRotate(ram string, cap, keep int64) (bool, error) {
	fi, err := os.Stat(ram)
	if err != nil {
		return false, err
	}
	if fi.Size() <= cap {
		return false, nil
	}
	tail, err := readTail(ram, keep)
	if err != nil {
		return false, err
	}
	if err := os.WriteFile(ram+".old", tail, 0o644); err != nil {
		return false, err
	}
	return true, os.Truncate(ram, 0)
}

func readTail(path string, n int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	var off int64
	if fi.Size() > n {
		off = fi.Size() - n
	}
	buf := make([]byte, fi.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil {
		return nil, err
	}
	return buf, nil
}
