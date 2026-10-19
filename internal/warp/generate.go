package warp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"mawg/internal/wgconf"
)

// retryWait - пауза перед ретраем после 429/5xx (var: тесты укорачивают).
var retryWait = 3 * time.Second

// GenerateOne - одна генерация: локальная пара ключей -> регистрация
// устройства (один ретрай на 429/5xx - Cloudflare рейт-лимитит, честная
// ошибка после второй попытки) -> текст конфига + разобранная форма.
// Device возвращается для реестра регистраций (id/токен, без приватного
// ключа); приватный ключ живёт только внутри конфига.
func GenerateOne(ctx context.Context, c *Client, endpoint string, allowedIPs []string) (wgconf.NamedConfig, *Device, error) {
	private, public, err := GenerateKeys()
	if err != nil {
		return wgconf.NamedConfig{}, nil, err
	}
	dev, err := c.Register(ctx, public)
	if err != nil {
		var se *StatusError
		if errors.As(err, &se) {
			select {
			case <-ctx.Done():
				return wgconf.NamedConfig{}, nil, ctx.Err()
			case <-time.After(retryWait):
			}
			dev, err = c.Register(ctx, public)
		}
		if err != nil {
			return wgconf.NamedConfig{}, nil, err
		}
	}
	ep := endpoint
	if ep == "" {
		ep = dev.PeerEndpoint // из ответа: engage.cloudflareclient.com:2408
	}
	if err := ValidEndpoint(ep); err != nil {
		return wgconf.NamedConfig{}, nil, fmt.Errorf("эндпоинт %q не годится: %v", ep, err)
	}
	// Слово владельца (2026-10-19, финальное): «суть в обфускации, чтобы
	// там было АВГ» - конфиг в пуле сразу с полями AWG 2.0 (уровень
	// awg2): прозрачная обфускация warp-generator/LxBox + I1/I2 SIP-пара.
	// Чистый WG-вариант для скачивания выводится StripAWGParams'ом.
	raw := WithAWGFields(BuildConfig(private, dev.AddressV4, dev.PeerPublic, ep, allowedIPs))
	cfg, err := wgconf.Parse(raw)
	if err != nil {
		return wgconf.NamedConfig{}, nil, fmt.Errorf("собранный конфиг не прошёл разбор: %v", err)
	}
	return wgconf.NamedConfig{
		OriginalName: "WARP " + ep,
		Raw:          raw,
		Config:       cfg,
	}, dev, nil
}
