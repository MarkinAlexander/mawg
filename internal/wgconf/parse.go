package wgconf

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var keyRe = regexp.MustCompile(`^[A-Za-z0-9+/]{43}=$`)

func validKey(s string) bool {
	return keyRe.MatchString(s)
}

func parseInt(s string) (int, error) {
	return strconv.Atoi(strings.TrimSpace(s))
}

// parseAWGNumber понимает и одиночные значения, и диапазоны N-M из
// конфигов AmneziaWG 2.0: Keenetic asc принимает одиночное число, берём
// нижнюю границу - любое значение из диапазона валидно для сервера.
func parseAWGNumber(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "-"); i > 0 {
		s = s[:i]
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

func parseEndpoint(s string) (string, int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", 0, fmt.Errorf("empty endpoint")
	}
	if strings.HasPrefix(s, "[") {
		end := strings.LastIndex(s, "]")
		if end < 0 {
			return "", 0, fmt.Errorf("bad ipv6 endpoint %q", s)
		}
		host := s[:end+1]
		rest := s[end+1:]
		if rest == "" {
			return host, 0, nil
		}
		if !strings.HasPrefix(rest, ":") {
			return "", 0, fmt.Errorf("bad ipv6 endpoint %q", s)
		}
		port, err := parseInt(rest[1:])
		if err != nil {
			return "", 0, fmt.Errorf("bad port in endpoint %q", s)
		}
		return host, port, nil
	}
	if i := strings.LastIndex(s, ":"); i >= 0 {
		port, err := parseInt(s[i+1:])
		if err != nil {
			return "", 0, fmt.Errorf("bad port in endpoint %q", s)
		}
		return s[:i], port, nil
	}
	return s, 0, nil
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func setAWG(p *AWGParams, key string, v int) {
	var slot **int
	switch strings.ToLower(key) {
	case "jc":
		slot = &p.Jc
	case "jmin":
		slot = &p.Jmin
	case "jmax":
		slot = &p.Jmax
	case "s1":
		slot = &p.S1
	case "s2":
		slot = &p.S2
	case "s3":
		slot = &p.S3
	case "s4":
		slot = &p.S4
	case "h1":
		slot = &p.H1
	case "h2":
		slot = &p.H2
	case "h3":
		slot = &p.H3
	case "h4":
		slot = &p.H4
	default:
		return
	}
	n := v
	*slot = &n
}

func setInitPacket(p *AWGParams, key, raw string) bool {
	switch strings.ToLower(key) {
	case "i1":
		p.I1 = &raw
	case "i2":
		p.I2 = &raw
	case "i3":
		p.I3 = &raw
	case "i4":
		p.I4 = &raw
	case "i5":
		p.I5 = &raw
	default:
		return false
	}
	return true
}

func Parse(data []byte) (Config, error) {
	var cfg Config
	section := ""
	peers := 0

	for lineno, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			if section == "peer" {
				peers++
				if peers > 1 {
					return cfg, fmt.Errorf("line %d: multiple [Peer] sections not supported", lineno+1)
				}
			}
			continue
		}
		eq := strings.Index(line, "=")
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		value := strings.TrimSpace(line[eq+1:])
		value = strings.Trim(value, `"`)
		lower := strings.ToLower(key)

		switch section {
		case "interface":
			switch lower {
			case "privatekey":
				cfg.PrivateKey = value
			case "address":
				cfg.Addresses = append(cfg.Addresses, splitList(value)...)
			case "dns":
				cfg.DNS = append(cfg.DNS, splitList(value)...)
			case "mtu":
				cfg.MTU, _ = parseInt(value)
			default:
				if setInitPacket(&cfg.AWG, lower, value) {
					continue
				}
				if v, ok := parseAWGNumber(value); ok {
					setAWG(&cfg.AWG, lower, v)
				}
			}
		case "peer":
			switch lower {
			case "publickey":
				cfg.Peer.PublicKey = value
			case "presharedkey":
				cfg.Peer.PresharedKey = value
			case "endpoint":
				host, port, err := parseEndpoint(value)
				if err != nil {
					return cfg, fmt.Errorf("line %d: %w", lineno+1, err)
				}
				cfg.Peer.EndpointHost, cfg.Peer.EndpointPort = host, port
			case "allowedips":
				cfg.Peer.AllowedIPs = append(cfg.Peer.AllowedIPs, splitList(value)...)
			case "persistentkeepalive":
				cfg.Peer.PersistentKeepalive, _ = parseInt(value)
			}
		}
	}

	if !validKey(cfg.PrivateKey) {
		return cfg, fmt.Errorf("missing or invalid Interface PrivateKey")
	}
	if !validKey(cfg.Peer.PublicKey) {
		return cfg, fmt.Errorf("missing or invalid Peer PublicKey")
	}
	if cfg.Peer.PresharedKey != "" && !validKey(cfg.Peer.PresharedKey) {
		return cfg, fmt.Errorf("invalid Peer PresharedKey")
	}
	if cfg.Peer.EndpointHost == "" {
		return cfg, fmt.Errorf("missing Peer Endpoint")
	}
	if cfg.Peer.EndpointPort <= 0 || cfg.Peer.EndpointPort > 65535 {
		return cfg, fmt.Errorf("missing or invalid Endpoint port")
	}
	if len(cfg.Addresses) == 0 {
		return cfg, fmt.Errorf("missing Interface Address")
	}
	return cfg, nil
}
