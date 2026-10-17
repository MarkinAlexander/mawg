package magitrickle

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
)

// GetVersion опрашивает MT по /api/v1/version (форк rev5+). Старые бинари
// эндпоинта не знают (404) - вызывающий обязан tolerate'ить ошибку и
// уходить в фолбэк по configVersion дискового конфига.
func (c *Client) GetVersion(ctx context.Context) (string, error) {
	var out struct {
		Version string `json:"version"`
	}
	if err := c.call(ctx, http.MethodGet, "/api/v1/version", nil, &out); err != nil {
		return "", err
	}
	return out.Version, nil
}

// sourcesRevMin - первая ревизия форка с полем sources (0.8.2-rev3).
const sourcesRevMin = 3

var revRe = regexp.MustCompile(`rev(\d+)`)

// SourcesSupported говорит, понимает ли MT поле sources: ревизии форка
// rev3+ - да; апстрим, старые ревизии и нечитаемые строки - нет.
func SourcesSupported(version string) bool {
	m := revRe.FindStringSubmatch(version)
	if m == nil {
		return false
	}
	n, err := strconv.Atoi(m[1])
	return err == nil && n >= sourcesRevMin
}

// dnsUpstreamsRevMin - первая ревизия форка с API
// /api/v1/dns/upstreams: состояние каскада апстримов dns-proxy и его
// правка без рестарта демона (0.8.2-rev8).
const dnsUpstreamsRevMin = 8

// DnsUpstreamsSupported говорит, умеет ли MT отдавать живой стейт
// каскада апстримов и принимать правку списка (rev8+). На старых
// ревизиях панель показывает апстримы из дискового config.yaml и честно
// сообщает, что статус/редактор требуют обновления MT.
func DnsUpstreamsSupported(version string) bool {
	m := revRe.FindStringSubmatch(version)
	if m == nil {
		return false
	}
	n, err := strconv.Atoi(m[1])
	return err == nil && n >= dnsUpstreamsRevMin
}

var configVersionRe = regexp.MustCompile(`(?m)^\s*configVersion:\s*"?([^"\s"]+)`)

// ConfigVersion вытаскивает configVersion из дискового config.yaml MT:
// строку пишет бинарник, последним сохранявший конфиг. Это фолбэк
// capability-детекта для бинарей без /api/v1/version (rev4 в поле).
func ConfigVersion(configYAML []byte) string {
	m := configVersionRe.FindSubmatch(configYAML)
	if m == nil {
		return ""
	}
	return string(m[1])
}
