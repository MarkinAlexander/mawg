package web

// Загрузка бинаря/пакета через HTTP (112-док 4.1/4.5, слово владельца
// 04.11: две кнопки, панель сама понимает ЧТО ей дали): фаза analyze
// (детект sing-box/mihomo/MT-пакета, ничего не меняется) и фаза apply.

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

func (e *uploadEnv) stagedPath() string {
	return filepath.Join(e.tmp, "mawg-core-upload.bin")
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
			// загруженный staged отвечает конвейеру той же версией
			filepath.Join(tmp, "mawg-core-upload.bin"): "1.14.4-lx.30",
		},
		// цель: 1-й вызов - prevVer (старая), 2-й - после копии (новая)
		targetNewAfter: 2,
	}
	if err := os.MkdirAll(filepath.Dir(env.target), 0o755); err != nil {
		t.Fatal(err)
	}

	oldDetect, oldVer, oldFree, oldTmp := swapDetectArch, swapVersionProvider, swapDiskFree, uploadTmpDir
	oldLX, oldMh, oldRun := swapLXTarget, swapMhTarget, swapRun
	swapDetectArch = func() (string, string, error) { return "amd64", "тест", nil }
	swapVersionProvider = func(string) func(string) (string, error) { return env.versionFn }
	swapRun = func(argv0 string, args ...string) ([]byte, error) {
		// детект загруженного файла: отвечает как sing-box version
		if argv0 == env.stagedPath() {
			return []byte("sing-box version " + env.versions["new"] + "\n"), nil
		}
		return nil, os.ErrInvalid
	}
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
		swapLXTarget, swapMhTarget, swapRun = oldLX, oldMh, oldRun
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, env
}

// uploadMultipartRaw - POST файла с произвольным содержимым.
func uploadMultipartRaw(t *testing.T, url string, name string, content []byte) (int, map[string]any) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(content); err != nil {
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

func elfContent(arch string, size int64) []byte {
	hdr := make([]byte, 20)
	copy(hdr, "\x7fELF")
	hdr[5] = 1
	machine := uint16(62) // amd64
	if arch == "mipsle-softfloat" {
		machine = 8
	}
	hdr[18], hdr[19] = byte(machine), byte(machine>>8)
	buf := bytes.Repeat([]byte{0}, int(size))
	copy(buf, hdr)
	return buf
}

func TestUploadAnalyzeDetectsWrongArch(t *testing.T) {
	ts, env := newUploadServer(t)
	code, out := uploadMultipartRaw(t, ts.URL+"/api/v1/cores/binary", "engine.bin", elfContent("mipsle-softfloat", 3<<20))
	if code != http.StatusOK || out["ok"] != false || !strings.Contains(errText(out), "архитектура не подходит") {
		t.Fatalf("code=%d out=%v", code, out)
	}
	if _, err := os.Stat(env.stagedPath()); !os.IsNotExist(err) {
		t.Fatal("staged не удалён после отказа")
	}
}

func TestUploadAnalyzeRejectsGarbage(t *testing.T) {
	ts, env := newUploadServer(t)
	_, out := uploadMultipartRaw(t, ts.URL+"/api/v1/cores/binary", "engine.bin", []byte("not-an-elf"))
	if out["ok"] != false || !strings.Contains(errText(out), "не бинарь") {
		t.Fatalf("ожидался отказ мусора: %v", out)
	}
	if _, err := os.Stat(env.stagedPath()); !os.IsNotExist(err) {
		t.Fatal("staged не удалён после отказа")
	}
}

func TestUploadAnalyzeDetectsMTPackage(t *testing.T) {
	ts, _ := newUploadServer(t)
	_, out := uploadMultipartRaw(t, ts.URL+"/api/v1/cores/binary",
		"magitrickle_0.8.2-19_openwrt_i386_pentium4.ipk", bytes.Repeat([]byte("p"), 300_000))
	if out["ok"] != true || out["kind"] != "magitrickle" {
		t.Fatalf("пакет MT не распознан: %v", out)
	}
	if !strings.Contains(out["label"].(string), "MagiTrickle") {
		t.Fatalf("label: %v", out["label"])
	}
}

func TestUploadAnalyzeThenApplySingbox(t *testing.T) {
	ts, env := newUploadServer(t)
	// старый бинарь на месте
	if err := os.WriteFile(env.target, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, out := uploadMultipartRaw(t, ts.URL+"/api/v1/cores/binary", "engine.bin", elfContent("amd64", 3<<20))
	if out["ok"] != true || out["kind"] != "singbox" {
		t.Fatalf("детект: %v", out)
	}
	if v, _ := out["version"].(string); v != "1.14.4-lx.30" {
		t.Fatalf("версия детекта: %v", out["version"])
	}

	// подтверждение пользователя -> apply
	resp, err := http.Post(ts.URL+"/api/v1/cores/binary/apply",
		"application/json", strings.NewReader(`{"backup":true}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var res map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatal(err)
	}
	if res["ok"] != true {
		t.Fatalf("apply не прошёл: %v (лог: %v)", res, res["output"])
	}
	if res["version"] != "1.14.4-lx.30" || res["previous"] != "1.14.2-lx.12" {
		t.Fatalf("результат: %v", res)
	}
	// повторный apply без новой загрузки - отказ
	resp2, _ := http.Post(ts.URL+"/api/v1/cores/binary/apply",
		"application/json", strings.NewReader(`{"backup":true}`))
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("повторный apply должен отказывать, code=%d", resp2.StatusCode)
	}
}

func errText(out map[string]any) string {
	if s, ok := out["error"].(string); ok {
		return s
	}
	return ""
}
