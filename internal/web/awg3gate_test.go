package web

// Гейт AWG 3.x × движки (95-док, слово владельца 30.10): в нативный пул
// конфиг, требующий движка, не сохраняется; движковый выбор ядра
// (sing-box / mihomo) валидируется по установленным ядрам.

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
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

func awg3ConfBody(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/awg3-premium.conf")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// awg2ConfBody - тот же конфиг без 3.x-полей: натив его поднимает.
func awg2ConfBody(t *testing.T) string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(awg3ConfBody(t), "\n") {
		trim := strings.TrimSpace(strings.ToLower(line))
		skip := false
		for _, bad := range []string{"headerprotectionkey", "rekeyafter", "rekeytimeout", "rejectafter",
			"keepalivetimeout", "maxhandshake", "contentpadding", "randomtrailers", "disablecookies", "i1 ", "persistentkeepalive"} {
			if strings.HasPrefix(trim, bad) {
				skip = true
				break
			}
		}
		if !skip {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func uploadConfigsPost(t *testing.T, srv *httptest.Server, pool, name, body string) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("files", name)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte(body))
	mw.Close()
	resp, err := http.Post(srv.URL+"/api/v1/pools/"+pool+"/configs", mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func postJSONBody(t *testing.T, srv *httptest.Server, url, body string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(srv.URL+url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// configsOf - число конфигов пула из /status.
func configsOf(t *testing.T, srv *httptest.Server, pool string) int {
	t.Helper()
	resp, err := http.Get(srv.URL + "/api/v1/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var st struct {
		Pools []struct {
			Name    string `json:"name"`
			Configs []any  `json:"configs"`
		} `json:"pools"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	for _, p := range st.Pools {
		if p.Name == pool {
			return len(p.Configs)
		}
	}
	return -1
}

// Чистый AWG 3.x в нативный пул: отказ целиком, конфиг НЕ сохранён.
func TestUploadConfigsRefusesAWG3(t *testing.T) {
	ts := newTestServer(t)
	if code, out := postJSONBody(t, ts, "/api/v1/pools", `{"name":"nat","fallback":"direct"}`); code != http.StatusOK {
		t.Fatalf("создание пула: код %d: %v", code, out)
	}
	code, out := uploadConfigsPost(t, ts, "nat", "awg3.conf", awg3ConfBody(t))
	if code != http.StatusBadRequest {
		t.Fatalf("код %d: %v", code, out)
	}
	if msg, _ := out["error"].(string); !strings.Contains(msg, "ни sing-box, ни mihomo") {
		t.Fatalf("текст отказа без движков: %v", out)
	}
	if n := configsOf(t, ts, "nat"); n != 0 {
		t.Fatalf("AWG 3.x конфиг сохранился: %d", n)
	}
}

// Смешанная загрузка: AWG 2.0 проходит, AWG 3.x отброшен с ошибкой.
func TestUploadConfigsMixed(t *testing.T) {
	ts := newTestServer(t)
	if code, out := postJSONBody(t, ts, "/api/v1/pools", `{"name":"nat2","fallback":"direct"}`); code != http.StatusOK {
		t.Fatalf("создание пула: код %d: %v", code, out)
	}
	if code, out := uploadConfigsPost(t, ts, "nat2", "awg3.conf", awg3ConfBody(t)); code != http.StatusBadRequest {
		t.Fatalf("awg3 должен быть отклонён: код %d: %v", code, out)
	}
	code, out := uploadConfigsPost(t, ts, "nat2", "awg2.conf", awg2ConfBody(t))
	if code != http.StatusOK {
		t.Fatalf("awg2 отклонён: код %d: %v", code, out)
	}
	if added, _ := out["added"].(float64); added != 1 {
		t.Fatalf("added %v: %v", out["added"], out)
	}
}

// Пустой пул на движке без установленных ядер - честный отказ.
func TestCreateEnginePoolRefusesWithoutEngines(t *testing.T) {
	ts := newTestServer(t)
	code, out := postJSONBody(t, ts, "/api/v1/pools", `{"name":"eng","fallback":"direct","engineMode":"singbox"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("код %d: %v", code, out)
	}
	if msg, _ := out["error"].(string); !strings.Contains(msg, "ни sing-box, ни mihomo") {
		t.Fatalf("текст: %v", out)
	}
}

// awg3CorePick: без ядер отказывает с перечнем; явное ядро без движка - тоже.
func TestAwg3CorePick(t *testing.T) {
	st, errr := store.Open(t.TempDir())
	if errr != nil {
		t.Fatal(errr)
	}
	fb := fake.New()
	e := rotator.New(st, fb, magitrickle.New("http://127.0.0.1:1"))
	srv := New(st, e, fb, nil, "test", nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	if _, _, err := srv.awg3CorePick(""); err == nil {
		t.Fatal("без ядер автопик должен отказывать")
	} else if !strings.Contains(err.Error(), "ни sing-box, ни mihomo") {
		t.Fatalf("текст: %v", err)
	}
	if _, _, err := srv.awg3CorePick("mihomo"); err == nil {
		t.Fatal("явный mihomo без движка должен отказывать")
	}
}
