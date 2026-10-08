package provision

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

const versionOut = `
          release: 5.01.C.3.0-1
            title: 5.1.3
             arch: mips
           components: acl,base,wireguard,
                       wireguard-server
      description: Keenetic Giga (KN-1011)
            model: Giga (KN-1011)
`

func TestParseKeeneticVersion(t *testing.T) {
	info := parseKeeneticVersion(versionOut)
	if info.title != "5.1.3" {
		t.Fatalf("title = %q", info.title)
	}
	if info.description != "Keenetic Giga (KN-1011)" {
		t.Fatalf("description = %q", info.description)
	}
	if info.major != 5 || info.minor != 1 {
		t.Fatalf("gate version = %d.%d", info.major, info.minor)
	}
	if !info.components["wireguard"] {
		t.Fatal("wireguard component not parsed")
	}
}

// fakeRunner - подмена shell для lxCoreItem: знает `version` бинаря и маркер.
type fakeRunner map[string]string

func (f fakeRunner) run(script string, _ time.Duration) (string, error) {
	for pattern, out := range f {
		if strings.Contains(script, pattern) {
			return out, nil
		}
	}
	return "", fmt.Errorf("команда не поддержана: %s", script)
}

func TestLxCoreItemStates(t *testing.T) {
	bin := "/opt/bin/sing-box"
	marker := "test -f /opt/etc/sing-box-lx/.installed-by-mawg"

	// чужое upstream-ядро: замена только по явной кнопке
	r := fakeRunner{bin + " version": "sing-box version 1.13.3\n"}
	r[marker] = ""
	item := lxCoreItem(r.run, "keenetic")
	if item.Installed || item.Action != "singbox-lx-replace" {
		t.Fatalf("чужое ядро: %+v", item)
	}
	if !strings.Contains(item.Confirm, "1.13.3") || !strings.Contains(item.Note, "не трогает") {
		t.Fatalf("confirm/note: %q %q", item.Confirm, item.Note)
	}

	// своё lx-ядро с маркером: обновление
	r = fakeRunner{bin + " version": "sing-box version 1.14.2-lx.7\n", marker: "marked"}
	item = lxCoreItem(r.run, "keenetic")
	if !item.Installed || item.Action != "singbox-lx" || item.Note != "" {
		t.Fatalf("lx с маркером: %+v", item)
	}

	// lx без маркера - миграция: считается своим, после кнопки появится маркер
	r = fakeRunner{bin + " version": "sing-box version 1.14.2-lx.6\n"}
	item = lxCoreItem(r.run, "keenetic")
	if !item.Installed || item.Note == "" || item.ActionLabel != "пометить своим и обновить" {
		t.Fatalf("lx без маркера: %+v", item)
	}

	// ядра нет: установка с нуля
	item = lxCoreItem(fakeRunner{}.run, "keenetic")
	if item.Installed || item.Action != "singbox-lx" || item.Version != "" {
		t.Fatalf("нет ядра: %+v", item)
	}
	if !strings.Contains(item.Confirm, "не установлено") {
		t.Fatalf("confirm: %q", item.Confirm)
	}

	// openwrt-пути
	r = fakeRunner{"/usr/bin/sing-box version": "sing-box version 1.14.2-lx.7\n",
		"test -f /etc/sing-box-lx/.installed-by-mawg": "marked"}
	item = lxCoreItem(r.run, "openwrt")
	if !item.Installed {
		t.Fatalf("openwrt lx: %+v", item)
	}
}
