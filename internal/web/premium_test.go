package web

import (
	"bytes"
	"compress/zlib"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"mawg/internal/platform/fake"
	"mawg/internal/premium"
	"mawg/internal/rotator"
	"mawg/internal/store"
)

type premiumOpenWrtBackend struct{ *fake.Fake }

func (premiumOpenWrtBackend) Name() string { return store.PlatformOpenwrt }

func TestPremiumRequiresAuthentication(t *testing.T) {
	ts, pass := newAuthedServer(t)
	for _, path := range []string{"/api/v1/pools/amnezia-free", "/api/v1/pools/from-source", "/api/v1/pools/premium/premium", "/api/v1/pools/premium/premium/country"} {
		resp, err := http.Post(ts.URL+path, "application/json", strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s: want 401, got %d", path, resp.StatusCode)
		}
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	resp, err := client.Post(ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"login":"admin","password":"`+pass+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatal("login failed")
	}
	for _, path := range []string{"/api/v1/pools/amnezia-free", "/api/v1/pools/from-source", "/api/v1/pools/premium/premium", "/api/v1/pools/premium/premium/country"} {
		resp, err := client.Post(ts.URL+path, "application/json", strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: authenticated endpoint not registered: %d", path, resp.StatusCode)
		}
	}
}

func webPremiumLink(v any) string {
	raw, _ := json.Marshal(v)
	var b bytes.Buffer
	w := zlib.NewWriter(&b)
	w.Write(raw)
	w.Close()
	prefix := make([]byte, 4)
	binary.BigEndian.PutUint32(prefix, uint32(len(raw)))
	return "vpn://" + base64.RawURLEncoding.EncodeToString(append(prefix, b.Bytes()...))
}

func TestPremiumHTTPImportSwitchAndSanitizedStatus(t *testing.T) {
	st, _ := store.Open(t.TempDir())
	fb := premiumOpenWrtBackend{fake.New()}
	engine := rotator.New(st, fb, nil)
	rsaKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	devices := map[string]string{}
	gateway := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var env map[string][]byte
		json.NewDecoder(r.Body).Decode(&env)
		session, err := rsa.DecryptPKCS1v15(rand.Reader, rsaKey, env["key_payload"])
		if err != nil {
			t.Error(err)
			return
		}
		var keys map[string][]byte
		json.Unmarshal(session, &keys)
		block, _ := aes.NewCipher(keys["aes_key"])
		raw := env["api_payload"]
		cipher.NewCBCDecrypter(block, keys["aes_iv"][:16]).CryptBlocks(raw, raw)
		var payload map[string]any
		json.Unmarshal(raw[:len(raw)-int(raw[len(raw)-1])], &payload)
		response := map[string]any{"http_status": 200}
		if r.URL.Path == "/v1/account_info" {
			response["available_countries"] = []any{map[string]any{"server_country_code": "NL", "server_country_name": "Netherlands", "available_protocols": []string{"awg"}}, map[string]any{"server_country_code": "RO", "available_protocols": []string{"awg"}}}
			issued := []any{}
			for id, country := range devices {
				issued = append(issued, map[string]string{"installation_uuid": id, "source_type": "gateway_account", "server_country_code": country})
			}
			response["issued_configs"] = issued
		} else if r.URL.Path == "/v1/config" {
			country := payload["server_country_code"].(string)
			devices[payload["installation_uuid"].(string)] = country
			native := strings.ReplaceAll(testConf, "QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE=", "$WIREGUARD_CLIENT_PRIVATE_KEY")
			native = strings.Replace(native, "Address = 10.2.0.2/32", "Address = 10.2.0.2/32\nJc = 3", 1)
			native = strings.ReplaceAll(native, "at1.example.net", strings.ToLower(country)+".example.net")
			last, _ := json.Marshal(map[string]string{"config": native})
			response["config"] = webPremiumLink(map[string]any{"containers": []any{map[string]any{"container": "amnezia-awg", "awg": map[string]string{"last_config": string(last)}}}}) + "\n"
		} else {
			t.Error("unexpected API operation")
		}
		raw, _ = json.Marshal(response)
		n := aes.BlockSize - len(raw)%aes.BlockSize
		raw = append(raw, bytes.Repeat([]byte{byte(n)}, n)...)
		cipher.NewCBCEncrypter(block, keys["aes_iv"][:16]).CryptBlocks(raw, raw)
		w.Write(raw)
	}))
	defer gateway.Close()
	engine.PremiumClient = &premium.Client{BaseURL: gateway.URL, HTTP: gateway.Client(), Key: &rsaKey.PublicKey}
	server := httptest.NewServer(New(st, engine, fb, nil, "test", nil).Handler())
	defer server.Close()
	request := func(method, path string, v any) (int, string) {
		t.Helper()
		body, _ := json.Marshal(v)
		req, _ := http.NewRequest(method, server.URL+path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(raw)
	}
	link := webPremiumLink(map[string]any{"config_version": 2, "api_config": map[string]string{"service_type": "amnezia-premium", "service_protocol": "awg"}, "auth_data": map[string]string{"api_key": "synthetic-web-secret"}})
	if done := os.Getenv("MAWG_PREMIUM_BROWSER_DONE"); done != "" {
		t.Logf("Premium browser fixture: %s", server.URL)
		t.Logf("Synthetic browser link: %s", link)
		deadline := time.Now().Add(180 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(done); err == nil {
				p, ok := st.Pool("premium-browser")
				if !ok || !p.Premium || len(p.Configs) != 1 || len(devices) != 1 || st.PremiumView(p.Name).Country != "RO" || st.PremiumView(p.Name).Pending {
					t.Fatal("browser did not complete managed single-device country switching")
				}
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("Premium browser acceptance timed out")
	}
	code, body := request("POST", "/api/v1/pools/from-source", map[string]any{"name": "premium", "source": link, "exchange": true, "openwrtProto": "wireguard"})
	if code != 200 || !strings.Contains(body, `"premium":true`) {
		t.Fatalf("Premium source import failed: HTTP %d %s", code, body)
	}
	p, ok := st.Pool("premium")
	if !ok || !p.Premium || p.Settings.OpenwrtProto != "amneziawg" {
		t.Fatal("source import did not create a managed AWG pool")
	}
	if p.Settings.Source != "" || len(p.Configs) != 0 || len(devices) != 0 || strings.Contains(body, "synthetic-web-secret") {
		t.Fatal("source import exposed the key or issued a config before country selection")
	}
	code, body = request("POST", "/api/v1/pools/from-source", map[string]any{"name": "second-premium", "source": link, "exchange": true})
	if code != 400 || len(st.Pools()) != 1 || len(devices) != 0 {
		t.Fatal("source retry created a second Premium pool or device")
	}
	code, body = request("POST", "/api/v1/pools/premium/premium", map[string]string{"key": link})
	if code != 200 || strings.Contains(body, "synthetic-web-secret") {
		t.Fatalf("import failed: HTTP %d %s", code, body)
	}
	uuid := st.Premium().UUID
	for _, country := range []string{"NL", "RO"} {
		code, body = request("POST", "/api/v1/pools/premium/premium/country", map[string]string{"country": country})
		if code != 200 || st.PremiumView("premium").Country != country {
			t.Fatalf("switch failed: %d %s", code, body)
		}
	}
	code, body = request("GET", "/api/v1/status", nil)
	if code != 200 || !strings.Contains(body, `"country":"RO"`) || !strings.Contains(body, `"countries"`) {
		t.Fatal("status missing country selector data")
	}
	for _, secret := range []string{"synthetic-web-secret", uuid, "installationUUID", "pendingPrivate", "api_key", "PrivateKey"} {
		if strings.Contains(body, secret) {
			t.Fatal("status leaked subscription secret")
		}
	}
	code, body = request("POST", "/api/v1/pools/premium/premium", map[string]string{"key": "vpn://synthetic-web-secret!"})
	if code != 400 || strings.Contains(body, "synthetic-web-secret") {
		t.Fatal("invalid import leaked key")
	}
	code, body = request("GET", "/api/v1/pools/premium/premium", nil)
	if code != 200 || !strings.Contains(body, `"imported":true`) || strings.Contains(body, "synthetic-web-secret") {
		t.Fatal("Premium view failed")
	}
	code, body = request("GET", "/app/panel.js", nil)
	for _, snippet := range []string{"premium-key", "premium-country", "/premium/country", "upgradeSelect(q('.premium-country'))", "/pools/amnezia-free", "$('#pModeFree').onclick"} {
		if code != 200 || !strings.Contains(body, snippet) {
			t.Fatalf("missing active Vue script behavior: %s", snippet)
		}
	}
	code, body = request("GET", "/app/app.js", nil)
	for _, snippet := range []string{"pModeFree", "pFreeHint", "pSourceRow"} {
		if code != 200 || !strings.Contains(body, snippet) {
			t.Fatalf("missing active Vue template behavior: %s", snippet)
		}
	}
	code, body = request("GET", "/", nil)
	for _, snippet := range []string{"premium-key", "premium-country", "/premium/country", "type=\"password\"", "keyInput.value = ''", "!isEng && !p.disabled && !premium.imported", "upgradeSelect(q('.premium-country'))"} {
		if code != 200 || !strings.Contains(body, snippet) {
			t.Fatalf("missing UI import/switch behavior: %s", snippet)
		}
	}
}
