package warp

import (
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
)

// Пресеты подсетей для AllowedIPs (split-tunnel). Данные - из
// warp-generator (sites_data.go, генерируются экстрактором), реализация
// своя. Слово владельца: это НЕ «маскировочные сайты», а РАЗРЕШЁННЫЕ
// ПОДСЕТИ - что пойдёт через туннель, если конфиг использовать в
// AmneziaWG и подобных клиентах. Свои подсети юзер вводит CIDR'ом:
// WireGuard в AllowedIPs понимает только подсети (домен - это снапшот
// резолва, у CDN неполный и устаревает - не делаем).

// SitePreset - один пресет: сколько CIDR-префиксов входит.
type SitePreset struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

// SitePresets - все пресеты в стабильном порядке (как в источнике).
func SitePresets() []SitePreset {
	out := make([]SitePreset, 0, len(sitePresetsData))
	for _, s := range sitePresetsData {
		out = append(out, SitePreset{ID: s.ID, Label: s.Label, Count: len(strings.Split(s.Raw, ","))})
	}
	return out
}

// NormalizeSubnet - пользовательский ввод -> CIDR: голый IP превращается
// в /32 (v4) или /128 (v6), CIDR проверяется и нормализуется. Не подсеть -
// ошибка с человеческим текстом (слово владельца: «проверять что именно
// там подсети»).
func NormalizeSubnet(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errors.New("пустая подсеть")
	}
	if ip := net.ParseIP(s); ip != nil {
		if ip.To4() != nil {
			return ip.String() + "/32", nil
		}
		return ip.String() + "/128", nil
	}
	_, ipnet, err := net.ParseCIDR(s)
	if err != nil {
		return "", fmt.Errorf("%q - не IP и не подсеть (ждём a.b.c.d или a.b.c.d/nn)", s)
	}
	_ = ipnet
	return s, nil
}

// AllowedIPs - сборка списка AllowedIPs: CIDR выбранных пресетов +
// пользовательские подсети (строгая валидация). Без выбранного и
// введённого - полный туннель 0.0.0.0/0 (как на сайте warp-generator).
func AllowedIPs(sites, subnets []string) (cidrs []string, warnings []string, err error) {
	chosen := map[string]bool{}
	byID := map[string]string{}
	for _, s := range sitePresetsData {
		byID[s.ID] = s.Raw
	}
	for _, id := range sites {
		raw, ok := byID[strings.TrimSpace(id)]
		if !ok {
			warnings = append(warnings, "неизвестный пресет подсетей: "+id)
			continue
		}
		for _, c := range strings.Split(raw, ",") {
			if c = strings.TrimSpace(c); c != "" {
				chosen[c] = true
			}
		}
	}
	var bad []string
	for _, s := range subnets {
		n, err := NormalizeSubnet(s)
		if err != nil {
			bad = append(bad, strings.TrimSpace(s))
			continue
		}
		chosen[n] = true
	}
	if len(bad) > 0 {
		return nil, nil, fmt.Errorf("это не подсети (ждём IP или CIDR вида a.b.c.d/nn): %s",
			strings.Join(bad, ", "))
	}
	for c := range chosen {
		cidrs = append(cidrs, c)
	}
	sort.Strings(cidrs)
	if len(cidrs) == 0 {
		cidrs = []string{"0.0.0.0/0"}
	}
	return cidrs, warnings, nil
}
