package main

import (
	"net"
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
