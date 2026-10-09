package rotator

import (
	"net"
	"testing"
)

func TestBlockedIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.0.1", "169.254.1.1",
		"100.64.0.1", "100.127.255.255", "224.0.0.1", "255.255.255.255",
		"0.0.0.0", "::1", "fe80::1", "fc00::1", "ff02::1",
	}
	for _, s := range blocked {
		if !blockedIP(net.ParseIP(s)) {
			t.Fatalf("%s должен быть заблокирован", s)
		}
	}
	allowed := []string{"8.8.8.8", "1.1.1.1", "149.154.160.1", "2606:4700::1", "100.63.255.255", "100.128.0.1"}
	for _, s := range allowed {
		if blockedIP(net.ParseIP(s)) {
			t.Fatalf("%s не должен блокироваться", s)
		}
	}
}

func TestSafeAddrs(t *testing.T) {
	// публичный v4 - единственный адрес
	addrs, err := safeAddrs("8.8.8.8", "443")
	if err != nil || len(addrs) != 1 || addrs[0] != "8.8.8.8:443" {
		t.Fatalf("v4: %v %v", addrs, err)
	}
	// публичный v6 - валидная цель (интерфейсы без v6 отсеются перебором)
	addrs, err = safeAddrs("2606:4700::1", "443")
	if err != nil || len(addrs) != 1 || addrs[0] != "[2606:4700::1]:443" {
		t.Fatalf("v6: %v %v", addrs, err)
	}
	// заблокированный - ошибка
	if _, err := safeAddrs("192.168.0.1", "80"); err == nil {
		t.Fatal("приватный адрес должен отклоняться")
	}
}

func TestSafeAddrLiterals(t *testing.T) {
	if _, err := safeAddr("127.0.0.1", "80"); err == nil {
		t.Fatal("loopback должен отклоняться")
	}
	if _, err := safeAddr("192.168.0.1", "80"); err == nil {
		t.Fatal("private должен отклоняться")
	}
	addr, err := safeAddr("8.8.8.8", "80")
	if err != nil || addr != "8.8.8.8:80" {
		t.Fatalf("публичный литерал: %q, %v", addr, err)
	}
}
