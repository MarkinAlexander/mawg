package web

// Upload бинаря движка через HTTP (112-док 4.1/4.5): multipart-приём,
// отказ чужой архитектуры без последствий, полный конвейер по фейкам.

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mawg/internal/magitrickle"
	"mawg/internal/mihomo"
	"mawg/internal/platform/fake"
	"mawg/internal/rotator"
	"mawg/internal/singbox"
	"mawg/internal/store"
)

type uploadEnv struct {
	tmp       string // «tmpfs»
	target    string // целевой бинарь
	markerDir string
	versions  map[string]string
	// targetNewAfter - с какого обращения к цели VersionFn отвечает
	// новой версией (симуляция: бинарь на месте уже заменён)
	targetNewAfter int
	targetCalls    int
}

func (e *uploadEnv) stagedPath(core string) string {
	return filepath.Join(e.tmp, "mawg-core-upload-"+core+".bin")
}

func (e *uploadEnv) versionFn(bin string) (string, error) {
	if bin == e.target {
		e.targetCalls++
		if e.targetCalls >= e.targetNewAfter {
			return e.versions["new"], nil
		}
		if v, ok := e.versions["old"]; ok {
			return v, nil
		}
		return "", os.ErrInvalid
	}
	if v, ok := e.versions[bin]; ok {
		return v, nil
	}
	return "", os.ErrInvalid
}

func newUploadServer(t *testing.T) (*httptest.Server, *uploadEnv) {
	t.Helper()
	tmp := t.TempDir()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fb := fake.New()
	e := rotator.New(st, fb, magitrickle.New("http://127.0.0.1:1"))
	srv := New(st, e, fb, nil, "test", nil)

	env := &uploadEnv{
		tmp:       tmp,
		target:    filepath.Join(tmp, "target", "engine"),
		markerDir: filepath.Join(tmp, "marker"),
		versions: map[string]string{
			"new": "1.14.4-lx.30", "old": "1.14.2-lx.12",
			filepath.Join(tmp, "mawg-core-upload-singbox.bin"): "1.14.4-lx.30",
			filepath.Join(tmp, "mawg-core-upload-mihomo.bin"):  "1.19.4",
		},
		// цель: 1-й вызов - prevVer (старая), 2-й - после копии (новая)
		targetNewAfter: 2,
	}
	if err := os.MkdirAll(filepath.Dir(env.target), 0o755); err != nil {
		t.Fatal(err)
	}

	oldDetect, oldVer, oldFree, oldTmp := swapDetectArch, swapVersionProvider, swapDiskFree, uploadTmpDir
	oldLX, oldMh := swapLXTarget, swapMhTarget
	swapDetectArch = func() (string, string, error) { return "amd64", "тест", nil }
	swapVersionProvider = func(string) func(string) (string, error) { return env.versionFn }
	swapDiskFree = func(string) int64 { return 1 << 30 }
	uploadTmpDir = tmp
	swapLXTarget = func(string) singbox.LXTarget {
		return singbox.LXTarget{Bin: env.target, MarkerDir: env.markerDir}
	}
	swapMhTarget = func(string) mihomo.Target {
		return mihomo.Target{Bin: env.target, MarkerDir: env.markerDir}
	}
	t.Cleanup(func() {
		swapDetectArch, swapVersionProvider, swapDiskFree, uploadTmpDir = oldDetect, oldVer, oldFree, oldTmp
		swapLXTarget, swapMhTarget = oldLX, oldMh
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, env
}

func uploadMultipart(t *testing.T, url string, mips bool, size int64, backup string) (int, map[string]any) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if backup != "" {
		if err := mw.WriteField("backup", backup); err != nil {
			t.Fatal(err)
		}
	}
	fw, err := mw.CreateFormFile("file", "engine.bin")
	if err != nil {
		t.Fatal(err)
	}
	hdr := make([]byte, 20)
	copy(hdr, "\x7fELF")
	hdr[5] = 1
	machine := uint16(62) // amd64
	if mips {
		machine = 8
	}
	hdr[18], hdr[19] = byte(machine), byte(machine>>8)
	if _, err := fw.Write(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(bytes.Repeat([]byte{0}, int(size)-len(hdr))); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(url, mw.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("ответ не json: %s", data)
	}
	return resp.StatusCode, out
}

func TestPostCoreBinaryUnknownCore(t *testing.T) {
	ts, _ := newUploadServer(t)
	code, out := uploadMultipart(t, ts.URL+"/api/v1/cores/vpn/binary", false, 3<<20, "")
	if code != http.StatusBadRequest || !strings.Contains(errText(out), "неизвестное ядро") {
		t.Fatalf("code=%d out=%v", code, out)
	}
}

func TestPostCoreBinaryRejectsWrongArchOverHTTP(t *testing.T) {
	ts, env := newUploadServer(t)
	code, out := uploadMultipart(t, ts.URL+"/api/v1/cores/singbox/binary", true, 3<<20, "1")
	if code != http.StatusOK || out["ok"] != false {
		t.Fatalf("ожидался ok:false, code=%d out=%v", code, out)
	}
	if !strings.Contains(errText(out), "архитектура не подходит") {
		t.Fatalf("текст ошибки: %v", out)
	}
	if _, err := os.Stat(env.stagedPath("singbox")); !os.IsNotExist(err) {
		t.Fatal("staged не удалён после отказа")
	}
}

func TestPostCoreBinarySuccessOverHTTP(t *testing.T) {
	ts, env := newUploadServer(t)
	// старый бинарь на месте
	if err := os.WriteFile(env.target, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	code, out := uploadMultipart(t, ts.URL+"/api/v1/cores/singbox/binary", false, 3<<20, "1")
	if code != http.StatusOK || out["ok"] != true {
		t.Fatalf("code=%d out=%v (лог: %v)", code, out, out["output"])
	}
	if out["version"] != "1.14.4-lx.30" || out["previous"] != "1.14.2-lx.12" {
		t.Fatalf("результат: %v", out)
	}
	if _, err := os.Stat(env.target); err != nil {
		t.Fatalf("новый бинарь не на месте: %v", err)
	}
	marker, err := os.ReadFile(filepath.Join(env.markerDir, singbox.LXMarkerName))
	if err != nil || !strings.Contains(string(marker), "source=upload") {
		t.Fatalf("маркер не записан: %v", err)
	}
}

func errText(out map[string]any) string {
	if s, ok := out["error"].(string); ok {
		return s
	}
	return ""
}
