package links

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"mawg/internal/wgconf"
)

func parseWG(source, raw string, awg bool) (Node, error) {
	u, frag, err := splitFragment(raw)
	if err != nil {
		return Node{}, err
	}
	q := query(u)
	typ := "wireguard"
	if awg {
		typ = "amneziawg"
	}
	if frag == "" {
		frag = "wg-" + u.Hostname()
		if awg {
			frag = "awg-" + u.Hostname()
		}
	}
	n := Node{
		Type:       typ,
		Tag:        source + "|" + frag,
		Host:       u.Hostname(),
		Port:       portOf(u),
		PrivateKey: u.User.Username(),
		Raw:        raw,
	}
	n.Addresses = splitComma(q.Get("address"))
	n.PublicKey = q.Get("publickey")
	n.PresharedKey = q.Get("presharedkey")
	n.MTU, _ = strconv.Atoi(q.Get("mtu"))
	n.Keepalive, _ = strconv.Atoi(q.Get("keepalive"))
	if awg {
		a := &AWG{}
		a.Jc, a.Jmin, a.Jmax = q.Get("jc"), q.Get("jmin"), q.Get("jmax")
		a.S1, a.S2, a.S3, a.S4 = q.Get("s1"), q.Get("s2"), q.Get("s3"), q.Get("s4")
		a.H1, a.H2, a.H3, a.H4 = q.Get("h1"), q.Get("h2"), q.Get("h3"), q.Get("h4")
		a.I1, a.I2, a.I3, a.I4, a.I5 = q.Get("i1"), q.Get("i2"), q.Get("i3"), q.Get("i4"), q.Get("i5")
		if a.present() {
			n.AWG = a
		}
	}
	return n, nil
}

func (a *AWG) present() bool {
	return a.Jc != "" || a.Jmin != "" || a.Jmax != "" ||
		a.S1 != "" || a.S2 != "" || a.S3 != "" || a.S4 != "" ||
		a.H1 != "" || a.H2 != "" || a.H3 != "" || a.H4 != "" ||
		a.I1 != "" || a.I2 != "" || a.I3 != "" || a.I4 != "" || a.I5 != ""
}

func splitComma(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func awgFromWGConf(p wgconf.AWGParams) *AWG {
	deref := func(s *string) string {
		if s == nil {
			return ""
		}
		return strings.TrimSpace(*s)
	}
	a := &AWG{
		Jc: deref(p.Jc), Jmin: deref(p.Jmin), Jmax: deref(p.Jmax),
		S1: deref(p.S1), S2: deref(p.S2), S3: deref(p.S3), S4: deref(p.S4),
		H1: deref(p.H1), H2: deref(p.H2), H3: deref(p.H3), H4: deref(p.H4),
		I1: deref(p.I1), I2: deref(p.I2), I3: deref(p.I3), I4: deref(p.I4), I5: deref(p.I5),
	}
	if !a.present() {
		return nil
	}
	return a
}

func confName(data []byte) string {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") && len(strings.TrimSpace(line[1:])) > 0 {
			return strings.TrimSpace(line[1:])
		}
	}
	return ""
}

func NodeFromConf(source, name string, data []byte) (Node, error) {
	cfg, err := wgconf.Parse(data)
	if err != nil {
		return Node{}, err
	}
	if name == "" {
		name = confName(data)
	}
	if name == "" {
		name = cfg.Endpoint()
	}
	typ := "wireguard"
	if cfg.AWG.Present() {
		typ = "amneziawg"
	}
	return Node{
		Type:         typ,
		Tag:          source + "|" + name,
		Host:         cfg.Peer.EndpointHost,
		Port:         cfg.Peer.EndpointPort,
		PrivateKey:   cfg.PrivateKey,
		PublicKey:    cfg.Peer.PublicKey,
		PresharedKey: cfg.Peer.PresharedKey,
		Addresses:    cfg.Addresses,
		MTU:          cfg.MTU,
		Keepalive:    cfg.Peer.PersistentKeepalive,
		AWG:          awgFromWGConf(cfg.AWG),
	}, nil
}

type vpnExport struct {
	Containers []struct {
		Container string `json:"container"`
		AWG       struct {
			LastConfig string `json:"last_config"`
		} `json:"awg"`
		WireGuard struct {
			LastConfig string `json:"last_config"`
		} `json:"wireguard"`
		Vless struct {
			LastConfig string `json:"last_config"`
		} `json:"vless"`
	} `json:"containers"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	ConfigVersion int    `json:"config_version"`
	APIConfig     struct {
		ServiceType     string `json:"service_type"`
		ServiceProtocol string `json:"service_protocol"`
		UserCountryCode string `json:"user_country_code"`
	} `json:"api_config"`
	AuthData struct {
		APIKey string `json:"api_key"`
	} `json:"auth_data"`
}

func zlibAll(b []byte) ([]byte, bool) {
	zr, err := zlib.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, false
	}
	doc, err := io.ReadAll(zr)
	zr.Close()
	if err != nil {
		return nil, false
	}
	return doc, true
}

type vpnLastConfig struct {
	Config string `json:"config"`
}

func vpnConfText(lastConfig string) string {
	var lc vpnLastConfig
	if err := json.Unmarshal([]byte(lastConfig), &lc); err == nil && lc.Config != "" {
		return lc.Config
	}
	return lastConfig
}

func parseVPN(source, raw string) (Node, error) {
	payload, err := decodeBase64URL(strings.TrimSpace(strings.TrimPrefix(raw, "vpn://")))
	if err != nil {
		return Node{}, fmt.Errorf("vpn://: %w", err)
	}
	if len(payload) < 5 {
		return Node{}, fmt.Errorf("vpn://: слишком короткое тело")
	}
	doc, okDoc := zlibAll(payload[4:])
	if !okDoc {
		if d, ok := zlibAll(payload); ok {
			doc = d
		} else {
			return Node{}, fmt.Errorf("vpn://: тело не zlib")
		}
	}
	var exp vpnExport
	if err := json.Unmarshal(doc, &exp); err != nil {
		return Node{}, fmt.Errorf("vpn://: %w", err)
	}
	if exp.APIConfig.ServiceType != "" && exp.AuthData.APIKey != "" {
		return Node{}, fmt.Errorf(
			"vpn:// содержит ключ Amnezia %s API (протокол %s), а не статический конфиг: конфиг по ключу выдаёт gateway Амнезии - mawg может обменять его (диалог «ссылка или подписка» -> «Запросить конфиг», либо mawg links amnezia)",
			strings.TrimPrefix(exp.APIConfig.ServiceType, "amnezia-"), exp.APIConfig.ServiceProtocol)
	}
	for _, c := range exp.Containers {
		lastConfig := c.AWG.LastConfig
		if lastConfig == "" {
			lastConfig = c.WireGuard.LastConfig
		}
		if lastConfig == "" {
			continue
		}
		conf := vpnConfText(lastConfig)
		node, err := NodeFromConf(source, "", []byte(conf))
		if err != nil {
			return Node{}, fmt.Errorf("vpn://: %w", err)
		}
		node.Raw = raw
		return node, nil
	}
	return Node{}, fmt.Errorf("vpn://: в контейнерах нет awg-конфига")
}

func decodeBase64URL(s string) ([]byte, error) {
	s = strings.Join(strings.Fields(s), "")
	trimmed := strings.TrimRight(s, "=")
	padded := trimmed + strings.Repeat("=", (4-len(trimmed)%4)%4)
	for _, enc := range []*base64.Encoding{base64.URLEncoding, base64.StdEncoding} {
		if b, err := enc.DecodeString(padded); err == nil {
			return b, nil
		}
	}
	for _, enc := range []*base64.Encoding{base64.RawURLEncoding, base64.RawStdEncoding} {
		if b, err := enc.DecodeString(trimmed); err == nil {
			return b, nil
		}
	}
	return nil, fmt.Errorf("не base64")
}
