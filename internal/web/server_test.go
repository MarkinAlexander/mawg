package web

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"

	"strings"
	"testing"

	"mawg/internal/magitrickle"
	"mawg/internal/platform/fake"
	"mawg/internal/rotator"
	"mawg/internal/store"
)

const testConf = "[Interface]\nPrivateKey = QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE=\nAddress = 10.2.0.2/32\n" +
	"[Peer]\nPublicKey = QkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkI=\nEndpoint = at1.example.net:51820\nAllowedIPs = 0.0.0.0/0\n"

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fb := fake.New()
	e := rotator.New(st, fb, magitrickle.New("http://127.0.0.1:1"))
	srv := New(st, e, fb, nil, "test", nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func TestStatusAndPoolLifecycle(t *testing.T) {
	ts := newTestServer(t)

	resp, err := http.Get(ts.URL + "/api/v1/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status code %d", resp.StatusCode)
	}

	body := `{"name":"proton","fallback":"direct"}`
	resp, err = http.Post(ts.URL+"/api/v1/pools", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("create pool code %d", resp.StatusCode)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("files", "at-1.conf")
	fw.Write([]byte(testConf))
	mw.Close()
	resp, err = http.Post(ts.URL+"/api/v1/pools/proton/configs", mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var up map[string]any
	json.NewDecoder(resp.Body).Decode(&up)
	if up["added"] != float64(1) {
		t.Fatalf("upload response: %v", up)
	}

	resp, err = http.Get(ts.URL + "/api/v1/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var status struct {
		Pools []struct {
			Name    string `json:"name"`
			Configs []struct {
				File     string `json:"file"`
				Endpoint string `json:"endpoint"`
			} `json:"configs"`
		} `json:"pools"`
	}
	json.NewDecoder(resp.Body).Decode(&status)
	if len(status.Pools) != 1 || status.Pools[0].Name != "proton" {
		t.Fatalf("status pools: %+v", status.Pools)
	}
	if len(status.Pools[0].Configs) != 1 || status.Pools[0].Configs[0].Endpoint != "at1.example.net:51820" {
		t.Fatalf("pool configs: %+v", status.Pools[0].Configs)
	}

	uiResp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer uiResp.Body.Close()
	html, _ := io.ReadAll(uiResp.Body)
	if !bytes.Contains(html, []byte("mawg")) {
		t.Fatal("ui page not served")
	}
}

func TestAppUIServed(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/app/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), `id="app"`) {
		t.Fatalf("новая панель не отдаётся: %d %s", resp.StatusCode, body[:min(120, len(body))])
	}
	resp2, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	body2, _ := io.ReadAll(resp2.Body)
	if !strings.Contains(string(body2), "mawg") {
		t.Fatalf("старая панель пропала с /")
	}
}
