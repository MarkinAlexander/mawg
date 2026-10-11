package magitrickle

import (
	"context"
	"encoding/json"
	"testing"
)

func TestDedupRemovesDuplicateRules(t *testing.T) {
	SetShadowStorage(nil, nil)
	srv, st := shadowServer(t)
	c := New(srv.URL)
	// прогрев тени: рантайм без правил
	if err := c.MutateGroups(context.Background(), func(groups []Group) bool { return false }); err != nil {
		t.Fatal(err)
	}

	var messages []string
	OnDedup = func(message string) { messages = append(messages, message) }
	defer func() { OnDedup = nil }()

	// в рантайм попадает группа с прямым дубликатом домена (разный регистр
	// и пробелы) и дубликатом другого типа - первый экземпляр остаётся
	dup := Group{ID: "g1", Name: "a", Interface: "nwg1", Enable: true, Rules: []Rule{
		{ID: "r1", Type: "domain", Rule: "example.com", Enable: true},
		{ID: "r2", Type: "domain", Rule: "  Example.COM ", Enable: true},
		{ID: "r3", Type: "subnet", Rule: "10.0.0.0/8", Enable: true},
		{ID: "r4", Type: "subnet", Rule: "10.0.0.0/8", Enable: true},
		{ID: "r5", Type: "domain", Rule: "example.org", Enable: true},
	}}
	if err := c.MutateGroups(context.Background(), func(groups []Group) bool {
		for i := range groups {
			if groups[i].ID == "g1" {
				groups[i].Rules = dup.Rules
				return true
			}
		}
		return false
	}); err != nil {
		t.Fatal(err)
	}

	var got []Rule
	for _, g := range st.snapshot() {
		if g.ID == "g1" {
			got = g.Rules
		}
	}
	if len(got) != 3 {
		t.Fatalf("правил после дедупа %d, want 3", len(got))
	}
	if got[0].ID != "r1" || got[1].ID != "r3" || got[2].ID != "r5" {
		t.Fatalf("первый экземпляр не сохранён: %v", ids(got))
	}
	if len(messages) != 1 || messages[0] != "a: 2" {
		t.Fatalf("сообщение дедупа: %v, want [a: 2]", messages)
	}

	// повторный no-op больше ничего не находит и не пишет
	before := len(messages)
	if err := c.MutateGroups(context.Background(), func(groups []Group) bool { return false }); err != nil {
		t.Fatal(err)
	}
	if len(messages) != before {
		t.Fatalf("дедуп сработал повторно: %v", messages)
	}
}

func ids(rules []Rule) []string {
	var out []string
	for _, rl := range rules {
		out = append(out, rl.ID)
	}
	return out
}

// страховка: PUT с дедупом не теряет группы (контроль усечения считает
// уже дедупнутые суммы)
func TestDedupKeepsGroupCount(t *testing.T) {
	SetShadowStorage(nil, nil)
	srv, st := shadowServer(t)
	c := New(srv.URL)
	if err := c.MutateGroups(context.Background(), func(groups []Group) bool { return false }); err != nil {
		t.Fatal(err)
	}
	if err := c.MutateGroups(context.Background(), func(groups []Group) bool {
		for i := range groups {
			if groups[i].ID == "g1" {
				groups[i].Rules = []Rule{
					{ID: "r1", Type: "domain", Rule: "dup.com", Enable: true},
					{ID: "r2", Type: "domain", Rule: "dup.com", Enable: true},
				}
				return true
			}
		}
		return false
	}); err != nil {
		t.Fatal(err)
	}
	if got := len(st.snapshot()); got != 4 {
		t.Fatalf("групп после записи с дедупом %d, want 4", got)
	}
	raw, _ := json.Marshal(st.snapshot()[0].Rules)
	if len(raw) == 0 {
		t.Fatal("пустые правила g1")
	}
}
