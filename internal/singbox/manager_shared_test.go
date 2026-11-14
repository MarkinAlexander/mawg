//go:build linux

package singbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mawg/internal/links"
)

// applyShared пишет фрагмент ДО preflight (117 §0, инцидент 12.11): при
// мёртвом сервисе с нечитаемым чужим config.json раньше выходили до
// записи - старый mawg-pools.json консервировал уже удалённые пулы и
// продолжал валить сервис при его старте (лечилось rm фрагмента + рестарт
// mawg). Теперь желаемое состояние попадает в SharedDir всегда, отказ
// preflight честный, но стейл не оставляет.
func TestApplySharedWritesFragmentBeforePreflight(t *testing.T) {
	tmp := t.TempDir()
	// чужой конфиг - мусор: preflight падает «не разбирается»
	if err := os.WriteFile(filepath.Join(tmp, "config.json"), []byte("не-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := &Manager{Mode: "shared", SharedDir: tmp, Dir: tmp, eng: &Engine{SourceID: "lx"}, ClashPort: 22990}
	// стейл-фрагмент прошлой удачной сборки: в нём ещё удалённый пул «a»
	if err := m.writeFragment([]byte(`{"outbounds":[{"tag":"mawg-a|conf-a"}]}`)); err != nil {
		t.Fatal(err)
	}
	b := PoolSpec{Name: "b", Tun: "tun1", TunIP: "172.19.1.1/30", MixedPort: 2282,
		Nodes: []links.Node{{Type: "vmess", Tag: "sub|vm-ws", Host: "1.2.3.4", Port: 8443,
			UUID: "u-1", Security: "chacha20-poly1305", AlterId: 8,
			Transport: "ws", Path: "/ws", TLS: &links.TLS{Security: "tls", SNI: "ex.com"}}}}
	if _, err := m.applyShared([]PoolSpec{b}, nil); err == nil ||
		!strings.Contains(err.Error(), "не разбирается") {
		t.Fatalf("ждём отказ preflight на битом чужом конфиге: %v", err)
	}
	data, rerr := os.ReadFile(m.fragmentPath())
	if rerr != nil {
		t.Fatal(rerr)
	}
	if strings.Contains(string(data), "mawg-a") {
		t.Fatalf("стейл-фрагмент не перезаписан: удалённый пул остался\n%s", string(data))
	}
	if !strings.Contains(string(data), "mawg-b") {
		t.Fatalf("желаемое состояние не попало во фрагмент\n%s", string(data))
	}
}
