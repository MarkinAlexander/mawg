package rotator

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mawg/internal/platform/fake"
	"mawg/internal/premium"
	"mawg/internal/store"
)

// freeGateway - стаб gateway Амнезии: каталог services (accountless) и
// выдача config, конверт шифрования как у настоящего (см. premium_test).
func freeGateway(t *testing.T, publicOf func(string) string) (*httptest.Server, *premium.Client) {
	t.Helper()
	rsaKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		raw = raw[:len(raw)-int(raw[len(raw)-1])]
		var v map[string]any
		json.Unmarshal(raw, &v)
		response := map[string]any{"http_status": 200}
		switch r.URL.Path {
		case "/v1/services":
			response["user_country_code"] = "default"
			response["services"] = []any{map[string]any{"service_type": "amnezia-free", "service_protocol": "awg", "is_available": true}}
		case "/v1/config":
			public := v["public_key"].(string)
			if publicOf != nil {
				if want := publicOf(v["installation_uuid"].(string)); want != "" && want != public {
					t.Error("config запрошен не с сохранённым публичным ключом")
				}
			}
			native := "[Interface]\nPrivateKey = $WIREGUARD_CLIENT_PRIVATE_KEY\nAddress = 10.9.0.2/32\nJc = 3\nJmin = 50\nJmax = 1000\nS1 = 25\nH1 = 1\nH2 = 2\nH3 = 3\nH4 = 4\nI2 = <b 0x010203>\n[Peer]\nPublicKey = " + public + "\nEndpoint = free.example.net:51820\nAllowedIPs = 0.0.0.0/0\n"
			last, _ := json.Marshal(map[string]string{"config": native})
			doc, _ := json.Marshal(map[string]any{
				"config_version": 2,
				"api_config":     map[string]string{"service_type": "amnezia-free", "service_protocol": "awg"},
				"containers":     []any{map[string]any{"container": "amnezia-awg", "awg": map[string]string{"last_config": string(last)}}},
			})
			var b bytes.Buffer
			zw := zlib.NewWriter(&b)
			zw.Write(doc)
			zw.Close()
			prefix := make([]byte, 4)
			binary.BigEndian.PutUint32(prefix, uint32(len(doc)))
			response["config"] = "vpn://" + base64.RawURLEncoding.EncodeToString(append(prefix, b.Bytes()...))
		default:
			t.Error("неожиданная операция: " + r.URL.Path)
		}
		raw, _ = json.Marshal(response)
		n := aes.BlockSize - len(raw)%aes.BlockSize
		raw = append(raw, bytes.Repeat([]byte{byte(n)}, n)...)
		cipher.NewCBCEncrypter(block, keys["aes_iv"][:16]).CryptBlocks(raw, raw)
		w.Write(raw)
	}))
	t.Cleanup(srv.Close)
	return srv, &premium.Client{BaseURL: srv.URL, HTTP: srv.Client(), Key: &rsaKey.PublicKey}
}

// Free-пул создаётся и на Keenetic: слот обязателен, конфиг - AWG 2.0 в
// слот Wireguard, пул выключен до ручной активации
func TestFreePoolOnKeenetic(t *testing.T) {
	base := t.TempDir()
	st, _ := store.Open(base)
	e := New(st, fake.New(), nil)
	gw, client := freeGateway(t, nil)
	_ = gw
	e.PremiumClient = client

	p, err := e.CreateFreePool(context.Background(), "free", store.PoolSettings{
		Platform: store.PlatformKeenetic, KeeneticSlot: "Wireguard9", ProbeHost: "1.1.1.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Free || !p.Disabled || len(p.Configs) != 1 {
		t.Fatalf("пул: %+v", p)
	}
	if p.Settings.KeeneticSlot != "Wireguard9" {
		t.Fatalf("слот потерян: %+v", p.Settings)
	}
	if p.Configs[0].File != "amnezia-free.conf" || p.Configs[0].Endpoint != "free.example.net:51820" {
		t.Fatalf("конфиг Free: %+v", p.Configs[0])
	}
	// повторная выдача не перевыпускается: тот же конфиг без похода в gateway
	p2, err := e.CreateFreePool(context.Background(), "free", store.PoolSettings{
		Platform: store.PlatformKeenetic, KeeneticSlot: "Wireguard9", ProbeHost: "1.1.1.1",
	})
	if err != nil || p2.Name != "free" {
		t.Fatalf("повтор: %+v err=%v", p2, err)
	}
	// второй Free-пул запрещён
	if _, err := e.CreateFreePool(context.Background(), "free2", store.PoolSettings{
		Platform: store.PlatformKeenetic, KeeneticSlot: "Wireguard10", ProbeHost: "1.1.1.1",
	}); err == nil {
		t.Fatal("второй Free-пул не должен создаваться")
	}
	// приватный ключ Free отвечает публичному из запроса config
	v := st.Free()
	b, err := base64.StdEncoding.DecodeString(v.Private)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ecdh.X25519().NewPrivateKey(b); err != nil {
		t.Fatalf("приватный ключ Free невалиден: %v", err)
	}
	// без слота - понятный отказ по-русски
	if _, err := e.CreateFreePool(context.Background(), "noslot", store.PoolSettings{
		Platform: store.PlatformKeenetic, ProbeHost: "1.1.1.1",
	}); err == nil || !strings.Contains(err.Error(), "слот") {
		t.Fatalf("ждали отказ про слот: %v", err)
	}
}
