package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mawg/internal/wgconf"
)

// Миграция уровня обфускации: у конфигов, добавленных до появления поля
// Awg, оно досчитывается при старте (слово владельца: кнопки скачивания
// WG/AWG - по уровню конфига).

const protonLikeStoreConf = `[Interface]
PrivateKey = gEyQZ7ioEGZMg6xF8Wwf0A9mifQodrcdrjDEzIwjNmw=
Address = 10.2.0.2/32
MTU = 1280
Jc = 3
Jmin = 1
Jmax = 3
I1 = <b 0xc20000000101e6000040560c0710de4ad50c0f0109d83b3530f508ed348da5bebaa4a20474c162eec61eb0268d8b83ad524716f7672d466516893db6a7a1cf86d2d50b5863380e72035c104767a339a32a029e699ca829684dc756b6b2222269bd>

[Peer]
# Server: US-FREE#34
PublicKey = zLHCHCqpUPL9mypYmlDoEpbkl9+60DCBs9Iu1suiJmI=
Endpoint = 149.102.254.89:51820
AllowedIPs = 0.0.0.0/0, ::/0
`

func TestOpenMigratesAwgLevels(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "pools", "warp"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "pools", "warp", "w.conf"), []byte(protonLikeStoreConf), 0o600); err != nil {
		t.Fatal(err)
	}
	root := RootConfig{Pools: []Pool{{
		Name:     "warp",
		Settings: PoolSettings{Platform: "openwrt", OpenwrtProto: "amneziawg"},
		Configs:  []ManagedConfig{{File: "w.conf", Original: "w", Endpoint: "149.102.254.89:51820", Enabled: true}},
	}}}
	raw, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "config.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	st, err := Open(base)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	p, ok := st.Pool("warp")
	if !ok {
		t.Fatal("пул не найден")
	}
	if p.Configs[0].Awg != wgconf.Awg2 {
		t.Fatalf("уровень не домигрировал: %q", p.Configs[0].Awg)
	}
	// уровень сохранён в config.json - перезапуск читает без пересчёта
	saved, err := os.ReadFile(filepath.Join(base, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), `"awg": "awg2"`) {
		t.Error("мигрированный уровень не сохранён в config.json")
	}
}

func TestAddConfigsSetsAwgLevel(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreatePool("p", PoolSettings{Platform: "openwrt"}); err != nil {
		t.Fatal(err)
	}
	ncs := []wgconf.NamedConfig{
		{OriginalName: "plain", Raw: []byte("[Interface]\nPrivateKey = " + strings.Repeat("A", 43) + "=\nAddress = 10.0.0.2/32\n\n[Peer]\nPublicKey = " + strings.Repeat("B", 43) + "=\nEndpoint = 1.2.3.4:51820\n"), Config: mustParse(t, "[Interface]\nPrivateKey = " + strings.Repeat("A", 43) + "=\nAddress = 10.0.0.2/32\n\n[Peer]\nPublicKey = " + strings.Repeat("B", 43) + "=\nEndpoint = 1.2.3.4:51820\n")},
		{OriginalName: "obf", Raw: []byte(protonLikeStoreConf), Config: mustParse(t, protonLikeStoreConf)},
	}
	if _, _, err := st.AddConfigs("p", ncs); err != nil {
		t.Fatal(err)
	}
	p, _ := st.Pool("p")
	if p.Configs[0].Awg != wgconf.AwgPlain || p.Configs[1].Awg != wgconf.Awg2 {
		t.Fatalf("уровни: %q / %q", p.Configs[0].Awg, p.Configs[1].Awg)
	}
}

func mustParse(t *testing.T, raw string) wgconf.Config {
	t.Helper()
	cfg, err := wgconf.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("фикстура не разбирается: %v", err)
	}
	return cfg
}
