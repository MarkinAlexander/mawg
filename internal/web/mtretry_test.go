package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"mawg/internal/magitrickle"
	"mawg/internal/platform/fake"
	"mawg/internal/rotator"
	"mawg/internal/store"
)

// правка группы из панели на Кинетике: первый bulk-PUT ловит 500 на
// грязных nf-объектах - веб-путь обязан долечиться рестартом и ретраем
// (кейс владельца: правка telega теряла группу)
func TestMTUpdateGroupRetriesAfter500(t *testing.T) {
	magitrickle.SetShadowStorage(nil, nil)
	var restarts atomic.Int32
	var state []magitrickle.Group
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
	state = []magitrickle.Group{
		{ID: "g1", Name: "telega", Interface: "nwg7", Enable: true, Rules: []magitrickle.Rule{
			{ID: "r1", Type: "domain", Rule: "t.me", Enable: true},
		}},
	}
	rb := &restartCountingBackendWeb{Fake: fake.New(), restarts: &restarts}
	e := rotator.New(st, rb, magitrickle.New(mt.URL))
	srv := New(st, e, rb, magitrickle.New(mt.URL), "test", nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp, err := httpPut(ts.URL+"/api/v1/mt/groups/g1", `{"interface":"nwg9"}`)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("правка группы не долечилась: status %d, body %s", resp.StatusCode, raw)
	}
	if restarts.Load() != 1 {
		t.Fatalf("рестартов %d, want 1", restarts.Load())
	}
	if len(state) == 0 || state[0].Interface != "nwg9" {
		raw, _ := json.Marshal(state)
		t.Fatalf("правка не применилась после ретрая: %s", raw)
	}
	if len(state[0].Rules) != 1 {
		t.Fatal("правила группы потерялись при ретрае")
	}
}

func httpPut(url, body string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPut, url, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return http.DefaultClient.Do(req)
}

type restartCountingBackendWeb struct {
	*fake.Fake
	restarts *atomic.Int32
}

func (b *restartCountingBackendWeb) RestartMagitrickle() error {
	b.restarts.Add(1)
	return nil
}
