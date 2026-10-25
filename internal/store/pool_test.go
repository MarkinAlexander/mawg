package store

import "testing"


// OpenWrt: дефис в имени пула не проходит в uci-секцию - устройство
// зовётся с подчёркиванием; имена без дефисов не меняются
func TestDeviceNameOpenwrtHyphen(t *testing.T) {
	p := Pool{Name: "xorek-fin-grpc", Settings: PoolSettings{Platform: PlatformOpenwrt}}
	if got := p.DeviceName(); got != "xorek_fin_grpc" {
		t.Fatalf("DeviceName = %q, want xorek_fin_grpc", got)
	}
	p2 := Pool{Name: "warp", Settings: PoolSettings{Platform: PlatformOpenwrt}}
	if got := p2.DeviceName(); got != "warp" {
		t.Fatalf("DeviceName = %q, want warp", got)
	}
}

// переименование пула перепривязывает каскады (Source/Via "pool:имя") и
// Via умных каскадов других пулов - иначе sync терял бы источник и
// каскад застывал со старым набором адресов
func TestRenamePoolRewritesCascadeRefs(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"proton", "warp", "other"} {
		if _, err := st.CreatePool(n, PoolSettings{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.UpdatePool("other", PoolSettings{SmartCascade: &SmartCascadeSettings{Via: []string{"proton", "warp"}}}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetCascades([]CascadeEntry{{Group: "grp1", Name: "каскад", Source: "pool:proton", Via: "pool:warp"}}); err != nil {
		t.Fatal(err)
	}

	if _, err := st.RenamePool("proton", "proton2"); err != nil {
		t.Fatal(err)
	}
	cs := st.Cascades()
	if len(cs) != 1 || cs[0].Source != "pool:proton2" {
		t.Fatalf("Source после переименования: %+v", cs)
	}
	p, ok := st.Pool("other")
	if !ok {
		t.Fatal("пул other исчез")
	}
	if got := p.Settings.SmartCascade.Via; len(got) != 2 || got[0] != "proton2" || got[1] != "warp" {
		t.Fatalf("Via умного каскада: %v, want [proton2 warp]", got)
	}

	// via-пул переименовали - ссылка тоже следует
	if _, err := st.RenamePool("warp", "warp2"); err != nil {
		t.Fatal(err)
	}
	if cs := st.Cascades(); cs[0].Via != "pool:warp2" {
		t.Fatalf("Via после переименования warp: %+v", cs)
	}
}
