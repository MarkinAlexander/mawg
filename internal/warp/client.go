package warp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Клиент регистрации устройств Cloudflare WARP. Контракт - по LxBox
// (app/lib/services/warp/warp_client.dart, задача 418
// «warp-api-host-failover», проверено их релизом 20 регистраций подряд):
//
//   - основной хост api.devices.cloudflare.com - доступен из РФ без
//     запрета; api.cloudflareclient.com - запасной (TCP-тихий без
//     анти-DPI). Перебор по порядку: сетевая ошибка/таймаут - следующий
//     хост, ЛЮБОЙ HTTP-статус - финал (хост жив, дело не в доступности);
//   - ключи генерируются локально (keys.go), наружу - только pubkey;
//   - ответ парсится top-level (форма v0a2158): id/token/config.

const (
	// Version - путь API; Cloudflare периодически поднимает - при
	// «registration failed» в первую очередь сверять с LxBox.
	Version       = "v0a2158"
	clientVersion = "a-7.21-0721"
	userAgent     = "okhttp/3.12.1"
	perHostWait   = 5 * time.Second // бюджет одной попытки (LxBox)
)

// APIHosts в порядке предпочтения (как в warp_endpoints.json LxBox).
var APIHosts = []string{
	"https://api.devices.cloudflare.com",
	"https://api.cloudflareclient.com",
}

// Device - результат регистрации: что нужно для сборки конфига и что
// стоит помнить про устройство (токен - для будущих операций с устройством).
type Device struct {
	ID           string `json:"id"`
	Token        string `json:"token"`
	PeerPublic   string `json:"peerPublic"`   // config.peers[0].public_key
	PeerEndpoint string `json:"peerEndpoint"` // config.peers[0].endpoint.host
	AddressV4    string `json:"addressV4"`    // config.interface.addresses.v4
	AddressV6    string `json:"addressV6,omitempty"`
}

// UnreachableError - оба хоста API глухи (сетевые ошибки/таймауты):
// честная ошибка с перечислением хостов; web-слой дополняет советом
// про анти-DPI (zator) и сайт-генератор.
type UnreachableError struct {
	Hosts []string
}

func (e *UnreachableError) Error() string {
	return "оба адреса Cloudflare (" + strings.Join(e.Hosts, ", ") +
		") недоступны с вашей сети"
}

// unreachableHost - внутренняя метка: хост не отвечает по сети.
type unreachableHost struct {
	host string
	err  error
}

func (e *unreachableHost) Error() string { return e.host + ": " + e.err.Error() }

// StatusError - Cloudflare ответил 429/5xx: сервер жив (перебор хостов
// не нужен), но просит паузу - GenerateOne делает один ретрай.
type StatusError struct {
	Code int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("регистрация не удалась (HTTP %d: лимит запросов или сбой Cloudflare)", e.Code)
}

// Client - регистратор. Hosts/HTTP инъектятся в тестах; в бою Hosts=nil
// (значит APIHosts), HTTP=nil (клиент с таймаутом на попытку).
type Client struct {
	Hosts []string
	HTTP  *http.Client
}

func (c *Client) hosts() []string {
	if len(c.Hosts) > 0 {
		return c.Hosts
	}
	return APIHosts
}

// Register - POST {host}/{Version}/reg с pubkey локально сгенерированной
// пары. Тело и заголовки - ровно как у LxBox (Android-клиент).
func (c *Client) Register(ctx context.Context, publicKey string) (*Device, error) {
	body := fmt.Sprintf(
		`{"key":%q,"install_id":"","fcm_token":"","tos":%q,"model":"PC","type":"Android","locale":"en_US"}`,
		publicKey, time.Now().UTC().Format(time.RFC3339Nano))
	for _, host := range c.hosts() {
		dev, err := c.tryHost(ctx, host, body)
		if err == nil {
			return dev, nil
		}
		var uh *unreachableHost
		if errors.As(err, &uh) {
			continue // сеть/таймаут - пробуем следующий хост
		}
		return nil, err // HTTP-статус - финал: хост жив
	}
	return nil, &UnreachableError{Hosts: c.hosts()}
}

func (c *Client) tryHost(ctx context.Context, host, body string) (*Device, error) {
	hctx, cancel := context.WithTimeout(ctx, perHostWait)
	defer cancel()
	req, err := http.NewRequestWithContext(hctx, http.MethodPost, host+"/"+Version+"/reg", strings.NewReader(body))
	if err != nil {
		return nil, &unreachableHost{host, err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("CF-Client-Version", clientVersion)
	httpc := c.HTTP
	if httpc == nil {
		httpc = &http.Client{}
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return nil, &unreachableHost{host, err}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, &unreachableHost{host, err}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			return nil, &StatusError{Code: resp.StatusCode}
		}
		return nil, fmt.Errorf("регистрация WARP не удалась (HTTP %d). Контракт API мог измениться (%s)",
			resp.StatusCode, Version)
	}
	var parsed struct {
		ID    string `json:"id"`
		Token string `json:"token"`
		// Старые формы API (путь v0i...) заворачивали ответ в result -
		// допускаем обе, читая результат из result, если он есть.
		Result *struct {
			ID    string `json:"id"`
			Token string `json:"token"`
		} `json:"result"`
		Config struct {
			Peers []struct {
				PublicKey string `json:"public_key"`
				Endpoint  struct {
					Host string `json:"host"`
				} `json:"endpoint"`
			} `json:"peers"`
			Interface struct {
				Addresses struct {
					V4 string `json:"v4"`
					V6 string `json:"v6"`
				} `json:"addresses"`
			} `json:"interface"`
		} `json:"config"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("Cloudflare ответил не-JSON (HTTP %d): контракт мог измениться (%s)",
			resp.StatusCode, Version)
	}
	id, token := parsed.ID, parsed.Token
	if parsed.Result != nil && id == "" {
		id, token = parsed.Result.ID, parsed.Result.Token
	}
	if id == "" || token == "" {
		return nil, fmt.Errorf("Cloudflare ответил без id/token (HTTP %d): контракт мог измениться (%s)",
			resp.StatusCode, Version)
	}
	if len(parsed.Config.Peers) == 0 || parsed.Config.Peers[0].PublicKey == "" {
		return nil, errors.New("Cloudflare ответил без публичного ключа пира")
	}
	if parsed.Config.Interface.Addresses.V4 == "" {
		return nil, errors.New("Cloudflare ответил без IPv4-адреса интерфейса")
	}
	dev := &Device{
		ID:           id,
		Token:        token,
		PeerPublic:   parsed.Config.Peers[0].PublicKey,
		PeerEndpoint: parsed.Config.Peers[0].Endpoint.Host,
		AddressV4:    parsed.Config.Interface.Addresses.V4,
		AddressV6:    parsed.Config.Interface.Addresses.V6,
	}
	if dev.PeerEndpoint == "" {
		dev.PeerEndpoint = "engage.cloudflareclient.com:2408"
	}
	return dev, nil
}
