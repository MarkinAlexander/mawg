package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// portTaken: живой слушатель = занят; свободный порт = свободен
func TestPortTaken(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	busy := ln.Addr().(*net.TCPAddr).Port
	if !portTaken("127.0.0.1", busy) {
		t.Fatalf("занятый порт %d не распознан", busy)
	}
	// подбираем заведомо свободный (тот же механизм, что у ядра)
	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	freePort := free.Addr().(*net.TCPAddr).Port
	free.Close()
	if portTaken("127.0.0.1", freePort) {
		t.Fatalf("свободный порт %d помечен занятым", freePort)
	}
	// wildcard-адрес "[::]" не должен ломать JoinHostPort
	if portTaken("[::]", freePort) {
		t.Fatalf("[::] со свободным портом помечен занятым")
	}
}

// Статус обязан работать на битой базе (nil-deref в ServerSettings ронял
// команду паникой - живой кейс друга владельца на OpenWrt): битый json
// после сбоя питания = честное предупреждение, дефолтный порт, код 0.
func TestCmdStatusBrokenStore(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{обрезанный json после сбоя питания"), 0o600); err != nil {
		t.Fatal(err)
	}
	// init-скрипт платформы в тесте не находится - exec вернёт ошибку,
	// alive=false: валидный путь вывода предупреждения
	code := cmdStatus("openwrt", dir, 0)
	if code != 0 {
		t.Fatalf("статус на битой базе должен возвращать 0, получил %d", code)
	}
}

// Чистая пустая база - статус не падает, portOverride учитывается.
func TestCmdStatusHealthyStore(t *testing.T) {
	dir := t.TempDir()
	if code := cmdStatus("openwrt", dir, 8123); code != 0 {
		t.Fatalf("код %d", code)
	}
}
