package dnsmgr

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

const sampleConfig = `configVersion: 0.8.2-rev6-mawgfix1
app:
  httpWeb:
    enabled: true
  dnsProxy:
    host:
      address: '[::]'
      port: 3553
    upstreams:
      - address: 8.8.8.8
        port: 53
      - address: 77.88.8.8
        port: 53
    upstream:
      address: 8.8.8.8
      port: 53
    disableRemap53: false
    timeout: 5000ms
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
	if legacy2, list2 := ParseMTUpstreams("app:\n  dnsProxy:\n    upstream:\n      address: 127.0.0.1\n      port: 53\n"); legacy2 != "127.0.0.1:53" || len(list2) != 0 {
		t.Fatalf("legacy-only: %q %v", legacy2, list2)
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
