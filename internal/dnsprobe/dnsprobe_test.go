package dnsprobe

import (
	"encoding/binary"
	"net"
	"net/http"
	"net/http/httptest"
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
	fixed := map[string]bool{
		"dnsmasq": true, "dns-proxy MT": true,
		"DoH Cloudflare": true, "DoH Google": true, "DoT Google": true,
	}
	seen := 0
	for _, l := range levels {
		if fixed[l.Level] {
			seen++
			continue
		}
		// upstream ISP опционален: есть только там, где есть resolv.conf
		if l.Level == "upstream ISP" && l.Server == "" {
			t.Fatalf("upstream ISP без адреса")
		}
	}
	if seen != len(fixed) {
		t.Fatalf("Defaults: %d из %d обязательных уровней (%+v)", seen, len(fixed), levels)
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
