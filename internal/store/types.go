package store

import (
	"encoding/json"
	"strconv"
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
	// EngineDetour - каскад движка: туннель пула (wireguard-эндпоинт)
	// заводится через группу другого движкового пула (detour в sing-box).
	EngineDetour string `json:"engineDetour,omitempty"`
	// GroupMode - режим группы движка (60-док): "selector" - ручной выбор
	// узла; "rotate" - ротация по таймеру строго на рабочие узлы;
	// пусто/"urltest" - лучший по пингу (авто, дефолт).
	GroupMode string `json:"groupMode,omitempty"`
	// RotateSec - интервал ротации узлов для GroupMode="rotate" (сек).
	RotateSec int `json:"rotateSec,omitempty"`
	// NodeOff - выключенные узлы подписки (ключ - имя узла, ConfName):
	// в конфиг движка не попадают, переживают обновление источника.
	NodeOff map[string]bool `json:"nodeOff,omitempty"`
	// NativeAWG3 - free-пул поднимается нативным kmod amneziawg 3.x
	// (OpenWrt), без движка sing-box. Служебный: задаёт web-слой при
	// создании, в JSON настроек не пишем.
	NativeAWG3 bool         `json:"-"`
	TunName    string       `json:"tunName,omitempty"`
	Amnezia    *AmneziaMeta `json:"amnezia,omitempty"`
	// UpdateIntervalH - интервал автообновления источника в часах;
	// 0 = из заголовка подписки Profile-Update-Interval, без него сутки.
	UpdateIntervalH float64 `json:"updateIntervalH,omitempty"`
	// Warp - параметры генератора WARP у пулов, созданных им (режим
	// эндпоинтов, сайты маскировки): кнопка «Генерация нового конфига»
	// в карточке добавляет конфиг с теми же параметрами, без мастера.
	Warp *WarpMeta `json:"warp,omitempty"`
	// SmartCascade - умный каскад (86-док): при блокировке пула его
	// трафик заворачивается через первый живой пул Via штатным каскадом,
	// с возвратом на прямой путь по интервалу/расписанию/кнопке.
	SmartCascade *SmartCascadeSettings `json:"smartCascade,omitempty"`
}

// SmartCascadeSettings - настройки умного каскада пула (86-док).
type SmartCascadeSettings struct {
	// Via - пулы запасного пути в порядке приоритета; включается первый
	// живой (Mode==up, не Disabled).
	Via []string `json:"via,omitempty"`
	// TriggerMode - когда включать каскад: "fails" - после TriggerFails
	// отказов подряд активного конфига (тот же конфиг сразу пробуется
	// через Via, без перебора остальных); "exhaust" (пусто) - после
	// исчерпания всех конфигов пула, перед фоллбэком (слово владельца:
	// гибкость - пусть пользователь выбирает).
	TriggerMode  string `json:"triggerMode,omitempty"`
	TriggerFails int    `json:"triggerFails,omitempty"` // для fails; 0 = 3
	// Return - стратегия возврата на прямой путь.
	Return SmartCascadeReturn `json:"return,omitempty"`
	// MinDwellMin - антифлап: минимум минут в состоянии до АВТОматического
	// возврата (ручные кнопки гейт минуют). 0 = 10.
	MinDwellMin int `json:"minDwellMin,omitempty"`
}

// SmartCascadeReturn - когда пробовать прямой путь обратно.
type SmartCascadeReturn struct {
	// Mode: interval (раз в IntervalH), schedule (в TimeOfDay), manual
	// (только кнопкой). Пусто = schedule 03:00 (слово владельца).
	Mode      string   `json:"mode,omitempty"`
	IntervalH int      `json:"intervalH,omitempty"` // 0 = 6
	TimeOfDay []string `json:"timeOfDay,omitempty"` // "HH:MM"
}

// SmartCascadeState - рантайм автомата умного каскада (state.json).
type SmartCascadeState struct {
	On  bool   `json:"on,omitempty"`
	Via string `json:"via,omitempty"` // активный запасной пул
	// Group - id служебной группы каскада, управляемой автоматом (своя
	// запись; чужие каскады того же пула не трогаем).
	Group      string    `json:"group,omitempty"`
	Since      time.Time `json:"since,omitempty"`
	NextCheck  time.Time `json:"nextCheck,omitempty"`
	LastDetail string    `json:"lastDetail,omitempty"`
	// EnsureErr/EnsureAt - служебная группа каскада не создалась при
	// сохранении настроек (MT недоступен): домашка ретраит раз в 5 минут.
	EnsureErr string    `json:"ensureErr,omitempty"`
	EnsureAt  time.Time `json:"ensureAt,omitempty"`
}

// ParseTimeOfDay - "HH:MM" (00:00..23:59) для расписания возврата.
func ParseTimeOfDay(s string) (int, int, bool) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 || len(parts[0]) != 2 || len(parts[1]) != 2 {
		return 0, 0, false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}

// WarpMeta - параметры пула генератора WARP (см. PoolSettings.Warp).
type WarpMeta struct {
	Mode     string   `json:"mode"` // "random" | "fixed"
	Endpoint string   `json:"endpoint,omitempty"`
	Sites    []string `json:"sites,omitempty"`
	Subnets  []string `json:"subnets,omitempty"` // разрешённые подсети (AllowedIPs), не домены
}

// AmneziaMeta - состояние выдачи gateway Амнезии для пула из vpn://-ключа:
// какой протокол выдан, текущая локация и список доступных локаций.
type AmneziaMeta struct {
	Protocol  string           `json:"protocol,omitempty"`
	Country   string           `json:"country,omitempty"`
	Countries []GatewayCountry `json:"countries,omitempty"`
}

type GatewayCountry struct {
	Code      string   `json:"code"`
	Name      string   `json:"name,omitempty"`
	Protocols []string `json:"protocols,omitempty"`
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
	if out.SmartCascade != nil && len(out.SmartCascade.Via) > 0 {
		sc := *out.SmartCascade
		if sc.Return.Mode == "" {
			sc.Return.Mode = "schedule"
		}
		switch sc.Return.Mode {
		case "interval":
			if sc.Return.IntervalH <= 0 {
				sc.Return.IntervalH = 6
			}
		case "schedule":
			if len(sc.Return.TimeOfDay) == 0 {
				sc.Return.TimeOfDay = []string{"03:00"}
			}
		}
		if sc.MinDwellMin <= 0 {
			sc.MinDwellMin = 10
		}
		if sc.TriggerMode == "fails" && sc.TriggerFails <= 0 {
			sc.TriggerFails = 3
		}
		out.SmartCascade = &sc
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
	// Awg - уровень обфускации: plain | awg2 | awg3 (wgconf.AwgLevelOf).
	// Панель по нему решает, какие кнопки скачивания показывать: голому
	// конфигу - AWG-вариант, obfuscation 2.0 - снутый вариант, 3.x -
	// никаких преобразований (слово владельца: «до ВГ смысла нет»).
	Awg string `json:"awg,omitempty"`
}

type Pool struct {
	Free     bool            `json:"free,omitempty"`
	Premium  bool            `json:"premium,omitempty"`
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
	// ProbeOK/ProbeMs - структурированный итог последней пробы (для
	// карточки пула и строки интерфейса: единый источник, время
	// LastCheck переиспользуется). Рантайм, как соседи выше.
	ProbeOK bool `json:"-"`
	ProbeMs int  `json:"-"`

	// автообновление источника: хеш тела (гейт «ничего нового»), время и
	// интервал обновления, кулдаун деградации, счётчик неудач для панели.
	LastSubHash     string    `json:"lastSubHash,omitempty"`
	SubRefreshAt    time.Time `json:"subRefreshAt,omitempty"`
	SubIntervalH    float64   `json:"subIntervalH,omitempty"`
	LastFailRefresh time.Time `json:"lastFailRefresh,omitempty"`
	RefreshFails    int       `json:"refreshFails,omitempty"`
	LastRefreshErr  string    `json:"lastRefreshErr,omitempty"`

	// Smart - состояние автомата умного каскада (86-док); персистится,
	// чтобы расписание и флаг переживали рестарт mawg (истина о хуке -
	// Enable служебной группы в MT, SyncHooks по нему).
	Smart *SmartCascadeState `json:"smart,omitempty"`
}

// GroupPolicy - поведение группы магитрикла при отвале её основного
// интерфейса. Пустая политика = обычное поведение (выключение при
// фоллбеке пула). Direct - прямой ход после проверки WAN, Iface -
// переписать на запасной интерфейс. Блокировка (blackhole) делается
// выбором интерфейса группы, а не политикой.
//
// SourcesMode ограничивает, кто ходит в группу: "" | "all" - вся
// локальная сеть (как всегда), "all+router" - локалка плюс сам роутер
// (его трафик выводится в цепочку группы OUTPUT-хуком), "list" - только
// перечисленные IP/CIDR; SourcesRouter добавляет к списку адреса самого
// роутера. Трафик роутера идёт через OUTPUT, а не PREROUTING - без хука
// он в группу не попадает вовсе, потому режим - не поле в списке, а хук.
type GroupPolicy struct {
	OnDead string `json:"onDead"`          // "" | "direct" | "iface"
	Iface  string `json:"iface,omitempty"` // для OnDead == "iface"

	SourcesMode   string   `json:"sourcesMode,omitempty"`   // "" | "all" | "all+router" | "list"
	Sources       []string `json:"sources,omitempty"`       // список юзера для "list"
	SourcesRouter bool     `json:"sourcesRouter,omitempty"` // "list": включить адреса роутера
}

func (p GroupPolicy) Default() bool { return p.OnDead == "" }

// SourcesEmpty - источниковый режим не задан или дефолтный (вся локалка,
// без роутера): запись политики с таким режимом хранить не нужно.
func (p GroupPolicy) SourcesEmpty() bool {
	return p.SourcesMode == "" || p.SourcesMode == "all"
}

// RouterHook - группе нужен OUTPUT-хук: роутер входит в её источники.
func (p GroupPolicy) RouterHook() bool {
	return p.SourcesMode == "all+router" || (p.SourcesMode == "list" && p.SourcesRouter)
}

// EffectiveSources - список источников для MagiTrickle. Режимы all и
// all+router guard не ставят (локалка не ограничена); в list адреса
// роутера дописываются к пользовательскому списку - его трафик проходит
// guard в OUTPUT-пути по собственным адресам.
func (p GroupPolicy) EffectiveSources(routerIPs []string) []string {
	if p.SourcesMode != "list" {
		return nil
	}
	out := make([]string, 0, len(p.Sources)+len(routerIPs))
	seen := map[string]bool{}
	for _, s := range p.Sources {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	for _, s := range routerIPs {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// CascadeEntry - служебная группа-каскад: адреса эндпоинтов источника
// направляются через интерфейс Via.
type CascadeEntry struct {
	Group   string   `json:"group"`
	Name    string   `json:"name"`
	Source  string   `json:"source"` // "pool:warp" | "iface:wg0"
	Via     string   `json:"via"`    // "pool:proton" | "iface:wg0"
	Domains []string `json:"domains,omitempty"`
	// AutoDisabled - каскад погас сам sync (пул-источник опустел/удалён):
	// при оживания пула вернуть. Ручное выключение не трогаем.
	AutoDisabled bool `json:"autoDisabled,omitempty"`
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
	// EngineDisabled - движок sing-box выключен совсем (Система → Ядра
	// VPN): не стартует ни своим процессом, ни фрагментом в чужом ядре.
	EngineDisabled bool `json:"engineDisabled,omitempty"`
	// BackupKeep - сколько последних копий каждого вида бэкапа держать
	// в base/backups (2..10, слово владельца; 0 = дефолт 10).
	BackupKeep int `json:"backupKeep,omitempty"`
	// PolicyFailCycles/PolicyRestoreCycles - гистерезис политик групп
	// МагиТрикла: сколько отказных циклов до переключения с исходного
	// интерфейса и сколько здоровых до возврата (по умолчанию 2 и 2).
	PolicyFailCycles    int `json:"policyFailCycles,omitempty"`
	PolicyRestoreCycles int `json:"policyRestoreCycles,omitempty"`
	// MTGlitchy - проба совместимости при старте поймала усечение на
	// no-op PUT: глючная сборка MagiTrickle, панели показывают баннер со
	// ссылкой на форк.
	MTGlitchy bool `json:"mtGlitchy,omitempty"`
	// DNSCascade - политика каскада DNS (46-док §3, 66-док): уровни
	// L1-L5 компоновщиком mawg превращаются в упорядоченный список
	// апстримов dns-proxy MT (один PUT в форк); гистерезис переключений
	// K/M/restoreGap живёт в форке.
	DNSCascade *DnsCascadePolicy `json:"dnsCascade,omitempty"`
	// DNSProbe - свои серверы в карточке «Проверка DNS и сети» (слово
	// владельца 2026-10-18): пользователь добавляет udp/dot/doh - зонд
	// проверяет их канарейкой наравне со штатными уровнями.
	DNSProbe []DnsProbeServer `json:"dnsProbe,omitempty"`
	// DnsZones - зонный DNS (80-док, форк rev13): зона -> свои
	// упорядоченные DNS-серверы, путь до них - через пул (группа
	// dnszone-*); каскад и health серверов живёт в форке.
	DnsZones []DnsZone `json:"dnsZones,omitempty"`
}

// DnsZone - запись зонного DNS: доменная зона (покрывает себя и все
// поддомены), упорядоченный список DNS-серверов (схемы как у каскада:
// host:port / tls:// / https://), пул-туннель для пути (пусто - напрямую)
// и FallbackOpen - разрешить деградацию в общий список при полном отвале
// серверов зоны (по умолчанию fail-closed: SERVFAIL, имена не утекают).
type DnsZone struct {
	ID           string   `json:"id"`
	Zone         string   `json:"zone"`
	Upstreams    []string `json:"upstreams"`
	Pool         string   `json:"pool,omitempty"`
	FallbackOpen bool     `json:"fallbackOpen,omitempty"`
	Enable       bool     `json:"enable"`
}

// DnsProbeServer - свой сервер пробы: Kind udp|dot|doh, Server - адрес
// в каноническом виде (udp: host:port; dot: tls://host[:853];
// doh: https://host[:443]/dns-query). Заполняется панелью с валидацией.
type DnsProbeServer struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Server string `json:"server"`
}

// DnsCascadePolicy - уровни каскада: L1 основные (напрямую или L2 через
// туннель - пресет dns-tunnel на пуле), L3 запасные, L4 Яндекс,
// L5 провайдер (из resolv.conf.auto, срез на момент применения).
type DnsCascadePolicy struct {
	Enabled  bool     `json:"enabled"`
	Primary  []string `json:"primary,omitempty"`  // host[:port], порядок = приоритет
	Tunnel   string   `json:"tunnel,omitempty"`   // имя пула; пусто = основные напрямую
	Fallback []string `json:"fallback,omitempty"` // host[:port]
	Yandex   bool     `json:"yandex,omitempty"`   // 77.88.8.8
	Provider bool     `json:"provider,omitempty"` // интерфейсный upstream
}

func (s Settings) WithDefaults() Settings {
	out := s
	if out.WebPort <= 0 {
		out.WebPort = 8090
	}
	if out.ListenAddr == "" {
		out.ListenAddr = "0.0.0.0"
	}
	if out.BackupKeep <= 0 {
		out.BackupKeep = 10
	}
	if out.PolicyFailCycles <= 0 {
		out.PolicyFailCycles = 2
	}
	if out.PolicyRestoreCycles <= 0 {
		out.PolicyRestoreCycles = 2
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
