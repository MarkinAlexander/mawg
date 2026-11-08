package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"mawg/internal/store"
	"mawg/internal/wgconf"
)

// export sanitized обязан маскировать секреты, сохраняя функционал:
// приватный ключ/PSK -> ***, endpoint/AllowedIPs/AWG-параметры остаются.
func TestCmdExportSanitized(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	priv := "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
	psk := "ZmVkY2JhOTg3NjU0MzIxMGZlZGNiYTk4NzY1NDMyMTA="
	raw := "[Interface]\n" +
		"PrivateKey = " + priv + "\n" +
		"Address = 10.7.0.2/32\n" +
		"MTU = 1280\n" +
		"\n[Peer]\n" +
		"PublicKey = YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXowMTIzNDU=\n" +
		"PresharedKey = " + psk + "\n" +
		"Endpoint = 203.0.113.10:51820\n" +
		"AllowedIPs = 0.0.0.0/0\n" +
		"PersistentKeepalive = 25\n"
	parsed, err := wgconf.Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreatePool("testpool", store.PoolSettings{ProbeHost: "1.1.1.1"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.AddConfigs("testpool", []wgconf.NamedConfig{{OriginalName: "n1", Raw: []byte(raw), Config: parsed}}); err != nil {
		t.Fatal(err)
	}
	// RCI-токен живёт в settings - тоже секрет
	if err := st.SetRCIToken("top-secret-rci-token"); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	code := cmdExport(dir, []string{"all"})
	w.Close()
	os.Stdout = old
	if code != 0 {
		t.Fatalf("код %d", code)
	}
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	if strings.Contains(out, priv) || strings.Contains(out, psk) || strings.Contains(out, "top-secret-rci-token") {
		t.Fatal("секрет утёк в дамп")
	}
	for _, must := range []string{"testpool", "203.0.113.10", "51820", "10.7.0.2/32", "0.0.0.0/0", "25", "YWJjZGVmZ2hpamts"} {
		if !strings.Contains(out, must) {
			t.Fatalf("функционал потерян: %q нет в дампе\nДАМП:\n%s", must, out)
		}
	}
	if !strings.Contains(out, redacted) {
		t.Fatal("маска *** не встретилась в дампе")
	}
}
