package links

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const testAPIKey = "FAKE.API.KEY.1234567890"

type fakeGateway struct {
	mu         sync.Mutex
	lastAPI    map[string]any
	lastKeyPal map[string]string
	priv       *rsa.PrivateKey
}

func (f *fakeGateway) pem() string {
	der, err := x509.MarshalPKIXPublicKey(&f.priv.PublicKey)
	if err != nil {
		panic(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// handler расшифровывает конверт своим приватным ключом, проверяет api_payload
// и отвечает зашифрованным серверным конфигом (как реальный gateway).
func (f *fakeGateway) handler(t *testing.T, serverConfig func(api map[string]any) any) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/config" {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, `{"http_status":404,"message":"no route"}`)
			return
		}
		var env exchangeEnvelope
		if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
			t.Errorf("тело не конверт: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		encKey, err := base64.StdEncoding.DecodeString(env.KeyPayload)
		if err != nil {
			t.Errorf("key_payload не base64: %v", err)
			return
		}
		keyPlain, err := rsa.DecryptPKCS1v15(rand.Reader, f.priv, encKey)
		if err != nil {
			t.Errorf("key_payload не расшифровывается RSA: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var pal map[string]string
		if err := json.Unmarshal(keyPlain, &pal); err != nil {
			t.Errorf("key_payload не JSON: %v", err)
			return
		}
		key, err := base64.StdEncoding.DecodeString(pal["aes_key"])
		if err != nil || len(key) != 32 {
			t.Errorf("aes_key: %v (len %d)", err, len(key))
			return
		}
		iv, err := base64.StdEncoding.DecodeString(pal["aes_iv"])
		if err != nil || len(iv) != 32 {
			t.Errorf("aes_iv: %v", err)
			return
		}
		if _, err := base64.StdEncoding.DecodeString(pal["aes_salt"]); err != nil {
			t.Errorf("aes_salt: %v", err)
			return
		}
		encAPI, err := base64.StdEncoding.DecodeString(env.APIPayload)
		if err != nil {
			t.Errorf("api_payload не base64: %v", err)
			return
		}
		apiPlain, ok := decryptAesCbcPkcs7(encAPI, key, iv[:16])
		if !ok {
			t.Error("api_payload не расшифровывается AES")
			return
		}
		var api map[string]any
		if err := json.Unmarshal(apiPlain, &api); err != nil {
			t.Errorf("api_payload не JSON: %v", err)
			return
		}
		f.mu.Lock()
		f.lastAPI, f.lastKeyPal = api, pal
		f.mu.Unlock()
		srvJSON, err := json.Marshal(serverConfig(api))
		if err != nil {
			t.Fatal(err)
		}
		var zbuf bytes.Buffer
		zw := zlib.NewWriter(&zbuf)
		if _, err := zw.Write(srvJSON); err != nil {
			t.Fatal(err)
		}
		zw.Close()
		envelope := map[string]string{
			"config": "vpn://" + base64.RawURLEncoding.EncodeToString(zbuf.Bytes()),
		}
		respBody, err := json.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
		encResp, err := encryptAesCbcPkcs7(respBody, key, iv[:16])
		if err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(encResp)
	})
}

func vpnKeyForTests(t *testing.T, protocol string) string {
	t.Helper()
	service := "amnezia-premium"
	if protocol == "awg" {
		service = "amnezia-free"
	}
	return vpnServiceKeyForTests(t, service, protocol)
}

func vpnServiceKeyForTests(t *testing.T, service, protocol string) string {
	t.Helper()
	doc := map[string]any{
		"name":           "тест",
		"config_version": 2,
		"api_config":     map[string]string{"service_type": service, "service_protocol": protocol, "user_country_code": "RU"},
		"auth_data":      map[string]string{"api_key": testAPIKey},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		t.Fatal(err)
	}
	zw.Close()
	return "vpn://" + base64.RawURLEncoding.EncodeToString(buf.Bytes())
}

func testServerConfig(api map[string]any, protocol string) any {
	pubKey, _ := api["public_key"].(string)
	if protocol == "awg" {
		conf := "[Interface]\nPrivateKey = $WIREGUARD_CLIENT_PRIVATE_KEY\nAddress = 10.8.1.2/32\nDNS = 1.1.1.1\n" +
			"[Peer]\nPublicKey = " + pubKey + "\nEndpoint = 203.0.113.50:31283\nAllowedIPs = 0.0.0.0/0\n"
		lastConf := map[string]any{"config": conf}
		lcJSON, _ := json.Marshal(lastConf)
		return map[string]any{
			"config_version": 2,
			"api_config": map[string]any{
				"service_type": "amnezia-premium", "service_protocol": "awg",
				"server_country_code": "NL", "server_country_name": "Нидерланды",
				"active_device_count": 2, "max_device_count": 5, "issued_configs": 3,
				"available_countries": []any{
					map[string]any{"server_country_code": "NL", "server_country_name": "Нидерланды", "available_protocols": []string{"awg", "vless"}},
					map[string]any{"server_country_code": "DE", "server_country_name": "Германия", "available_protocols": []string{"awg"}},
				},
			},
			"containers": []any{map[string]any{
				"container": "amnezia-awg",
				"awg":       map[string]any{"last_config": string(lcJSON)},
			}},
		}
	}
	return map[string]any{
		"config_version": 2,
		"api_config": map[string]any{
			"service_type": "amnezia-premium", "service_protocol": "vless",
			"server_country_code": "NL",
		},
		"containers": []any{map[string]any{
			"container": "amnezia-xray",
			"vless":     map[string]any{"last_config": "vless://" + pubKey + "@203.0.113.51:443?type=tcp&security=reality#amnezia-vless"},
		}},
	}
}

func TestPremiumAWGCannotBypassManagedLifecycle(t *testing.T) {
	key := vpnServiceKeyForTests(t, "amnezia-premium", "awg")
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(503) }))
	defer ts.Close()
	_, err := ExchangeAmneziaKey(context.Background(), key, ExchangeOptions{GatewayURL: ts.URL})
	if err == nil || !strings.Contains(err.Error(), "Premium") || calls != 0 {
		t.Fatalf("unmanaged Premium exchange: calls=%d err=%v", calls, err)
	}
}

func TestExchangeAmneziaKeyAWG(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	fg := &fakeGateway{priv: priv}
	ts := httptest.NewServer(fg.handler(t, func(api map[string]any) any { return testServerConfig(api, "awg") }))
	defer ts.Close()

	rawKey := vpnKeyForTests(t, "awg")
	res, err := ExchangeAmneziaKey(context.Background(), rawKey, ExchangeOptions{
		GatewayURL: ts.URL, PublicKeyPEM: fg.pem(), Version: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Nodes) != 1 || res.Nodes[0].Type != "amneziawg" && res.Nodes[0].Type != "wireguard" {
		t.Fatalf("узлы: %+v", res.Nodes)
	}
	n := res.Nodes[0]
	if n.PrivateKey == "" || strings.Contains(n.PrivateKey, "$WIREGUARD") {
		t.Fatalf("приватный ключ не подставлен: %q", n.PrivateKey)
	}
	if n.Host != "203.0.113.50" || n.Port != 31283 {
		t.Fatalf("endpoint: %s:%d", n.Host, n.Port)
	}
	if res.ServerCountry != "NL" || res.ServerCountryName != "Нидерланды" {
		t.Fatalf("локация: %s/%s", res.ServerCountry, res.ServerCountryName)
	}
	if res.ActiveDevices != 2 || res.MaxDevices != 5 || res.IssuedConfigs != 3 {
		t.Fatalf("счётчики: %d/%d issued %d", res.ActiveDevices, res.MaxDevices, res.IssuedConfigs)
	}
	if len(res.AvailableCountries) != 2 || res.AvailableCountries[0].Code != "NL" || res.AvailableCountries[1].Code != "DE" {
		t.Fatalf("локации: %+v", res.AvailableCountries)
	}
	if res.AvailableCountries[0].Protocols == nil || len(res.AvailableCountries[0].Protocols) != 2 {
		t.Fatalf("протоколы локации: %+v", res.AvailableCountries[0])
	}
	fg.mu.Lock()
	defer fg.mu.Unlock()
	if fg.lastAPI["auth_data"] == nil || fg.lastAPI["service_protocol"] != "awg" {
		t.Fatalf("api_payload: %v", fg.lastAPI)
	}
	if fg.lastAPI["public_key"] == "" {
		t.Fatal("в api_payload нет public_key клиента")
	}
}

func TestExchangeAmneziaKeyReusesPrivateKey(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	fg := &fakeGateway{priv: priv}
	ts := httptest.NewServer(fg.handler(t, func(api map[string]any) any { return testServerConfig(api, "awg") }))
	defer ts.Close()
	rawKey := vpnKeyForTests(t, "awg")

	first, err := ExchangeAmneziaKey(context.Background(), rawKey, ExchangeOptions{GatewayURL: ts.URL, PublicKeyPEM: fg.pem()})
	if err != nil {
		t.Fatal(err)
	}
	again, err := ExchangeAmneziaKey(context.Background(), rawKey, ExchangeOptions{GatewayURL: ts.URL, PublicKeyPEM: fg.pem(), ClientPrivKey: first.ClientPrivKey})
	if err != nil {
		t.Fatal(err)
	}
	if again.ClientPrivKey != first.ClientPrivKey {
		t.Fatal("повторный обмен должен использовать переданный приватный ключ")
	}
	fg.mu.Lock()
	defer fg.mu.Unlock()
	if fg.lastAPI["public_key"] == "" {
		t.Fatal("нет public_key")
	}
	if first.Nodes[0].PrivateKey != again.Nodes[0].PrivateKey {
		t.Fatal("приватные ключи в конфигах не совпали")
	}
}

func TestExchangeAmneziaKeyVless(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	fg := &fakeGateway{priv: priv}
	ts := httptest.NewServer(fg.handler(t, func(api map[string]any) any { return testServerConfig(api, "vless") }))
	defer ts.Close()
	res, err := ExchangeAmneziaKey(context.Background(), vpnKeyForTests(t, "vless"), ExchangeOptions{
		GatewayURL: ts.URL, PublicKeyPEM: fg.pem(), VlessUUID: "00000000-0000-4000-8000-000000000009",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Nodes) != 1 || res.Nodes[0].Type != "vless" {
		t.Fatalf("узлы: %+v", res.Nodes)
	}
	if res.Nodes[0].UUID != "00000000-0000-4000-8000-000000000009" {
		t.Fatalf("uuid узла: %q", res.Nodes[0].UUID)
	}
	if res.Nodes[0].Host != "203.0.113.51" {
		t.Fatalf("host: %q", res.Nodes[0].Host)
	}
}

func TestExchangeAmneziaKeyCountryCode(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	fg := &fakeGateway{priv: priv}
	ts := httptest.NewServer(fg.handler(t, func(api map[string]any) any { return testServerConfig(api, "awg") }))
	defer ts.Close()
	_, err := ExchangeAmneziaKey(context.Background(), vpnKeyForTests(t, "awg"), ExchangeOptions{
		GatewayURL: ts.URL, PublicKeyPEM: fg.pem(), ServerCountryCode: "DE",
	})
	if err != nil {
		t.Fatal(err)
	}
	fg.mu.Lock()
	defer fg.mu.Unlock()
	if fg.lastAPI["server_country_code"] != "DE" {
		t.Fatalf("server_country_code не передан: %v", fg.lastAPI["server_country_code"])
	}
}

func TestExchangeAmneziaKeyErrors(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><body>доступ запрещён</body></html>"))
	}))
	defer stub.Close()
	if _, err := ExchangeAmneziaKey(context.Background(), vpnKeyForTests(t, "awg"), ExchangeOptions{GatewayURL: stub.URL}); err == nil {
		t.Fatal("HTML-заглушка должна давать ошибку")
	} else if !strings.Contains(err.Error(), "заглушка") && !strings.Contains(err.Error(), "не распознан") {
		t.Fatalf("ожидалась честная ошибка заглушки: %v", err)
	}

	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"http_status":404,"message":"Account not found. Check your key"}`)
	}))
	defer errSrv.Close()
	_, err := ExchangeAmneziaKey(context.Background(), vpnKeyForTests(t, "awg"), ExchangeOptions{GatewayURL: errSrv.URL})
	if err == nil || !strings.Contains(err.Error(), "не найден") {
		t.Fatalf("ожидалась ошибка ключа: %v", err)
	}

	if _, err := ExchangeAmneziaKey(context.Background(), "vless://x@203.0.113.1:443#not-a-key", ExchangeOptions{}); err == nil {
		t.Fatal("не-ключ должен отклоняться")
	}
}
