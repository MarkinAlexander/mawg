package web

// Переключатель «DNS управляется mawg» (115-док задача B): off без
// confirm = 409; off с confirm = yaml MT получил disableRemap53, рестарт
// зван, флаг в сторе; PUT-ы DNS гейтятся 409; on возвращает перехват.
// ShellFn эмулирует правду роутера: цепочка MT_DNSOR жива, только если
// в yaml MT нет disableRemap53: true (рестарт применяет файл).

import (
	"encoding/json"
	"io"
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

type managedBackend struct {
	*fake.Fake
	cfgPath string
}

func (b *managedBackend) MTConfigPath() string { return b.cfgPath }

func dnsManagedTestEnv(t *testing.T) (*httptest.Server, *store.Store, *managedBackend) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const cfg = `configVersion: 0.7
app:
  dnsProxy:
    host:
      address: '[::]'
      port: 3553
`
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	fb := fake.New()
	b := &managedBackend{Fake: fb, cfgPath: p}
	fb.ShellFn = func(script string) (string, error) {
		if strings.Contains(script, "MT_DNSOR") {
			data, _ := os.ReadFile(b.cfgPath)
			if strings.Contains(string(data), "disableRemap53: true") {
				return "0\n", nil
			}
			return "1\n", nil
		}
		return "", nil
	}
	magitrickle.SetShadowStorage(nil, nil)
	e := rotator.New(st, b, magitrickle.New("http://127.0.0.1:1"))
	srv := New(st, e, b, magitrickle.New("http://127.0.0.1:1"), "test", nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, st, b
}

func readBody(resp *http.Response) string {
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return string(raw)
}

func httpGetJSON(t *testing.T, url string) map[string]any {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestDNSManagedOffOn(t *testing.T) {
	ts, st, b := dnsManagedTestEnv(t)
	url := ts.URL

	// off без confirm - честный 409
	resp, err := httpPut(url+"/api/v1/dns/managed", `{"managed":false}`)
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(resp)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(body, "needConfirm") {
		t.Fatalf("off без confirm: status %d, body %s", resp.StatusCode, body)
	}

	// off с confirm: yaml правится, MT рестартует, флаг в сторе
	before := b.MTRestarts()
	resp, err = httpPut(url+"/api/v1/dns/managed", `{"managed":false,"confirm":true}`)
	if err != nil {
		t.Fatal(err)
	}
	body = readBody(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("off с confirm: status %d, body %s", resp.StatusCode, body)
	}
	if b.MTRestarts() != before+1 {
		t.Fatalf("рестарт MT не зван: %d -> %d", before, b.MTRestarts())
	}
	data, _ := os.ReadFile(b.cfgPath)
	if !strings.Contains(string(data), "disableRemap53: true") {
		t.Fatalf("disableRemap53 не записан:\n%s", string(data))
	}
	if v := st.DnsManaged(); v == nil || *v {
		t.Fatalf("флаг в сторе: %v", v)
	}

	// GET показывает режим и факт
	got := httpGetJSON(t, url+"/api/v1/dns/managed")
	if managed, ok := got["managed"].(bool); !ok || managed {
		t.Fatalf("GET managed: %v", got["managed"])
	}
	if got["remap53"] != false {
		t.Fatalf("GET remap53: %v", got["remap53"])
	}

	// гейты: правки DNS в режиме off - 409
	resp, err = http.Post(url+"/api/v1/dns/dnsmasq", "application/json", strings.NewReader(`{"mode":"mt"}`))
	if err != nil {
		t.Fatal(err)
	}
	body = readBody(resp)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(body, "не лезет") {
		t.Fatalf("гейт dnsmasq: status %d, body %s", resp.StatusCode, body)
	}
	for _, ep := range []string{"/api/v1/dns/upstreams", "/api/v1/dns/policy", "/api/v1/dns/zones"} {
		resp, err := httpPut(url+ep, `{"mode":"mt"}`)
		if err != nil {
			t.Fatal(err)
		}
		body = readBody(resp)
		if resp.StatusCode != http.StatusConflict || !strings.Contains(body, "не лезет") {
			t.Fatalf("гейт %s: status %d, body %s", ep, resp.StatusCode, body)
		}
	}

	// on: перехват возвращается, флаг в nil
	resp, err = httpPut(url+"/api/v1/dns/managed", `{"managed":true}`)
	if err != nil {
		t.Fatal(err)
	}
	body = readBody(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("on: status %d, body %s", resp.StatusCode, body)
	}
	data, _ = os.ReadFile(b.cfgPath)
	if !strings.Contains(string(data), "disableRemap53: false") {
		t.Fatalf("disableRemap53 не снят:\n%s", string(data))
	}
	if v := st.DnsManaged(); v != nil {
		t.Fatalf("флаг после on: %v, want nil", v)
	}
}

func TestDNSManagedOnNoop(t *testing.T) {
	ts, _, b := dnsManagedTestEnv(t)
	before := b.MTRestarts()
	resp, err := httpPut(ts.URL+"/api/v1/dns/managed", `{"managed":true}`)
	if err != nil {
		t.Fatal(err)
	}
	readBody(resp)
	if b.MTRestarts() != before {
		t.Fatalf("noop on трогает рестарт: %d -> %d", before, b.MTRestarts())
	}
}
