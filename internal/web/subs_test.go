package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"mawg/internal/links"
)

const fakeVless = "vless://00000000-0000-4000-8000-000000000001@203.0.113.10:23747?encryption=none&fp=randomized&pbk=FAKE0public0key0for0tests0only0not0real0key00&security=reality&sid=deadbeef&sni=www.example.com&type=tcp#sub-test-node"

func TestSubsLifecycle(t *testing.T) {
	ts := newTestServer(t)

	resp, err := http.Get(ts.URL + "/api/v1/subs")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var list struct {
		Subs []storeSub `json:"subs"`
	}
	json.NewDecoder(resp.Body).Decode(&list)
	if len(list.Subs) != 0 {
		t.Fatalf("свежая панель не должна иметь подписок: %d", len(list.Subs))
	}

	resp, err = http.Post(ts.URL+"/api/v1/subs", "application/json",
		strings.NewReader(`{"source":"`+fakeVless+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("добавление: код %d", resp.StatusCode)
	}
	var added storeSub
	json.NewDecoder(resp.Body).Decode(&added)
	resp.Body.Close()
	if added.Name != "sub-test-node" || len(added.Nodes) != 1 {
		t.Fatalf("имя/узлы: %+v", added)
	}
	if added.Nodes[0].TLS == nil || added.Nodes[0].TLS.Security != "reality" {
		t.Fatalf("узел: %+v", added.Nodes[0])
	}

	dup, err := http.Post(ts.URL+"/api/v1/subs", "application/json",
		strings.NewReader(`{"source":"`+fakeVless+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	var dupSub storeSub
	json.NewDecoder(dup.Body).Decode(&dupSub)
	dup.Body.Close()
	if dupSub.Name == added.Name {
		t.Fatalf("дубликаты имён: %q", dupSub.Name)
	}

	resp, err = http.Post(ts.URL+"/api/v1/subs/"+added.Name+"/refresh", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("refresh: код %d", resp.StatusCode)
	}
	resp.Body.Close()

	del, err := httpDelete(ts.URL + "/api/v1/subs/" + added.Name)
	if err != nil {
		t.Fatal(err)
	}
	defer del.Body.Close()
	if del.StatusCode != 200 {
		t.Fatalf("delete: код %d", del.StatusCode)
	}

	resp, err = http.Get(ts.URL + "/api/v1/subs")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var after struct {
		Subs []storeSub `json:"subs"`
	}
	json.NewDecoder(resp.Body).Decode(&after)
	if len(after.Subs) != 1 || after.Subs[0].Name != dupSub.Name {
		t.Fatalf("после удаления: %+v", after.Subs)
	}
}

func TestSubsRejectsGarbage(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Post(ts.URL+"/api/v1/subs", "application/json",
		strings.NewReader(`{"source":"абракадабра без схемы"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		t.Fatal("мусорный источник не должен приниматься")
	}
	var e struct {
		Error string `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&e)
	if e.Error == "" {
		t.Fatal("должно быть сообщение об ошибке")
	}
}

type storeSub struct {
	Name   string       `json:"name"`
	Source string       `json:"source"`
	Nodes  []links.Node `json:"nodes"`
	Error  string       `json:"error"`
}

func httpDelete(url string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return nil, err
	}
	return http.DefaultClient.Do(req)
}
