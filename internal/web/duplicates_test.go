package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
	srv := New(st, e, fb, magitrickle.New(mtURL), "test")
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
