package links

// v2rayN-экспорт: подписка отдаёт массив полных Xray-конфигов, по узлу на
// элемент (панель 11.2026 - «неизвестная схема» на первом же JSON).
// Фикстуры синтетические: TEST-NET-адреса, фейковые uuid/ключи.

import (
	"strings"
	"testing"
)

const xrayExportArray = `[
 {"dns":{"hosts":{"dns.google":"8.8.4.4"},"servers":[{"tag":"dns-tunnel","address":"8.8.4.4"}]},
  "outbounds":[
   {"tag":"proxy","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.70","port":23747,
     "users":[{"id":"` + fakeUUID + `","encryption":"none","flow":""}]}]},
    "streamSettings":{"network":"tcp","security":"none","tcpSettings":{}}},
   {"tag":"direct","protocol":"freedom","settings":{"domainStrategy":"UseIPv4"}},
   {"tag":"block","protocol":"blackhole","settings":{}}
  ],
  "routing":{},"remarks":"node-plain v2rayTUN"},
 {"outbounds":[
   {"tag":"proxy","protocol":"vmess","settings":{"vnext":[{"address":"198.51.100.80","port":443,
     "users":[{"id":"` + fakeUUID2 + `","security":"auto","alterId":0}]}]},
    "streamSettings":{"network":"ws","security":"tls",
     "tlsSettings":{"serverName":"example.org","fingerprint":"chrome","alpn":["h2","http/1.1"]},
     "wsSettings":{"path":"/ws","headers":{"Host":"example.org"}}}},
   {"tag":"auto-choice","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.70","port":23747,
     "users":[{"id":"` + fakeUUID + `","encryption":"none"}]}]},
    "streamSettings":{"network":"tcp","security":"none"}},
   {"tag":"direct","protocol":"freedom","settings":{}}
  ],
  "remarks":"node-ws-tls"}
]`

func TestParseSubscriptionXrayArray(t *testing.T) {
	res := ParseSubscription("v2n", xrayExportArray)
	if len(res.Nodes) != 2 {
		t.Fatalf("узлов: %d, warnings: %v", len(res.Nodes), res.Warnings)
	}
	a := res.Nodes[0]
	if a.Type != "vless" || a.Host != "203.0.113.70" || a.Port != 23747 || a.UUID != fakeUUID {
		t.Fatalf("vless: %+v", a)
	}
	if a.Tag != "v2n|node-plain v2rayTUN" || a.TLS != nil || a.Transport != "" {
		t.Fatalf("тег/tls/transport: %+v", a)
	}
	b := res.Nodes[1]
	if b.Type != "vmess" || b.Security != "auto" || b.Transport != "ws" ||
		b.Path != "/ws" || b.HeaderHost != "example.org" {
		t.Fatalf("vmess: %+v", b)
	}
	if b.TLS == nil || b.TLS.Security != "tls" || b.TLS.SNI != "example.org" ||
		b.TLS.Fingerprint != "chrome" || len(b.TLS.ALPN) != 2 {
		t.Fatalf("tls: %+v", b.TLS)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("warnings: %v", res.Warnings)
	}
}

// Альтернативы (auto-*-choice) - полноценные узлы подписки: панель часть
// серверов отдаёт только ими. Выбранный узел зовётся по remarks
// конфига, альтернативы - по тегу.
func TestParseSubscriptionXrayChoices(t *testing.T) {
	cfg := `{"outbounds":[
	  {"tag":"proxy","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.71","port":1,
	    "users":[{"id":"` + fakeUUID + `","encryption":"none"}]}]}},
	  {"tag":"auto-choice","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.72","port":2,
	    "users":[{"id":"` + fakeUUID + `","encryption":"none"}]}]}},
	  {"tag":"auto-choice-2","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.73","port":3,
	    "users":[{"id":"` + fakeUUID + `","encryption":"none"}]}]}}
	 ],"remarks":"chosen-name"}`
	res := ParseSubscription("v2n", cfg)
	if len(res.Nodes) != 3 {
		t.Fatalf("узлов: %d, warnings: %v", len(res.Nodes), res.Warnings)
	}
	if res.Nodes[0].Tag != "v2n|chosen-name" || res.Nodes[1].Tag != "v2n|auto-choice" ||
		res.Nodes[2].Host != "203.0.113.73" {
		t.Fatalf("имена/узлы: %+v", res.Nodes)
	}
}

// base64-обёртка вокруг JSON-экспорта без единой proxy-схемы внутри
// (dns по IP) - decodeBody обязан признать содержимое подпиской.
func TestParseSubscriptionXrayBase64Wrapped(t *testing.T) {
	res := ParseSubscription("v2n", padlessB64(xrayExportArray))
	if len(res.Nodes) != 2 || res.Nodes[0].Type != "vless" {
		t.Fatalf("узлы: %d, warnings: %v", len(res.Nodes), res.Warnings)
	}
}

// Одиночный конфиг-объект Xray (reality+grpc), имя узла из remarks.
func TestParseSubscriptionXraySingle(t *testing.T) {
	cfg := `{"outbounds":[
	  {"tag":"proxy","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.90","port":49809,
	    "users":[{"id":"` + fakeUUID + `","encryption":"none","flow":"xtls-rprx-vision"}]}]},
	   "streamSettings":{"network":"grpc","security":"reality",
	    "realitySettings":{"serverName":"sni.example.org","fingerprint":"chrome",
	      "publicKey":"` + fakeKey('b') + `","shortId":"ab12"},
	    "grpcSettings":{"serviceName":"grpc-svc"}}},
	  {"tag":"direct","protocol":"freedom","settings":{}}
	 ],"remarks":"reality-grpc"}`
	res := ParseSubscription("v2n", cfg)
	if len(res.Nodes) != 1 {
		t.Fatalf("узлов: %d, warnings: %v", len(res.Nodes), res.Warnings)
	}
	n := res.Nodes[0]
	if n.Type != "vless" || n.Flow != "xtls-rprx-vision" || n.Transport != "grpc" ||
		n.ServiceName != "grpc-svc" || n.Tag != "v2n|reality-grpc" {
		t.Fatalf("нода: %+v", n)
	}
	if n.TLS == nil || n.TLS.Security != "reality" || n.TLS.SNI != "sni.example.org" ||
		n.TLS.PublicKey != fakeKey('b') || n.TLS.ShortID != "ab12" {
		t.Fatalf("tls: %+v", n.TLS)
	}
}

// Неизвестный движку протокол - честный warning, не нода-пустышка.
func TestParseSubscriptionXrayUnsupported(t *testing.T) {
	cfg := `{"outbounds":[
	  {"tag":"ss","protocol":"shadowsocks","settings":{"servers":[{"address":"203.0.113.95","port":8388,
	    "password":"x"}]}},
	  {"tag":"direct","protocol":"freedom","settings":{}}
	 ],"remarks":"ss-node"}`
	res := ParseSubscription("v2n", cfg)
	if len(res.Nodes) != 0 || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "shadowsocks") {
		t.Fatalf("узлы/warnings: %d %v", len(res.Nodes), res.Warnings)
	}
}

// Битый элемент массива пропускается с warning, соседние узлы живы.
func TestParseSubscriptionXrayArrayBadElement(t *testing.T) {
	body := `["битый-элемент",` + xrayExportArray[1:]
	res := ParseSubscription("v2n", body)
	if len(res.Nodes) != 2 {
		t.Fatalf("узлов: %d, warnings: %v", len(res.Nodes), res.Warnings)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "конфиг #1") {
		t.Fatalf("warnings: %v", res.Warnings)
	}
}

// Заглушка панели (панель: адрес 0.0.0.0, порт 1 - реальные адреса
// видит только приложение с привязкой устройства): узлы выкидываются с
// внятным warning, а не уезжают в пул мёртвыми.
func TestParseSubscriptionStubNodes(t *testing.T) {
	arr := `[
	 {"outbounds":[{"tag":"proxy","protocol":"vless","settings":{"vnext":[{"address":"0.0.0.0","port":1,
	   "users":[{"id":"` + fakeUUID + `","encryption":"none"}]}]}}],"remarks":"stub-node"}
	]`
	res := ParseSubscription("v2n", arr)
	if len(res.Nodes) != 0 {
		t.Fatalf("узлов: %d", len(res.Nodes))
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "заглушка") ||
		!strings.Contains(res.Warnings[0], "stub-node") || !strings.Contains(res.Warnings[0], "0.0.0.0:1") {
		t.Fatalf("warning: %v", res.Warnings)
	}
	// то же для одиночной ссылки в списке
	res = ParseSubscription("v2n", "vless://"+fakeUUID+"@0.0.0.0:1?encryption=none#stub\n")
	if len(res.Nodes) != 0 || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "заглушка") {
		t.Fatalf("линия: узлы %d, warnings %v", len(res.Nodes), res.Warnings)
	}
}
