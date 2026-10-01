package keenetic

import "testing"

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
