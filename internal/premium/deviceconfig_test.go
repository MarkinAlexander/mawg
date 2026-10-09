package premium

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"strings"
	"testing"
)

// freeLink - конверт vpn:// вокруг native-конфига (контейнер amnezia-awg).
func freeLink(t *testing.T, native string) string {
	t.Helper()
	last, _ := json.Marshal(map[string]string{"config": native})
	doc, _ := json.Marshal(map[string]any{
		"containers": []any{map[string]any{"container": "amnezia-awg", "awg": map[string]string{"last_config": string(last)}}},
	})
	var b bytes.Buffer
	zw := zlib.NewWriter(&b)
	zw.Write(doc)
	zw.Close()
	prefix := make([]byte, 4)
	binary.BigEndian.PutUint32(prefix, uint32(len(doc)))
	return "vpn://" + base64.RawURLEncoding.EncodeToString(append(prefix, b.Bytes()...))
}

const devTestPrivate = "uJd2xK5t0mFvqWz+YbN8cLrA3eHh7oPqQ1wZsX4dC6k="

// реальные конфигы несут h-значения до 4294967295: раньше проверка гнала
// их через int и на 32-битных роутерах (mipsle/386) переполнялась - рабочий
// конфиг отвергался с «unsupported or invalid»
func TestDeviceConfigLargeHValues(t *testing.T) {
	native := "[Interface]\nPrivateKey = $WIREGUARD_CLIENT_PRIVATE_KEY\nAddress = 10.9.0.2/32\n" +
		"Jc = 3\nJmin = 50\nJmax = 1000\nS1 = 25\n" +
		"H1 = 4294967295\nH2 = 1000000000-2000000000\nH3 = 3000000000\nH4 = 7\n" +
		"I2 = <b 0x010203>\n" +
		"[Peer]\nPublicKey = QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE=\nEndpoint = free.example.net:51820\nAllowedIPs = 0.0.0.0/0\n"
	data, _, err := deviceConfig(freeLink(t, native), devTestPrivate)
	if err != nil {
		t.Fatalf("большие h-значения должны приниматься: %v", err)
	}
	if !strings.Contains(string(data), "4294967295") {
		t.Fatal("конфиг искажён")
	}
}

// причина отказа называется конкретно - по сырой ошибке было не понять,
// что именно gateway прислал не так
func TestDeviceConfigRejectReasons(t *testing.T) {
	native := "[Interface]\nPrivateKey = $WIREGUARD_CLIENT_PRIVATE_KEY\nAddress = 10.9.0.2/32\nListenPort = 51820\n" +
		"[Peer]\nPublicKey = QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE=\nEndpoint = free.example.net:51820\nAllowedIPs = 0.0.0.0/0\n"
	_, _, err := deviceConfig(freeLink(t, native), devTestPrivate)
	var bad *BadConfigError
	if err == nil {
		t.Fatal("ожидался отказ")
	}
	if ok := asBad(err, &bad); !ok || !strings.Contains(bad.Reason, "listenport") || bad.Raw == "" {
		t.Fatalf("причина/сырая ссылка: %+v", bad)
	}
	// готовый конфиг с настоящим ключом - юзер может унести его в клиент
	if !strings.Contains(bad.Config, devTestPrivate) || !strings.Contains(bad.Config, "ListenPort") {
		t.Fatalf("конфиг в ошибке: %q", bad.Config)
	}
}

// живой кейс Amnezia Free (2026-10-09): полный AWG 3.x-конфиг проходит
// валидацию - защита заголовка, диапазонные таймеры, keepalive-диапазон
func TestDeviceConfigAWG3Accepted(t *testing.T) {
	native := "[Interface]\nPrivateKey = $WIREGUARD_CLIENT_PRIVATE_KEY\nAddress = 100.81.189.224/32\nDNS = 100.64.0.1, 8.8.4.4\n" +
		"Jc = 6\nJmin = 10\nJmax = 80\nS1 = 31\nS2 = 1101\nS3 = 1240\nS4 = 12\nH1 = 1\nH2 = 2\nH3 = 3\nH4 = 4\n" +
		"HeaderProtectionKey = c3ludGhldGljLWhwLWtleQ==\n" +
		"RekeyAfterTime = 100-120\nRekeyTimeout = 3-8\nRejectAfterTime = 150-180\nKeepaliveTimeout = 7-13\nMaxHandshakeAttempts = 15-20\nContentPaddingAddition = 10-100\n" +
		"I1 = <b 0xc70000000108ce1bf31e><r 640><b 0xe78ab395ff2f><r 64>\n" +
		"[Peer]\nPublicKey = ll+gozj6agFD5WRhXQUFna6LLoC4zxi7eka5RMUfKE8=\nPresharedKey = kLKh8p7664kHCd8rKtcfHWgmaWqzv1mTUAQxtCf/GVU=\n" +
		"AllowedIPs = 100.64.0.1/32, 8.8.8.8/32\nEndpoint = 84.54.51.212:1545\nPersistentKeepalive = 25-35\n"
	data, cfg, err := deviceConfig(freeLink(t, native), devTestPrivate)
	if err != nil {
		t.Fatalf("AWG 3.x конфиг должен приниматься: %v", err)
	}
	if !cfg.NeedsEngine() {
		t.Fatal("AWG 3.x конфиг должен требовать движок")
	}
	if !strings.Contains(string(data), "HeaderProtectionKey") {
		t.Fatal("конфиг искажён")
	}
}

func asBad(err error, target **BadConfigError) bool {
	if b, ok := err.(*BadConfigError); ok {
		*target = b
		return true
	}
	return false
}
