package magitrickle

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DnsUpstream - здоровье одного апстрима dns-proxy MT (форк rev8+):
// порядок в списке = приоритет, Active - первый живой (в него идут
// клиентские запросы).
type DnsUpstream struct {
	Address  string    `json:"address"`
	Alive    bool      `json:"alive"`
	Active   bool      `json:"active"`
	Fails    uint      `json:"fails"`
	Oks      uint      `json:"oks"`
	LastSwap time.Time `json:"lastSwap"`
	Reason   string    `json:"reason,omitempty"`
}

// DnsUpstreamsState - снимок каскада апстримов dns-proxy MT: политика
// зондов-канареек + стейт каждого апстрима (только в памяти MT).
type DnsUpstreamsState struct {
	Available bool `json:"available"`
	Health    struct {
		Enabled       bool   `json:"enabled"`
		Interval      string `json:"interval"`
		Timeout       string `json:"timeout"`
		FailCycles    uint   `json:"failCycles"`
		RestoreCycles uint   `json:"restoreCycles"`
		RestoreGap    string `json:"restoreGap"`
	} `json:"health"`
	Upstreams []DnsUpstream `json:"upstreams"`
}

// DnsUpstreams - состояние каскада апстримов dns-proxy (форк rev8+,
// GET /api/v1/dns/upstreams).
func (c *Client) DnsUpstreams(ctx context.Context) (*DnsUpstreamsState, error) {
	out := &DnsUpstreamsState{Upstreams: []DnsUpstream{}}
	if err := c.call(ctx, http.MethodGet, "/api/v1/dns/upstreams", nil, out); err != nil {
		return nil, err
	}
	return out, nil
}

// DnsHealthPatch - правка политики зондов апстримов (форк rev14+,
// управление нагрузкой проб из панели): nil-поля = «не трогать»,
// длительности в секундах. Указатели обязательны: enabled=false обязан
// доезжать (голый bool с omitempty потерял бы false).
type DnsHealthPatch struct {
	Enabled       *bool `json:"enabled,omitempty"`
	IntervalSec   *uint `json:"intervalSec,omitempty"`
	TimeoutSec    *uint `json:"timeoutSec,omitempty"`
	FailCycles    *uint `json:"failCycles,omitempty"`
	RestoreCycles *uint `json:"restoreCycles,omitempty"`
	RestoreGapSec *uint `json:"restoreGapSec,omitempty"`
}

// SetDnsUpstreams - замена списка апстримов dns-proxy (порядок =
// приоритет; форк сам пишет конфиг и горячо подменяет список без
// рестарта демона). Ошибка форка отдаётся с её текстом ({"error": ...}),
// чтобы панель показала причину отказа, а не номер статуса.
// addrs == nil (health-only запись, rev14+) - ключа upstreams в теле
// нет, MT список не трогает; health != nil правит политику зондов.
func (c *Client) SetDnsUpstreams(ctx context.Context, addrs []string, health *DnsHealthPatch) error {
	body := map[string]any{}
	if addrs != nil {
		body["upstreams"] = addrs
	}
	if health != nil {
		body["health"] = health
	}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.BaseURL+"/api/v1/dns/upstreams", bytes.NewReader(data))
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
	errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var ferr struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(errBody, &ferr) == nil && ferr.Error != "" {
		return fmt.Errorf("MT: %s", ferr.Error)
	}
	return &statusError{method: http.MethodPut, path: "/api/v1/dns/upstreams", code: resp.StatusCode}
}
