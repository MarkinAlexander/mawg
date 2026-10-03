package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"mawg/internal/magitrickle"
	"mawg/internal/platform/fake"
	"mawg/internal/rotator"
	"mawg/internal/store"
)

func newTestServerWithMT(t *testing.T, mtURL string) *httptest.Server {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fb := fake.New()
	e := rotator.New(st, fb, magitrickle.New(mtURL))
	srv := New(st, e, fb, magitrickle.New(mtURL), "test", nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

const fakeMTGroups = `{"groups":[
	{"id":"a","name":"ga","enable":true,"rules":[{"id":"1","type":"namespace","rule":"t.me","enable":true}]},
	{"id":"b","name":"gb","enable":true,"rules":[{"id":"2","type":"namespace","rule":"t.me","enable":true}]},
	{"id":"c","name":"gc","enable":false,"rules":[{"id":"3","type":"namespace","rule":"t.me","enable":true}]},
	{"id":"d","name":"gd","enable":true,"rules":[{"id":"4","type":"namespace","rule":"t.me","enable":false}]}
]}`

func TestMtDuplicatesSkipsDisabledGroupsAndRules(t *testing.T) {
	mt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/groups" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, fakeMTGroups)
	}))
	defer mt.Close()

	ts := newTestServerWithMT(t, mt.URL)
	resp, err := http.Get(ts.URL + "/api/v1/mt/duplicates")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status code %d", resp.StatusCode)
	}
	var out struct {
		Duplicates map[string]json.RawMessage `json:"duplicates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Duplicates) != 1 {
		t.Fatalf("ожидался 1 дубликат (две включённые группы), получено %d: %v",
			len(out.Duplicates), out.Duplicates)
	}
	if _, ok := out.Duplicates["t.me"]; !ok {
		t.Fatalf("дубликат t.me потерялся: %v", out.Duplicates)
	}
}

func TestMtApplyPresetSingleBulkPut(t *testing.T) {
	var mu sync.Mutex
	rulePosts := 0
	runtime := `{"groups":[]}`
	mt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/groups"):
			mu.Lock()
			io.WriteString(w, runtime)
			mu.Unlock()
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/groups":
			body, _ := io.ReadAll(r.Body)
			var in struct {
				Groups []map[string]any `json:"groups"`
			}
			_ = json.Unmarshal(body, &in)
			for _, g := range in.Groups {
				if g["id"] == nil || g["id"] == "" {
					g["id"] = "assigned"
				}
			}
			out, _ := json.Marshal(map[string]any{"groups": in.Groups})
			mu.Lock()
			runtime = string(out)
			mu.Unlock()
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/rules"):
			mu.Lock()
			rulePosts++
			mu.Unlock()
			io.WriteString(w, `{"id":"x"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer mt.Close()

	ts := newTestServerWithMT(t, mt.URL)
	resp, err := http.Post(ts.URL+"/api/v1/mt/presets/roblox/apply", "application/json",
		strings.NewReader(`{"interface":"nwg7"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	mu.Lock()
	defer mu.Unlock()
	if rulePosts != 0 {
		t.Fatalf("пресет создал правила по одному (POST rules = %d), должен быть один bulk PUT", rulePosts)
	}
	var out struct {
		Rules int `json:"rules"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out.Rules != 6 {
		t.Fatalf("правил в ответе %d, ожидалось 6", out.Rules)
	}
	if !strings.Contains(runtime, `"roblox"`) || !strings.Contains(runtime, "rbxcdn.com") {
		t.Fatalf("bulk PUT не содержал группу roblox с правилами: %s", runtime[:min(len(runtime), 300)])
	}
}
