package mihomo

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"mawg/internal/links"
	"mawg/internal/singbox"
	"mawg/internal/wgconf"
)

// PoolSpec - спецификация движкового пула; общая с sing-box (один источник
// правды), поэтому алиас.
type PoolSpec = singbox.PoolSpec

// Tun stack конфига: system - ядро (легче по RAM, на роутерах важнее),
// gvisor/mixed - запасные варианты на живом брингапе.
const tunStack = "system"

// --- YAML-схема (типизированная: детерминированный порядок полей) ---

type wsOpts struct {
	Path    string            `yaml:"path,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty"`
}

type grpcOpts struct {
	ServiceName string `yaml:"grpc-service-name,omitempty"`
}

type httpOpts struct {
	Path    string              `yaml:"path,omitempty"`
	Headers map[string][]string `yaml:"headers,omitempty"`
}

type realityOpts struct {
	PublicKey string `yaml:"public-key,omitempty"`
	ShortID   string `yaml:"short-id,omitempty"`
}

type amneziaOpts struct {
	Version int `yaml:"version,omitempty"` // 3 = реализация v3 (AWG 3.x); пусто = легаси 2.0

	JC   int `yaml:"jc,omitempty"`
	JMin int `yaml:"jmin,omitempty"`
	JMax int `yaml:"jmax,omitempty"`
	S1   int `yaml:"s1,omitempty"`
	S2   int `yaml:"s2,omitempty"`
	S3   int `yaml:"s3,omitempty"`
	S4   int `yaml:"s4,omitempty"`

	H1 string `yaml:"h1,omitempty"`
	H2 string `yaml:"h2,omitempty"`
	H3 string `yaml:"h3,omitempty"`
	H4 string `yaml:"h4,omitempty"`
	I1 string `yaml:"i1,omitempty"`
	I2 string `yaml:"i2,omitempty"`
	I3 string `yaml:"i3,omitempty"`
	I4 string `yaml:"i4,omitempty"`
	I5 string `yaml:"i5,omitempty"`

	HeaderProtectionKey    string `yaml:"header-protection-key,omitempty"`
	ContentPaddingAddition string `yaml:"content-padding-addition,omitempty"`
	RekeyAfterTime         string `yaml:"rekey-after-time,omitempty"`
	RekeyTimeout           string `yaml:"rekey-timeout,omitempty"`
	RejectAfterTime        string `yaml:"reject-after-time,omitempty"`
	KeepaliveTimeout       string `yaml:"keepalive-timeout,omitempty"`
	MaxHandshakeAttempts   string `yaml:"max-handshake-attempts,omitempty"`
	RandomTrailers         bool   `yaml:"random-trailers,omitempty"`
	DisableCookies         bool   `yaml:"disable-cookies,omitempty"`
}

type proxy struct {
	Name   string `yaml:"name"`
	Type   string `yaml:"type"`
	Server string `yaml:"server"`
	Port   int    `yaml:"port"`

	UUID     string `yaml:"uuid,omitempty"`     // vless/vmess
	Password string `yaml:"password,omitempty"` // trojan
	Cipher   string `yaml:"cipher,omitempty"`   // vmess: security
	AlterID  int    `yaml:"alterId,omitempty"`  // vmess
	Flow     string `yaml:"flow,omitempty"`     // vless

	UDP bool `yaml:"udp,omitempty"`

	TLS               bool         `yaml:"tls,omitempty"` // vless/vmess
	ServerName        string       `yaml:"servername,omitempty"`
	SNI               string       `yaml:"sni,omitempty"` // trojan
	ClientFingerprint string       `yaml:"client-fingerprint,omitempty"`
	ALPN              []string     `yaml:"alpn,omitempty"`
	Reality           *realityOpts `yaml:"reality-opts,omitempty"`

	Network  string     `yaml:"network,omitempty"`
	WSOpts   *wsOpts    `yaml:"ws-opts,omitempty"`   // ws и httpupgrade
	GrpcOpts *grpcOpts  `yaml:"grpc-opts,omitempty"`
	HTTPOpts *httpOpts  `yaml:"http-opts,omitempty"` // network: http

	// wireguard
	IP                  string       `yaml:"ip,omitempty"`
	IPv6                string       `yaml:"ipv6,omitempty"`
	PrivateKey          string       `yaml:"private-key,omitempty"`
	PublicKey           string       `yaml:"public-key,omitempty"`
	PreSharedKey        string       `yaml:"pre-shared-key,omitempty"`
	AllowedIPs          []string     `yaml:"allowed-ips,omitempty"`
	MTU                 int          `yaml:"mtu,omitempty"`
	PersistentKeepalive int          `yaml:"persistent-keepalive,omitempty"`
	Amnezia             *amneziaOpts `yaml:"amnezia-wg-option,omitempty"`
}

type group struct {
	Name            string   `yaml:"name"`
	Type            string   `yaml:"type"` // select | url-test
	Proxies         []string `yaml:"proxies"`
	URL             string   `yaml:"url,omitempty"`
	Interval        int      `yaml:"interval,omitempty"`
	Tolerance       int      `yaml:"tolerance,omitempty"`
	Lazy            *bool    `yaml:"lazy,omitempty"`
	DefaultSelected string   `yaml:"default-selected,omitempty"`
}

type listener struct {
	Name   string `yaml:"name"`
	Type   string `yaml:"type"`
	Listen string `yaml:"listen,omitempty"`
	Port   int    `yaml:"port,omitempty"`
	Proxy  string `yaml:"proxy,omitempty"` // привязка inbound к группе пула

	// tun-listener: device задаёт имя интерфейса (tun1, tun2…), по нему
	// MT-группы маршрутизируют трафик пула. dns-hijack ОБЯЗАН идти пустым
	// списком - у listener-варианта дефолт перехватывает 0.0.0.0:53,
	// а DNS роутера уже обслуживает MT.
	Device       string   `yaml:"device,omitempty"`
	Stack        string   `yaml:"stack,omitempty"`
	DNSHijack    []string `yaml:"dns-hijack"`
	AutoRoute    bool     `yaml:"auto-route"`
	MTU          int      `yaml:"mtu,omitempty"`
	Inet4Address []string `yaml:"inet4-address,omitempty"`
}

type config struct {
	Mode               string     `yaml:"mode"`
	LogLevel           string     `yaml:"log-level"`
	IPv6               bool       `yaml:"ipv6"`
	ExternalController string     `yaml:"external-controller"`
	Listeners          []listener `yaml:"listeners"`
	Proxies            []proxy    `yaml:"proxies"`
	ProxyGroups        []group    `yaml:"proxy-groups"`
}

// EligibleNodes - узлы, которые mihomo умеет запустить: vless/vmess/trojan
// с транспортами ws/grpc/http/httpupgrade (xhttp - lx-специфика).
func EligibleNodes(nodes []links.Node) (ok []links.Node, reasons []string) {
	for _, n := range nodes {
		if reason := nodeReason(n); reason != "" {
			reasons = append(reasons, reason)
			continue
		}
		ok = append(ok, n)
	}
	return ok, reasons
}

func nodeReason(n links.Node) string {
	switch n.Type {
	case "vless", "trojan", "vmess":
	default:
		return fmt.Sprintf("%s: тип %s идёт нативным пулом", n.Tag, n.Type)
	}
	if n.Encryption != "" && n.Encryption != "none" {
		return fmt.Sprintf("%s: vless-шифрование не поддерживается mihomo (нужен sing-box lx)", n.Tag)
	}
	switch n.Transport {
	case "", "tcp", "ws", "grpc", "http", "httpupgrade":
	default:
		return fmt.Sprintf("%s: транспорт %s не поддерживается mihomo (xhttp - только sing-box lx)", n.Tag, n.Transport)
	}
	return ""
}

func nodeProxy(poolTag string, n links.Node) (proxy, bool) {
	p := proxy{
		Name: poolTag + "|" + n.ConfName(), Type: n.Type,
		Server: n.Host, Port: n.Port, UDP: true,
	}
	switch n.Type {
	case "vless":
		p.UUID = n.UUID
		if n.Flow != "" && n.Transport == "" {
			p.Flow = n.Flow
		}
	case "vmess":
		p.UUID = n.UUID
		p.Cipher = n.Security
		if p.Cipher == "" {
			p.Cipher = "auto"
		}
		p.AlterID = n.AlterId
	case "trojan":
		p.Password = n.Password
	}
	if t := n.TLS; t != nil {
		p.TLS = n.Type != "trojan" // trojan в clash всегда TLS, поля не имеет
		if n.Type == "trojan" {
			p.SNI = t.SNI
		} else {
			p.ServerName = t.SNI
		}
		p.ClientFingerprint = t.Fingerprint
		p.ALPN = t.ALPN
		if t.Security == "reality" {
			p.Reality = &realityOpts{PublicKey: t.PublicKey, ShortID: t.ShortID}
		}
	}
	switch n.Transport {
	case "", "tcp":
	case "ws":
		p.Network = "ws"
		ws := &wsOpts{Path: n.Path}
		if n.HeaderHost != "" {
			ws.Headers = map[string]string{"Host": n.HeaderHost}
		}
		p.WSOpts = ws
	case "httpupgrade":
		p.Network = "httpupgrade"
		ws := &wsOpts{Path: n.Path}
		if n.HeaderHost != "" {
			ws.Headers = map[string]string{"Host": n.HeaderHost}
		}
		p.WSOpts = ws
	case "grpc":
		p.Network = "grpc"
		p.GrpcOpts = &grpcOpts{ServiceName: n.ServiceName}
	case "http":
		p.Network = "http"
		h := &httpOpts{Path: n.Path}
		if n.HeaderHost != "" {
			h.Headers = map[string][]string{"Host": {n.HeaderHost}}
		}
		p.HTTPOpts = h
	}
	return p, true
}

// wgProxy - wireguard-узел из конфига WG/AWG пула; Amnezia-поля (2.0 и
// 3.x) мапятся в amnezia-wg-option. Маркеры <r N> в I-полях и прочие
// экзотические значения честно ловит mihomo -t на Apply.
func wgProxy(cfg wgconf.Config, tag string) (proxy, error) {
	if cfg.PrivateKey == "" || cfg.Peer.PublicKey == "" || cfg.Peer.EndpointHost == "" {
		return proxy{}, fmt.Errorf("%s: конфиг без ключа или эндпоинта", tag)
	}
	p := proxy{
		Name: tag, Type: "wireguard",
		Server: cfg.Peer.EndpointHost, Port: cfg.Peer.EndpointPort,
		IP: stripCIDR(cfg.FirstIPv4()), PrivateKey: cfg.PrivateKey,
		PublicKey: cfg.Peer.PublicKey, UDP: true,
	}
	for _, a := range cfg.Addresses {
		if strings.Contains(a, ":") {
			p.IPv6 = stripCIDR(a)
			break
		}
	}
	if cfg.Peer.PresharedKey != "" {
		p.PreSharedKey = cfg.Peer.PresharedKey
	}
	if len(cfg.Peer.AllowedIPs) > 0 {
		p.AllowedIPs = cfg.Peer.AllowedIPs
	}
	if cfg.MTU > 0 {
		p.MTU = cfg.MTU
	}
	awg := cfg.AWG
	if awg.Present() {
		am := &amneziaOpts{}
		num := func(v *string) (int, bool) {
			if v == nil {
				return 0, false
			}
			n, err := strconv.Atoi(strings.SplitN(*v, "-", 2)[0])
			if err != nil {
				return 0, false
			}
			return n, true
		}
		if v, ok := num(awg.Jc); ok {
			am.JC = v
		}
		if v, ok := num(awg.Jmin); ok {
			am.JMin = v
		}
		if v, ok := num(awg.Jmax); ok {
			am.JMax = v
		}
		for _, f := range []struct {
			src *string
			dst *int
		}{
			{awg.S1, &am.S1}, {awg.S2, &am.S2}, {awg.S3, &am.S3}, {awg.S4, &am.S4},
		} {
			if v, ok := num(f.src); ok {
				*f.dst = v
			}
		}
		str := func(v *string) string {
			if v == nil {
				return ""
			}
			return strings.TrimSpace(*v)
		}
		am.H1, am.H2, am.H3, am.H4 = str(awg.H1), str(awg.H2), str(awg.H3), str(awg.H4)
		am.I1, am.I2, am.I3, am.I4, am.I5 = str(awg.I1), str(awg.I2), str(awg.I3), str(awg.I4), str(awg.I5)
		if cfg.AWG.AWG3() {
			am.Version = 3
			am.HeaderProtectionKey = str(awg.HeaderProtectionKey)
			am.ContentPaddingAddition = str(awg.ContentPaddingAddition)
			am.RekeyAfterTime = str(awg.RekeyAfterTime)
			am.RekeyTimeout = str(awg.RekeyTimeout)
			am.RejectAfterTime = str(awg.RejectAfterTime)
			am.MaxHandshakeAttempts = str(awg.MaxHandshakeAttempts)
			// keepalive-timeout у v3 - таймер; диапазонный keepalive
			// конфига идёт сюда
			am.KeepaliveTimeout = str(awg.KeepaliveTimeout)
		}
		if v := str(awg.RandomTrailers); v == "on" {
			am.RandomTrailers = true
		}
		if v := str(awg.DisableCookies); v == "on" {
			am.DisableCookies = true
		}
		p.Amnezia = am
	}
	switch {
	case cfg.Peer.KeepaliveRange != "":
		// AWG 3.x диапазонный keepalive: обычного поля нет, живёт в
		// amnezia-wg-option (v3); держим его там
		if p.Amnezia == nil {
			p.Amnezia = &amneziaOpts{Version: 3}
		} else {
			p.Amnezia.Version = 3
		}
		p.Amnezia.KeepaliveTimeout = cfg.Peer.KeepaliveRange
	case cfg.Peer.PersistentKeepalive > 0:
		p.PersistentKeepalive = cfg.Peer.PersistentKeepalive
	}
	return p, nil
}

func stripCIDR(a string) string {
	if i := strings.IndexByte(a, '/'); i > 0 {
		return a[:i]
	}
	return a
}

type Params struct {
	ClashPort int
}

// BuildConfig собирает YAML mawg-экземпляра mihomo: на каждый пул
// tun-listener + mixed-listener (обa привязаны proxy к группе пула) и
// группа url-test/select. Теги узлов - те же mawg-<пул>|<имя>, что у
// sing-box: UI и ротация не различают ядра.
func BuildConfig(pools []PoolSpec, p Params) ([]byte, []string, error) {
	if p.ClashPort == 0 {
		return nil, nil, fmt.Errorf("порт external-controller не задан")
	}
	var skipped []string
	cfg := config{
		Mode: "rule", LogLevel: "info",
		ExternalController: fmt.Sprintf("127.0.0.1:%d", p.ClashPort),
		Listeners:          []listener{}, Proxies: []proxy{}, ProxyGroups: []group{},
	}
	for _, spec := range pools {
		if spec.MixedPort == 0 {
			return nil, nil, fmt.Errorf("пул %s: порт пробы не задан", spec.Name)
		}
		groupTag := "mawg-" + spec.Name
		var tags []string
		for _, n := range spec.Nodes {
			if reason := nodeReason(n); reason != "" {
				skipped = append(skipped, reason)
				continue
			}
			np, _ := nodeProxy(groupTag, n)
			cfg.Proxies = append(cfg.Proxies, np)
			tags = append(tags, np.Name)
		}
		for _, c := range spec.WG {
			tag := groupTag + "|" + strconv.Itoa(len(tags)+1)
			wp, err := wgProxy(c, tag)
			if err != nil {
				skipped = append(skipped, fmt.Sprintf("пул %s: %v", spec.Name, err))
				continue
			}
			cfg.Proxies = append(cfg.Proxies, wp)
			tags = append(tags, tag)
		}
		if len(tags) == 0 {
			skipped = append(skipped, fmt.Sprintf("пул %s: ни один узел не подходит mihomo, tun не создан", spec.Name))
			continue
		}
		cfg.Listeners = append(cfg.Listeners,
			listener{
				Name: "tun-" + spec.Name, Type: "tun", Proxy: groupTag,
				Device: spec.Tun, Stack: tunStack, DNSHijack: []string{},
				AutoRoute: false, MTU: 9000, Inet4Address: []string{spec.TunIP},
			},
			listener{
				Name: "mixed-" + spec.Name, Type: "mixed", Proxy: groupTag,
				Listen: "127.0.0.1", Port: spec.MixedPort,
			},
		)
		g := group{Name: groupTag, Proxies: tags}
		if spec.GroupMode == "selector" || spec.GroupMode == "rotate" {
			// rotate - тоже select: переключает сам mawg по таймеру,
			// строго на ответившие замеру (слово владельца 2026-10-17)
			g.Type = "select"
			for _, t := range tags {
				if t == spec.Selected {
					g.DefaultSelected = t
					break
				}
			}
		} else {
			g.Type = "url-test"
			g.URL = spec.ProbeTarget
			interval := spec.CheckIntervalSec
			if interval <= 0 {
				interval = 60
			}
			g.Interval = interval
			g.Tolerance = 10
			lazy := false // замеры честные и когда узел не активен (как urltest sing-box)
			g.Lazy = &lazy
		}
		cfg.ProxyGroups = append(cfg.ProxyGroups, g)
	}
	if len(cfg.ProxyGroups) == 0 {
		return nil, skipped, fmt.Errorf("нет подходящих mihomo узлов")
	}
	data, err := yaml.Marshal(cfg)
	return data, skipped, err
}
