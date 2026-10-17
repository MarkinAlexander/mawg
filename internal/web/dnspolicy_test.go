package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mawg/internal/magitrickle"
)

// normalizeUpstream: bare host -> host:53, ipv6, явный порт; мусор - ошибка
func TestNormalizeUpstream(t *testing.T) {
	ok := map[string]string{
		"8.8.8.8":       "8.8.8.8:53",
		" 8.8.8.8:5353": "8.8.8.8:5353",
		"[::1]:5353":    "[::1]:5353",
		"::1":           "[::1]:53",
	}
	for raw, want := range ok {
		got, err := normalizeUpstream(raw)
		if err != nil || got != want {
			t.Fatalf("normalizeUpstream(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, bad := range []string{"", "  ", "8.8.8.8:0", "8.8.8.8:99999", "8.8.8.8:dns", ":53"} {
		if _, err := normalizeUpstream(bad); err == nil {
			t.Fatalf("normalizeUpstream(%q) должен ошибаться", bad)
		}
	}
}

func policyServer(t *testing.T) (*httptest.Server, *fakeMT) {
	t.Helper()
	// пакетная тень групп - глобальное состояние клиента MT: без сброса
	// группы прошлого теста «воскресают» в repairFromShadow (SetShadowStorage
	// с nil - штатный тестовый сброс, 44-док)
	magitrickle.SetShadowStorage(nil, nil)
	fm := &fakeMT{version: "0.8.2-rev8-mawgfix1"}
	mt := httptest.NewServer(fm.handler())
	t.Cleanup(mt.Close)
	return dnsTestServerMT(t, mt.URL), fm
}

func getJSON(t *testing.T, url string, status int) map[string]any {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != status {
		t.Fatalf("GET %s: status=%d", url, resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

func putJSON(t *testing.T, url string, body string) (int, map[string]any) {
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

// GET пустой политики -> capable + нулевая; PUT включённой -> группа
// dns-tunnel создана, порядок скомпонован и уехал в форк; повторный GET
// отдаёт сохранённое; выключенная политику MT не трогает
func TestDnsPolicyApply(t *testing.T) {
	srv, fm := policyServer(t)

	body := getJSON(t, srv.URL+"/api/v1/dns/policy", http.StatusOK)
	if body["capable"] != true {
		t.Fatalf("capable: %+v", body)
	}
	if p, ok := body["policy"].(map[string]any); !ok || p["enabled"] != false {
		t.Fatalf("пустая политика: %+v", body["policy"])
	}

	code, out := putJSON(t, srv.URL+"/api/v1/dns/policy",
		`{"enabled":true,"primary":["8.8.8.8"],"tunnel":"proton","fallback":["1.1.1.1:5353"],"yandex":true,"provider":false}`)
	if code != http.StatusOK {
		t.Fatalf("PUT: status=%d out=%+v", code, out)
	}
	if out["composed"] == nil {
		t.Fatalf("composed в ответе: %+v", out)
	}

	// порядок: основные -> запасные -> Яндекс; группа создана на proton
	if len(fm.lastPuts) != 1 {
		t.Fatalf("ожидается один PUT upstream: %v", fm.lastPuts)
	}
	if !strings.Contains(fm.lastPuts[0], `"8.8.8.8:53"`) ||
		!strings.Contains(fm.lastPuts[0], `"1.1.1.1:5353"`) ||
		!strings.Contains(fm.lastPuts[0], `"77.88.8.8:53"`) {
		t.Fatalf("компоновка неверна: %s", fm.lastPuts[0])
	}
	if len(fm.groupPuts) != 1 || !strings.Contains(fm.groupPuts[0], "bulk:dns-tunnel@proton") {
		t.Fatalf("группа dns-tunnel не создана: %v", fm.groupPuts)
	}

	// сохранённое читается обратно, tunnelGroup виден
	body = getJSON(t, srv.URL+"/api/v1/dns/policy", http.StatusOK)
	p := body["policy"].(map[string]any)
	if p["enabled"] != true || p["tunnel"] != "proton" {
		t.Fatalf("политика не сохранилась: %+v", p)
	}
	tg, ok := body["tunnelGroup"].(map[string]any)
	if !ok || tg["interface"] != "proton" {
		t.Fatalf("tunnelGroup: %+v", body["tunnelGroup"])
	}

	// выключенная: сохраняется, MT не трогается
	putsBefore := len(fm.lastPuts)
	code, out = putJSON(t, srv.URL+"/api/v1/dns/policy", `{"enabled":false,"primary":["8.8.8.8"]}`)
	if code != http.StatusOK {
		t.Fatalf("PUT off: %d", code)
	}
	if len(fm.lastPuts) != putsBefore {
		t.Fatalf("выключенная политика трогает форк: %v", fm.lastPuts)
	}
	body = getJSON(t, srv.URL+"/api/v1/dns/policy", http.StatusOK)
	if body["policy"].(map[string]any)["enabled"] != false {
		t.Fatalf("выключенная не сохранилась: %+v", body["policy"])
	}

	// кривой порт -> 400, MT не тронут
	code, _ = putJSON(t, srv.URL+"/api/v1/dns/policy", `{"enabled":true,"primary":["8.8.8.8:0"]}`)
	if code != http.StatusBadRequest {
		t.Fatalf("кривой порт: %d", code)
	}
	if len(fm.lastPuts) != putsBefore {
		t.Fatalf("кривая политика доехала до форка")
	}
}

// существующая группа dns-tunnel на другом пуле - обновление single-PUT-ом,
// на том же - no-op
func TestDnsPolicyTunnelGroupReuse(t *testing.T) {
	srv, fm := policyServer(t)
	fm.mu.Lock()
	fm.groups = []magitrickle.Group{{ID: "g1", Name: "dns-tunnel", Interface: "wg0"}}
	fm.mu.Unlock()

	code, _ := putJSON(t, srv.URL+"/api/v1/dns/policy", `{"enabled":true,"primary":["9.9.9.9"],"tunnel":"proton"}`)
	if code != http.StatusOK {
		t.Fatalf("PUT: %d", code)
	}
	if len(fm.groupPuts) != 1 || !strings.Contains(fm.groupPuts[0], "single:dns-tunnel@proton") {
		t.Fatalf("группа должна обновиться single-PUT-ом: %v", fm.groupPuts)
	}

	code, _ = putJSON(t, srv.URL+"/api/v1/dns/policy", `{"enabled":true,"primary":["9.9.9.9"],"tunnel":"proton"}`)
	if code != http.StatusOK {
		t.Fatalf("PUT 2: %d", code)
	}
	if len(fm.groupPuts) != 1 {
		t.Fatalf("повторное применение должно быть no-op по группе: %v", fm.groupPuts)
	}
}

// POST /dns/policy/probe: канарейка по уровням с подписями L1-L5
func TestDnsPolicyProbe(t *testing.T) {
	srv, _ := policyServer(t)
	// сохраним политику (PUT с живым upstream)
	if code, _ := putJSON(t, srv.URL+"/api/v1/dns/policy", `{"enabled":true,"primary":["127.0.0.1:1"],"fallback":["127.0.0.1:2"],"yandex":true}`); code != http.StatusOK {
		t.Fatalf("PUT: %d", code)
	}

	resp, err := http.Post(srv.URL+"/api/v1/dns/policy/probe", "application/json", strings.NewReader(`{}`))
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
	if resp.StatusCode != http.StatusOK || len(body.Results) != 3 {
		t.Fatalf("probe: status=%d results=%+v", resp.StatusCode, body.Results)
	}
	if !strings.HasPrefix(body.Results[0].Level, "L1 напрямую: 127.0.0.1:1") {
		t.Fatalf("подпись L1: %q", body.Results[0].Level)
	}
	if !strings.HasPrefix(body.Results[2].Level, "L4 Яндекс") {
		t.Fatalf("подпись L4: %q", body.Results[2].Level)
	}
	// мёртвые локальные порты - error; L4 Яндекс (реальный сервер) - чисто.
	// Внешний UDP из CI-раннера может не пройти за бюджет (флейк v0.7.1:
	// раннер US не дотянулся до 77.88.8.8) - transport-error терпим,
	// подмену (spoof) - нет: это содержательный вердикт канарейки
	if body.Results[0].Verdict != "error" || body.Results[1].Verdict != "error" {
		t.Fatalf("мёртвые уровни должны быть error: %+v", body.Results)
	}
	switch body.Results[2].Verdict {
	case "clean":
	case "error":
		t.Skipf("L4 Яндекс не ответил за бюджет (внешняя сеть CI): %+v", body.Results[2])
	default:
		t.Fatalf("L4 Яндекс подменён?: %+v", body.Results[2])
	}
}

// normalizeUpstream со схемами: DoT/DoH-канонизация (умолчания портов/пути)
func TestNormalizeUpstreamSchemes(t *testing.T) {
	ok := map[string]string{
		"tls://1.1.1.1":             "tls://1.1.1.1:853",
		"tls://dns.google:8853":     "tls://dns.google:8853",
		"https://dns.google":        "https://dns.google:443/dns-query",
		"https://1.1.1.1/dns-query": "https://1.1.1.1:443/dns-query",
		" https://dns.google/ ":     "https://dns.google:443/dns-query",
	}
	for raw, want := range ok {
		got, err := normalizeUpstream(raw)
		if err != nil || got != want {
			t.Fatalf("normalizeUpstream(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, bad := range []string{"tls://", "https://", "http://x", "quic://x", "tls://1.1.1.1/p", "tls://x:0", "https://x:99999/"} {
		if _, err := normalizeUpstream(bad); err == nil {
			t.Fatalf("normalizeUpstream(%q) должен ошибаться", bad)
		}
	}
}

// rev8-форк: политика с DoT/DoH-уровнями честно отказывается (нужен rev11),
// plain работает как раньше; rev11: схема доезжает каноном, статика
// dns-tunnel группы пополняется адресом DoH-провайдера политики
func TestDnsPolicyEncryptedLevels(t *testing.T) {
	srv, fm := policyServer(t) // fakeMT rev8

	code, out := putJSON(t, srv.URL+"/api/v1/dns/policy",
		`{"enabled":true,"primary":["tls://1.1.1.1"],"fallback":["https://dns.google"]}`)
	if code != http.StatusNotImplemented || !strings.Contains(out["error"].(string), "rev11") {
		t.Fatalf("rev8 должен требовать rev11: %d %+v", code, out)
	}
	if len(fm.lastPuts) != 0 || len(fm.groupPuts) != 0 {
		t.Fatalf("отказ должен быть до записи в MT: %v %v", fm.lastPuts, fm.groupPuts)
	}

	// выключенная с схемой - сохраняется (применения нет)
	code, _ = putJSON(t, srv.URL+"/api/v1/dns/policy", `{"enabled":false,"primary":["tls://1.1.1.1"]}`)
	if code != http.StatusOK {
		t.Fatalf("выключенная политика с DoT: %d", code)
	}

	// rev11-форк: применение проходит, адреса каноном, статика в группе
	fm.mu.Lock()
	fm.version = "0.8.2-rev11-mawg-dev"
	fm.mu.Unlock()
	code, out = putJSON(t, srv.URL+"/api/v1/dns/policy",
		`{"enabled":true,"primary":["tls://1.1.1.1"],"tunnel":"proton","fallback":["https://203.0.113.9"]}`)
	if code != http.StatusOK {
		t.Fatalf("rev11 apply: %d %+v", code, out)
	}
	if len(fm.lastPuts) != 1 ||
		!strings.Contains(fm.lastPuts[0], `"tls://1.1.1.1:853"`) ||
		!strings.Contains(fm.lastPuts[0], `"https://203.0.113.9:443/dns-query"`) {
		t.Fatalf("порядок с схемами: %s", fm.lastPuts[0])
	}
	if len(fm.groupPuts) != 1 || !strings.Contains(fm.groupPuts[0], "dns-tunnel@proton") {
		t.Fatalf("группа dns-tunnel: %v", fm.groupPuts)
	}
	// статика политики (адрес DoH-провайдера) - в правилах группы
	fm.mu.Lock()
	staticOK := false
	for _, g := range fm.groups {
		if g.Name != "dns-tunnel" {
			continue
		}
		for _, r := range g.Rules {
			if r.Type == "subnet" && r.Rule == "203.0.113.9/32" {
				staticOK = true
			}
		}
	}
	fm.mu.Unlock()
	if !staticOK {
		t.Fatalf("статика dns-tunnel для DoH-провайдера не добавлена: %+v", fm.groups)
	}

	// GET теперь видит dohCapable
	body := getJSON(t, srv.URL+"/api/v1/dns/policy", http.StatusOK)
	if body["dohCapable"] != true {
		t.Fatalf("dohCapable: %+v", body)
	}
}

// upstreamHostIPs: IP-литерал напрямую, имя резолвится, мусор - в failed
func TestUpstreamHostIPs(t *testing.T) {
	ips, failed := upstreamHostIPs(context.Background(), []string{
		"tls://192.0.2.10:853",
		"https://198.51.100.7:443/dns-query",
		"8.8.8.8:53",
		"tls://192.0.2.10:853", // дубль - схлопнется
	})
	if len(failed) != 0 {
		t.Fatalf("failed: %v", failed)
	}
	want := []string{"192.0.2.10", "198.51.100.7", "8.8.8.8"}
	if len(ips) != len(want) {
		t.Fatalf("ips: %v", ips)
	}
	for i := range want {
		if ips[i] != want[i] {
			t.Fatalf("ips[%d]=%s, хочу %s", i, ips[i], want[i])
		}
	}
	// неразрешимое имя - честный failed, не падение
	ips, failed = upstreamHostIPs(context.Background(), []string{"tls://нет-такого-хоста.invalid"})
	if len(ips) != 0 || len(failed) != 1 {
		t.Fatalf("failed-имя: ips=%v failed=%v", ips, failed)
	}
}
