package store

import (
	"net"
	"net/url"
	"strings"
)

func ValidProbeHost(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil || ip.To4() == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	return ip.String() != "255.255.255.255"
}

func IsHTTPProbe(target string) bool {
	return strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://")
}

func ValidProbeTarget(target string) bool {
	if IsHTTPProbe(target) {
		u, err := url.Parse(target)
		return err == nil && u.Host != ""
	}
	return ValidProbeHost(target)
}
