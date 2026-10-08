package links

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestParseVlessRealityTCP(t *testing.T) {
	n, err := ParseLink("manual", vlessRealityTCP)
	if err != nil {
		t.Fatal(err)
	}
	if n.Type != "vless" || n.Tag != "manual|test-rocket" {
		t.Fatalf("type/tag: %q %q", n.Type, n.Tag)
	}
	if n.Host != "203.0.113.10" || n.Port != 23747 || n.UUID != fakeUUID {
		t.Fatalf("host/port/uuid: %+v", n)
	}
	if n.Encryption != "" {
		t.Fatalf("encryption=none должен исчезнуть, есть %q", n.Encryption)
	}
	if n.TLS == nil || n.TLS.Security != "reality" {
		t.Fatal("нет tls-блока reality")
	}
	if n.TLS.SNI != "www.example.com" || n.TLS.Fingerprint != "randomized" ||
		n.TLS.PublicKey != fakeKey('a') || n.TLS.ShortID != "deadbeef01" {
		t.Fatalf("tls: %+v", n.TLS)
	}
	if n.Transport != "" {
		t.Fatalf("tcp не даёт транспорта, есть %q", n.Transport)
	}
}

func TestParseVlessEncryptionVerbatimPQVIgnored(t *testing.T) {
	n, err := ParseLink("manual", vlessRealityGRPC)
	if err != nil {
		t.Fatal(err)
	}
	if n.Encryption != fakeMlkem {
		t.Fatalf("encryption должен лечь вербатимом, есть %q", n.Encryption)
	}
	if n.Transport != "grpc" || n.ServiceName != "" {
		t.Fatalf("transport/grpc: %+v", n)
	}
	if !strings.Contains(n.Raw, "pqv=") {
		t.Fatal("raw потерял исходную ссылку")
	}
}

func TestParseVlessSecurityNone(t *testing.T) {
	n, err := ParseLink("sub", vlessNoneHTTPUpgrade)
	if err != nil {
		t.Fatal(err)
	}
	if n.TLS != nil {
		t.Fatalf("security=none не должен давать tls, есть %+v", n.TLS)
	}
	if n.Transport != "httpupgrade" || n.Path != "/" {
		t.Fatalf("transport: %+v", n)
	}
	if n.Tag != "sub|httpupgrade-test" {
		t.Fatalf("tag: %q", n.Tag)
	}
}

func TestParseVlessXHTTPALPNCyrillicEmoji(t *testing.T) {
	n, err := ParseLink("marz", vlessXHTTPALPN)
	if err != nil {
		t.Fatal(err)
	}
	if n.TLS == nil || n.TLS.Security != "tls" {
		t.Fatal("нет tls")
	}
	wantALPN := []string{"h2", "http/1.1"}
	if len(n.TLS.ALPN) != 2 || n.TLS.ALPN[0] != wantALPN[0] || n.TLS.ALPN[1] != wantALPN[1] {
		t.Fatalf("alpn: %v", n.TLS.ALPN)
	}
	if n.Tag != "marz|"+fakeSubTag {
		t.Fatalf("тег должен декодироваться в UTF-8, есть %q", n.Tag)
	}
	if n.Mode != "packet-up" || n.Path != "/uploadfiles/" || n.HeaderHost != "cdn.example.org" {
		t.Fatalf("xhttp: %+v", n)
	}
}

func TestParseVlessXHTTPNone(t *testing.T) {
	n, err := ParseLink("s3", vlessXHTTPNone)
	if err != nil {
		t.Fatal(err)
	}
	if n.TLS != nil || n.Transport != "xhttp" || n.Mode != "auto" {
		t.Fatalf("unexpected: %+v", n)
	}
}

func TestParseTrojan(t *testing.T) {
	n, err := ParseLink("s4", trojanNoneHTTPUpgrade)
	if err != nil {
		t.Fatal(err)
	}
	if n.Type != "trojan" || n.Password != fakePass {
		t.Fatalf("trojan: %+v", n)
	}
	if n.TLS != nil || n.Transport != "httpupgrade" {
		t.Fatalf("security=none: %+v", n)
	}
}

func TestParseWireguardLink(t *testing.T) {
	n, err := ParseLink("s4", wireguardLink)
	if err != nil {
		t.Fatal(err)
	}
	if n.Type != "wireguard" || n.Tag != "s4|wg-test" {
		t.Fatalf("type/tag: %+v", n)
	}
	if n.PrivateKey != fakeKey('c') || n.PublicKey != fakeKey('e') || n.PresharedKey != fakeKey('d') {
		t.Fatalf("keys: %+v", n)
	}
	if n.Host != "203.0.113.30" || n.Port != 35091 || n.MTU != 1280 || n.Keepalive != 25 {
		t.Fatalf("endpoint: %+v", n)
	}
	if len(n.Addresses) != 1 || n.Addresses[0] != "10.0.0.7/32" {
		t.Fatalf("address: %v", n.Addresses)
	}
	if n.AWG != nil {
		t.Fatal("wireguard:// не должен давать awg-поля")
	}
}

func TestParseAmneziaWGLink(t *testing.T) {
	n, err := ParseLink("s4", amneziaWGLink)
	if err != nil {
		t.Fatal(err)
	}
	if n.Type != "amneziawg" || n.Tag != "s4|amw-test" {
		t.Fatalf("type/tag: %+v", n)
	}
	a := n.AWG
	if a == nil {
		t.Fatal("нет awg-полей")
	}
	if a.Jc != "6" || a.Jmin != "10" || a.Jmax != "50" || a.S1 != "56" || a.S3 != "24" {
		t.Fatalf("jc/s: %+v", a)
	}
	if a.H1 != "24294-85973" || a.H4 != "331092-380876" {
		t.Fatalf("диапазоны h: %+v", a)
	}
	if a.I1 != fakeCPS {
		t.Fatalf("i1 CPS-строка должна храниться как есть")
	}
	if n.MTU != 1296 {
		t.Fatalf("mtu: %d", n.MTU)
	}
}

func TestNodeFromConfAWG(t *testing.T) {
	n, err := NodeFromConf("files", "amw.conf", []byte(fakeConf))
	if err != nil {
		t.Fatal(err)
	}
	if n.Type != "amneziawg" || n.Tag != "files|amw.conf" {
		t.Fatalf("type/tag: %+v", n)
	}
	if n.Host != "203.0.113.30" || n.Port != 789 || n.MTU != 1296 || n.Keepalive != 25 {
		t.Fatalf("endpoint: %+v", n)
	}
	if n.AWG == nil || n.AWG.H1 != "24294-85973" || n.AWG.I1 != fakeCPS {
		t.Fatalf("awg: %+v", n.AWG)
	}
}

func TestNodeFromConfPlain(t *testing.T) {
	n, err := NodeFromConf("files", "wg.conf", []byte(plainWgConf))
	if err != nil {
		t.Fatal(err)
	}
	if n.Type != "wireguard" || n.AWG != nil {
		t.Fatalf("plain conf: %+v", n)
	}
}

func encodeVPNFixture(t *testing.T, conf string) string {
	t.Helper()
	lastConfig, err := json.Marshal(map[string]any{
		"H1": "24294-85973", "Jc": "6",
		"config": conf,
	})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := json.Marshal(map[string]any{
		"containers": []any{map[string]any{
			"container": "amnezia-awg",
			"awg":       map[string]any{"last_config": string(lastConfig), "port": 789},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var zbuf bytes.Buffer
	zw := zlib.NewWriter(&zbuf)
	if _, err := zw.Write(doc); err != nil {
		t.Fatal(err)
	}
	zw.Close()
	payload := make([]byte, 4)
	binary.BigEndian.PutUint32(payload, uint32(len(doc)))
	payload = append(payload, zbuf.Bytes()...)
	return "vpn://" + base64.RawURLEncoding.EncodeToString(payload)
}

func TestParseVPN(t *testing.T) {
	link := encodeVPNFixture(t, fakeConf)
	n, err := ParseLink("s4", link)
	if err != nil {
		t.Fatal(err)
	}
	if n.Type != "amneziawg" {
		t.Fatalf("type: %q", n.Type)
	}
	if n.Tag != "s4|amw-test" {
		t.Fatalf("имя должно браться из первого комментария conf, есть %q", n.Tag)
	}
	if n.AWG == nil || n.AWG.H2 != "100485-179345" || n.AWG.I1 != fakeCPS {
		t.Fatalf("awg: %+v", n.AWG)
	}
	if n.Host != "203.0.113.30" || n.Port != 789 {
		t.Fatalf("endpoint: %+v", n)
	}
}

func TestParseVPNRawConfFallback(t *testing.T) {
	var zbuf bytes.Buffer
	zw := zlib.NewWriter(&zbuf)
	doc := []byte(`{"containers":[{"container":"amnezia-awg","awg":{"last_config":` + strconv.Quote(plainWgConf) + `}}]}`)
	if _, err := zw.Write(doc); err != nil {
		t.Fatal(err)
	}
	zw.Close()
	payload := make([]byte, 4)
	binary.BigEndian.PutUint32(payload, uint32(len(doc)))
	payload = append(payload, zbuf.Bytes()...)
	link := "vpn://" + base64.RawURLEncoding.EncodeToString(payload)
	n, err := ParseLink("s4", link)
	if err != nil {
		t.Fatal(err)
	}
	if n.Type != "wireguard" || n.Host != "203.0.113.40" || n.Port != 51820 {
		t.Fatalf("сырой .conf в last_config: %+v", n)
	}
}

func TestParseSubscriptionBase64Padless(t *testing.T) {
	body := padlessB64(strings.Join(subLinks(), "\n"))
	res := ParseSubscription("sub1", body)
	if len(res.Nodes) != 3 {
		t.Fatalf("узлов: %d, warnings: %v", len(res.Nodes), res.Warnings)
	}
	if res.Nodes[0].Tag != "sub1|test-rocket" || res.Nodes[2].Type != "trojan" {
		t.Fatalf("теги: %+v", res.Nodes)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("warnings: %v", res.Warnings)
	}
}

func TestParseSubscriptionPlainLines(t *testing.T) {
	res := ParseSubscription("sub1", strings.Join(subLinks(), "\n"))
	if len(res.Nodes) != 3 {
		t.Fatalf("узлов: %d", len(res.Nodes))
	}
}

func TestParseSubscriptionUnknownScheme(t *testing.T) {
	body := vlessRealityTCP + "\nss://YWVzLTEyOC1nY206dGVzdA==@203.0.113.9:443#fake-ss\n"
	res := ParseSubscription("sub1", body)
	if len(res.Nodes) != 1 {
		t.Fatalf("узлов: %d", len(res.Nodes))
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "ss://") {
		t.Fatalf("warning: %v", res.Warnings)
	}
}

func TestParseSubscriptionJSON(t *testing.T) {
	cfg := `{
	  "outbounds": [
	    {"type": "selector", "tag": "PROXY", "outbounds": ["v", "wg"]},
	    {"type": "vless", "tag": "v", "server": "203.0.113.50", "server_port": 443,
	     "uuid": "` + fakeUUID + `", "flow": "xtls-rprx-vision",
	     "tls": {"enabled": true, "server_name": "example.org", "alpn": ["h2"],
	             "utls": {"enabled": true, "fingerprint": "chrome"},
	             "reality": {"enabled": true, "public_key": "` + fakeKey('a') + `", "short_id": "ab12"}},
	     "transport": {"type": "xhttp", "path": "/up", "mode": "auto"}},
	    {"type": "direct", "tag": "direct"}
	  ],
	  "endpoints": [
	    {"type": "wireguard", "tag": "wg", "address": ["10.0.0.7/32"],
	     "private_key": "` + fakeKey('c') + `", "mtu": 1280,
	     "peers": [{"address": "203.0.113.60", "port": 51820,
	                "public_key": "` + fakeKey('e') + `",
	                "persistent_keepalive_interval": 25}]}
	  ]
	}`
	res := ParseSubscription("sb", cfg)
	if len(res.Nodes) != 2 {
		t.Fatalf("узлов: %d, warnings: %v", len(res.Nodes), res.Warnings)
	}
	v := res.Nodes[0]
	if v.Type != "vless" || v.Tag != "sb|v" || v.Host != "203.0.113.50" || v.Port != 443 {
		t.Fatalf("vless: %+v", v)
	}
	if v.TLS == nil || v.TLS.Security != "reality" || v.TLS.ShortID != "ab12" ||
		len(v.TLS.ALPN) != 1 || v.TLS.ALPN[0] != "h2" {
		t.Fatalf("tls: %+v", v.TLS)
	}
	if v.Transport != "xhttp" || v.Path != "/up" || v.Flow != "xtls-rprx-vision" {
		t.Fatalf("transport/flow: %+v", v)
	}
	w := res.Nodes[1]
	if w.Type != "wireguard" || w.Tag != "sb|wg" || w.PrivateKey != fakeKey('c') ||
		w.Host != "203.0.113.60" || w.Port != 51820 || w.PublicKey != fakeKey('e') {
		t.Fatalf("wireguard: %+v", w)
	}
	if w.AWG != nil {
		t.Fatal("json endpoint без awg-полей не должен давать awg")
	}
}

func TestParseSubscriptionUserinfoHeader(t *testing.T) {
	info := ParseUserinfo("upload=3575; download=34514; total=0; expire=0")
	if info.Upload != 3575 || info.Download != 34514 || info.Total != 0 || info.Expire != 0 {
		t.Fatalf("userinfo: %+v", info)
	}
	if iv, ok := ParseUpdateInterval("12"); !ok || iv != 12 {
		t.Fatalf("interval: %v %v", iv, ok)
	}
	if _, ok := ParseUpdateInterval(""); ok {
		t.Fatal("пустой интервал не должен распознаваться")
	}
}

func TestFetchSubscription(t *testing.T) {
	body := padlessB64(strings.Join(subLinks(), "\n"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(subscriptionUserinfo, "upload=3575; download=34514; total=0; expire=0")
		w.Header().Set(profileUpdateInterval, "12")
		fmt.Fprint(w, body)
	}))
	defer srv.Close()
	f, err := Fetch(context.Background(), srv.URL+"/sub")
	if err != nil {
		t.Fatal(err)
	}
	if f.Sub.Upload != 3575 || f.Sub.UpdateIntervalHours != 12 {
		t.Fatalf("заголовки: %+v", f.Sub)
	}
	if len(f.Result.Nodes) != 3 {
		t.Fatalf("узлов: %d", len(f.Result.Nodes))
	}
	if f.Result.Nodes[0].Tag == "" || !strings.HasPrefix(f.Result.Nodes[0].Tag, "127.0.0.1|") {
		t.Fatalf("источник-префикс: %q", f.Result.Nodes[0].Tag)
	}
}

func TestParseVPNPremiumKeyHonestError(t *testing.T) {
	doc := `{"api_config":{"service_type":"amnezia-premium","service_protocol":"awg"},
	         "auth_data":{"api_key":"FAKE.KEY123"},"config_version":2,"name":"Amnezia Premium"}`
	var zbuf bytes.Buffer
	zw := zlib.NewWriter(&zbuf)
	zw.Write([]byte(doc))
	zw.Close()
	payload := make([]byte, 4)
	binary.BigEndian.PutUint32(payload, uint32(len(doc)))
	payload = append(payload, zbuf.Bytes()...)
	link := "vpn://" + base64.RawURLEncoding.EncodeToString(payload)
	_, err := ParseLink("s4", link)
	if err == nil {
		t.Fatal("ключ Amnezia Premium не должен разбираться как конфиг")
	}
	if !strings.Contains(err.Error(), "Amnezia premium API") || !strings.Contains(err.Error(), "awg") {
		t.Fatalf("текст ошибки: %v", err)
	}
}

func TestParseVPNBareZlibAndWireguardContainer(t *testing.T) {
	var zbuf bytes.Buffer
	zw := zlib.NewWriter(&zbuf)
	zw.Write([]byte(`{"containers":[{"container":"amnezia-wg","wireguard":{"last_config":` + strconv.Quote(plainWgConf) + `}}]}`))
	zw.Close()
	link := "vpn://" + base64.RawURLEncoding.EncodeToString(zbuf.Bytes()) // без 4-байтного префикса
	n, err := ParseLink("s4", link)
	if err != nil {
		t.Fatal(err)
	}
	if n.Type != "wireguard" || n.Host != "203.0.113.40" || n.Port != 51820 {
		t.Fatalf("wireguard-контейнер: %+v", n)
	}
}

func TestDecodeBodyRejectsPlainText(t *testing.T) {
	if _, ok := decodeBody("vless://" + fakeUUID + "@203.0.113.1:1#x"); ok {
		t.Fatal("чистый текст ссылок не должен считаться base64")
	}
}
