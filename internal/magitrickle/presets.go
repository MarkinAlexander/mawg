package magitrickle

type Preset struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Rules []Rule `json:"rules"`
}

var Presets = []Preset{
	{
		ID:    "telegram",
		Title: "Telegram (подсети + домены)",
		Rules: []Rule{
			{Type: "subnet", Rule: "149.154.160.0/20", Name: "TG DC", Enable: true},
			{Type: "subnet", Rule: "91.108.0.0/16", Name: "TG DC2 all", Enable: true},
			{Type: "subnet", Rule: "91.105.192.0/21", Name: "TG DC1", Enable: true},
			{Type: "subnet", Rule: "5.28.192.0/18", Name: "TG", Enable: true},
			{Type: "subnet", Rule: "185.76.128.0/22", Name: "TG voice", Enable: true},
			{Type: "subnet", Rule: "185.76.148.0/22", Name: "TG voice2", Enable: true},
			{Type: "subnet", Rule: "95.161.64.0/20", Name: "TG", Enable: true},
			{Type: "subnet", Rule: "95.161.128.0/22", Name: "TG", Enable: true},
			{Type: "subnet", Rule: "194.221.250.0/24", Name: "TG", Enable: true},
			{Type: "namespace", Rule: "t.me", Enable: true},
			{Type: "namespace", Rule: "telegram.org", Enable: true},
			{Type: "namespace", Rule: "telesco.pe", Enable: true},
			{Type: "namespace", Rule: "tdesktop.com", Enable: true},
			{Type: "namespace", Rule: "tg.dev", Enable: true},
		},
	},
}

func PresetByID(id string) (Preset, bool) {
	for _, p := range Presets {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}
