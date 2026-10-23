package magitrickle

import (
	"embed"
	"encoding/json"
	"strings"
)

// Списки пресетов запечены из репозитория itdoginfo/allow-domains
// (обновление: sh tools/update-presets.sh). Источник указывается честно
// в каждом пресете и в ответе API.
//
//go:embed data/allow-domains
var listsFS embed.FS

const AllowDomainsURL = "https://github.com/itdoginfo/allow-domains"

type Preset struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Source string `json:"source,omitempty"`
	// Note - предупреждение о составе (tooltip в селекте шаблонов):
	// например подсети Meta общие для всех сервисов и ловят весь Meta.
	Note  string `json:"note,omitempty"`
	Rules []Rule `json:"rules"`
}

type listManifestInfo struct {
	URL     string `json:"source"`
	Commit  string `json:"commit"`
	Updated string `json:"updated"`
}

var listManifest = loadManifest()

type presetSpec struct {
	ID       string
	Title    string
	Note     string
	Domains  []string
	SubnetV4 []string
	SubnetV6 []string
}

var presetSpecs = []presetSpec{
	{
		ID:       "telegram",
		Title:    "Telegram (домены + подсети IPv4/IPv6)",
		Domains:  []string{"domains/telegram.lst"},
		SubnetV4: []string{"subnets4/telegram.lst"},
		SubnetV6: []string{"subnets6/telegram.lst"},
	},
	{
		ID:       "meta",
		Title:    "Meta: всё (Instagram, Facebook, WhatsApp, Threads; домены + IPv4/IPv6)",
		Domains:  []string{"domains/meta.lst"},
		SubnetV4: []string{"subnets4/meta.lst"},
		SubnetV6: []string{"subnets6/meta.lst"},
	},
	// Сплит Meta по сервисам (85-док): домены режутся честно, подсети -
	// нет (общие у всего Meta), потому WhatsApp несёт их с предупреждением,
	// а отдельный meta-subnets - «весь Meta-трафик» без доменов.
	{
		ID:       "whatsapp",
		Title:    "WhatsApp (домены + подсети IPv4)",
		Note:     "подсети WhatsApp общие у всего Meta: группа поймает трафик ВСЕХ сервисов Meta (Instagram/Facebook/Threads); для доменов есть отдельные шаблоны",
		Domains:  []string{"domains/whatsapp.lst"},
		SubnetV4: []string{"subnets4/whatsapp.lst"},
	},
	{
		ID:      "instagram",
		Title:   "Instagram (домены)",
		Domains: []string{"domains/instagram.lst"},
	},
	{
		ID:      "facebook",
		Title:   "Facebook (домены)",
		Domains: []string{"domains/facebook.lst"},
	},
	{
		ID:      "threads",
		Title:   "Threads (домены)",
		Domains: []string{"domains/threads.lst"},
	},
	{
		ID:       "meta-subnets",
		Title:    "Meta (подсети): весь Meta-трафик без доменов",
		Note:     "правила только по подсетям Meta IPv4/IPv6 - через группу пойдёт трафик всех сервисов Meta (Instagram, Facebook, WhatsApp, Threads); отдельные доменные шаблоны точнее",
		SubnetV4: []string{"subnets4/meta.lst"},
		SubnetV6: []string{"subnets6/meta.lst"},
	},
	{
		ID:       "discord",
		Title:    "Discord (домены + подсети IPv4/IPv6)",
		Domains:  []string{"domains/discord.lst"},
		SubnetV4: []string{"subnets4/discord.lst"},
		SubnetV6: []string{"subnets6/discord.lst"},
	},
	{
		ID:       "twitter",
		Title:    "Twitter/X (домены + подсети IPv4/IPv6)",
		Domains:  []string{"domains/twitter.lst"},
		SubnetV4: []string{"subnets4/twitter.lst"},
		SubnetV6: []string{"subnets6/twitter.lst"},
	},
	{
		ID:      "tiktok",
		Title:   "TikTok (домены)",
		Domains: []string{"domains/tiktok.lst"},
	},
	{
		ID:       "roblox",
		Title:    "Roblox (домены + подсети IPv4)",
		Domains:  []string{"domains/roblox.lst"},
		SubnetV4: []string{"subnets4/roblox.lst"},
	},
	{
		ID:       "google",
		Title:    "Google AI, Meet, Play (домены + IPv4)",
		Domains:  []string{"domains/google_ai.lst", "domains/google_meet.lst", "domains/google_play.lst"},
		SubnetV4: []string{"subnets4/google_meet.lst"},
	},
	{
		ID:      "porn",
		Title:   "Porn (домены)",
		Domains: []string{"categories/porn.lst"},
	},
	{
		ID:      "anime",
		Title:   "Anime (домены)",
		Domains: []string{"categories/anime.lst"},
	},
	{
		ID:      "geoblock",
		Title:   "GeoBlock: доступ из РФ через VPN (ChatGPT, Claude, новости и др.)",
		Domains: []string{"categories/geoblock.lst"},
	},
}

func loadManifest() listManifestInfo {
	b, err := listsFS.ReadFile("data/allow-domains/MANIFEST.json")
	if err != nil {
		return listManifestInfo{URL: AllowDomainsURL}
	}
	var m listManifestInfo
	if err := json.Unmarshal(b, &m); err != nil {
		return listManifestInfo{URL: AllowDomainsURL}
	}
	if m.URL == "" {
		m.URL = AllowDomainsURL
	}
	return m
}

// SourceNote: человекочитаемая пометка о версии списков (дата, коммит).
func SourceNote() string {
	if listManifest.Updated == "" {
		return ""
	}
	note := "списки от " + listManifest.Updated
	if listManifest.Commit != "" {
		c := listManifest.Commit
		if len(c) > 7 {
			c = c[:7]
		}
		note += ", коммит " + c
	}
	return note
}

func listLines(rel string) []string {
	raw, err := listsFS.ReadFile("data/allow-domains/" + rel)
	if err != nil {
		return nil
	}
	var out []string
	for _, ln := range strings.Split(string(raw), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		out = append(out, strings.ToLower(ln))
	}
	return out
}

func buildPreset(spec presetSpec) Preset {
	p := Preset{ID: spec.ID, Title: spec.Title, Source: AllowDomainsURL, Note: spec.Note}
	add := func(files []string, typ string) {
		for _, f := range files {
			for _, v := range listLines(f) {
				p.Rules = append(p.Rules, Rule{Type: typ, Rule: v, Enable: true})
			}
		}
	}
	add(spec.Domains, "namespace")
	add(spec.SubnetV4, "subnet")
	add(spec.SubnetV6, "subnet6")
	return p
}

// dnsTunnelPreset - пресет «DNS через туннель»: публичные резолверы
// (часто жёстко вшиты в телевизорах/приставках) уходят в туннель целиком,
// DHCP-резолв при этом остаётся как настроен. В отличие от allow-domains
// список встроен в код (subnet-правила матчатся по адресу назначения без
// DNS); после создания группа обычная - правила правятся, источники
// навешиваются политикой «кто ходит в группу».
var dnsTunnelPreset = Preset{
	ID:     "dns-tunnel",
	Title:  "DNS через туннель (публичные резолверы)",
	Source: "встроенный список",
	Rules: []Rule{
		{Type: "subnet", Rule: "8.8.8.8/32", Enable: true},
		{Type: "subnet", Rule: "8.8.4.4/32", Enable: true},
		{Type: "subnet", Rule: "1.1.1.1/32", Enable: true},
		{Type: "subnet", Rule: "1.0.0.1/32", Enable: true},
		{Type: "subnet", Rule: "9.9.9.9/32", Enable: true},
		{Type: "subnet", Rule: "149.112.112.112/32", Enable: true},
		{Type: "subnet", Rule: "208.67.222.222/32", Enable: true},
		{Type: "subnet", Rule: "208.67.220.220/32", Enable: true},
		{Type: "subnet6", Rule: "2001:4860:4860::8888/128", Enable: true},
		{Type: "subnet6", Rule: "2001:4860:4860::8844/128", Enable: true},
		{Type: "subnet6", Rule: "2606:4700:4700::1111/128", Enable: true},
		{Type: "subnet6", Rule: "2606:4700:4700::1001/128", Enable: true},
		{Type: "subnet6", Rule: "2620:fe::fe/128", Enable: true},
		{Type: "subnet6", Rule: "2620:fe::9/128", Enable: true},
	},
}

var Presets = func() []Preset {
	out := make([]Preset, 0, len(presetSpecs)+1)
	for _, spec := range presetSpecs {
		out = append(out, buildPreset(spec))
	}
	return append(out, dnsTunnelPreset)
}()

func PresetByID(id string) (Preset, bool) {
	for _, p := range Presets {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}
