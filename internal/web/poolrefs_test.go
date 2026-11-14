package web

// Хвосты аудита мёртвых ссылок (118 §3-4, слово владельца 14.11):
// GET /pools/{name}/references - кто использует пул, предупреждение ДО
// удаления (аудит подсвечивает постфактум); DELETE пула пересчитывает
// зоны - осиротевшая dnszone-* группа пути сносится сама, а не руками.

import (
	"net/http/httptest"
	"strings"
	"testing"

	"mawg/internal/magitrickle"
	"mawg/internal/platform/fake"
	"mawg/internal/rotator"
	"mawg/internal/store"
)

func TestPoolReferences(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"main", "pfall", "free"} {
		if _, err := st.CreatePool(name, store.PoolSettings{Platform: store.PlatformOpenwrt}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.UpdatePool("pfall", store.PoolSettings{Platform: store.PlatformOpenwrt, Fallback: "pool:main"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateBundle(store.Bundle{Name: "tg", Members: []string{"main", "tun9"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetDnsZones([]store.DnsZone{{ID: "z1", Zone: "corp.example", Upstreams: []string{"10.0.0.1:53"}, Pool: "main", Enable: true}}); err != nil {
		t.Fatal(err)
	}
	fb := fake.New()
	e := rotator.New(st, fb, magitrickle.New("http://127.0.0.1:1"))
	srv := New(st, e, fb, magitrickle.New("http://127.0.0.1:1"), "test", nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	got := httpGetJSON(t, ts.URL+"/api/v1/pools/main/references")
	bundles := strings.Join(listStrings(got["bundles"]), ",")
	zones := strings.Join(listStrings(got["zones"]), ",")
	pools := strings.Join(listStrings(got["pools"]), ",")
	if bundles != "tg" || zones != "corp.example" || pools != "pfall" {
		t.Fatalf("references главного пула: bundles=%q zones=%q pools=%q", bundles, zones, pools)
	}
	empty := httpGetJSON(t, ts.URL+"/api/v1/pools/free/references")
	if len(listStrings(empty["bundles"]))+len(listStrings(empty["zones"]))+len(listStrings(empty["pools"])) != 0 {
		t.Fatalf("свободный пул не должен иметь ссылок: %v", empty)
	}
}

// удаление пула: зона с путём в него остаётся в таблице (вернётся с
// пулом, poolDead подсвечивает), но группа пути dnszone-* сносится
func TestPoolDeleteDropsZonePathGroups(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreatePool("zpool", store.PoolSettings{Platform: store.PlatformOpenwrt}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetDnsZones([]store.DnsZone{{ID: "z1", Zone: "corp.example", Upstreams: []string{"10.0.0.1:53"}, Pool: "zpool", Enable: true}}); err != nil {
		t.Fatal(err)
	}
	f := &fakeMT{version: "0.8.2-rev22"}
	mt := httptest.NewServer(f.handler())
	t.Cleanup(mt.Close)
	fb := fake.New()
	e := rotator.New(st, fb, magitrickle.New(mt.URL))
	srv := New(st, e, fb, magitrickle.New(mt.URL), "test", nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// применяем зону: группа пути dnszone-corp.example-xxxx создаётся в MT
	resp, err := httpPut(ts.URL+"/api/v1/dns/zones", `{"zones":[{"zone":"corp.example","upstreams":["10.0.0.1:53"],"pool":"zpool","enable":true}]}`)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if len(f.groups) != 1 || !strings.HasPrefix(f.groups[0].Name, "dnszone-corp-example-") {
		t.Fatalf("группа пути не создана: %v", f.groups)
	}

	if err := httpDeleteJSON(ts.URL + "/api/v1/pools/zpool"); err != nil {
		t.Fatal(err)
	}
	if len(f.groups) != 0 {
		t.Fatalf("группа пути должна снестись при удалении пула: %v", f.groups)
	}
	if len(f.groupsDelete) != 1 {
		t.Fatalf("ожидали один DELETE группы: %v", f.groupsDelete)
	}
	// зона осталась в таблице с мёртвым пулом (poolDead подсвечивает)
	zones := httpGetJSON(t, ts.URL+"/api/v1/dns/zones")
	zlist, _ := zones["zones"].([]any)
	if len(zlist) != 1 {
		t.Fatalf("зона должна остаться в таблице: %v", zones)
	}
	if zlist[0].(map[string]any)["poolDead"] != true {
		t.Fatalf("мёртвый путь зоны не помечен: %v", zlist[0])
	}
}

func listStrings(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, x := range list {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}

func httpDeleteJSON(url string) error {
	resp, err := httpDelete(url)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}
