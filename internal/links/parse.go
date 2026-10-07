package links

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
)

var ErrUnknownScheme = errors.New("неизвестная схема")

func schemeOf(raw string) string {
	i := strings.Index(raw, "://")
	if i <= 0 {
		return ""
	}
	return strings.ToLower(raw[:i])
}

func splitFragment(raw string) (*url.URL, string, error) {
	i := strings.IndexByte(raw, '#')
	if i < 0 {
		u, err := url.Parse(strings.TrimSpace(raw))
		return u, "", err
	}
	u, err := url.Parse(strings.TrimSpace(raw[:i]))
	if err != nil {
		return nil, "", err
	}
	frag, err := url.PathUnescape(raw[i+1:])
	if err != nil {
		frag = raw[i+1:]
	}
	return u, frag, nil
}

func portOf(u *url.URL) int {
	p, err := strconv.Atoi(u.Port())
	if err != nil {
		return 0
	}
	return p
}

func query(u *url.URL) url.Values {
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return url.Values{}
	}
	return q
}

func ParseLink(source, raw string) (Node, error) {
	raw = strings.TrimSpace(raw)
	switch schemeOf(raw) {
	case "vless":
		return parseVless(source, raw)
	case "trojan":
		return parseTrojan(source, raw)
	case "wireguard":
		return parseWG(source, raw, false)
	case "amneziawg":
		return parseWG(source, raw, true)
	case "vpn":
		return parseVPN(source, raw)
	}
	return Node{}, ErrUnknownScheme
}

func proxyTag(u *url.URL, frag string) string {
	if frag != "" {
		return frag
	}
	return u.Hostname() + ":" + u.Port()
}

func parseVless(source, raw string) (Node, error) {
	u, frag, err := splitFragment(raw)
	if err != nil {
		return Node{}, err
	}
	q := query(u)
	n := Node{
		Type: "vless",
		Tag:  source + "|" + proxyTag(u, frag),
		Host: u.Hostname(),
		UUID: u.User.Username(),
		Raw:  raw,
	}
	n.Port = portOf(u)
	if enc := q.Get("encryption"); enc != "" && enc != "none" {
		n.Encryption = enc
	}
	n.Flow = q.Get("flow")
	n.TLS = tlsFromQuery(q)
	transportFromQuery(&n, q)
	return n, nil
}

func parseTrojan(source, raw string) (Node, error) {
	u, frag, err := splitFragment(raw)
	if err != nil {
		return Node{}, err
	}
	q := query(u)
	n := Node{
		Type:     "trojan",
		Tag:      source + "|" + proxyTag(u, frag),
		Host:     u.Hostname(),
		Password: u.User.Username(),
		Raw:      raw,
	}
	n.Port = portOf(u)
	if enc := q.Get("encryption"); enc != "" && enc != "none" {
		n.Encryption = enc
	}
	n.Flow = q.Get("flow")
	n.TLS = tlsFromQuery(q)
	transportFromQuery(&n, q)
	return n, nil
}

func tlsFromQuery(q url.Values) *TLS {
	sec := q.Get("security")
	if sec != "tls" && sec != "reality" {
		return nil
	}
	t := &TLS{Security: sec, SNI: q.Get("sni")}
	if fp := q.Get("fp"); fp != "" {
		t.Fingerprint = fp
	}
	for _, a := range strings.Split(q.Get("alpn"), ",") {
		if a = strings.TrimSpace(a); a != "" {
			t.ALPN = append(t.ALPN, a)
		}
	}
	if sec == "reality" {
		t.PublicKey = q.Get("pbk")
		t.ShortID = q.Get("sid")
	}
	return t
}

func transportFromQuery(n *Node, q url.Values) {
	once := func(s string) string {
		if d, err := url.PathUnescape(s); err == nil {
			return d
		}
		return s
	}
	switch q.Get("type") {
	case "ws", "httpupgrade":
		n.Transport = q.Get("type")
		n.Path = once(q.Get("path"))
		n.HeaderHost = once(q.Get("host"))
	case "grpc":
		n.Transport = "grpc"
		n.ServiceName = q.Get("serviceName")
	case "xhttp":
		n.Transport = "xhttp"
		n.Path = q.Get("path")
		n.HeaderHost = q.Get("host")
		n.Mode = q.Get("mode")
		if n.Mode == "" {
			n.Mode = "auto"
		}
	}
}
