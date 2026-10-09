package rotator

import (
	"fmt"
	"net"
)

func blockedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() ||
		ip.Equal(net.IPv4bcast) || isCGNAT(ip)
}

func isCGNAT(ip net.IP) bool {
	ip = ip.To4()
	if ip == nil {
		return false
	}
	return ip[0] == 100 && ip[1] >= 64 && ip[1] < 128
}

// safeAddr: хост пробы резолвится и дозвон идёт только по проверенному
// публичному IP - иначе root-процесс можно использовать как зонд в LAN.
func safeAddr(host, port string) (string, error) {
	addrs, err := safeAddrs(host, port)
	if err != nil {
		return "", err
	}
	return addrs[0], nil
}

// safeAddrs - то же, но все подходящие адреса: IPv4 впереди, IPv6 после.
// Порядок важен: на интерфейсах без v6 дозвон по первому попавшемуся
// AAAA всегда падает ("нет ответа" при рабочем ping), а curl выживает
// за счёт перебора семей.
func safeAddrs(host, port string) ([]string, error) {
	var out []string
	var v6 []string
	add := func(ip net.IP) bool {
		if blockedIP(ip) {
			return false
		}
		if v4 := ip.To4(); v4 != nil {
			out = append(out, net.JoinHostPort(v4.String(), port))
		} else {
			v6 = append(v6, net.JoinHostPort(ip.String(), port))
		}
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		if !add(ip) {
			return nil, fmt.Errorf("адрес %s заблокирован для проб", host)
		}
		return append(out, v6...), nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return nil, err
	}
	for _, ip := range ips {
		add(ip)
	}
	if len(out)+len(v6) == 0 {
		return nil, fmt.Errorf("%s резолвится только в заблокированные адреса", host)
	}
	return append(out, v6...), nil
}
