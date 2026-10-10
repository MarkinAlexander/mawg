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
