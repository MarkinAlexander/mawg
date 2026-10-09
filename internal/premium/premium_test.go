package premium

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testLink(raw []byte, prefix string) string {
	var b bytes.Buffer
	w := zlib.NewWriter(&b)
	w.Write(raw)
	w.Close()
	data := b.Bytes()
	if prefix != "bare" {
		p := make([]byte, 4)
		binary.BigEndian.PutUint32(p, uint32(len(raw)))
		if prefix == "signature" {
			p = []byte{0, 0, 0, 255}
		}
		if prefix == "bad" {
			p = []byte{0, 0, 0, 1}
		}
		data = append(p, data...)
	}
	return "vpn://" + base64.RawURLEncoding.EncodeToString(data)
}

func TestImportKey(t *testing.T) {
	raw := []byte(`{"config_version":2,"api_config":{"service_type":"amnezia-premium","service_protocol":"awg","user_country_code":"RU"},"auth_data":{"api_key":"synthetic-subscription"}}`)
	for _, p := range []string{"signature", "length", "bare"} {
		key, country, err := ImportKey(testLink(raw, p))
		if err != nil || key != "synthetic-subscription" || country != "RU" {
			t.Fatalf("%s: import failed", p)
		}
	}
	for _, link := range []string{"vpn://bad!", testLink(raw, "bad"), testLink(bytes.Repeat([]byte("x"), maxBytes+1), "length"), testLink([]byte(`{"config_version":1}`), "signature"), testLink(bytes.ReplaceAll(raw, []byte("awg"), []byte("openvpn")), "signature")} {
		if _, _, err := ImportKey(link); err == nil {
			t.Fatal("accepted invalid import")
		}
	}
}

func TestGatewayRoundTrip(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	var issued atomic.Value
	issued.Store(`[]`)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/v1/account_info" || r.Header.Get("X-Client-Request-ID") == "" {
			t.Error("bad request metadata")
		}
		var env map[string][]byte
		if json.NewDecoder(r.Body).Decode(&env) != nil {
			t.Error("bad envelope")
			return
		}
		session, err := rsa.DecryptPKCS1v15(rand.Reader, rsaKey, env["key_payload"])
		if err != nil {
			t.Error(err)
			return
		}
		var keys map[string][]byte
		json.Unmarshal(session, &keys)
		if len(keys["aes_iv"]) != 32 || len(keys["aes_salt"]) != 8 {
			t.Error("incorrect session sizes")
		}
		block, _ := aes.NewCipher(keys["aes_key"])
		data := env["api_payload"]
		cipher.NewCBCDecrypter(block, keys["aes_iv"][:16]).CryptBlocks(data, data)
		data = data[:len(data)-int(data[len(data)-1])]
		var payload map[string]any
		if json.Unmarshal(data, &payload) != nil {
			t.Error("bad API JSON")
			return
		}
		if payload["installation_uuid"] != "fixed-router-uuid" || payload["service_type"] != "amnezia-premium" || payload["user_country_code"] != "RU" || payload["app_version"] != "5.0.3.0" {
			t.Error("bad payload")
		}
		raw := []byte(`{"http_status":200,"available_countries":[{"server_country_code":"nl","server_country_name":"Netherlands","available_protocols":["awg"]},{"server_country_code":"us-east","server_country_name":"US East","available_protocols":["awg"]},{"server_country_code":"US","available_protocols":["openvpn"]}]}`)
		raw = append(raw[:len(raw)-1], []byte(`,"issued_configs":`+issued.Load().(string)+`}`)...)
		n := aes.BlockSize - len(raw)%aes.BlockSize
		raw = append(raw, bytes.Repeat([]byte{byte(n)}, n)...)
		cipher.NewCBCEncrypter(block, keys["aes_iv"][:16]).CryptBlocks(raw, raw)
		w.Write(raw)
	}))
	defer server.Close()
	client := &Client{BaseURL: server.URL, HTTP: server.Client(), Key: &rsaKey.PublicKey}
	countries, err := client.Countries(context.Background(), "synthetic-subscription", "fixed-router-uuid", "RU")
	if err != nil || len(countries) != 2 || countries[0].Code != "nl" || countries[1].Code != "us-east" || calls != 1 {
		t.Fatalf("roundtrip failed: %v", err)
	}
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, server.URL+"/v1/account_info", http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	redirect.Config.ErrorLog = log.New(io.Discard, "", 0)
	client.BaseURL = redirect.URL
	client.HTTP = redirect.Client()
	if _, err := client.Countries(context.Background(), "secret", "id", ""); err == nil || strings.Contains(err.Error(), "secret") || calls != 1 {
		t.Fatal("redirect followed or secret leaked")
	}
	client.HTTP = http.DefaultClient
	if _, err := client.Countries(context.Background(), "secret", "id", ""); err == nil {
		t.Fatal("accepted untrusted TLS")
	}
	client.BaseURL = "http://example.com"
	if _, err := client.Countries(context.Background(), "secret", "id", ""); err == nil {
		t.Fatal("accepted HTTP")
	}
	client.BaseURL, client.HTTP = server.URL, server.Client()
	device := map[string]string{"installation_uuid": "fixed-router-uuid", "source_type": "gateway_account", "server_country_code": "nl"}
	for _, mode := range []string{"exact", "wrong UUID", "wrong source", "wrong country", "duplicate", "missing"} {
		v := map[string]string{}
		for k, value := range device {
			v[k] = value
		}
		switch mode {
		case "wrong UUID":
			v["installation_uuid"] = "unrelated"
		case "wrong source":
			v["source_type"] = "country_config"
		case "wrong country":
			v["server_country_code"] = "ro"
		}
		rows := []map[string]string{v}
		if mode == "duplicate" {
			rows = append(rows, v)
		}
		if mode == "missing" {
			rows = nil
		}
		raw, _ := json.Marshal(rows)
		issued.Store(string(raw))
		err := client.VerifyDevice(context.Background(), "synthetic-subscription", "fixed-router-uuid", "RU", "nl")
		if (err == nil) != (mode == "exact") {
			t.Fatalf("device verification %s: %v", mode, err)
		}
	}
}

func TestDeviceConfiguration(t *testing.T) {
	private, public, err := WireGuardKeys()
	if err != nil || len(private) != 44 || len(public) != 44 {
		t.Fatal("key generation failed")
	}
	id, err := UUID()
	if err != nil || len(id) != 36 || id[14] != '4' {
		t.Fatal("invalid UUID")
	}
	native := "[Interface]\nPrivateKey = $WIREGUARD_CLIENT_PRIVATE_KEY\nAddress = 10.0.0.2/32\nDNS = 1.1.1.1\nJc = 3\nJmin = 30\nJmax = 90\nS1 = 0\nS2 = 0\nS3 = 0\nS4 = 0\nH1 = 4000000000\nH2 = 2\nH3 = 3\nH4 = 4\nI1 = <b 0x010203>\n[Peer]\nPublicKey = " + public + "\nEndpoint = nl.example.net:51820\nAllowedIPs = 0.0.0.0/0, ::/0\n"
	wrap := func(native string) string {
		last, _ := json.Marshal(map[string]string{"config": native})
		root, _ := json.Marshal(map[string]any{"containers": []any{map[string]any{"container": "amnezia-awg", "awg": map[string]string{"last_config": string(last)}}}})
		return testLink(root, "length") + "\n"
	}
	raw, cfg, err := deviceConfig(wrap(native), private)
	if err != nil || cfg.PrivateKey != private || cfg.AWG.S4 == nil || cfg.AWG.I1 == nil || strings.Contains(string(raw), "$WIREGUARD") {
		t.Fatalf("decode failed: %v", err)
	}
	for _, h := range []string{"1-40", "4000000000-4294967295"} {
		_, ranged, err := deviceConfig(wrap(strings.Replace(native, "H1 = 4000000000", "H1 = "+h, 1)), private)
		if err != nil || ranged.AWG.H1 == nil || *ranged.AWG.H1 != h {
			t.Fatalf("supported H range lost: %s: %v", h, err)
		}
	}
	for _, bad := range []string{strings.Replace(native, "H1 = 4000000000", "H1 = 40-1", 1), strings.Replace(native, "Jc = 3", "Jc = bad", 1), strings.Replace(native, "I1 = <b 0x010203>", "I1 = 'injection'", 1), strings.Replace(native, "Jc = 3", "Jc = 3\nS5 = 0", 1), strings.Replace(native, "nl.example.net", "host'bad", 1)} {
		if _, _, err := deviceConfig(wrap(bad), private); err == nil {
			t.Fatal("unsupported or unsafe protocol fields accepted")
		}
	}
	for _, bad := range []string{strings.Replace(native, "DNS = 1.1.1.1", "DNS = 1.1.1.1\nMTU = invalid", 1), strings.Replace(native, "AllowedIPs = 0.0.0.0/0, ::/0", "AllowedIPs = 0.0.0.0/0, ::/0\nPersistentKeepalive = bad", 1)} {
		if _, _, err := deviceConfig(wrap(bad), private); err == nil {
			t.Fatal("invalid required numeric field silently discarded")
		}
	}
}

func TestGatewayRejectsFailureResponses(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var mode atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var env map[string][]byte
		if json.NewDecoder(r.Body).Decode(&env) != nil {
			t.Error("invalid request")
			return
		}
		session, err := rsa.DecryptPKCS1v15(rand.Reader, rsaKey, env["key_payload"])
		if err != nil {
			t.Error(err)
			return
		}
		var keys map[string][]byte
		json.Unmarshal(session, &keys)
		block, _ := aes.NewCipher(keys["aes_key"])
		m := mode.Load()
		raw := []byte(`{"http_status":429,"message":"synthetic-secret"}`)
		switch m {
		case 1:
			raw = []byte(`{"http_status":409,"message":"synthetic-secret"}`)
		case 2:
			raw = []byte(`{"http_status":"200","message":"synthetic-secret"}`)
		case 3:
			w.Write(make([]byte, maxBytes+16))
			return
		case 4:
			w.Write([]byte("synthetic-secret"))
			return
		case 5:
			raw = []byte(`{"http_status":200,"available_countries":[]}`)
		case 6:
			time.Sleep(80 * time.Millisecond)
		case 7:
			w.WriteHeader(503)
			raw = []byte(`{"http_status":200,"available_countries":[]}`)
		}
		n := aes.BlockSize - len(raw)%aes.BlockSize
		raw = append(raw, bytes.Repeat([]byte{byte(n)}, n)...)
		cipher.NewCBCEncrypter(block, keys["aes_iv"][:16]).CryptBlocks(raw, raw)
		w.Write(raw)
	}))
	defer server.Close()
	client := &Client{BaseURL: server.URL, HTTP: server.Client(), Key: &rsaKey.PublicKey}
	for i := int32(0); i <= 7; i++ {
		mode.Store(i)
		if i == 6 {
			client.HTTP.Timeout = 20 * time.Millisecond
		}
		_, err := client.Countries(context.Background(), "synthetic-secret", "id", "")
		if err == nil || strings.Contains(err.Error(), "synthetic-secret") {
			t.Fatalf("failure mode %d accepted or leaked secret", i)
		}
		if i == 7 && !strings.Contains(err.Error(), "503") {
			t.Fatal("HTTP failure was reported as success code 200")
		}
	}
}
