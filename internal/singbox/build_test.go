package singbox

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"mawg/internal/links"
	"mawg/internal/wgconf"
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

func TestVmessOutbound(t *testing.T) {
	spec := PoolSpec{Name: "vm", Tun: "tun1", TunIP: "172.19.1.1/30", MixedPort: 2282,
		ProbeTarget: "http://www.gstatic.com/generate_204",
		Nodes: []links.Node{{
			Type: "vmess", Tag: "sub|vm-ws", Host: "1.2.3.4", Port: 8443,
			UUID: "u-1", Security: "chacha20-poly1305", AlterId: 8,
			Transport: "ws", Path: "/ws", HeaderHost: "cdn.ex.com",
			TLS: &links.TLS{Security: "tls", SNI: "ex.com"},
		}},
	}
	data, _, err := BuildConfig([]PoolSpec{spec}, Params{ClashPort: 2291})
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	var ob map[string]any
	for _, o := range cfg.Outbounds {
		if o["tag"] == "mawg-vm|vm-ws" {
			ob = o
		}
	}
	if ob == nil {
		t.Fatal("vmess-outbound не построен")
	}
	if ob["type"] != "vmess" || ob["security"] != "chacha20-poly1305" || ob["alter_id"].(float64) != 8 {
		t.Fatalf("vmess-поля: %v", ob)
	}
	tr, _ := ob["transport"].(map[string]any)
	if tr["type"] != "ws" || tr["path"] != "/ws" {
		t.Fatalf("транспорт: %v", tr)
	}
	tls, _ := ob["tls"].(map[string]any)
	if tls["server_name"] != "ex.com" {
		t.Fatalf("tls: %v", tls)
	}
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
	data, skipped, err := BuildConfig([]PoolSpec{spec}, Params{ClashPort: 2291, Profile: SourceLXID, Caps: capsOf(SourceLXID, "1.14.4-lx.30")})
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped: %v", skipped)
	}
	if !strings.Contains(string(data), `"xhttp"`) {
		t.Fatal("нет xhttp-транспорта")
	}
	if !strings.Contains(string(data), "x_padding_bytes") {
		t.Fatal("xhttp обязан нести x_padding_bytes (extended без него не проходит check)")
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
	data, _, err := BuildConfig([]PoolSpec{spec}, Params{Profile: SourceLXID, Caps: capsOf(SourceLXID, "1.14.4-lx.30"), Merged: true})
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
	custom, _, err := BuildConfig([]PoolSpec{spec}, Params{Profile: SourceLXID, Caps: capsOf(SourceLXID, "1.14.4-lx.30"), Merged: true, ResolverTag: "mawg-local"})
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
	if _, _, err := BuildConfig([]PoolSpec{spec}, Params{Profile: SourceLXID, Caps: capsOf(SourceLXID, "1.14.4-lx.30"), Merged: true, ClashPort: 0}); err != nil {
		t.Fatalf("merged-профиль не должен требовать clash-порт: %v", err)
	}
	if _, _, err := BuildConfig([]PoolSpec{spec}, Params{ClashPort: 0}); err == nil {
		t.Fatal("own-профиль без clash-порта должен падать")
	}
}

// Ссылка владельца 111-дока §1 (пароль фейковый): hysteria2-outbound с
// password/tls(server_name=IP, alpn); utls для QUIC не пишем, гварда
// fullCaps не нужно (hysteria2 в апстриме с 1.9).
func TestBuildConfigHysteria2Outbound(t *testing.T) {
	spec := PoolSpec{Name: "hy", Tun: "tun1", TunIP: TuneIP(1), MixedPort: 2282,
		Nodes: mustNodes(t, "hysteria2://test-password@movk231.my.to:443"+
			"?alpn=h3&fp=chrome&security=tls&sni=203.0.113.40#Hysteria2-vitas")}
	data, skipped, err := BuildConfig([]PoolSpec{spec}, Params{ClashPort: 2291})
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped: %v", skipped)
	}
	var cfg struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	var ob map[string]any
	for _, o := range cfg.Outbounds {
		if o["tag"] == "mawg-hy|Hysteria2-vitas" {
			ob = o
		}
	}
	if ob == nil {
		t.Fatalf("hysteria2-outbound не построен: %v", cfg.Outbounds)
	}
	if ob["type"] != "hysteria2" || ob["server"] != "movk231.my.to" || ob["server_port"].(float64) != 443 {
		t.Fatalf("шапка: %v", ob)
	}
	if ob["password"] != "test-password" {
		t.Fatalf("password: %v", ob["password"])
	}
	tls := ob["tls"].(map[string]any)
	if tls["server_name"] != "203.0.113.40" || tls["insecure"] != nil {
		t.Fatalf("tls: %v", tls)
	}
	if alpn, _ := tls["alpn"].([]any); len(alpn) != 1 || alpn[0] != "h3" {
		t.Fatalf("alpn: %v", tls["alpn"])
	}
	if _, has := tls["utls"]; has {
		t.Fatalf("utls у QUIC-протокола писать нельзя: %v", tls)
	}
	if _, has := ob["transport"]; has {
		t.Fatalf("у hysteria2 транспорта нет: %v", ob)
	}
}

// obfs-пара и полоса: up_mbps/down_mbps, obfs{type,password}; insecure=1
// -> tls.insecure.
func TestBuildConfigHysteria2ObfsBandwidth(t *testing.T) {
	spec := PoolSpec{Name: "hy", Tun: "tun1", TunIP: TuneIP(1), MixedPort: 2282,
		Nodes: mustNodes(t, "hysteria2://test-password@203.0.113.50:36812"+
			"?insecure=1&obfs=salamander&obfs-password=obfs-pass&upmbps=100&downmbps=500#hy2")}
	data, skipped, err := BuildConfig([]PoolSpec{spec}, Params{ClashPort: 2291})
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped: %v", skipped)
	}
	var cfg struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	var ob map[string]any
	for _, o := range cfg.Outbounds {
		if o["tag"] == "mawg-hy|hy2" {
			ob = o
		}
	}
	if ob == nil {
		t.Fatal("outbound не построен")
	}
	if ob["up_mbps"].(float64) != 100 || ob["down_mbps"].(float64) != 500 {
		t.Fatalf("полоса: %v", ob)
	}
	obf := ob["obfs"].(map[string]any)
	if obf["type"] != "salamander" || obf["password"] != "obfs-pass" {
		t.Fatalf("obfs: %v", obf)
	}
	tls := ob["tls"].(map[string]any)
	if tls["insecure"] != true {
		t.Fatalf("insecure=1 не дошёл: %v", tls)
	}
}

// Честные отказы: mport и чужой obfs - skip с причиной, не тихая потеря.
func TestBuildConfigHysteria2Skips(t *testing.T) {
	spec := PoolSpec{Name: "hy", Tun: "tun1", TunIP: TuneIP(1), MixedPort: 2282,
		Nodes: mustNodes(t, "hysteria2://test-password@203.0.113.50:36812"+
			"?mport=50000-60000#hop\n"+
			"hysteria2://test-password@203.0.113.51:36812?obfs=fake#weird")}
	_, skipped, err := BuildConfig([]PoolSpec{spec}, Params{ClashPort: 2291})
	if err == nil || !strings.Contains(err.Error(), "нет подходящих") {
		t.Fatalf("все узлы отбиты - ожидается ошибка: %v / %v", err, skipped)
	}
	joined := strings.Join(skipped, "; ")
	if !strings.Contains(joined, "mport") || !strings.Contains(joined, "salamander") {
		t.Fatalf("причины не объяснены: %v", skipped)
	}
}

// Пост-квантовый REALITY (Xray 26.9.8+): гейты по матрице возможностей
// (112-док 2.2). На свежем lx узел жив, key_share=hybrid в конфиге; на
// extended/upstream/lx до SPEC 083 - честный skip (апстрим вырезал
// X25519MLKEM768 - сервер уводит на камуфляжный сайт; живой кейс
// vlessfrancmicro, 111-док §6).
const fakeVlessPQ = "vless://00000000-0000-4000-8000-000000000001@203.0.113.10:13122" +
	"?encryption=none&fp=chrome&pbk=FAKE0public0key0for0tests0only0not0real0kz" +
	"&security=reality&sid=fake154&sni=pq.example.org" +
	"&support-x25519mlkem768=true&type=tcp#pq-reality"

const fakeVlessClassical = "vless://00000000-0000-4000-8000-000000000002@203.0.113.10:13123" +
	"?encryption=none&fp=chrome&pbk=FAKE0public0key0for0tests0only0not0real0kz" +
	"&security=reality&sid=fake155&sni=classic.example.org" +
	"&key_share=classical&type=tcp#classic-reality"

// capsOf - матрица возможностей ядра по источнику+версии (как Engine.Caps).
func capsOf(srcID, ver string) CoreCaps {
	return Engine{SourceID: srcID, Version: ver}.Caps()
}

func TestBuildConfigPQReality(t *testing.T) {
	mkSpec := func(link string) PoolSpec {
		return PoolSpec{Name: "pq", Tun: "tun1", TunIP: TuneIP(1), MixedPort: 2282,
			Nodes: mustNodes(t, link)}
	}

	lx := mkSpec(fakeVlessPQ)
	data, skipped, err := BuildConfig([]PoolSpec{lx}, Params{ClashPort: 2291, Profile: SourceLXID, Caps: capsOf(SourceLXID, "1.14.4-lx.30")})
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 0 {
		t.Fatalf("на lx PQ-узел жив: %v", skipped)
	}
	if !strings.Contains(string(data), `"key_share": "hybrid"`) {
		t.Fatalf("на lx должен рендериться key_share hybrid:\n%s", data)
	}

	// не-mlkem ядра: extended (без SPEC 083), апстрим, СТАРЫЙ lx (до 083)
	for _, tc := range []struct {
		name string
		p    Params
	}{
		{"extended", Params{ClashPort: 2291, Profile: SourceExtID, Caps: capsOf(SourceExtID, "1.14.3-extended-2.7.3")}},
		{"upstream", Params{ClashPort: 2291, Caps: capsOf(SourceUpID, "1.14.0")}},
		{"lx до SPEC 083", Params{ClashPort: 2291, Profile: SourceLXID, Caps: capsOf(SourceLXID, "1.14.0-lx.35")}},
	} {
		_, skipped, err := BuildConfig([]PoolSpec{mkSpec(fakeVlessPQ)}, tc.p)
		if err == nil {
			t.Fatalf("%s: PQ-узел не должен собираться", tc.name)
		}
		if len(skipped) == 0 || !strings.Contains(strings.Join(skipped, "; "), "X25519MLKEM768") {
			t.Fatalf("%s: причина не объяснена: %v", tc.name, skipped)
		}
	}

	// lx с 083, но БЕЗ параметра key_share (до v1.14.1-lx.4): узел жив
	// (гибрид - дефолт таких ядер), поле не пишем - его ядро не знает
	data, skipped, err = BuildConfig([]PoolSpec{mkSpec(fakeVlessPQ)},
		Params{ClashPort: 2291, Profile: SourceLXID, Caps: capsOf(SourceLXID, "1.14.0-lx.36")})
	if err != nil || len(skipped) != 0 {
		t.Fatalf("lx v1.14.0-lx.36: PQ-узел жив без поля: %v %v", err, skipped)
	}
	if strings.Contains(string(data), "key_share") {
		t.Fatalf("lx без параметра key_share не должен получать поле:\n%s", data)
	}
}

func TestBuildConfigKeyShareClassical(t *testing.T) {
	mkSpec := func() PoolSpec {
		return PoolSpec{Name: "cl", Tun: "tun1", TunIP: TuneIP(1), MixedPort: 2282,
			Nodes: mustNodes(t, fakeVlessClassical)}
	}
	// свежий lx: явный classical key_share рендерится
	data, _, err := BuildConfig([]PoolSpec{mkSpec()}, Params{ClashPort: 2291, Profile: SourceLXID, Caps: capsOf(SourceLXID, "1.14.4-lx.30")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"key_share": "classical"`) {
		t.Fatalf("на свежем lx ждём key_share classical:\n%s", data)
	}
	// extended: классика - дефолт не-lx ядер, узел жив, поле не пишем
	data, _, err = BuildConfig([]PoolSpec{mkSpec()}, Params{ClashPort: 2291, Profile: SourceExtID, Caps: capsOf(SourceExtID, "1.14.3-extended-2.7.3")})
	if err != nil {
		t.Fatalf("на extended классика жива: %v", err)
	}
	if strings.Contains(string(data), "key_share") {
		t.Fatalf("extended не знает key_share - поле быть не должно:\n%s", data)
	}
	// mlkem-ядро БЕЗ параметра: понесло бы гибрид, а узел ждёт классику -
	// честный skip
	_, skipped, err := BuildConfig([]PoolSpec{mkSpec()}, Params{ClashPort: 2291, Profile: SourceLXID, Caps: capsOf(SourceLXID, "1.14.0-lx.36")})
	if err == nil || len(skipped) == 0 || !strings.Contains(strings.Join(skipped, "; "), "классический key_share") {
		t.Fatalf("lx без параметра: ждём честный skip классики, получено err=%v skipped=%v", err, skipped)
	}
}

// Конфиг обязан быть байт-детерминированным: скип «идентичный фрагмент -
// без перезагрузки ядра» в Apply опирается на сравнение с прошлым файлом.
func TestBuildConfigDeterministic(t *testing.T) {
	spec := PoolSpec{Name: "demo", Tun: "tun1", TunIP: TuneIP(1), MixedPort: 2282, GroupMode: "selector", Nodes: mustNodes(t, fakeVless)}
	p := Params{ClashPort: 2291, Merged: true, ResolverTag: "local"}
	a, _, err := BuildConfig([]PoolSpec{spec}, p)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		b, _, err := BuildConfig([]PoolSpec{spec}, p)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(a, b) {
			t.Fatalf("поколение %d: конфиг недетерминирован (%d vs %d байт)", i, len(a), len(b))
		}
	}
}


// EligibleWG: апстрим AWG не умеет - один AWG-конфиг не должен валить
// всю сборку движка (класс «распарсилось, но применилось не всё»,
// 116-док); lx/extended поднимают и конфиг-минимум из одних 3.1-флагов
func TestEligibleWGUpstream(t *testing.T) {
	base := func(awg wgconf.AWGParams) wgconf.Config {
		return wgconf.Config{
			PrivateKey: "PRIV=", Addresses: []string{"172.16.0.2/32"},
			AWG:        awg,
			Peer:       wgconf.Peer{PublicKey: "PUB=", EndpointHost: "198.51.100.11", EndpointPort: 51820},
		}
	}
	plain := base(wgconf.AWGParams{})
	jc, rt := "4", "on"
	awg20 := base(wgconf.AWGParams{Jc: &jc})
	awg31Only := base(wgconf.AWGParams{RandomTrailers: &rt}) // минимум: только флаг 3.1

	// апстрим: AWG (2.0 и 3.x) выфильтровывается, чистый WG живёт
	kept, reasons := EligibleWG([]wgconf.Config{plain, awg20, awg31Only}, capsOf(SourceUpID, "1.14.4"))
	if len(kept) != 1 || len(reasons) != 2 {
		t.Fatalf("апстрим: kept=%d reasons=%d (%v)", len(kept), len(reasons), reasons)
	}
	if !strings.Contains(reasons[0], "lx или extended") {
		t.Fatalf("reason без подсказки ядра: %v", reasons)
	}

	// lx/extended: всё живо
	for _, src := range []string{SourceLXID, SourceExtID} {
		kept, reasons = EligibleWG([]wgconf.Config{plain, awg20, awg31Only}, capsOf(src, "1.14.4-lx.30"))
		if len(kept) != 3 || len(reasons) != 0 {
			t.Fatalf("%s: kept=%d reasons=%v", src, len(kept), reasons)
		}
	}
}

// подписки дают разным узлам одинаковые имена (панель 12.11: два разных
// #fr-grpc, tcp и grpc) - дубль тега ронял ВЕСЬ конфиг ядра; теперь
// теги уникализируются суффиксом, оба узла живут в группе
func TestBuildConfigDuplicateNodeNames(t *testing.T) {
	a := "vless://00000000-0000-4000-8000-000000000001@203.0.113.10:443?encryption=none&security=tls&sni=a.example.org&type=tcp#fr-grpc"
	b := "vless://00000000-0000-4000-8000-000000000002@203.0.113.11:443?encryption=none&security=tls&sni=b.example.org&type=grpc#fr-grpc"
	spec := PoolSpec{Name: "dup", Tun: "tun1", TunIP: TuneIP(1), MixedPort: 2282,
		Nodes: mustNodes(t, a+"\n"+b)}
	data, _, err := BuildConfig([]PoolSpec{spec}, Params{ClashPort: 2291})
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Outbounds []struct {
			Tag string `json:"tag"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, ob := range cfg.Outbounds {
		if ob.Tag == "" {
			continue
		}
		if seen[ob.Tag] {
			t.Fatalf("дубль тега: %s", ob.Tag)
		}
		seen[ob.Tag] = true
	}
	if !seen["mawg-dup|fr-grpc"] || !seen["mawg-dup|fr-grpc-2"] {
		t.Fatalf("оба узла обязаны попасть в конфиг: %v", seen)
	}
}
