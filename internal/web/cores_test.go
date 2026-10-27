package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mawg/internal/mihomo"
)

type coreViewT struct {
	ID        string `json:"id"`
	Installed bool   `json:"installed"`
	Disabled  bool   `json:"disabled"`
	Running   bool   `json:"running"`
	Note      string `json:"note"`
	Caps      struct {
		Start     bool `json:"start"`
		Stop      bool `json:"stop"`
		Uninstall bool `json:"uninstall"`
		Install   bool `json:"install"`
	} `json:"caps"`
}

func getCores(t *testing.T, ts *httptest.Server) []coreViewT {
	t.Helper()
	resp, err := http.Get(ts.URL + "/api/v1/cores")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Cores []coreViewT `json:"cores"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out.Cores
}

// 88-док: список ядер структурный, mihomo - второй элемент; на стенде без
// установленного mihomo строка честная: не установлен + капа установки.
func TestCoresListHasMihomo(t *testing.T) {
	cores := getCores(t, newTestServer(t))
	if len(cores) != 2 {
		t.Fatalf("два ядра: %+v", cores)
	}
	if cores[0].ID != "singbox" || cores[1].ID != "mihomo" {
		t.Fatalf("порядок ядер: %+v", cores)
	}
	mh := cores[1]
	if mh.Installed || !mh.Caps.Install {
		t.Fatalf("mihomo не установлен - капа установки: %+v", mh)
	}
	if mh.Caps.Stop || mh.Caps.Uninstall || mh.Running {
		t.Fatalf("неустановленному ядру лишние капы: %+v", mh)
	}
}

func TestMihomoPowerStopStartPerCore(t *testing.T) {
	ts := newTestServer(t)
	// stop: свой флаг ядра, singbox не задевается
	resp, err := http.Post(ts.URL+"/api/v1/cores/mihomo/power", "application/json",
		strings.NewReader(`{"action":"stop"}`))
	if err != nil {
		t.Fatal(err)
	}
	var out struct{ Core coreViewT }
	json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if resp.StatusCode != 200 || !out.Core.Disabled {
		t.Fatalf("stop mihomo: код %d core %+v", resp.StatusCode, out.Core)
	}
	cores := getCores(t, ts)
	if cores[0].Disabled {
		t.Fatal("стоп mihomo не должен выключать sing-box (стоп/старт по ядру)")
	}
	// start: пулов mihomo нет - менеджер не нужен, просто снять флаг
	resp, err = http.Post(ts.URL+"/api/v1/cores/mihomo/power", "application/json",
		strings.NewReader(`{"action":"start"}`))
	if err != nil {
		t.Fatal(err)
	}
	json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if resp.StatusCode != 200 || out.Core.Disabled {
		t.Fatalf("start mihomo: код %d core %+v", resp.StatusCode, out.Core)
	}
}

func TestMihomoPowerUninstallForeign(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Post(ts.URL+"/api/v1/cores/mihomo/power", "application/json",
		strings.NewReader(`{"action":"uninstall"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == 200 {
		t.Fatal("удаление неустановленного mawg mihomo должно отказывать")
	}
}

func TestMihomoPowerUnknown(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Post(ts.URL+"/api/v1/cores/xray/power", "application/json",
		strings.NewReader(`{"action":"start"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == 200 {
		t.Fatal("неизвестное ядро должно отклоняться")
	}
}

func TestInstallMihomoRoute(t *testing.T) {
	ts := newTestServer(t)
	prev := mihomoInstallCore
	called := false
	mihomoInstallCore = func(ctx context.Context, opts mihomo.InstallOptions) (mihomo.InstallResult, error) {
		called = true
		if opts.Target.Bin == "" {
			t.Fatal("таргет не передан")
		}
		return mihomo.InstallResult{Tag: "v1.19.33", Version: "v1.19.33", Bin: opts.Target.Bin, Log: "- ок\n"}, nil
	}
	t.Cleanup(func() { mihomoInstallCore = prev })
	resp, err := http.Post(ts.URL+"/api/v1/system/install", "application/json",
		strings.NewReader(`{"id":"mihomo"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		OK      bool   `json:"ok"`
		Version string `json:"version"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != 200 || !out.OK || !called || out.Version != "v1.19.33" {
		t.Fatalf("установка mihomo: код %d %+v called=%v", resp.StatusCode, out, called)
	}
}

// пул из ссылки с engineCore=mihomo без установленного ядра - честный отказ
func TestFromSourceMihomoCoreNotInstalled(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Post(ts.URL+"/api/v1/pools/from-source", "application/json",
		strings.NewReader(`{"name":"mh","source":"`+fakeVlessLink+`","engineCore":"mihomo"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == 200 || !strings.Contains(string(body), "mihomo") {
		t.Fatalf("без установленного mihomo - честный отказ: код %d %s", resp.StatusCode, body)
	}
}

func TestFromSourceMihomoCoreInvalid(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Post(ts.URL+"/api/v1/pools/from-source", "application/json",
		strings.NewReader(`{"name":"mh","source":"`+fakeVlessLink+`","engineCore":"xray"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == 200 {
		t.Fatal("неизвестное ядро должно отклоняться")
	}
}
