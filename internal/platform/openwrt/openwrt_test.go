package openwrt

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// canned: пакетный сбор состояния (PR) - по одному вызову на link/addr
// вместо вызова на каждый интерфейс
func cannedCommand(links, addrs, uci string) func(string, ...string) (string, error) {
	return func(name string, args ...string) (string, error) {
		switch {
		case name == "uci":
			return uci, nil
		case name == "ip" && len(args) > 0 && args[0] == "-4":
			return addrs, nil
		case name == "ip":
			return links, nil
		}
		return "", errors.New("unexpected: " + name)
	}
}

const (
	cannedLinks = "1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536 qdisc noqueue state UNKNOWN\n" +
		"5: proton: <POINTOPOINT,NOARP,UP,LOWER_UP> mtu 1280 qdisc noqueue state UNKNOWN\n" +
		"6: veth0@if7: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc noqueue state UP\n" +
		"7: awgfree: <POINTOPOINT,NOARP> mtu 1280 qdisc noqueue state DOWN\n"
	cannedAddrs = "1: lo    inet 127.0.0.1/8 scope host lo\\       valid_lft forever preferred_lft forever\n" +
		"3: br-lan    inet 192.168.1.1/24 brd 192.168.1.255 scope global br-lan\n" +
		"5: proton    inet 10.2.0.2/32 brd 255.255.255.255 scope global proton\n"
	cannedUCI = "network.loopback=interface\nnetwork.loopback.proto='static'\n" +
		"network.proton=interface\nnetwork.proton.proto='amneziawg'\n" +
		"network.wg0=interface\nnetwork.wg0.proto='wireguard'\n" +
		"network.lan=interface\nnetwork.lan.proto='static'\n"
)

// пакетный снимок: UP/LOWER_UP по флагам, адрес по имени, @-имя без хвоста
func TestInterfaceSnapshot(t *testing.T) {
	got := interfaceSnapshot(cannedCommand(cannedLinks, cannedAddrs, ""))

	p := got["proton"]
	if !p.up || !p.lowerUp || p.address != "10.2.0.2/32" {
		t.Fatalf("proton: %+v", p)
	}
	v := got["veth0"] // @-имя: хвост @if7 срезается
	if !v.up || !v.lowerUp {
		t.Fatalf("veth0: %+v", v)
	}
	a := got["awgfree"] // без UP - оба флага false
	if a.up || a.lowerUp {
		t.Fatalf("awgfree: %+v", a)
	}
	if _, ok := got["lo"]; !ok {
		t.Fatal("lo потерян")
	}
}

// слоты WireGuard/AWG из пакетного снимка: linkUp/address, прочие протоколы
// и loopback - мимо
func TestSlotsImplBatched(t *testing.T) {
	links := "5: wg0: <POINTOPOINT,UP,LOWER_UP> mtu 1420 qdisc noqueue state UNKNOWN\n" +
		"6: awg0: <POINTOPOINT,NOARP> mtu 1280 qdisc noqueue state DOWN\n"
	addrs := "5: wg0    inet 10.6.0.2/32 brd 255.255.255.255 scope global wg0\n"
	slots, err := slotsImpl(cannedCommand(links, addrs, cannedUCI))
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]slotsFields{}
	for _, sl := range slots {
		byID[sl.ID] = slotsFields{up: sl.LinkUp, addr: sl.Address}
	}
	if len(slots) != 2 {
		t.Fatalf("слотов %d, хочу 2 (только wireguard/amneziawg): %+v", len(slots), slots)
	}
	wg := byID["wg0"]
	if !wg.up || wg.addr != "10.6.0.2/32" {
		t.Fatalf("wg0: %+v", wg)
	}
	aw := byID["awg0"]
	if aw.up {
		t.Fatalf("awg0: %+v", aw)
	}
	if _, ok := byID["lan"]; ok {
		t.Fatal("static-интерфейс не должен попадать в слоты")
	}
}

type slotsFields struct {
	up   bool
	addr string
}

// системные туннели: адреса из пакетного снимка, флаг IFF_UP из flags
func TestSysTunnelsBatchedAddresses(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"tun0", "wg9", "eth0"} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
		flags := "0x00001091" // IFF_UP
		if name == "eth0" {
			flags = "0x00001090" // down
		}
		if err := os.WriteFile(filepath.Join(dir, name, "flags"), []byte(flags), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	addrs := "9: tun0    inet 10.8.0.1/32 scope global tun0\n"
	tunnels, err := sysTunnelsImpl(dir, cannedCommand("", addrs, ""))
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]slotsFields{}
	for _, sl := range tunnels {
		byName[sl.Device] = slotsFields{up: sl.LinkUp, addr: sl.Address}
	}
	if len(tunnels) != 2 {
		t.Fatalf("туннелей %d, хочу 2 (eth0 мимо регулярки): %+v", len(tunnels), tunnels)
	}
	tn := byName["tun0"]
	if !tn.up || tn.addr != "10.8.0.1/32" {
		t.Fatalf("tun0: %+v", tn)
	}
	if byName["wg9"].addr != "" {
		t.Fatalf("wg9 без адреса должен остаться пустым: %+v", byName["wg9"])
	}
}
