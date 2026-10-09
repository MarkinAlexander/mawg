package premium

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	_ "embed"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

//go:embed gateway-public.pem
var gatewayPublic []byte

var countryCode = regexp.MustCompile(`^[A-Za-z]{2}(?:-[A-Za-z0-9]{1,8})?$`)

type Country struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type Client struct {
	BaseURL string
	HTTP    *http.Client
	Key     *rsa.PublicKey
}

func NewClient() *Client {
	block, _ := pem.Decode(gatewayPublic)
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		panic("invalid embedded gateway public key")
	}
	return &Client{BaseURL: "https://gw.amnezia.org", HTTP: &http.Client{Timeout: 25 * time.Second}, Key: key.(*rsa.PublicKey)}
}

func UUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// Captcha - испытание gateway Amnezia Free: картинка (base64 PNG) и
// подсказка. Решение вводит человек в панели, запрос повторяется с
// captcha_id + captcha_solution.
type Captcha struct {
	ID    string `json:"id"`
	Image string `json:"image"`
	Hint  string `json:"hint,omitempty"`
}

// CaptchaError - запрос не выполнен, ждём решение капчи от человека.
type CaptchaError struct {
	Captcha Captcha
}

func (e *CaptchaError) Error() string {
	return "gateway требует решение капчи"
}

// CaptchaAnswer - ответ человека на испытание.
type CaptchaAnswer struct {
	ID       string
	Solution string
}

// NormalizeSolution - только цифры; полноширинные ０-９ приводятся к
// ascii, как официальный клиент (PR #2508 amnezia-client).
func NormalizeSolution(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 0xFF10 && r <= 0xFF19:
			b.WriteRune(r - 0xFF10 + '0')
		}
	}
	if b.Len() > 0 {
		return b.String()
	}
	return strings.TrimSpace(s)
}

func payload(key, id, country string) map[string]any {
	v := map[string]any{"os_version": "linux", "app_version": "5.0.3.0", "cli_name": "AmneziaVPN", "distribution": "github", "app_language": "ru", "installation_uuid": id, "service_type": "amnezia-premium", "auth_data": map[string]string{"api_key": key}}
	if country != "" {
		v["user_country_code"] = country
	}
	return v
}

func (c *Client) post(ctx context.Context, operation string, v map[string]any) ([]byte, error) {
	bad := errors.New("ответ gateway не расшифровывается")
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("gateway работает только по HTTPS")
	}
	if operation != "account_info" && operation != "config" && operation != "services" {
		return nil, errors.New("неизвестная операция gateway")
	}
	key, iv, salt := make([]byte, 32), make([]byte, 32), make([]byte, 8)
	for _, b := range [][]byte{key, iv, salt} {
		if _, err := rand.Read(b); err != nil {
			return nil, errors.New("ошибка шифрования запроса к gateway")
		}
	}
	session, _ := json.Marshal(map[string][]byte{"aes_key": key, "aes_iv": iv, "aes_salt": salt})
	wrapped, err := rsa.EncryptPKCS1v15(rand.Reader, c.Key, session)
	if err != nil {
		return nil, errors.New("ошибка шифрования запроса к gateway")
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, errors.New("некорректный запрос к gateway")
	}
	n := aes.BlockSize - len(raw)%aes.BlockSize
	raw = append(raw, bytes.Repeat([]byte{byte(n)}, n)...)
	block, _ := aes.NewCipher(key)
	cipher.NewCBCEncrypter(block, iv[:16]).CryptBlocks(raw, raw)
	env, _ := json.Marshal(map[string][]byte{"key_payload": wrapped, "api_payload": raw})
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(c.BaseURL, "/")+"/v1/"+operation, bytes.NewReader(env))
	if err != nil {
		return nil, errors.New("некорректный запрос к gateway")
	}
	requestID, err := UUID()
	if err != nil {
		return nil, errors.New("ошибка шифрования запроса к gateway")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Client-Request-ID", requestID)
	client := *c.HTTP
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		return nil, errors.New("gateway недоступен (сеть, TLS или таймаут)")
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return nil, errors.New("gateway попытался перенаправить запрос - отказано")
	}
	encrypted, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil || len(encrypted) == 0 || len(encrypted) > maxBytes || len(encrypted)%aes.BlockSize != 0 {
		return nil, bad
	}
	cipher.NewCBCDecrypter(block, iv[:16]).CryptBlocks(encrypted, encrypted)
	n = int(encrypted[len(encrypted)-1])
	if n == 0 || n > aes.BlockSize {
		return nil, bad
	}
	for _, b := range encrypted[len(encrypted)-n:] {
		if int(b) != n {
			return nil, bad
		}
	}
	raw = encrypted[:len(encrypted)-n]
	var status struct {
		HTTP         *int   `json:"http_status"`
		CaptchaID    string `json:"captcha_id"`
		CaptchaImage string `json:"captcha_image"`
		Hint         string `json:"hint"`
		Message      string `json:"message"`
	}
	if json.Unmarshal(raw, &status) != nil || len(raw) == 0 || raw[0] != '{' {
		return nil, bad
	}
	code := response.StatusCode
	if status.HTTP != nil && (code >= 200 && code < 300 || *status.HTTP >= 300) {
		code = *status.HTTP
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || code < 200 || code >= 300 {
		msg := strings.ToLower(status.Message)
		if v["service_type"] == "amnezia-free" && code == 402 && (status.CaptchaID != "" || status.CaptchaImage != "" ||
			strings.Contains(msg, "captcha") || strings.Contains(msg, "rate_limit_exceeded")) {
			// gateway требует человеко-проверку: картинка base64 PNG,
			// решение возвращается повтором запроса с captcha_id +
			// captcha_solution (протокол официального клиента, PR #2508)
			return nil, &CaptchaError{Captcha: Captcha{
				ID: status.CaptchaID, Image: status.CaptchaImage, Hint: status.Hint,
			}}
		}
		switch code {
		case 409:
			return nil, errors.New("достигнут лимит устройств Premium (HTTP 409) - освободите слот в приложении Амнезии")
		case 429:
			return nil, errors.New("слишком много запросов к gateway (HTTP 429), повторите позже")
		case 408:
			return nil, errors.New("gateway не ответил вовремя (HTTP 408), повторите позже")
		case 402, 422:
			return nil, fmt.Errorf("подписка Premium недоступна (HTTP %d)", code)
		default:
			return nil, fmt.Errorf("gateway отклонил запрос (HTTP %d)", code)
		}
	}
	return raw, nil
}

func (c *Client) Countries(ctx context.Context, key, id, userCountry string) ([]Country, error) {
	v := payload(key, id, userCountry)
	v["cli_version"] = "5.0.3.0"
	v["subscription_status"] = "active"
	raw, err := c.post(ctx, "account_info", v)
	if err != nil {
		return nil, err
	}
	var response struct {
		Countries []struct {
			Code      string   `json:"server_country_code"`
			Name      string   `json:"server_country_name"`
			Protocols []string `json:"available_protocols"`
		} `json:"available_countries"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return nil, errors.New("gateway вернул неразборчивый список стран")
	}
	out := []Country{}
	seen := map[string]bool{}
	for _, v := range response.Countries {
		supported := false
		for _, p := range v.Protocols {
			if p == "awg" {
				supported = true
			}
		}
		if !supported || !countryCode.MatchString(v.Code) || seen[v.Code] {
			continue
		}
		seen[v.Code] = true
		name := v.Name
		if len(name) > 128 || strings.Contains(name, key) {
			name = v.Code
		}
		if name == "" {
			name = v.Code
		}
		out = append(out, Country{Code: v.Code, Name: name})
	}
	if len(out) == 0 {
		return nil, errors.New("в подписке Premium нет стран с AWG")
	}
	return out, nil
}

func (c *Client) VerifyDevice(ctx context.Context, key, id, userCountry, country string) error {
	v := payload(key, id, userCountry)
	v["cli_version"], v["subscription_status"] = "5.0.3.0", "active"
	raw, err := c.post(ctx, "account_info", v)
	if err != nil {
		return err
	}
	var response struct {
		Issued []struct {
			UUID    string `json:"installation_uuid"`
			Source  string `json:"source_type"`
			Country string `json:"server_country_code"`
		} `json:"issued_configs"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return errors.New("gateway вернул неразборчивый ответ регистрации устройства")
	}
	count := 0
	for _, device := range response.Issued {
		if device.UUID == id && device.Source == "gateway_account" {
			if device.Country != country {
				return errors.New("gateway зарегистрировал не ту страну, что запрошена")
			}
			count++
		}
	}
	if count != 1 {
		return errors.New("не удалось подтвердить регистрацию роутера у gateway")
	}
	return nil
}
