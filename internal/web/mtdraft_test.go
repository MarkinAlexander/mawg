package web

// Черновик записей MagiTrickle (114-док): автосохранение выключено -
// правки копятся в base/mt-draft.json и пишутся в MT одним PUT по команде.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mawg/internal/magitrickle"
	"mawg/internal/platform/fake"
	"mawg/internal/rotator"
	"mawg/internal/store"
)

// (а) dirty-дифф: изменённые/добавленные/удалённые группы и правила - числа
func TestMTDraftDirty(t *testing.T) {
	var srv Server
	base := []magitrickle.Group{
		{ID: "g1", Name: "гео", Enable: true, Interface: "awg0", Rules: []magitrickle.Rule{
			{ID: "r1", Type: "domain", Rule: "a.com", Enable: true},
			{ID: "r2", Type: "domain", Rule: "b.com", Enable: false},
		}},
		{ID: "g2", Name: "прочее", Enable: true},
	}
	clone := func() []magitrickle.Group {
		raw, _ := json.Marshal(base)
		var out []magitrickle.Group
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	cases := []struct {
		name string
		mut  func(cur []magitrickle.Group) []magitrickle.Group
		want int
	}{
		{"без правок", func(cur []magitrickle.Group) []magitrickle.Group { return cur }, 0},
		{"тумблер правила", func(cur []magitrickle.Group) []magitrickle.Group {
			cur[0].Rules[0].Enable = false
			return cur
		}, 1},
		{"имя правила", func(cur []magitrickle.Group) []magitrickle.Group {
			cur[0].Rules[0].Name = "метка"
			return cur
		}, 1},
		{"паттерн правила", func(cur []magitrickle.Group) []magitrickle.Group {
			cur[0].Rules[0].Rule = "c.com"
			return cur
		}, 1},
		{"имя группы", func(cur []magitrickle.Group) []magitrickle.Group {
			cur[1].Name = "другое"
			return cur
		}, 1},
		{"интерфейс группы", func(cur []magitrickle.Group) []magitrickle.Group {
			cur[0].Interface = "nwg1"
			return cur
		}, 1},
		{"удалённое правило", func(cur []magitrickle.Group) []magitrickle.Group {
			cur[0].Rules = cur[0].Rules[:1]
			return cur
		}, 1},
		{"добавленное правило", func(cur []magitrickle.Group) []magitrickle.Group {
			cur[0].Rules = append(cur[0].Rules, magitrickle.Rule{ID: "r3", Type: "domain", Rule: "c.com", Enable: true})
			return cur
		}, 1},
		{"удалённая группа", func(cur []magitrickle.Group) []magitrickle.Group { return cur[:1] }, 1},
		{"добавленная группа", func(cur []magitrickle.Group) []magitrickle.Group {
			return append(cur, magitrickle.Group{ID: "g3", Name: "новая", Enable: true})
		}, 1},
		{"порядок групп", func(cur []magitrickle.Group) []magitrickle.Group {
			cur[0], cur[1] = cur[1], cur[0]
			return cur
		}, 1},
	}
	for _, tc := range cases {
		d, _, _ := srv.draftDiffs(base, tc.mut(clone()))
		if d != tc.want {
			t.Fatalf("%s: dirty %d, want %d", tc.name, d, tc.want)
		}
	}
	// сумма правок
	cur := clone()
	cur[0].Rules[0].Enable = false
	cur[1].Enable = false
	cur = append(cur, magitrickle.Group{ID: "g3", Name: "новая", Enable: true})
	d2, _, _ := srv.draftDiffs(base, cur)
	if d2 != 3 {
		t.Fatalf("сумма правок: dirty %d, want 3", d2)
	}
}

func newDraftTestServer(t *testing.T) (*httptest.Server, *restoreMTState, *store.Store) {
	t.Helper()
	magitrickle.SetShadowStorage(nil, nil)
	mtSrv, mtState := restoreMTServer(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fb := fake.New()
	e := rotator.New(st, fb, magitrickle.New(mtSrv.URL))
	srv := New(st, e, fb, magitrickle.New(mtSrv.URL), "test", nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, mtState, st
}

func draftFixture(t *testing.T, mtState *restoreMTState) {
	t.Helper()
	mtState.mu.Lock()
	mtState.groups = []magitrickle.Group{
		{ID: "g1", Name: "гео", Enable: true, Interface: "awg0", Rules: []magitrickle.Rule{
			{ID: "r1", Type: "domain", Rule: "a.com", Enable: true},
		}},
		{ID: "svc", Name: "Служебная: каскад", Enable: true, Interface: "awg0"},
	}
	mtState.mu.Unlock()
}

// (б) мутация при автосохранении off: до MT не доходит, файл черновика
// появился, ответ несёт draft-статус, mtGetGroups отдаёт черновик
func TestMTDraftMutationHolds(t *testing.T) {
	ts, mtState, st := newDraftTestServer(t)
	draftFixture(t, mtState)
	if err := st.SetAutoSaveMT(false); err != nil {
		t.Fatal(err)
	}

	code, raw := httpDo(t, ts.URL, "POST", "/api/v1/mt/groups/g1/enable", `{"enable":false}`)
	if code != 200 {
		t.Fatalf("toggle: status %d, body %s", code, raw)
	}
	var out struct {
		Ok    string `json:"ok"`
		Draft struct {
			Active bool `json:"active"`
			Dirty  int  `json:"dirty"`
		} `json:"draft"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Ok != "ok" || !out.Draft.Active || out.Draft.Dirty < 1 {
		t.Fatalf("draft в ответе: %+v", out)
	}
	mtState.mu.Lock()
	writes, mtOn := mtState.writes, mtState.groups[0].Enable
	mtState.mu.Unlock()
	if writes != 0 {
		t.Fatalf("мутация дошла до MT (%d записей), want 0", writes)
	}
	if !mtOn {
		t.Fatal("MT изменился при выключенном автосохранении")
	}
	if _, err := os.Stat(filepath.Join(st.Base(), "mt-draft.json")); err != nil {
		t.Fatalf("файл черновика: %v", err)
	}

	// mtGetGroups отдаёт черновик: правка видна, автосохранение off
	code, raw = httpDo(t, ts.URL, "GET", "/api/v1/mt/groups", "")
	if code != 200 {
		t.Fatalf("get groups: status %d, body %s", code, raw)
	}
	var view struct {
		Groups []struct {
			ID     string `json:"id"`
			Enable bool   `json:"enable"`
		} `json:"groups"`
		AutoSave bool `json:"autoSave"`
		Draft    struct {
			Active bool `json:"active"`
		} `json:"draft"`
	}
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	if view.AutoSave || !view.Draft.Active || len(view.Groups) != 2 || view.Groups[0].ID != "g1" || view.Groups[0].Enable {
		t.Fatalf("mtGetGroups должен отдать черновик: autosave %v, draft %+v, groups %+v",
			view.AutoSave, view.Draft, view.Groups)
	}
}

// (в) apply-draft: массовый PUT со слитым списком (правка пользователя +
// служебная группа в версии рантайма + созданная в черновике без draft-ID),
// файл черновика удалён
func TestMTDraftApply(t *testing.T) {
	ts, mtState, st := newDraftTestServer(t)
	draftFixture(t, mtState)
	if err := st.SetAutoSaveMT(false); err != nil {
		t.Fatal(err)
	}

	// правка существующей + создание новой - обе в черновик
	if code, raw := httpDo(t, ts.URL, "PUT", "/api/v1/mt/groups/g1", `{"name":"гео-2"}`); code != 200 {
		t.Fatalf("update: status %d, body %s", code, raw)
	}
	code, raw := httpDo(t, ts.URL, "POST", "/api/v1/mt/groups", `{"name":"новая","interface":"awg0"}`)
	if code != 200 {
		t.Fatalf("create: status %d, body %s", code, raw)
	}

	// фон правит служебную группу поверх черновика
	mtState.mu.Lock()
	mtState.groups[1].Enable = false
	mtState.mu.Unlock()

	code, raw = httpDo(t, ts.URL, "POST", "/api/v1/mt/apply-draft", "")
	if code != 200 {
		t.Fatalf("apply: status %d, body %s", code, raw)
	}
	var applied struct {
		Ok      bool `json:"ok"`
		Applied int  `json:"applied"`
	}
	if err := json.Unmarshal(raw, &applied); err != nil {
		t.Fatal(err)
	}
	if !applied.Ok || applied.Applied != 3 {
		t.Fatalf("apply: %+v", applied)
	}
	if _, err := os.Stat(filepath.Join(st.Base(), "mt-draft.json")); !os.IsNotExist(err) {
		t.Fatalf("файл черновика не удалён: %v", err)
	}
	mtState.mu.Lock()
	defer mtState.mu.Unlock()
	if len(mtState.groups) != 3 {
		raw, _ := json.Marshal(mtState.groups)
		t.Fatalf("после apply %d групп: %s", len(mtState.groups), raw)
	}
	byID := map[string]magitrickle.Group{}
	var created magitrickle.Group
	for _, g := range mtState.groups {
		byID[g.ID] = g
		if g.Name == "новая" {
			created = g
		}
	}
	g1, ok := byID["g1"]
	if !ok || g1.Name != "гео-2" || !g1.Enable || len(g1.Rules) != 1 {
		t.Fatalf("правка пользователя не доехала: %+v", g1)
	}
	svc, ok := byID["svc"]
	// служебная - версия рантайма (фон выключил), не черновика
	if !ok || svc.Name != "Служебная: каскад" || svc.Enable {
		t.Fatalf("служебная затёрта версией черновика: %+v", svc)
	}
	// созданной в черновике группе MT назначил свой ID
	if created.ID == "" || created.ID == "draft-1" {
		t.Fatalf("draft-ID не выброшен: %+v", created)
	}
}

// (г) автосохранение: выкл -> правка в черновик -> 409 на включение ->
// сброс -> включение проходит; apply без черновика - 404
func TestMTDraftAutosaveGate(t *testing.T) {
	ts, mtState, st := newDraftTestServer(t)
	draftFixture(t, mtState)
	if !st.AutoSaveMT() {
		t.Fatal("AutoSaveMT по умолчанию должен быть включён (nil = true)")
	}

	code, _ := httpDo(t, ts.URL, "POST", "/api/v1/mt/apply-draft", "")
	if code != http.StatusNotFound {
		t.Fatalf("apply без черновика: status %d, want 404", code)
	}

	code, _ = httpDo(t, ts.URL, "POST", "/api/v1/mt/autosave", `{"enable":false}`)
	if code != 200 {
		t.Fatalf("autosave off: status %d", code)
	}
	code, _ = httpDo(t, ts.URL, "POST", "/api/v1/mt/groups/g1/enable", `{"enable":false}`)
	if code != 200 {
		t.Fatalf("toggle: status %d", code)
	}
	code, raw := httpDo(t, ts.URL, "POST", "/api/v1/mt/autosave", `{"enable":true}`)
	if code != http.StatusConflict {
		t.Fatalf("autosave on с черновиком: status %d, body %s", code, raw)
	}
	if !strings.Contains(string(raw), "сначала примените или сбросьте черновик") {
		t.Fatalf("текст 409 без подсказки: %s", raw)
	}

	code, _ = httpDo(t, ts.URL, "POST", "/api/v1/mt/discard-draft", "")
	if code != 200 {
		t.Fatalf("discard: status %d", code)
	}
	code, _ = httpDo(t, ts.URL, "POST", "/api/v1/mt/autosave", `{"enable":true}`)
	if code != 200 {
		t.Fatalf("autosave on после сброса: status %d", code)
	}
	if _, err := os.Stat(filepath.Join(st.Base(), "mt-draft.json")); !os.IsNotExist(err) {
		t.Fatalf("черновик не удалён сбросом: %v", err)
	}
	if !st.AutoSaveMT() {
		t.Fatal("автосохранение не включилось обратно")
	}
	mtState.mu.Lock()
	defer mtState.mu.Unlock()
	if !mtState.groups[0].Enable {
		t.Fatal("сброс черновика изменил MT")
	}
}

// правило в черновике: создание/правка/удаление не пишут в MT, dirty растёт
func TestMTDraftRules(t *testing.T) {
	ts, mtState, st := newDraftTestServer(t)
	draftFixture(t, mtState)
	if err := st.SetAutoSaveMT(false); err != nil {
		t.Fatal(err)
	}

	code, raw := httpDo(t, ts.URL, "POST", "/api/v1/mt/groups/g1/rules", `{"type":"domain","rule":"b.com","name":"б"}`)
	if code != 200 {
		t.Fatalf("create rule: status %d, body %s", code, raw)
	}
	var created magitrickle.Rule
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" {
		t.Fatalf("созданному правилу не выдан локальный ID: %+v", created)
	}
	code, raw = httpDo(t, ts.URL, "PUT", "/api/v1/mt/groups/g1/rules/r1", `{"type":"domain","rule":"z.com","name":"з"}`)
	if code != 200 {
		t.Fatalf("update rule: status %d, body %s", code, raw)
	}
	code, raw = httpDo(t, ts.URL, "DELETE", "/api/v1/mt/groups/g1/rules/"+created.ID, "")
	if code != 200 {
		t.Fatalf("delete rule: status %d, body %s", code, raw)
	}
	mtState.mu.Lock()
	writes := mtState.writes
	mtState.mu.Unlock()
	if writes != 0 {
		t.Fatalf("правила доехали до MT (%d записей), want 0", writes)
	}

	// применить: правка r1 единственная живая в черновике
	code, _ = httpDo(t, ts.URL, "POST", "/api/v1/mt/apply-draft", "")
	if code != 200 {
		t.Fatalf("apply: status %d", code)
	}
	mtState.mu.Lock()
	defer mtState.mu.Unlock()
	g1 := mtState.groups[0]
	if len(g1.Rules) != 1 || g1.Rules[0].ID != "r1" || g1.Rules[0].Rule != "z.com" {
		t.Fatalf("правка правила не применилась: %+v", g1.Rules)
	}
}

// откат правки (выключил - включил обратно): дифф пуст - черновик закрывается
// сам (слово владельца 08.11: «случайно задел и вернул» не требует Сбросить).
func TestMTDraftRollbackClosesDraft(t *testing.T) {
	ts, mtState, st := newDraftTestServer(t)
	draftFixture(t, mtState)
	if err := st.SetAutoSaveMT(false); err != nil {
		t.Fatal(err)
	}
	if code, raw := httpDo(t, ts.URL, "POST", "/api/v1/mt/groups/g1/enable", `{"enable":false}`); code != 200 {
		t.Fatalf("off: %d %s", code, raw)
	}
	if _, ok := st.MTDraftGet(); !ok {
		t.Fatal("файл черновика не создался на правке")
	}
	if code, raw := httpDo(t, ts.URL, "POST", "/api/v1/mt/groups/g1/enable", `{"enable":true}`); code != 200 {
		t.Fatalf("on: %d %s", code, raw)
	}
	if _, ok := st.MTDraftGet(); ok {
		t.Fatal("пустой дифф должен закрыть черновик (файл удалён)")
	}
	if !mtState.groups[0].Enable {
		t.Fatal("MT изменился при черновике")
	} // группа осталась в исходном включённом состоянии

}
