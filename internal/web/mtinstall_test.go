package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mawg/internal/mtinstall"
)

func TestSystemInstallMagitrickle(t *testing.T) {
	ts := newTestServer(t)
	platform := ""
	orig := mtInstall
	mtVersionWait = 0
	t.Cleanup(func() {
		mtInstall = orig
		mtVersionWait = 15 * time.Second
	})
	mtInstall = func(_ context.Context, opts mtinstall.Options) (mtinstall.Result, error) {
		platform = opts.Platform
		return mtinstall.Result{
			Tag:   "0.8.2-rev9",
			Asset: "magitrickle_0.8.2-9_openwrt_x86_64.ipk",
			Bin:   "/usr/bin/magitrickled",
			Log:   "- служба запущена",
		}, nil
	}
	resp, err := http.Post(ts.URL+"/api/v1/system/install", "application/json", strings.NewReader(`{"id":"magitrickle"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status code %d", resp.StatusCode)
	}
	var out struct {
		OK      bool   `json:"ok"`
		Tag     string `json:"tag"`
		Log     string `json:"output"`
		Warning string `json:"warning"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if platform != "fake" {
		t.Fatalf("installer не вызван или платформа %q", platform)
	}
	if !out.OK || out.Tag != "0.8.2-rev9" || !strings.Contains(out.Log, "запущена") {
		t.Fatalf("ответ: %+v", out)
	}
	// MT в тестах не отвечает - предупреджение о непрочитанной версии обязательно
	if !strings.Contains(out.Warning, "не прочитана") {
		t.Fatalf("warning: %q", out.Warning)
	}
}

func TestSystemInstallMagitrickleError(t *testing.T) {
	ts := newTestServer(t)
	orig := mtInstall
	mtVersionWait = 0
	t.Cleanup(func() {
		mtInstall = orig
		mtVersionWait = 15 * time.Second
	})
	mtInstall = func(context.Context, mtinstall.Options) (mtinstall.Result, error) {
		return mtinstall.Result{Log: "- halfway"}, context.DeadlineExceeded
	}
	resp, err := http.Post(ts.URL+"/api/v1/system/install", "application/json", strings.NewReader(`{"id":"magitrickle"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
		Log   string `json:"output"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.OK || out.Error == "" || !strings.Contains(out.Log, "halfway") {
		t.Fatalf("ответ при ошибке: %+v", out)
	}
}

// пакет той же версии (переустановлен принудительно): равенство версий
// неизбежно - без ожидания смены и без «бинарь мог не замениться» (0.62,
// 24.11: переустановка rev23 отвечала предупреждением через 15с)
func TestSystemInstallMagitrickleReinstalledSameVersion(t *testing.T) {
	mt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/version" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"0.8.2-rev23-mawg-dev"}`))
	}))
	t.Cleanup(mt.Close)
	ts := newTestServerWithMT(t, mt.URL)
	origInstall, origWait := mtInstall, mtVersionWait
	t.Cleanup(func() { mtInstall, mtVersionWait = origInstall, origWait })
	mtVersionWait = 1500 * time.Millisecond

	post := func(reinstalled bool) (warning string, took time.Duration) {
		mtInstall = func(context.Context, mtinstall.Options) (mtinstall.Result, error) {
			return mtinstall.Result{Tag: "0.8.2-rev23", SameVersion: reinstalled}, nil
		}
		started := time.Now()
		resp, err := http.Post(ts.URL+"/api/v1/system/install", "application/json", strings.NewReader(`{"id":"magitrickle"}`))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out struct {
			OK      bool   `json:"ok"`
			Version string `json:"version"`
			Warning string `json:"warning"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		if !out.OK || out.Version != "0.8.2-rev23-mawg-dev" {
			t.Fatalf("ответ: %+v", out)
		}
		return out.Warning, time.Since(started)
	}

	if warn, took := post(true); warn != "" || took > time.Second {
		t.Errorf("переустановка той же версии: предупреждение %q за %s", warn, took)
	}
	if warn, _ := post(false); !strings.Contains(warn, "прежней версией") {
		t.Errorf("обычная установка с прежней версией должна предупреждать: %q", warn)
	}
}
