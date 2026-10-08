package singbox

import (
	"encoding/json"
	"strings"
	"testing"
)

const cfgWithCache14 = `{
  "experimental": {
    "cache_file": {"enabled": true}
  },
  "outbounds": [{"type": "direct", "tag": "direct"}]
}`

const cfgWithCache = `{
  "log": {"level": "info"},
  "experimental": {
    "clash_api": {
      "external_controller": "0.0.0.0:9090",
      "cache_file": "/opt/var/lib/sing-box/cache.db",
      "store_selected": true
    }
  },
  "outbounds": [{"type": "direct", "tag": "direct"}]
}`

func TestEditCacheFileToTmp(t *testing.T) {
	out, old, changed, err := editCacheFile([]byte(cfgWithCache), CacheModeTmp)
	if err != nil || !changed {
		t.Fatalf("err=%v changed=%v", err, changed)
	}
	if old != "/opt/var/lib/sing-box/cache.db" {
		t.Fatalf("old path: %q", old)
	}
	var cfg map[string]any
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("результат не json: %v", err)
	}
	exp := cfg["experimental"].(map[string]any)
	clash := exp["clash_api"].(map[string]any)
	if clash["cache_file"] != CacheTmpPath {
		t.Fatalf("cache_file не заменён: %v", clash["cache_file"])
	}
	if clash["store_selected"] != true || clash["external_controller"] != "0.0.0.0:9090" {
		t.Fatalf("соседние поля потеряны: %v", clash)
	}
	if len(cfg["outbounds"].([]any)) != 1 {
		t.Fatalf("outbounds потеряны")
	}
}

func TestEditCacheFileToTmpSchema14(t *testing.T) {
	// схема 1.14: cache_file - объект без path (путь по умолчанию = рабочий каталог)
	out, old, changed, err := editCacheFile([]byte(cfgWithCache14), CacheModeTmp)
	if err != nil || !changed {
		t.Fatalf("err=%v changed=%v", err, changed)
	}
	if old != "cache.db в рабочем каталоге ядра" {
		t.Fatalf("old: %q", old)
	}
	var cfg map[string]any
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("не json: %v", err)
	}
	cf := cfg["experimental"].(map[string]any)["cache_file"].(map[string]any)
	if cf["path"] != CacheTmpPath || cf["enabled"] != true {
		t.Fatalf("cache_file: %v", cf)
	}
	// уже в /tmp - no-op
	noop, _, changed2, _ := editCacheFile(out, CacheModeTmp)
	if changed2 || string(noop) != string(out) {
		t.Fatalf("no-op не сработал: changed=%v", changed2)
	}
	// выключенный кэш
	disabled := strings.Replace(cfgWithCache14, `"enabled": true`, `"enabled": false`, 1)
	if _, _, _, err := editCacheFile([]byte(disabled), CacheModeOff); err == nil || !strings.Contains(err.Error(), "уже выключен") {
		t.Fatalf("ожидалась ошибка выключенного кэша: %v", err)
	}
}

func TestEditCacheFileToTmpNoopAndErrors(t *testing.T) {
	// уже в /tmp - без изменения
	out, old, changed, err := editCacheFile([]byte(strings.Replace(cfgWithCache, "/opt/var/lib/sing-box/cache.db", "/tmp/x.db", 1)), CacheModeTmp)
	if err != nil || changed || old != "/tmp/x.db" || string(out) != strings.Replace(cfgWithCache, "/opt/var/lib/sing-box/cache.db", "/tmp/x.db", 1) {
		t.Fatalf("noop: changed=%v old=%q", changed, old)
	}
	// нет cache_file
	noCache := strings.Replace(cfgWithCache, `"cache_file": "/opt/var/lib/sing-box/cache.db",`+"\n", "", 1)
	if _, _, _, err := editCacheFile([]byte(noCache), CacheModeTmp); err == nil || !strings.Contains(err.Error(), "нет cache_file") {
		t.Fatalf("ожидалась ошибка отсутствия cache_file: %v", err)
	}
	// битый json
	if _, _, _, err := editCacheFile([]byte("{"), CacheModeTmp); err == nil {
		t.Fatal("битый json должен давать ошибку")
	}
}
