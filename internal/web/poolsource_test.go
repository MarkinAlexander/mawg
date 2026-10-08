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

func TestFromSourceEngineOnlyCreatesNothing(t *testing.T) {
	code, plan := postFromSource(t, `{"name":"vless-only","source":"`+fakeVlessLink+`","keeneticSlot":"Wireguard2"}`)
	if code != 200 {
		t.Fatalf("код %d", code)
	}
	if plan.Pool != "" || plan.Added != 0 {
		t.Fatalf("пул не должен создаваться: %+v", plan)
	}
	if len(plan.Engine) != 1 {
		t.Fatalf("движок-узлы: %+v", plan.Engine)
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
