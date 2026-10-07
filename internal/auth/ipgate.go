package auth

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type IPGate struct {
	mu     sync.RWMutex
	rules  []*net.IPNet
	single []net.IP
	raw    []string

	ownMu      sync.Mutex
	ownIPs     []net.IP
	ownRefresh time.Time
}

func NewIPGate(allowed []string) *IPGate {
	g := &IPGate{}
	g.Set(allowed)
	return g
}

func (g *IPGate) Set(allowed []string) {
	rules := []*net.IPNet{}
	single := []net.IP{}
	for _, raw := range allowed {
		c := strings.TrimSpace(raw)
		if c == "" {
			continue
		}
		if ip := net.ParseIP(c); ip != nil {
			single = append(single, ip)
			continue
		}
		if _, n, err := net.ParseCIDR(c); err == nil {
			rules = append(rules, n)
		}
	}
	g.mu.Lock()
	g.rules, g.single, g.raw = rules, single, allowed
	g.mu.Unlock()
}

func (g *IPGate) Empty() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.rules) == 0 && len(g.single) == 0
}

func (g *IPGate) Raw() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return append([]string(nil), g.raw...)
}

func (g *IPGate) allowed(ip net.IP) bool {
	for _, s := range g.single {
		if s.Equal(ip) {
			return true
		}
	}
	for _, n := range g.rules {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func (g *IPGate) Allowed(ip net.IP) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.allowed(ip)
}

// OwnAddress - источник является адресом самого хоста: роутер открывает
// свою панель через любой из своих IP, не только loopback.
func (g *IPGate) OwnAddress(ip net.IP) bool {
	g.ownMu.Lock()
	defer g.ownMu.Unlock()
	if time.Since(g.ownRefresh) > time.Minute {
		g.ownRefresh = time.Now()
		g.ownIPs = nil
		if addrs, err := net.InterfaceAddrs(); err == nil {
			for _, a := range addrs {
				if ipn, ok := a.(*net.IPNet); ok {
					g.ownIPs = append(g.ownIPs, ipn.IP)
				}
			}
		}
	}
	for _, own := range g.ownIPs {
		if own.Equal(ip) {
			return true
		}
	}
	return false
}

// Middleware пускает самого хоста всегда: CLI и локальные проверки не должны ломаться.
func (g *IPGate) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g.Empty() {
			next.ServeHTTP(w, r)
			return
		}
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		ip := net.ParseIP(host)
		if ip == nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if ip.IsLoopback() || g.OwnAddress(ip) || g.allowed(ip) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, "доступ к панели запрещён для этого адреса")
	})
}
