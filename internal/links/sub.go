package links

import (
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
	trimmed := strings.TrimSpace(body)
	if strings.HasPrefix(trimmed, "{") {
		if err := addJSONConfig(&res, []byte(trimmed)); err != nil {
			res.warn(err.Error())
		}
		return res
	}
	text := body
	if decoded, ok := decodeBody(trimmed); ok {
		text = decoded
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
	return res
}

func cut(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
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
	for _, scheme := range []string{"vless://", "trojan://", "wireguard://", "amneziawg://", "vpn://", "ss://", "hysteria", "tuic://", "socks://", "http://", "https://"} {
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

func addJSONConfig(res *Result, body []byte) error {
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
