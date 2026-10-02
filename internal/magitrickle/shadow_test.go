package magitrickle

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func shadowServer(t *testing.T) (*httptest.Server, *shadowState) {
	t.Helper()
	st := &shadowState{groups: []Group{
		{ID: "g1", Name: "a", Interface: "nwg1", Enable: true},
		{ID: "g2", Name: "b", Interface: "nwg2", Enable: true},
		{ID: "g3", Name: "c", Interface: "nwg3", Enable: true},
		{ID: "g4", Name: "d", Interface: "nwg4", Enable: true},
	}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		defer st.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/groups":
			json.NewEncoder(w).Encode(map[string]any{"groups": st.groups})
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/groups":
			var req struct {
				Groups []Group `json:"groups"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			st.groups = req.Groups
			w.WriteHeader(200)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, st
}

type shadowState struct {
	mu     sync.Mutex
	groups []Group
}

func (s *shadowState) snapshot() []Group {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Group(nil), s.groups...)
}

func (s *shadowState) dropIDs(ids ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kill := map[string]bool{}
	for _, id := range ids {
		kill[id] = true
	}
	var out []Group
	for _, g := range s.groups {
		if !kill[g.ID] {
			out = append(out, g)
		}
	}
	s.groups = out
}

func TestMutateGroupsSeedsShadow(t *testing.T) {
	SetShadowStorage(nil, nil)
	srv, st := shadowServer(t)
	c := New(srv.URL)
	if err := c.MutateGroups(context.Background(), func(groups []Group) bool { return false }); err != nil {
		t.Fatal(err)
	}
	if len(mtShadow) != len(st.snapshot()) {
		t.Fatalf("shadow = %d групп, want %d", len(mtShadow), len(st.snapshot()))
	}
}

func TestTruncatedRuntimeIsRepaired(t *testing.T) {
	SetShadowStorage(nil, nil)
	srv, st := shadowServer(t)
	c := New(srv.URL)
	if err := c.MutateGroups(context.Background(), func(groups []Group) bool { return false }); err != nil {
		t.Fatal(err)
	}
	st.dropIDs("g1", "g2", "g3")
	if err := c.MutateGroups(context.Background(), func(groups []Group) bool { return false }); err != nil {
		t.Fatal(err)
	}
	if got := len(st.snapshot()); got != 4 {
		t.Fatalf("после ремонта групп %d, want 4", got)
	}
}

func TestSingleDeletionRespected(t *testing.T) {
	SetShadowStorage(nil, nil)
	srv, st := shadowServer(t)
	c := New(srv.URL)
	if err := c.MutateGroups(context.Background(), func(groups []Group) bool { return false }); err != nil {
		t.Fatal(err)
	}
	st.dropIDs("g2")
	if err := c.MutateGroups(context.Background(), func(groups []Group) bool { return false }); err != nil {
		t.Fatal(err)
	}
	if got := len(st.snapshot()); got != 3 {
		t.Fatalf("одиночное удаление не уважено: групп %d, want 3", got)
	}
	if len(mtShadow) != 3 {
		t.Fatalf("тень не обновилась после удаления: %d, want 3", len(mtShadow))
	}
}

func TestFoughtGroupNotResurrectedForever(t *testing.T) {
	SetShadowStorage(nil, nil)
	srv, st := shadowServer(t)
	c := New(srv.URL)
	if err := c.MutateGroups(context.Background(), func(groups []Group) bool { return false }); err != nil {
		t.Fatal(err)
	}
	for round := 1; round <= 3; round++ {
		st.dropIDs("g1", "g2", "g3")
		if err := c.MutateGroups(context.Background(), func(groups []Group) bool { return false }); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(st.snapshot()); got != 1 {
		t.Fatalf("после трёх кругов групп %d, want 1 (g1-g3 признаны удалёнными)", got)
	}
}
