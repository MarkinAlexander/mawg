package web

import (
	"context"
	"encoding/json"
	"net/http"
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
		OK   bool   `json:"ok"`
		Tag  string `json:"tag"`
		Log  string `json:"output"`
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
