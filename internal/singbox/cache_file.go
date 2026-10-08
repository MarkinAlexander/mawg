package singbox

import (
	"encoding/json"
	"fmt"
	"strings"
)

// CacheTmpPath - куда переносится cache_file в режиме "tmp": база clash
// (селекторы, fakeip, urltest) пишет себя через mmap постоянно, на флешке
// это ~170МБ износа в сутки; в /tmp записи идут в RAM.
const CacheTmpPath = "/tmp/sing-box/cache.db"

// Режимы обращения с кэшем ядра: off - выключить совсем (ничего не пишется,
// выборы сбрасываются при каждом рестарте ядра), tmp - перенести в RAM
// (не пишется на флешку, выборы живут до ребута роутера).
const (
	CacheModeOff = "off"
	CacheModeTmp = "tmp"
)

// editCacheFile правит кэш ядра в конфиге, поддерживая обе схемы: 1.14 -
// experimental.cache_file {enabled, path} (без path ядро кладёт cache.db в
// свой рабочий каталог), 1.13 - строка experimental.clash_api.cache_file.
// Возвращает новый конфиг, прежний эффективный путь и признак изменения;
// выключенный кэш (off) или уже в /tmp (tmp) - без изменения.
func editCacheFile(data []byte, mode string) (out []byte, oldPath string, changed bool, err error) {
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, "", false, fmt.Errorf("config.json не разбирается: %w", err)
	}
	exp, ok := cfg["experimental"].(map[string]any)
	if !ok {
		return nil, "", false, fmt.Errorf("в конфиге нет секции experimental")
	}

	if cf, ok := exp["cache_file"].(map[string]any); ok {
		enabled, _ := cf["enabled"].(bool)
		old, _ := cf["path"].(string)
		if !enabled {
			return nil, "", false, fmt.Errorf("cache_file уже выключен (enabled: false)")
		}
		if old != "" && strings.HasPrefix(old, "/tmp") && mode == CacheModeTmp {
			return data, old, false, nil
		}
		if old == "" {
			old = "cache.db в рабочем каталоге ядра"
		}
		switch mode {
		case CacheModeOff:
			cf["enabled"] = false
		case CacheModeTmp:
			cf["path"] = CacheTmpPath
		default:
			return nil, "", false, fmt.Errorf("неизвестный режим %q (off или tmp)", mode)
		}
		if out, err = json.MarshalIndent(cfg, "", "  "); err != nil {
			return nil, "", false, err
		}
		return out, old, true, nil
	}

	if clash, ok := exp["clash_api"].(map[string]any); ok {
		old, _ := clash["cache_file"].(string)
		if strings.TrimSpace(old) == "" {
			return nil, "", false, fmt.Errorf("в конфиге нет cache_file - и так ничего не пишется")
		}
		if strings.HasPrefix(old, "/tmp") && mode == CacheModeTmp {
			return data, old, false, nil
		}
		switch mode {
		case CacheModeOff:
			delete(clash, "cache_file")
		case CacheModeTmp:
			clash["cache_file"] = CacheTmpPath
		default:
			return nil, "", false, fmt.Errorf("неизвестный режим %q (off или tmp)", mode)
		}
		if out, err = json.MarshalIndent(cfg, "", "  "); err != nil {
			return nil, "", false, err
		}
		return out, old, true, nil
	}

	return nil, "", false, fmt.Errorf("в конфиге нет cache_file - и так ничего не пишется")
}
