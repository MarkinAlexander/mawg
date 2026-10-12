package web

// импорт групп файлом: POST /api/v1/mt/groups/import - добавить выбранные
// к существующим или заменить весь список. Формат файла - общий с нативным
// mtrickle и нашей тенью; ID из файла всегда выбрасываются (новые назначает
// MT), дубли имени в режиме add пропускаются (второй ipset-to-link с тем же
// именем ронял iptables-restore батча - корень 500-х 2026-10-11)

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"mawg/internal/magitrickle"
	"mawg/internal/platform/fake"
	"mawg/internal/rotator"
	"mawg/internal/store"
)

func newImportTestServer(t *testing.T) (*httptest.Server, *restoreMTState) {
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
	return ts, mtState
}

// fixtureGroups читает репозиторную фикстуру нативного формата и заодно
// проверяет, что она парсится в наш Group (формат совместим по определению)
func fixtureGroups(t *testing.T) []magitrickle.Group {
	t.Helper()
	raw, err := os.ReadFile("testdata/import-groups.mtrickle")
	if err != nil {
		t.Fatal(err)
	}
	var dump struct {
		Groups []magitrickle.Group `json:"groups"`
	}
	if err := json.Unmarshal(raw, &dump); err != nil {
		t.Fatalf("фикстура не парсится: %v", err)
	}
	return dump.Groups
}

func postImport(t *testing.T, ts *httptest.Server, body string) (int, []byte) {
	t.Helper()
	resp, err := http.Post(ts.URL+"/api/v1/mt/groups/import", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, raw
}

func importBody(t *testing.T, mode string, groups []magitrickle.Group) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"mode": mode, "groups": groups})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestMTImportGroupsAdd(t *testing.T) {
	ts, mtState := newImportTestServer(t)
	// в рантайме уже есть группа с именем из файла - дубль имени
	mtState.mu.Lock()
	mtState.groups = []magitrickle.Group{{
		ID: "old1", Name: "зарубежное", Interface: "awg0", Enable: true,
		Rules: []magitrickle.Rule{{Type: "domain", Rule: "example.com", Enable: true}},
	}}
	mtState.mu.Unlock()

	code, raw := postImport(t, ts, importBody(t, "add", fixtureGroups(t)))
	if code != 200 {
		t.Fatalf("import add: status %d, body %s", code, raw)
	}
	var out struct {
		OK      bool     `json:"ok"`
		Added   int      `json:"added"`
		Rules   int      `json:"rules"`
		Skipped []string `json:"skipped"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	// файл: зарубежное (дубль, 1 правило), видео (3 правила, 1 дубликат
	// внутри), мессенджеры (1 правило) - считаем правила добавленных
	if !out.OK || out.Added != 2 || out.Rules != 4 || len(out.Skipped) != 1 || out.Skipped[0] != "зарубежное" {
		t.Fatalf("отчёт импорта: %+v", out)
	}

	mtState.mu.Lock()
	defer mtState.mu.Unlock()
	if len(mtState.groups) != 3 {
		raw, _ := json.Marshal(mtState.groups)
		t.Fatalf("в рантайме %d групп, want 3: %s", len(mtState.groups), raw)
	}
	byName := map[string]magitrickle.Group{}
	for _, g := range mtState.groups {
		byName[g.Name] = g
	}
	// дубль не добавился, старый цел
	old, ok := byName["зарубежное"]
	if !ok || old.ID != "old1" || len(old.Rules) != 1 {
		t.Fatalf("существующая группа задета: %+v", old)
	}
	// импортированные получили НОВЫЕ ID (из файла выброшены)
	vid, ok := byName["видео"]
	if !ok || vid.ID == "" || vid.ID == "file-2" || len(vid.Rules) != 2 {
		raw, _ := json.Marshal(vid)
		t.Fatalf("видео: %s (дедуп дубля правила внутри группы не сработал или ID чужой)", raw)
	}
	mess, ok := byName["мессенджеры"]
	if !ok || mess.ID == "" || mess.ID == "file-3" || len(mess.Rules) != 1 {
		t.Fatalf("мессенджеры: %+v", mess)
	}
	if mess.Color != "#4a9eff" {
		t.Fatalf("пустой цвет не заменён дефолтным: %q", mess.Color)
	}
	if mess.Enable {
		t.Fatal("enable из файла потерялся")
	}
}

func TestMTImportGroupsReplace(t *testing.T) {
	ts, mtState := newImportTestServer(t)
	mtState.mu.Lock()
	mtState.groups = []magitrickle.Group{
		{ID: "g1", Name: "один"}, {ID: "g2", Name: "два"}, {ID: "g3", Name: "три"},
	}
	mtState.mu.Unlock()

	code, raw := postImport(t, ts, importBody(t, "replace", fixtureGroups(t)))
	if code != 200 {
		t.Fatalf("import replace: status %d, body %s", code, raw)
	}
	var out struct {
		OK    bool `json:"ok"`
		Added int  `json:"added"`
		Rules int  `json:"rules"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	// фикстура: 1 + 3 + 1 = 5 правил из файла, дедуп внутри видео убрал
	// дубликат youtube.com - записалось 4
	if !out.OK || out.Added != 3 || out.Rules != 4 {
		t.Fatalf("отчёт импорта: %+v", out)
	}

	mtState.mu.Lock()
	defer mtState.mu.Unlock()
	if len(mtState.groups) != 3 {
		raw, _ := json.Marshal(mtState.groups)
		t.Fatalf("в рантайме %d групп, want 3: %s", len(mtState.groups), raw)
	}
	for _, g := range mtState.groups {
		if g.ID == "g1" || g.ID == "g2" || g.ID == "g3" || strings.HasPrefix(g.ID, "file-") || g.ID == "" {
			raw, _ := json.Marshal(mtState.groups)
			t.Fatalf("старые/файловые ID не выброшены: %s", raw)
		}
	}
}

func TestMTImportGroupsValidation(t *testing.T) {
	ts, mtState := newImportTestServer(t)
	cases := []string{
		`{"mode":"upsert","groups":[{"name":"x","interface":"awg0"}]}`, // плохой mode
		`{"mode":"add","groups":[]}`,
		`{"mode":"add"}`,                                 // групп нет вовсе
		`{"mode":"add","groups":[{"name":"x"}]}`,         // без интерфейса
		`{"mode":"add","groups":[{"interface":"awg0"}]}`, // без имени
		`{"mode":"replace","groups":[{"name":"","interface":"awg0"}]}`,
	}
	for _, body := range cases {
		code, raw := postImport(t, ts, body)
		if code != 400 {
			t.Fatalf("тело %s прошло с кодом %d: %s", body, code, raw)
		}
	}
	// рантайм не тронут
	mtState.mu.Lock()
	defer mtState.mu.Unlock()
	if len(mtState.groups) != 1 || mtState.groups[0].ID != "old1" {
		raw, _ := json.Marshal(mtState.groups)
		t.Fatalf("рантайм изменился при отказе: %s", raw)
	}
}

// поле sources группы: сквозной прогон через создание и правку
func TestMTGroupSources(t *testing.T) {
	ts, _ := newImportTestServer(t)

	// создание с источниками
	code, raw := httpDo(t, ts.URL, "POST", "/api/v1/mt/groups",
		`{"name":"srct","interface":"awg0","sources":["192.168.1.50","10.0.0.0/24"]}`)
	if code != 200 {
		t.Fatalf("create: status %d, body %s", code, raw)
	}
	var created magitrickle.Group
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	if len(created.Sources) != 2 || created.Sources[0] != "192.168.1.50" {
		t.Fatalf("created sources: %v", created.Sources)
	}

	// правка источников
	code, raw = httpDo(t, ts.URL, "PUT", "/api/v1/mt/groups/"+created.ID, `{"sources":["10.9.9.9"]}`)
	if code != 200 {
		t.Fatalf("update: status %d, body %s", code, raw)
	}
	var got []string
	for _, g := range getGroups(t, ts.URL) {
		if g.ID == created.ID {
			got = g.Sources
		}
	}
	if len(got) != 1 || got[0] != "10.9.9.9" {
		t.Fatalf("sources after update: %v", got)
	}

	// кривой источник -> 400, список не меняется
	code, _ = httpDo(t, ts.URL, "PUT", "/api/v1/mt/groups/"+created.ID, `{"sources":["junk"]}`)
	if code != 400 {
		t.Fatalf("bad source: status %d, want 400", code)
	}

	// без поля sources список не трогается
	code, _ = httpDo(t, ts.URL, "PUT", "/api/v1/mt/groups/"+created.ID, `{"name":"srct2"}`)
	if code != 200 {
		t.Fatalf("rename: status %d", code)
	}
	for _, g := range getGroups(t, ts.URL) {
		if g.ID == created.ID && (len(g.Sources) != 1 || g.Sources[0] != "10.9.9.9") {
			t.Fatalf("sources clobbered by unrelated PUT: %v", g.Sources)
		}
	}

	// пустой массив чистит список
	code, _ = httpDo(t, ts.URL, "PUT", "/api/v1/mt/groups/"+created.ID, `{"sources":[]}`)
	if code != 200 {
		t.Fatalf("clear: status %d", code)
	}
	for _, g := range getGroups(t, ts.URL) {
		if g.ID == created.ID && len(g.Sources) != 0 {
			t.Fatalf("sources not cleared: %v", g.Sources)
		}
	}
}

// getGroups - свежий список групп из панели (каждый раз с нулевым
// декодером: json.Unmarshal не сбрасывает отсутствующие в JSON поля)
func getGroups(t *testing.T, base string) []magitrickle.Group {
	t.Helper()
	code, raw := httpDo(t, base, "GET", "/api/v1/mt/groups", "")
	if code != 200 {
		t.Fatalf("get groups: status %d, body %s", code, raw)
	}
	var list struct {
		Groups []magitrickle.Group `json:"groups"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	return list.Groups
}

func httpDo(t *testing.T, base, method, path, body string) (int, []byte) {
	t.Helper()
	var req *http.Request
	var err error
	if body != "" {
		req, err = http.NewRequest(method, base+path, strings.NewReader(body))
	} else {
		req, err = http.NewRequest(method, base+path, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, raw
}
