package keenetic

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const versionOut = `
          release: 5.01.C.3.0-1
          sandbox: stable
            title: 5.1.3
             arch: mips
           components: acl,base,cloudcontrol,corewireless,dhcpd,
                       dns-filter,dot1x,easyeasy,
                       ntfs,ntfs-utils,openvpn,opkg,opkg-kmod-fs,
                       pingcheck,pptp,vpnserver-l2tp,webdav,wireguard,
                       wireguard-server,zerotier
           manufacturer: Keenetic Ltd.
              model: Giga (KN-1011)
`

// detект: NDM CLI не ответил (инцидент пользователя 15.11: ndmc
// system failed [0xcffd0062] валил запуск mawg целиком) - вердикт
// ErrNDMDown (панель стартует в деградировавшем режиме); ndmc жив -
// семантические проверки как раньше и НЕ как ErrNDMDown
// detект: NDM CLI не ответил (инцидент пользователя 15.11: ndmc
// system failed [0xcffd0062] валил запуск mawg целиком) - вердикт
// ErrNDMDown (панель стартует в деградировавшем режиме); ndmc жив -
// семантические проверки как раньше и НЕ как ErrNDMDown
func TestDetectVerdict(t *testing.T) {
	down := "ndmc \"show version\": exit status 1: [C] Nov 15 01:45:29 ndm: ndmc: system failed [0xcffd0062].\n[C] Nov 15 01:45:29 ndm: Cli::Main: failed to initialize."
	err := verdictDetect(down, errors.New("exit status 1"))
	if !errors.Is(err, ErrNDMDown) {
		t.Fatalf("ndm не ответил - ждём ErrNDMDown: %v", err)
	}
	old := verdictDetect("release: 4.03.C.1.0-1\ncomponents: base,opkg\n", nil)
	if errors.Is(old, ErrNDMDown) || !strings.Contains(old.Error(), "ниже 5.1") {
		t.Fatalf("4.x - семантический отказ, не ErrNDMDown: %v", old)
	}
	nowg := verdictDetect("release: 5.1\ncomponents: base,opkg\n", nil)
	if errors.Is(nowg, ErrNDMDown) || !strings.Contains(nowg.Error(), "WireGuard") {
		t.Fatalf("5.1 без компонента: %v", nowg)
	}
	if err := verdictDetect("release: 5.2\ncomponents: base,opkg,wireguard\n", nil); err != nil {
		t.Fatalf("5.2 с wireguard - detект должен пройти: %v", err)
	}
}

func TestParseVersion(t *testing.T) {
	b := New()
	info := b.parseVersion(versionOut)
	if info.major != 5 || info.minor != 1 {
		t.Fatalf("version = %d.%d", info.major, info.minor)
	}
	for _, c := range []string{"wireguard", "wireguard-server", "opkg", "zerotier"} {
		if !info.components[c] {
			t.Fatalf("component %s not parsed, have %v", c, info.components)
		}
	}
}

func TestParseVersionOldFirmware(t *testing.T) {
	b := New()
	info := b.parseVersion("release: 4.03.C.1.0-1\ncomponents: base,opkg\n")
	if info.major != 4 || info.minor != 3 {
		t.Fatalf("version = %d.%d", info.major, info.minor)
	}
	if info.components["wireguard"] {
		t.Fatal("wireguard must be absent")
	}
}

func TestDeviceName(t *testing.T) {
	for slot, dev := range map[string]string{
		"Wireguard0": "nwg0", "Wireguard12": "nwg12", "Home": "Home",
	} {
		if got := DeviceName(slot); got != dev {
			t.Fatalf("DeviceName(%s) = %s, want %s", slot, got, dev)
		}
	}
}

func TestIfaceEndpoints(t *testing.T) {
	payload := `{"id":"Wireguard7","description":"france-router","wireguard":{"peer":[
		{"public-key":"AAA=","remote-endpoint-address":"203.0.113.10:35091"},
		{"public-key":"BBB=","endpoint":"8.39.125.1:51820"},
		{"public-key":"CCC=","description":"без адреса"},
		{"public-key":"DDD=","remote-endpoint-address":"203.0.113.10:35091"}]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rci/show/interface/Wireguard7" {
			w.Write([]byte(payload))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()
	oldBase := rciBase
	rciBase = srv.URL
	defer func() { rciBase = oldBase }()

	b := New()
	// nwg7 -> Wireguard7 (мапинг DeviceName/slotFromDevice)
	eps, err := b.IfaceEndpoints("nwg7")
	if err != nil {
		t.Fatalf("IfaceEndpoints: %v", err)
	}
	if len(eps) != 2 || eps[0] != "203.0.113.10" || eps[1] != "8.39.125.1" {
		t.Fatalf("eps = %v", eps)
	}
	// не-WG имя - внятная ошибка
	if _, err := b.IfaceEndpoints("eth0"); err == nil {
		t.Fatal("ожидали ошибку на не-слотовом имени")
	}
}

// живой вывод 5.2 Beta 1 (Netcraze Ultra, тестер 2026-10-26): ведущий
// ноль в миноре, sandbox preview - SupportsNativeAWG3 должен узнать 5.2
func TestParseVersion52Beta(t *testing.T) {
	b := New()
	out := `release: 5.02.B.1.0-3
sandbox: preview
title: 5.2 Beta 1
arch: aarch64
components: base,opkg,wireguard,wireguard-server
`
	info := b.parseVersion(out)
	if info.major != 5 || info.minor != 2 {
		t.Fatalf("version = %d.%d, want 5.2", info.major, info.minor)
	}
	if !info.components["wireguard"] {
		t.Fatal("component wireguard not parsed")
	}
}
