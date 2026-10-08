package web

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func fakeWGKey(b byte) string {
	k := make([]byte, 32)
	for i := range k {
		k[i] = b
	}
	return base64.StdEncoding.EncodeToString(k)
}

var (
	fakeWGLink = "wireguard://" + fakeWGKey('c') + "@203.0.113.30:35091" +
		"?address=10.0.0.7%2F32&keepalive=25&mtu=1280&presharedkey=" + fakeWGKey('d') +
		"&publickey=" + fakeWGKey('e') + "#wg-from-source"

	fakeVlessLink = "vless://00000000-0000-4000-8000-000000000001@203.0.113.10:23747" +
		"?encryption=none&security=reality&sid=deadbeef&sni=www.example.com&type=tcp#vless-from-source"
)

func postFromSource(t *testing.T, body string) (int, sourcePlan) {
	t.Helper()
	ts := newTestServer(t)
	resp, err := http.Post(ts.URL+"/api/v1/pools/from-source", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var plan sourcePlan
	json.NewDecoder(resp.Body).Decode(&plan)
	return resp.StatusCode, plan
}

func TestFromSourceCreatesNativePool(t *testing.T) {
	code, plan := postFromSource(t, `{"name":"my-wg-fr","source":"`+fakeWGLink+`","keeneticSlot":"Wireguard2"}`)
	if code != 200 {
		t.Fatalf("код %d", code)
	}
	if plan.Pool != "my-wg-fr" || plan.Added != 1 {
		t.Fatalf("план: %+v", plan)
	}
	if len(plan.Engine) != 0 || len(plan.Skipped) != 0 {
		t.Fatalf("лишнее: %+v", plan)
	}
}

func TestFromSourceMixedShowsEngineNodes(t *testing.T) {
	body, _ := json.Marshal(map[string]string{
		"name": "mixed", "source": fakeWGLink + "\n" + fakeVlessLink, "keeneticSlot": "Wireguard2",
	})
	code, plan := postFromSource(t, string(body))
	if code != 200 {
		t.Fatalf("код %d", code)
	}
	if plan.Pool != "mixed" || plan.Added != 1 {
		t.Fatalf("нативная часть: %+v", plan)
	}
	if len(plan.Engine) != 1 || plan.Engine[0].Type != "vless" {
		t.Fatalf("движок-узлы: %+v", plan.Engine)
	}
}

func TestFromSourceLxOnlyNodeCreatesNothing(t *testing.T) {
	lxOnly := "vless://00000000-0000-4000-8000-000000000001@203.0.113.12:443" +
		"?encryption=mlkem768x25519plus.native.0rtt.FAKE&type=xhttp&path=%2Fup&mode=auto" +
		"&security=tls&sni=cdn.example.org#lx-only-node"
	body, _ := json.Marshal(map[string]string{"name": "lx-wait", "source": lxOnly})
	code, plan := postFromSource(t, string(body))
	if code != 200 {
		t.Fatalf("код %d", code)
	}
	if plan.Pool != "" {
		t.Fatalf("пул без поддерживаемых узлов не должен создаваться: %+v", plan)
	}
	if !anyContains(plan.Warnings, "Пул не создан") {
		t.Fatalf("должна быть причина: %v", plan.Warnings)
	}
}

func anyContains(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func TestFromSourceEnginePoolCreated(t *testing.T) {
	code, plan := postFromSource(t, `{"name":"vless-only","source":"`+fakeVlessLink+`"}`)
	if code != 200 {
		t.Fatalf("код %d", code)
	}
	if plan.Pool != "vless-only" {
		t.Fatalf("пул движка должен создаваться: %+v", plan)
	}
	if len(plan.Engine) != 0 {
		t.Fatalf("рабочий узел не должен помечаться «ждёт lx»: %+v", plan.Engine)
	}
}

func TestFromSourceMixedLxNodeFlagged(t *testing.T) {
	lxOnly := "vless://00000000-0000-4000-8000-000000000001@203.0.113.12:443" +
		"?encryption=mlkem768x25519plus.native.0rtt.FAKE&type=xhttp&path=%2Fup&mode=auto" +
		"&security=tls&sni=cdn.example.org#lx-only-node"
	body, _ := json.Marshal(map[string]string{"name": "vless-lx", "source": fakeVlessLink + " \n" + lxOnly})
	code, plan := postFromSource(t, string(body))
	if code != 200 {
		t.Fatalf("код %d", code)
	}
	if plan.Pool != "vless-lx" {
		t.Fatalf("пул должен создаваться из поддерживаемых узлов: %+v", plan)
	}
	if len(plan.Engine) != 1 || plan.Engine[0].Tag != "manual|lx-only-node" {
		t.Fatalf("ждущий-lx список: %+v", plan.Engine)
	}
}

func TestFromSourceRejectsGarbage(t *testing.T) {
	code, _ := postFromSource(t, `{"name":"x","source":"абракадабра"}`)
	if code == 200 {
		t.Fatal("мусор не должен приниматься")
	}
	code, _ = postFromSource(t, `{"name":"x","source":""}`)
	if code == 200 {
		t.Fatal("пустой источник не должен приниматься")
	}
}
