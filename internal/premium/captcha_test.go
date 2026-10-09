package premium

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
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// captchaGateway - gateway, требующий капчу на первый config и принимающий
// повтор только с правильным captcha_id + captcha_solution; неверный ответ
// возвращает invalid_captcha со свежей капчей.
func captchaGateway(t *testing.T, wantSolution string, gotPayloads *[]map[string]any) (*httptest.Server, *Client) {
	t.Helper()
	rsaKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	captchaNo := 0
	mkCaptcha := func() (string, string) {
		captchaNo++
		id := "cap-synthetic-" + string(rune('a'+captchaNo))
		return id, base64.StdEncoding.EncodeToString([]byte("synthetic-png-" + id))
	}
	goodID, goodImg := mkCaptcha()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var env map[string][]byte
		json.NewDecoder(r.Body).Decode(&env)
		session, _ := rsa.DecryptPKCS1v15(rand.Reader, rsaKey, env["key_payload"])
		var keys map[string][]byte
		json.Unmarshal(session, &keys)
		block, _ := aes.NewCipher(keys["aes_key"])
		raw := env["api_payload"]
		cipher.NewCBCDecrypter(block, keys["aes_iv"][:16]).CryptBlocks(raw, raw)
		raw = raw[:len(raw)-int(raw[len(raw)-1])]
		var v map[string]any
		json.Unmarshal(raw, &v)
		if gotPayloads != nil {
			*gotPayloads = append(*gotPayloads, v)
		}
		response := map[string]any{"http_status": 200}
		switch r.URL.Path {
		case "/v1/services":
			response["user_country_code"] = "default"
			response["services"] = []any{map[string]any{"service_type": "amnezia-free", "service_protocol": "awg", "is_available": true}}
		case "/v1/config":
			solution, _ := v["captcha_solution"].(string)
			if v["captcha_id"] == nil {
				response["http_status"] = 402
				response["captcha_id"] = goodID
				response["captcha_image"] = goodImg
				response["hint"] = "введите цифры"
			} else if v["captcha_id"] == goodID && solution == wantSolution {
				priv, _ := base64.StdEncoding.DecodeString(freePrivateForTest)
				key, _ := ecdh.X25519().NewPrivateKey(priv)
				public := base64.StdEncoding.EncodeToString(key.PublicKey().Bytes())
				native := "[Interface]\nPrivateKey = $WIREGUARD_CLIENT_PRIVATE_KEY\nAddress = 10.9.0.2/32\nJc = 3\nH1 = 1\nI2 = <b 0x010203>\n[Peer]\nPublicKey = " + public + "\nEndpoint = free.example.net:51820\nAllowedIPs = 0.0.0.0/0\n"
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
				// неверное решение: invalid_captcha + свежая капча
				newID, newImg := mkCaptcha()
				goodID, goodImg = newID, newImg
				response["http_status"] = 402
				response["message"] = "invalid_captcha"
				response["captcha_id"] = newID
				response["captcha_image"] = newImg
			}
		default:
			t.Error("неожиданная операция: " + r.URL.Path)
		}
		raw, _ = json.Marshal(response)
		n := aes.BlockSize - len(raw)%aes.BlockSize
		raw = append(raw, bytes.Repeat([]byte{byte(n)}, n)...)
		cipher.NewCBCEncrypter(block, keys["aes_iv"][:16]).CryptBlocks(raw, raw)
		w.WriteHeader(200)
		w.Write(raw)
	}))
	t.Cleanup(srv.Close)
	return srv, &Client{BaseURL: srv.URL, HTTP: srv.Client(), Key: &rsaKey.PublicKey}
}

const freePrivateForTest = "uJd2xK5t0mFvqWz+YbN8cLrA3eHh7oPqQ1wZsX4dC6k="

func TestFreeConfigCaptchaFlow(t *testing.T) {
	priv := func() string {
		b := make([]byte, 32)
		rand.Read(b)
		return base64.StdEncoding.EncodeToString(b)
	}()
	var payloads []map[string]any
	srv, client := captchaGateway(t, "1234", &payloads)
	_ = srv
	private := priv

	// первый запрос - испытание капчей с картинкой и подсказкой
	_, _, err := client.FreeConfig(context.Background(), "uuid-1", "default", private, nil)
	var cap *CaptchaError
	if !errors.As(err, &cap) {
		t.Fatalf("ожидалась капча: %v", err)
	}
	if cap.Captcha.ID == "" || cap.Captcha.Image == "" || cap.Captcha.Hint != "введите цифры" {
		t.Fatalf("испытание: %+v", cap.Captcha)
	}
	firstID := cap.Captcha.ID

	// неверное решение - новая капча
	_, _, err = client.FreeConfig(context.Background(), "uuid-1", "default", private, &CaptchaAnswer{ID: firstID, Solution: "9999"})
	if !errors.As(err, &cap) || cap.Captcha.ID == firstID {
		t.Fatalf("ожидалась свежая капча после неверного ответа: %v", err)
	}
	secondID := cap.Captcha.ID

	// верное решение (полноширинные цифры нормализуются) - конфиг выдан
	data, _, err := client.FreeConfig(context.Background(), "uuid-1", "default", private, &CaptchaAnswer{ID: secondID, Solution: "１２３４"})
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("пустой конфиг")
	}
	// в повторе ушли captcha_id и нормализованное решение
	last := payloads[len(payloads)-1]
	if last["captcha_id"] != secondID || last["captcha_solution"] != "1234" {
		t.Fatalf("повтор: captcha_id=%v solution=%v", last["captcha_id"], last["captcha_solution"])
	}
}

func TestNormalizeSolution(t *testing.T) {
	if got := NormalizeSolution("１２３４"); got != "1234" {
		t.Fatalf("полноширинные: %q", got)
	}
	if got := NormalizeSolution(" 12 34 "); got != "1234" {
		t.Fatalf("пробелы: %q", got)
	}
	if got := NormalizeSolution("abc"); got != "abc" {
		t.Fatalf("не-цифры как есть: %q", got)
	}
}
