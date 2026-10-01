package wgconf

import (
	"strconv"
	"strings"
)

type AWGParams struct {
	Jc, Jmin, Jmax     *int
	S1, S2, S3, S4     *int
	H1, H2, H3, H4     *int
	I1, I2, I3, I4, I5 *string
}

func (p AWGParams) anyInitPacket() bool {
	return p.I1 != nil || p.I2 != nil || p.I3 != nil || p.I4 != nil || p.I5 != nil
}

func (p AWGParams) Present() bool {
	return p.Jc != nil || p.Jmin != nil || p.Jmax != nil ||
		p.S1 != nil || p.S2 != nil || p.S3 != nil || p.S4 != nil ||
		p.H1 != nil || p.H2 != nil || p.H3 != nil || p.H4 != nil ||
		p.anyInitPacket()
}

func (p AWGParams) HasExtended() bool {
	return p.S3 != nil || p.S4 != nil || p.anyInitPacket()
}

func val(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func (p AWGParams) ClassicValues() []int {
	return []int{val(p.Jc), val(p.Jmin), val(p.Jmax), val(p.S1), val(p.S2), val(p.H1), val(p.H2), val(p.H3), val(p.H4)}
}

func initArg(v *string) string {
	if v == nil {
		return "\"\""
	}
	return "\"" + strings.TrimSpace(*v) + "\""
}

func (p AWGParams) AscArgs() []string {
	classic := p.ClassicValues()
	out := make([]string, len(classic))
	for i, v := range classic {
		out[i] = strconv.Itoa(v)
	}
	if p.HasExtended() {
		out = append(out, strconv.Itoa(val(p.S3)), strconv.Itoa(val(p.S4)))
		out = append(out, initArg(p.I1), initArg(p.I2), initArg(p.I3), initArg(p.I4), initArg(p.I5))
	}
	return out
}

type Peer struct {
	PublicKey           string
	PresharedKey        string
	EndpointHost        string
	EndpointPort        int
	AllowedIPs          []string
	PersistentKeepalive int
}

type Config struct {
	PrivateKey string
	Addresses  []string
	DNS        []string
	MTU        int
	AWG        AWGParams
	Peer       Peer
}

func (c Config) FirstIPv4() string {
	for _, a := range c.Addresses {
		if !strings.Contains(a, ":") {
			return a
		}
	}
	return ""
}

func (c Config) Endpoint() string {
	if c.Peer.EndpointHost == "" {
		return ""
	}
	if c.Peer.EndpointPort == 0 {
		return c.Peer.EndpointHost
	}
	return c.Peer.EndpointHost + ":" + strconv.Itoa(c.Peer.EndpointPort)
}
