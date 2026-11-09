package magitrickle

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Статические hosts форка (rev21): имена, на которые dns-proxy отвечает
// сам, мимо апстримов и AAAA-фильтра. Таблицу наполняет контроллер
// (свой список + /etc/hosts + ip host) и проверяет пробы.

// DnsHostPair - пара имя/адрес таблицы.
type DnsHostPair struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

// DnsHostsState - снимок таблицы (available=false - демон до Start).
type DnsHostsState struct {
	Available bool          `json:"available"`
	Hosts     []DnsHostPair `json:"hosts"`
}

// DnsHosts - состояние таблицы статических hosts (требует rev21).
func (c *Client) DnsHosts(ctx context.Context) (*DnsHostsState, error) {
	out := &DnsHostsState{Hosts: []DnsHostPair{}}
	if err := c.call(ctx, http.MethodGet, "/api/v1/dns/hosts", nil, out); err != nil {
		return nil, err
	}
	return out, nil
}

// SetDnsHosts - замена таблицы (форк пишет конфиг и горячо подменяет
// без рестарта демона); пустой список = очистка. Ошибка форка отдаётся
// с её текстом ({"error": ...}).
func (c *Client) SetDnsHosts(ctx context.Context, pairs []DnsHostPair) error {
	if pairs == nil {
		pairs = []DnsHostPair{}
	}
	data, err := json.Marshal(map[string]any{"hosts": pairs})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.BaseURL+"/api/v1/dns/hosts", bytes.NewReader(data))
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
	return &statusError{method: http.MethodPut, path: "/api/v1/dns/hosts", code: resp.StatusCode}
}
