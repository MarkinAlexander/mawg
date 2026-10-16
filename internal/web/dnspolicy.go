package web

// Политика каскада DNS (46-док §3, 66-док): уровни L1-L5 компоновщиком
// mawg превращаются в упорядоченный список апстримов dns-proxy MT -
// один PUT в форк rev8 (гистерезис K/M/restoreGap живёт в форке, панель
// порядок не переключает сама). L2 «через туннель» - штатный пресет
// dns-tunnel на пуле: публичные резолверы маршрутизируются в туннель
// group-правилами, список апстримов при этом не меняется.

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"mawg/internal/dnsprobe"
	"mawg/internal/magitrickle"
	"mawg/internal/store"
)

// normalizeUpstream - "8.8.8.8" / "[::1]:5353" -> "host:port" (порт по
// умолчанию 53).
func normalizeUpstream(raw string) (string, error) {
	t := strings.TrimSpace(raw)
	if t == "" {
		return "", fmt.Errorf("пустой адрес")
	}
	host, port := t, "53"
	if h, p, err := net.SplitHostPort(t); err == nil {
		host, port = h, p
	}
	host = strings.Trim(host, "[]")
	if host == "" {
		return "", fmt.Errorf("нет адреса в %q", raw)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("порт в %q: %q", raw, port)
	}
	return net.JoinHostPort(host, port), nil
}

// composeCascadePolicy - порядок апстримов из политики: основные →
// запасные → Яндекс → провайдер (срез resolv.conf.auto на момент
// композиции; DHCP может сменить его позже - честно в подсказке UI).
// Дедуп по host:port.
func composeCascadePolicy(p store.DnsCascadePolicy) ([]string, error) {
	if !p.Enabled {
		return nil, nil
	}
	var out []string
	seen := map[string]struct{}{}
	add := func(list []string, what string) error {
		for _, raw := range list {
			a, err := normalizeUpstream(raw)
			if err != nil {
				return fmt.Errorf("%s: %w", what, err)
			}
			if _, dup := seen[a]; dup {
				continue
			}
			seen[a] = struct{}{}
			out = append(out, a)
		}
		return nil
	}
	if err := add(p.Primary, "основные"); err != nil {
		return nil, err
	}
	if err := add(p.Fallback, "запасные"); err != nil {
		return nil, err
	}
	if p.Yandex {
		_ = add([]string{"77.88.8.8"}, "Яндекс")
	}
	if p.Provider {
		if up := dnsprobe.UpstreamFromResolv(); up != "" {
			_ = add([]string{up}, "провайдер")
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("политика включена, но ни один уровень не задан")
	}
	return out, nil
}

// dnsTunnelGroup - живая группа dns-tunnel (id/интерфейс), nil если нет.
func (s *Server) dnsTunnelGroup(ctx context.Context) map[string]string {
	groups, err := s.mtClient().GroupsWithRules(ctx)
	if err != nil {
		return nil
	}
	for _, g := range groups {
		if g.Name == "dns-tunnel" {
			return map[string]string{"id": g.ID, "interface": g.Interface}
		}
	}
	return nil
}

// ensureDnsTunnelGroup - группа dns-tunnel на пуле: создаётся по пресету,
// при смене интерфейса обновляется (группа целиком - GOTCHAS про частичный
// PUT), при совпадении - no-op. Идемпотентно для повторного применения.
func (s *Server) ensureDnsTunnelGroup(ctx context.Context, iface string) (string, error) {
	preset, ok := magitrickle.PresetByID("dns-tunnel")
	if !ok {
		return "", fmt.Errorf("пресет dns-tunnel не найден")
	}
	client := s.mtClient()
	if err := client.EnsureHealthy(ctx); err != nil {
		return "", err
	}
	groups, err := client.GroupsWithRules(ctx)
	if err != nil {
		return "", err
	}
	for _, g := range groups {
		if g.Name != "dns-tunnel" {
			continue
		}
		if g.Interface == iface {
			return g.ID, nil
		}
		g.Interface = iface
		g.Rules = preset.Rules // список резолверов встроен в код - актуальнее дискового
		if err := client.UpdateGroup(ctx, g, true); err != nil {
			return "", fmt.Errorf("группа dns-tunnel не обновлена: %w", err)
		}
		return g.ID, nil
	}
	if err := client.AddGroup(ctx, magitrickle.Group{Name: "dns-tunnel", Interface: iface, Enable: true, Rules: preset.Rules}); err != nil {
		return "", fmt.Errorf("группа dns-tunnel не создана: %w", err)
	}
	if gs, err := client.GroupsWithRules(ctx); err == nil {
		for _, g := range gs {
			if g.Name == "dns-tunnel" && g.Interface == iface {
				return g.ID, nil
			}
		}
	}
	return "", nil
}

// getDnsPolicy - GET /api/v1/dns/policy: сохранённая политика, скомпонованный
// порядок, живой порядок форка, статус группы dns-tunnel.
func (s *Server) getDnsPolicy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	capable := s.mtDnsUpstreamsCapable(ctx)
	res := map[string]any{"capable": capable}
	p := s.store.DNSCascade()
	if p == nil {
		p = &store.DnsCascadePolicy{}
	}
	res["policy"] = p
	if composed, err := composeCascadePolicy(*p); err == nil {
		res["composed"] = composed
	}
	if capable {
		if st, err := s.mtClient().DnsUpstreams(ctx); err == nil {
			active := make([]string, 0, len(st.Upstreams))
			for _, u := range st.Upstreams {
				active = append(active, u.Address)
			}
			res["active"] = active
		}
		if tg := s.dnsTunnelGroup(ctx); tg != nil {
			res["tunnelGroup"] = tg
		}
	}
	writeJSON(w, http.StatusOK, res)
}

// putDnsPolicy - PUT /api/v1/dns/policy {policy}: сохранить и применить -
// группа dns-tunnel на выбранном пуле, затем один PUT порядка в форк.
// Выключенная политика только сохраняется (текущие апстримы не трогаем).
func (s *Server) putDnsPolicy(w http.ResponseWriter, r *http.Request) {
	var p store.DnsCascadePolicy
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&p); err != nil {
		writeErr(w, fmt.Errorf("кривой запрос"))
		return
	}
	composed, err := composeCascadePolicy(p)
	if err != nil {
		writeErr(w, err)
		return
	}
	if p.Enabled && !s.mtDnsUpstreamsCapable(r.Context()) {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "применение политики требует MagiTrickle 0.8.2-rev8+"})
		return
	}
	if p.Enabled && p.Tunnel != "" {
		if _, err := s.ensureDnsTunnelGroup(r.Context(), p.Tunnel); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
	}
	if p.Enabled {
		if err := s.mtClient().SetDnsUpstreams(r.Context(), composed); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
	}
	if err := s.store.SetDNSCascade(&p); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if p.Enabled {
		s.store.LogEvent("dns", "policy", "политика каскада применена (порядок: "+strings.Join(composed, " → ")+")")
	} else {
		s.store.LogEvent("dns", "policy", "политика каскада выключена (апстримы не тронуты)")
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "composed": composed})
}

// postDnsPolicyProbe - POST /api/v1/dns/policy/probe: канарейка
// (deb.torproject.org) по каждому уровню политики; трафик самой mawg
// маркируется группами MT (rev7+), так что туннельные уровни зондируются
// по-настоящему - через туннель.
func (s *Server) postDnsPolicyProbe(w http.ResponseWriter, r *http.Request) {
	p := s.store.DNSCascade()
	if p == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "политика не задана"})
		return
	}
	var levels []dnsprobe.Level
	label := func(lvl, addr string) string {
		return lvl + ": " + addr
	}
	for _, raw := range p.Primary {
		if a, err := normalizeUpstream(raw); err == nil {
			name := "L1 напрямую"
			if p.Tunnel != "" {
				name = "L2 туннель " + p.Tunnel
			}
			levels = append(levels, dnsprobe.Level{Level: label(name, a), Kind: "udp", Server: a})
		}
	}
	for _, raw := range p.Fallback {
		if a, err := normalizeUpstream(raw); err == nil {
			levels = append(levels, dnsprobe.Level{Level: label("L3 запасной", a), Kind: "udp", Server: a})
		}
	}
	if p.Yandex {
		levels = append(levels, dnsprobe.Level{Level: "L4 Яндекс", Kind: "udp", Server: "77.88.8.8:53"})
	}
	if p.Provider {
		if up := dnsprobe.UpstreamFromResolv(); up != "" {
			if a, err := normalizeUpstream(up); err == nil {
				levels = append(levels, dnsprobe.Level{Level: label("L5 провайдер", a), Kind: "udp", Server: a})
			}
		}
	}
	if len(levels) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "в политике нет ни одного уровня"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": dnsprobe.Run(levels, 3*time.Second)})
}
