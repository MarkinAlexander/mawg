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

func asBad(err error, target **BadConfigError) bool {
	if b, ok := err.(*BadConfigError); ok {
		*target = b
		return true
	}
	return false
}
