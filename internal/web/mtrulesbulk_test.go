package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mawg/internal/magitrickle"
	"mawg/internal/platform/fake"
	"mawg/internal/rotator"
	"mawg/internal/store"
)

// bulk-enable правил группы (слово владельца: 465 правил по одному
// не нащёлкаешь) - группа уходит в MT одним PUT целиком.

func TestMTRulesEnableAll(t *testing.T) {
	magitrickle.SetShadowStorage(nil, nil)
	groups := []magitrickle.Group{
		{ID: "g1", Name: "geoblock", Enable: true, Rules: []magitrickle.Rule{
			{ID: "r1", Type: "domain", Rule: "a.com", Enable: false},
			{ID: "r2", Type: "domain", Rule: "b.com", Enable: false},
			{ID: "r3", Type: "domain", Rule: "c.com", Enable: true},
		}},
		{ID: "g2", Name: "другая", Enable: true, Rules: []magitrickle.Rule{
			{ID: "r9", Type: "domain", Rule: "x.com", Enable: true},
		}},
	}
	var puts int // сколько полных PUT группы дошло до MT
	mtSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/groups":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"groups": groups})
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/v1/groups/"):
			var g magitrickle.Group
			if err := json.NewDecoder(r.Body).Decode(&g); err != nil {
				w.WriteHeader(400)
				return
			}
			for i := range groups {
				if groups[i].ID == g.ID {
					groups[i] = g
				}
			}
			puts++
			w.WriteHeader(200)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(mtSrv.Close)

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fb := fake.New()
	e := rotator.New(st, fb, magitrickle.New(mtSrv.URL))
	srv := New(st, e, fb, magitrickle.New(mtSrv.URL), "test", nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// включить все в g1: 2 changed, чужая группа не тронута
	resp, err := http.Post(ts.URL+"/api/v1/mt/groups/g1/rules/enable", "application/json",
		strings.NewReader(`{"enable":true}`))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Ok      bool `json:"ok"`
		Total   int  `json:"total"`
		Changed int  `json:"changed"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if resp.StatusCode != 200 || !out.Ok || out.Total != 3 || out.Changed != 2 {
		t.Fatalf("enable: status %d, out %+v", resp.StatusCode, out)
	}
	if puts != 1 {
		t.Fatalf("ожидался 1 PUT группы, было %d", puts)
	}
	for _, r := range groups[0].Rules {
		if !r.Enable {
			t.Fatalf("правило %s осталось выключенным", r.ID)
		}
	}
	if !groups[1].Rules[0].Enable {
		t.Fatal("чужая группа затронута")
	}

	// выключить все: 3 changed
	resp, err = http.Post(ts.URL+"/api/v1/mt/groups/g1/rules/enable", "application/json",
		strings.NewReader(`{"enable":false}`))
	if err != nil {
		t.Fatal(err)
	}
	json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if out.Changed != 3 {
		t.Fatalf("disable: out %+v", out)
	}
	for _, r := range groups[0].Rules {
		if r.Enable {
			t.Fatalf("правило %s не выключилось", r.ID)
		}
	}

	// неопределённое состояние: всё уже выключено - changed 0, PUT нет
	putsAtStart := puts
	resp, err = http.Post(ts.URL+"/api/v1/mt/groups/g1/rules/enable", "application/json",
		strings.NewReader(`{"enable":false}`))
	if err != nil {
		t.Fatal(err)
	}
	json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if out.Changed != 0 || puts != putsAtStart {
		t.Fatalf("no-op сработал зря: out %+v, puts %d", out, puts)
	}

	// неизвестная группа - 400
	resp, err = http.Post(ts.URL+"/api/v1/mt/groups/nope/rules/enable", "application/json",
		strings.NewReader(`{"enable":true}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("неизвестная группа: status %d", resp.StatusCode)
	}
}
