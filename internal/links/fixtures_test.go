package links

import (
	"encoding/base64"
	"net/url"
	"strings"
)

func fakeKey(b byte) string {
	k := make([]byte, 32)
	for i := range k {
		k[i] = b
	}
	return base64.StdEncoding.EncodeToString(k)
}

func fakeB64URL(n int) string {
	r := strings.Repeat("xK2f9Q7mLp3vR8nT5wZy1cE6jB4hNs0dA", 4)
	for len(r) < n {
		r += r
	}
	return r[:n]
}

const (
	fakeUUID      = "00000000-0000-4000-8000-000000000001"
	fakeUUID2     = "11111111-2222-4333-8444-555555555555"
	fakePass      = "test-password"
	fakeSubTag    = "\xF0\x9F\x87\xB7\xF0\x9F\x87\xBA \xD0\xA2\xD0\xB5\xD1\x81\xD1\x82 (\xD0\xBE\xD0\xB1\xD1\x85\xD0\xBE\xD0\xB4)"
	fakeSubTagEnc = "%F0%9F%87%B7%F0%9F%87%BA%20%D0%A2%D0%B5%D1%81%D1%82%20(%D0%BE%D0%B1%D1%85%D0%BE%D0%B4)"
)

var (
	fakePQV   = fakeB64URL(2728)
	fakeMlkem = "mlkem768x25519plus.native.0rtt." + fakeB64URL(43)
	fakeCPS   = "<b 0x1603010138010001340303><r 32><b 0x20><r 32><b 0x0020" + fakeB64URL(120) + ">"
)

var (
	vlessRealityTCP = "vless://" + fakeUUID + "@203.0.113.10:23747" +
		"?encryption=none&fp=randomized&pbk=" + fakeKey('a') +
		"&security=reality&sid=deadbeef01&sni=www.example.com&spx=%2Ffakepath&type=tcp#test-rocket"

	vlessRealityGRPC = "vless://" + fakeUUID + "@198.51.100.20:49809" +
		"?authority=&encryption=" + fakeMlkem + "&fp=chrome&pbk=" + fakeKey('b') +
		"&pqv=" + fakePQV + "&security=reality&serviceName=&sid=cafebabe02" +
		"&sni=aws.example.com&spx=%2Fanother&type=grpc#grpc-test"

	vlessNoneHTTPUpgrade = "vless://" + fakeUUID + "@198.51.100.20:54312" +
		"?encryption=none&host=&path=%2F&security=none&type=httpupgrade#httpupgrade-test"

	vlessXHTTPALPN = "vless://" + fakeUUID2 + "@cdn.example.org:443" +
		"?encryption=none&type=xhttp&path=%2Fuploadfiles%2F&host=cdn.example.org&mode=packet-up" +
		"&extra=%7B%22mode%22%3A%22packet-up%22%7D&security=tls&sni=cdn.example.org&fp=firefox" +
		"&alpn=h2%2Chttp%2F1.1#" + fakeSubTagEnc

	vlessXHTTPNone = "vless://" + fakeUUID + "@203.0.113.30:39216" +
		"?encryption=none&extra=%7B%22mode%22%3A%22auto%22%7D&host=&mode=auto&path=%2F" +
		"&security=none&type=xhttp&x_padding_bytes=100-1000#france-xhttp"

	trojanNoneHTTPUpgrade = "trojan://" + fakePass + "@203.0.113.30:55392" +
		"?host=&path=%2F&security=none&type=httpupgrade#trojantest"

	wireguardLink = "wireguard://" + fakeKey('c') + "@203.0.113.30:35091" +
		"?address=10.0.0.7%2F32&keepalive=25&mtu=1280&presharedkey=" + fakeKey('d') +
		"&publickey=" + fakeKey('e') + "#wg-test"

	amneziaWGLink = "amneziawg://" + fakeKey('c') + "@203.0.113.30:789" +
		"?address=10.200.0.4%2F32&dns=1.1.1.1%2C+1.0.0.1" +
		"&h1=24294-85973&h2=100485-179345&h3=201034-276124&h4=331092-380876" +
		"&i1=" + url.QueryEscape(fakeCPS) + "&jc=6&jmax=50&jmin=10&keepalive=25&mtu=1296" +
		"&presharedkey=" + fakeKey('d') + "&publickey=" + fakeKey('f') +
		"&s1=56&s2=32&s3=24&s4=12#amw-test"

	fakeConf = "# amw-test\n" +
		"[Interface]\n" +
		"PrivateKey = " + fakeKey('c') + "\n" +
		"Address = 10.200.0.4/32\n" +
		"DNS = 1.1.1.1, 1.0.0.1\n" +
		"MTU = 1296\n" +
		"Jc = 6\n" +
		"Jmin = 10\n" +
		"Jmax = 50\n" +
		"S1 = 56\n" +
		"S2 = 32\n" +
		"S3 = 24\n" +
		"S4 = 12\n" +
		"H1 = 24294-85973\n" +
		"H2 = 100485-179345\n" +
		"H3 = 201034-276124\n" +
		"H4 = 331092-380876\n" +
		"I1 = " + fakeCPS + "\n" +
		"\n[Peer]\n" +
		"PublicKey = " + fakeKey('f') + "\n" +
		"PresharedKey = " + fakeKey('d') + "\n" +
		"AllowedIPs = 0.0.0.0/0, ::/0\n" +
		"Endpoint = 203.0.113.30:789\n" +
		"PersistentKeepalive = 25\n"

	plainWgConf = "[Interface]\n" +
		"PrivateKey = " + fakeKey('c') + "\n" +
		"Address = 10.0.0.7/32\n" +
		"\n[Peer]\n" +
		"PublicKey = " + fakeKey('e') + "\n" +
		"AllowedIPs = 0.0.0.0/0\n" +
		"Endpoint = 203.0.113.40:51820\n"
)

func subLinks() []string {
	return []string{vlessRealityTCP, vlessNoneHTTPUpgrade, trojanNoneHTTPUpgrade}
}

func padlessB64(s string) string {
	return strings.TrimRight(base64.StdEncoding.EncodeToString([]byte(s)), "=")
}
