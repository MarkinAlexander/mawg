package magitrickle

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"strings"
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

const sourcesRevMin = 3 // первая ревизия с полем sources (0.8.2-rev3)

var revRe = regexp.MustCompile(`rev(\d+)`)

// SourcesSupported: ревизии форка rev3+ (0.8.2-rev3) знают поле sources;
// апстрим, старые ревизии и нечитаемые строки - нет.
func SourcesSupported(version string) bool {
	m := revRe.FindStringSubmatch(version)
	if m == nil {
		return false
	}
	n, err := strconv.Atoi(m[1])
	return err == nil && n >= sourcesRevMin
}

const dnsUpstreamsRevMin = 8 // первая ревизия с /api/v1/dns/upstreams (0.8.2-rev8)

// DnsUpstreamsSupported: rev8+ отдают живой стейт каскада апстримов
// dns-proxy и принимают правку без рестарта демона; на старых ревизиях
// панель показывает апстримы из дискового config.yaml и честно просит
// обновления MT.
func DnsUpstreamsSupported(version string) bool {
	m := revRe.FindStringSubmatch(version)
	if m == nil {
		return false
	}
	n, err := strconv.Atoi(m[1])
	return err == nil && n >= dnsUpstreamsRevMin
}

const dohUpstreamsRevMin = 11 // первая ревизия с DoT/DoH-апстримами каскада (0.8.2-rev11)

// DohUpstreamsSupported: rev11+ понимают схемы tls:// (DoT, RFC 7858) и
// https:// (DoH, RFC 8484) в списке апстримов dns-proxy; на старых
// ревизиях политика с DoT/DoH-уровнями честно отказывается применяться.
func DohUpstreamsSupported(version string) bool {
	m := revRe.FindStringSubmatch(version)
	if m == nil {
		return false
	}
	n, err := strconv.Atoi(m[1])
	return err == nil && n >= dohUpstreamsRevMin
}

const dnsZonesRevMin = 13 // первая ревизия с зонным DNS (0.8.2-rev13)

// DnsZonesSupported: rev13+ умеют зонный DNS - таблица зон dns-proxy
// (зона -> свои упорядоченные апстримы, fail-closed) и API
// GET/PUT /api/v1/dns/zones; на старых ревизиях карточка «Зонные DNS» скрыта.
func DnsZonesSupported(version string) bool {
	m := revRe.FindStringSubmatch(version)
	if m == nil {
		return false
	}
	n, err := strconv.Atoi(m[1])
	return err == nil && n >= dnsZonesRevMin
}

const dnsHealthRevMin = 14 // первая ревизия с health-политикой зондов (0.8.2-rev14)

// DnsHealthSupported: rev14+ принимают health-блок в PUT
// /api/v1/dns/upstreams (enabled, интервал/таймаут/циклы/пауза) и stagger
// залпа проб; на старых ревизиях редактор зондов скрыт - тело со старым
// форком посылать бессмысленно.
func DnsHealthSupported(version string) bool {
	m := revRe.FindStringSubmatch(version)
	if m == nil {
		return false
	}
	n, err := strconv.Atoi(m[1])
	return err == nil && n >= dnsHealthRevMin
}

// RevOf - номер ревизии форка из строки версии (0.8.2-rev11-mawg-dev -> 11);
// 0 = не форк или ревизия не читается.
func RevOf(version string) int {
	m := revRe.FindStringSubmatch(version)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// MtFlavor - семейство бэкенда MT по строке версии: mt-c (форк на C,
// суффикс -mawg-c), наш Go-форк (подстрока mawg: -mawg-dev и старые
// -mawgfix1/2) или апстрим (в т.ч. бинари без /api/v1/version - строка
// пустая или нечитаемая).
type MtFlavor string

const (
	FlavorMtC      MtFlavor = "mawg-c"
	FlavorGoFork   MtFlavor = "mawg-dev"
	FlavorUpstream MtFlavor = "upstream"
)

// FlavorOf - flavor бэкенда MT из строки версии. Порядок проверок важен:
// "-mawg-c" сама содержит "mawg", поэтому mt-c детектится первой.
func FlavorOf(version string) MtFlavor {
	if strings.Contains(version, "-mawg-c") {
		return FlavorMtC
	}
	if strings.Contains(version, "mawg") {
		return FlavorGoFork
	}
	return FlavorUpstream
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
