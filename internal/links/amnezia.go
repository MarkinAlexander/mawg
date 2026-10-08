package links

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ProdGatewayPublicKey - публичный RSA-4096 ключ gateway Амнезии (PROD),
// извлечён из релизного бинаря AmneziaVPN 5.x и сверен живой расшифровкой
// их S3-списка прокси; это ПУБЛИЧНЫЙ ключ, секретом не является.
const ProdGatewayPublicKey = `-----BEGIN PUBLIC KEY-----
MIICIjANBgkqhkiG9w0BAQEFAAOCAg8AMIICCgKCAgEAj5mxl/4DL3Sk89ntxs5G
X3JawGQWIoq6rvNkOzNGuNgedNS2+pi6hZl3Izl1Io9om4KiUlMT6mgLO1hTr9q+
s7CYhlvroFA7ErucF+9L+7FCt0Igi0kIK/R2/vxd/2HaUrorn/aSvvutkYwbfxqW
SwtzE+RuBeDWGvEt937OW0oqYONPYv9E4T56Dz/EZ6v2t8ejAnKLbGD/GocMmipK
7etFSiSMAB2RmaztqTq4NleBepfO80XpYlW9pCSXuHcE8wxHczkzxsbyMAMsG/K3
vUQY6qPtohqqzSSBwa/8u2ptNHBeor7l7DdYXeR/Nqcc4z92VUkZ5lOVR4evkS5V
/wQqp5tnOJEj3NjUhEhXFoNEapbZd1bh6iQoUk7jC1TdvKJ/nPKGZAsHRpr0rNKz
fx/N/Oo6lr2yh/+ps6VxTkbPmB6E85WOO3UvjImZUY0XQdBjWle/4iJLdEC77Nr0
jXhdgeypucy6jkB6iBHMeVMlrNMEV7UxoBR/cCNx55zu/8sml5ByiDvCDT7sRomN
NgVt5S/FaVjYuzFUifJ12ToChXFgESKFmuso7WluEaWvMIGREdrMrKQKHfYLOzWF
2B5ZJDqw4o03fU4J/6rw61M1b+rjVpXMjPnzc2A+RgcjTvXv955gfZkwe4lt5wk/
3j8zMVo3+zLrMTAaEeIUM0UCAwEAAQ==
-----END PUBLIC KEY-----`

const DefaultGatewayURL = "https://gw.amnezia.org/"

// DefaultClientVersion - версия официального клиента, которую gateway
// считает допустимой: свою версию mawg он отвергает 501 "client version
// update is required" (проверено живьём 2026-10-08).
const DefaultClientVersion = "5.0.3.0"

type AmneziaKey struct {
	Name            string
	Description     string
	ConfigVersion   int
	ServiceType     string
	ServiceProtocol string
	UserCountryCode string
	APIKey          string
	Raw             string
}

// IsAmneziaKey распознаёт vpn://-ключ Amnezia Premium/Free API (это НЕ конфиг:
// параметры сервера внутри отсутствуют, только api_key для обмена у gateway).
func IsAmneziaKey(raw string) (AmneziaKey, bool) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "vpn://") {
		return AmneziaKey{}, false
	}
	payload, err := decodeBase64URL(strings.TrimSpace(strings.TrimPrefix(raw, "vpn://")))
	if err != nil || len(payload) < 5 {
		return AmneziaKey{}, false
	}
	doc, ok := zlibAll(payload[4:])
	if !ok {
		if d, ok2 := zlibAll(payload); ok2 {
			doc = d
		} else {
			return AmneziaKey{}, false
		}
	}
	var exp vpnExport
	if err := json.Unmarshal(doc, &exp); err != nil {
		return AmneziaKey{}, false
	}
	if exp.APIConfig.ServiceType == "" || exp.AuthData.APIKey == "" {
		return AmneziaKey{}, false
	}
	return AmneziaKey{
		Name:            exp.Name,
		Description:     exp.Description,
		ConfigVersion:   exp.ConfigVersion,
		ServiceType:     exp.APIConfig.ServiceType,
		ServiceProtocol: exp.APIConfig.ServiceProtocol,
		UserCountryCode: exp.APIConfig.UserCountryCode,
		APIKey:          exp.AuthData.APIKey,
		Raw:             raw,
	}, true
}

type ExchangeOptions struct {
	GatewayURL   string // по умолчанию https://gw.amnezia.org/
	PublicKeyPEM string // по умолчанию PROD-ключ выше
	Socks5       string // "host:port" - ходить через socks5 (bootstrap через рабочий пул)
	Version      string // cli_version/app_version в api_payload
	// ClientPrivKey - приватный WG/AWG-ключ клиента: передавайте при обновлении
	// пула, чтобы сервер считал выдачу тем же устройством, а не новой.
	ClientPrivKey string
	// VlessUUID - то же для протокола vless: xray-UUID клиента из пула.
	VlessUUID string
	// ServerCountryCode - желаемая локация (список - в ответе прошлого обмена).
	ServerCountryCode string
}

type GatewayCountry struct {
	Code      string   `json:"code"`
	Name      string   `json:"name,omitempty"`
	Protocols []string `json:"protocols,omitempty"`
}

type ExchangeResult struct {
	Nodes              []Node
	Protocol           string // протокол фактически выданного конфига (сервер может заменить на доступный в локации)
	ServerCountry      string
	ServerCountryName  string
	AvailableCountries []GatewayCountry
	ActiveDevices      int
	MaxDevices         int
	IssuedConfigs      int
	ClientPrivKey      string // приватный WG-ключ клиента (сохранить пулу для повторов)
	ClientUUID         string // xray-UUID клиента для vless (аналогично)
}

type exchangeEnvelope struct {
	KeyPayload string `json:"key_payload"`
	APIPayload string `json:"api_payload"`
}

type gatewayResponse struct {
	Config     string `json:"config"`
	HTTPStatus int    `json:"http_status"`
	Message    string `json:"message"`
}

// ExchangeAmneziaKey обменивает vpn://-ключ Amnezia на узлы AWG или VLESS
// (протокол - из ключа; сервер может отдать другой, доступный в локации):
// сеансовый AES-256-CBC + RSA(PKCS#1 v1.5) конверт -> POST <gateway>v1/config
// -> расшифровка ответа -> контейнеры сервера -> обычный парсер.
// Транспорт - прямо или через socks5 (по умолчанию прямо, строго HTTPS).
func ExchangeAmneziaKey(ctx context.Context, raw string, opts ExchangeOptions) (ExchangeResult, error) {
	key, ok := IsAmneziaKey(raw)
	if !ok {
		return ExchangeResult{}, fmt.Errorf("это не vpn://-ключ Amnezia API")
	}
	protocol := key.ServiceProtocol
	if protocol == "" {
		protocol = "awg"
	}
	gw := opts.GatewayURL
	if gw == "" {
		gw = DefaultGatewayURL
	}
	if !strings.HasSuffix(gw, "/") {
		gw += "/"
	}
	pemKey := opts.PublicKeyPEM
	if pemKey == "" {
		pemKey = ProdGatewayPublicKey
	}
	pub, err := parsePEMPublicKey([]byte(pemKey))
	if err != nil {
		return ExchangeResult{}, err
	}

	res := ExchangeResult{Protocol: protocol}
	var publicKey string
	switch protocol {
	case "awg":
		privB64 := strings.TrimSpace(opts.ClientPrivKey)
		if privB64 == "" {
			privB64, err = generateWGPrivateKey()
			if err != nil {
				return ExchangeResult{}, err
			}
		}
		pubB64, err := wgPublicKey(privB64)
		if err != nil {
			return ExchangeResult{}, fmt.Errorf("приватный ключ пула не WG-формата: %v", err)
		}
		res.ClientPrivKey = privB64
		publicKey = pubB64
	case "vless":
		uuid := strings.TrimSpace(opts.VlessUUID)
		if uuid == "" {
			uuid = uuid4()
		}
		res.ClientUUID = uuid
		publicKey = uuid
	default:
		return ExchangeResult{}, fmt.Errorf("неизвестный протокол сервиса %q (жду awg или vless)", protocol)
	}

	aesKey := randBytes(32)
	aesIV := randBytes(32)
	aesSalt := randBytes(8)

	keyPayload, err := json.Marshal(map[string]string{
		"aes_key":  base64.StdEncoding.EncodeToString(aesKey),
		"aes_iv":   base64.StdEncoding.EncodeToString(aesIV),
		"aes_salt": base64.StdEncoding.EncodeToString(aesSalt),
	})
	if err != nil {
		return ExchangeResult{}, err
	}
	encKeyPayload, err := rsa.EncryptPKCS1v15(rand.Reader, pub, keyPayload)
	if err != nil {
		return ExchangeResult{}, fmt.Errorf("rsa: %w", err)
	}

	payloadFields := map[string]any{
		"os_version":        "linux",
		"app_version":       opts.Version,
		"cli_version":       opts.Version,
		"cli_name":          "mawg",
		"distribution":      "github",
		"app_language":      "ru",
		"installation_uuid": uuid4(),
		"user_country_code": key.UserCountryCode,
		"service_type":      key.ServiceType,
		"service_protocol":  protocol,
		"public_key":        publicKey,
		"auth_data":         map[string]string{"api_key": key.APIKey},
		"is_connect_event":  false,
	}
	if payloadFields["app_version"] == "" {
		payloadFields["app_version"] = DefaultClientVersion
		payloadFields["cli_version"] = DefaultClientVersion
	}
	if opts.ServerCountryCode != "" {
		payloadFields["server_country_code"] = opts.ServerCountryCode
	}
	apiPayload, err := json.Marshal(payloadFields)
	if err != nil {
		return ExchangeResult{}, err
	}
	encAPIPayload, err := encryptAesCbcPkcs7(apiPayload, aesKey, aesIV[:16])
	if err != nil {
		return ExchangeResult{}, err
	}
	body, err := json.Marshal(exchangeEnvelope{
		KeyPayload: base64.StdEncoding.EncodeToString(encKeyPayload),
		APIPayload: base64.StdEncoding.EncodeToString(encAPIPayload),
	})
	if err != nil {
		return ExchangeResult{}, err
	}

	tr := &http.Transport{}
	if opts.Socks5 != "" {
		tr.DialContext = socks5Dialer(opts.Socks5)
	}
	client := &http.Client{Timeout: 30 * time.Second, Transport: tr}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, gw+"v1/config", bytes.NewReader(body))
	if err != nil {
		return ExchangeResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Client-Request-ID", uuid4())
	resp, err := client.Do(req)
	if err != nil {
		return ExchangeResult{}, fmt.Errorf("gateway %s: %v (если провайдер режет gw.amnezia.org - укажите socks5 работающего пула)", gw, err)
	}
	defer resp.Body.Close()
	rawBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return ExchangeResult{}, err
	}
	_, configRef, errMsg := unwrapGatewayBody(rawBody, aesKey, aesIV, resp.StatusCode)
	if errMsg != "" {
		return res, fmt.Errorf("gateway отказал: %s", errMsg)
	}
	if configRef == "" {
		return res, fmt.Errorf("gateway вернул ответ без конфига")
	}
	configText, err := decodeServerConfig(configRef, protocol, res.ClientPrivKey)
	if err != nil {
		return res, err
	}
	nodes, meta, err := parseGatewayServerConfig(configText, raw)
	if err != nil {
		return res, err
	}
	res.Nodes = nodes
	if meta.Protocol != "" {
		res.Protocol = meta.Protocol
	}
	res.ServerCountry = meta.ServerCountry
	res.ServerCountryName = meta.ServerCountryName
	for _, c := range meta.AvailableCountries {
		name := c.ServerCountryName
		if name == "" {
			name = c.ServerCountryCodeL10n
		}
		res.AvailableCountries = append(res.AvailableCountries, GatewayCountry{
			Code: c.ServerCountryCode, Name: name, Protocols: c.AvailableProtocols,
		})
	}
	res.ActiveDevices = meta.ActiveDevices
	res.MaxDevices = meta.MaxDevices
	res.IssuedConfigs = meta.IssuedConfigs
	return res, nil
}

func unwrapGatewayBody(body, aesKey, aesIV []byte, httpStatus int) (plain []byte, configRef, errMsg string) {
	var gr gatewayResponse
	if plain, ok := decryptAesCbcPkcs7(body, aesKey, aesIV[:16]); ok {
		if err := json.Unmarshal(plain, &gr); err == nil {
			return plain, gr.Config, gatewayError(gr)
		}
		return plain, "", "ответ gateway не JSON после расшифровки"
	}
	if err := json.Unmarshal(body, &gr); err == nil && (gr.HTTPStatus != 0 || gr.Message != "" || gr.Config != "") {
		return body, gr.Config, gatewayError(gr)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		if httpStatus >= 500 {
			return body, "", fmt.Sprintf("ошибка на стороне gateway (HTTP %d, пустой ответ); проверьте ключ в приложении Амнезии и повторите позже", httpStatus)
		}
		return body, "", fmt.Sprintf("пустой ответ (HTTP %d)", httpStatus)
	}
	head := string(body)
	if len(head) > 120 {
		head = head[:120]
	}
	return body, "", fmt.Sprintf("ответ не распознан (вероятна заглушка провайдера; HTTP %d): %q", httpStatus, strings.TrimSpace(head))
}

func gatewayError(gr gatewayResponse) string {
	if gr.Config != "" {
		return ""
	}
	return humanGatewayError(gr.HTTPStatus, gr.Message)
}

func humanGatewayError(status int, msg string) string {
	switch {
	case status == 402:
		return "подписка Амнезии не активна (HTTP 402): " + msg
	case status == 409, strings.Contains(msg, "limit of allowable configurations"):
		return "лимит выданных конфигов для ключа исчерпан (HTTP 409): каждая выдача на новый публичный ключ занимает слот - обновляйте пул тем же ключом («обновить из источника»), лишние конфиги отзовите в приложении Амнезии"
	case status == 429:
		return "слишком много запросов к gateway (HTTP 429), повторите позже: " + msg
	case status == 501, strings.Contains(msg, "client version update is required"):
		return "gateway требует свежую версию клиента - обновите mawg (HTTP 501): " + msg
	case strings.Contains(msg, "No active configuration found for"),
		strings.Contains(msg, "No non-revoked public key found for"):
		return "у ключа нет активной конфигурации (перевыпустите ключ в приложении Амнезии): " + msg
	case strings.Contains(msg, "Account not found"):
		return "ключ не найден (Account not found): " + msg
	case msg == "":
		return fmt.Sprintf("HTTP %d", status)
	default:
		return fmt.Sprintf("HTTP %d: %s", status, msg)
	}
}

type gatewayServerMeta struct {
	Protocol           string `json:"service_protocol"`
	ServerCountry      string `json:"server_country_code"`
	ServerCountryName  string `json:"server_country_name"`
	ActiveDevices      int    `json:"active_device_count"`
	MaxDevices         int    `json:"max_device_count"`
	IssuedConfigs      int    `json:"issued_configs"`
	AvailableCountries []struct {
		ServerCountryCode     string   `json:"server_country_code"`
		ServerCountryCodeL10n string   `json:"server_country_code_l10n"`
		ServerCountryName     string   `json:"server_country_name"`
		AvailableProtocols    []string `json:"available_protocols"`
	} `json:"available_countries"`
}

// decodeServerConfig разворачивает поле config ответа: vpn:// + base64url +
// zlib (с qCompress-префиксом или без); для awg подставляет приватный ключ
// клиента вместо плейсхолдера сервера.
func decodeServerConfig(configRef, protocol, privB64 string) (string, error) {
	payloadPart := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(configRef), "vpn://"))
	payload, err := decodeBase64URL(payloadPart)
	if err != nil {
		return "", fmt.Errorf("config не base64: %w", err)
	}
	doc, ok := zlibAll(payload)
	if !ok && len(payload) > 4 {
		doc, ok = zlibAll(payload[4:])
	}
	if !ok {
		return "", fmt.Errorf("config не zlib")
	}
	text := string(doc)
	if protocol == "awg" && privB64 != "" && strings.Contains(text, "$WIREGUARD_CLIENT_PRIVATE_KEY") {
		text = strings.ReplaceAll(text, "$WIREGUARD_CLIENT_PRIVATE_KEY", privB64)
	}
	return text, nil
}

func parseGatewayServerConfig(doc, raw string) ([]Node, gatewayServerMeta, error) {
	var meta gatewayServerMeta
	var apiCfg struct {
		APIConfig gatewayServerMeta `json:"api_config"`
	}
	if err := json.Unmarshal([]byte(doc), &apiCfg); err == nil {
		meta = apiCfg.APIConfig
	}
	var exp vpnExport
	if err := json.Unmarshal([]byte(doc), &exp); err != nil {
		return nil, meta, fmt.Errorf("серверный конфиг не JSON: %w", err)
	}
	var nodes []Node
	var warn []string
	for _, c := range exp.Containers {
		switch {
		case c.AWG.LastConfig != "" || c.WireGuard.LastConfig != "":
			lastConfig := c.AWG.LastConfig
			if lastConfig == "" {
				lastConfig = c.WireGuard.LastConfig
			}
			node, err := NodeFromConf("amnezia", "", []byte(vpnConfText(lastConfig)))
			if err != nil {
				warn = append(warn, err.Error())
				continue
			}
			node.Raw = raw
			nodes = append(nodes, node)
		case c.Vless.LastConfig != "":
			node, err := vlessNodeFromLastConfig(c.Vless.LastConfig)
			if err != nil {
				warn = append(warn, err.Error())
				continue
			}
			node.Raw = raw
			nodes = append(nodes, node)
		}
	}
	if len(nodes) == 0 {
		reason := "в контейнерах нет awg/wireguard/vless-конфигов"
		if len(warn) > 0 {
			reason += ": " + strings.Join(warn, "; ")
		}
		return nil, meta, fmt.Errorf("%s", reason)
	}
	return nodes, meta, nil
}

func vlessNodeFromLastConfig(lastConfig string) (Node, error) {
	lastConfig = strings.TrimSpace(lastConfig)
	if strings.HasPrefix(lastConfig, "vless://") {
		return ParseLink("amnezia", lastConfig)
	}
	var vc struct {
		Config string `json:"config"`
	}
	if err := json.Unmarshal([]byte(lastConfig), &vc); err == nil && strings.TrimSpace(vc.Config) != "" {
		return vlessNodeFromLastConfig(vc.Config)
	}
	return Node{}, fmt.Errorf("vless-контейнер: не vless://-ссылка и не JSON с ней")
}

func parsePEMPublicKey(data []byte) (*rsa.PublicKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("публичный ключ gateway: не PEM")
	}
	if pub, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		if rsaPub, ok := pub.(*rsa.PublicKey); ok {
			return rsaPub, nil
		}
		return nil, fmt.Errorf("публичный ключ gateway: не RSA")
	}
	return nil, fmt.Errorf("публичный ключ gateway: не разобран")
}

func encryptAesCbcPkcs7(plain, key, iv []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	padded := append(append([]byte{}, plain...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, padded)
	return out, nil
}

func decryptAesCbcPkcs7(data, key, iv []byte) ([]byte, bool) {
	if len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return nil, false
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, false
	}
	out := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, data)
	pad := int(out[len(out)-1])
	if pad == 0 || pad > aes.BlockSize || pad > len(out) {
		return nil, false
	}
	for _, b := range out[len(out)-pad:] {
		if int(b) != pad {
			return nil, false
		}
	}
	return out[:len(out)-pad], true
}

func generateWGPrivateKey() (string, error) {
	priv := make([]byte, 32)
	if _, err := rand.Read(priv); err != nil {
		return "", err
	}
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64
	return base64.StdEncoding.EncodeToString(priv), nil
}

func wgPublicKey(privB64 string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(privB64))
	if err != nil || len(raw) != 32 {
		return "", fmt.Errorf("ключ должен быть 32 байта base64")
	}
	key, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(key.PublicKey().Bytes()), nil
}

func randBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

func uuid4() string {
	b := randBytes(16)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// socks5Dialer - минимальный SOCKS5-коннект (без авторизации) на stdlib,
// чтобы mawg мог ходить в gateway через socks5 уже работающего пула.
func socks5Dialer(addr string) func(ctx context.Context, network, address string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		portNum, err := strconv.Atoi(port)
		if err != nil {
			return nil, err
		}
		var d net.Dialer
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, err
		}
		done := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				conn.Close()
			case <-done:
			}
		}()
		defer close(done)
		if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
			conn.Close()
			return nil, err
		}
		reply := make([]byte, 2)
		if _, err := io.ReadFull(conn, reply); err != nil || reply[0] != 5 || reply[1] != 0 {
			conn.Close()
			return nil, fmt.Errorf("socks5 %s: рукопожатие не удалось", addr)
		}
		req := append([]byte{5, 1, 0, 3, byte(len(host))}, []byte(host)...)
		req = append(req, byte(portNum>>8), byte(portNum))
		if _, err := conn.Write(req); err != nil {
			conn.Close()
			return nil, err
		}
		head := make([]byte, 4)
		if _, err := io.ReadFull(conn, head); err != nil || head[1] != 0 {
			conn.Close()
			return nil, fmt.Errorf("socks5 %s: соединение отклонено (код %d)", addr, headAt(head, 1))
		}
		skip := 0
		switch head[3] {
		case 1:
			skip = 4
		case 4:
			skip = 16
		default:
			ln := make([]byte, 1)
			if _, err := io.ReadFull(conn, ln); err != nil {
				conn.Close()
				return nil, err
			}
			skip = int(ln[0])
		}
		tail := make([]byte, skip+2)
		if _, err := io.ReadFull(conn, tail); err != nil {
			conn.Close()
			return nil, err
		}
		return conn, nil
	}
}

func headAt(b []byte, i int) byte {
	if i < len(b) {
		return b[i]
	}
	return 0
}
