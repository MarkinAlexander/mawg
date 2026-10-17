package links

// vmess:// - контейнер v2rayN: base64(JSON), реже legacy-cleartext
// base64("method:uuid@host:port?query"). Разбор по контракту L×Box
// (assets/contract/registry/protocols/vmess.json - обёртка от автора
// sing-box-lx) и схеме ядра option/vmess.go; шифры - набор sing-vmess,
// мусор не режектим, а уводим на auto (иначе весь конфиг мёртв).

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

func parseVmess(source, raw string) (Node, error) {
	body := strings.TrimPrefix(raw, "vmess://")
	// '#fragment' после base64-части - метка только legacy-ветки (контракт:
	// в JSON-форме метку несёт ps, fragment выбрасывается)
	frag := ""
	if i := strings.IndexByte(body, '#'); i >= 0 {
		frag = body[i+1:]
		if d, err := url.PathUnescape(frag); err == nil {
			frag = d
		}
		body = body[:i]
	}
	decoded, err := decodeBase64URL(body)
	if err != nil {
		return Node{}, fmt.Errorf("vmess: base64 не читается: %w", err)
	}
	text := strings.TrimSpace(string(decoded))
	if strings.HasPrefix(text, "{") {
		return vmessFromJSON(source, raw, text)
	}
	return vmessFromLegacy(source, raw, text, frag)
}

// vmessSecurity - шифр канала: точный набор ядра (auto/none/zero/
// aes-128-cfb/aes-128-gcm/chacha20-poly1305); пусто/мусор -> auto.
func vmessSecurity(s string) string {
	v := strings.ToLower(strings.TrimSpace(s))
	switch v {
	case "none", "zero", "aes-128-cfb", "aes-128-gcm", "chacha20-poly1305":
		return v
	case "chacha20-ietf-poly1305":
		return "chacha20-poly1305"
	}
	return "auto"
}

func vmessTag(ps, host string, port int) string {
	if ps = strings.TrimSpace(ps); ps != "" {
		return ps
	}
	return host + ":" + strconv.Itoa(port)
}

func vmessFromJSON(source, raw, body string) (Node, error) {
	var j struct {
		Add         string `json:"add"`
		Port        any    `json:"port"`
		ID          string `json:"id"`
		PS          string `json:"ps"`
		Scy         string `json:"scy"`
		AID         any    `json:"aid"`
		TLS         any    `json:"tls"`
		SNI         string `json:"sni"`
		ALPN        string `json:"alpn"`
		FP          string `json:"fp"`
		Net         string `json:"net"`
		Path        string `json:"path"`
		Host        string `json:"host"`
		ServiceName string `json:"serviceName"`
		Mode        string `json:"mode"`
	}
	if err := json.Unmarshal([]byte(body), &j); err != nil {
		return Node{}, fmt.Errorf("vmess: контейнер не разбирается: %w", err)
	}
	port := anyToInt(j.Port)
	if j.Add == "" || port <= 0 || j.ID == "" {
		return Node{}, errors.New("vmess: нет сервера, порта или uuid")
	}
	n := Node{
		Type: "vmess", Host: j.Add, Port: port, UUID: j.ID,
		Security: vmessSecurity(j.Scy),
		Tag:      source + "|" + vmessTag(j.PS, j.Add, port),
		Raw:      raw,
	}
	if aid := anyToInt(j.AID); aid > 0 {
		n.AlterId = aid
	}
	if vmessTLSOn(j.TLS) {
		t := &TLS{Security: "tls", SNI: j.SNI}
		if t.SNI == "" {
			t.SNI = j.Host // host -> sni для ws/xhttp (контракт LxBox)
		}
		t.Fingerprint = j.FP
		for _, a := range strings.Split(j.ALPN, ",") {
			if a = strings.TrimSpace(a); a != "" {
				t.ALPN = append(t.ALPN, a)
			}
		}
		n.TLS = t
	}
	vmessTransport(&n, j.Net, j.Path, j.Host, j.ServiceName, j.Mode)
	return n, nil
}

// vmessTLSOn: tls=true (JSON bool), "tls" или "true" (строкой).
func vmessTLSOn(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t == "tls" || t == "true"
	}
	return false
}

// net: tcp(пусто)/ws/http/h2/grpc/httpupgrade/xhttp; h2 = http+TLS.
func vmessTransport(n *Node, net, path, host, serviceName, mode string) {
	switch net {
	case "", "tcp":
	case "ws":
		n.Transport = "ws"
		if path == "" {
			path = "/"
		}
		n.Path, n.HeaderHost = path, host
	case "httpupgrade":
		n.Transport, n.Path, n.HeaderHost = "httpupgrade", path, host
	case "grpc":
		n.Transport = "grpc"
		if serviceName != "" {
			n.ServiceName = serviceName
		} else {
			n.ServiceName = path
		}
	case "http", "h2":
		n.Transport, n.Path, n.HeaderHost = "http", path, host
		if n.TLS == nil {
			n.TLS = &TLS{Security: "tls"}
		}
	case "xhttp":
		n.Transport, n.Path, n.HeaderHost = "xhttp", path, host
		n.Mode = mode
		if n.Mode == "" {
			n.Mode = "auto"
		}
	}
}

// vmessFromLegacy - cleartext-ветка: base64("method:uuid@host:port?query"),
// query как у vless (security=tls, type=ws/grpc/...).
func vmessFromLegacy(source, raw, body, frag string) (Node, error) {
	u, err := url.Parse("vmess://" + body)
	if err != nil || u.Host == "" {
		return Node{}, errors.New("vmess: legacy-запись не разбирается")
	}
	method, uuid := "", u.User.Username()
	if pw, ok := u.User.Password(); ok {
		method, uuid = u.User.Username(), pw
	}
	// метка бывает снаружи (после base64) и внутри самой legacy-записи
	if frag == "" && u.Fragment != "" {
		if d, err := url.PathUnescape(u.Fragment); err == nil {
			frag = d
		} else {
			frag = u.Fragment
		}
	}
	n := Node{
		Type: "vmess", Host: u.Hostname(), Port: portOf(u), UUID: uuid,
		Security: vmessSecurity(method),
		Tag:      source + "|" + proxyTag(u, frag),
		Raw:      raw,
	}
	if n.Host == "" || n.Port == 0 || n.UUID == "" {
		return Node{}, errors.New("vmess: нет сервера, порта или uuid")
	}
	q := query(u)
	n.TLS = tlsFromQuery(q)
	transportFromQuery(&n, q)
	if s := q.Get("scy"); s != "" {
		n.Security = vmessSecurity(s)
	}
	return n, nil
}

func anyToInt(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(t))
		return n
	}
	return 0
}
