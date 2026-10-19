package warp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mawg/internal/wgconf"
)

// validKey - корректный по формату WG-ключ (44 символа base64): фейковый
// ответ Cloudflare и фикстуры конфигов.
func validKey(byteVal byte) string {
	return base64.StdEncoding.EncodeToString(make([]byte, 32)) [0:0] + strings.Repeat("A", 43) + "="
}

// fakeCF - сервер, притворяющийся Cloudflare-регистрацией: отвечает
// top-level формой v0a2158. Считает запросы и записывает заголовки.
type fakeCF struct {
	srv    *httptest.Server
	calls  int
	lastUA string
	lastCV string
	fail   int  // сколько первых запросов отдать failCode
	failCode int
}

func newFakeCF(t *testing.T) *fakeCF {
	t.Helper()
	f := &fakeCF{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /"+Version+"/reg", func(w http.ResponseWriter, r *http.Request) {
		f.calls++
		f.lastUA = r.Header.Get("User-Agent")
		f.lastCV = r.Header.Get("CF-Client-Version")
		if f.fail > 0 {
			f.fail--
			w.WriteHeader(f.failCode)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(new(map[string]any)); err != nil {
			t.Errorf("тело регистрации не JSON: %v", err)
		}
		writeJSON(w, map[string]any{
			"id":    "dev-1",
			"token": "tok-1",
			"config": map[string]any{
				"peers": []any{map[string]any{
					"public_key": validKey(0),
					"endpoint":   map[string]any{"host": DomainEndpoint + ":2408"},
				}},
				"interface": map[string]any{
					"addresses": map[string]any{"v4": "172.16.0.2", "v6": "2606:4700:1111::/128"},
				},
			},
		})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func TestRegisterSuccess(t *testing.T) {
	f := newFakeCF(t)
	c := &Client{Hosts: []string{f.srv.URL}}
	dev, err := c.Register(context.Background(), validKey(0))
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if dev.ID != "dev-1" || dev.Token != "tok-1" {
		t.Errorf("id/token: %+v", dev)
	}
	if dev.PeerPublic != validKey(0) || dev.AddressV4 != "172.16.0.2" {
		t.Errorf("пир/адрес: %+v", dev)
	}
	if dev.PeerEndpoint != DomainEndpoint+":2408" {
		t.Errorf("эндпоинт из ответа: %q", dev.PeerEndpoint)
	}
	if f.lastUA != userAgent {
		t.Errorf("User-Agent: %q", f.lastUA)
	}
	if f.lastCV != clientVersion {
		t.Errorf("CF-Client-Version: %q", f.lastCV)
	}
}

// TestRegisterHostFailover - первый хост глухой по сети -> перебор,
// HTTP-статус от живого хоста -> финал без перебора (контракт LxBox 418).
func TestRegisterHostFailover(t *testing.T) {
	f := newFakeCF(t)
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("не должен вызваться")
	}))
	dead.Close() // порт закрыт: соединение мгновенно отказывает
	deadURL := strings.Replace(dead.URL, "127.0.0.1", "127.0.0.1", 1)

	c := &Client{Hosts: []string{deadURL, f.srv.URL}}
	dev, err := c.Register(context.Background(), validKey(0))
	if err != nil {
		t.Fatalf("Register с перебором: %v", err)
	}
	if dev.ID != "dev-1" || f.calls != 1 {
		t.Fatalf("ожидали один успешный вызов,_calls=%d", f.calls)
	}

	// HTTP 403 от первого хоста - финал: второй хост не должен зваться.
	f2 := newFakeCF(t)
	f2.fail, f2.failCode = 1, http.StatusForbidden
	c2 := &Client{Hosts: []string{f2.srv.URL, f.srv.URL}}
	if _, err := c2.Register(context.Background(), validKey(0)); err == nil {
		t.Fatal("ожидали ошибку HTTP 403")
	} else if !strings.Contains(err.Error(), "403") {
		t.Fatalf("ошибка без статуса: %v", err)
	}
	if f.calls != 1 {
		t.Fatalf("второй хост не должен зваться при HTTP-статусе, calls=%d", f.calls)
	}
}

func TestRegisterUnreachable(t *testing.T) {
	dead1 := "http://127.0.0.1:1"
	dead2 := "http://127.0.0.1:2"
	c := &Client{Hosts: []string{dead1, dead2}}
	_, err := c.Register(context.Background(), validKey(0))
	if err == nil {
		t.Fatal("ожидали UnreachableError")
	}
	var ue *UnreachableError
	if !asUnreachable(err, &ue) {
		t.Fatalf("тип ошибки не UnreachableError: %T %v", err, err)
	}
	if len(ue.Hosts) != 2 {
		t.Fatalf("в ошибке должны быть оба хоста: %v", ue.Hosts)
	}
}

func asUnreachable(err error, target **UnreachableError) bool {
	if ue, ok := err.(*UnreachableError); ok {
		*target = ue
		return true
	}
	return false
}

// TestGenerateOneRetry429 - 429 потом успех: один ретрай спасает.
func TestGenerateOneRetry429(t *testing.T) {
	old := retryWait
	retryWait = time.Millisecond
	t.Cleanup(func() { retryWait = old })

	f := newFakeCF(t)
	f.fail, f.failCode = 1, http.StatusTooManyRequests
	nc, dev, err := GenerateOne(context.Background(), &Client{Hosts: []string{f.srv.URL}}, "162.159.192.1:2408", []string{"0.0.0.0/0"})
	if err != nil {
		t.Fatalf("GenerateOne после ретрая: %v", err)
	}
	if dev == nil || dev.AddressV4 != "172.16.0.2" {
		t.Fatalf("device: %+v", dev)
	}
	if nc.Config.Endpoint() != "162.159.192.1:2408" {
		t.Errorf("эндпоинт конфига: %q", nc.Config.Endpoint())
	}
	if nc.Config.FirstIPv4() != "172.16.0.2" {
		t.Errorf("v4 конфига: %q", nc.Config.FirstIPv4())
	}
	if nc.Config.Peer.PublicKey != validKey(0) || nc.Config.MTU != DefaultMTU {
		t.Errorf("пир/MTU: %+v", nc.Config)
	}
	if !nc.Config.AWG.Present() {
		t.Fatal("конфиг пула должен нести поля обфускации AWG 2.0 (слово владельца)")
	}
	if nc.Config.AWG.AWG3() {
		t.Error("уровень должен быть 2.0, без 3.x-полей")
	}
	if *nc.Config.AWG.Jc != "4" || *nc.Config.AWG.H1 != "1" {
		t.Errorf("значения обфускации: Jc=%s H1=%s", *nc.Config.AWG.Jc, *nc.Config.AWG.H1)
	}
	if f.calls != 2 {
		t.Errorf("ожидали 2 вызова (429 + ретрай), было %d", f.calls)
	}
}

func TestRandomPairsDistinct(t *testing.T) {
	pairs, err := RandomPairs(16, nil)
	if err != nil {
		t.Fatalf("RandomPairs: %v", err)
	}
	seen := map[string]bool{}
	for _, e := range pairs {
		if seen[e] {
			t.Fatalf("дубль пары: %s", e)
		}
		seen[e] = true
		if err := ValidEndpoint(e); err != nil {
			t.Fatalf("пара %s не валидна: %v", e, err)
		}
		if !IsWarpEndpoint(e) {
			t.Fatalf("пара %s не из варповых сетей", e)
		}
	}
}

func TestIsWarpEndpointHost(t *testing.T) {
	yes := []string{
		"162.159.192.5", "162.159.193.1", "162.159.195.200", "188.114.96.1",
		"188.114.99.254", "8.39.125.5", "8.6.112.10",
		DomainEndpoint, "[2606:4700:d0::1]",
	}
	no := []string{"8.8.8.8", "1.1.1.1", "217.23.3.76", "example.com", ""}
	for _, h := range yes {
		if !IsWarpEndpointHost(h) {
			t.Errorf("%s должен распознаваться как WARP", h)
		}
	}
	for _, h := range no {
		if IsWarpEndpointHost(h) {
			t.Errorf("%s не должен распознаваться как WARP", h)
		}
	}
}

// TestBuildConfigAWGVariant - простой конфиг парсится без AWG-полей,
// вариант с полями - с ними (и значения - прозрачная обфускация).
func TestBuildConfigAWGVariant(t *testing.T) {
	priv, _, err := GenerateKeys()
	if err != nil {
		t.Fatalf("GenerateKeys: %v", err)
	}
	raw := BuildConfig(priv, "172.16.0.2", validKey(0), DomainEndpoint+":500", []string{"0.0.0.0/0"})
	cfg, err := wgconf.Parse(raw)
	if err != nil {
		t.Fatalf("разбор простого конфига: %v", err)
	}
	if cfg.AWG.Present() {
		t.Error("простой конфиг с AWG-полями")
	}
	if cfg.Peer.PersistentKeepalive != DefaultKeepalive || cfg.Peer.EndpointPort != 500 {
		t.Errorf("keepalive/порт: %+v", cfg.Peer)
	}
	if !strings.Contains(string(raw), "AllowedIPs = 0.0.0.0/0") {
		t.Errorf("AllowedIPs: %s", raw)
	}

	awg := WithAWGFields(raw)
	cfg2, err := wgconf.Parse(awg)
	if err != nil {
		t.Fatalf("разбор AWG-варианта: %v", err)
	}
	if !cfg2.AWG.Present() {
		t.Fatal("AWG-вариант без AWG-полей")
	}
	for name, val := range map[string]*string{
		"Jc": cfg2.AWG.Jc, "Jmin": cfg2.AWG.Jmin, "Jmax": cfg2.AWG.Jmax,
		"S1": cfg2.AWG.S1, "H1": cfg2.AWG.H1, "H4": cfg2.AWG.H4,
	} {
		if val == nil || *val == "" {
			t.Errorf("AWG-поле %s пустое", name)
		}
	}
	if *cfg2.AWG.Jc != "4" || *cfg2.AWG.H1 != "1" || *cfg2.AWG.H4 != "4" {
		t.Errorf("значения обфускации не как у warp-generator: Jc=%s H1=%s H4=%s", *cfg2.AWG.Jc, *cfg2.AWG.H1, *cfg2.AWG.H4)
	}
	if cfg2.AWG.AWG3() {
		t.Error("прозрачная обфускация не должна быть AWG3 (нативный kmod поднимет)")
	}
}

// TestAllowedIPsSitesAndSubnets - пресеты дают CIDR, свой ввод - только
// подсети (слово владельца: WireGuard в AllowedIPs домены не понимает),
// голый IP -> /32, не-подсеть -> ошибка, пусто - полный туннель.
func TestAllowedIPsSitesAndSubnets(t *testing.T) {
	cidrs, warns, err := AllowedIPs([]string{"rutracker"}, nil)
	if err != nil || len(warns) != 0 {
		t.Fatalf("err=%v warns=%v", err, warns)
	}
	if len(cidrs) < 2 || !containsStr(cidrs, "185.81.128.0/23") {
		t.Errorf("пресет rutracker не лёг в AllowedIPs: %v", cidrs)
	}
	cidrs2, _, err := AllowedIPs(nil, []string{"192.0.2.10", "203.0.113.0/24"})
	if err != nil {
		t.Fatalf("валидные подсети отклонены: %v", err)
	}
	if !containsStr(cidrs2, "192.0.2.10/32") || !containsStr(cidrs2, "203.0.113.0/24") {
		t.Errorf("подсети не легли в AllowedIPs: %v", cidrs2)
	}
	cidrs3, _, err := AllowedIPs(nil, nil)
	if err != nil || len(cidrs3) != 1 || cidrs3[0] != "0.0.0.0/0" {
		t.Errorf("дефолт должен быть полным туннелем: %v (err=%v)", cidrs3, err)
	}
	if _, _, err := AllowedIPs(nil, []string{"youtube.com", "192.0.2.1"}); err == nil ||
		!strings.Contains(err.Error(), "youtube.com") {
		t.Errorf("домен должен быть отвергнут с именем в ошибке: %v", err)
	}
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// TestParsePresetsEndpoint - пресеты пар все валидны и варповы.
func TestParsePresetsEndpoint(t *testing.T) {
	for _, p := range Presets {
		if err := ValidEndpoint(p); err != nil {
			t.Errorf("пресет %s: %v", p, err)
		}
		host, _, err := net.SplitHostPort(p)
		if err != nil || !IsWarpEndpointHost(host) {
			t.Errorf("пресет %s не из варповых сетей", p)
		}
	}
	if len(Ports) < 50 {
		t.Errorf("пул портов подозрительно мал: %d", len(Ports))
	}
	fmtForce := fmt.Sprintf("%d", Ports[0])
	if fmtForce == "" {
		t.Error("порты пусты")
	}
}
