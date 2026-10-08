package singbox

import (
	"encoding/json"
	"strings"
	"testing"

	"mawg/internal/links"
)

const fakeVless = "vless://00000000-0000-4000-8000-000000000001@203.0.113.10:23747" +
	"?encryption=none&fp=randomized&pbk=FAKE0public0key0for0tests0only0not0real0key00" +
	"&security=reality&sid=deadbeef&sni=www.example.com&type=tcp#test-node"

const fakeVlessXhttp = "vless://00000000-0000-4000-8000-000000000001@203.0.113.11:443" +
	"?encryption=none&type=xhttp&path=%2Fup&mode=auto&security=tls&sni=cdn.example.org#xhttp-node"

func mustNodes(t *testing.T, src string) []links.Node {
	t.Helper()
	res := links.ParseSubscription("t", src)
	if len(res.Nodes) == 0 {
		t.Fatal("узел не распарсился")
	}
	return res.Nodes
}

func TestBuildConfigUpstream(t *testing.T) {
	spec := PoolSpec{Name: "demo", Tun: "tun1", TunIP: TuneIP(1), MixedPort: 2282, GroupMode: "selector", Nodes: mustNodes(t, fakeVless)}
	data, skipped, err := BuildConfig([]PoolSpec{spec}, Params{ClashPort: 2291})
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped: %v", skipped)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	inbounds := cfg["inbounds"].([]any)
	if len(inbounds) != 2 {
		t.Fatalf("инбаундов: %d", len(inbounds))
	}
	tun := inbounds[0].(map[string]any)
	if tun["interface_name"] != "tun1" || tun["auto_route"] != false {
		t.Fatalf("tun: %v", tun)
	}
	mixed := inbounds[1].(map[string]any)
	if mixed["tag"] != "mixed-demo" || mixed["listen_port"] != float64(2282) {
		t.Fatalf("mixed пула: %v", mixed)
	}
	if tun["address"].([]any)[0] != "172.19.1.1/30" {
		t.Fatalf("tun address: %v", tun["address"])
	}
	outbounds := cfg["outbounds"].([]any)
	var node, group map[string]any
	for _, o := range outbounds {
		ob := o.(map[string]any)
		if ob["tag"] == "mawg-demo|test-node" {
			node = ob
		}
		if ob["tag"] == "mawg-demo" {
			group = ob
		}
	}
	if node == nil || group == nil {
		t.Fatalf("нет outbound-узла или группы: %v", outbounds)
	}
	if node["server"] != "203.0.113.10" || node["flow"] != "" && node["flow"] != nil {
		t.Fatalf("узел: %v", node)
	}
	tls := node["tls"].(map[string]any)
	if tls["server_name"] != "www.example.com" {
		t.Fatalf("tls: %v", tls)
	}
	reality := tls["reality"].(map[string]any)
	if reality["short_id"] != "deadbeef" {
		t.Fatalf("reality: %v", reality)
	}
	if group["default"] != "mawg-demo|test-node" {
		t.Fatalf("группа: %v", group)
	}
	if group["type"] != "selector" {
		t.Fatalf("тип группы: %v", group)
	}
	rules := cfg["route"].(map[string]any)["rules"].([]any)
	r0 := rules[0].(map[string]any)
	if r0["inbound"] != "tun-in-1" || r0["outbound"] != "mawg-demo" {
		t.Fatalf("route: %v", r0)
	}
	r1 := rules[1].(map[string]any)
	if r1["inbound"] != "mixed-demo" || r1["outbound"] != "mawg-demo" {
		t.Fatalf("route mixed: %v", r1)
	}
	if _, has := cfg["route"].(map[string]any)["default_domain_resolver"]; has {
		t.Fatal("upstream-профиль не должен иметь default_domain_resolver")
	}
}

func TestBuildConfigSkipsXhttpOnUpstream(t *testing.T) {
	spec := PoolSpec{Name: "x", Tun: "tun1", TunIP: TuneIP(1), MixedPort: 2282, Nodes: mustNodes(t, fakeVlessXhttp)}
	_, skipped, err := BuildConfig([]PoolSpec{spec}, Params{ClashPort: 2291})
	if err == nil {
		t.Fatal("xhttp на upstream не должен собираться")
	}
	if len(skipped) == 0 || !strings.Contains(skipped[0], "xhttp") {
		t.Fatalf("skipped: %v", skipped)
	}
}

func TestBuildConfigLXAllowsXhttp(t *testing.T) {
	spec := PoolSpec{Name: "x", Tun: "tun1", TunIP: TuneIP(1), MixedPort: 2282, Nodes: mustNodes(t, fakeVlessXhttp)}
	data, skipped, err := BuildConfig([]PoolSpec{spec}, Params{ClashPort: 2291, LX: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped: %v", skipped)
	}
	if !strings.Contains(string(data), `"xhttp"`) {
		t.Fatal("нет xhttp-транспорта")
	}
	if !strings.Contains(string(data), "default_domain_resolver") {
		t.Fatal("lx-профиль обязан иметь default_domain_resolver")
	}
}

func TestBuildConfigUrltestDefault(t *testing.T) {
	spec := PoolSpec{Name: "u", Tun: "tun1", TunIP: TuneIP(1), MixedPort: 2282,
		ProbeTarget: "http://www.gstatic.com/generate_204", CheckIntervalSec: 45,
		Nodes: mustNodes(t, fakeVless)}
	data, _, err := BuildConfig([]PoolSpec{spec}, Params{ClashPort: 2291})
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	json.Unmarshal(data, &cfg)
	for _, o := range cfg["outbounds"].([]any) {
		ob := o.(map[string]any)
		if ob["tag"] == "mawg-u" {
			if ob["type"] != "urltest" {
				t.Fatalf("группа должна быть urltest: %v", ob)
			}
			if ob["interval"] != "45s" || ob["url"] != "http://www.gstatic.com/generate_204" {
				t.Fatalf("urltest параметры: %v", ob)
			}
			return
		}
	}
	t.Fatal("группа не найдена")
}

func TestTuneIPDistinct(t *testing.T) {
	seen := map[string]bool{}
	for i := 1; i <= 32; i++ {
		ip := TuneIP(i)
		if seen[ip] {
			t.Fatalf("дубль адреса: %s", ip)
		}
		seen[ip] = true
	}
	if TuneIP(1) == "172.19.0.1/30" {
		t.Fatal("tun1 не должен пересекаться с чужим tun0 172.19.0.1/30")
	}
}

func TestBuildConfigMergedFragment(t *testing.T) {
	spec := PoolSpec{Name: "demo", Tun: "tun1", TunIP: TuneIP(1), MixedPort: 2282,
		ProbeTarget: "http://www.gstatic.com/generate_204", Nodes: mustNodes(t, fakeVless)}
	data, _, err := BuildConfig([]PoolSpec{spec}, Params{ClashPort: 2291, Merged: true})
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"log", "experimental"} {
		if _, has := cfg[key]; has {
			t.Fatalf("фрагмент не должен содержать %s: %s", key, data)
		}
	}
	route := cfg["route"].(map[string]any)
	if _, has := route["final"]; has {
		t.Fatal("фрагмент не должен задавать route.final - у чужого конфига свой")
	}
	if _, has := route["auto_detect_interface"]; has {
		t.Fatal("фрагмент не должен задавать auto_detect_interface")
	}
	if _, has := route["default_domain_resolver"]; has {
		t.Fatal("upstream-фрагмент не должен иметь default_domain_resolver")
	}
	for _, o := range cfg["outbounds"].([]any) {
		if o.(map[string]any)["tag"] == "direct" {
			t.Fatal("фрагмент не должен добавлять свой direct - не нужен без route.final")
		}
	}
	if len(cfg["inbounds"].([]any)) != 2 {
		t.Fatal("должен остаться tun+mixed пула")
	}
	if len(route["rules"].([]any)) != 2 {
		t.Fatal("должны остаться правила inbound->группа")
	}
}

func TestBuildConfigMergedLXAddsResolver(t *testing.T) {
	spec := PoolSpec{Name: "x", Tun: "tun1", TunIP: TuneIP(1), MixedPort: 2282,
		ProbeTarget: "http://www.gstatic.com/generate_204", Nodes: mustNodes(t, fakeVlessXhttp)}
	data, _, err := BuildConfig([]PoolSpec{spec}, Params{LX: true, Merged: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "default_domain_resolver") {
		t.Fatal("lx-фрагмент обязан иметь default_domain_resolver")
	}
	// резолверу нужна запись в dns.servers - без неё lx-ядро на check
	// отвечает "default domain resolver not found: local"
	if !strings.Contains(string(data), `"type": "local"`) || !strings.Contains(string(data), `"tag": "local"`) {
		t.Fatalf("lx-фрагмент обязан определять локальный dns-сервер: %s", data)
	}
	custom, _, err := BuildConfig([]PoolSpec{spec}, Params{LX: true, Merged: true, ResolverTag: "mawg-local"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(custom), `"tag": "mawg-local"`) || !strings.Contains(string(custom), `"server": "mawg-local"`) {
		t.Fatalf("свой тег резолвера не подставился: %s", custom)
	}
	// не-lx фрагмент dns-секцию не добавляет вовсе (чужой dns не трогаем)
	plainSpec := PoolSpec{Name: "x", Tun: "tun1", TunIP: TuneIP(1), MixedPort: 2282,
		ProbeTarget: "http://www.gstatic.com/generate_204", Nodes: mustNodes(t, fakeVless)}
	plain, _, err := BuildConfig([]PoolSpec{plainSpec}, Params{Merged: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), `"dns"`) || strings.Contains(string(plain), "default_domain_resolver") {
		t.Fatalf("upstream-фрагмент не должен содержать dns/resolver: %s", plain)
	}
	if _, _, err := BuildConfig([]PoolSpec{spec}, Params{LX: true, Merged: true, ClashPort: 0}); err != nil {
		t.Fatalf("merged-профиль не должен требовать clash-порт: %v", err)
	}
	if _, _, err := BuildConfig([]PoolSpec{spec}, Params{ClashPort: 0}); err == nil {
		t.Fatal("own-профиль без clash-порта должен падать")
	}
}
