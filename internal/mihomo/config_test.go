package mihomo

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"mawg/internal/links"
	"mawg/internal/wgconf"
)

func vlessNode(tag string) links.Node {
	return links.Node{
		Type: "vless", Tag: "manual|" + tag, Host: "203.0.113.10", Port: 443,
		UUID: "00000000-0000-4000-8000-000000000001",
		Transport: "ws", Path: "/ws", HeaderHost: "cdn.example.org",
		TLS: &links.TLS{Security: "tls", SNI: "sni.example.org", Fingerprint: "chrome", ALPN: []string{"h2", "http/1.1"}},
	}
}

func parseYAML(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := yaml.Unmarshal(data, &out); err != nil {
		t.Fatalf("YAML не разбирается: %v\n%s", err, data)
	}
	return out
}

func TestBuildConfigBasics(t *testing.T) {
	spec := PoolSpec{
		Name: "proton", Tun: "tun2", TunIP: "172.19.2.1/30", MixedPort: 2382,
		ProbeTarget: "http://www.gstatic.com/generate_204", CheckIntervalSec: 90,
		Nodes: []links.Node{vlessNode("node-a")},
	}
	data, skipped, err := BuildConfig([]PoolSpec{spec}, Params{ClashPort: 2391})
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 0 {
		t.Fatalf("лишние skip: %v", skipped)
	}
	cfg := parseYAML(t, data)

	if cfg["external-controller"] != "127.0.0.1:2391" {
		t.Fatalf("external-controller: %v", cfg["external-controller"])
	}
	if cfg["mode"] != "rule" {
		t.Fatalf("mode: %v", cfg["mode"])
	}
	listeners := cfg["listeners"].([]any)
	if len(listeners) != 2 {
		t.Fatalf("два listener на пул: %v", listeners)
	}
	tun := listeners[0].(map[string]any)
	if tun["type"] != "tun" || tun["device"] != "tun2" || tun["proxy"] != "mawg-proton" {
		t.Fatalf("tun-listener: %v", tun)
	}
	// dns-hijack ОБЯЗАН быть пустым: дефолт listener-варианта перехватывает 0.0.0.0:53
	hj, _ := tun["dns-hijack"].([]any)
	if tun["dns-hijack"] == nil || len(hj) != 0 {
		t.Fatalf("dns-hijack должен быть пустым списком: %#v", tun["dns-hijack"])
	}
	if tun["auto-route"] != false {
		t.Fatalf("auto-route: %#v", tun["auto-route"])
	}
	if addrs, _ := tun["inet4-address"].([]any); len(addrs) != 1 || addrs[0] != "172.19.2.1/30" {
		t.Fatalf("inet4-address: %v", tun["inet4-address"])
	}
	mixed := listeners[1].(map[string]any)
	if mixed["type"] != "mixed" || mixed["listen"] != "127.0.0.1" || mixed["port"] != 2382 || mixed["proxy"] != "mawg-proton" {
		t.Fatalf("mixed-listener: %v", mixed)
	}

	proxies := cfg["proxies"].([]any)
	if len(proxies) != 1 {
		t.Fatalf("прокси: %v", proxies)
	}
	p := proxies[0].(map[string]any)
	if p["name"] != "mawg-proton|node-a" || p["type"] != "vless" || p["server"] != "203.0.113.10" {
		t.Fatalf("прокси: %v", p)
	}
	if p["tls"] != true || p["servername"] != "sni.example.org" || p["client-fingerprint"] != "chrome" {
		t.Fatalf("tls-поля: %v", p)
	}
	if p["network"] != "ws" {
		t.Fatalf("network: %v", p["network"])
	}
	ws := p["ws-opts"].(map[string]any)
	if ws["path"] != "/ws" || ws["headers"].(map[string]any)["Host"] != "cdn.example.org" {
		t.Fatalf("ws-opts: %v", ws)
	}

	groups := cfg["proxy-groups"].([]any)
	g := groups[0].(map[string]any)
	if g["name"] != "mawg-proton" || g["type"] != "url-test" {
		t.Fatalf("группа: %v", g)
	}
	if g["url"] != "http://www.gstatic.com/generate_204" || g["interval"] != 90 || g["tolerance"] != 10 {
		t.Fatalf("url-test параметры: %v", g)
	}
	if g["lazy"] != false {
		t.Fatalf("lazy: %#v (замеры честные, как у sing-box urltest)", g["lazy"])
	}
}

func TestBuildConfigSelectorDefault(t *testing.T) {
	spec := PoolSpec{
		Name: "p", Tun: "tun1", TunIP: "172.19.1.1/30", MixedPort: 2382,
		GroupMode: "selector", Selected: "mawg-p|second",
		Nodes: []links.Node{vlessNode("first"), vlessNode("second")},
	}
	data, _, err := BuildConfig([]PoolSpec{spec}, Params{ClashPort: 2391})
	if err != nil {
		t.Fatal(err)
	}
	cfg := parseYAML(t, data)
	g := cfg["proxy-groups"].([]any)[0].(map[string]any)
	if g["type"] != "select" {
		t.Fatalf("type: %v", g["type"])
	}
	if g["default-selected"] != "mawg-p|second" {
		t.Fatalf("выбранный узел должен переживать пересборку: %v", g["default-selected"])
	}
}

func TestBuildConfigSkipsUnsupported(t *testing.T) {
	xhttp := vlessNode("x1")
	xhttp.Transport = "xhttp"
	enc := vlessNode("enc")
	enc.Encryption = "mlkem768x25519plus.native.0rtt.FAKE"
	wgType := vlessNode("wg")
	wgType.Type = "wireguard"
	spec := PoolSpec{
		Name: "p", Tun: "tun1", TunIP: "172.19.1.1/30", MixedPort: 2382,
		Nodes: []links.Node{xhttp, enc, wgType},
	}
	_, skipped, err := BuildConfig([]PoolSpec{spec}, Params{ClashPort: 2391})
	if err == nil || !strings.Contains(err.Error(), "нет подходящих") {
		t.Fatalf("пул без подходящих узлов - ошибка, получили %v / %v", err, skipped)
	}
	for _, sub := range []string{"xhttp", "шифрование", "нативным пулом"} {
		if !anySkip(skipped, sub) {
			t.Fatalf("причина %q не объяснена: %v", sub, skipped)
		}
	}
}

func anySkip(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func TestBuildConfigWGAmnezia(t *testing.T) {
	jc, jmin, jmax := "3", "4", "8"
	s1 := "10"
	h1 := "99-120"
	hpk := "HPKEY=="
	spec := PoolSpec{
		Name: "free", Tun: "tun1", TunIP: "172.19.1.1/30", MixedPort: 2382,
		WG: []wgconf.Config{{
			PrivateKey: "PRIV=", Addresses: []string{"172.16.0.2/32", "fd00::2/128"}, MTU: 1280,
			AWG: wgconf.AWGParams{Jc: &jc, Jmin: &jmin, Jmax: &jmax, S1: &s1, H1: &h1, HeaderProtectionKey: &hpk},
			Peer: wgconf.Peer{
				PublicKey: "PUB=", EndpointHost: "engage.cloudflareclient.com", EndpointPort: 2408,
				AllowedIPs: []string{"0.0.0.0/0"}, PersistentKeepalive: 25,
			},
		}},
	}
	data, _, err := BuildConfig([]PoolSpec{spec}, Params{ClashPort: 2391})
	if err != nil {
		t.Fatal(err)
	}
	cfg := parseYAML(t, data)
	p := cfg["proxies"].([]any)[0].(map[string]any)
	if p["type"] != "wireguard" || p["name"] != "mawg-free|1" {
		t.Fatalf("wireguard-прокси: %v", p)
	}
	if p["ip"] != "172.16.0.2" || p["ipv6"] != "fd00::2" {
		t.Fatalf("адреса без CIDR: ip=%v ipv6=%v", p["ip"], p["ipv6"])
	}
	if p["mtu"] != 1280 || p["persistent-keepalive"] != 25 {
		t.Fatalf("mtu/keepalive: %v", p)
	}
	if p["allowed-ips"].([]any)[0] != "0.0.0.0/0" {
		t.Fatalf("allowed-ips: %v", p["allowed-ips"])
	}
	am := p["amnezia-wg-option"].(map[string]any)
	if am["jc"] != 3 || am["jmin"] != 4 || am["jmax"] != 8 || am["s1"] != 10 {
		t.Fatalf("awg 2.0: %v", am)
	}
	if am["h1"] != "99-120" {
		t.Fatalf("h1-диапазон: %v", am["h1"])
	}
	if am["version"] != 3 || am["header-protection-key"] != "HPKEY==" {
		t.Fatalf("awg 3.x: %v", am)
	}
}

func TestBuildConfigEscaping(t *testing.T) {
	n := vlessNode("слэш: узел #1 'х'")
	n.Type = "trojan"
	n.UUID = ""
	n.Password = "pass: #with 'quotes' & [brackets]"
	n.HeaderHost = "host: evil\ninjected: true"
	spec := PoolSpec{
		Name: "esc", Tun: "tun1", TunIP: "172.19.1.1/30", MixedPort: 2382,
		Nodes: []links.Node{n},
	}
	data, _, err := BuildConfig([]PoolSpec{spec}, Params{ClashPort: 2391})
	if err != nil {
		t.Fatal(err)
	}
	cfg := parseYAML(t, data)
	p := cfg["proxies"].([]any)[0].(map[string]any)
	if p["name"] != "mawg-esc|слэш: узел #1 'х'" {
		t.Fatalf("имя узла с спецсимволами потеряно: %q", p["name"])
	}
	if p["password"] != "pass: #with 'quotes' & [brackets]" {
		t.Fatalf("пароль потерян: %q", p["password"])
	}
	// перенос строки в Host сериализуется блоком, не рождая новых ключей:
	// round-trip возвращает исходную строку, ключа «injected» в дереве нет
	ws := p["ws-opts"].(map[string]any)
	if ws["headers"].(map[string]any)["Host"] != "host: evil\ninjected: true" {
		t.Fatalf("Host с переносом потерян: %q", ws["headers"])
	}
	if hasKey(cfg, "injected") {
		t.Fatalf("перенос строки не должен рождать новые YAML-ключи:\n%s", data)
	}
}

func hasKey(m map[string]any, key string) bool {
	for k, v := range m {
		if k == key {
			return true
		}
		if sub, ok := v.(map[string]any); ok && hasKey(sub, key) {
			return true
		}
	}
	return false
}
