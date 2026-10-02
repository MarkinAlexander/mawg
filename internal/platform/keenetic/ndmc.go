package keenetic

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	ndmcHeaderRe = regexp.MustCompile(`Interface, name = "([^"]+)"`)
	ndmcKVRe     = regexp.MustCompile(`^\s*([a-z-]+):\s*(.*)$`)
)

// parseNDMCInterfaces разбирает текст `ndmc show interface [X]`. Поля
// интерфейса читаются только до секций wireguard/summary, поля пиров -
// внутри блока peer; длинные значения (публичный ключ) продолжаются на
// следующей строке.
func parseNDMCInterfaces(out string) map[string]rciInterface {
	res := map[string]rciInterface{}
	var cur *rciInterface
	var peer *rciPeer
	section := ""
	lastKey := ""
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimRight(raw, "\r")
		if m := ndmcHeaderRe.FindStringSubmatch(line); m != nil {
			if cur != nil {
				res[cur.ID] = *cur
			}
			cur = &rciInterface{ID: m[1]}
			peer = nil
			section = ""
			lastKey = ""
			continue
		}
		if cur == nil {
			// одиночный `show interface X` печатает блок без заголовка
			if m := ndmcKVRe.FindStringSubmatch(line); m != nil && m[1] == "id" {
				cur = &rciInterface{ID: strings.TrimSpace(m[2])}
			}
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "peer:") || strings.HasPrefix(trimmed, "peer,") {
			cur.Wireguard.Peers = append(cur.Wireguard.Peers, rciPeer{})
			peer = &cur.Wireguard.Peers[len(cur.Wireguard.Peers)-1]
			lastKey = ""
			continue
		}
		m := ndmcKVRe.FindStringSubmatch(line)
		if m == nil {
			if lastKey == "public-key" && peer != nil && peer.PublicKey == "" {
				peer.PublicKey = trimmed
			}
			continue
		}
		key, val := m[1], strings.TrimSpace(m[2])
		lastKey = key
		switch key {
		case "description", "link", "connected", "state", "address", "mask":
			if peer == nil && section == "" {
				switch key {
				case "description":
					cur.Description = val
				case "link":
					cur.Link = val
				case "connected":
					cur.Connected = val
				case "state":
					cur.State = val
				case "address":
					cur.Address = val
				case "mask":
					cur.Mask = val
				}
			}
		case "wireguard", "summary", "ipv6":
			if peer == nil {
				section = key
			}
		case "public-key":
			if peer != nil {
				peer.PublicKey = val
			}
		case "last-handshake":
			if peer != nil {
				if n, err := strconv.ParseInt(val, 10, 64); err == nil {
					peer.LastHandshake = n
				}
			}
		case "online":
			if peer != nil {
				peer.Online = val == "yes"
			}
		case "enabled":
			if peer != nil {
				peer.Enabled = val == "yes"
			}
		}
	}
	if cur != nil {
		res[cur.ID] = *cur
	}
	return res
}
