package magitrickle

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Зонный DNS форка (rev13, GET/PUT /api/v1/dns/zones): таблица зон
// dns-proxy - зона -> свои упорядоченные апстримы (формат общего каскада:
// plain host:port / tls:// / https://), запрос имени из зоны обслуживает
// список зоны (longest-suffix), полный отвал - SERVFAIL (fail-closed)
// или общий каскад при Fallback.

// DnsZoneState - живое состояние одной зоны в dns-proxy MT: имя без
// хвостовой точки, fallback-политика и стейт её апстримов (порядок =
// приоритет, Active - первый живой).
type DnsZoneState struct {
	Zone      string        `json:"zone"`
	Fallback  bool          `json:"fallback"`
	Upstreams []DnsUpstream `json:"upstreams"`
}

// DnsZonesState - снимок таблицы зон dns-proxy MT.
type DnsZonesState struct {
	Available bool           `json:"available"`
	Zones     []DnsZoneState `json:"zones"`
}

// DnsZones - состояние таблицы зон (форк rev13+, GET /api/v1/dns/zones).
func (c *Client) DnsZones(ctx context.Context) (*DnsZonesState, error) {
	out := &DnsZonesState{Zones: []DnsZoneState{}}
	if err := c.call(ctx, http.MethodGet, "/api/v1/dns/zones", nil, out); err != nil {
		return nil, err
	}
	return out, nil
}

// DnsZoneSpec - запись таблицы зон для PUT: имя зоны (покрывает себя и
// поддомены), упорядоченные адреса её серверов, fallback-политика.
type DnsZoneSpec struct {
	Zone      string   `json:"zone"`
	Upstreams []string `json:"upstreams"`
	Fallback  bool     `json:"fallback"`
}

// SetDnsZones - замена таблицы зон dns-proxy (форк сам пишет конфиг и
// горячо подменяет таблицу без рестарта демона); пустой список = очистка.
// Ошибка форка отдаётся с её текстом ({"error": ...}).
func (c *Client) SetDnsZones(ctx context.Context, zones []DnsZoneSpec) error {
	if zones == nil {
		zones = []DnsZoneSpec{}
	}
	data, err := json.Marshal(map[string]any{"zones": zones})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.BaseURL+"/api/v1/dns/zones", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var ferr struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &ferr) == nil && ferr.Error != "" {
		return fmt.Errorf("MT: %s", ferr.Error)
	}
	return &statusError{method: http.MethodPut, path: "/api/v1/dns/zones", code: resp.StatusCode}
}

