package wgconf

import (
	"strings"
	"testing"
)

// asc-хвост 3.x: позиционная форма из 31 аргумента, подтверждённая live
// на Keenetic 5.2 Beta 1 (running-config persists один в один)
func TestAscArgsAWG3Tail(t *testing.T) {
	cfg := loadConf(t, "awg3-premium.conf")
	if !cfg.AWG.AWG3() {
		t.Fatal("премиум-конфиг должен распознаваться как AWG 3.x")
	}
	got := strings.Join(cfg.AWG.AscArgs(), " ")
	want := `6 10 80 951 924 1 2 3 4 1274 12 ` +
		`"<b 0x4d455353414745207369703a39><rd 9><b 0x407369702e6d616e676f2d6f66666963652e7275>" "" "" "" "" ` +
		`YMG9kWQIDtUsXbYMG9kWQIDtUsXbYMG9kWQIDtUsXbYMG9kWQ= ` +
		`10 100 100 120 3 8 150 180 7 13 15 20 1 0`
	if got != want {
		t.Fatalf("asc:\n got %s\nwant %s", got, want)
	}
	if n := len(cfg.AWG.AscArgs()); n != 31 {
		t.Fatalf("аргументов %d, want 31", n)
	}

	// диапазонный keepalive пира не одно число
	if cfg.Peer.PersistentKeepalive != 0 || cfg.Peer.KeepaliveRange != "25-35" {
		t.Fatalf("keepalive: %d / %q", cfg.Peer.PersistentKeepalive, cfg.Peer.KeepaliveRange)
	}
}

// фолбэк для прошивок без <rd>/<r>-токенов: I-поля чистой статикой,
// <b>-блобы склеиваются, случайные куски выпадают, хвост 3.x на месте
func TestAscArgsNoRandom(t *testing.T) {
	cfg := loadConf(t, "awg3-premium.conf")
	args, ok := cfg.AWG.AscArgsNoRandom()
	if !ok {
		t.Fatal("в I1 есть <rd> - должен быть вариант повтора")
	}
	got := strings.Join(args, " ")
	want := `6 10 80 951 924 1 2 3 4 1274 12 ` +
		`"<b 0x4d455353414745207369703a39407369702e6d616e676f2d6f66666963652e7275>" "" "" "" "" ` +
		`YMG9kWQIDtUsXbYMG9kWQIDtUsXbYMG9kWQIDtUsXbYMG9kWQ= ` +
		`10 100 100 120 3 8 150 180 7 13 15 20 1 0`
	if got != want {
		t.Fatalf("asc-no-random:\n got %s\nwant %s", got, want)
	}

	plain := loadConf(t, "awg-proton.conf")
	if _, ok := plain.AWG.AscArgsNoRandom(); ok {
		t.Fatal("без случайных маркеров повтора быть не должно")
	}
}

// выключенные флаги 3.x не делают конфиг «трёхъядерным»: протокол
// эквивалентен 2.0, нативу хвост не нужен
func TestAWG3FlagsOff(t *testing.T) {
	conf := "[Interface]\nPrivateKey=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaAAA=\nAddress=10.1.0.2/32\n" +
		"Jc=4\nJmin=40\nJmax=70\nS1=15\nS2=16\nRandomTrailers=off\nDisableCookies=off\n" +
		"[Peer]\nPublicKey=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbBBB=\nEndpoint=198.51.100.7:51820\nAllowedIPs=0.0.0.0/0\n"
	cfg, err := Parse([]byte(conf))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AWG.AWG3() {
		t.Fatal("off-флаги не должны включать AWG3")
	}
	if got := cfg.AWG.AscArgs(); len(got) != 9 {
		t.Fatalf("хвост не нужен, аргументов %d: %v", len(got), got)
	}
	if cfg.AWG.RandomTrailers == nil || cfg.AWG.DisableCookies == nil {
		t.Fatal("флаги не распарсились")
	}

	confOn := strings.Replace(conf, "RandomTrailers=off", "RandomTrailers=on", 1)
	cfgOn, err := Parse([]byte(confOn))
	if err != nil {
		t.Fatal(err)
	}
	if !cfgOn.AWG.AWG3() {
		t.Fatal("RandomTrailers=on включает AWG3")
	}
	tail := cfgOn.AWG.AscArgs()
	if len(tail) != 31 || tail[29] != "1" || tail[30] != "0" {
		t.Fatalf("хвост с флагом: %v", tail)
	}
}
