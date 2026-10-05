package auth

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
)

type IPGate struct {
	mu    sync.RWMutex
	rules []*net.IPNet
	single []net.IP
	raw   []string
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

// Middleware пускает loopback всегда: CLI и локальные проверки не должны ломаться.
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
		if ip.IsLoopback() || g.allowed(ip) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, "доступ к панели запрещён для этого адреса")
	})
}
