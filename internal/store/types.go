package store

import (
	"encoding/json"
	"strings"
	"time"
)

const (
	FallbackDirect      = "direct"
	FallbackHold        = "hold"
	FallbackPoolPrefix  = "pool:"
	FallbackIfacePrefix = "iface:"

	PlatformKeenetic = "keenetic"
	PlatformOpenwrt  = "openwrt"

	ModeUp       = "up"
	ModeFallback = "fallback"

	IfaceManaged  = "managed"
	IfaceExternal = "external"
	IfaceHidden   = "hidden"
)

func FallbackPoolName(fallback string) (string, bool) {
	name := strings.TrimPrefix(fallback, FallbackPoolPrefix)
	return name, strings.HasPrefix(fallback, FallbackPoolPrefix) && name != ""
}

func FallbackIfaceName(fallback string) (string, bool) {
	name := strings.TrimPrefix(fallback, FallbackIfacePrefix)
	return name, strings.HasPrefix(fallback, FallbackIfacePrefix) && name != ""
}

const (
	ProbeTypeICMP = "icmp"
	ProbeTypeHTTP = "http"

	DefaultProbeHTTP = "http://www.gstatic.com/generate_204"
	DefaultProbeICMP = "1.1.1.1"
)

type ProbeConfig struct {
	Type     string `json:"type"`
	Target   string `json:"target"`
	MaxRTTms int    `json:"maxRttMs,omitempty"`
}

func (p *ProbeConfig) Normalized() ProbeConfig {
	if p == nil {
		return ProbeConfig{Type: ProbeTypeHTTP, Target: DefaultProbeHTTP}
	}
	out := *p
	if out.Type != ProbeTypeICMP && out.Type != ProbeTypeHTTP {
		if out.Target == "" {
			out.Type = ProbeTypeHTTP
		} else if IsHTTPProbe(out.Target) {
			out.Type = ProbeTypeHTTP
		} else {
			out.Type = ProbeTypeICMP
		}
	}
	if out.Target == "" {
		if out.Type == ProbeTypeICMP {
			out.Target = DefaultProbeICMP
		} else {
			out.Target = DefaultProbeHTTP
		}
	}
	return out
}

func (p *ProbeConfig) Empty() bool {
	return p == nil || p.Target == ""
}

type PoolSettings struct {
	ProbeHost        string `json:"probeHost"`
	CheckIntervalSec int    `json:"checkIntervalSec"`
	FailThreshold    int    `json:"failThreshold"`
	CooldownMin      int    `json:"cooldownMin"`
	Fallback         string `json:"fallback"`
	Keepalive        int    `json:"keepalive"`
	MaxRTTms         int    `json:"maxRttMs,omitempty"`

	Platform           string `json:"platform"`
	KeeneticSlot       string `json:"keeneticSlot,omitempty"`
	OpenwrtProto       string `json:"openwrtProto,omitempty"`
	MagitrickleGroupID string `json:"magitrickleGroupID,omitempty"`
	Source             string `json:"source,omitempty"`     // URL/ссылка, из которой собраны конфиги пула (для цикла обновлений)
	EngineMode         string `json:"engineMode,omitempty"` // "singbox" - узлы пула в tun-интерфейсе движка, а не в нативном слоте
	TunName            string `json:"tunName,omitempty"`
}

func (s PoolSettings) WithDefaults() PoolSettings {
	out := s
	if out.ProbeHost == "" {
		out.ProbeHost = "1.1.1.1"
	}
	if out.CheckIntervalSec <= 0 {
		out.CheckIntervalSec = 30
	}
	if out.FailThreshold <= 0 {
		out.FailThreshold = 3
	}
	if out.CooldownMin <= 0 {
		out.CooldownMin = 10
	}
	if out.Fallback != FallbackHold && !strings.HasPrefix(out.Fallback, FallbackPoolPrefix) && !strings.HasPrefix(out.Fallback, FallbackIfacePrefix) {
		out.Fallback = FallbackDirect
	}
	if out.Keepalive <= 0 {
		out.Keepalive = 25
	}
	return out
}

type ManagedConfig struct {
	File      string   `json:"file"`
	Original  string   `json:"original"`
	Endpoint  string   `json:"endpoint"`
	PublicKey string   `json:"publicKey"`
	Enabled   bool     `json:"enabled"`
	Addresses []string `json:"addresses,omitempty"`
}

type Pool struct {
	Name     string          `json:"name"`
	Settings PoolSettings    `json:"settings"`
	Configs  []ManagedConfig `json:"configs"`
	Disabled bool            `json:"disabled,omitempty"`
}

type PoolState struct {
	ActiveFile     string           `json:"activeFile"`
	Mode           string           `json:"mode"`
	Rotations      int              `json:"rotations"`
	Cooldowns      map[string]int64 `json:"cooldowns"`
	Cursor         int              `json:"cursor"`
	DisabledGroups []string         `json:"disabledGroups"`
	ReboundGroups  []string         `json:"reboundGroups,omitempty"`
	SuspendPending bool             `json:"suspendPending,omitempty"`

	Since       time.Time `json:"-"`
	ConsecFails int       `json:"-"`
	LastCheck   time.Time `json:"-"`
	LastResult  string    `json:"-"`
	LastError   string    `json:"-"`
	GraceUntil  int64     `json:"-"`
}

// GroupPolicy - поведение группы магитрикла при отвале её основного
// интерфейса. Пустая политика = обычное поведение (выключение при
// фоллбеке пула). Direct - прямой ход после проверки WAN, Iface -
// переписать на запасной интерфейс. Блокировка (blackhole) делается
// выбором интерфейса группы, а не политикой.
type GroupPolicy struct {
	OnDead string `json:"onDead"`          // "" | "direct" | "iface"
	Iface  string `json:"iface,omitempty"` // для OnDead == "iface"
}

func (p GroupPolicy) Default() bool { return p.OnDead == "" }

// CascadeEntry - служебная группа-каскад: адреса эндпоинтов источника
// направляются через интерфейс Via.
type CascadeEntry struct {
	Group   string   `json:"group"`
	Name    string   `json:"name"`
	Source  string   `json:"source"` // "pool:warp" | "iface:wg0"
	Via     string   `json:"via"`    // "pool:proton" | "iface:wg0"
	Domains []string `json:"domains,omitempty"`
}

type DegradedGroup struct {
	OrigIface string `json:"origIface"`
	Mode      string `json:"mode"` // "iface" | "direct" | "direct-pending"
}

const (
	PolicyDirect = "direct"
	PolicyIface  = "iface"
)

type IfaceEntry struct {
	Device string       `json:"device"`
	Mode   string       `json:"mode"`
	Probe  *ProbeConfig `json:"probe,omitempty"`
}

// IPAllow - запись allowlist панели с тумблером: выключенная запись
// хранится, но не применяется. JSON совместим со старым форматом
// (простая строка = включённая запись).
type IPAllow struct {
	Value string `json:"v"`
	On    bool   `json:"on"`
}

func (e *IPAllow) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		e.Value, e.On = s, true
		return nil
	}
	var obj struct {
		Value string `json:"v"`
		On    *bool  `json:"on"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	e.Value = obj.Value
	e.On = obj.On == nil || *obj.On
	return nil
}

type Settings struct {
	WebPort       int                    `json:"webPort"`
	ListenAddr    string                 `json:"listenAddr,omitempty"`
	AllowedIPs    []IPAllow              `json:"allowedIps,omitempty"`
	AuthDisabled  bool                   `json:"authDisabled,omitempty"`
	Ifaces        []IfaceEntry           `json:"ifaces,omitempty"`
	WANProbe      *ProbeConfig           `json:"wanProbe,omitempty"`
	RCIToken      string                 `json:"rciToken,omitempty"`
	GroupPolicies map[string]GroupPolicy `json:"groupPolicies,omitempty"`
	Cascades      []CascadeEntry         `json:"cascades,omitempty"`
	SingboxMode   string                 `json:"singboxMode,omitempty"`
}

func (s Settings) WithDefaults() Settings {
	out := s
	if out.WebPort <= 0 {
		out.WebPort = 8090
	}
	if out.ListenAddr == "" {
		out.ListenAddr = "0.0.0.0"
	}
	return out
}

type RootConfig struct {
	Settings Settings `json:"settings"`
	Pools    []Pool   `json:"pools"`
	Bundles  []Bundle `json:"bundles,omitempty"`
}

type Bundle struct {
	Name    string   `json:"name"`
	Members []string `json:"members"`
	Groups  []string `json:"groups"`
}

type BundleState struct {
	Member string `json:"member"`
}

type StateFile struct {
	Pools          map[string]*PoolState    `json:"pools"`
	Bundles        map[string]*BundleState  `json:"bundles,omitempty"`
	DegradedGroups map[string]DegradedGroup `json:"degradedGroups,omitempty"`
}

type Event struct {
	Time    time.Time `json:"time"`
	Pool    string    `json:"pool"`
	Kind    string    `json:"kind"`
	Message string    `json:"message"`
}

func NewPoolState() *PoolState {
	return &PoolState{Mode: ModeUp, Cooldowns: map[string]int64{}}
}
