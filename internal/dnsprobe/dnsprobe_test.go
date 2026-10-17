package dnsprobe

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

// ответ с одной A-записью (имя-указатель, ответ на buildQuery)
func cannedResponse(query []byte, ips ...string) []byte {
	resp := make([]byte, len(query))
	copy(resp, query)
	// ANCOUNT = len(ips)
	binary.BigEndian.PutUint16(resp[6:8], uint16(len(ips)))
	for _, ip := range ips {
		resp = append(resp, 0xC0, 0x0C)           // имя - указатель на offset 12 (вопрос)
		resp = binary.BigEndian.AppendUint16(resp, 1)  // A
		resp = binary.BigEndian.AppendUint16(resp, 1)  // IN
		resp = binary.BigEndian.AppendUint32(resp, 60) // TTL
		resp = binary.BigEndian.AppendUint16(resp, 4)  // RDLENGTH
		resp = append(resp, net.ParseIP(ip).To4()...)
	}
	return resp
}

func TestBuildQueryAndParseA(t *testing.T) {
	q := buildQuery(CanaryDomain, 0x1234)
	if len(q) != 12+4+4+4+len(CanaryDomain)+1 {
		// 12 header + qname (len+labels+0) + 4 (qtype+qclass) - считаем фактически
		t.Logf("query len = %d", len(q))
	}
	if got := binary.BigEndian.Uint16(q[2:4]); got != 0x0100 {
		t.Fatalf("flags = %#x, want RD", got)
	}
	// вопрос кончается нулём, дальше A/IN
	if q[len(q)-4] != 0 || binary.BigEndian.Uint16(q[len(q)-4:len(q)-2]) != 1 {
		t.Fatalf("qtype/qclass сломаны")
	}
	resp := cannedResponse(q, "204.8.99.144", "95.216.163.36", "204.8.99.144")
	ips := parseA(resp)
	if len(ips) != 2 || ips[0] != "204.8.99.144" || ips[1] != "95.216.163.36" {
		t.Fatalf("parseA = %v, want 2 уникальных", ips)
	}
}

func TestParseAОбрезан(t *testing.T) {
	if ips := parseA([]byte{0, 1, 2}); len(ips) != 0 {
		t.Fatalf("обрезанный ответ дал %v", ips)
	}
}

func TestVerdictOf(t *testing.T) {
	cases := []struct {
		ips   []string
		want  string
		canon int
		total int
	}{
		{[]string{"204.8.99.144", "1.2.3.4"}, VerdictClean, 1, 2},
		{[]string{"6.6.6.6", "7.7.7.7"}, VerdictSpoof, 0, 2},
		{nil, VerdictNoAnswer, 0, 0},
	}
	for _, c := range cases {
		v, canon, total := verdictOf(c.ips)
		if v != c.want || canon != c.canon || total != c.total {
			t.Fatalf("verdictOf(%v) = %s/%d/%d, want %s/%d/%d", c.ips, v, canon, total, c.want, c.canon, c.total)
		}
	}
}

// фейковый UDP-DNS: отвечает canned-ответом на любой запрос
func fakeUDPDNS(t *testing.T, ips ...string) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 4096)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			resp := cannedResponse(buf[:n], ips...)
			_, _ = pc.WriteTo(resp, addr)
		}
	}()
	return pc.LocalAddr().(*net.UDPAddr).String()
}

func TestCheckUDPVerdicts(t *testing.T) {
	clean := Check(Level{Level: "t", Kind: "udp", Server: fakeUDPDNS(t, "204.8.99.144", "1.2.3.4")}, 2*time.Second)
	if clean.Verdict != VerdictClean || clean.Canonical != 1 || clean.Total != 2 {
		t.Fatalf("clean: %+v", clean)
	}
	spoof := Check(Level{Level: "t", Kind: "udp", Server: fakeUDPDNS(t, "6.6.6.6")}, 2*time.Second)
	if spoof.Verdict != VerdictSpoof {
		t.Fatalf("spoof: %+v", spoof)
	}
	// «сервер» молчит (ни на кого не слушает) - error
	dead := Check(Level{Level: "t", Kind: "udp", Server: "127.0.0.1:1"}, 300*time.Millisecond)
	if dead.Verdict != VerdictError || dead.Detail == "" {
		t.Fatalf("dead: %+v", dead)
	}
}

func TestCheckDoH(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/dns-message" {
			http.Error(w, "bad type", http.StatusBadRequest)
			return
		}
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		_, _ = w.Write(cannedResponse(buf[:n], "204.8.99.146"))
	}))
	defer srv.Close()
	res := Check(Level{Level: "t", Kind: "doh", Server: srv.URL}, 2*time.Second)
	if res.Verdict != VerdictClean || res.Canonical != 1 {
		t.Fatalf("doh: %+v", res)
	}
}

func TestDefaultsContainsMTAndUpstream(t *testing.T) {
	levels := Defaults()
	// первые три - человекочитаемые (67-док), DoH/DoT/TCP - как были
	// (слово владельца: расшифровка терминов - в подсказках [?] UI)
	fixed := map[string]bool{
		"Служба DNS роутера": true, "DNS через MT": true,
		"DoH Cloudflare": true, "DoH Google": true, "DoT Google": true,
		"интернет TCP 1.1.1.1:443": true, "интернет TCP 77.88.8.8:443": true,
	}
	seen := 0
	for _, l := range levels {
		if fixed[l.Level] {
			seen++
			continue
		}
		// DNS провайдера опционален: есть только там, где есть resolv.conf
		if l.Level == "DNS провайдера" && l.Server == "" {
			t.Fatalf("DNS провайдера без адреса")
		}
	}
	if seen != len(fixed) {
		t.Fatalf("Defaults: %d из %d обязательных уровней (%+v)", seen, len(fixed), levels)
	}
}

func TestCheckTCPAlive(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	res := Check(Level{Level: "t", Kind: "tcp", Server: ln.Addr().String()}, time.Second)
	if res.Verdict != VerdictClean {
		t.Fatalf("tcp жив: %+v", res)
	}
	res = Check(Level{Level: "t", Kind: "tcp", Server: "127.0.0.1:1"}, 300*time.Millisecond)
	if res.Verdict != VerdictError || res.Detail == "" {
		t.Fatalf("tcp мёртв: %+v", res)
	}
}

func TestRunKeepsOrder(t *testing.T) {
	a := fakeUDPDNS(t, "204.8.99.144")
	b := fakeUDPDNS(t, "6.6.6.6")
	res := Run([]Level{{Level: "a", Kind: "udp", Server: a}, {Level: "b", Kind: "udp", Server: b}}, 2*time.Second)
	if len(res) != 2 || res[0].Level != "a" || res[1].Level != "b" {
		t.Fatalf("Run порядок сломан: %+v", res)
	}
	if res[0].Verdict != VerdictClean || res[1].Verdict != VerdictSpoof {
		t.Fatalf("Run вердикты: %+v", res)
	}
}

// перебор ног (Фаза E): чистая нога находится за мёртвой/отравленной,
// нечистые дают лучший вердикт со своей ногой, все мертвы - error со списком
func TestCheckLegsAggregation(t *testing.T) {
	spoof := fakeUDPDNS(t, "6.6.6.6")
	clean := fakeUDPDNS(t, "204.8.99.144")
	dead := "127.0.0.1:1"
	res := Check(Level{Level: "t", Kind: "udp", Server: dead, IPs: []string{spoof, clean}}, 2*time.Second)
	if res.Verdict != VerdictClean || res.IP != clean {
		t.Fatalf("spoof->clean: %+v", res)
	}
	res = Check(Level{Level: "t", Kind: "udp", Server: dead, IPs: []string{spoof, dead}}, 2*time.Second)
	if res.Verdict != VerdictSpoof || res.IP != spoof {
		t.Fatalf("spoof+мёртвая: %+v", res)
	}
	res = Check(Level{Level: "t", Kind: "udp", Server: dead, IPs: []string{dead, "127.0.0.1:2"}}, time.Second)
	if res.Verdict != VerdictError || !strings.Contains(res.Detail, "127.0.0.1:1") || !strings.Contains(res.Detail, "127.0.0.1:2") {
		t.Fatalf("все мертвы: %+v", res)
	}
}

// двуногий DoH: мёртвая нога не роняет уровень - перебор доходит до живой
func TestCheckLegsDoH(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		_, _ = w.Write(cannedResponse(buf[:n], "204.8.99.146"))
	}))
	defer srv.Close()
	live := strings.TrimPrefix(srv.URL, "http://") // 127.0.0.1:порт
	res := Check(Level{Level: "t", Kind: "doh", Server: srv.URL, IPs: []string{"127.0.0.2:9", live}}, 2*time.Second)
	if res.Verdict != VerdictClean || res.IP != live {
		t.Fatalf("doh ноги: %+v", res)
	}
}

// фейковый DoT-сервер: самоподписанный сертификат с IP-SAN 127.0.0.1,
// framed-протокол RFC 7858; возвращает адрес и сертификат (для пула зонда)
func fakeDoT(t *testing.T, ips ...string) (string, *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "dns.google"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"dns.google"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				var lenBuf [2]byte
				if _, err := io.ReadFull(c, lenBuf[:]); err != nil {
					return
				}
				msg := make([]byte, binary.BigEndian.Uint16(lenBuf[:]))
				if _, err := io.ReadFull(c, msg); err != nil {
					return
				}
				resp := cannedResponse(msg, ips...)
				out := make([]byte, 2+len(resp))
				binary.BigEndian.PutUint16(out, uint16(len(resp)))
				copy(out[2:], resp)
				_, _ = c.Write(out)
			}(c)
		}
	}()
	return ln.Addr().String(), leaf
}

// двуногий DoT: то же, что DoH - живая нога находится за мёртвой
func TestCheckLegsDoT(t *testing.T) {
	live, leaf := fakeDoT(t, "204.8.99.144")
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	testDotRoots = func() *x509.CertPool { return pool }
	t.Cleanup(func() { testDotRoots = nil })
	res := Check(Level{Level: "t", Kind: "dot", Server: "127.0.0.1:1", IPs: []string{"127.0.0.2:9", live}}, 3*time.Second)
	if res.Verdict != VerdictClean || res.IP != live {
		t.Fatalf("dot ноги: %+v", res)
	}
}

func TestDefaultsLegs(t *testing.T) {
	want := map[string][]string{
		"DoH Cloudflare": {"1.1.1.1", "1.0.0.1"},
		"DoH Google":     {"8.8.8.8", "8.8.4.4"},
		"DoT Google":     {"8.8.8.8", "8.8.4.4"},
	}
	got := map[string][]string{}
	for _, l := range Defaults() {
		if len(l.IPs) > 0 {
			got[l.Level] = l.IPs
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ноги в Defaults = %v, want %v", got, want)
	}
}
