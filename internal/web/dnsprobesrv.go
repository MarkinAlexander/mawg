package web

// Свои серверы карточки «Проверка DNS и сети» (слово владельца
// 2026-10-18): пользователь добавляет udp/dot/doh - зонд проверяет их
// канарейкой наравне со штатными уровнями, обычным клиентским запросом
// (без fwmark: проба должна видеть ту же подмену, что и устройства).

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"mawg/internal/dnsprobe"
	"mawg/internal/store"
)

const maxProbeServers = 16

// convertProbeServer - свой сервер -> уровень зонда: адрес канонизируется
// (порт/путь достраиваются), тип сверяется со схемой.
func convertProbeServer(s store.DnsProbeServer) (dnsprobe.Level, error) {
	name := strings.TrimSpace(s.Name)
	a, err := normalizeUpstream(s.Server)
	if err != nil {
		return dnsprobe.Level{}, fmt.Errorf("%s: %w", name, err)
	}
	switch s.Kind {
	case "udp":
		if strings.Contains(a, "://") {
			return dnsprobe.Level{}, fmt.Errorf("%s: UDP-серверу нужен адрес без схемы (8.8.8.8 или 8.8.8.8:5353)", name)
		}
		if name == "" {
			name = a
		}
		return dnsprobe.Level{Level: name, Kind: "udp", Server: a}, nil
	case "dot":
		if !strings.HasPrefix(a, "tls://") {
			return dnsprobe.Level{}, fmt.Errorf("%s: DoT-серверу нужен адрес вида tls://dns.adguard.com", name)
		}
		hostPort := strings.TrimPrefix(a, "tls://")
		host, port := hostPort, "853"
		if h, p, err := net.SplitHostPort(hostPort); err == nil {
			host, port = h, p
		}
		if name == "" {
			name = "DoT " + host
		}
		return dnsprobe.Level{Level: name, Kind: "dot", Server: net.JoinHostPort(host, port), SNI: host}, nil
	case "doh":
		if !strings.HasPrefix(a, "https://") {
			return dnsprobe.Level{}, fmt.Errorf("%s: DoH-серверу нужен адрес вида https://dns.google/dns-query", name)
		}
		u, _ := url.Parse(a)
		if name == "" {
			name = "DoH " + u.Hostname()
		}
		return dnsprobe.Level{Level: name, Kind: "doh", Server: a, SNI: u.Hostname()}, nil
	}
	return dnsprobe.Level{}, fmt.Errorf("%s: неизвестный тип %q (udp, dot или doh)", name, s.Kind)
}

// dnsProbeServers - GET /api/v1/dns/probe/servers: свой список.
func (s *Server) dnsProbeServersGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"servers": s.store.DNSProbeServers()})
}

// dnsProbeServersSet - PUT /api/v1/dns/probe/servers {"servers": [...]}:
// валидация и сохранение своего списка (пустой список - очистить).
func (s *Server) dnsProbeServersSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Servers []store.DnsProbeServer `json:"servers"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil {
		writeErr(w, fmt.Errorf("кривой запрос"))
		return
	}
	if len(req.Servers) > maxProbeServers {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("не больше %d серверов", maxProbeServers)})
		return
	}
	for i := range req.Servers {
		if _, err := convertProbeServer(req.Servers[i]); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	if err := s.store.SetDNSProbeServers(req.Servers); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "servers": req.Servers})
}
