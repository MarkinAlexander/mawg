package web

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
)

// readdressProton - структурная копия живого Proton-конфига владельца
// (120-док, fixtures-proton): один адрес у ВСЕХ конфигов провайдера -
// ровно тот случай, который закрывает перенумерация. Ключи и эндпоинты
// синтетические, pub/endpoint у конфигов разные (дубликат стором не был бы).
func readdressProton(pub, host string) string {
	return "[Interface]\nPrivateKey = QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE=\n" +
		"Address = 10.2.0.2/32, 2a07:b944::2:2/128\n" +
		"DNS = 10.2.0.1, 2a07:b944::2:1\nMTU = 1420\nJc = 3\nJmin = 1\nJmax = 3\nH1 = 1\nH2 = 2\nH3 = 3\nH4 = 4\n" +
		"I1 = <b 0x010203>\n\n[Peer]\nPublicKey = " + pub +
		"\nEndpoint = " + host + ":51820\nAllowedIPs = 0.0.0.0/0, ::/0\n"
}

func readdressSetup(t *testing.T, ts *httptest.Server, pool string, files map[string]string) {
	t.Helper()
	resp, err := http.Post(ts.URL+"/api/v1/pools", "application/json", strings.NewReader(`{"name":"`+pool+`","fallback":"direct"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("create %s: %d", pool, resp.StatusCode)
	}
	// Порядок ротации = порядок загрузки: map обходится случайно,
	// фиксируем отсортированными ключами
	keys := make([]string, 0, len(files))
	for f := range files {
		keys = append(keys, f)
	}
	sort.Strings(keys)
	for _, file := range keys {
		raw := files[file]
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fw, _ := mw.CreateFormFile("files", file)
		fw.Write([]byte(raw))
		mw.Close()
		resp, err := http.Post(ts.URL+"/api/v1/pools/"+pool+"/configs", mw.FormDataContentType(), &buf)
		if err != nil {
			t.Fatal(err)
		}
		var up map[string]any
		json.NewDecoder(resp.Body).Decode(&up)
		resp.Body.Close()
		if up["added"] != float64(1) {
			t.Fatalf("upload %s to %s: %v", file, pool, up)
		}
	}
}

func readdressPost(t *testing.T, ts *httptest.Server, pool, base string, files []string) (int, map[string]any) {
	t.Helper()
	body := map[string]any{"base": base}
	if files != nil {
		body["files"] = files
	}
	raw, _ := json.Marshal(body)
	resp, err := http.Post(ts.URL+"/api/v1/pools/"+pool+"/configs/readdress", "application/json", strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestReaddressConfigs(t *testing.T) {
	ts := newTestServer(t)
	readdressSetup(t, ts, "proton", map[string]string{
		"nl-15.conf": readdressProton("QkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkI=", "198.51.100.15"),
		"nl-29.conf": readdressProton("Q0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0M=", "198.51.100.29"),
	})
	code, out := readdressPost(t, ts, "proton", "10.2.0.5", nil)
	if code != 200 {
		t.Fatalf("readdress: %d %v", code, out)
	}
	changed := out["changed"].([]any)
	if len(changed) != 2 {
		t.Fatalf("changed: %v", changed)
	}
	one := changed[0].(map[string]any)
	if one["file"] != "nl-15.conf" || one["address"] != "10.2.0.5" || one["old"] != "10.2.0.2" {
		t.Fatalf("changed[0]: %v", one)
	}
	if changed[1].(map[string]any)["address"] != "10.2.0.6" {
		t.Fatalf("changed[1]: %v", changed[1])
	}
	// Файл на диске: новый v4 + синхронный v6, обфускация и DNS целы
	resp, err := http.Get(ts.URL + "/api/v1/pools/proton/configs/nl-15.conf")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw := make([]byte, 4096)
	n, _ := resp.Body.Read(raw)
	text := string(raw[:n])
	for _, want := range []string{"Address = 10.2.0.5/32, 2a07:b944::2:5/128", "DNS = 10.2.0.1", "Jc = 3", "I1 = <b 0x010203>"} {
		if !strings.Contains(text, want) {
			t.Fatalf("в конфиге нет %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "10.2.0.2") {
		t.Fatalf("старый адрес остался:\n%s", text)
	}
}

func TestReaddressConflictAcrossPools(t *testing.T) {
	ts := newTestServer(t)
	readdressSetup(t, ts, "proton", map[string]string{
		"nl-15.conf": readdressProton("QkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkI=", "198.51.100.15"),
	})
	readdressSetup(t, ts, "proton2", map[string]string{
		"nl-29.conf": readdressProton("Q0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0M=", "198.51.100.29"),
	})
	// Два ВЫКЛЮЧЕННЫХ пула с одним 10.2.0.2: живых туннелей нет, но
	// реестр адресов обязан увидеть занятость (120-док)
	code, out := readdressPost(t, ts, "proton2", "10.2.0.2", nil)
	if code != 400 {
		t.Fatalf("пересечение пулов пропущено: %d %v", code, out)
	}
	if !strings.Contains(out["error"].(string), "занят") {
		t.Fatalf("текст ошибки: %v", out)
	}
	code, out = readdressPost(t, ts, "proton2", "10.2.0.7", nil)
	if code != 200 {
		t.Fatalf("свободная подсеть: %d %v", code, out)
	}
}

func TestReaddressSubnetHint(t *testing.T) {
	ts := newTestServer(t)
	readdressSetup(t, ts, "proton", map[string]string{
		"nl-15.conf": readdressProton("QkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkI=", "198.51.100.15"),
	})
	// Вне подсети Proton - работает, но с честным варнингом
	code, out := readdressPost(t, ts, "proton", "192.0.2.9", nil)
	if code != 200 {
		t.Fatalf("readdress: %d %v", code, out)
	}
	warns := out["warnings"].([]any)
	if len(warns) == 0 || !strings.Contains(warns[0].(string), "вне подсети") {
		t.Fatalf("варнинг подсети: %v", warns)
	}
}

func TestReaddressOverflowAndPremium(t *testing.T) {
	ts := newTestServer(t)
	readdressSetup(t, ts, "proton", map[string]string{
		"nl-15.conf": readdressProton("QkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkI=", "198.51.100.15"),
		"nl-29.conf": readdressProton("Q0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0M=", "198.51.100.29"),
	})
	// База у края /24: второму адресу не влезает - отказ ДО первой записи
	code, _ := readdressPost(t, ts, "proton", "10.2.0.255", nil)
	if code != 400 {
		t.Fatalf("переполнение /24 пропущено: %d", code)
	}
	// Активный конфиг не переприменен мимо ошибок: переполнение не задело файлы
	resp, err := http.Get(ts.URL + "/api/v1/pools/proton/configs/nl-15.conf")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw := make([]byte, 4096)
	n, _ := resp.Body.Read(raw)
	if !strings.Contains(string(raw[:n]), "10.2.0.2") {
		t.Fatalf("файл перезаписан при отказе:\n%s", raw[:n])
	}
}
