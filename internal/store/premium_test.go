package store

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"mawg/internal/premium"
)

func TestPremiumIdentityAndReplacement(t *testing.T) {
	base := t.TempDir()
	st, err := Open(base)
	if err != nil {
		t.Fatal(err)
	}
	st.CreatePool("premium", PoolSettings{Platform: PlatformOpenwrt, OpenwrtProto: "amneziawg"})
	st.CreatePool("other", PoolSettings{})
	subscription, err := st.ImportPremium("premium", "synthetic-subscription", "RU")
	if err != nil || subscription.UUID == "" {
		t.Fatalf("import: %v", err)
	}
	subscription.Countries = []premium.Country{{Code: "NL", Name: "Netherlands"}}
	subscription.Country = "NL"
	if err := st.SavePremium(subscription); err != nil {
		t.Fatal(err)
	}
	raw := []byte("[Interface]\nPrivateKey = QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE=\nAddress = 10.0.0.2/32\nJc = 3\n[Peer]\nPublicKey = QkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkI=\nEndpoint = nl.example.net:51820\n")
	file, err := st.ReplacePremiumConfig("premium", raw)
	if err != nil {
		t.Fatal(err)
	}
	os.Mkdir(filepath.Join(base, "config.json.tmp"), 0700)
	if _, err := st.ReplacePremiumConfig("premium", bytes.ReplaceAll(raw, []byte("nl.example"), []byte("ro.example"))); err == nil {
		t.Fatal("write failure ignored")
	}
	cfg, err := st.LoadConfigFile("premium", file)
	if err != nil || cfg.Endpoint() != "nl.example.net:51820" {
		t.Fatal("old current config damaged")
	}
	os.Remove(filepath.Join(base, "config.json.tmp"))
	if _, _, err := st.AddConfigs("premium", nil); err == nil {
		t.Fatal("static configs accepted in Premium pool")
	}
	if err := st.RemoveConfig("premium", file); err == nil {
		t.Fatal("current Premium config deletion accepted")
	}
	if _, err := st.ImportPremium("other", "synthetic-subscription", "RU"); err == nil {
		t.Fatal("second subscription pool accepted")
	}
	if _, err := st.RenamePool("premium", "renamed"); err != nil {
		t.Fatal(err)
	}
	st, err = Open(base)
	if err != nil {
		t.Fatal(err)
	}
	saved := st.Premium()
	if saved.UUID != subscription.UUID || saved.Country != "NL" {
		t.Fatal("identity lost on restart/rename")
	}
	if !st.PremiumView("renamed").Imported {
		t.Fatal("rename lost binding")
	}
	public, _ := json.Marshal(st.PremiumView("renamed"))
	root, _ := os.ReadFile(filepath.Join(base, "config.json"))
	if bytes.Contains(public, []byte(subscription.Key)) || bytes.Contains(root, []byte(subscription.Key)) {
		t.Fatal("secret exposed")
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(filepath.Join(base, "premium.json"))
		if fi.Mode().Perm() != 0600 {
			t.Fatal("secret file not root-only")
		}
	}
	if err := st.DeletePool("renamed"); err != nil {
		t.Fatal(err)
	}
	st.CreatePool("again", PoolSettings{})
	again, err := st.ImportPremium("again", "synthetic-subscription", "RU")
	if err != nil || again.UUID != subscription.UUID {
		t.Fatal("pool recreation consumed new device identity")
	}
}

func TestPremiumSettingsCannotDropAWG(t *testing.T) {
	st, _ := Open(t.TempDir())
	p, _ := st.CreatePool("premium", PoolSettings{Platform: PlatformOpenwrt, OpenwrtProto: "amneziawg"})
	st.ImportPremium(p.Name, "synthetic-key", "")
	settings := p.Settings
	settings.OpenwrtProto = "wireguard"
	if err := st.UpdatePool(p.Name, settings); err == nil {
		t.Fatal("Premium lost AWG protocol")
	}
	// движковый режим разрешён (Keenetic: конфиги gateway - AWG 3.x,
	// слот 5.1 их не поднимает - миграция в движок)
	settings = p.Settings
	settings.EngineMode = "singbox"
	settings.TunName = "tun4"
	if err := st.UpdatePool(p.Name, settings); err != nil {
		t.Fatalf("Premium -> engine: %v", err)
	}
	// но статический источник Premium-пулу по-прежнему нельзя
	settings2 := p.Settings
	settings2.Source = "https://sub.example.net/sub"
	if err := st.UpdatePool(p.Name, settings2); err == nil {
		t.Fatal("Premium accepted static source")
	}
}

// движковый пул (Keenetic) принимает импорт Premium
func TestPremiumAcceptsEnginePool(t *testing.T) {
	st, _ := Open(t.TempDir())
	st.CreatePool("engine", PoolSettings{EngineMode: "singbox", TunName: "tun1"})
	if _, err := st.ImportPremium("engine", "synthetic-key", ""); err != nil {
		t.Fatalf("Premium rejected engine pool: %v", err)
	}
	pool, ok := st.Pool("engine")
	if !ok || !pool.Premium {
		t.Fatal("пул не помечен Premium")
	}
}

func TestPremiumMigratedDeviceIdentity(t *testing.T) {
	base := t.TempDir()
	uuid := "a69ce500-d0a6-4c26-a17c-36a44a90cd56"
	if err := os.WriteFile(filepath.Join(base, "premium.json"), []byte(`{"installationUUID":"`+uuid+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	st, err := Open(base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreatePool("premium", PoolSettings{Platform: PlatformOpenwrt, OpenwrtProto: "amneziawg"}); err != nil {
		t.Fatal(err)
	}
	v, err := st.ImportPremium("premium", "synthetic-migrated-subscription", "")
	if err != nil || v.UUID != uuid {
		t.Fatal("preseeded existing device identity was replaced")
	}
	st, err = Open(base)
	if err != nil || st.Premium().UUID != uuid {
		t.Fatal("migrated identity did not survive restart")
	}
}

func TestStatePersistenceRetriesAfterWriteFailure(t *testing.T) {
	base := t.TempDir()
	st, _ := Open(base)
	st.CreatePool("premium", PoolSettings{})
	os.Mkdir(filepath.Join(base, "state.json.tmp"), 0700)
	if err := st.MutateState("premium", func(s *PoolState) { s.ActiveFile = "current.conf" }); err == nil {
		t.Fatal("state write error ignored")
	}
	os.Remove(filepath.Join(base, "state.json.tmp"))
	if err := st.MutateState("premium", func(s *PoolState) {}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(base, "state.json"))
	if err != nil || !bytes.Contains(data, []byte("current.conf")) {
		t.Fatal("failed state write was incorrectly cached as persisted")
	}
}
