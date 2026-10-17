package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mawg/internal/wgconf"
)

// журнал живёт в tmpfs (слово владельца 2026-10-17): база на флеше
// событий не получает вовсе; легаси-файл из базы читается при старте
func TestEventsOnTmpfs(t *testing.T) {
	cleanTmpJournal(t)
	base := t.TempDir()
	// легаси-журнал на «флеше»: должен быть подхвачен при старте
	legacy := filepath.Join(base, "events.json")
	legacyLine := `{"time":"2026-10-17T00:00:00Z","pool":"p","kind":"probe","message":"легаси"}` + "\n"
	if err := os.WriteFile(legacy, []byte(legacyLine), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := Open(base)
	if err != nil {
		t.Fatal(err)
	}
	st.LogEvent("p", "probe", "новое событие")

	// база: легаси-файл прочитан и УДАЛЕН (мёртвый груз не висит)
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("легаси events.json должен быть удалён после чтения")
	}
	if _, err := os.Stat(filepath.Join(base, "events.json.old")); !os.IsNotExist(err) {
		t.Fatalf("events.json.old появился на флешке")
	}
	// tmpfs-журнал: новое событие пишется в него (легаси-хвост живёт
	// в памяти - на флешку после апгрейда не возвращается)
	data, err := os.ReadFile(st.eventsTmpPath())
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(st.eventsTmpPath()) {
		t.Fatalf("tmp-путь не абсолютный: %s", st.eventsTmpPath())
	}
	text := string(data)
	if strings.Contains(text, "легаси") || !strings.Contains(text, "новое событие") {
		t.Fatalf("tmp-журнал не тот: %s", text)
	}
	if evs := st.Events(); len(evs) != 2 || evs[0].Message != "легаси" || evs[1].Message != "новое событие" {
		t.Fatalf("в памяти %d событий, want легаси+новое: %+v", len(evs), evs)
	}
}

func seedConfigs(t *testing.T, st *Store) {
	t.Helper()
	raws := map[string]string{
		"a.conf": "[Interface]\nPrivateKey=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaAAA=\nAddress=10.1.0.2/32\n[Peer]\nPublicKey=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbBBB=\nEndpoint=1.1.1.1:1\nAllowedIPs=0.0.0.0/0\n",
		"b.conf": "[Interface]\nPrivateKey=ccccccccccccccccccccccccccccccccccccccccCCC=\nAddress=10.2.0.2/32\n[Peer]\nPublicKey=ddddddddddddddddddddddddddddddddddddddddDDD=\nEndpoint=1.1.1.2:2\nAllowedIPs=0.0.0.0/0\n",
		"c.conf": "[Interface]\nPrivateKey=eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeEEE=\nAddress=10.3.0.2/32\n[Peer]\nPublicKey=ffffffffffffffffffffffffffffffffffffffffFFF=\nEndpoint=1.1.1.3:3\nAllowedIPs=0.0.0.0/0\n",
	}
	var ncs []wgconf.NamedConfig
	for _, name := range []string{"a.conf", "b.conf", "c.conf"} {
		parsed, err := wgconf.IngestFile(name, []byte(raws[name]))
		if err != nil {
			t.Fatal(err)
		}
		ncs = append(ncs, parsed...)
	}
	added, _, err := st.AddConfigs("p", ncs)
	if err != nil {
		t.Fatal(err)
	}
	if added != 3 {
		t.Fatalf("want 3 added, got %d", added)
	}
}

func configOrder(t *testing.T, st *Store) []string {
	t.Helper()
	pool, ok := st.Pool("p")
	if !ok {
		t.Fatal("pool missing")
	}
	var out []string
	for _, c := range pool.Configs {
		out = append(out, c.File)
	}
	return out
}

func TestMoveConfig(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreatePool("p", PoolSettings{ProbeHost: "1.1.1.1"}); err != nil {
		t.Fatal(err)
	}
	seedConfigs(t, st)
	order := configOrder(t, st)
	if len(order) != 3 {
		t.Fatalf("want 3 configs, got %v", order)
	}

	if err := st.MoveConfig("p", order[2], -1); err != nil {
		t.Fatal(err)
	}
	if got := configOrder(t, st); got[1] != order[2] || got[2] != order[1] {
		t.Fatalf("move up wrong: %v", got)
	}

	cur := configOrder(t, st)
	if err := st.MoveConfig("p", cur[0], -1); err == nil {
		t.Fatal("edge move must fail")
	}
	if err := st.MoveConfig("p", cur[len(cur)-1], 1); err == nil {
		t.Fatal("edge move must fail")
	}
	if err := st.MoveConfig("p", "missing.conf", 1); err == nil {
		t.Fatal("missing config must fail")
	}
	if err := st.MoveConfig("nosuch", order[0], 1); err == nil {
		t.Fatal("missing pool must fail")
	}

	if err := st.MoveConfig("p", cur[0], 1); err != nil {
		t.Fatal(err)
	}
	got := configOrder(t, st)
	if got[0] != cur[1] || got[1] != cur[0] {
		t.Fatalf("move down wrong: %v", got)
	}
}

func TestIfaceModes(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreatePool("p", PoolSettings{ProbeHost: "1.1.1.1", Platform: PlatformKeenetic, KeeneticSlot: "Wireguard2"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetIfaceMode("nwg2", IfaceExternal); err == nil {
		t.Fatal("managed device must not be switchable")
	}
	if err := st.SetIfaceMode("nwg7", IfaceExternal); err != nil {
		t.Fatal(err)
	}
	if err := st.SetIfaceMode("nwg9", "bogus"); err == nil {
		t.Fatal("bogus mode must fail")
	}
	modes := st.IfaceModes()
	if modes["nwg7"] != IfaceExternal {
		t.Fatalf("want external, got %v", modes)
	}
	if err := st.SetIfaceMode("nwg7", IfaceHidden); err != nil {
		t.Fatal(err)
	}
	if st.IfaceModes()["nwg7"] != IfaceHidden {
		t.Fatal("mode flip failed")
	}
}

func TestRenamePool(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreatePool("old", PoolSettings{ProbeHost: "1.1.1.1"}); err != nil {
		t.Fatal(err)
	}
	seedConfigsPool(t, st, "old")
	st.MutateState("old", func(s *PoolState) { s.Rotations = 5 })

	if _, err := st.RenamePool("old", "1bad name"); err == nil {
		t.Fatal("bad name must fail")
	}
	if _, err := st.RenamePool("nosuch", "new"); err == nil {
		t.Fatal("missing pool must fail")
	}
	renamed, err := st.RenamePool("old", "new")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Name != "new" {
		t.Fatalf("want new, got %s", renamed.Name)
	}
	if _, ok := st.Pool("old"); ok {
		t.Fatal("old name still resolves")
	}
	if len(renamed.Configs) != 3 {
		t.Fatalf("configs lost on rename: %v", renamed.Configs)
	}
	if st.State("new").Rotations != 5 {
		t.Fatal("state lost on rename")
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := reopened.Pool("new")
	if !ok || len(p.Configs) != 3 {
		t.Fatalf("rename not persisted: %v", p)
	}
	if reopened.State("new").Rotations != 5 {
		t.Fatal("state not persisted after rename")
	}
}

func seedConfigsPool(t *testing.T, st *Store, pool string) {
	t.Helper()
	raws := map[string]string{
		"a.conf": "[Interface]\nPrivateKey=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaAAA=\nAddress=10.1.0.2/32\n[Peer]\nPublicKey=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbBBB=\nEndpoint=1.1.1.1:1\nAllowedIPs=0.0.0.0/0\n",
		"b.conf": "[Interface]\nPrivateKey=ccccccccccccccccccccccccccccccccccccccccCCC=\nAddress=10.2.0.2/32\n[Peer]\nPublicKey=ddddddddddddddddddddddddddddddddddddddddDDD=\nEndpoint=1.1.1.2:2\nAllowedIPs=0.0.0.0/0\n",
		"c.conf": "[Interface]\nPrivateKey=eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeEEE=\nAddress=10.3.0.2/32\n[Peer]\nPublicKey=ffffffffffffffffffffffffffffffffffffffffFFF=\nEndpoint=1.1.1.3:3\nAllowedIPs=0.0.0.0/0\n",
	}
	var ncs []wgconf.NamedConfig
	for _, name := range []string{"a.conf", "b.conf", "c.conf"} {
		parsed, err := wgconf.IngestFile(name, []byte(raws[name]))
		if err != nil {
			t.Fatal(err)
		}
		ncs = append(ncs, parsed...)
	}
	if _, _, err := st.AddConfigs(pool, ncs); err != nil {
		t.Fatal(err)
	}
}

func TestProbeConfigs(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var nilCfg *ProbeConfig
	if got := nilCfg.Normalized(); got.Target != DefaultProbeHTTP || got.Type != ProbeTypeHTTP {
		t.Fatalf("nil normalized = %+v", got)
	}
	cfg := &ProbeConfig{Target: "8.8.8.8"}
	norm := cfg.Normalized()
	if norm.Type != ProbeTypeICMP {
		t.Fatalf("icmp guess failed: %+v", norm)
	}
	cfg = &ProbeConfig{Type: ProbeTypeICMP}
	norm = cfg.Normalized()
	if norm.Target != DefaultProbeICMP {
		t.Fatalf("icmp default failed: %+v", norm)
	}

	if err := st.SetIfaceMode("nwg7", IfaceExternal); err != nil {
		t.Fatal(err)
	}
	if err := st.SetIfaceProbe("nwg7", &ProbeConfig{MaxRTTms: 700}); err != nil {
		t.Fatal(err)
	}
	got := st.IfaceProbe("nwg7")
	if got == nil || got.Target != DefaultProbeHTTP || got.Type != ProbeTypeHTTP || got.MaxRTTms != 700 {
		t.Fatalf("iface probe = %+v", got)
	}
	if err := st.SetIfaceProbe("nwg7", nil); err != nil {
		t.Fatal(err)
	}
	if st.IfaceProbe("nwg7") != nil {
		t.Fatal("probe not cleared by nil")
	}

	if err := st.SetWANProbe(&ProbeConfig{Type: ProbeTypeICMP}); err != nil {
		t.Fatal(err)
	}
	if !st.WANProbeEnabled() || st.WANProbe().Target != DefaultProbeICMP {
		t.Fatalf("wan probe = %+v enabled=%v", st.WANProbe(), st.WANProbeEnabled())
	}
	if err := st.SetWANProbe(nil); err != nil {
		t.Fatal(err)
	}
	if st.WANProbeEnabled() {
		t.Fatal("wan probe not disabled")
	}
}
