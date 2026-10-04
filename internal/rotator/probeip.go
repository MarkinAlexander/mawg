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
	if ip := net.ParseIP(host); ip != nil {
		if blockedIP(ip) {
			return "", fmt.Errorf("адрес %s заблокирован для проб", host)
		}
		return net.JoinHostPort(ip.String(), port), nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return "", err
	}
	for _, ip := range ips {
		if blockedIP(ip) {
			continue
		}
		return net.JoinHostPort(ip.String(), port), nil
	}
	return "", fmt.Errorf("%s резолвится только в заблокированные адреса", host)
}
