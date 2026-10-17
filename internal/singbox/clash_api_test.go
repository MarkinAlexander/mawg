package singbox

import (
	"encoding/json"
	"strings"
	"testing"
)

func parseEdited(t *testing.T, out []byte) map[string]any {
	t.Helper()
	var cfg map[string]any
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("результат не разбирается: %v", err)
	}
	return cfg
}

func TestEditClashAPIAddsController(t *testing.T) {
	in := []byte(`{"inbounds":[{"type":"tun","tag":"tun-in"}],"outbounds":[{"type":"direct","tag":"direct"}]}`)
	out, changed, note, err := editClashAPI(in, 2291, nil, func() int { return 8388 })
	if err != nil || !changed {
		t.Fatalf("ожидалась правка: changed=%v err=%v", changed, err)
	}
	cfg := parseEdited(t, out)
	clash := cfg["experimental"].(map[string]any)["clash_api"].(map[string]any)
	if clash["external_controller"] != "127.0.0.1:2291" {
		t.Fatalf("external_controller = %v", clash["external_controller"])
	}
	if !strings.Contains(note, "clash_api") {
		t.Fatalf("примечание без упоминания clash_api: %q", note)
	}
}

func TestEditClashAPIKeepsExistingController(t *testing.T) {
	in := []byte(`{"experimental":{"clash_api":{"external_controller":"127.0.0.1:9090"}}}`)
	out, changed, _, err := editClashAPI(in, 2291, nil, func() int { return 8388 })
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("чужой external_controller не должен трогаться")
	}
	clash := parseEdited(t, out)["experimental"].(map[string]any)["clash_api"].(map[string]any)
	if clash["external_controller"] != "127.0.0.1:9090" {
		t.Fatalf("external_controller перезаписан: %v", clash["external_controller"])
	}
}

// пакетный дефолт OpenWrt-пакета sing-box: shadowsocks inbound на :::8080
// пересекается с API MagiTrickle - порт переносится на свободный
func TestEditClashAPIRemapsReservedInbound(t *testing.T) {
	in := []byte(`{"inbounds":[{"type":"shadowsocks","tag":"ss-in","listen":"::","listen_port":8080}],
		"outbounds":[{"type":"direct","tag":"direct"}]}`)
	reserved := map[int]string{8080: "API MagiTrickle"}
	out, changed, note, err := editClashAPI(in, 2291, reserved, func() int { return 8388 })
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	inb := parseEdited(t, out)["inbounds"].([]any)[0].(map[string]any)
	if inb["listen_port"] != float64(8388) {
		t.Fatalf("listen_port = %v, хотел 8388", inb["listen_port"])
	}
	if !strings.Contains(note, "8080") || !strings.Contains(note, "8388") {
		t.Fatalf("примечание без переноса порта: %q", note)
	}
}

func TestEditClashAPINoFreePort(t *testing.T) {
	in := []byte(`{"inbounds":[{"type":"shadowsocks","listen_port":8080}]}`)
	_, _, _, err := editClashAPI(in, 2291, map[int]string{8080: "API MT"}, func() int { return 0 })
	if err == nil {
		t.Fatal("хотел отказ при отсутствии свободного порта")
	}
}

func TestEditClashAPIGarbage(t *testing.T) {
	if _, _, _, err := editClashAPI([]byte(`{не json`), 2291, nil, func() int { return 0 }); err == nil {
		t.Fatal("хотел ошибку разбора")
	}
}
