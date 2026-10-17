package singbox

import (
	"encoding/json"
	"fmt"
	"strings"
)

// editClashAPI включает clash_api в чужом config.json (без него mawg не
// может ни пробовать узлы, ни переключать их) и разгружает inbound'ы,
// чьи listen_port заняты службами роутера (пакетный дефолт sing-box
// слушает shadowsocks на :::8080 - это API MagiTrickle). Существующий
// external_controller не трогается. pick подбирает свободный порт для
// переноса; возвращает новый конфиг, признак изменения и примечание для
// журнала событий.
func editClashAPI(data []byte, port int, reserved map[int]string, pick func() int) (out []byte, changed bool, note string, err error) {
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, false, "", fmt.Errorf("config.json не разбирается: %w", err)
	}
	var notes []string

	exp, _ := cfg["experimental"].(map[string]any)
	if exp == nil {
		exp = map[string]any{}
		cfg["experimental"] = exp
	}
	clash, _ := exp["clash_api"].(map[string]any)
	if clash == nil {
		clash = map[string]any{}
		exp["clash_api"] = clash
	}
	if cur, _ := clash["external_controller"].(string); cur == "" {
		clash["external_controller"] = fmt.Sprintf("127.0.0.1:%d", port)
		notes = append(notes, fmt.Sprintf("clash_api включён (127.0.0.1:%d): без него пробы и переключение узлов невозможны", port))
	}

	if len(reserved) > 0 {
		if inbounds, ok := cfg["inbounds"].([]any); ok {
			for _, ib := range inbounds {
				in, ok := ib.(map[string]any)
				if !ok {
					continue
				}
				lp, ok := in["listen_port"].(float64)
				if !ok {
					continue
				}
				why, busy := reserved[int(lp)]
				if !busy {
					continue
				}
				np := pick()
				if np == 0 {
					return nil, false, "", fmt.Errorf("inbound %q слушает порт %d (%s), свободного порта для переноса не нашлось", tagOf(in), int(lp), why)
				}
				in["listen_port"] = np
				notes = append(notes, fmt.Sprintf("inbound %s: порт %d освобождён (%s), перенесён на %d", tagOf(in), int(lp), why, np))
			}
		}
	}

	if len(notes) == 0 {
		return data, false, "", nil
	}
	if out, err = json.MarshalIndent(cfg, "", "  "); err != nil {
		return nil, false, "", err
	}
	return out, true, strings.Join(notes, "; "), nil
}

func tagOf(in map[string]any) string {
	if t, _ := in["tag"].(string); t != "" {
		return fmt.Sprintf("%q", t)
	}
	return "без тега"
}
