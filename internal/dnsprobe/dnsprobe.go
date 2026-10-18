// Package dnsprobe - зонд DNS-уровней канарейкой (63-док, схема владельца):
// A-запрос канарейного домена через каждый уровень (локальный резолвер,
// dns-proxy MT, апстрим провайдера, DoH/DoT) и сверка ответа с каноническим
// набором адресов. Чисто = минимум один адрес из набора в ответе; ноль при
// непустом ответе = подмена/блок (например NXDOMAIN-перехват даёт «нет
// ответа»). DNS-проводка собрана на stdlib: UDP и RFC 8484 DoH (POST
// application/dns-message), DoT (RFC 7858, 2-байтовое обрамление в TLS).
// Уровень может нести ноги сервиса (Level.IPs, эмпирика Фазы E): при
// смерти одной ноги сервиса пробовать ЕГО ЖЕ оставшиеся IP - иначе зонд
// флипает по порядку A-записей (кейс dns.google: 8.8.8.8 мёртв, 8.8.4.4
// жив; диал мёртвой ноги съедал весь таймаут, до живой не доходил).
package dnsprobe

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
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
	Level  string `json:"level"`         // подпись для панели
	Kind   string `json:"kind"`          // udp | dot | doh | tcp
	Server string `json:"server"`        // host:port | URL (для doh)
	SNI    string `json:"sni,omitempty"` // ServerName для DoT (если адрес - IP)
	// IPs - ноги сервиса (63-док, эмпирика Фазы E): при смерти одной ноги
	// сервиса пробовать ЕГО ЖЕ оставшиеся IP; нога = IP или ip:port (порт
	// по умолчанию берётся из Server). Пусто = одна нога как в Server.
	IPs []string `json:"ips,omitempty"`
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
	IP        string `json:"ip,omitempty"`     // нога сервиса, которая ответила
	Detail    string `json:"detail,omitempty"` // текст ошибки сети, если была
}

// Defaults - стандартный набор уровней для openwrt/keenetic: локальный
// dnsmasq, dns-proxy MT, апстрим из resolv.conf, два публичных DoH и DoT.
// 1.1.1.1/dns.google - по IP/с известным SNI, чтобы зонд не зависел от
// bootstrap-резолва.
func Defaults() []Level {
	// Level-лейблы - человекочитаемые подписи для панели (67-док, глоссарий-
	// проход Обзора: «dnsmasq» и пр. в подсказках [?], не в подписях).
	// DoH/DoT/TCP-лейблы владелец велел оставить как есть - расшифровка
	// каждого уровня делается подсказкой [?] на стороне UI.
	levels := []Level{
		{Level: "Служба DNS роутера", Kind: "udp", Server: "127.0.0.1:53"},
		{Level: "DNS через MT", Kind: "udp", Server: "127.0.0.1:3553"},
	}
	if up := UpstreamFromResolv(); up != "" {
		name := "DNS провайдера"
		if host, _, err := net.SplitHostPort(up); err == nil {
			if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
				// Entware-Кинетик: /etc/resolv.conf -> 127.0.0.1 (ndm) -
				// это не провайдер, зовём честно (слово владельца
				// 2026-10-18: «может он за DNS провайдера посчитал
				// роутер?» - да, он)
				name = "Системный резолвер"
			}
		}
		levels = append(levels, Level{Level: name, Kind: "udp", Server: up})
	}
	// прямые UDP к публичным - примитивный DNS-чекер (слово владельца
	// 2026-10-18): по канарейке честно видно «работает / ПОДМЕНА / нет
	// ответа» для каждого сервера на этой сети; с fwmark проба идёт
	// напрямую, мимо перехвата
	return append(levels,
		Level{Level: "UDP Google 8.8.8.8", Kind: "udp", Server: "8.8.8.8:53"},
		Level{Level: "UDP Cloudflare 1.1.1.1", Kind: "udp", Server: "1.1.1.1:53"},
		Level{Level: "UDP Яндекс 77.88.8.8", Kind: "udp", Server: "77.88.8.8:53"},
		// Ноги сервисов - из списка блокировки 63-док (Фаза E: Google выжил
		// на 8.8.4.4 при мёртвом 8.8.8.8; перебор ног делает вердикт уровня
		// независимым от порядка A-записей)
		Level{Level: "DoH Cloudflare", Kind: "doh", Server: "https://1.1.1.1/dns-query",
			IPs: []string{"1.1.1.1", "1.0.0.1"}},
		Level{Level: "DoH Google", Kind: "doh", Server: "https://dns.google/dns-query",
			IPs: []string{"8.8.8.8", "8.8.4.4"}},
		Level{Level: "DoT Google", Kind: "dot", Server: "8.8.8.8:853", SNI: "dns.google",
			IPs: []string{"8.8.8.8", "8.8.4.4"}},
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
// udp/dot/doh перебирают ноги сервиса (Level.IPs, Фаза E).
func Check(l Level, timeout time.Duration) (res Result) {
	res = Result{Level: l.Level, Kind: l.Kind, Server: l.Server}
	start := time.Now()
	defer func() { res.MS = time.Since(start).Milliseconds() }()
	switch l.Kind {
	case "tcp":
		conn, derr := net.DialTimeout("tcp", l.Server, timeout)
		if derr != nil {
			res.Verdict = VerdictError
			res.Detail = fmt.Errorf("dial tcp %s: %w", l.Server, derr).Error()
			return res
		}
		_ = conn.Close()
		res.Verdict = VerdictClean
		return res
	case "udp", "dot", "doh":
		res.IP, res.Verdict, res.Canonical, res.Total, res.Detail = probeLegs(l, timeout)
		return res
	default:
		res.Verdict = VerdictError
		res.Detail = fmt.Sprintf("неизвестный транспорт %q", l.Kind)
		return res
	}
}

// probeLegs - перебор ног сервиса (эмпирика Фазы E: сервер «жив», но на
// отравленной сети отдаёт яд, а диал мёртвой ноги не должен ронять уровень).
// Бюджет на ногу = timeout/len(ног) (одинокой ноге - весь timeout, прочим
// минимум 800мс). Первый чистый ответ останавливает перебор; из нечистых
// сохраняется лучший (spoof/noanswer - транспорт ответил - ценнее error)
// с ногой, которая его дала; все ноги в ошибке - error со списком.
func probeLegs(l Level, timeout time.Duration) (leg, verdict string, canonical, total int, detail string) {
	legs := l.IPs
	if len(legs) == 0 {
		legs = []string{""} // пустая нога = адрес как задано в Server
	}
	budget := timeout / time.Duration(len(legs))
	switch {
	case len(legs) == 1:
		budget = timeout
	case budget < 800*time.Millisecond:
		budget = 800 * time.Millisecond
	}
	var errs []string
	bestRank := 0
	for _, ip := range legs {
		ips, err := probeLeg(l, ip, budget)
		if err == nil {
			v, c, t := verdictOf(ips)
			if v == VerdictClean {
				return ip, v, c, t, ""
			}
			if r := legRank(v); r > bestRank {
				leg, verdict, canonical, total, bestRank = ip, v, c, t, r
			}
			continue
		}
		errs = append(errs, fmt.Sprintf("%s: %v", legLabel(l, ip), err))
	}
	if bestRank > 0 {
		return leg, verdict, canonical, total, ""
	}
	if len(errs) > 0 {
		detail = strings.Join(errs, "; ")
	} else {
		detail = "нет живых ног сервиса"
	}
	return "", VerdictError, 0, 0, detail
}

// legRank - «лучше» среди нечистых ответов: транспорт ответил
// (spoof/noanswer) ценнее ошибки транспорта.
func legRank(v string) int {
	if v == VerdictSpoof || v == VerdictNoAnswer {
		return 1
	}
	return 0
}

func legLabel(l Level, ip string) string {
	if ip == "" {
		return l.Server
	}
	return ip
}

// probeLeg - одна проба одной ноги: обмен канарейкой выбранным транспортом.
func probeLeg(l Level, ip string, budget time.Duration) ([]string, error) {
	switch l.Kind {
	case "udp":
		return exchangeUDP(legAddr(l.Server, ip, "53"), CanaryDomain, budget)
	case "dot":
		host, port := hostPort(l.Server, "853")
		sni := l.SNI
		if sni == "" {
			sni = host
		}
		return exchangeDoT(legAddr(l.Server, ip, port), sni, CanaryDomain, budget)
	case "doh":
		return exchangeDoH(l.Server, ip, CanaryDomain, budget)
	}
	return nil, fmt.Errorf("неизвестный транспорт %q", l.Kind)
}

// legAddr - адрес ноги: ip:port из ноги (если в ней указан свой порт),
// иначе ip + порт из Server (или defPort); пустая нога = Server как есть.
func legAddr(server, ip, defPort string) string {
	if ip == "" {
		return server
	}
	if _, _, err := net.SplitHostPort(ip); err == nil {
		return ip // нога с собственным портом
	}
	_, port := hostPort(server, defPort)
	return net.JoinHostPort(ip, port)
}

// hostPort - host:port из адреса уровня; без порта подставляется defPort.
func hostPort(server, defPort string) (host, port string) {
	if h, p, err := net.SplitHostPort(server); err == nil && p != "" {
		return h, p
	}
	return server, defPort
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

// testDotRoots - шов для тестов: фейковый DoT-сервер с самоподписанным
// сертификатом. В бою nil - системный пул корневых (проверка сертификата
// DoT строгая и намеренная: «доступность DoT проверять реальным
// TLS-хендшейком», 63-док).
var testDotRoots func() *x509.CertPool

func exchangeUDP(server, domain string, timeout time.Duration) ([]string, error) {
	id := randomID()
	q := buildQuery(domain, id)
	// fwmark «Magi»: перехват MT_DNSOR пропускает помеченные пакеты -
	// UDP-проба меряет реальный сервер, а не цепочку MT (DoT/DoH не
	// метим: их порты перехват :53 не задевает, а вмешиваться в
	// групповое маркирование туннеля не хотим)
	d := net.Dialer{Timeout: timeout, Control: dialControl}
	conn, err := d.Dial("udp", server)
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
	if testDotRoots != nil {
		conf.RootCAs = testDotRoots()
	}
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
// Нога пуста - диал как обычно (по имени из URL); ip задан - диал пинится
// к ноге сервиса, а TLS-имя/сертификат проверяются по хосту URL, поэтому
// bootstrap-резолв не нужен и подмене не подвержен (Фаза E).
func exchangeDoH(urlStr, ip, domain string, timeout time.Duration) ([]string, error) {
	id := randomID()
	q := buildQuery(domain, id)
	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		// #nosec G402 - зонд публичных DoH, имя хоста задаёт уровень
		TLSClientConfig:  &tls.Config{ServerName: urlHost(urlStr)},
		ForceAttemptHTTP2: true,
	}
	if ip != "" {
		u, err := url.Parse(urlStr)
		if err != nil {
			return nil, fmt.Errorf("doh %s: %w", urlStr, err)
		}
		port := u.Port()
		if port == "" {
			port = "443"
		}
		dialAddr := legAddr(u.Host, ip, port)
		tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, dialAddr)
		}
	}
	cl := &http.Client{Transport: tr}
	defer cl.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, urlStr, bytes.NewReader(q))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/dns-message")
	// RFC 8484: без Accept dns.google отвечает FORMERR на валидный запрос
	// (живой кейс 0.42: форк-каскад с Accept чист, зонд без него - noanswer)
	req.Header.Set("Accept", "application/dns-message")
	resp, err := cl.Do(req)
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

// urlHost - имя хоста из URL без порта (для TLS ServerName).
func urlHost(urlStr string) string {
	if u, err := url.Parse(urlStr); err == nil {
		return u.Hostname()
	}
	return ""
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
