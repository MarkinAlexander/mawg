package cascade

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"mawg/internal/magitrickle"
	"mawg/internal/store"
	"mawg/internal/wgconf"
)

// репродукс живых жалоб: правила каскада не следили за составом пула -
// новые конфиги не попадали в каскад, адреса удалённых/выключенных
// оставались навсегда. SyncPoolRules должен приводить группу к текущему
// набору эндпоинтов ВКЛЮЧЁННЫХ конфигов и гасить каскад пустого пула
// ровно одним событием.
func TestSyncPoolRulesRebuild(t *testing.T) {
	magitrickle.SetShadowStorage(nil, nil)
	var state []magitrickle.Group
	mt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/groups":
			json.NewEncoder(w).Encode(map[string]any{"groups": state})
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/groups":
			var req struct {
				Groups []magitrickle.Group `json:"groups"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			for i := range req.Groups {
				if req.Groups[i].ID == "" {
					req.Groups[i].ID = "mock" + strconv.Itoa(i)
				}
			}
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
	if _, err := st.CreatePool("proton", store.PoolSettings{}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreatePool("warp", store.PoolSettings{}); err != nil {
		t.Fatal(err)
	}
	mk := func(file, endpoint string) []wgconf.NamedConfig {
		conf := "[Interface]\nPrivateKey=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaAAA=\nAddress=10.1.0.2/32\n[Peer]\nPublicKey=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbBBB=\nEndpoint=" + endpoint + "\nAllowedIPs=0.0.0.0/0\n"
		parsed, err := wgconf.IngestFile(file, []byte(conf))
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	}
	fileByEndpoint := func(pool, host string) string {
		p, ok := st.Pool(pool)
		if !ok {
			t.Fatalf("пул %s исчез", pool)
		}
		for _, c := range p.Configs {
			if strings.HasPrefix(c.Endpoint, host+":") || c.Endpoint == host {
				return c.File
			}
		}
		t.Fatalf("в пуле %s нет конфига с эндпоинтом %s", pool, host)
		return ""
	}
	if _, _, err := st.AddConfigs("proton", mk("a.conf", "198.51.100.7:51820")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.AddConfigs("warp", mk("w.conf", "162.159.192.6:2408")); err != nil {
		t.Fatal(err)
	}
	m := New(st, magitrickle.New(mt.URL))
	ctx := context.Background()

	g, err := m.Create(ctx, "pool:proton", "pool:warp", nil)
	if err != nil {
		t.Fatalf("каскад не создался: %v", err)
	}
	groupByID := func() magitrickle.Group {
		for _, gr := range state {
			if gr.ID == g.ID {
				return gr
			}
		}
		t.Fatal("группа каскада исчезла из MT")
		return magitrickle.Group{}
	}
	ruleCount := func() int { return len(groupByID().Rules) }

	// 1) новый конфиг в пуле -> его эндпоинт появляется в правилах
	if _, _, err := st.AddConfigs("proton", mk("c.conf", "203.0.113.9:51820")); err != nil {
		t.Fatal(err)
	}
	m.SyncPoolRules(ctx, "pool:proton")
	if !hasRule(groupByID().Rules, "subnet", "203.0.113.9/32") {
		t.Fatal("новый конфиг не попал в правила каскада")
	}
	base := ruleCount()
	cFile := fileByEndpoint("proton", "203.0.113.9")
	aFile := fileByEndpoint("proton", "198.51.100.7")

	// 2) выключили конфиг -> его адрес ушёл из правил, остальные живы
	if err := st.SetConfigEnabled("proton", cFile, false); err != nil {
		t.Fatal(err)
	}
	m.SyncPoolRules(ctx, "pool:proton")
	if hasRule(groupByID().Rules, "subnet", "203.0.113.9/32") {
		t.Fatal("адрес выключенного конфига остался в правилах")
	}
	if !hasRule(groupByID().Rules, "subnet", "198.51.100.7/32") {
		t.Fatal("живой конфиг пропал из правил")
	}
	if ruleCount() != base-1 {
		t.Fatalf("правил %d после выключения, want %d", ruleCount(), base-1)
	}

	// 3) удалили конфиг -> адрес ушёл из правил
	if err := st.RemoveConfig("proton", cFile); err != nil {
		t.Fatal(err)
	}
	m.SyncPoolRules(ctx, "pool:proton")
	if hasRule(groupByID().Rules, "subnet", "203.0.113.9/32") {
		t.Fatal("адрес удалённого конфига остался в правилах")
	}

	// 4) пул опустел -> каскад погашен, событие ровно одно (без спама)
	if err := st.SetConfigEnabled("proton", aFile, false); err != nil {
		t.Fatal(err)
	}
	m.SyncPoolRules(ctx, "pool:proton")
	if groupByID().Enable {
		t.Fatal("пустой пул не погасил каскад")
	}
	syncEvents := func() int {
		n := 0
		for _, ev := range st.Events() {
			if ev.Pool == "cascade" && ev.Kind == "sync" && strings.Contains(ev.Message, "каскад выключен") {
				n++
			}
		}
		return n
	}
	if n := syncEvents(); n != 1 {
		t.Fatalf("событий выключения %d, want 1", n)
	}
	m.SyncPoolRules(ctx, "pool:proton") // повторный sync молчит
	if n := syncEvents(); n != 1 {
		t.Fatalf("повторный sync насыпал событий: %d", n)
	}

	// 5) конфиг вернулся -> правила восстановились, каскад снова включён
	if err := st.SetConfigEnabled("proton", aFile, true); err != nil {
		t.Fatal(err)
	}
	m.SyncPoolRules(ctx, "pool:proton")
	if !groupByID().Enable {
		t.Fatal("каскад не вернулся после включения конфига")
	}
	if !hasRule(groupByID().Rules, "subnet", "198.51.100.7/32") {
		t.Fatal("правила не восстановились после возврата конфига")
	}

	// 6) пул удалён -> каскад гасится (отдельная ветка)
	if err := st.DeletePool("proton"); err != nil {
		t.Fatal(err)
	}
	m.SyncPoolRules(ctx, "pool:proton")
	if groupByID().Enable {
		t.Fatal("удаление пула-источника не погасило каскад")
	}
	if n := syncEvents(); n != 2 {
		t.Fatalf("событий выключения после удаления пула %d, want 2", n)
	}
}
