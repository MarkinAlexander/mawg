package web

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mawg/internal/platform"
	"mawg/internal/platform/fake"
	"mawg/internal/rotator"
	"mawg/internal/store"
	"mawg/internal/warp"
	"mawg/internal/wgconf"
)

// warpCFServer - фейк регистрации Cloudflare (top-level форма v0a2158).
// У каждого устройства свои id/token/ПИРОВОЙ ключ (у Cloudflare пир свой
// на регистрацию) - дедуп AddConfigs идёт по peer|endpoint.
func warpCFServer(t *testing.T) *httptest.Server {
	t.Helper()
	// Счётчик вместо часов: time.Now() на Windows гранулярный, два
	// быстрых запроса обязаны отличаться (дедуп AddConfigs по peer|endpoint).
	var cfSeq atomic.Int64
	uniq := func() string { return strconv.FormatInt(cfSeq.Add(1), 10) }
	b64key := func() string {
		return base64.StdEncoding.EncodeToString([]byte(pad32(uniq())))
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/reg") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id": "dev-" + uniq(), "token": "tok-" + uniq(),
			"config": map[string]any{
				"peers": []any{map[string]any{
					"public_key": b64key(),
					"endpoint":   map[string]any{"host": "engage.cloudflareclient.com:2408"},
				}},
				"interface": map[string]any{
					"addresses": map[string]any{"v4": "172.16.0.2", "v6": "2606:4700:1111::/128"},
				},
			},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func pad32(s string) string {
	if len(s) > 32 {
		return s[:32]
	}
	return s + strings.Repeat("A", 32-len(s))
}

// warpTestServer - панель с фейк-платформой, фейком Cloudflare и
// ускоренными часами движка (ProbeConfigNow иначе спит 3с на конфиг -
// грейс iface-операций).
func warpTestServer(t *testing.T) (*httptest.Server, *fake.Fake, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	fb := fake.New()
	engine := rotator.New(st, fb, nil)
	at := time.Now()
	engine.Clock = func() time.Time {
		at = at.Add(3 * time.Second) // грейс iface-операций уже «прошёл»
		return at
	}
	srv := New(st, engine, fb, nil, "test", nil)
	srv.SetWarpClient(&warp.Client{Hosts: []string{warpCFServer(t).URL}})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, fb, st
}

func warpPost(ts *httptest.Server, path, body string) (int, []byte) {
	resp, err := http.Post(ts.URL+path, "application/json", strings.NewReader(body))
	if err != nil {
		return -1, nil
	}
	defer resp.Body.Close()
	buf := make([]byte, 1<<20)
	n, _ := resp.Body.Read(buf)
	return resp.StatusCode, buf[:n]
}

func warpGetBody(resp *http.Response) string {
	defer resp.Body.Close()
	buf := make([]byte, 1<<16)
	n, _ := resp.Body.Read(buf)
	return string(buf[:n])
}

func TestWarpGenerateDownloadOnly(t *testing.T) {
	ts, _, st := warpTestServer(t)
	code, body := warpPost(ts, "/api/v1/warp/generate",
		`{"count":2,"mode":"fixed","endpoint":"engage.cloudflareclient.com:2408"}`)
	if code != http.StatusOK {
		t.Fatalf("generate: %d %s", code, body)
	}
	var plan struct {
		Pool    string `json:"pool"`
		Added   int    `json:"added"`
		Configs []struct {
			Endpoint string `json:"endpoint"`
			WG       string `json:"wg"`
			AWG      string `json:"awg"`
		} `json:"configs"`
	}
	if err := json.Unmarshal(body, &plan); err != nil {
		t.Fatalf("план: %v (%s)", err, body)
	}
	if plan.Pool != "" || plan.Added != 2 || len(plan.Configs) != 2 {
		t.Fatalf("download-режим не должен создавать пул: %+v", plan)
	}
	for _, cf := range plan.Configs {
		if !strings.Contains(cf.WG, "PrivateKey = ") {
			t.Error("WG-текст без приватного ключа (он нужен для внешнего клиента)")
		}
		if !strings.Contains(cf.AWG, "Jc = 4") || !strings.Contains(cf.AWG, "I1 = <b 0x494e56495445") {
			t.Error("AWG-текст без полей обфускации 2.0")
		}
		if strings.Contains(cf.WG, "Jc = 4") {
			t.Error("чистый WG не должен нести AWG-поля")
		}
	}
	if pools := st.Pools(); len(pools) != 0 {
		t.Fatalf("download-режим создал пулы: %v", pools)
	}
}

func TestWarpGeneratePoolAutoDelete(t *testing.T) {
	ts, _, st := warpTestServer(t)
	// пробы НЕ настроены - все конфиги «не отвечают»: автоудаление,
	// пустой пул не оставляем
	code, body := warpPost(ts, "/api/v1/warp/generate",
		`{"count":2,"mode":"random","dest":"pool"}`)
	if code != http.StatusOK {
		t.Fatalf("generate: %d %s", code, body)
	}
	var plan struct {
		Pool    string `json:"pool"`
		Added   int    `json:"added"`
		Results []struct {
			Endpoint string `json:"endpoint"`
			OK       bool   `json:"ok"`
			Removed  bool   `json:"removed"`
		} `json:"results"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(body, &plan); err != nil {
		t.Fatalf("план: %v (%s)", err, body)
	}
	if plan.Pool != "" || plan.Added != 0 {
		t.Fatalf("пул без рабочих конфигов должен быть удалён: %+v", plan)
	}
	if len(plan.Results) != 2 {
		t.Fatalf("ожидали 2 результата: %+v", plan.Results)
	}
	for _, res := range plan.Results {
		if !res.Removed || res.OK {
			t.Errorf("конфиг должен быть помечен удалённым: %+v", res)
		}
	}
	if pools := st.Pools(); len(pools) != 0 {
		t.Fatalf("пул не удалён после провала всех конфигов: %v", pools)
	}
	joined := strings.Join(plan.Warnings, " ")
	if !strings.Contains(joined, "удалены") {
		t.Errorf("в предупреждениях нет автоудаления: %v", plan.Warnings)
	}
}

func TestWarpGenerateAddToExistingActivate(t *testing.T) {
	ts, fb, st := warpTestServer(t)
	fb.SetProbe("warp", true)
	fb.StatusMap["warp"] = platform.TunnelStatus{LinkUp: true, Connected: true, HandshakeAgo: 5}

	// первый прогон создаёт пул
	code, _ := warpPost(ts, "/api/v1/warp/generate", `{"count":1,"mode":"random","dest":"pool"}`)
	if code != http.StatusOK {
		t.Fatalf("generate: %d", code)
	}
	before := st.State("warp").ActiveFile
	if before == "" {
		t.Fatal("пул без активного конфига")
	}

	// второй - в существующий пул с переключением на новый
	code, body := warpPost(ts, "/api/v1/warp/generate",
		`{"count":1,"mode":"random","dest":"pool","pool":"warp","activate":true}`)
	if code != http.StatusOK {
		t.Fatalf("add: %d %s", code, body)
	}
	var plan struct {
		Pool   string `json:"pool"`
		Added  int    `json:"added"`
		Results []struct {
			File string `json:"file"`
			OK   bool   `json:"ok"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &plan); err != nil {
		t.Fatalf("план: %v (%s)", err, body)
	}
	if plan.Pool != "warp" || plan.Added != 1 || !plan.Results[0].OK {
		t.Fatalf("план: %+v", plan)
	}
	after := st.State("warp").ActiveFile
	if after == before || after != plan.Results[0].File {
		t.Fatalf("activate не переключил конфиг: было %q, стало %q", before, after)
	}
	warpPool, _ := st.Pool("warp")
	if len(warpPool.Configs) != 2 {
		t.Fatalf("в пуле %d конфигов, ожидали 2", len(warpPool.Configs))
	}
}

func TestWarpGenerateCreatesPool(t *testing.T) {
	ts, fb, st := warpTestServer(t)
	fb.SetProbe("warp", true)
	fb.StatusMap["warp"] = platform.TunnelStatus{LinkUp: true, Connected: true, HandshakeAgo: 5}
	code, body := warpPost(ts, "/api/v1/warp/generate",
		`{"count":2,"mode":"fixed","endpoint":"engage.cloudflareclient.com:2408","dest":"pool"}`)
	if code != http.StatusOK {
		t.Fatalf("generate: %d %s", code, body)
	}
	var plan struct {
		Pool    string `json:"pool"`
		Added   int    `json:"added"`
		Results []struct {
			File     string `json:"file"`
			Endpoint string `json:"endpoint"`
			Address  string `json:"address"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &plan); err != nil {
		t.Fatalf("план не разобрался: %v (%s)", err, body)
	}
	if plan.Pool != "warp" || plan.Added != 2 || len(plan.Results) != 2 {
		t.Fatalf("план: %+v", plan)
	}
	for _, res := range plan.Results {
		if res.Address != "172.16.0.2" || res.File == "" {
			t.Errorf("результат: %+v", res)
		}
	}
	// fixed-режим: оба конфига на заданном эндпоинте (регистрации разные,
	// эндпоинт один - так и задумано).
	p, ok := st.Pool("warp")
	if !ok {
		t.Fatal("пул warp не создан")
	}
	if len(p.Configs) != 2 || p.Settings.Warp == nil || p.Settings.Warp.Mode != "fixed" {
		t.Fatalf("пул/мета: %+v %+v", p.Configs, p.Settings.Warp)
	}
	for _, c := range p.Configs {
		if c.Endpoint != "engage.cloudflareclient.com:2408" {
			t.Errorf("эндпоинт конфига: %q", c.Endpoint)
		}
	}
	// Секреты: приватный ключ не должен гулять по ответу генерации.
	if strings.Contains(string(body), "PrivateKey") {
		t.Error("приватный ключ в ответе генерации")
	}
	// Реестр регистраций: по записи на каждое устройство, без ключей.
	devs := st.WarpDevices()
	if len(devs) != 2 {
		t.Fatalf("реестр WARP: %d записей", len(devs))
	}
	for _, d := range devs {
		if d.ID == "" || d.Token == "" || d.Pool != "warp" {
			t.Errorf("запись реестра: %+v", d)
		}
	}
}

func TestWarpGenerateUnreachable(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	dead.Close() // порт закрыт: оба хоста «глухи»
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	fb := fake.New()
	engine := rotator.New(st, fb, nil)
	srv := New(st, engine, fb, nil, "test", nil)
	srv.SetWarpClient(&warp.Client{Hosts: []string{dead.URL, "http://127.0.0.1:1"}})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	code, body := warpPost(ts, "/api/v1/warp/generate", `{"count":1,"mode":"fixed"}`)
	if code != http.StatusBadGateway {
		t.Fatalf("ожидали 502, получили %d %s", code, body)
	}
	for _, marker := range []string{"unreachable", "zator", "warp-generation.github.io"} {
		if !strings.Contains(string(body), marker) {
			t.Errorf("в ошибке нет %q: %s", marker, body)
		}
	}
}

func TestWarpGenerateRandomWorking(t *testing.T) {
	ts, fb, _ := warpTestServer(t)
	fb.SetProbe("warp", true)
	fb.StatusMap["warp"] = platform.TunnelStatus{LinkUp: true, Connected: true, HandshakeAgo: 5}

	code, body := warpPost(ts, "/api/v1/warp/generate", `{"count":2,"mode":"random","dest":"pool"}`)
	if code != http.StatusOK {
		t.Fatalf("generate: %d %s", code, body)
	}
	var plan struct {
		Results []struct {
			Endpoint string `json:"endpoint"`
			OK       bool   `json:"ok"`
			Detail   string `json:"detail"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &plan); err != nil {
		t.Fatalf("план: %v (%s)", err, body)
	}
	if len(plan.Results) != 2 {
		t.Fatalf("ожидали 2 конфига: %+v", plan)
	}
	// Режим «Случайно»: пары заведомо разные (слово владельца - из
	// скольких-то конфигов хоть один жив).
	if plan.Results[0].Endpoint == plan.Results[1].Endpoint {
		t.Fatalf("random-режим дал одинаковые эндпоинты: %+v", plan.Results)
	}
	for _, res := range plan.Results {
		if !res.OK {
			t.Errorf("конфиг %s не прошёл живую проверку: %s", res.Endpoint, res.Detail)
		}
	}
}

// TestWarpGeneratePoolForcesFullTunnel - в пуле AllowedIPs всегда
// 0.0.0.0/0: пресеты/подсети игнорируются (слово владельца - юзер с
// «одним ютубом» в пуле сам себе чёрная дыра).
func TestWarpGeneratePoolForcesFullTunnel(t *testing.T) {
	ts, fb, st := warpTestServer(t)
	fb.SetProbe("warp", true)
	fb.StatusMap["warp"] = platform.TunnelStatus{LinkUp: true, Connected: true, HandshakeAgo: 5}
	code, body := warpPost(ts, "/api/v1/warp/generate",
		`{"count":1,"mode":"fixed","dest":"pool","sites":["youtube"],"subnets":["203.0.113.0/24"]}`)
	if code != http.StatusOK {
		t.Fatalf("generate: %d %s", code, body)
	}
	p, _ := st.Pool("warp")
	stored, err := os.ReadFile(filepath.Join(st.PoolDir("warp"), p.Configs[0].File))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stored), "AllowedIPs = 0.0.0.0/0\n") || strings.Contains(string(stored), "youtube") {
		t.Error("пул-режим должен форсировать полный туннель")
	}
	var plan struct {
		Warnings []string `json:"warnings"`
	}
	json.Unmarshal(body, &plan)
	joined := strings.Join(plan.Warnings, " ")
	if !strings.Contains(joined, "0.0.0.0/0") {
		t.Errorf("в предупреждениях нет пояснения: %v", plan.Warnings)
	}
}

func TestPoolWarpAddRequiresWarpPool(t *testing.T) {
	ts, fb, st := warpTestServer(t)

	// Чужой пул (конфиг мимо варповых сетей) - отказ 409.
	raw := warp.BuildConfig(strings.Repeat("A", 43)+"=", "10.2.0.2",
		strings.Repeat("B", 43)+"=", "217.23.3.76:51820", []string{"0.0.0.0/0"})
	cfg, err := wgconf.Parse(raw)
	if err != nil {
		t.Fatalf("фикстура: %v", err)
	}
	if _, err := st.CreatePool("proton", store.PoolSettings{Platform: "fake"}); err != nil {
		t.Fatalf("пул proton: %v", err)
	}
	if _, _, err := st.AddConfigs("proton", []wgconf.NamedConfig{{OriginalName: "proton", Raw: raw, Config: cfg}}); err != nil {
		t.Fatalf("конфиг proton: %v", err)
	}
	code, body := warpPost(ts, "/api/v1/pools/proton/warp", `{}`)
	if code != http.StatusConflict {
		t.Fatalf("не-варп пул должен дать 409, получили %d %s", code, body)
	}

	// Генерируем варп-пул и добавляем конфиг без мастера (пустое тело =
	// параметры из меты пула). Пробы OK - иначе пул самоудалится.
	fb.SetProbe("warp", true)
	fb.StatusMap["warp"] = platform.TunnelStatus{LinkUp: true, Connected: true, HandshakeAgo: 5}
	code, body = warpPost(ts, "/api/v1/warp/generate", `{"count":1,"mode":"fixed","dest":"pool"}`)
	if code != http.StatusOK {
		t.Fatalf("generate: %d %s", code, body)
	}
	code, body = warpPost(ts, "/api/v1/pools/warp/warp", `{}`)
	if code != http.StatusOK {
		t.Fatalf("add в варп-пул: %d %s", code, body)
	}
	var plan struct {
		Added   int `json:"added"`
		Results []struct {
			Endpoint string `json:"endpoint"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &plan); err != nil {
		t.Fatalf("план: %v (%s)", err, body)
	}
	if plan.Added != 1 || len(plan.Results) != 1 {
		t.Fatalf("ожидали +1 конфиг: %+v", plan)
	}
	p, _ := st.Pool("warp")
	if len(p.Configs) != 2 {
		t.Fatalf("в пуле %d конфигов, ожидали 2", len(p.Configs))
	}
}

func TestPoolConfigRawAWGVariant(t *testing.T) {
	ts, fb, _ := warpTestServer(t)
	// готовим пул пул-режимом (пробы OK), чтобы файл существовал на диске
	fb.SetProbe("warp", true)
	fb.StatusMap["warp"] = platform.TunnelStatus{LinkUp: true, Connected: true, HandshakeAgo: 5}
	code, body := warpPost(ts, "/api/v1/warp/generate", `{"count":1,"mode":"fixed","dest":"pool"}`)
	if code != http.StatusOK {
		t.Fatalf("generate: %d %s", code, body)
	}
	var plan struct {
		Results []struct {
			File string `json:"file"`
		} `json:"results"`
	}
	json.Unmarshal(body, &plan)
	file := plan.Results[0].File

	// конфиг в пуле теперь сразу awg2 (слово владельца: «суть в обфускации»)
	rawText := warpGetBody(mustGet(t, ts.URL+"/api/v1/pools/warp/configs/"+file))
	if !strings.Contains(rawText, "Jc = 4") {
		t.Error("конфиг пула должен нести AWG-поля")
	}
	plainText := warpGetBody(mustGet(t, ts.URL+"/api/v1/pools/warp/configs/"+file+"?variant=plain"))
	if strings.Contains(plainText, "Jc = 4") {
		t.Error("WG-вариант (variant=plain) должен снимать AWG-поля")
	}
	if _, err := wgconf.Parse([]byte(plainText)); err != nil {
		t.Errorf("снутый конфиг не разбирается: %v", err)
	}
	awgText := warpGetBody(mustGet(t, ts.URL+"/api/v1/pools/warp/configs/"+file+"?variant=awg"))
	for _, field := range []string{"Jc = 4", "Jmin = 40", "Jmax = 70", "S1 = 0", "H1 = 1", "H4 = 4",
		"I1 = <b 0x494e56495445", "I2 = <b 0x5349502f"} {
		if !strings.Contains(awgText, field) {
			t.Errorf("AWG-вариант без %q", field)
		}
	}
	// AWG 3.x-полей быть не должно (уровень 2.0 - слово владельца).
	if strings.Contains(awgText, "HeaderProtectionKey") || strings.Contains(awgText, "RekeyAfterTime") {
		t.Error("AWG-вариант с 3.x-полями")
	}
	if !strings.Contains(awgText, "PrivateKey") {
		t.Error("AWG-вариант без приватного ключа (файл конфига же)")
	}
}

func mustGet(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %d", url, resp.StatusCode)
	}
	return resp
}
