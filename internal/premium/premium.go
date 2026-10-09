package premium

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"regexp"
	"strconv"
	"strings"

	"mawg/internal/wgconf"
)

const maxBytes = 2 << 20

func decodeLink(link string) ([]byte, error) {
	bad := errors.New("invalid or unsupported vpn:// configuration")
	if !strings.HasPrefix(link, "vpn://") || len(link) > maxBytes {
		return nil, bad
	}
	encoded := strings.TrimPrefix(link, "vpn://")
	for _, c := range encoded {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '=') {
			return nil, bad
		}
	}
	data, err := base64.URLEncoding.Strict().DecodeString(encoded + strings.Repeat("=", (4-len(encoded)%4)%4))
	if err != nil {
		return nil, bad
	}
	for _, offset := range []int{4, 0} {
		if len(data) <= offset {
			continue
		}
		input := bytes.NewReader(data[offset:])
		r, err := zlib.NewReader(input)
		if err != nil {
			continue
		}
		raw, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
		r.Close()
		if err != nil || len(raw) > maxBytes || input.Len() != 0 {
			continue
		}
		if offset == 4 && binary.BigEndian.Uint32(data[:4]) != 255 && binary.BigEndian.Uint32(data[:4]) != uint32(len(raw)) {
			continue
		}
		if !json.Valid(raw) {
			continue
		}
		return raw, nil
	}
	return nil, bad
}

func ImportKey(link string) (key, country string, err error) {
	raw, err := decodeLink(link)
	if err != nil {
		return "", "", err
	}
	var v struct {
		Version int `json:"config_version"`
		API     struct {
			Type     string `json:"service_type"`
			Protocol string `json:"service_protocol"`
			Country  string `json:"user_country_code"`
		} `json:"api_config"`
		Auth struct {
			Key string `json:"api_key"`
		} `json:"auth_data"`
	}
	if json.Unmarshal(raw, &v) != nil || v.Version != 2 || v.API.Type != "amnezia-premium" || v.API.Protocol != "awg" || v.Auth.Key == "" || len(v.Auth.Key) > 4096 {
		return "", "", errors.New("expected an Amnezia Premium AWG v2 access key")
	}
	return v.Auth.Key, v.API.Country, nil
}

func WireGuardKeys() (private, public string, err error) {
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", errors.New("Premium key generation failed")
	}
	b := key.Bytes()
	b[0] &= 248
	b[31] = (b[31] & 127) | 64
	return base64.StdEncoding.EncodeToString(b), base64.StdEncoding.EncodeToString(key.PublicKey().Bytes()), nil
}

func (c *Client) Config(ctx context.Context, key, id, userCountry, country, private string) ([]byte, error) {
	if !countryCode.MatchString(country) {
		return nil, errors.New("invalid Premium country")
	}
	b, err := base64.StdEncoding.DecodeString(private)
	if err != nil {
		return nil, errors.New("invalid saved Premium private key")
	}
	wg, err := ecdh.X25519().NewPrivateKey(b)
	if err != nil {
		return nil, errors.New("invalid saved Premium private key")
	}
	v := payload(key, id, userCountry)
	v["service_protocol"] = "awg"
	v["server_country_code"] = country
	v["public_key"] = base64.StdEncoding.EncodeToString(wg.PublicKey().Bytes())
	raw, err := c.post(ctx, "config", v)
	if err != nil {
		return nil, err
	}
	var response struct {
		Config string `json:"config"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return nil, errors.New("invalid Premium configuration response")
	}
	data, _, err := deviceConfig(response.Config, private)
	return data, err
}

var endpointHost = regexp.MustCompile(`^[a-zA-Z0-9.-]+$`)

func deviceConfig(link, private string) ([]byte, wgconf.Config, error) {
	bad := errors.New("unsupported or invalid Premium AWG configuration")
	raw, err := decodeLink(strings.Trim(link, "\r\n"))
	if err != nil {
		return nil, wgconf.Config{}, bad
	}
	var root struct {
		Containers []struct {
			Container string `json:"container"`
			AWG       struct {
				Last string `json:"last_config"`
			} `json:"awg"`
		} `json:"containers"`
	}
	if json.Unmarshal(raw, &root) != nil {
		return nil, wgconf.Config{}, bad
	}
	for _, container := range root.Containers {
		if container.Container != "amnezia-awg" {
			continue
		}
		var last struct {
			Config string `json:"config"`
		}
		if json.Unmarshal([]byte(container.AWG.Last), &last) != nil || !strings.Contains(last.Config, "$WIREGUARD_CLIENT_PRIVATE_KEY") {
			continue
		}
		text := strings.ReplaceAll(last.Config, "$WIREGUARD_CLIENT_PRIVATE_KEY", private)
		section := ""
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if line == "[Interface]" || line == "[Peer]" {
				section = line
				continue
			}
			key, value, ok := strings.Cut(line, "=")
			key, value = strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(value)
			if !ok || strings.ContainsAny(value, "'\r\n\x00") {
				return nil, wgconf.Config{}, bad
			}
			if key == "mtu" || key == "persistentkeepalive" {
				n, err := strconv.Atoi(value)
				if err != nil || n < 0 || n > 65535 {
					return nil, wgconf.Config{}, bad
				}
			}
			allowed := false
			if section == "[Interface]" {
				switch key {
				case "privatekey", "address", "dns", "mtu":
					allowed = true
				case "jc", "jmin", "jmax", "s1", "s2", "s3", "s4", "h1", "h2", "h3", "h4":
					parts := []string{value}
					if strings.HasPrefix(key, "h") {
						parts = strings.Split(value, "-")
					}
					if len(parts) > 2 {
						return nil, wgconf.Config{}, bad
					}
					var previous uint64
					for i, part := range parts {
						n, err := strconv.ParseUint(part, 10, 32)
						if err != nil || uint64(int(n)) != n || int(n) < 0 || (i > 0 && n < previous) {
							return nil, wgconf.Config{}, bad
						}
						previous = n
					}
					allowed = true
				case "i1", "i2", "i3", "i4", "i5":
					allowed = true
				}
			} else if section == "[Peer]" {
				switch key {
				case "publickey", "presharedkey", "endpoint", "allowedips", "persistentkeepalive":
					allowed = true
				}
			}
			if !allowed {
				return nil, wgconf.Config{}, bad
			}
		}
		data := []byte(text)
		cfg, err := wgconf.Parse(data)
		if err != nil || cfg.PrivateKey != private || !cfg.AWG.Present() {
			return nil, wgconf.Config{}, bad
		}
		for _, ip := range append(append([]string{}, cfg.Addresses...), cfg.Peer.AllowedIPs...) {
			if _, err := netip.ParsePrefix(ip); err != nil {
				return nil, wgconf.Config{}, bad
			}
		}
		for _, ip := range cfg.DNS {
			if _, err := netip.ParseAddr(ip); err != nil {
				return nil, wgconf.Config{}, bad
			}
		}
		host := strings.Trim(cfg.Peer.EndpointHost, "[]")
		if _, err := netip.ParseAddr(host); err != nil && !endpointHost.MatchString(host) {
			return nil, wgconf.Config{}, bad
		}
		return data, cfg, nil
	}
	return nil, wgconf.Config{}, bad
}
