package web

import (
	"bytes"
	"compress/zlib"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mawg/internal/platform/fake"
	"mawg/internal/premium"
	"mawg/internal/rotator"
	"mawg/internal/store"
)

// сквозняк капчи Amnezia Free: создание требует человеко-проверку, повтор с
// решением выдаёт конфиг и создаёт выключенный пул
func TestFreePoolCaptchaHTTPFlow(t *testing.T) {
	st, _ := store.Open(t.TempDir())
	fb := premiumOpenWrtBackend{fake.New()}
	engine := rotator.New(st, fb, nil)

	rsaKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	solved := false
	var seen map[string]any
	gateway := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var env map[string][]byte
		json.NewDecoder(r.Body).Decode(&env)
		session, _ := rsa.DecryptPKCS1v15(rand.Reader, rsaKey, env["key_payload"])
		var keys map[string][]byte
		json.Unmarshal(session, &keys)
		block, _ := aes.NewCipher(keys["aes_key"])
		raw := env["api_payload"]
		cipher.NewCBCDecrypter(block, keys["aes_iv"][:16]).CryptBlocks(raw, raw)
		raw = raw[:len(raw)-int(raw[len(raw)-1])]
		var payload map[string]any
		json.Unmarshal(raw, &payload)
		seen = payload
		response := map[string]any{"http_status": 200}
		switch r.URL.Path {
		case "/v1/services":
			response["user_country_code"] = "default"
			response["services"] = []any{map[string]any{"service_type": "amnezia-free", "service_protocol": "awg", "is_available": true}}
		case "/v1/config":
			if payload["captcha_id"] == "cap-1" && payload["captcha_solution"] == "1234" {
				solved = true
				private, _ := payload["public_key"].(string)
				native := "[Interface]\nPrivateKey = $WIREGUARD_CLIENT_PRIVATE_KEY\nAddress = 10.9.0.2/32\nJc = 3\nH1 = 1\nI2 = <b 0x010203>\n[Peer]\nPublicKey = " + publicOfFree(t) + "\nEndpoint = free.example.net:51820\nAllowedIPs = 0.0.0.0/0\n"
				_ = private
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
			} else {
				response["http_status"] = 402
				response["captcha_id"] = "cap-1"
				response["captcha_image"] = base64.StdEncoding.EncodeToString([]byte("synthetic-png"))
				response["hint"] = "введите цифры"
			}
		default:
			t.Error("неожиданная операция: " + r.URL.Path)
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
	post := func(path string, v any) (int, string) {
		t.Helper()
		body, _ := json.Marshal(v)
		resp, err := http.Post(server.URL+path, "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var sb strings.Builder
		io.Copy(&sb, resp.Body)
		return resp.StatusCode, sb.String()
	}

	code, body := post("/api/v1/pools/amnezia-free", map[string]string{"name": "free"})
	if code != 200 || !strings.Contains(body, `"captchaRequired":true`) || !strings.Contains(body, `"id":"cap-1"`) || !strings.Contains(body, "c3ludGhldGljLXBuZw==") {
		t.Fatalf("первый ответ не капча: %d %s", code, body)
	}

	code, body = post("/api/v1/pools/amnezia-free/captcha", map[string]string{"name": "free", "captchaId": "cap-1", "captchaSolution": "1234"})
	if code != 200 || !strings.Contains(body, `"pool":"free"`) {
		t.Fatalf("ответ с решением: %d %s", code, body)
	}
	if !solved {
		t.Fatal("gateway не получил верное решение")
	}
	if seen["captcha_id"] != "cap-1" || seen["captcha_solution"] != "1234" {
		t.Fatalf("payload повтора: %v %v", seen["captcha_id"], seen["captcha_solution"])
	}
	p, ok := st.Pool("free")
	if !ok || !p.Free || !p.Disabled || len(p.Configs) != 1 {
		t.Fatalf("пул Free не создан выключенным: %+v", p)
	}

	// без решения - отказ, а не молчание
	code, _ = post("/api/v1/pools/amnezia-free/captcha", map[string]string{"name": "free", "captchaId": "", "captchaSolution": ""})
	if code == 200 {
		t.Fatal("пустой ответ капчи должен отклоняться")
	}
}

// publicOfFree - публичный ключ по стобы; конфиг подменяет
// $WIREGUARD_CLIENT_PRIVATE_KEY приватным из free.json, поэтому для
// валидации достаточно любого согласуемого ключа - здесь просто
// пере-выводим из сохранённого (тесту хватает эхо публичного ключа).
func publicOfFree(t *testing.T) string {
	t.Helper()
	b := make([]byte, 32)
	rand.Read(b)
	key, err := ecdh.X25519().NewPrivateKey(b)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(key.PublicKey().Bytes())
}
