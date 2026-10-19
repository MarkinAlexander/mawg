package warp

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"errors"
)

// GenerateKeys - пара ключей X25519 в формате WireGuard (base64-std).
// Ключи генерируются ЛОКАЛЬНО: в Cloudflare уходит только pubkey.
// Чужие воркеры-генераторы не используются - предупреждение LxBox:
// воркер генерит приватник на своём сервере (warp-generator.site так
// и делает - приватник транзитом через их CF-Worker). Клэмпинг битов
// как в wg-генераторе ключей (прецедент premium.WireGuardKeys).
func GenerateKeys() (private, public string, err error) {
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", errors.New("не удалось сгенерировать ключ WARP")
	}
	priv := key.Bytes()
	priv[0] &= 248
	priv[31] = (priv[31] & 127) | 64
	return base64.StdEncoding.EncodeToString(priv),
		base64.StdEncoding.EncodeToString(key.PublicKey().Bytes()), nil
}
