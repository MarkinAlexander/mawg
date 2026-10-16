package dnsmgr

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// реальный формат MT (yaml.v2: элементы списка на отступе ключа) -
// снят со стенда 0.42 (dist/stand42-config-20261016.yaml)
const sampleConfig = `configVersion: 0.8.2-rev6-mawgfix1
app:
  httpWeb:
    enabled: true
    auth:
      enabled: false
    host:
      address: '[::]'
      port: 8080
    skin: default
  dnsProxy:
    host:
      address: '[::]'
      port: 3553
    upstream:
      address: 8.8.8.8
      port: 53
    upstreams:
    - address: 8.8.8.8
      port: 53
    - address: 77.88.8.8
      port: 53
    health:
      enabled: true
      interval: 10s
      timeout: 2s
      failCycles: 3
      restoreCycles: 2
      restoreGap: 5m0s
    disableRemap53: false
  netfilter:
    iptables:
      chainPrefix: MT_
groups: []
`

// мини-парсер config.yaml: legacy upstream, список приоритета,
// чужие блоки (host/netfilter) не подмешиваются
func TestParseMTUpstreams(t *testing.T) {
	legacy, list := ParseMTUpstreams(sampleConfig)
	if legacy != "8.8.8.8:53" {
		t.Fatalf("legacy = %q", legacy)
	}
	if !reflect.DeepEqual(list, []string{"8.8.8.8:53", "77.88.8.8:53"}) {
		t.Fatalf("list = %v", list)
	}
	// рукописный стиль: элементы глубже ключа
	hand := "app:\n  dnsProxy:\n    upstreams:\n      - address: 8.8.8.8\n        port: 53\n      - address: 77.88.8.8\n        port: 53\n    upstream:\n      address: 127.0.0.1\n      port: 53\n"
	legacy2, list2 := ParseMTUpstreams(hand)
	if legacy2 != "127.0.0.1:53" || !reflect.DeepEqual(list2, []string{"8.8.8.8:53", "77.88.8.8:53"}) {
		t.Fatalf("рукописный стиль: %q %v", legacy2, list2)
	}
	if legacy3, list3 := ParseMTUpstreams("app:\n  dnsProxy:\n    upstream:\n      address: 127.0.0.1\n      port: 53\n"); legacy3 != "127.0.0.1:53" || len(list3) != 0 {
		t.Fatalf("legacy-only: %q %v", legacy3, list3)
	}
}

func TestParseUCIValues(t *testing.T) {
	got := parseUCIValues("dhcp.@dnsmasq[0].server='127.0.0.1#3553'\n")
	if !reflect.DeepEqual(got, []string{"127.0.0.1#3553"}) {
		t.Fatalf("одиночный: %v", got)
	}
	got = parseUCIValues("dhcp.@dnsmasq[0].server='192.168.0.1' '8.8.8.8'\ndhcp.@dnsmasq[0].server+='9.9.9.9'\n")
	if !reflect.DeepEqual(got, []string{"192.168.0.1", "8.8.8.8", "9.9.9.9"}) {
		t.Fatalf("список+приращение: %v", got)
	}
	if got := parseUCIValues(""); len(got) != 0 {
		t.Fatalf("пусто: %v", got)
	}
}

// canned-раннер: состояние собирается из выводов uci/iptables
func TestCollect(t *testing.T) {
	r := func(script string, timeout time.Duration) (string, error) {
		switch {
		case strings.Contains(script, "dhcp.@dnsmasq[0].server"):
			return "dhcp.@dnsmasq[0].server='127.0.0.1#3553'\n", nil
		case strings.Contains(script, "noresolv"):
			return "0\n", nil
		case strings.Contains(script, "cachesize"):
			return "1000\n", nil
		case strings.Contains(script, "MT_DNSOR"):
			return "3\n", nil
		}
		return "", nil
	}
	st, err := Collect(r, "")
	if err != nil {
		t.Fatal(err)
	}
	if !st.Dnsmasq.InMT || st.Dnsmasq.Strict || st.Dnsmasq.NoResolv {
		t.Fatalf("dnsmasq: %+v", st.Dnsmasq)
	}
	if st.Dnsmasq.CacheSize == nil || *st.Dnsmasq.CacheSize != 1000 {
		t.Fatalf("cachesize: %+v", st.Dnsmasq.CacheSize)
	}
	if !st.MT.Remap53 {
		t.Fatalf("remap53 не увиден")
	}

	// строгий режим (форкоп-стиль): noresolv=1 + cachesize=0
	r2 := func(script string, timeout time.Duration) (string, error) {
		switch {
		case strings.Contains(script, "noresolv"):
			return "1\n", nil
		case strings.Contains(script, "cachesize"):
			return "0\n", nil
		case strings.Contains(script, "MT_DNSOR"):
			return "0\n", nil
		}
		return "", nil
	}
	st2, _ := Collect(r2, "")
	if !st2.Dnsmasq.Strict {
		t.Fatalf("строгий режим не распознан: %+v", st2.Dnsmasq)
	}
	if st2.MT.Remap53 {
		t.Fatalf("remap53 должен быть false")
	}
}

// переключения: в MT - бэкап прядевого списка; обратно - рестор и чистка
// keenetic (71-док): только перехват MT_DNSOR + апстримы из конфига,
// dnsmasq-блок нулевой, платформа проставлена
func TestCollectKeenetic(t *testing.T) {
	calls := 0
	r := func(script string, timeout time.Duration) (string, error) {
		calls++
		if strings.Contains(script, "MT_DNSOR") {
			return "3\n", nil
		}
		t.Fatalf("неожиданный скрипт на keenetic: %s", script)
		return "", nil
	}
	st, err := CollectKeenetic(r, "")
	if err != nil {
		t.Fatal(err)
	}
	if st.Platform != PlatformKeenetic {
		t.Fatalf("platform = %q, want keenetic", st.Platform)
	}
	if !st.MT.Remap53 {
		t.Fatal("MT_DNSOR жив, а remap53=false")
	}
	if calls != 1 {
		t.Fatalf("ожидался 1 шелл-вызов, было %d", calls)
	}
	if st.Dnsmasq.InMT || st.Dnsmasq.Strict || len(st.Dnsmasq.Servers) != 0 {
		t.Fatalf("dnsmasq-блок должен быть нулевым: %+v", st.Dnsmasq)
	}

	// конфиг MT: апстримы читаются тем же парсером (реальный формат
	// yaml.v2 - sampleConfig со стенда 0.42)
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfg, []byte(sampleConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	r2 := func(script string, timeout time.Duration) (string, error) {
		return "3\n", nil
	}
	st2, err := CollectKeenetic(r2, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(st2.MT.Upstreams) != 2 || st2.MT.Upstreams[0] != "8.8.8.8:53" {
		t.Fatalf("апстримы из конфига: %+v", st2.MT)
	}

	// цепочки нет - перехвата нет
	r3 := func(script string, timeout time.Duration) (string, error) {
		return "0\n", nil
	}
	st3, _ := CollectKeenetic(r3, "")
	if st3.MT.Remap53 {
		t.Fatal("MT_DNSOR пуст, а remap53=true")
	}
}

// реальный формат строк из разведки 69-док (running-config Кинетика
// владельца): DoH dnsm, DoT без порта/sni, name-server по подключения,
// filter-profile upstream не должен попадать в глобальные
func TestParseKeeneticOwn(t *testing.T) {
	cfgText := `dns-proxy
    rebind-protect auto
    https upstream https://dns.google/dns-query dnsm
    tls upstream 1.1.1.1
    filter profile DnsProfile0 description antizapret
    filter profile DnsProfile0 dns53 upstream 7.8.8.8
    filter profile DnsProfile0 intercept enable
!
ip name-server 8.8.8.8 "" on ISP
ip name-server 8.8.4.4 "" on Wireguard5
ip name-server 10.2.0.1 "" on Wireguard6
ipv6 name-server 2001:4860:4860::8888 "" on Wireguard5
`
	own := ParseKeeneticOwn(cfgText)
	if len(own.DoH) != 1 || own.DoH[0].URI != "https://dns.google/dns-query" || own.DoH[0].Format != "dnsm" {
		t.Fatalf("DoH: %+v", own.DoH)
	}
	if len(own.DoT) != 1 || own.DoT[0].Address != "1.1.1.1" || own.DoT[0].Port != "" {
		t.Fatalf("DoT: %+v", own.DoT)
	}
	if len(own.Udp) != 3 {
		t.Fatalf("UDP: %+v", own.Udp)
	}
	if own.Udp[0].Interface != "ISP" || len(own.Udp[0].Servers) != 1 || own.Udp[0].Servers[0] != "8.8.8.8" {
		t.Fatalf("UDP ISP: %+v", own.Udp[0])
	}
	if len(own.V6) != 1 || own.V6[0].Servers[0] != "2001:4860:4860::8888" {
		t.Fatalf("V6: %+v", own.V6)
	}

	// DoT с портом и sni + http-формат json
	cfgText2 := `dns-proxy
    tls upstream 8.8.8.8 853 sni dns.google
    https upstream https://dns.adguard.com/dns-query json
!`
	own2 := ParseKeeneticOwn(cfgText2)
	if own2.DoT[0].Port != "853" || own2.DoT[0].SNI != "dns.google" {
		t.Fatalf("DoT порт/sni: %+v", own2.DoT[0])
	}
	if own2.DoH[0].Format != "json" {
		t.Fatalf("DoH формат: %+v", own2.DoH[0])
	}
}

func TestSetDnsmasqTarget(t *testing.T) {
	var lastScript string
	r := func(script string, timeout time.Duration) (string, error) {
		if strings.HasPrefix(script, "uci -q show") {
			if strings.Contains(script, "mawg_server_bak") {
				return "dhcp.@dnsmasq[0].mawg_server_bak='192.168.0.1'\n", nil
			}
			return "dhcp.@dnsmasq[0].server='127.0.0.1#3553'\n", nil
		}
		lastScript = script
		return "", nil
	}
	if err := SetDnsmasqTarget(r, "direct"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"delete dhcp.@dnsmasq[0].server",
		"add_list dhcp.@dnsmasq[0].server='192.168.0.1'",
		"delete dhcp.@dnsmasq[0].mawg_server_bak",
		"commit dhcp",
		"/etc/init.d/dnsmasq restart",
	} {
		if !strings.Contains(lastScript, want) {
			t.Fatalf("direct: нет %q в %q", want, lastScript)
		}
	}

	// в MT из прядевого списка: серверы прячутся в бэкап
	r2 := func(script string, timeout time.Duration) (string, error) {
		if strings.HasPrefix(script, "uci -q show") {
			if strings.Contains(script, "mawg_server_bak") {
				return "", nil
			}
			return "dhcp.@dnsmasq[0].server='192.168.0.1' '9.9.9.9'\n", nil
		}
		lastScript = script
		return "", nil
	}
	if err := SetDnsmasqTarget(r2, "mt"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"add_list dhcp.@dnsmasq[0].mawg_server_bak='192.168.0.1'",
		"add_list dhcp.@dnsmasq[0].mawg_server_bak='9.9.9.9'",
		"delete dhcp.@dnsmasq[0].server",
		"add_list dhcp.@dnsmasq[0].server='127.0.0.1#3553'",
	} {
		if !strings.Contains(lastScript, want) {
			t.Fatalf("mt: нет %q в %q", want, lastScript)
		}
	}

	if err := SetDnsmasqTarget(r2, "кривой"); err == nil {
		t.Fatalf("кривой режим не отвергнут")
	}
}
