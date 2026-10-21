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

// zonesServer - тестовый стенд с пулом proton (DeviceName совпадает с
// именем на openwrt-фейке); стирание тени групп - как в policyServer.
func zonesServer(t *testing.T, version string) (*httptest.Server, *fakeMT, *store.Store) {
	t.Helper()
	magitrickle.SetShadowStorage(nil, nil)
	fm := &fakeMT{version: version}
	mt := httptest.NewServer(fm.handler())
	t.Cleanup(mt.Close)
	fb := fake.New()
	fb.ShellFn = dnsShellStub("dhcp.@dnsmasq[0].server='127.0.0.1#3553'\n")
	st, _ := store.Open(t.TempDir())
	if _, err := st.CreatePool("proton", store.PoolSettings{Platform: store.PlatformOpenwrt, ProbeHost: "1.1.1.1"}); err != nil {
		t.Fatal(err)
	}
	engine := rotator.New(st, fb, nil)
	srv := httptest.NewServer(New(st, engine, fb, magitrickle.New(mt.URL), "test", nil).Handler())
	t.Cleanup(srv.Close)
	return srv, fm, st
}

func zonesPut(t *testing.T, url, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, out
}

// не-capable MT (rev12): GET честно capable=false, PUT таблицы - 501 и
// форк не тронут
func TestDnsZonesNotCapable(t *testing.T) {
	srv, fm, _ := zonesServer(t, "0.8.2-rev12-mawg-dev")

	body := getJSON(t, srv.URL+"/api/v1/dns/zones", http.StatusOK)
	if body["capable"] != false {
		t.Fatalf("rev12 должен быть not capable: %+v", body)
	}
	code, _ := zonesPut(t, srv.URL+"/api/v1/dns/zones",
		`{"zones":[{"id":"z1","zone":"corp.example.com","upstreams":["10.0.0.1:53"],"enable":true}]}`)
	if code != http.StatusNotImplemented {
		t.Fatalf("PUT на rev12: status=%d", code)
	}
	if len(fm.zonePutsList()) != 0 {
		t.Fatalf("таблица уехала в старый форк: %v", fm.zonePutsList())
	}
}

// валидация: кривое имя зоны, пустые серверы, кривой адрес, несуществующий
// пул - 400, форк не тронут; дубль зоны схлопывается
func TestDnsZonesValidate(t *testing.T) {
	srv, fm, _ := zonesServer(t, "0.8.2-rev13-mawg-dev")

	for i, body := range []string{
		`{"zones":[{"zone":"","upstreams":["10.0.0.1"],"enable":true}]}`,
		`{"zones":[{"zone":"a..b","upstreams":["10.0.0.1"],"enable":true}]}`,
		`{"zones":[{"zone":"corp.example.com","upstreams":[],"enable":true}]}`,
		`{"zones":[{"zone":"corp.example.com","upstreams":["10.0.0.1:0"],"enable":true}]}`,
		`{"zones":[{"zone":"corp.example.com","upstreams":["10.0.0.1"],"pool":"нет-такого","enable":true}]}`,
	} {
		if code, _ := zonesPut(t, srv.URL+"/api/v1/dns/zones", body); code != http.StatusBadRequest {
			t.Fatalf("кривая таблица[%d] должна быть 400: %d", i, code)
		}
	}
	if len(fm.zonePutsList()) != 0 {
		t.Fatalf("кривые таблицы доехали до форка: %v", fm.zonePutsList())
	}

	code, out := zonesPut(t, srv.URL+"/api/v1/dns/zones", `{"zones":[
		{"zone":"Corp.Example.COM.","upstreams":["10.0.0.1"],"pool":"proton","enable":true},
		{"zone":"corp.example.com","upstreams":["10.0.0.9"],"pool":"proton","enable":true}]}`)
	if code != http.StatusOK {
		t.Fatalf("дубли должны схлопываться: %d %+v", code, out)
	}
	zl := out["zones"].([]any)
	if len(zl) != 1 || zl[0].(map[string]any)["zone"] != "corp.example.com" {
		t.Fatalf("канонизация/дедуп зон: %+v", out["zones"])
	}
}

// применение: PUT таблицы -> группа пути dnszone-* на пуле с subnet-
// статикой адресов серверов + таблица зон в форке; повторный PUT без
// изменений - группа не пересоздаётся; зона без пула - без группы;
// очистка таблицы - осиротевшая группа удаляется, в форк уходит []
func TestDnsZonesApply(t *testing.T) {
	srv, fm, _ := zonesServer(t, "0.8.2-rev13-mawg-dev")

	code, out := zonesPut(t, srv.URL+"/api/v1/dns/zones", `{"zones":[
		{"zone":"corp.example.com","upstreams":["10.0.0.1","10.0.0.2:53"],"pool":"proton","enable":true},
		{"zone":"lab.corp.example.com","upstreams":["tls://10.1.1.1"],"enable":true},
		{"zone":"off.example.com","upstreams":["10.2.2.2"],"pool":"proton","enable":false}]}`)
	if code != http.StatusOK {
		t.Fatalf("PUT: %d %+v", code, out)
	}

	// форк: таблица из ДВУХ включённых зон, выключенная не поехала
	puts := fm.zonePutsList()
	if len(puts) != 1 {
		t.Fatalf("ожидается один PUT таблицы: %v", puts)
	}
	if !strings.Contains(puts[0], `"zone":"corp.example.com"`) || !strings.Contains(puts[0], `"zone":"lab.corp.example.com"`) {
		t.Fatalf("зоны потерялись: %s", puts[0])
	}
	if strings.Contains(puts[0], "off.example.com") {
		t.Fatalf("выключенная зона уехала в форк: %s", puts[0])
	}
	if !strings.Contains(puts[0], `"fallback":false`) {
		t.Fatalf("fallback-флаг потерялся: %s", puts[0])
	}

	// группы: одна для corp (пул proton), у lab пула нет - группы нет
	fm.mu.Lock()
	var zoneGroups []magitrickle.Group
	for _, g := range fm.groups {
		if strings.HasPrefix(g.Name, "dnszone-") {
			zoneGroups = append(zoneGroups, g)
		}
	}
	fm.mu.Unlock()
	if len(zoneGroups) != 1 {
		t.Fatalf("ожидается одна группа пути: %+v", zoneGroups)
	}
	g := zoneGroups[0]
	if g.Interface != "proton" || !strings.HasPrefix(g.Name, "dnszone-corp-example-com-") {
		t.Fatalf("группа пути: %+v", g)
	}
	rules := map[string]bool{}
	for _, r := range g.Rules {
		rules[r.Type+"|"+r.Rule] = true
	}
	if !rules["subnet|10.0.0.1/32"] || !rules["subnet|10.0.0.2/32"] {
		t.Fatalf("статика адресов серверов: %+v", g.Rules)
	}

	// повторный PUT той же таблицы: группа не пересоздаётся (no-op),
	// таблица форка переписывается идемпотентно
	fm.mu.Lock()
	groupPutsBefore := len(fm.groupPuts)
	fm.mu.Unlock()
	code, _ = zonesPut(t, srv.URL+"/api/v1/dns/zones", `{"zones":[
		{"zone":"corp.example.com","upstreams":["10.0.0.1","10.0.0.2:53"],"pool":"proton","enable":true},
		{"zone":"lab.corp.example.com","upstreams":["tls://10.1.1.1"],"enable":true}]}`)
	if code != http.StatusOK {
		t.Fatalf("повторный PUT: %d %+v", code, out)
	}
	if len(fm.groupPuts) != groupPutsBefore {
		t.Fatalf("идемпотентность сломана: %v", fm.groupPuts)
	}

	// очистка: группы пути удаляются, в форк уходит пустая таблица
	code, _ = zonesPut(t, srv.URL+"/api/v1/dns/zones", `{"zones":[]}`)
	if code != http.StatusOK {
		t.Fatalf("очистка: %d", code)
	}
	fm.mu.Lock()
	del := append([]string(nil), fm.groupsDelete...)
	last := fm.zonePuts[len(fm.zonePuts)-1]
	fm.mu.Unlock()
	if len(del) != 1 || del[0] != g.ID {
		t.Fatalf("осиротевшая группа не удалена: %v", del)
	}
	if !strings.Contains(last, `"zones":[]`) {
		t.Fatalf("очистка таблицы форка: %s", last)
	}
}

// петлезащита: сервер зоны - адрес dnsmasq при inMT -> 409 c loopRisk,
// повтор с confirm проходит
func TestDnsZonesLoopGuard(t *testing.T) {
	srv, fm, _ := zonesServer(t, "0.8.2-rev13-mawg-dev")

	code, out := zonesPut(t, srv.URL+"/api/v1/dns/zones",
		`{"zones":[{"zone":"corp.example.com","upstreams":["127.0.0.1"],"enable":true}]}`)
	if code != http.StatusConflict {
		t.Fatalf("петля должна давать 409: %d %+v", code, out)
	}
	if out["loopRisk"] == nil {
		t.Fatalf("loopRisk в ответе: %+v", out)
	}
	if len(fm.zonePutsList()) != 0 {
		t.Fatalf("рискованная таблица уехала в форк")
	}
	code, out = zonesPut(t, srv.URL+"/api/v1/dns/zones",
		`{"confirm":true,"zones":[{"zone":"corp.example.com","upstreams":["127.0.0.1"],"enable":true}]}`)
	if code != http.StatusOK {
		t.Fatalf("confirm-повтор: %d %+v", code, out)
	}
	if len(fm.zonePutsList()) != 1 {
		t.Fatalf("confirm-таблица не уехала в форк: %v", fm.zonePutsList())
	}
}

// probe по id: канарейка по каждому серверу зоны; неизвестный id - 404
func TestDnsZonesProbe(t *testing.T) {
	srv, _, st := zonesServer(t, "0.8.2-rev13-mawg-dev")
	if err := st.SetDnsZones([]store.DnsZone{{ID: "z1", Zone: "corp.example.com", Upstreams: []string{"127.0.0.1:53"}, Enable: true}}); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Post(srv.URL+"/api/v1/dns/zones/z1/probe", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Results []struct {
			Level   string `json:"level"`
			Verdict string `json:"verdict"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || len(body.Results) != 1 {
		t.Fatalf("probe: status=%d %+v", resp.StatusCode, body)
	}
	if !strings.Contains(body.Results[0].Level, "corp.example.com") {
		t.Fatalf("подпись уровня без зоны: %+v", body.Results[0])
	}

	resp2, err := http.Post(srv.URL+"/api/v1/dns/zones/none/probe", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("чужой id: %d", resp2.StatusCode)
	}
}

// путь зоны через готовый интерфейс (iface:<dev>, фидбэк пользователя
// зон: WG настроен руками мимо пулов): валидация без пула, группа пути
// на устройстве как есть; прямой мусор в имени - 400
func TestDnsZonesIfacePath(t *testing.T) {
	srv, fm, _ := zonesServer(t, "0.8.2-rev13-mawg-dev")

	code, out := zonesPut(t, srv.URL+"/api/v1/dns/zones", `{"zones":[
		{"zone":"corp.example.com","upstreams":["10.0.0.1"],"pool":"iface:wg9","enable":true}]}`)
	if code != http.StatusOK {
		t.Fatalf("iface-путь отвергнут: %d %+v", code, out)
	}
	fm.mu.Lock()
	var g *magitrickle.Group
	for i := range fm.groups {
		if strings.HasPrefix(fm.groups[i].Name, "dnszone-") {
			g = &fm.groups[i]
		}
	}
	fm.mu.Unlock()
	if g == nil || g.Interface != "wg9" {
		t.Fatalf("группа пути не на iface-устройстве: %+v", g)
	}
	if len(fm.zonePutsList()) != 1 || !strings.Contains(fm.zonePutsList()[0], `"zone":"corp.example.com"`) {
		t.Fatalf("таблица зон не уехала: %v", fm.zonePutsList())
	}

	code, _ = zonesPut(t, srv.URL+"/api/v1/dns/zones", `{"zones":[{"zone":"x.example.com","upstreams":["10.0.0.1"],"pool":"iface:","enable":true}]}`)
	if code != http.StatusBadRequest {
		t.Fatalf("пустое имя устройства должно быть 400: %d", code)
	}
}

// normalizeDnsZoneName/dnsZoneGroupName: канонизация и стабильные имена
// групп без коллизий слага
func TestDnsZoneNames(t *testing.T) {
	n, err := normalizeDnsZoneName("  Corp.Example.COM. ")
	if err != nil || n != "corp.example.com" {
		t.Fatalf("normalizeDnsZoneName: %q %v", n, err)
	}
	for _, bad := range []string{"", "a..b", "corp/example.com", "tls://corp.example.com"} {
		if _, err := normalizeDnsZoneName(bad); err == nil {
			t.Fatalf("кривое имя %q должно ошибаться", bad)
		}
	}
	a := dnsZoneGroupName("a.b-c.example.com")
	b := dnsZoneGroupName("a-b.c.example.com")
	if a == b {
		t.Fatalf("коллизия имён групп: %q", a)
	}
	if dnsZoneGroupName("corp.example.com") != dnsZoneGroupName("corp.example.com") {
		t.Fatal("имя группы нестабильно")
	}
}
