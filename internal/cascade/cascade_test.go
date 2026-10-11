package cascade

import (
	"testing"

	"mawg/internal/magitrickle"
	"mawg/internal/store"
	"mawg/internal/wgconf"
)

func hasRule(rules []magitrickle.Rule, typ, rule string) bool {
	for _, r := range rules {
		if r.Type == typ && r.Rule == rule && r.Enable {
			return true
		}
	}
	return false
}

func TestBuildRules(t *testing.T) {
	res := map[string][]string{"engage.example.com": {"198.51.100.11", "198.51.100.11"}}
	rules := BuildRules([]string{
		"162.159.192.6",
		"engage.example.com:1387",
		"10.8.0.0/24",
		"engage.example.com",
		"162.159.192.6",
	}, res)
	if len(rules) != 4 {
		t.Fatalf("ожидались 4 уникальных правила, получено %d: %+v", len(rules), rules)
	}
	if !hasRule(rules, "subnet", "162.159.192.6/32") {
		t.Fatal("нет подсети для IP-эндпоинта")
	}
	if !hasRule(rules, "subnet", "10.8.0.0/24") {
		t.Fatal("нет подсети CIDR")
	}
	if !hasRule(rules, "domain", "engage.example.com") {
		t.Fatal("нет доменного правила")
	}
	if !hasRule(rules, "subnet", "198.51.100.11/32") {
		t.Fatal("нет резолва домена")
	}
}

func TestBuildRulesEmpty(t *testing.T) {
	if rules := BuildRules(nil, nil); len(rules) != 0 {
		t.Fatalf("пустой вход дал правила: %+v", rules)
	}
}

func TestViaEndpointIPs(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// пул с IP-эндпоинтом и доменным (домен не резолвим в синтетике -
	// просто не добавляет адресов)
	conf := "[Interface]\nPrivateKey=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaAAA=\nAddress=10.1.0.2/32\n[Peer]\nPublicKey=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbBBB=\nEndpoint=198.51.100.7:51820\nAllowedIPs=0.0.0.0/0\n"
	parsed, err := wgconf.IngestFile("a.conf", []byte(conf))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreatePool("warp", store.PoolSettings{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.AddConfigs("warp", parsed); err != nil {
		t.Fatal(err)
	}
	m := New(st, nil)
	ips := m.viaEndpointIPs("pool:warp")
	if len(ips) != 1 || ips[0] != "198.51.100.7" {
		t.Fatalf("эндпоинты: %v, want [198.51.100.7]", ips)
	}
	if ips := m.viaEndpointIPs("iface:nwg1"); ips != nil {
		t.Fatalf("iface-канал не даёт эндпоинтов: %v", ips)
	}
	if ips := m.viaEndpointIPs("pool:нет"); ips != nil {
		t.Fatalf("несуществующий пул: %v", ips)
	}
}
