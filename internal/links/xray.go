package links

// Полные конфиги Xray в подписке: часть панелей (панель 11.2026)
// отдают не список proxy-ссылок, а v2rayN-экспорт - массив клиентских
// конфигов, по одному на узел ({dns, outbounds, routing, remarks}); имя
// узла несёт remarks. До этого тело шло в построчный разбор и падало
// «неизвестная схема» на первом же JSON-фрагменте.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// xrayUtilityProtocols - служебные outbound шаблона v2rayN: узла не
// описывают (конфиг = прокси + freedom + blackhole)
var xrayUtilityProtocols = map[string]bool{
	"freedom": true, "blackhole": true, "dns": true, "loopback": true,
}

type xraySettings struct {
	Vnext []struct {
		Address string `json:"address"`
		Port    int    `json:"port"`
		Users   []struct {
			ID         string `json:"id"`
			Encryption string `json:"encryption"`
			Flow       string `json:"flow"`
			Security   string `json:"security"`
			AlterId    int    `json:"alterId"`
		} `json:"users"`
	} `json:"vnext"`
	// trojan/shadowsocks держат серверы в settings.servers
	Servers []struct {
		Address  string `json:"address"`
		Port     int    `json:"port"`
		Password string `json:"password"`
	} `json:"servers"`
}

// xrayTLS - tlsSettings и realitySettings совпадают по полям, нужным ноде
type xrayTLS struct {
	ServerName    string   `json:"serverName"`
	Fingerprint   string   `json:"fingerprint"`
	ALPN          []string `json:"alpn"`
	AllowInsecure bool     `json:"allowInsecure"`
	PublicKey     string   `json:"publicKey"`
	ShortID       string   `json:"shortId"`
}

type xrayStream struct {
	Network         string   `json:"network"`
	Security        string   `json:"security"`
	TLSSettings     *xrayTLS `json:"tlsSettings"`
	RealitySettings *xrayTLS `json:"realitySettings"`
	WsSettings      struct {
		Path    string `json:"path"`
		Headers struct {
			Host string `json:"Host"`
		} `json:"headers"`
	} `json:"wsSettings"`
	GrpcSettings struct {
		ServiceName string `json:"serviceName"`
	} `json:"grpcSettings"`
	HttpupgradeSettings struct {
		Path string `json:"path"`
		Host string `json:"host"`
	} `json:"httpupgradeSettings"`
	// xhttp в старых сборках Xray назывался splithttp
	XhttpSettings struct {
		Path string `json:"path"`
		Host string `json:"host"`
		Mode string `json:"mode"`
	} `json:"xhttpSettings"`
	SplithttpSettings struct {
		Path string `json:"path"`
		Host string `json:"host"`
		Mode string `json:"mode"`
	} `json:"splithttpSettings"`
}

// addXrayConfig - один полный конфиг Xray: узел = первый неслужебный
// outbound (остальные - служебные цепочки шаблона). Ошибка - целиком на
// конфиг, чтобы подписка-массив честно пропустила битый элемент.
func addXrayConfig(res *Result, body []byte) error {
	var cfg struct {
		Remarks   string            `json:"remarks"`
		Outbounds []json.RawMessage `json:"outbounds"`
	}
	if err := json.Unmarshal(body, &cfg); err != nil {
		return fmt.Errorf("json xray: %w", err)
	}
	added := false
	first := true
	warnsBefore := len(res.Warnings)
	for _, raw := range cfg.Outbounds {
		var ob struct {
			Tag      string          `json:"tag"`
			Protocol string          `json:"protocol"`
			Settings json.RawMessage `json:"settings"`
			Stream   *xrayStream     `json:"streamSettings"`
		}
		if json.Unmarshal(raw, &ob) != nil || xrayUtilityProtocols[ob.Protocol] {
			continue
		}
		name := ob.Tag
		if first {
			if r := strings.TrimSpace(cfg.Remarks); r != "" {
				name = r
			}
			first = false
		}
		n, err := xrayNode(res.Source, name, ob.Protocol, ob.Settings, ob.Stream)
		if err != nil {
			res.warn(fmt.Sprintf("outbound %q: %v", ob.Tag, err))
			continue
		}
		res.Nodes = append(res.Nodes, n)
		added = true
	}
	// ничего не вышло и жалоб не было - пустой шаблон, а не попытки
	if !added && len(res.Warnings) == warnsBefore {
		return fmt.Errorf("в конфиге Xray нет узла (vless/vmess/trojan)")
	}
	return nil
}

func xrayNode(source, name, protocol string, settings json.RawMessage, st *xrayStream) (Node, error) {
	var s xraySettings
	if len(settings) > 0 {
		if err := json.Unmarshal(settings, &s); err != nil {
			return Node{}, fmt.Errorf("%s: settings не читаются: %w", protocol, err)
		}
	}
	vnext := func() (string, int, bool) {
		if len(s.Vnext) == 0 || s.Vnext[0].Address == "" || s.Vnext[0].Port <= 0 {
			return "", 0, false
		}
		return s.Vnext[0].Address, s.Vnext[0].Port, true
	}
	var n Node
	switch protocol {
	case "vless":
		host, port, ok := vnext()
		if !ok {
			return Node{}, fmt.Errorf("vless: нет vnext с сервером и портом")
		}
		n = Node{Type: "vless", Host: host, Port: port}
		if len(s.Vnext[0].Users) > 0 {
			u := s.Vnext[0].Users[0]
			n.UUID, n.Flow = u.ID, u.Flow
			if u.Encryption != "" && u.Encryption != "none" {
				n.Encryption = u.Encryption
			}
		}
	case "vmess":
		host, port, ok := vnext()
		if !ok {
			return Node{}, fmt.Errorf("vmess: нет vnext с сервером и портом")
		}
		n = Node{Type: "vmess", Host: host, Port: port, Security: "auto"}
		if len(s.Vnext[0].Users) > 0 {
			u := s.Vnext[0].Users[0]
			n.UUID = u.ID
			n.Security = vmessSecurity(u.Security)
			if u.AlterId > 0 {
				n.AlterId = u.AlterId
			}
		}
	case "trojan":
		if len(s.Servers) == 0 || s.Servers[0].Address == "" || s.Servers[0].Port <= 0 {
			return Node{}, fmt.Errorf("trojan: нет servers с сервером и портом")
		}
		n = Node{Type: "trojan", Host: s.Servers[0].Address, Port: s.Servers[0].Port, Password: s.Servers[0].Password}
	default:
		return Node{}, fmt.Errorf("протокол %s не поддерживается", protocol)
	}
	if n.UUID == "" && n.Password == "" {
		return Node{}, fmt.Errorf("%s: нет учётных данных (uuid/пароль)", protocol)
	}
	n.Tag = source + "|" + name
	if st != nil {
		xrayStreamInto(&n, st)
	}
	return n, nil
}

// xrayStreamInto - streamSettings в поля ноды; сетевые типы сведены к
// общему разбору vmess-контейнера (vmessTransport: дефолты ws-пути,
// xhttp-режима, h2->TLS).
func xrayStreamInto(n *Node, st *xrayStream) {
	switch st.Security {
	case "tls":
		t := &TLS{Security: "tls"}
		if st.TLSSettings != nil {
			t.SNI, t.Fingerprint, t.ALPN = st.TLSSettings.ServerName, st.TLSSettings.Fingerprint, st.TLSSettings.ALPN
			t.Insecure = st.TLSSettings.AllowInsecure
		}
		n.TLS = t
	case "reality":
		t := &TLS{Security: "reality"}
		if st.RealitySettings != nil {
			t.SNI, t.Fingerprint = st.RealitySettings.ServerName, st.RealitySettings.Fingerprint
			t.PublicKey, t.ShortID = st.RealitySettings.PublicKey, st.RealitySettings.ShortID
		}
		n.TLS = t
	}
	path, host, mode := "", "", ""
	switch st.Network {
	case "ws":
		path, host = st.WsSettings.Path, st.WsSettings.Headers.Host
	case "grpc":
		path = st.GrpcSettings.ServiceName
	case "httpupgrade":
		path, host = st.HttpupgradeSettings.Path, st.HttpupgradeSettings.Host
	case "xhttp":
		path, host, mode = st.XhttpSettings.Path, st.XhttpSettings.Host, st.XhttpSettings.Mode
	case "splithttp":
		path, host, mode = st.SplithttpSettings.Path, st.SplithttpSettings.Host, st.SplithttpSettings.Mode
	}
	if st.Network != "" && st.Network != "tcp" {
		vmessTransport(n, st.Network, path, host, st.GrpcSettings.ServiceName, mode)
	}
}
