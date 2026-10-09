package singbox

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"mawg/internal/links"
	"mawg/internal/wgconf"
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
	// WG - конфиги WireGuard/Amnezia пула: едут wireguard-эндпоинтами
	// sing-box (в т.ч. AWG 3.x, который нативные интерфейсы не поднимут).
	WG        []wgconf.Config
	GroupMode string // "urltest" (по умолчанию) | "selector"
	// Detour - тег группы другого пула: туннель этого пула заводится
	// через неё (каскад движка, detour в sing-box).
	Detour string
}

// wgEndpoint - wireguard-эндпоинт sing-box-lx из конфига WG/AWG: AWG-поля
// (2.0 и 3.x) прикрепляются к корню эндпоинта, как в опциях форка.
func wgEndpoint(cfg wgconf.Config, tag, detour string) (map[string]any, error) {
	if cfg.PrivateKey == "" || cfg.Peer.PublicKey == "" || cfg.Peer.EndpointHost == "" {
		return nil, fmt.Errorf("%s: конфиг без ключа или эндпоинта", tag)
	}
	peer := map[string]any{
		"address":    cfg.Peer.EndpointHost,
		"public_key": cfg.Peer.PublicKey,
	}
	if cfg.Peer.EndpointPort > 0 {
		peer["port"] = cfg.Peer.EndpointPort
	}
	if cfg.Peer.PresharedKey != "" {
		peer["pre_shared_key"] = cfg.Peer.PresharedKey
	}
	if len(cfg.Peer.AllowedIPs) > 0 {
		peer["allowed_ips"] = cfg.Peer.AllowedIPs
	}
	switch {
	case cfg.Peer.KeepaliveRange != "":
		peer["persistent_keepalive_interval"] = cfg.Peer.KeepaliveRange
	case cfg.Peer.PersistentKeepalive > 0:
		peer["persistent_keepalive_interval"] = cfg.Peer.PersistentKeepalive
	}
	ep := map[string]any{
		"type": "wireguard", "tag": tag, "system": false,
		"address":     cfg.Addresses,
		"private_key": cfg.PrivateKey,
		"peers":       []map[string]any{peer},
	}
	if detour != "" {
		ep["detour"] = detour
	}
	if cfg.MTU > 0 {
		ep["mtu"] = cfg.MTU
	}
	awg := cfg.AWG
	num := func(v *string, name string) any {
		if v == nil {
			return nil
		}
		n, err := strconv.Atoi(strings.SplitN(*v, "-", 2)[0])
		if err != nil {
			return nil // пустое значение поля не пишем
		}
		return n
	}
	if v := num(awg.Jc, "jc"); v != nil {
		ep["jc"] = v
	}
	if v := num(awg.Jmin, "jmin"); v != nil {
		ep["jmin"] = v
	}
	if v := num(awg.Jmax, "jmax"); v != nil {
		ep["jmax"] = v
	}
	for _, f := range []struct {
		src *string
		key string
	}{
		{awg.S1, "s1"}, {awg.S2, "s2"}, {awg.S3, "s3"}, {awg.S4, "s4"},
	} {
		if v := num(f.src, f.key); v != nil {
			ep[f.key] = v
		}
	}
	// h1..h4 и AWG 3.x-диапазоны - строкой «N»/«N-M», как ждёт AWGRange
	for _, f := range []struct {
		src *string
		key string
	}{
		{awg.H1, "h1"}, {awg.H2, "h2"}, {awg.H3, "h3"}, {awg.H4, "h4"},
		{awg.I1, "i1"}, {awg.I2, "i2"}, {awg.I3, "i3"}, {awg.I4, "i4"}, {awg.I5, "i5"},
		{awg.HeaderProtectionKey, "header_protection_key"},
		{awg.ContentPaddingAddition, "content_padding_addition"},
		{awg.RekeyAfterTime, "rekey_after_time"},
		{awg.RekeyTimeout, "rekey_timeout"},
		{awg.RejectAfterTime, "reject_after_time"},
		{awg.KeepaliveTimeout, "keepalive_timeout"},
		{awg.MaxHandshakeAttempts, "max_handshake_attempts"},
	} {
		if f.src != nil && strings.TrimSpace(*f.src) != "" {
			ep[f.key] = strings.TrimSpace(*f.src)
		}
	}
	return ep, nil
}

type Params struct {
	ClashPort int
	LX        bool
	Merged    bool
	// ResolverTag - тег dns-сервера для default_domain_resolver lx-профиля
	// (пусто = "local"); в shared выбирается свободный у чужого конфига.
	ResolverTag string
}

// lxResolverDNS - dns-секция lx-профиля: без записи в dns.servers ядро
// на check отвечает "default domain resolver not found".
func lxResolverDNS(tag string) map[string]any {
	return map[string]any{
		"servers": []map[string]any{{"type": "local", "tag": tag}},
	}
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
	var endpoints []map[string]any
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
		for _, c := range spec.WG {
			tag := groupTag + "|" + strconv.Itoa(len(tags)+1)
			ep, err := wgEndpoint(c, tag, spec.Detour)
			if err != nil {
				skipped = append(skipped, fmt.Sprintf("пул %s: %v", spec.Name, err))
				continue
			}
			endpoints = append(endpoints, ep)
			tags = append(tags, tag)
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
	if len(outbounds) == 0 && len(endpoints) == 0 {
		return nil, skipped, fmt.Errorf("нет подходящих движку узлов")
	}
	route := map[string]any{"rules": routeRules}
	cfgDNS := map[string]any(nil)
	if p.LX {
		tag := p.ResolverTag
		if tag == "" {
			tag = "local"
		}
		route["default_domain_resolver"] = map[string]any{"server": tag}
		cfgDNS = lxResolverDNS(tag)
	}
	if p.Merged {
		frag := map[string]any{"inbounds": inbounds, "outbounds": outbounds, "route": route}
		if len(endpoints) > 0 {
			frag["endpoints"] = endpoints
		}
		if cfgDNS != nil {
			frag["dns"] = cfgDNS
		}
		data, err := json.MarshalIndent(frag, "", "  ")
		return data, skipped, err
	}
	outbounds = append(outbounds, map[string]any{"type": "direct", "tag": "direct"})
	cfgRoute := map[string]any{
		"rules":                 routeRules,
		"final":                 "direct",
		"auto_detect_interface": true,
	}
	if p.LX {
		tag := p.ResolverTag
		if tag == "" {
			tag = "local"
		}
		cfgRoute["default_domain_resolver"] = map[string]any{"server": tag}
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
	if len(endpoints) > 0 {
		cfg["endpoints"] = endpoints
	}
	if cfgDNS != nil {
		cfg["dns"] = cfgDNS
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	return data, skipped, err
}

func TuneName(index int) string { return fmt.Sprintf("tun%d", index) }

func TuneIP(index int) string { return fmt.Sprintf("172.19.%d.1/30", index) }

const FragmentName = "mawg-pools.json"
