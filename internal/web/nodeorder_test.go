package web

// Ручной порядок узлов движкового пула (13.11): переживает обновление
// подписок - знакомые узлы на местах, новые в конец, исчезнувшие
// выпадают из сохранённого списка; эндпоинт принимает порядок и
// пересобирает движок.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mawg/internal/links"
	"mawg/internal/magitrickle"
	"mawg/internal/platform/fake"
	"mawg/internal/rotator"
	"mawg/internal/store"
)

func confNames(nodes []links.Node) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.ConfName())
	}
	return out
}

func eqSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestApplyNodeOrderStable(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fb := fake.New()
	e := rotator.New(st, fb, magitrickle.New("http://127.0.0.1:1"))
	srv := New(st, e, fb, nil, "test", nil)

	nodes := func() []links.Node {
		return []links.Node{
			{Tag: "sub|a", Host: "10.0.0.1"},
			{Tag: "sub|b", Host: "10.0.0.2"},
			{Tag: "sub|c", Host: "10.0.0.3"},
		}
	}
	if err := srv.writePoolNodes("p", nodes()); err != nil {
		t.Fatal(err)
	}
	p, err := st.CreatePool("p", store.PoolSettings{Platform: store.PlatformOpenwrt, EngineMode: "singbox"})
	if err != nil {
		t.Fatal(err)
	}

	// без NodeOrder - порядок подписки
	if got := confNames(srv.applyNodeOrder(p, nodes())); !eqSlice(got, []string{"a", "b", "c"}) {
		t.Fatalf("без порядка: %v", got)
	}

	// ручной порядок [c, a]: b уезжает в конец, порядок сохраняется в стор
	if err := st.SetPoolNodeOrder("p", []string{"c", "a"}); err != nil {
		t.Fatal(err)
	}
	p, _ = st.Pool("p") // engineSpecs читает свежий пул - как и здесь
	got := confNames(srv.applyNodeOrder(p, nodes()))
	if !eqSlice(got, []string{"c", "a", "b"}) {
		t.Fatalf("после нормализации: %v", got)
	}
	np, _ := st.Pool("p")
	if !eqSlice(np.Settings.NodeOrder, []string{"c", "a", "b"}) {
		t.Fatalf("NodeOrder в сторе не нормализован: %v", np.Settings.NodeOrder)
	}

	// новый узел d из обновления подписки - в конец, знакомые на местах
	nodes2 := append(nodes(), links.Node{Tag: "sub|d", Host: "10.0.0.4"})
	got = confNames(srv.applyNodeOrder(p, nodes2))
	if !eqSlice(got, []string{"c", "a", "b", "d"}) {
		t.Fatalf("новый узел должен попасть в конец: %v", got)
	}

	// исчезнувший b выпадает из сохранённого порядка
	nodes3 := []links.Node{{Tag: "sub|a", Host: "10.0.0.1"}, {Tag: "sub|c", Host: "10.0.0.3"}, {Tag: "sub|d", Host: "10.0.0.4"}}
	srv.applyNodeOrder(p, nodes3)
	np, _ = st.Pool("p")
	if !eqSlice(np.Settings.NodeOrder, []string{"c", "a", "d"}) {
		t.Fatalf("исчезнувший узел должен выпасть из порядка: %v", np.Settings.NodeOrder)
	}
}

func TestPoolNodeOrderEndpoint(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fb := fake.New()
	e := rotator.New(st, fb, magitrickle.New("http://127.0.0.1:1"))
	srv := New(st, e, fb, nil, "test", nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	if _, err := st.CreatePool("eng", store.PoolSettings{Platform: store.PlatformOpenwrt, EngineMode: "singbox"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreatePool("native", store.PoolSettings{Platform: store.PlatformOpenwrt}); err != nil {
		t.Fatal(err)
	}

	code, out := putJSON(t, ts.URL+"/api/v1/pools/eng/node-order", `{"order":["n2","n1"]}`)
	if code != http.StatusOK || out["ok"] != true {
		t.Fatalf("PUT node-order: status=%d out=%+v", code, out)
	}
	p, _ := st.Pool("eng")
	if !eqSlice(p.Settings.NodeOrder, []string{"n2", "n1"}) {
		t.Fatalf("порядок не сохранён: %v", p.Settings.NodeOrder)
	}

	// движковый пул обязателен
	code, _ = putJSON(t, ts.URL+"/api/v1/pools/native/node-order", `{"order":["n2"]}`)
	if code != http.StatusBadRequest {
		t.Fatalf("нативный пул должен отвергаться: %d", code)
	}
	// пустой order - сброс (порядок подписки)
	code, _ = putJSON(t, ts.URL+"/api/v1/pools/eng/node-order", `{"order":[]}`)
	if code != http.StatusOK {
		t.Fatalf("сброс порядка: %d", code)
	}
	p, _ = st.Pool("eng")
	if len(p.Settings.NodeOrder) != 0 {
		t.Fatalf("сброс не сработал: %v", p.Settings.NodeOrder)
	}
	if !strings.HasPrefix(ts.URL, "http") {
		t.Fatal("не бывает")
	}
}
