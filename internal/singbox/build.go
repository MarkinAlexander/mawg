package singbox

import (
	"encoding/json"
	"fmt"

	"mawg/internal/links"
)

type PoolSpec struct {
	Name             string
	Tun              string
	TunIP            string
	MixedPort        int
	ProbeTarget      string
	CheckIntervalSec int
	FailThreshold    int
	CooldownMin      int
	MaxRTTms         int
	Nodes            []links.Node
	GroupMode        string // "urltest" (по умолчанию) | "selector"
}

type Params struct {
	ClashPort int
	LX        bool
	Merged    bool
}

// EligibleNodes - узлы, которые текущий профиль движка умеет запустить
func EligibleNodes(nodes []links.Node, lx bool) (ok []links.Node, reasons []string) {
	for _, n := range nodes {
		ob, good, reason := nodeOutbound("probe", n, lx)
		_ = ob
		if good {
			ok = append(ok, n)
		} else {
			reasons = append(reasons, reason)
		}
	}
	return ok, reasons
}

func nodeOutbound(poolTag string, n links.Node, lx bool) (map[string]any, bool, string) {
	skip := func(reason string) (map[string]any, bool, string) { return nil, false, reason }
	switch n.Type {
	case "vless", "trojan":
	default:
		return skip(fmt.Sprintf("%s: тип %s идёт нативным пулом", n.Tag, n.Type))
	}
	if n.Encryption != "" && n.Encryption != "none" && !lx {
		return skip(fmt.Sprintf("%s: vless-шифрование требует движок lx", n.Tag))
	}
	out := map[string]any{
		"type": n.Type, "tag": poolTag + "|" + n.ConfName(),
		"server": n.Host, "server_port": n.Port,
	}
	if n.UUID != "" {
		out["uuid"] = n.UUID
	}
	if n.Password != "" {
		out["password"] = n.Password
	}
	if n.Encryption != "" && n.Encryption != "none" {
		out["encryption"] = n.Encryption
	}
	if n.Type == "vless" {
		out["packet_encoding"] = "xudp"
		if n.Flow != "" && n.Transport == "" {
			out["flow"] = n.Flow
		}
	}
	if t := n.TLS; t != nil {
		tls := map[string]any{"enabled": true}
		if t.SNI != "" {
			tls["server_name"] = t.SNI
		}
		if len(t.ALPN) > 0 {
			tls["alpn"] = t.ALPN
		}
		if t.Fingerprint != "" {
			tls["utls"] = map[string]any{"enabled": true, "fingerprint": t.Fingerprint}
		}
		if t.Security == "reality" {
			tls["reality"] = map[string]any{"enabled": true, "public_key": t.PublicKey, "short_id": t.ShortID}
		}
		out["tls"] = tls
	}
	switch n.Transport {
	case "", "tcp":
	case "ws":
		tr := map[string]any{"type": "ws", "path": n.Path}
		if n.HeaderHost != "" {
			tr["headers"] = map[string]any{"Host": n.HeaderHost}
		}
		out["transport"] = tr
	case "httpupgrade":
		tr := map[string]any{"type": "httpupgrade", "path": n.Path}
		if n.HeaderHost != "" {
			tr["host"] = n.HeaderHost
		}
		out["transport"] = tr
	case "grpc":
		out["transport"] = map[string]any{"type": "grpc", "service_name": n.ServiceName}
	case "xhttp":
		if !lx {
			return skip(fmt.Sprintf("%s: транспорт xhttp требует движок lx", n.Tag))
		}
		out["transport"] = map[string]any{"type": "xhttp", "path": n.Path, "mode": n.Mode}
	default:
		return skip(fmt.Sprintf("%s: транспорт %s не поддерживается", n.Tag, n.Transport))
	}
	return out, true, ""
}

// BuildConfig собирает ЕДИНЫЙ конфиг mawg-экземпляра: на каждый пул свой
// tun-inbound и selector-группа, route-правило inbound -> группа.
// Merged=true даёт ФРАГМЕНТ для -C merge с чужим конфигом: только inbounds,
// outbounds и route.rules - чужие скаляры (log, clash_api, route.final)
// по семантике badjson-мержа всё равно побеждают, а прям не нужен
// (все наши inbounds закрыты явными правилами).
func BuildConfig(pools []PoolSpec, p Params) ([]byte, []string, error) {
	if !p.Merged && p.ClashPort == 0 {
		return nil, nil, fmt.Errorf("порт clash_api не задан")
	}
	for _, spec := range pools {
		if spec.MixedPort == 0 {
			return nil, nil, fmt.Errorf("пул %s: порт пробы не задан", spec.Name)
		}
	}
	var skipped []string
	var inbounds []map[string]any
	var outbounds []map[string]any
	var routeRules []map[string]any
	for i, spec := range pools {
		inTag := fmt.Sprintf("tun-in-%d", i+1)
		inbounds = append(inbounds, map[string]any{
			"type": "tun", "tag": inTag, "interface_name": spec.Tun,
			"address": []string{spec.TunIP},
			"mtu":     9000, "auto_route": false, "strict_route": false,
			"stack": "gvisor",
		})
		mixedTag := "mixed-" + spec.Name
		inbounds = append(inbounds, map[string]any{
			"type": "mixed", "tag": mixedTag, "listen": "127.0.0.1", "listen_port": spec.MixedPort,
		})
		groupTag := "mawg-" + spec.Name
		var tags []string
		for _, n := range spec.Nodes {
			ob, ok, reason := nodeOutbound(groupTag, n, p.LX)
			if !ok {
				skipped = append(skipped, reason)
				continue
			}
			outbounds = append(outbounds, ob)
			tags = append(tags, ob["tag"].(string))
		}
		if len(tags) == 0 {
			skipped = append(skipped, fmt.Sprintf("пул %s: ни один узел не подходит движку, tun не создан", spec.Name))
			continue
		}
		if spec.GroupMode == "selector" {
			outbounds = append(outbounds, map[string]any{
				"type": "selector", "tag": groupTag, "outbounds": tags, "default": tags[0],
			})
		} else {
			interval := spec.CheckIntervalSec
			if interval <= 0 {
				interval = 60
			}
			outbounds = append(outbounds, map[string]any{
				"type": "urltest", "tag": groupTag, "outbounds": tags,
				"url": spec.ProbeTarget, "interval": fmt.Sprintf("%ds", interval),
				"tolerance": 50,
			})
		}
		routeRules = append(routeRules,
			map[string]any{"inbound": inTag, "outbound": groupTag},
			map[string]any{"inbound": mixedTag, "outbound": groupTag},
		)
	}
	if len(outbounds) == 0 {
		return nil, skipped, fmt.Errorf("нет подходящих движку узлов")
	}
	route := map[string]any{"rules": routeRules}
	if p.Merged {
		if p.LX {
			route["default_domain_resolver"] = map[string]any{"server": "local"}
		}
		data, err := json.MarshalIndent(map[string]any{
			"inbounds": inbounds, "outbounds": outbounds, "route": route,
		}, "", "  ")
		return data, skipped, err
	}
	outbounds = append(outbounds, map[string]any{"type": "direct", "tag": "direct"})
	cfgRoute := map[string]any{
		"rules":                 routeRules,
		"final":                 "direct",
		"auto_detect_interface": true,
	}
	if p.LX {
		cfgRoute["default_domain_resolver"] = map[string]any{"server": "local"}
	}
	cfg := map[string]any{
		"log":       map[string]any{"level": "info"},
		"inbounds":  inbounds,
		"outbounds": outbounds,
		"route":     cfgRoute,
		"experimental": map[string]any{
			"clash_api": map[string]any{"external_controller": fmt.Sprintf("127.0.0.1:%d", p.ClashPort)},
		},
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	return data, skipped, err
}

func TuneName(index int) string { return fmt.Sprintf("tun%d", index) }

func TuneIP(index int) string { return fmt.Sprintf("172.19.%d.1/30", index) }

const FragmentName = "mawg-pools.json"
