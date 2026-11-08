package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"time"

	"mawg/internal/store"
	"mawg/internal/wgconf"
)

// export sanitized - обезличенный дамп для анализа (док 114): config.json и
// пул-конфиги без секретов, но с полным функционалом (эндпоинты, AWG-параметры,
// настройки проб). Согласован с web-диагностикой (enginediag.go): приватные
// ключи/PSK/пароли/токены -> ***, base64-ключи 43+ и UUID -> ***.
var (
	// подстрока в имени JSON-ключа (rciToken, authEnabled не путать:
	// bool-поля не маскируются, маска только строковым значениям)
	reSecretName = regexp.MustCompile(`(?i)(password|passwd|secret|private|preshared|token|authorization|cookie|session|bearer|apikey|api[_-]?key)`)
	reUUID       = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	reB64Key     = regexp.MustCompile(`[A-Za-z0-9+/]{43,}={0,2}`)
)

const redacted = "***"

func sanitizedValueString(v any) any {
	switch t := v.(type) {
	case string:
		if reB64Key.MatchString(t) || reUUID.MatchString(t) {
			return redacted
		}
		return t
	case map[string]any:
		for k, val := range t {
			if reSecretName.MatchString(k) {
				if _, isStr := val.(string); isStr || val == nil {
					t[k] = redacted
					continue
				}
			}
			t[k] = sanitizedValueString(val)
		}
		return t
	case []any:
		for i := range t {
			t[i] = sanitizedValueString(t[i])
		}
		return t
	default:
		return v
	}
}

// sanitizedConfig маскирует только секретные поля структуры (PrivateKey,
// PresharedKey); AWG-параметры (I1/I2/H1-H4...) не секреты - оставляются,
// без них анализ функционала AWG бесполезен.
func sanitizedConfig(c wgconf.Config) map[string]any {
	b, _ := json.Marshal(c)
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return map[string]any{"error": "пересборка конфига не удалась"}
	}
	m["PrivateKey"] = redacted
	if peer, ok := m["Peer"].(map[string]any); ok {
		peer["PresharedKey"] = redacted
	}
	return m
}

// cmdExport - "mawg export sanitized [пул|all] [файл|-]": JSON-дамп базы
// для передачи в анализ (нейросети/чаты) без риска утечки ключей.
func cmdExport(dir string, args []string) int {
	wantPool := "all"
	out := ""
	if len(args) > 0 {
		wantPool = args[0]
	}
	if len(args) > 1 {
		out = args[1]
	}

	st, err := store.Open(dir)
	if err != nil {
		fmt.Println("store:", err)
		return 1
	}

	rootJSON, err := st.ConfigJSON()
	if err != nil {
		fmt.Println("config:", err)
		return 1
	}
	var root map[string]any
	if json.Unmarshal(rootJSON, &root) != nil {
		fmt.Println("config.json не читается как JSON")
		return 1
	}
	delete(root, "pools") // пулы идут отдельной секцией с санитайзом конфигов

	dump := map[string]any{
		"generated": time.Now().Format(time.RFC3339),
		"platform":  platLabel(),
		"config":    sanitizedValueString(root),
		"pools":     []map[string]any{},
	}
	pools := []map[string]any{}
	for _, p := range st.Pools() {
		if wantPool != "all" && p.Name != wantPool {
			continue
		}
		entry := map[string]any{
			"name":     p.Name,
			"free":     p.Free,
			"premium":  p.Premium,
			"disabled": p.Disabled,
			"settings": sanitizedValueString(p.Settings),
			"configs":  []map[string]any{},
		}
		if stt := st.State(p.Name); stt != nil {
			entry["state"] = map[string]any{
				"activeFile": stt.ActiveFile,
				"mode":       stt.Mode,
				"rotations":  stt.Rotations,
			}
		}
		configs := []map[string]any{}
		for _, cfg := range p.Configs {
			c := map[string]any{"file": cfg.File}
			parsed, err := st.LoadConfigFile(p.Name, cfg.File)
			if err != nil {
				c["error"] = err.Error()
			} else {
				c["config"] = sanitizedConfig(parsed)
			}
			configs = append(configs, c)
		}
		entry["configs"] = configs
		pools = append(pools, entry)
	}
	dump["pools"] = pools

	final, err := json.MarshalIndent(dump, "", "  ")
	if err != nil {
		fmt.Println("marshal:", err)
		return 1
	}
	if out == "" || out == "-" {
		fmt.Println(string(final))
		return 0
	}
	if err := os.WriteFile(out, final, 0o600); err != nil {
		fmt.Println("запись:", err)
		return 1
	}
	fmt.Printf("обезличенный дамп записан: %s (%d пул(ов))\n", out, len(pools))
	return 0
}
