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
	"net/url"
	"strconv"
	"strings"
	"testing"

	"mawg/internal/wgconf"
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

// amneziawg:// с 3.x-полями в query (camel и snake-варианты): до фикса
// они молча терялись и узел 3.x деградировал до 2.0 - сервер молчал
// (116-док). Round-trip через ConfText обязан всё сохранить.
func TestParseAmneziaWG31Link(t *testing.T) {
	link := "amneziawg://" + fakeKey('c') + "@203.0.113.31:51820" +
		"?address=10.9.0.2%2F32&jc=4&jmin=40&jmax=70&s1=30&s2=30&s3=30&s4=30" +
		"&h1=99-120&i3=" + url.QueryEscape("<b 0x41424344>") +
		"&headerProtectionKey=RTmM0tUsXbRTmM0tUsXbRTmM0tUsXbRTmM0tUsXb" +
		"&contentPaddingAddition=10-100&rekeyAfterTime=100-120&rekeyTimeout=3-8" +
		"&reject_after_time=150-180&keepalive_timeout=7-13&maxHandshakeAttempts=15-20" +
		"&randomTrailers=on&disableCookies=1&publickey=" + fakeKey('f') + "#awg31"
	n, err := ParseLink("s4", link)
	if err != nil {
		t.Fatal(err)
	}
	a := n.AWG
	if a == nil {
		t.Fatal("нет awg-полей")
	}
	if a.HeaderProtectionKey != "RTmM0tUsXbRTmM0tUsXbRTmM0tUsXbRTmM0tUsXb" {
		t.Fatalf("hp: %+v", a)
	}
	if a.ContentPaddingAddition != "10-100" || a.RekeyAfterTime != "100-120" || a.RekeyTimeout != "3-8" {
		t.Fatalf("диапазоны 3.0: %+v", a)
	}
	if a.RejectAfterTime != "150-180" || a.KeepaliveTimeout != "7-13" || a.MaxHandshakeAttempts != "15-20" {
		t.Fatalf("snake-варианты 3.0: %+v", a)
	}
	if a.RandomTrailers != "on" || a.DisableCookies != "1" {
		t.Fatalf("флаги 3.1: %+v", a)
	}
	if a.I3 != "<b 0x41424344>" {
		t.Fatalf("i3: %q", a.I3)
	}
	if !a.AWG3() {
		t.Fatal("узел обязан считаться AWG 3.x")
	}
	// round-trip: ConfText -> Parse сохраняет 3.x полностью (111-док, задача Д)
	confText, err := n.ConfText()
	if err != nil {
		t.Fatal(err)
	}
	n2, err := NodeFromConf("s4", "", []byte(confText))
	if err != nil {
		t.Fatal(err)
	}
	if n2.AWG == nil || !n2.AWG.AWG3() ||
		n2.AWG.HeaderProtectionKey != a.HeaderProtectionKey ||
		n2.AWG.ContentPaddingAddition != a.ContentPaddingAddition ||
		n2.AWG.RandomTrailers != "on" || n2.AWG.DisableCookies != "1" ||
		n2.AWG.RejectAfterTime != "150-180" || n2.AWG.I3 != a.I3 {
		t.Fatalf("round-trip потерял 3.x:\n%+v\n---\n%s", n2.AWG, confText)
	}
}

// sing-box JSON-подписка с AWG 3.x: lx кладёт поля в корень эндпоинта
// (h-диапазоны и таймеры - СТРОКИ, до фикса валили unmarshal endpoint),
// extended - в объект amnezia (91-док). Оба обязаны собраться в полный
// amneziawg-узел.
func TestParseSubscriptionSBWireAWG3(t *testing.T) {
	hp := "RTmM0tUsXbRTmM0tUsXbRTmM0tUsXbRTmM0tUsXb"
	cfg := `{"endpoints": [
	  {"type": "wireguard", "tag": "lx-awg3", "address": ["10.7.0.2/32"],
	   "private_key": "` + fakeKey('c') + `", "mtu": 1280,
	   "jc": 5, "jmin": 50, "jmax": 1000, "s1": 30, "s2": 30, "s3": 30, "s4": 30,
	   "h1": "99-120", "h2": "19-33", "h3": "25-41", "h4": "70-135",
	   "i1": "<b 0x41424344>",
	   "header_protection_key": "` + hp + `", "content_padding_addition": "10-100",
	   "rekey_after_time": "100-120", "rekey_timeout": "3-8",
	   "reject_after_time": "150-180", "keepalive_timeout": "7-13",
	   "max_handshake_attempts": "15-20",
	   "random_trailers": true, "disable_cookies": false,
	   "peers": [{"address": "198.51.100.12", "port": 51820, "public_key": "` + fakeKey('e') + `",
	              "persistent_keepalive_interval": "25-35"}]},
	  {"type": "wireguard", "tag": "ext-awg3", "address": ["10.8.0.2/32"],
	   "private_key": "` + fakeKey('c') + `",
	   "amnezia": {"jc": 6, "jmin": 40, "jmax": 70, "s1": 30, "s2": 30, "s3": 30, "s4": 30,
	    "h1": "1", "h2": "2", "h3": "3", "h4": "4",
	    "header_protection_key": "` + hp + `", "rekey_after_time": "90-120",
	    "random_trailers": true, "disable_cookies": true},
	   "peers": [{"address": "198.51.100.13", "port": 51820, "public_key": "` + fakeKey('f') + `"}]}
	]}`
	res := ParseSubscription("sb", cfg)
	if len(res.Nodes) != 2 {
		t.Fatalf("узлов: %d, warnings: %v", len(res.Nodes), res.Warnings)
	}
	lx := res.Nodes[0]
	if lx.Type != "amneziawg" || lx.Tag != "sb|lx-awg3" {
		t.Fatalf("lx-узел: %+v", lx)
	}
	a := lx.AWG
	if a == nil {
		t.Fatal("lx: нет awg-полей")
	}
	if a.H1 != "99-120" || a.I1 != "<b 0x41424344>" {
		t.Fatalf("lx h/i: %+v", a)
	}
	if a.HeaderProtectionKey != hp || a.ContentPaddingAddition != "10-100" ||
		a.RekeyAfterTime != "100-120" || a.MaxHandshakeAttempts != "15-20" {
		t.Fatalf("lx 3.0: %+v", a)
	}
	if a.RandomTrailers != "on" || a.DisableCookies != "" {
		t.Fatalf("lx 3.1-флаги: %+v", a)
	}
	if lx.Host != "198.51.100.12" || lx.PublicKey != fakeKey('e') {
		t.Fatalf("lx peer: %+v", lx)
	}
	if lx.KeepaliveRange != "25-35" || lx.Keepalive != 0 {
		t.Fatalf("lx keepalive-диапазон: %q / %d", lx.KeepaliveRange, lx.Keepalive)
	}

	ext := res.Nodes[1]
	if ext.Type != "amneziawg" {
		t.Fatalf("ext-узел: %+v", ext)
	}
	ea := ext.AWG
	if ea == nil {
		t.Fatal("ext: нет amnezia-объекта")
	}
	if ea.HeaderProtectionKey != hp || ea.RekeyAfterTime != "90-120" ||
		ea.RandomTrailers != "on" || ea.DisableCookies != "on" || ea.Jc != "6" {
		t.Fatalf("ext amnezia: %+v", ea)
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

func b64vmess(json string) string {
	return "vmess://" + base64.StdEncoding.EncodeToString([]byte(json))
}

func TestParseVmessV2rayN(t *testing.T) {
	// живой узел подписки стенда 0.42 (2026-10-17): tcp, scy auto
	n, err := ParseLink("sub", b64vmess(`{"add":"203.0.113.7","id":"11111111-2222-3333-4444-555555555555","net":"tcp","port":36167,"ps":"vmess-test","scy":"auto","tls":"none","type":"none","v":"2"}`))
	if err != nil {
		t.Fatal(err)
	}
	if n.Type != "vmess" || n.Host != "203.0.113.7" || n.Port != 36167 {
		t.Fatalf("основа: %+v", n)
	}
	if n.UUID != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("uuid: %s", n.UUID)
	}
	if n.Tag != "sub|vmess-test" || n.Security != "auto" || n.Transport != "" || n.TLS != nil {
		t.Fatalf("vmess-поля: %+v", n)
	}
}

func TestParseVmessWS(t *testing.T) {
	n, err := ParseLink("sub", b64vmess(`{"add":"1.2.3.4","port":"8443","id":"u-1","ps":"vm-ws","scy":"chacha20-ietf-poly1305","aid":"8","tls":"true","sni":"ex.com","alpn":"h2,http/1.1","fp":"chrome","net":"ws","path":"/ws","host":"cdn.ex.com"}`))
	if err != nil {
		t.Fatal(err)
	}
	if n.Port != 8443 || n.AlterId != 8 {
		t.Fatalf("порт/aid: %+v", n)
	}
	// ietf-алиас -> канонический шифр ядра
	if n.Security != "chacha20-poly1305" {
		t.Fatalf("security: %s", n.Security)
	}
	if n.Transport != "ws" || n.Path != "/ws" || n.HeaderHost != "cdn.ex.com" {
		t.Fatalf("транспорт: %+v", n)
	}
	if n.TLS == nil || n.TLS.Security != "tls" || n.TLS.SNI != "ex.com" || n.TLS.Fingerprint != "chrome" {
		t.Fatalf("tls: %+v", n.TLS)
	}
	if len(n.TLS.ALPN) != 2 || n.TLS.ALPN[0] != "h2" {
		t.Fatalf("alpn: %v", n.TLS.ALPN)
	}
}

func TestParseVmessH2AndJunkSecurity(t *testing.T) {
	// h2 => transport http + TLS автоматически; aid 0 не пишем
	n, err := ParseLink("sub", b64vmess(`{"add":"h.com","port":443,"id":"u-2","ps":"vm-h2","scy":"aes-128-ctr","net":"h2","path":"/x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if n.Transport != "http" || n.TLS == nil {
		t.Fatalf("h2: %+v", n)
	}
	// неизвестный ядру шифр -> auto (не резектим весь узел)
	if n.Security != "auto" {
		t.Fatalf("security: %s", n.Security)
	}
}

func TestParseVmessLegacyAndBroken(t *testing.T) {
	// legacy: base64("auto:uuid@host:port?security=tls&type=ws#метка")
	raw := "vmess://" + base64.StdEncoding.EncodeToString([]byte("auto:u-3@5.6.7.8:443?security=tls&type=ws&path=%2Fw#leg-vm"))
	n, err := ParseLink("sub", raw)
	if err != nil {
		t.Fatal(err)
	}
	if n.Host != "5.6.7.8" || n.Port != 443 || n.UUID != "u-3" || n.Security != "auto" {
		t.Fatalf("legacy-основа: %+v", n)
	}
	if n.Tag != "sub|leg-vm" || n.Transport != "ws" || n.TLS == nil {
		t.Fatalf("legacy-хвосты: %+v", n)
	}
	if _, err := ParseLink("sub", b64vmess(`{"add":"a","port":1,"ps":"нет uuid"}`)); err == nil {
		t.Fatal("без uuid хотел ошибку")
	}
	if _, err := ParseLink("sub", "vmess://!!!!"); err == nil {
		t.Fatal("битый base64 хотел ошибку")
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

// Ссылка владельца (111-док §1, пароль фейковый): userinfo=auth,
// sni=IP валиден, alpn списком, fp=chrome, TLS по умолчанию даже при
// security=tls.
func TestParseHysteria2Owner(t *testing.T) {
	n, err := ParseLink("s4", hysteria2Owner)
	if err != nil {
		t.Fatal(err)
	}
	if n.Type != "hysteria2" || n.Host != "movk231.my.to" || n.Port != 443 {
		t.Fatalf("хост/порт/тип: %+v", n)
	}
	if n.Password != fakePass || n.Tag != "s4|Hysteria2-vitas" {
		t.Fatalf("auth/tag: %q %q", n.Password, n.Tag)
	}
	if n.TLS == nil || n.TLS.Security != "tls" || n.TLS.SNI != "203.0.113.40" ||
		n.TLS.Fingerprint != "chrome" || len(n.TLS.ALPN) != 1 || n.TLS.ALPN[0] != "h3" {
		t.Fatalf("TLS: %+v", n.TLS)
	}
	if n.TLS.Insecure {
		t.Fatal("insecure не выставлен в ссылке - должен быть false")
	}
}

// Вариации: insecure=1, obfs-пара, mport, up/down (111-док §3-А).
func TestParseHysteria2Variations(t *testing.T) {
	n, err := ParseLink("s4", hysteria2Full)
	if err != nil {
		t.Fatal(err)
	}
	if !n.TLS.Insecure {
		t.Fatal("insecure=1 не распознан")
	}
	if n.Obfs != "salamander" || n.ObfsPassword != fakePass+"2" {
		t.Fatalf("obfs-пара: %q/%q", n.Obfs, n.ObfsPassword)
	}
	if n.Mport != "50000-60000" || n.UpMbps != 100 || n.DownMbps != 500 {
		t.Fatalf("mport/up/down: %q %d/%d", n.Mport, n.UpMbps, n.DownMbps)
	}
}

// hysteria2 без auth-пароля - честная ошибка, а не нода-пустышка.
func TestParseHysteria2NoAuth(t *testing.T) {
	if _, err := ParseLink("s4", "hysteria2://203.0.113.50:443#noauth"); err == nil {
		t.Fatal("ссылка без userinfo не должна парситься")
	}
}

// hysteria2 в подписке: парсится в ноду, а не падает в warnings
// (до фикса схемы в перечислении не было вовсе).
func TestParseSubscriptionHysteria2(t *testing.T) {
	res := ParseSubscription("sub1", vlessRealityTCP+"\n"+hysteria2Owner+"\n")
	if len(res.Nodes) != 2 {
		t.Fatalf("узлов: %d", len(res.Nodes))
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("warnings: %v", res.Warnings)
	}
	if res.Nodes[1].Type != "hysteria2" {
		t.Fatalf("тип второго узла: %s", res.Nodes[1].Type)
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
	f, err := Fetch(context.Background(), srv.URL+"/sub", DeviceHeaders{})
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

// Подписки с лимитом устройств (Remnawave): клиент представляет себя
// заголовками x-hwid/x-device-os/x-device-model, ответ панели о лимите
// превращается в HwidLimit.
func TestFetchSubscriptionHwid(t *testing.T) {
	var gotHwid, gotOS string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHwid, gotOS = r.Header.Get("x-hwid"), r.Header.Get("x-device-os")
		w.Header().Set("x-hwid-max-devices-reached", "true")
		fmt.Fprint(w, padlessB64(strings.Join(subLinks(), "\n")))
	}))
	defer srv.Close()
	f, err := Fetch(context.Background(), srv.URL, DeviceHeaders{Hwid: "mawg-0123456789abcdef", OS: "mawg", Model: "openwrt"})
	if err != nil {
		t.Fatal(err)
	}
	if gotHwid != "mawg-0123456789abcdef" || gotOS != "mawg" {
		t.Fatalf("заголовки запроса: hwid=%q os=%q", gotHwid, gotOS)
	}
	if !f.HwidLimit {
		t.Fatal("x-hwid-max-devices-reached не распознан")
	}
	// без устройства заголовки не шлём (диагностический путь)
	if _, err := Fetch(context.Background(), srv.URL, DeviceHeaders{}); err != nil {
		t.Fatal(err)
	}
	if gotHwid != "" || gotOS != "" {
		t.Fatalf("без DeviceHeaders заголовки не нужны: %q %q", gotHwid, gotOS)
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

// Плоский флейвор AmneziaWG (нативный экспорт приложения): vpn:// = чистый
// base64 от INI-конфига с junk-параметрами, без zlib и JSON-обёртки.
// Реальный кейс пользователя 02.11 (110-док): «vpn://: тело не zlib».
func TestParseVPNPlainAWGIni(t *testing.T) {
	awgConf := "[Interface]\n" +
		"PrivateKey = " + fakeKey('c') + "\n" +
		"Address = 10.8.2.2/32\n" +
		"DNS = 1.1.1.1, 8.8.8.8\n" +
		"MTU = 1394\n" +
		"Jc = 6\nJmin = 51\nJmax = 240\nS1 = 127\nS2 = 28\nS3 = 29\nS4 = 26\n" +
		"H1 = 1\nH2 = 2\nH3 = 3\nH4 = 4\n" +
		"I1 = <r 156>\n" +
		"HeaderProtectionKey = " + fakeKey('h') + "\n" +
		"ContentPaddingAddition = 19-37\n" +
		"RekeyAfterTime = 109-145\n" +
		"RekeyTimeout = 3-4\n" +
		"RejectAfterTime = 194-263\n" +
		"KeepaliveTimeout = 9-14\n" +
		"MaxHandshakeAttempts = 25-47\n" +
		"\n# AmneziaWG-3.1-admin-awg\n[Peer]\n" +
		"PublicKey = " + fakeKey('e') + "\n" +
		"AllowedIPs = 0.0.0.0/0, ::/0\n" +
		"Endpoint = movk231.my.to:51822\n"
	link := "vpn://" + base64.RawURLEncoding.EncodeToString([]byte(awgConf))
	n, err := ParseLink("s4", link)
	if err != nil {
		t.Fatalf("плоский AWG-конфиг должен парситься: %v", err)
	}
	// NodeFromConf распознаёт AWG по junk-параметрам -> тип amneziawg
	if n.Type != "amneziawg" || n.Host != "movk231.my.to" || n.Port != 51822 {
		t.Fatalf("плоский AWG: %+v", n)
	}
	if n.AWG == nil {
		t.Fatal("AWG-параметры (junk) не сохранены в ноде")
	}
}

// Round-trip AWG 3.1 (111-док, задача Д): конфиг пользователя работает в
// Амнезии, но через vpn://-путь нода хранила только 2.0-поля - ConfText
// регенерировал .conf без спецпараметров, NeedsEngine был false, пул
// уходил нативным слотом как AWG 2.0 и сервер молча отвергал handshake.
// Ни одно поле §2а не должно теряться: ParseLink -> ConfText -> wgconf.
func TestAWG31RoundTripKeeps3xFields(t *testing.T) {
	link := "vpn://" + base64.RawURLEncoding.EncodeToString([]byte(awg31FullConf()))
	n, err := ParseLink("s4", link)
	if err != nil {
		t.Fatal(err)
	}
	if n.Type != "amneziawg" {
		t.Fatalf("тип: %s", n.Type)
	}
	a := n.AWG
	if a == nil || a.HeaderProtectionKey != fakeKey('h') ||
		a.ContentPaddingAddition != "19-37" || a.RekeyAfterTime != "109-145" ||
		a.RekeyTimeout != "3-4" || a.RejectAfterTime != "194-263" ||
		a.KeepaliveTimeout != "9-14" || a.MaxHandshakeAttempts != "25-47" ||
		a.I1 != "<r 156>" {
		t.Fatalf("3.x-поля потеряны в ноде: %+v", a)
	}
	if !a.AWG3() {
		t.Fatal("нода с 3.x-полями должна распознаваться как AWG3")
	}
	confText, err := n.ConfText()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := wgconf.Parse([]byte(confText))
	if err != nil {
		t.Fatalf("регенерированный .conf не парсится: %v", err)
	}
	if !cfg.AWG.AWG3() {
		t.Fatalf("ConfText потерял 3.x-поля:\n%s", confText)
	}
	if !cfg.NeedsEngine() || !cfg.AWG.HasRandomInit() {
		t.Fatalf("NeedsEngine/HasRandomInit: %v/%v - гейт пула сработает неверно",
			cfg.NeedsEngine(), cfg.AWG.HasRandomInit())
	}
	args := cfg.AWG.AscArgs()
	if len(args) != 31 {
		t.Fatalf("asc-хвост не полный (%d аргументов):\n%q", len(args), args)
	}
	// позиции: 0-8 префикс 2.0, 9-15 S3/S4/I1-I5, 16 HP-ключ, 17-28 шесть
	// пар min-max, 29-30 флаги
	want := []string{fakeKey('h'), "19", "37", "109", "145", "3", "4",
		"194", "263", "9", "14", "25", "47", "0", "0"}
	for i, w := range want {
		if args[16+i] != w {
			t.Fatalf("asc[%d] = %q, ждём %q\nвсе: %q", 16+i, args[16+i], w, args)
		}
	}
	if lvl := wgconf.AwgLevelOf([]byte(confText)); lvl != wgconf.Awg3 {
		t.Fatalf("уровень регенерата: %q, ждём awg3", lvl)
	}
}

// Мессенджер рвёт длинный base64 переносами - decodeBase64URL обязан
// их вычищать (инцидент 02.11: ссылка из Telegram со смятыми строками).
func TestParseVPNPlainAWGWrappedBase64(t *testing.T) {
	awgConf := "[Interface]\nPrivateKey = " + fakeKey('c') + "\nAddress = 10.8.2.2/32\n" +
		"\n[Peer]\nPublicKey = " + fakeKey('e') + "\nEndpoint = 203.0.113.41:51820\n"
	b64 := base64.RawURLEncoding.EncodeToString([]byte(awgConf))
	var wrapped strings.Builder
	for i, r := range b64 {
		if i > 0 && i%48 == 0 {
			wrapped.WriteString("\n")
		}
		wrapped.WriteRune(r)
	}
	n, err := ParseLink("s4", "vpn://"+wrapped.String())
	if err != nil {
		t.Fatalf("смятый переносами base64 должен парситься: %v", err)
	}
	if n.Host != "203.0.113.41" || n.Port != 51820 {
		t.Fatalf("wrapped: %+v", n)
	}
}

// Обрезанная ссылка (мессенджер свернул хвост под «...») - честная
// ошибка с подсказкой, а не «тело не zlib».
func TestParseVPNPlainAWGTruncated(t *testing.T) {
	awgConf := "[Interface]\nPrivateKey = " + fakeKey('c') + "\nAddress = 10.8.2.2/32\n"
	link := "vpn://" + base64.RawURLEncoding.EncodeToString([]byte(awgConf))
	_, err := ParseLink("s4", link)
	if err == nil {
		t.Fatal("обрезанный конфиг не должен парситься (нет [Peer])")
	}
	if !strings.Contains(err.Error(), "обрезан") {
		t.Fatalf("текст без подсказки об обрезке: %v", err)
	}
}

func TestDecodeBodyRejectsPlainText(t *testing.T) {
	if _, ok := decodeBody("vless://" + fakeUUID + "@203.0.113.1:1#x"); ok {
		t.Fatal("чистый текст ссылок не должен считаться base64")
	}
}

// Пост-квантовый REALITY (Xray 26.9.8+): support-x25519mlkem768=true
// распознаётся в TLS.Mlkem768; без флага - false (кейс vlessfrancmicro,
// 111-док §6: сервер молча уводит на камуфляж без key share).
func TestParseVlessRealityPQ(t *testing.T) {
	n, err := ParseLink("manual", vlessRealityPQ)
	if err != nil {
		t.Fatal(err)
	}
	if n.TLS == nil || n.TLS.EffectiveKeyShare() != "hybrid" {
		t.Fatalf("гибридный key share не распознан: %+v", n.TLS)
	}
	if n.TLS.SNI != "pq.example.org" || n.TLS.ShortID != "fake154" {
		t.Fatalf("reality-поля: %+v", n.TLS)
	}
	// key_share=classical - явный классический share (LxBox §457)
	nc, err := ParseLink("manual", "vless://00000000-0000-4000-8000-000000000002@203.0.113.10:13123"+
		"?encryption=none&fp=chrome&pbk=FAKE0public0key0for0tests0only0not0real0kz"+
		"&security=reality&sid=fake155&sni=classic.example.org&key_share=classical&type=tcp#cl")
	if err != nil {
		t.Fatal(err)
	}
	if nc.TLS == nil || nc.TLS.KeyShare != "classical" {
		t.Fatalf("classical key_share не распознан: %+v", nc.TLS)
	}
	// без флага - узел ничего не заявляет
	n2, err := ParseLink("manual", vlessRealityTCP)
	if err != nil {
		t.Fatal(err)
	}
	if n2.TLS != nil && n2.TLS.EffectiveKeyShare() != "" {
		t.Fatal("обычный reality не должен заявлять key share")
	}
	// легаси nodes.json с mlkem768=true - EffectiveKeyShare даёт гибрид
	legacy := &TLS{Security: "reality", Mlkem768: true}
	if legacy.EffectiveKeyShare() != "hybrid" {
		t.Fatalf("легаси Mlkem768 должен читаться как гибрид: %+v", legacy)
	}
}
