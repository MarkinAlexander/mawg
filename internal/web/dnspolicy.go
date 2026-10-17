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
	"net/url"
	"strconv"
	"strings"
	"time"

	"mawg/internal/dnsprobe"
	"mawg/internal/magitrickle"
	"mawg/internal/store"
)

// normalizeUpstream - "8.8.8.8" / "[::1]:5353" -> "host:port" (порт по
// умолчанию 53); "tls://host[:853]" -> канонический DoT-адрес,
// "https://host[:443][/dns-query]" -> канонический DoH-URL (форк rev11+).
func normalizeUpstream(raw string) (string, error) {
	t := strings.TrimSpace(raw)
	if t == "" {
		return "", fmt.Errorf("пустой адрес")
	}
	if strings.Contains(t, "://") {
		return normalizeEncryptedUpstream(t)
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

// normalizeEncryptedUpstream - DoT/DoH-адреса каскада (ревизия форка 11+):
// порты и путь DoH достраиваются умолчаниями, канонический вид - как в
// конфиге форка.
func normalizeEncryptedUpstream(t string) (string, error) {
	switch {
	case strings.HasPrefix(t, "tls://"):
		rest := strings.TrimPrefix(t, "tls://")
		if strings.ContainsAny(rest, "/?#") {
			return "", fmt.Errorf("путь не имеет смысла для DoT: %q", t)
		}
		host, port := rest, "853"
		if h, p, err := net.SplitHostPort(rest); err == nil {
			host, port = h, p
		}
		host = strings.Trim(host, "[]")
		if host == "" {
			return "", fmt.Errorf("нет адреса в %q", t)
		}
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("порт в %q: %q", t, port)
		}
		return "tls://" + net.JoinHostPort(host, port), nil
	case strings.HasPrefix(t, "https://"):
		u, err := url.Parse(t)
		if err != nil {
			return "", fmt.Errorf("%q: %v", t, err)
		}
		host := u.Hostname()
		if host == "" {
			return "", fmt.Errorf("нет адреса в %q", t)
		}
		port := u.Port()
		if port == "" {
			port = "443"
		}
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("порт в %q: %q", t, port)
		}
		path := u.EscapedPath()
		if path == "" || path == "/" {
			path = "/dns-query"
		}
		return (&url.URL{Scheme: "https", Host: net.JoinHostPort(host, port), Path: path}).String(), nil
	}
	return "", fmt.Errorf("неизвестная схема в %q (поддержаны tls:// и https://)", t)
}

// hasEncryptedUpstream - есть ли в списке DoT/DoH-адреса (нужен форк rev11+).
func hasEncryptedUpstream(addrs []string) bool {
	for _, a := range addrs {
		if strings.HasPrefix(a, "tls://") || strings.HasPrefix(a, "https://") {
			return true
		}
	}
	return false
}

// upstreamHostIPs - адреса (IP) апстримов политики для статики группы
// dns-tunnel: IP-литерал берётся напрямую, имя резолвится системным
// резолвером (обычно это текущий каскад - бутстрап замкнут, но работает:
// резолв обычный, шифруется только итоговый DNS-трафик). Неразрешимые
// пропускаются со списком для предупреждения.
func upstreamHostIPs(ctx context.Context, addrs []string) (ips []string, failed []string) {
	seen := map[string]struct{}{}
	resolveCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	for _, a := range addrs {
		var host string
		switch {
		case strings.HasPrefix(a, "tls://"):
			host = strings.TrimPrefix(a, "tls://")
		case strings.HasPrefix(a, "https://"):
			if u, err := url.Parse(a); err == nil {
				host = u.Hostname()
			}
		default:
			host = a
		}
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.Trim(host, "[]")
		if host == "" {
			continue
		}
		if ip := net.ParseIP(host); ip != nil {
			if _, dup := seen[host]; !dup {
				seen[host] = struct{}{}
				ips = append(ips, host)
			}
			continue
		}
		resolved, err := net.DefaultResolver.LookupIPAddr(resolveCtx, host)
		if err != nil || len(resolved) == 0 {
			failed = append(failed, host)
			continue
		}
		for _, ipa := range resolved {
			s := ipa.IP.String()
			if _, dup := seen[s]; !dup {
				seen[s] = struct{}{}
				ips = append(ips, s)
			}
			break // первого адреса достаточно (провайдеры - anycast)
		}
	}
	return ips, failed
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

// tunnelGroupRules - правила группы dns-tunnel: встроенный набор публичных
// резолверов + статика под адреса апстримов ПОЛИТИКИ (DoT/DoH-провайдеры
// тоже должны заворачиваться в туннель, когда выбран «через пул»).
func tunnelGroupRules(extraIPs []string) []magitrickle.Rule {
	preset, _ := magitrickle.PresetByID("dns-tunnel")
	rules := make([]magitrickle.Rule, 0, len(preset.Rules)+len(extraIPs))
	seen := map[string]struct{}{}
	for _, r := range preset.Rules {
		key := r.Type + "|" + r.Rule
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		rules = append(rules, r)
	}
	for _, ip := range extraIPs {
		typ, rule := "subnet", ip+"/32"
		if strings.Contains(ip, ":") {
			typ, rule = "subnet6", ip+"/128"
		}
		key := typ + "|" + rule
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		rules = append(rules, magitrickle.Rule{Type: typ, Rule: rule, Enable: true})
	}
	return rules
}

// ensureDnsTunnelGroup - группа dns-tunnel на пуле: создаётся по пресету,
// при смене интерфейса обновляется (группа целиком - GOTCHAS про частичный
// PUT), при совпадении - no-op. Идемпотентно для повторного применения.
func (s *Server) ensureDnsTunnelGroup(ctx context.Context, iface string, extraIPs []string) (string, error) {
	if _, ok := magitrickle.PresetByID("dns-tunnel"); !ok {
		return "", fmt.Errorf("пресет dns-tunnel не найден")
	}
	rules := tunnelGroupRules(extraIPs)
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
		if g.Interface == iface && rulesEqual(g.Rules, rules) {
			return g.ID, nil
		}
		g.Interface = iface
		g.Rules = rules // встроенный список + статика политики - актуальнее дискового
		if err := client.UpdateGroup(ctx, g, true); err != nil {
			return "", fmt.Errorf("группа dns-tunnel не обновлена: %w", err)
		}
		return g.ID, nil
	}
	if err := client.AddGroup(ctx, magitrickle.Group{Name: "dns-tunnel", Interface: iface, Enable: true, Rules: rules}); err != nil {
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

// rulesEqual - одинаков ли набор правил (порядок важен - это порядок
// матчинга; честное сравнение без переупорядочивания).
func rulesEqual(a, b []magitrickle.Rule) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Type != b[i].Type || a[i].Rule != b[i].Rule || a[i].Enable != b[i].Enable {
			return false
		}
	}
	return true
}

// getDnsPolicy - GET /api/v1/dns/policy: сохранённая политика, скомпонованный
// порядок, живой порядок форка, статус группы dns-tunnel.
func (s *Server) getDnsPolicy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	capable := s.mtDnsUpstreamsCapable(ctx)
	res := map[string]any{"capable": capable, "dohCapable": s.mtDohUpstreamsCapable(ctx)}
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
// группа dns-tunnel на выбранном пуле (статика под адреса политики
// в комплекте), затем один PUT порядка в форк. Выключенная политика
// только сохраняется (текущие апстримы не трогаем).
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
	if p.Enabled && hasEncryptedUpstream(composed) && !s.mtDohUpstreamsCapable(r.Context()) {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "DoT/DoH-уровни требуют MagiTrickle 0.8.2-rev11+ (обновите MT в Системе)"})
		return
	}
	if p.Enabled && p.Tunnel != "" {
		// адреса апстримов политики -> статика dns-tunnel: запросы к
		// провайдерам DoT/DoH тоже заворачиваются в туннель
		ips, failed := upstreamHostIPs(r.Context(), composed)
		for _, h := range failed {
			s.store.LogEvent("dns", "policy", "адрес "+h+" не разрезолвился для статики dns-tunnel (применю без него; повторите позже)")
		}
		if _, err := s.ensureDnsTunnelGroup(r.Context(), p.Tunnel, ips); err != nil {
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

// policyProbeLevel - уровень политики как нога зонда: вид транспорта
// берётся из схемы адреса (tls:// -> DoT, https:// -> DoH, иначе UDP).
func policyProbeLevel(name, addr string) (dnsprobe.Level, bool) {
	switch {
	case strings.HasPrefix(addr, "tls://"):
		hostPort := strings.TrimPrefix(addr, "tls://")
		host, port := hostPort, "853"
		if h, p, err := net.SplitHostPort(hostPort); err == nil {
			host, port = h, p
		}
		return dnsprobe.Level{Level: name + ": " + addr, Kind: "dot", Server: net.JoinHostPort(host, port), SNI: host}, true
	case strings.HasPrefix(addr, "https://"):
		u, err := url.Parse(addr)
		if err != nil {
			return dnsprobe.Level{}, false
		}
		return dnsprobe.Level{Level: name + ": " + addr, Kind: "doh", Server: addr, SNI: u.Hostname()}, true
	}
	if a, err := normalizeUpstream(addr); err == nil {
		return dnsprobe.Level{Level: name + ": " + a, Kind: "udp", Server: a}, true
	}
	return dnsprobe.Level{}, false
}

// postDnsPolicyProbe - POST /api/v1/dns/policy/probe: канарейка
// (deb.torproject.org) по каждому уровню политики; трафик самой mawg
// маркируется группами MT (rev7+), так что туннельные уровни зондируются
// по-настоящему - через туннель; DoT/DoH-уровни - своим транспортом.
func (s *Server) postDnsPolicyProbe(w http.ResponseWriter, r *http.Request) {
	p := s.store.DNSCascade()
	if p == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "политика не задана"})
		return
	}
	var levels []dnsprobe.Level
	appendLevel := func(name, addr string) {
		if l, ok := policyProbeLevel(name, addr); ok {
			levels = append(levels, l)
		}
	}
	for _, raw := range p.Primary {
		name := "L1 напрямую"
		if p.Tunnel != "" {
			name = "L2 туннель " + p.Tunnel
		}
		appendLevel(name, raw)
	}
	for _, raw := range p.Fallback {
		appendLevel("L3 запасной", raw)
	}
	if p.Yandex {
		levels = append(levels, dnsprobe.Level{Level: "L4 Яндекс: 77.88.8.8:53", Kind: "udp", Server: "77.88.8.8:53"})
	}
	if p.Provider {
		if up := dnsprobe.UpstreamFromResolv(); up != "" {
			appendLevel("L5 провайдер", up)
		}
	}
	if len(levels) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "в политике нет ни одного уровня"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": dnsprobe.Run(levels, 3*time.Second)})
}
