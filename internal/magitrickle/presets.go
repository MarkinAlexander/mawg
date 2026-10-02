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
	Rules  []Rule `json:"rules"`
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
		Title:    "Meta: Instagram, Facebook, WhatsApp (домены + IPv4/IPv6)",
		Domains:  []string{"domains/meta.lst"},
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
	p := Preset{ID: spec.ID, Title: spec.Title, Source: AllowDomainsURL}
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

var Presets = func() []Preset {
	out := make([]Preset, 0, len(presetSpecs))
	for _, spec := range presetSpecs {
		out = append(out, buildPreset(spec))
	}
	return out
}()

func PresetByID(id string) (Preset, bool) {
	for _, p := range Presets {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}
