package web

// Петлезащита «Резервных серверов» (openwrt, dnsmasq.inMT): upstream
// 127.0.0.1:53 или собственный IP:53 - это dnsmasq, который сам кормится
// от MT:3553 - петля dnsmasq->MT->dnsmasq, DNS роутера встанет. На
// Keenetic 127.0.0.1:53 - системный резолвер ndm (легален; прод владельца
// живёт именно на нём) - правило платформенное. Не запрет, а честное
// предупреждение с подтверждением (слово владельца 2026-10-18): PUT
// отвечает 409 со списком рискованных адресов, клиент показывает
// предупреждение и повторяет запрос с confirm.

import (
	"net"
	"strings"

	"mawg/internal/dnsmgr"
	"mawg/internal/store"
)

// loopRiskUpstreams - подмножество addrs, замыкающее петлю при текущем
// состоянии DNS-стека (правило не-Кинетик: на Keenetic dnsmasq нулевой,
// а 127.0.0.1:53 - системный резолвер; лишние uci-вызовы на проде тоже
// не нужны).
func (s *Server) loopRiskUpstreams(addrs []string) []string {
	if s.backend.Name() == store.PlatformKeenetic {
		return nil
	}
	run, ok := s.dnsRunner()
	if !ok {
		return nil
	}
	st, err := dnsmgr.Collect(run, s.backend.MTConfigPath())
	if err != nil || !st.Dnsmasq.InMT {
		return nil
	}
	var risky []string
	for _, a := range addrs {
		if dnsmasqLoopHost(a, st.Self) {
			risky = append(risky, a)
		}
	}
	return risky
}

// dnsmasqLoopHost - адрес указывает на dnsmasq (plain udp:53 на loopback
// или собственном IP): шифрованные транспорта (tls://, https://) и не-53
// порты петли не дают.
func dnsmasqLoopHost(addr string, ownIPs []string) bool {
	t := strings.TrimSpace(addr)
	if t == "" || strings.HasPrefix(t, "tls://") || strings.HasPrefix(t, "https://") {
		return false
	}
	host, port := t, "53"
	if h, p, err := net.SplitHostPort(t); err == nil {
		host, port = h, p
	}
	if port != "53" {
		return false
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		return false // имя хоста: петля только по адресам
	}
	if ip.IsLoopback() {
		return true
	}
	for _, o := range ownIPs {
		if oip := net.ParseIP(o); oip != nil && ip.Equal(oip) {
			return true
		}
	}
	return false
}
