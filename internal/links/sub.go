package links

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	subscriptionUserinfo  = "Subscription-Userinfo"
	profileUpdateInterval = "Profile-Update-Interval"
)

func ParseSubscription(source, body string) Result {
	res := Result{Source: source}
	text := strings.TrimSpace(body)
	// base64-обёртка может скрывать и JSON-конфиг, не только список ссылок
	if !jsonHead(text) {
		if decoded, ok := decodeBody(text); ok {
			text = strings.TrimSpace(decoded)
		}
	}
	if jsonHead(text) {
		if err := addJSONConfig(&res, []byte(text)); err != nil {
			res.warn(err.Error())
		}
		dropStubNodes(&res)
		return res
	}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		node, err := ParseLink(source, line)
		if err != nil {
			res.warn(fmt.Sprintf("пропущено %q: %v", cut(line, 60), err))
			continue
		}
		res.Nodes = append(res.Nodes, node)
	}
	dropStubNodes(&res)
	return res
}

// dropStubNodes - выкинуть узлы-заглушки (сервер 0.0.0.0/пустой, порт<=0):
// панели с привязкой подписки к приложению/устройству (панель 11.2026: экспорт с 0.0.0.0:1, реальные адреса видит только приложение)
// так отвечают «чужим» клиентам; пул из заглушек - мёртвые пробы без
// понятной причины.
func dropStubNodes(res *Result) {
	kept := res.Nodes[:0]
	for _, n := range res.Nodes {
		if n.Host == "" || n.Host == "0.0.0.0" || n.Port <= 0 {
			name := n.Tag
			if i := strings.LastIndex(name, "|"); i >= 0 {
				name = name[i+1:]
			}
			res.warn(fmt.Sprintf(
				"пропущен узел %q: сервер %s:%d - заглушка панели; если подписка работает только в приложении (Happ и т.п.), добавьте vless-ссылку из него",
				name, n.Host, n.Port))
			continue
		}
		kept = append(kept, n)
	}
	res.Nodes = kept
}

func cut(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// jsonHead - тело (в т.ч. раскрытое из base64) - JSON-документ: объект
// или массив (v2rayN-экспорт конфигов, по узлу на элемент)
func jsonHead(s string) bool {
	return strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[")
}

func decodeBody(s string) (string, bool) {
	s = strings.Join(strings.Fields(s), "")
	if s == "" || !utf8.ValidString(s) {
		return "", false
	}
	trimmed := strings.TrimRight(s, "=")
	padded := trimmed + strings.Repeat("=", (4-len(trimmed)%4)%4)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding} {
		b, err := enc.DecodeString(padded)
		if err != nil || !looksLikeLinkList(b) {
			continue
		}
		return string(b), true
	}
	return "", false
}

func looksLikeLinkList(b []byte) bool {
	if !utf8.Valid(b) {
		return false
	}
	t := string(b)
	// полный конфиг (Xray/sing-box) без единой proxy-схемы внутри - тоже
	// валидное содержимое base64-подписки (кейс панели 11.2026: dns по IP)
	if jsonHead(t) && strings.Contains(t, `"outbounds"`) {
		return true
	}
	// hysteria2 - отдельный протокол; hysteria (1.x) сюда сознательно не
	// входит: ни парсер, ни движки его не умеют
	for _, scheme := range []string{"vless://", "vmess://", "trojan://", "wireguard://", "amneziawg://", "vpn://", "ss://", "hysteria2://", "tuic://", "socks://", "http://", "https://"} {
		if strings.Contains(t, scheme) {
			return true
		}
	}
	return false
}

func ParseUserinfo(header string) SubInfo {
	var info SubInfo
	info.Userinfo = header
	for _, part := range strings.Split(header, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "upload":
			info.Upload = n
		case "download":
			info.Download = n
		case "total":
			info.Total = n
		case "expire":
			info.Expire = n
		}
	}
	return info
}

func ParseUpdateInterval(header string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(header), 64)
	if err != nil || v <= 0 {
		return 0, false
	}
	return v, true
}

type sbConfig struct {
	Outbounds []json.RawMessage `json:"outbounds"`
	Endpoints []json.RawMessage `json:"endpoints"`
}

type sbOutbound struct {
	Type       string  `json:"type"`
	Tag        string  `json:"tag"`
	Server     string  `json:"server"`
	ServerPort int     `json:"server_port"`
	UUID       string  `json:"uuid"`
	Password   string  `json:"password"`
	Flow       string  `json:"flow"`
	Encryption string  `json:"encryption"`
	TLS        *sbTLS  `json:"tls"`
	Transport  *sbTr   `json:"transport"`
	Endpoint   *sbWire `json:"-"`
}

type sbTLS struct {
	Enabled    bool     `json:"enabled"`
	ServerName string   `json:"server_name"`
	ALPN       []string `json:"alpn"`
	UTLS       struct {
		Enabled     bool   `json:"enabled"`
		Fingerprint string `json:"fingerprint"`
	} `json:"utls"`
	Reality struct {
		Enabled   bool   `json:"enabled"`
		PublicKey string `json:"public_key"`
		ShortID   string `json:"short_id"`
	} `json:"reality"`
}

type sbTr struct {
	Type        string `json:"type"`
	Path        string `json:"path"`
	Host        string `json:"host"`
	ServiceName string `json:"service_name"`
	Mode        string `json:"mode"`
	Headers     struct {
		Host string `json:"Host"`
	} `json:"headers"`
}

type sbWire struct {
	Type       string       `json:"type"`
	Tag        string       `json:"tag"`
	Address    []string     `json:"address"`
	PrivateKey string       `json:"private_key"`
	MTU        int          `json:"mtu"`
	Jc         *json.Number `json:"jc"`
	Jmin       *json.Number `json:"jmin"`
	Jmax       *json.Number `json:"jmax"`
	S1         *json.Number `json:"s1"`
	S2         *json.Number `json:"s2"`
	S3         *json.Number `json:"s3"`
	S4         *json.Number `json:"s4"`
	H1         *json.Number `json:"h1"`
	H2         *json.Number `json:"h2"`
	H3         *json.Number `json:"h3"`
	H4         *json.Number `json:"h4"`
	Peers      []struct {
		Address      string   `json:"address"`
		Port         int      `json:"port"`
		PublicKey    string   `json:"public_key"`
		PreSharedKey string   `json:"pre_shared_key"`
		AllowedIPs   []string `json:"allowed_ips"`
		Keepalive    int      `json:"persistent_keepalive_interval"`
	} `json:"peers"`
}

var sbUtilityTypes = map[string]bool{
	"selector": true, "urltest": true, "direct": true, "block": true, "dns": true,
}

// addJSONConfig - JSON-подписка: массив полных конфигов (v2rayN-экспорт;
// битый элемент пропускается с warning) или один конфиг целиком.
// Экспорт повторяет узлы: «выбранный» outbound одного конфига живёт
// альтернативой (auto-*-choice) в других - схлопываем по сервер+учётка.
func addJSONConfig(res *Result, body []byte) error {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return fmt.Errorf("json подписки: пусто")
	}
	if trimmed[0] == '[' {
		var arr []json.RawMessage
		if err := json.Unmarshal(trimmed, &arr); err != nil {
			return fmt.Errorf("json подписки: %w", err)
		}
		for i, el := range arr {
			if err := addConfigDoc(res, el); err != nil {
				res.warn(fmt.Sprintf("конфиг #%d: %v", i+1, err))
			}
		}
		seen := map[string]bool{}
		kept := res.Nodes[:0]
		for _, n := range res.Nodes {
			k := n.Type + "|" + n.Host + "|" + strconv.Itoa(n.Port) + "|" + n.UUID + "|" + n.Password
			if seen[k] {
				continue
			}
			seen[k] = true
			kept = append(kept, n)
		}
		res.Nodes = kept
		return nil
	}
	return addConfigDoc(res, trimmed)
}

// addConfigDoc - один полный конфиг: Xray/v2rayN узнаётся по «protocol»
// в outbound (у sing-box поле называется «type»)
func addConfigDoc(res *Result, body []byte) error {
	var probe struct {
		Outbounds []struct {
			Protocol string `json:"protocol"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return fmt.Errorf("json подписки: %w", err)
	}
	for _, o := range probe.Outbounds {
		if o.Protocol != "" {
			return addXrayConfig(res, body)
		}
	}
	return addSbConfig(res, body)
}

func addSbConfig(res *Result, body []byte) error {
	var cfg sbConfig
	if err := json.Unmarshal(body, &cfg); err != nil {
		return fmt.Errorf("json подписки: %w", err)
	}
	for _, raw := range cfg.Outbounds {
		var probe struct {
			Type string `json:"type"`
			Tag  string `json:"tag"`
		}
		if json.Unmarshal(raw, &probe) != nil {
			continue
		}
		if sbUtilityTypes[probe.Type] {
			continue
		}
		if probe.Type == "wireguard" {
			var ep sbWire
			if err := json.Unmarshal(raw, &ep); err != nil {
				res.warn(fmt.Sprintf("wireguard %q: %v", probe.Tag, err))
				continue
			}
			res.Nodes = append(res.Nodes, nodeFromSBWire(res.Source, ep))
			continue
		}
		var ob sbOutbound
		if err := json.Unmarshal(raw, &ob); err != nil {
			res.warn(fmt.Sprintf("outbound %q: %v", probe.Tag, err))
			continue
		}
		res.Nodes = append(res.Nodes, nodeFromSBOutbound(res.Source, ob))
	}
	for _, raw := range cfg.Endpoints {
		var ep sbWire
		if err := json.Unmarshal(raw, &ep); err != nil {
			res.warn("endpoint: " + err.Error())
			continue
		}
		res.Nodes = append(res.Nodes, nodeFromSBWire(res.Source, ep))
	}
	return nil
}

func nodeFromSBOutbound(source string, ob sbOutbound) Node {
	n := Node{
		Type: ob.Type, Tag: source + "|" + ob.Tag,
		Host: ob.Server, Port: ob.ServerPort,
		UUID: ob.UUID, Password: ob.Password,
		Encryption: ob.Encryption, Flow: ob.Flow,
	}
	if ob.Transport != nil {
		n.Transport = ob.Transport.Type
		n.Path = ob.Transport.Path
		n.ServiceName = ob.Transport.ServiceName
		n.Mode = ob.Transport.Mode
		n.HeaderHost = ob.Transport.Headers.Host
		if n.HeaderHost == "" {
			n.HeaderHost = ob.Transport.Host
		}
	}
	if ob.TLS != nil && ob.TLS.Enabled {
		t := &TLS{Security: "tls", SNI: ob.TLS.ServerName}
		if ob.TLS.Reality.Enabled {
			t.Security = "reality"
			t.PublicKey = ob.TLS.Reality.PublicKey
			t.ShortID = ob.TLS.Reality.ShortID
		}
		if ob.TLS.UTLS.Enabled {
			t.Fingerprint = ob.TLS.UTLS.Fingerprint
		}
		t.ALPN = ob.TLS.ALPN
		n.TLS = t
	}
	return n
}

func nodeFromSBWire(source string, ep sbWire) Node {
	num := func(v *json.Number) string {
		if v == nil {
			return ""
		}
		return v.String()
	}
	n := Node{
		Type: "wireguard", Tag: source + "|" + ep.Tag,
		PrivateKey: ep.PrivateKey, Addresses: ep.Address, MTU: ep.MTU,
	}
	if strings.Contains(ep.Type, "awg") || num(ep.H1) != "" || num(ep.Jc) != "" {
		n.Type = "amneziawg"
		n.AWG = &AWG{
			Jc: num(ep.Jc), Jmin: num(ep.Jmin), Jmax: num(ep.Jmax),
			S1: num(ep.S1), S2: num(ep.S2), S3: num(ep.S3), S4: num(ep.S4),
			H1: num(ep.H1), H2: num(ep.H2), H3: num(ep.H3), H4: num(ep.H4),
		}
		if !n.AWG.present() {
			n.AWG = nil
		}
	}
	if len(ep.Peers) > 0 {
		p := ep.Peers[0]
		n.Host, n.Port = p.Address, p.Port
		n.PublicKey, n.PresharedKey = p.PublicKey, p.PreSharedKey
		n.Keepalive = p.Keepalive
	}
	return n
}
