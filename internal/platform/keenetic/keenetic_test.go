package keenetic

import (
	"net/http"
	"net/http/httptest"
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
		{"public-key":"AAA=","remote-endpoint-address":"194.76.147.234:35091"},
		{"public-key":"BBB=","endpoint":"8.39.125.1:51820"},
		{"public-key":"CCC=","description":"без адреса"},
		{"public-key":"DDD=","remote-endpoint-address":"194.76.147.234:35091"}]}}`
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
	if len(eps) != 2 || eps[0] != "194.76.147.234" || eps[1] != "8.39.125.1" {
		t.Fatalf("eps = %v", eps)
	}
	// не-WG имя - внятная ошибка
	if _, err := b.IfaceEndpoints("eth0"); err == nil {
		t.Fatal("ожидали ошибку на не-слотовом имени")
	}
}
