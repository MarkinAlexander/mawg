package cascade

import (
	"testing"

	"mawg/internal/magitrickle"
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
