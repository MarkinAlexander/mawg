package cascade

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"

	"mawg/internal/magitrickle"
	"mawg/internal/store"
)

// viaEndpointIPs - фактические адреса туннеля via из конфигов пула
// (домены резолвятся с коротким таймаутом: SyncHooks ходит каждые 30с,
// мёртвый DNS не должен тормозить цикл).
func (m *Manager) viaEndpointIPs(via string) []string {
	kind, name, err := parseSource(via)
	if err != nil || kind != "pool" {
		return nil
	}
	p, ok := m.Store.Pool(name)
	if !ok {
		return nil
	}
	hosts, domains := m.poolEndpoints(p)
	ips := append([]string(nil), hosts...)
	for _, d := range domains {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		ips = append(ips, m.resolve(ctx, d)...)
		cancel()
	}
	return ips
}

// FlushConns чистит conntrack: залипший connmark тащит поток в мёртвый путь.
func (m *Manager) FlushConns(hosts []string) {
	if _, err := exec.LookPath("conntrack"); err != nil {
		return
	}
	for _, h := range hosts {
		if net.ParseIP(h) == nil {
			continue
		}
		cmd := exec.Command("conntrack", "-D", "-p", "udp", "-d", h)
		cmd.Run()
	}
	m.log("cascade", "conntrack", "сброшены соединения каскадируемых эндпоинтов")
}

func parseSource(ref string) (kind, name string, err error) {
	parts := strings.SplitN(ref, ":", 2)
	if len(parts) != 2 || parts[1] == "" || (parts[0] != "pool" && parts[0] != "iface") {
		return "", "", fmt.Errorf("неверный адрес источника %q", ref)
	}
	return parts[0], parts[1], nil
}

func HostOf(endpoint string) string {
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		return strings.TrimSpace(endpoint)
	}
	return host
}

func normalizeHost(h string) string {
	h = strings.TrimSpace(h)
	if h == "" || net.ParseIP(h) != nil {
		return h
	}
	if _, _, err := net.ParseCIDR(h); err == nil {
		return h
	}
	if host, port, err := net.SplitHostPort(h); err == nil && port != "" {
		return strings.ToLower(host)
	}
	return strings.ToLower(h)
}

// BuildRules: домены дублируются A-записями - трафик туннеля идёт мимо DNS.
func BuildRules(hosts []string, resolutions map[string][]string) []magitrickle.Rule {
	seen := map[string]bool{}
	var rules []magitrickle.Rule
	add := func(t, v string) {
		key := t + " " + v
		if seen[key] {
			return
		}
		seen[key] = true
		rules = append(rules, magitrickle.Rule{Type: t, Rule: v, Enable: true})
	}
	for _, raw := range hosts {
		h := normalizeHost(raw)
		if h == "" {
			continue
		}
		if net.ParseIP(h) != nil {
			add("subnet", h+"/32")
			continue
		}
		if _, _, err := net.ParseCIDR(h); err == nil {
			add("subnet", h)
			continue
		}
		add("domain", h)
		for _, ip := range resolutions[h] {
			add("subnet", ip+"/32")
		}
	}
	return rules
}

func (m *Manager) resolve(ctx context.Context, host string) []string {
	rctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupHost(rctx, host)
	if err != nil {
		return nil
	}
	var ips []string
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil && ip.To4() != nil {
			ips = append(ips, ip.String())
		}
	}
	return ips
}

func (m *Manager) poolEndpoints(pool store.Pool) (hosts, domains []string) {
	seen := map[string]bool{}
	for _, c := range pool.Configs {
		if !c.Enabled {
			continue // выключенный конфиг не должен держать правила каскада
		}
		h := HostOf(c.Endpoint)
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		if net.ParseIP(h) == nil {
			domains = append(domains, strings.ToLower(h))
		} else {
			hosts = append(hosts, h)
		}
	}
	return hosts, domains
}

func viaDevice(m *Manager, via string) (device string, title string, err error) {
	kind, name, err := parseSource(via)
	if err != nil {
		return "", "", err
	}
	if kind == "iface" {
		return name, name, nil
	}
	p, ok := m.Store.Pool(name)
	if !ok {
		return "", "", fmt.Errorf("пул %q не найден", name)
	}
	return p.DeviceName(), name, nil
}

// IfaceEndpoints - адреса пиров роутерного WG-интерфейса для префилла
// диалога каскадов; source = "iface:<device>".
func (m *Manager) IfaceEndpoints(source string) ([]string, error) {
	kind, name, err := parseSource(source)
	if err != nil {
		return nil, err
	}
	if kind != "iface" {
		return nil, fmt.Errorf("автоподстановка - только для интерфейса-источника")
	}
	if m.EP == nil {
		return nil, fmt.Errorf("платформа не умеет определять эндпоинты интерфейсов - перечислите вручную")
	}
	return m.EP.IfaceEndpoints(name)
}
