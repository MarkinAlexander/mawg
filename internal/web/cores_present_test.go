package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"mawg/internal/magitrickle"
	"mawg/internal/platform/fake"
	"mawg/internal/rotator"
	"mawg/internal/store"
)

// жалоба 24.11: на роутере без памяти version-зонд ядра убивает OOM -
// карточка «Ядра VPN» показывала «не установлен» при стоящем ядре и звала
// ставить его заново
func TestCorePresentButUnresponsive(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fb := fake.New()
	srv := New(st, rotator.New(st, fb, magitrickle.New("http://127.0.0.1:1")), fb, nil, "test", nil)

	oldBin, oldMhBin := binPresent, mhBinPresent
	oldDetect, oldDetectOK, oldAt := coreDetect, coreDetectOK, coreDetectAt
	oldMh, oldMhOK, oldMhAt := mhDetect, mhDetectOK, mhDetectAt
	t.Cleanup(func() {
		binPresent, mhBinPresent = oldBin, oldMhBin
		coreDetect, coreDetectOK, coreDetectAt = oldDetect, oldDetectOK, oldAt
		mhDetect, mhDetectOK, mhDetectAt = oldMh, oldMhOK, oldMhAt
	})
	coreDetect, coreDetectOK, coreDetectAt = nil, false, time.Now()
	mhDetect, mhDetectOK, mhDetectAt = nil, false, time.Now()
	binPresent = func() (string, bool) { return "/usr/bin/sing-box", true }
	mhBinPresent = func() (string, bool) { return "/usr/bin/mihomo", true }

	v := srv.singboxCoreView()
	if !v.Installed || !strings.Contains(v.Note, "/usr/bin/sing-box на месте") || v.Caps.Install {
		t.Errorf("sing-box на месте, но глух: жду установленное ядро без предложения ставить: %+v", v)
	}
	if !strings.Contains(v.LastError, "мало памяти") {
		t.Errorf("жду честную причину в LastError: %q", v.LastError)
	}
	m := srv.mihomoCoreView()
	if !m.Installed || !strings.Contains(m.Note, "/usr/bin/mihomo на месте") || m.Caps.Install {
		t.Errorf("mihomo на месте, но глух: жду установленное ядро без предложения ставить: %+v", m)
	}

	// бинаря нет - как прежде: «не установлен»
	binPresent = func() (string, bool) { return "", false }
	mhBinPresent = func() (string, bool) { return "", false }
	if v = srv.singboxCoreView(); v.Installed || !strings.Contains(v.Note, "не установлен") {
		t.Errorf("без бинаря жду «не установлен»: %+v", v)
	}
	if m = srv.mihomoCoreView(); m.Installed || !m.Caps.Install || !strings.Contains(m.Note, "не установлен") {
		t.Errorf("без бинаря mihomo жду «не установлен» с кнопкой установки: %+v", m)
	}
}

// тот же кейс в /status: «available» = «ядро установлено» - диалог пула по
// нему решает «на роутере его нет, поставить сейчас»; ready:false говорит,
// что в выбор движка его предлагать нельзя
func TestStatusCorePresentButUnresponsive(t *testing.T) {
	oldBin, oldMhBin := binPresent, mhBinPresent
	binPresent = func() (string, bool) { return "/usr/bin/sing-box", true }
	mhBinPresent = func() (string, bool) { return "/usr/bin/mihomo", true }
	defer func() { binPresent, mhBinPresent = oldBin, oldMhBin }()
	ts := newTestServer(t)

	resp, err := http.Get(ts.URL + "/api/v1/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Engine  map[string]any            `json:"engine"`
		Engines map[string]map[string]any `json:"engines"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	for id, eng := range out.Engines {
		if eng["available"] != true || eng["ready"] != false {
			t.Errorf("%s: жду available=true, ready=false: %v", id, eng)
		}
	}
	if out.Engine["available"] != true || out.Engine["ready"] != false {
		t.Errorf("engine (копия для совместимости): %v", out.Engine)
	}
}
