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

// переименование пула перепривязывает ВСЕ ссылки на имя: фоллбек
// "pool:имя" других пулов, путь зонного DNS, участники бандлов и
// реестр привязок устройств - иначе каждая из них молча вела в
// несуществующий пул (аудит dangling-ссылок, кейс 12.11)
func TestRenamePoolRewritesAllNameRefs(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"proton", "warp"} {
		if _, err := st.CreatePool(n, PoolSettings{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.UpdatePool("warp", PoolSettings{Fallback: "pool:proton"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetDnsZones([]DnsZone{{ID: "z1", Zone: "corp.example", Upstreams: []string{"10.0.0.1:53"}, Pool: "proton", Enable: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateBundle(Bundle{Name: "tg", Members: []string{"proton", "tun9"}, Groups: []string{"g1"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetDeviceBindings([]DeviceBinding{{Pool: "proton", IPs: []string{"192.168.1.50/32"}, Name: "Устройство: телефон"}}); err != nil {
		t.Fatal(err)
	}

	if _, err := st.RenamePool("proton", "proton2"); err != nil {
		t.Fatal(err)
	}

	p, ok := st.Pool("warp")
	if !ok || p.Settings.Fallback != "pool:proton2" {
		t.Fatalf("fallback после переименования: %+v", p.Settings.Fallback)
	}
	if zs := st.DnsZones(); zs[0].Pool != "proton2" {
		t.Fatalf("пул зоны после переименования: %+v", zs[0].Pool)
	}
	for _, bundle := range st.Bundles() {
		if bundle.Name == "tg" && (len(bundle.Members) != 2 || bundle.Members[0] != "proton2") {
			t.Fatalf("участники бандла после переименования: %v", bundle.Members)
		}
	}
	for _, bnd := range st.DeviceBindings() {
		if bnd.Pool != "proton2" {
			t.Fatalf("привязка устройства после переименования: %+v", bnd)
		}
	}
}
