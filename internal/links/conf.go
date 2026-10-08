package links

import (
	"fmt"
	"strings"
)

// ConfText собирает из WG/AWG-узла текст .conf в формате wgconf -
// путь нативного пула без движка (vx://, wireguard://, amneziawg://)
func (n Node) ConfText() (string, error) {
	if n.Type != "wireguard" && n.Type != "amneziawg" {
		return "", fmt.Errorf("узел %q типа %s не превращается в .conf - нужен движок", n.Tag, n.Type)
	}
	if n.PrivateKey == "" || n.PublicKey == "" || n.Host == "" || n.Port == 0 {
		return "", fmt.Errorf("узел %q неполный: нет ключей или endpoint", n.Tag)
	}
	var b strings.Builder
	b.WriteString("[Interface]\n")
	fmt.Fprintf(&b, "PrivateKey = %s\n", n.PrivateKey)
	for _, a := range n.Addresses {
		fmt.Fprintf(&b, "Address = %s\n", a)
	}
	if n.MTU > 0 {
		fmt.Fprintf(&b, "MTU = %d\n", n.MTU)
	}
	if a := n.AWG; a != nil {
		for _, f := range []struct {
			key string
			val string
		}{
			{"Jc", a.Jc}, {"Jmin", a.Jmin}, {"Jmax", a.Jmax},
			{"S1", a.S1}, {"S2", a.S2}, {"S3", a.S3}, {"S4", a.S4},
			{"H1", a.H1}, {"H2", a.H2}, {"H3", a.H3}, {"H4", a.H4},
			{"I1", a.I1}, {"I2", a.I2}, {"I3", a.I3}, {"I4", a.I4}, {"I5", a.I5},
		} {
			if f.val != "" {
				fmt.Fprintf(&b, "%s = %s\n", f.key, f.val)
			}
		}
	}
	b.WriteString("\n[Peer]\n")
	fmt.Fprintf(&b, "PublicKey = %s\n", n.PublicKey)
	if n.PresharedKey != "" {
		fmt.Fprintf(&b, "PresharedKey = %s\n", n.PresharedKey)
	}
	fmt.Fprintf(&b, "AllowedIPs = 0.0.0.0/0, ::/0\n")
	fmt.Fprintf(&b, "Endpoint = %s:%d\n", n.Host, n.Port)
	if n.Keepalive > 0 {
		fmt.Fprintf(&b, "PersistentKeepalive = %d\n", n.Keepalive)
	}
	return b.String(), nil
}

// ConfName - имя узла для файла конфига: часть тега после "|", иначе хост
func (n Node) ConfName() string {
	_, name, ok := strings.Cut(n.Tag, "|")
	if !ok || name == "" {
		name = n.Host
	}
	return name
}
