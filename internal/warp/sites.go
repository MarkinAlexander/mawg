package warp

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"
)

// Пресеты маскировочных сайтов (split-tunnel AllowedIPs). Данные - из
// warp-generator (sites_data.go, генерируются экстрактором), реализация
// своя; RU-домены для подсказки «своих доменов» - из списков маскировки
// LxBox (loc.ru sni_pool).

// SitePreset - один сервис: сколько CIDR-префиксов входит.
type SitePreset struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

// DomainHints - подсказка поля «свои домены»: подо что маскируются
// российские пользователи (список LxBox).
var DomainHints = []string{
	"youtube.com", "x.com", "instagram.com", "facebook.com", "discord.com",
	"telegram.org", "whatsapp.com", "rutracker.org", "viber.com",
}

// SitePresets - все пресеты в стабильном порядке (как в источнике).
func SitePresets() []SitePreset {
	out := make([]SitePreset, 0, len(sitePresetsData))
	for _, s := range sitePresetsData {
		out = append(out, SitePreset{ID: s.ID, Label: s.Label, Count: len(strings.Split(s.Raw, ","))})
	}
	return out
}

// AllowedIPs - сборка списка AllowedIPs: CIDR выбранных пресетов +
// резолв пользовательских доменов (/32 по A-записям; нерешённые -
// предупреждением, не ошибкой). Без выбранного и решённого - полный
// туннель 0.0.0.0/0 (как на сайте warp-generator).
func AllowedIPs(ctx context.Context, sites, domains []string) (cidrs []string, warnings []string) {
	chosen := map[string]bool{}
	byID := map[string]string{}
	for _, s := range sitePresetsData {
		byID[s.ID] = s.Raw
	}
	for _, id := range sites {
		raw, ok := byID[strings.TrimSpace(id)]
		if !ok {
			warnings = append(warnings, "неизвестный пресет сайта: "+id)
			continue
		}
		for _, c := range strings.Split(raw, ",") {
			if c = strings.TrimSpace(c); c != "" {
				chosen[c] = true
			}
		}
	}
	for _, d := range domains {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "" {
			continue
		}
		if net.ParseIP(d) != nil {
			chosen[d+"/32"] = true
			continue
		}
		rctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		ips, err := net.DefaultResolver.LookupIPAddr(rctx, d)
		cancel()
		if err != nil || len(ips) == 0 {
			warnings = append(warnings, fmt.Sprintf("домен %s не решился - в AllowedIPs не попал", d))
			continue
		}
		var added int
		for _, ipa := range ips {
			if ip4 := ipa.IP.To4(); ip4 != nil {
				chosen[ip4.String()+"/32"] = true
				added++
			}
		}
		if added == 0 {
			warnings = append(warnings, fmt.Sprintf("у домена %s нет A-записей - в AllowedIPs не попал", d))
		}
	}
	for c := range chosen {
		cidrs = append(cidrs, c)
	}
	sort.Strings(cidrs)
	if len(cidrs) == 0 {
		cidrs = []string{"0.0.0.0/0"}
	}
	// Предупреждения (нерешённые домены) отдаются и при дефолтном
	// полном туннеле - иначе юзер не узнает, что ввод потерялся.
	return cidrs, warnings
}
