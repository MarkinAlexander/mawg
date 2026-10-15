// Package dnsprobe - зонд DNS-уровней канарейкой (63-док, схема владельца):
// A-запрос канарейного домена через каждый уровень (локальный резолвер,
// dns-proxy MT, апстрим провайдера, DoH/DoT) и сверка ответа с каноническим
// набором адресов. Чисто = минимум один адрес из набора в ответе; ноль при
// непустом ответе = подмена/блок (например NXDOMAIN-перехват даёт «нет
// ответа»). DNS-проводка собрана на stdlib: UDP и RFC 8484 DoH (POST
// application/dns-message), DoT (RFC 7858, 2-байтовое обрамление в TLS).
package dnsprobe

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// CanaryDomain - канарейка спуфа: фиксированный набор A-записей, который
// провайдер-перехватчик подменяет (NXDOMAIN/чужие IP), а честный резолвер
// возвращает целиком (63-док).
const CanaryDomain = "deb.torproject.org"

var CanaryIPs = map[string]bool{
	"204.8.99.144":     true,
	"204.8.99.146":     true,
	"95.216.163.36":    true,
	"116.202.120.165":  true,
	"116.202.120.166":  true,
}

// Вер dict'ы результата (JSON-контракт с панелью).
const (
	VerdictClean    = "clean"    // в ответе есть адрес из канонического набора
	VerdictSpoof    = "spoof"    // ответ есть, но канонических адресов ноль
	VerdictNoAnswer = "noanswer" // пустой ответ (NXDOMAIN/блок)
	VerdictError    = "error"    // сеть/таймаут/кривой ответ
)

// Level - один зондимый уровень.
type Level struct {
	Level  string `json:"level"`           // подпись для панели
	Kind   string `json:"kind"`            // udp | dot | doh
	Server string `json:"server"`          // host:port | URL (для doh)
	SNI    string `json:"sni,omitempty"`   // ServerName для DoT (если адрес - IP)
}

// Result - исход пробы одного уровня.
type Result struct {
	Level     string `json:"level"`
	Kind      string `json:"kind"`
	Server    string `json:"server"`
	Verdict   string `json:"verdict"`
	Canonical int    `json:"canonical"` // сколько из канонического набора
	Total     int    `json:"total"`     // всего A-записей в ответе
	MS        int64  `json:"ms"`
	Detail    string `json:"detail,omitempty"` // текст ошибки сети, если была
}

// Defaults - стандартный набор уровней для openwrt/keenetic: локальный
// dnsmasq, dns-proxy MT, апстрим из resolv.conf, два публичных DoH и DoT.
// 1.1.1.1/dns.google - по IP/с известным SNI, чтобы зонд не зависел от
// bootstrap-резолва.
func Defaults() []Level {
	levels := []Level{
		{Level: "dnsmasq", Kind: "udp", Server: "127.0.0.1:53"},
		{Level: "dns-proxy MT", Kind: "udp", Server: "127.0.0.1:3553"},
	}
	if up := UpstreamFromResolv(); up != "" {
		levels = append(levels, Level{Level: "upstream ISP", Kind: "udp", Server: up})
	}
	return append(levels,
		Level{Level: "DoH Cloudflare", Kind: "doh", Server: "https://1.1.1.1/dns-query"},
		Level{Level: "DoH Google", Kind: "doh", Server: "https://dns.google/dns-query"},
		Level{Level: "DoT Google", Kind: "dot", Server: "8.8.8.8:853", SNI: "dns.google"},
		// «интернет жив?» (46-док §3): TCP мимо DNS на стабильные IP -
		// один иностранный, один внутренний; если оба мертвы - сеть вниз,
		// эскалацию DNS не трогаем
		Level{Level: "интернет TCP 1.1.1.1:443", Kind: "tcp", Server: "1.1.1.1:443"},
		Level{Level: "интернет TCP 77.88.8.8:443", Kind: "tcp", Server: "77.88.8.8:443"},
	)
}

// UpstreamFromResolv - первый nameserver из resolv.conf (openwrt:
// /tmp/resolv.conf.d/resolv.conf.auto, fallback /etc/resolv.conf).
func UpstreamFromResolv() string {
	for _, path := range []string{
		"/tmp/resolv.conf.d/resolv.conf.auto",
		"/etc/resolv.conf",
	} {
		if up := firstNameserver(path); up != "" {
			return up
		}
	}
	return ""
}

func firstNameserver(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "nameserver ") {
			if ns := strings.TrimSpace(strings.TrimPrefix(line, "nameserver ")); ns != "" {
				return ns + ":53"
			}
		}
	}
	return ""
}

// Run прогоняет уровни параллельно (каждый со своим таймаутом), порядок
// результата соответствует порядку levels.
func Run(levels []Level, timeout time.Duration) []Result {
	out := make([]Result, len(levels))
	var wg sync.WaitGroup
	for i, l := range levels {
		wg.Add(1)
		go func(i int, l Level) {
			defer wg.Done()
			out[i] = Check(l, timeout)
		}(i, l)
	}
	wg.Wait()
	return out
}

// Check - проба одного уровня: запрос канарейки, вердикт по канонике.
// Kind "tcp" - особый: не DNS, а «интернет жив?» (46-док §3) - чистый
// TCP-connect на стабильный IP:port мимо DNS; clean = соединение прошло.
func Check(l Level, timeout time.Duration) Result {
	res := Result{Level: l.Level, Kind: l.Kind, Server: l.Server}
	start := time.Now()
	var ips []string
	var err error
	switch l.Kind {
	case "tcp":
		conn, derr := net.DialTimeout("tcp", l.Server, timeout)
		if derr != nil {
			err = fmt.Errorf("dial tcp %s: %w", l.Server, derr)
		} else {
			_ = conn.Close()
		}
		res.MS = time.Since(start).Milliseconds()
		if err != nil {
			res.Verdict = VerdictError
			res.Detail = err.Error()
			return res
		}
		res.Verdict = VerdictClean
		return res
	case "udp":
		ips, err = exchangeUDP(l.Server, CanaryDomain, timeout)
	case "dot":
		ips, err = exchangeDoT(l.Server, l.SNI, CanaryDomain, timeout)
	case "doh":
		ips, err = exchangeDoH(l.Server, CanaryDomain, timeout)
	default:
		err = fmt.Errorf("неизвестный транспорт %q", l.Kind)
	}
	res.MS = time.Since(start).Milliseconds()
	if err != nil {
		res.Verdict = VerdictError
		res.Detail = err.Error()
		return res
	}
	res.Verdict, res.Canonical, res.Total = verdictOf(ips)
	return res
}

func verdictOf(ips []string) (verdict string, canonical, total int) {
	for _, ip := range ips {
		total++
		if CanaryIPs[ip] {
			canonical++
		}
	}
	switch {
	case total == 0:
		return VerdictNoAnswer, 0, 0
	case canonical >= 1:
		return VerdictClean, canonical, total
	default:
		return VerdictSpoof, 0, total
	}
}

// buildQuery - DNS-запрос A по домену (RD=1, QTYPE=A, QCLASS=IN).
func buildQuery(domain string, id uint16) []byte {
	var b []byte
	b = binary.BigEndian.AppendUint16(b, id)
	b = binary.BigEndian.AppendUint16(b, 0x0100) // флаги: RD
	b = binary.BigEndian.AppendUint16(b, 1)      // QDCOUNT
	b = binary.BigEndian.AppendUint16(b, 0)      // ANCOUNT
	b = binary.BigEndian.AppendUint16(b, 0)      // NSCOUNT
	b = binary.BigEndian.AppendUint16(b, 0)      // ARCOUNT
	for _, label := range strings.Split(strings.TrimSuffix(domain, "."), ".") {
		if label == "" {
			continue
		}
		b = append(b, byte(len(label)))
		b = append(b, label...)
	}
	b = append(b, 0)
	b = binary.BigEndian.AppendUint16(b, 1) // QTYPE = A
	b = binary.BigEndian.AppendUint16(b, 1) // QCLASS = IN
	return b
}

// parseA - уникальные IPv4 из A-записей ответа (сжатие имён поддержано).
func parseA(resp []byte) []string {
	if len(resp) < 12 {
		return nil
	}
	qd := int(binary.BigEndian.Uint16(resp[4:6]))
	an := int(binary.BigEndian.Uint16(resp[6:8]))
	off := 12
	skipName := func() {
		for off < len(resp) {
			l := int(resp[off])
			if l == 0 {
				off++
				return
			}
			if l&0xC0 == 0xC0 {
				off += 2
				return
			}
			off += 1 + l
		}
	}
	for i := 0; i < qd && off < len(resp); i++ {
		skipName()
		off += 4
	}
	seen := map[string]bool{}
	var out []string
	for i := 0; i < an && off < len(resp); i++ {
		skipName()
		if off+10 > len(resp) {
			return out
		}
		typ := binary.BigEndian.Uint16(resp[off : off+2])
		rdlen := int(binary.BigEndian.Uint16(resp[off+8 : off+10]))
		off += 10
		if off+rdlen > len(resp) {
			return out
		}
		if typ == 1 && rdlen == 4 {
			ip := net.IP(resp[off : off+4]).String()
			if !seen[ip] {
				seen[ip] = true
				out = append(out, ip)
			}
		}
		off += rdlen
	}
	return out
}

func randomID() uint16 {
	var b [2]byte
	_, _ = rand.Read(b[:])
	return binary.BigEndian.Uint16(b[:])
}

func exchangeUDP(server, domain string, timeout time.Duration) ([]string, error) {
	id := randomID()
	q := buildQuery(domain, id)
	conn, err := net.DialTimeout("udp", server, timeout)
	if err != nil {
		return nil, fmt.Errorf("dial udp %s: %w", server, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if _, err = conn.Write(q); err != nil {
		return nil, fmt.Errorf("write udp %s: %w", server, err)
	}
	buf := make([]byte, 4096)
	// до двух чтений: возможен чужой/запоздалый ответ с другим id
	for attempt := 0; attempt < 2; attempt++ {
		n, err := conn.Read(buf)
		if err != nil {
			return nil, fmt.Errorf("read udp %s: %w", server, err)
		}
		if n >= 12 && binary.BigEndian.Uint16(buf[:2]) == id {
			return parseA(buf[:n]), nil
		}
	}
	return nil, fmt.Errorf("udp %s: ответ без совпадения id", server)
}

// exchangeDoT - DNS по TLS (RFC 7858): тот же TCP-фрейминг внутри TLS.
func exchangeDoT(server, sni, domain string, timeout time.Duration) ([]string, error) {
	id := randomID()
	q := framed(buildQuery(domain, id))
	dialer := net.Dialer{Timeout: timeout}
	conf := &tls.Config{ServerName: sni} // #nosec G402 - зонд публичных DoT, ServerName задаёт уровень
	conn, err := tls.DialWithDialer(&dialer, "tcp", server, conf)
	if err != nil {
		return nil, fmt.Errorf("dial dot %s: %w", server, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if _, err = conn.Write(q); err != nil {
		return nil, fmt.Errorf("write dot %s: %w", server, err)
	}
	resp, err := readFramed(conn)
	if err != nil {
		return nil, fmt.Errorf("read dot %s: %w", server, err)
	}
	return parseA(resp), nil
}

// exchangeDoH - RFC 8484: POST application/dns-message (универсально для
// всех DoH-серверов, в отличие от JSON-эндпоинтов конкретных вендоров).
func exchangeDoH(urlStr, domain string, timeout time.Duration) ([]string, error) {
	id := randomID()
	q := buildQuery(domain, id)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, urlStr, bytes.NewReader(q))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/dns-message")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("doh %s: %w", urlStr, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("doh %s: http %d", urlStr, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 65535))
	if err != nil {
		return nil, fmt.Errorf("doh %s: %w", urlStr, err)
	}
	return parseA(body), nil
}

func framed(msg []byte) []byte {
	out := make([]byte, 2+len(msg))
	binary.BigEndian.PutUint16(out, uint16(len(msg)))
	copy(out[2:], msg)
	return out
}

func readFramed(r io.Reader) ([]byte, error) {
	var lenBuf [2]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, err
	}
	msg := make([]byte, binary.BigEndian.Uint16(lenBuf[:]))
	if _, err := io.ReadFull(r, msg); err != nil {
		return nil, err
	}
	return msg, nil
}
