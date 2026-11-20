package cascade

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"mawg/internal/magitrickle"
	"mawg/internal/store"
)

// стаб MT: версия + группы; PUT обновляет отдаёмое состояние (как живой
// MT с save=true) - иначе дрейф не сходится и endMutate->SyncHooks
// зацикливает синк.
func localOnlyFixture(t *testing.T, version string, groups []magitrickle.Group) (func() []magitrickle.Group, *[]magitrickle.Group, *Manager) {
	t.Helper()
	magitrickle.SetShadowStorage(nil, nil)
	mt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/version":
			json.NewEncoder(w).Encode(map[string]string{"version": version})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/groups":
			json.NewEncoder(w).Encode(map[string]any{"groups": groups})
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/groups":
			var req struct {
				Groups []magitrickle.Group `json:"groups"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			groups = req.Groups
			w.WriteHeader(200)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(mt.Close)
	return func() []magitrickle.Group { return groups }, &groups, nil
}

// «вся локалка» (дефолт пуловой группы без stored-политики) на rev23+
// получает localOnly=true одним bulk-PUT; «+ роутер» и «список» гвардят
// роутера сами; каскад, dns-tunnel и чужая группа вне пулов не трогаются.
// Старая ревизия (rev22) - синк молчит (поле проигнорировалось бы).
func TestSyncLocalOnly(t *testing.T) {
	cases := []struct {
		name    string
		version string
		want    bool // ожидаем PUT с p1.LocalOnly=true
	}{
		{"capable", "0.8.2-rev23-mawg-dev", true},
		{"old", "0.8.2-rev22-mawg-dev", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, err := store.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.CreatePool("proton", store.PoolSettings{}); err != nil {
				t.Fatal(err)
			}
			if err := st.SetGroupPolicy("p2", &store.GroupPolicy{SourcesMode: "all+router"}); err != nil {
				t.Fatal(err)
			}
			if err := st.SetGroupPolicy("p3", &store.GroupPolicy{SourcesMode: "list", Sources: []string{"10.0.0.9"}}); err != nil {
				t.Fatal(err)
			}
			if err := st.SetCascades([]store.CascadeEntry{{Group: "casc", Name: "каскад", Source: "pool:proton", Via: "pool:warp"}}); err != nil {
				t.Fatal(err)
			}
			groups := []magitrickle.Group{
				{ID: "p1", Name: "пул1", Interface: "proton", Enable: true},
				{ID: "p2", Name: "пул2", Interface: "proton", Enable: true},
				{ID: "p3", Name: "пул3", Interface: "proton", Enable: true},
				{ID: "casc", Name: "Служебная: proton через warp", Interface: "warp", Enable: true},
				// служебная по имени, но НЕ зарегистрированная в сторе (кейс
				// 0.62): флаг, поставленный зря, обязан сняться обратным дрейфом
				{ID: "svc", Name: "Служебная: x через y", Interface: "proton", Enable: true, LocalOnly: true},
				{ID: "dns", Name: "dns-tunnel", Interface: "proton", Enable: true},
				{ID: "foreign", Name: "чужая", Interface: "nwg7", Enable: true},
			}
			var put [][]magitrickle.Group
			served := &groups
			mt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/version":
					json.NewEncoder(w).Encode(map[string]string{"version": tc.version})
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/groups":
					json.NewEncoder(w).Encode(map[string]any{"groups": *served})
				case r.Method == http.MethodPut && r.URL.Path == "/api/v1/groups":
					var req struct {
						Groups []magitrickle.Group `json:"groups"`
					}
					json.NewDecoder(r.Body).Decode(&req)
					*served = req.Groups
					put = append(put, req.Groups)
					w.WriteHeader(200)
				default:
					w.WriteHeader(404)
				}
			}))
			t.Cleanup(mt.Close)

			m := New(st, magitrickle.New(mt.URL))
			m.iptRun = (&fakeIpt{chains: map[string]bool{}}).run
			m.syncLocalOnly(context.Background(), groups, st.GroupPoliciesAll())

			if !tc.want {
				if len(put) != 0 {
					t.Fatalf("старая ревизия: PUT не ожидался, got %d", len(put))
				}
				return
			}
			if len(put) != 1 {
				t.Fatalf("PUT-ов %d, want 1 (рекурсия синка?)", len(put))
			}
			byID := map[string]bool{}
			for _, g := range put[0] {
				byID[g.ID] = g.LocalOnly
			}
			if !byID["p1"] {
				t.Fatalf("p1 (вся локалка) остался без localOnly: %v", byID)
			}
			for _, id := range []string{"p2", "p3", "casc", "svc", "dns", "foreign"} {
				if byID[id] {
					t.Fatalf("%s неожиданно получил localOnly=true", id)
				}
			}
			if m.mutating != 0 {
				t.Fatalf("окно мутации не закрыто: %d", m.mutating)
			}
			// стаб сходимся: второй тик дрейфа не находит
			m.syncLocalOnly(context.Background(), *served, st.GroupPoliciesAll())
			if len(put) != 1 {
				t.Fatalf("после схождения синк пишет снова: PUT-ов %d", len(put))
			}
		})
	}
}

// дрейф обратно: политика сменилась на «+ роутер» - флаг снимается
func TestSyncLocalOnlyDriftBack(t *testing.T) {
	magitrickle.SetShadowStorage(nil, nil) // тень предыдущих тестов воскресила бы группы
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreatePool("proton", store.PoolSettings{}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetGroupPolicy("p1", &store.GroupPolicy{SourcesMode: "all+router"}); err != nil {
		t.Fatal(err)
	}
	groups := []magitrickle.Group{
		{ID: "p1", Name: "пул1", Interface: "proton", Enable: true, LocalOnly: true},
	}
	var put [][]magitrickle.Group
	served := &groups
	mt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/version":
			json.NewEncoder(w).Encode(map[string]string{"version": "0.8.2-rev23-mawg-dev"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/groups":
			json.NewEncoder(w).Encode(map[string]any{"groups": *served})
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/groups":
			var req struct {
				Groups []magitrickle.Group `json:"groups"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			*served = req.Groups
			put = append(put, req.Groups)
			w.WriteHeader(200)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(mt.Close)

	m := New(st, magitrickle.New(mt.URL))
	m.iptRun = (&fakeIpt{chains: map[string]bool{}}).run
	m.syncLocalOnly(context.Background(), groups, st.GroupPoliciesAll())

	if len(put) != 1 {
		t.Fatalf("PUT-ов %d, want 1; последний: %+v", len(put), put[len(put)-1])
	}
	if put[0][0].LocalOnly {
		t.Fatalf("флаг localOnly не снялся: %+v", put[0][0])
	}
}
