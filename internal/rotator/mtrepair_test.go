package rotator

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"mawg/internal/magitrickle"
	"mawg/internal/platform/fake"
	"mawg/internal/store"
)

type restartCountingBackend struct {
	*fake.Fake
	restarts *atomic.Int32
}

func (b *restartCountingBackend) RestartMagitrickle() error {
	b.restarts.Add(1)
	return nil
}

// стоковый MT с грязными nf-объектами: PUT валится до рестарта демона,
// после рестарта та же запись проходит - applyGroupChanges обязан сам
// рестартнуть и повторить один раз.
func TestApplyGroupChangesRetriesAfterRestart(t *testing.T) {
	magitrickle.SetShadowStorage(nil, nil)
	var restarts atomic.Int32
	state := []magitrickle.Group{{ID: "g1", Name: "a", Enable: true}}
	mt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/groups":
			json.NewEncoder(w).Encode(map[string]any{"groups": state})
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/groups":
			if restarts.Load() == 0 {
				w.WriteHeader(500)
				return
			}
			var req struct {
				Groups []magitrickle.Group `json:"groups"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			state = req.Groups
			w.WriteHeader(200)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(mt.Close)

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rb := &restartCountingBackend{Fake: fake.New(), restarts: &restarts}
	e := New(st, rb, magitrickle.New(mt.URL))
	e.mtRestarts = nil

	err = e.applyGroupChanges("test", func(groups []magitrickle.Group) bool {
		return true // запись, которая на грязном MT падает
	})
	if err != nil {
		t.Fatalf("ретрай не спас запись: %v", err)
	}
	if restarts.Load() != 1 {
		t.Fatalf("рестартов %d, want 1", restarts.Load())
	}
	if len(state) != 1 || state[0].ID != "g1" {
		raw, _ := json.Marshal(state)
		t.Fatalf("рантайм после ретрая: %s", raw)
	}
}

// кап: седьмой рестарт в час не проходит
func TestMTRestartBudget(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := New(st, fake.New(), magitrickle.New("http://localhost:8080"))
	e.mtRestarts = nil
	for i := 0; i < mtHealHourlyCap; i++ {
		if !e.mtRestartBudget() {
			t.Fatalf("рестарт %d не прошёл, хотя лимит %d", i+1, mtHealHourlyCap)
		}
	}
	if e.mtRestartBudget() {
		t.Fatal("кап в час не сработал")
	}
	// час прошёл - бюджет снова есть
	e.Clock = func() time.Time { return time.Now().Add(time.Hour) }
	if !e.mtRestartBudget() {
		t.Fatal("бюджет не восстановился после часа")
	}
}
