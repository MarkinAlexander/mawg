package rotator

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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"mawg/internal/platform/fake"
	"mawg/internal/premium"
	"mawg/internal/store"
)

func premiumLink(v any, signature bool) string {
	raw, _ := json.Marshal(v)
	var b bytes.Buffer
	w := zlib.NewWriter(&b)
	w.Write(raw)
	w.Close()
	prefix := make([]byte, 4)
	binary.BigEndian.PutUint32(prefix, uint32(len(raw)))
	if signature {
		prefix = []byte{0, 0, 0, 255}
	}
	return "vpn://" + base64.RawURLEncoding.EncodeToString(append(prefix, b.Bytes()...))
}

type premiumBackend struct {
	*fake.Fake
	saves   int
	downErr error
}

func (b *premiumBackend) SaveConfig() error { b.saves++; return nil }
func (b *premiumBackend) Down(p store.Pool) error {
	if b.downErr != nil {
		return b.downErr
	}
	return b.Fake.Down(p)
}

func (b *premiumBackend) Destroy(p store.Pool) error {
	if b.downErr != nil {
		return b.downErr
	}
	return b.Fake.Destroy(p)
}

func TestPremiumDeletePreservesLegacyPools(t *testing.T) {
	st, _ := store.Open(t.TempDir())
	st.CreatePool("static", store.PoolSettings{})
	st.CreatePool("premium", store.PoolSettings{})
	st.ImportPremium("premium", "synthetic-subscription", "")
	b := &premiumBackend{Fake: fake.New(), downErr: errors.New("synthetic-subscription")}
	e := New(st, b, nil)
	if err := e.DeletePool("static"); err != nil {
		t.Fatal("legacy deletion changed:", err)
	}
	if err := e.DeletePool("premium"); err == nil || strings.Contains(err.Error(), "synthetic-subscription") {
		t.Fatal("Premium deletion did not retain/sanitize failed down")
	}
	if _, ok := st.Pool("premium"); !ok {
		t.Fatal("failed Premium down deleted pool")
	}
}

func TestPremiumCountrySwitchKeepsOneDevice(t *testing.T) {
	base := t.TempDir()
	st, _ := store.Open(base)
	st.CreatePool("premium", store.PoolSettings{Platform: store.PlatformOpenwrt, OpenwrtProto: "amneziawg"})
	st.CreatePool("other", store.PoolSettings{})
	backend := &premiumBackend{Fake: fake.New()}
	engine := New(st, backend, nil)
	rsaKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	devices := map[string]string{}
	configCalls := 0
	failure := 0
	verifyCalls := 0
	var publicKeys []string
	gateway := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var env map[string][]byte
		json.NewDecoder(r.Body).Decode(&env)
		session, err := rsa.DecryptPKCS1v15(rand.Reader, rsaKey, env["key_payload"])
		if err != nil {
			t.Error(err)
			return
		}
		var keys map[string][]byte
		json.Unmarshal(session, &keys)
		block, _ := aes.NewCipher(keys["aes_key"])
		raw := env["api_payload"]
		cipher.NewCBCDecrypter(block, keys["aes_iv"][:16]).CryptBlocks(raw, raw)
		raw = raw[:len(raw)-int(raw[len(raw)-1])]
		var v map[string]any
		json.Unmarshal(raw, &v)
		response := map[string]any{"http_status": 200}
		switch r.URL.Path {
		case "/v1/account_info":
			response["available_countries"] = []any{map[string]any{"server_country_code": "nl", "available_protocols": []string{"awg"}}, map[string]any{"server_country_code": "us-east", "available_protocols": []string{"awg"}}}
			issued := []any{}
			for id, country := range devices {
				issued = append(issued, map[string]string{"installation_uuid": id, "source_type": "gateway_account", "server_country_code": country})
			}
			response["issued_configs"] = issued
			if len(devices) > 0 {
				verifyCalls++
			}
		case "/v1/config":
			configCalls++
			id, country, public := v["installation_uuid"].(string), v["server_country_code"].(string), v["public_key"].(string)
			saved := st.Premium()
			b, _ := base64.StdEncoding.DecodeString(saved.PendingPrivate)
			priv, err := ecdh.X25519().NewPrivateKey(b)
			if err != nil || base64.StdEncoding.EncodeToString(priv.PublicKey().Bytes()) != public || saved.PendingCountry != country {
				t.Error("private key not persisted before request")
				return
			}
			devices[id] = country
			publicKeys = append(publicKeys, public)
			if failure != 0 {
				response["http_status"] = failure
				response["message"] = "synthetic-subscription " + saved.PendingPrivate
			} else {
				native := "[Interface]\nPrivateKey = $WIREGUARD_CLIENT_PRIVATE_KEY\nAddress = 10.0.0.2/32\nJc = 3\nH1 = 4000000000\nS4 = 0\nI1 = <b 0x010203>\n[Peer]\nPublicKey = " + public + "\nEndpoint = " + strings.ToLower(country) + ".example.net:51820\nAllowedIPs = 0.0.0.0/0\n"
				last, _ := json.Marshal(map[string]string{"config": native})
				response["config"] = premiumLink(map[string]any{"containers": []any{map[string]any{"container": "amnezia-awg", "awg": map[string]string{"last_config": string(last)}}}}, false) + "\n"
			}
		default:
			t.Error("unexpected operation: " + r.URL.Path)
		}
		raw, _ = json.Marshal(response)
		n := aes.BlockSize - len(raw)%aes.BlockSize
		raw = append(raw, bytes.Repeat([]byte{byte(n)}, n)...)
		cipher.NewCBCEncrypter(block, keys["aes_iv"][:16]).CryptBlocks(raw, raw)
		w.Write(raw)
	}))
	defer gateway.Close()
	client := &premium.Client{BaseURL: gateway.URL, HTTP: gateway.Client(), Key: &rsaKey.PublicKey}
	engine.PremiumClient = client
	link := premiumLink(map[string]any{"config_version": 2, "api_config": map[string]string{"service_type": "amnezia-premium", "service_protocol": "awg", "user_country_code": "RU"}, "auth_data": map[string]string{"api_key": "synthetic-subscription"}}, true)
	if err := engine.ImportPremium(context.Background(), "premium", link); err != nil {
		t.Fatal(err)
	}
	uuid := st.Premium().UUID
	for _, country := range []string{"nl", "us-east", "nl"} {
		if err := engine.SwitchPremium(context.Background(), "premium", country); err != nil {
			t.Fatal(err)
		}
		if engine.ifaceAt.IsZero() || backend.saves == 0 {
			t.Fatal("Premium bypassed interface cache/settling or router save")
		}
		if st.Premium().UUID != uuid || st.PremiumView("premium").Country != country || st.PremiumView("premium").Pending {
			t.Fatal("incorrect subscription state")
		}
		pool, _ := st.Pool("premium")
		if len(pool.Configs) != 1 || st.State("premium").ActiveFile != pool.Configs[0].File {
			t.Fatal("country switch built static pool or lost activation")
		}
		st, _ = store.Open(base)
		engine = New(st, backend, nil)
		engine.PremiumClient = client
	}
	if len(devices) != 1 || configCalls != 3 || publicKeys[0] == publicKeys[1] {
		t.Fatal("device identity changed or keys not regenerated")
	}
	if verifyCalls < 5 {
		t.Fatal("remote registration was not read back after every country switch")
	}
	engine.tunnelAddrs = map[string]string{"10.0.0.2": "other"}
	engine.tunnelAt = engine.now()
	before := len(backend.Applied())
	if err := engine.SwitchPremium(context.Background(), "premium", "us-east"); err == nil || len(backend.Applied()) != before {
		t.Fatal("Premium ignored occupied tunnel address")
	}
	engine.tunnelAddrs = map[string]string{}
	engine.tunnelAt = engine.now()
	old := st.State("premium").ActiveFile
	oldRaw, _ := os.ReadFile(filepath.Join(st.PoolDir("premium"), old))
	failure = 409
	err := engine.SwitchPremium(context.Background(), "premium", "us-east")
	if err == nil || !strings.Contains(err.Error(), "409") || strings.Contains(err.Error(), "synthetic-subscription") {
		t.Fatal("unsanitized/missing limit error")
	}
	retained, _ := os.ReadFile(filepath.Join(st.PoolDir("premium"), old))
	if !bytes.Equal(oldRaw, retained) || st.State("premium").ActiveFile != old || !st.PremiumView("premium").Pending {
		t.Fatal("failed acquisition damaged old local state or hid remote uncertainty")
	}
	pending := st.Premium().PendingPrivate
	for _, code := range []int{408, 429} {
		failure = code
		if err := engine.SwitchPremium(context.Background(), "premium", "us-east"); err == nil {
			t.Fatal("failed acquisition accepted")
		}
		retained, _ = os.ReadFile(filepath.Join(st.PoolDir("premium"), old))
		if !bytes.Equal(oldRaw, retained) || st.Premium().PendingPrivate != pending {
			t.Fatal("timeout/rate limit discarded working local config or recovery key")
		}
	}
	failure = 0
	backend.ApplyErrOn["us-east.example.net:51820"] = errors.New("backend leaked synthetic-subscription")
	if err := engine.SwitchPremium(context.Background(), "premium", "us-east"); err == nil || strings.Contains(err.Error(), "synthetic-subscription") {
		t.Fatal("backend error leaked")
	}
	retained, _ = os.ReadFile(filepath.Join(st.PoolDir("premium"), old))
	if !bytes.Equal(oldRaw, retained) {
		t.Fatal("apply failure damaged local config")
	}
	delete(backend.ApplyErrOn, "us-east.example.net:51820")
	if err := engine.SwitchPremium(context.Background(), "premium", "us-east"); err != nil {
		t.Fatal(err)
	}
	if st.Premium().PendingPrivate != "" || len(devices) != 1 || pending == "" {
		t.Fatal("pending recovery failed")
	}
	for _, public := range publicKeys[4:] {
		if public != publicKeys[3] {
			t.Fatal("retry discarded persisted private key")
		}
	}
	if err := engine.SwitchPremium(context.Background(), "premium", "US"); err == nil {
		t.Fatal("unavailable country accepted")
	}
	other, _ := st.Pool("other")
	if other.Premium || len(other.Configs) != 0 {
		t.Fatal("unrelated pool changed")
	}
	current := st.State("premium").ActiveFile
	backend.ApplyErrOn["us-east.example.net:51820"] = errors.New("leaked synthetic-subscription")
	if err := engine.SetActive("premium", current); err == nil || strings.Contains(err.Error(), "synthetic-subscription") || strings.Contains(st.State("premium").LastError, "synthetic-subscription") {
		t.Fatal("existing activation path leaked Premium secret")
	}
	events, _ := json.Marshal(st.Events())
	if bytes.Contains(events, []byte("synthetic-subscription")) {
		t.Fatal("activation events leaked Premium secret")
	}
	delete(backend.ApplyErrOn, "us-east.example.net:51820")
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for _, country := range []string{"nl", "us-east", "nl", "us-east", "nl", "us-east"} {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- engine.SwitchPremium(context.Background(), "premium", country) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	p, _ := st.Pool("premium")
	if len(devices) != 1 || len(p.Configs) != 1 || st.State("premium").ActiveFile != p.Configs[0].File {
		t.Fatal("concurrent switches lost one-device/current-config identity")
	}
	os.Mkdir(filepath.Join(base, "state.json.tmp"), 0700)
	if err := engine.SwitchPremium(context.Background(), "premium", "nl"); err == nil || !st.PremiumView("premium").Pending {
		t.Fatal("failed state persistence was reported as completed country switch")
	}
	os.Remove(filepath.Join(base, "state.json.tmp"))
	if err := engine.SwitchPremium(context.Background(), "premium", "nl"); err != nil {
		t.Fatal(err)
	}
}
