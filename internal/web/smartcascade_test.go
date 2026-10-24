package web

// Умный каскад - web-слой (86-док): PATCH пула создаёт выключенную
// служебную группу каскада, ручные кнопки on/off/check-now, валидация.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"mawg/internal/magitrickle"
	"mawg/internal/platform/fake"
	"mawg/internal/rotator"
	"mawg/internal/store"
	"mawg/internal/wgconf"
)

type smartMTFake struct {
	mu      sync.Mutex
	groups  []magitrickle.Group
	nextID  int
	chains  map[string]bool
	added   []string
	baseURL string
}

func newSmartMT(t *testing.T) *smartMTFake {
	t.Helper()
	magitrickle.SetShadowStorage(nil, nil)
	s := &smartMTFake{nextID: 200, chains: map[string]bool{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/groups":
			json.NewEncoder(w).Encode(map[string]any{"groups": s.groups})
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/groups":
			var req struct {
				Groups []magitrickle.Group `json:"groups"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			for i := range req.Groups {
				if req.Groups[i].ID == "" {
					s.nextID++
					req.Groups[i].ID = fmt.Sprintf("g%d", s.nextID)
					s.chains["MT_"+req.Groups[i].ID] = true
					s.added = append(s.added, req.Groups[i].Name)
				}
			}
			s.groups = req.Groups
			w.WriteHeader(200)
		case r.Method == http.MethodDelete && len(r.URL.Path) > len("/api/v1/groups/"):
			id := r.URL.Path[len("/api/v1/groups/"):]
			out := s.groups[:0]
			for _, g := range s.groups {
				if g.ID != id {
					out = append(out, g)
				}
			}
			s.groups = out
			w.WriteHeader(200)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { magitrickle.SetShadowStorage(nil, nil) })
	s.baseURL = srv.URL
	return s
}

func (s *smartMTFake) ipt(args ...string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case len(args) == 4 && args[2] == "-S" && args[3] == "OUTPUT":
		return "", nil // хуков не ставим - ChainExists важнее
	case len(args) == 5 && args[2] == "-L":
		if s.chains[args[3]] {
			return "Chain " + args[3], nil
		}
		return "", fmt.Errorf("no chain %s", args[3])
	}
	return "", nil
}

func smartServer(t *testing.T) (*httptest.Server, *smartMTFake, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	mt := newSmartMT(t)
	fb := fake.New()
	fb.SetProbe("proton", true)
	fb.SetProbe("warp", true)
	engine := rotator.New(st, fb, magitrickle.New(mt.baseURL))
	if engine.Cascade() != nil {
		engine.Cascade().SetIptRun(mt.ipt)
	}
	srv := New(st, engine, fb, magitrickle.New(mt.baseURL), "test", nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	for _, p := range []string{"proton", "warp"} {
		if _, err := st.CreatePool(p, store.PoolSettings{
			Platform: "fake", Fallback: store.FallbackDirect,
			CheckIntervalSec: 30, FailThreshold: 2, CooldownMin: 5,
		}); err != nil {
			t.Fatal(err)
		}
	}
	// каскад строит правила из эндпоинтов пула-источника
	conf := func(ep string) []byte {
		return []byte("[Interface]\nPrivateKey = QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE=\nAddress = 10.2.0.2/32\n" +
			"[Peer]\nPublicKey = QkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkI=\nEndpoint = " + ep + "\nAllowedIPs = 0.0.0.0/0\n")
	}
	for _, pc := range []struct{ pool, ep string }{{"proton", "203.0.113.10:51820"}, {"warp", "198.51.100.10:2408"}} {
		raw := conf(pc.ep)
		cfg, err := wgconf.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := st.AddConfigs(pc.pool, []wgconf.NamedConfig{
			{OriginalName: "c1.conf", Raw: raw, Config: cfg},
		}); err != nil {
			t.Fatal(err)
		}
	}
	return ts, mt, st
}

func smartReq(t *testing.T, method, url, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 1<<16)
	n, _ := resp.Body.Read(buf)
	return resp.StatusCode, string(buf[:n])
}

// PATCH с smartCascade: настройки сохранены, служебная группа создана
// ВЫКЛЮЧЕННОЙ (слово владельца), ручки отвечают
func TestSmartCascadePatchAndEndpoints(t *testing.T) {
	ts, mt, st := smartServer(t)

	code, body := smartReq(t, http.MethodPut, ts.URL+"/api/v1/pools/proton",
		`{"smartCascade":{"via":["warp"],"return":{"mode":"interval","intervalH":6},"minDwellMin":10}}`)
	if code != http.StatusOK {
		t.Fatalf("PATCH: %d %s", code, body)
	}
	p, _ := st.Pool("proton")
	if p.Settings.SmartCascade == nil || len(p.Settings.SmartCascade.Via) != 1 {
		t.Fatalf("настройки не сохранены: %+v", p.Settings.SmartCascade)
	}
	casc := st.Cascades()
	if len(casc) != 1 || casc[0].Source != "pool:proton" || casc[0].Via != "pool:warp" {
		t.Fatalf("каскад не создан: %+v", casc)
	}
	sst := st.State("proton")
	if sst.Smart == nil || sst.Smart.On || sst.Smart.Group != casc[0].Group {
		t.Fatalf("автомат должен ждать триггера выключенным: %+v", sst.Smart)
	}
	for _, g := range mt.snapshot() {
		if g.ID == casc[0].Group && g.Enable {
			t.Fatal("служебная группа создана включённой")
		}
	}

	// ручное «завернуть в каскад»
	code, body = smartReq(t, http.MethodPost, ts.URL+"/api/v1/pools/proton/smart-cascade/on", "{}")
	if code != http.StatusOK {
		t.Fatalf("on: %d %s", code, body)
	}
	if !st.State("proton").Smart.On {
		t.Fatalf("on не включил автомат: %s", body)
	}
	// прямой жив - «проверить сейчас» снимает
	code, body = smartReq(t, http.MethodPost, ts.URL+"/api/v1/pools/proton/smart-cascade/check-now", "{}")
	if code != http.StatusOK {
		t.Fatalf("check-now: %d %s", code, body)
	}
	if st.State("proton").Smart.On {
		t.Fatalf("check-now не снял каскад при живом прямом: %s", body)
	}
}

// валидация: сам себя via, неизвестный пул, кривой режим/время
func TestSmartCascadeValidation(t *testing.T) {
	ts, _, st := smartServer(t)
	for _, bad := range []string{
		`{"smartCascade":{"via":["proton"]}}`,
		`{"smartCascade":{"via":["нет такого"]}}`,
		`{"smartCascade":{"via":["warp"],"return":{"mode":"когда-нибудь"}}}`,
		`{"smartCascade":{"via":["warp"],"return":{"mode":"schedule","timeOfDay":["25:00"]}}}`,
		`{"smartCascade":{"via":["warp"],"minDwellMin":9999}}`,
		`{"smartCascade":{"via":["warp"],"triggerMode":"когда-нибудь"}}`,
		`{"smartCascade":{"via":["warp"],"triggerFails":99}}`,
	} {
		code, _ := smartReq(t, http.MethodPut, ts.URL+"/api/v1/pools/proton", bad)
		if code == http.StatusOK {
			t.Fatalf("ожидалась ошибка валидации: %s", bad)
		}
	}
	if p, _ := st.Pool("proton"); p.Settings.SmartCascade != nil {
		t.Fatal("невалидные настройки не должны сохраняться")
	}
}

// снятие настроек при живом автомате выключает каскад
func TestSmartCascadeDisableOnSettingsRemoval(t *testing.T) {
	ts, _, st := smartServer(t)
	code, _ := smartReq(t, http.MethodPut, ts.URL+"/api/v1/pools/proton",
		`{"smartCascade":{"via":["warp"],"return":{"mode":"manual"}}}`)
	if code != http.StatusOK {
		t.Fatal("настройки не сохранились")
	}
	code, body := smartReq(t, http.MethodPost, ts.URL+"/api/v1/pools/proton/smart-cascade/on", "{}")
	if code != http.StatusOK || !strings.Contains(body, `"on":true`) {
		t.Fatalf("on: %d %s", code, body)
	}
	code, body = smartReq(t, http.MethodPut, ts.URL+"/api/v1/pools/proton", `{"smartCascade":{"via":[]}}`)
	if code != http.StatusOK {
		t.Fatalf("снятие настроек: %d %s", code, body)
	}
	if p, _ := st.Pool("proton"); p.Settings.SmartCascade != nil {
		t.Fatal("настройки не сняты")
	}
	if st.State("proton").Smart.On {
		t.Fatal("каскад остался включённым после снятия настроек")
	}
}

func (s *smartMTFake) snapshot() []magitrickle.Group {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]magitrickle.Group(nil), s.groups...)
}
