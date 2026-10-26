package wgconf

import (
	"regexp"
	"strconv"
	"strings"
)

type AWGParams struct {
	Jc, Jmin, Jmax     *string
	S1, S2, S3, S4     *string
	H1, H2, H3, H4     *string
	I1, I2, I3, I4, I5 *string
	// AWG 3.x: защита заголовка, диапазонные таймеры и паддинг. Нативно
	// их применяет Keenetic 5.2+ (позиционный asc-хвост) и kmod
	// amneziawg 3.x на OpenWrt; Keenetic 5.1 и старый kmod - только
	// движок sing-box-lx.
	HeaderProtectionKey    *string
	ContentPaddingAddition *string
	RekeyAfterTime         *string
	RekeyTimeout           *string
	RejectAfterTime        *string
	KeepaliveTimeout       *string
	MaxHandshakeAttempts   *string
	RandomTrailers         *string // on/off - флаги в хвосте asc
	DisableCookies         *string
}

func (p AWGParams) anyInitPacket() bool {
	return p.I1 != nil || p.I2 != nil || p.I3 != nil || p.I4 != nil || p.I5 != nil
}

func (p AWGParams) Present() bool {
	return p.Jc != nil || p.Jmin != nil || p.Jmax != nil ||
		p.S1 != nil || p.S2 != nil || p.S3 != nil || p.S4 != nil ||
		p.H1 != nil || p.H2 != nil || p.H3 != nil || p.H4 != nil ||
		p.anyInitPacket()
}

// AWG3 - признаки формата AmneziaWG 3.x, которые нативные интерфейсы не
// поднимут (HP-ключ, диапазонные таймеры). <r>-маркеры в I-полях сюда
// не входят: Keenetic и kmod amneziawg 3.1 понимают их нативно, старому
// kmod OpenWrt такой конфиг вместо натива даёт движок (web-слой,
// HasRandomInit).
func (p AWGParams) AWG3() bool {
	if p.HeaderProtectionKey != nil {
		return true
	}
	for _, f := range []*string{p.ContentPaddingAddition, p.RekeyAfterTime,
		p.RekeyTimeout, p.RejectAfterTime, p.KeepaliveTimeout, p.MaxHandshakeAttempts} {
		if f != nil {
			return true
		}
	}
	// включённые флаги меняют протокол; выключенные (= off/нет) -
	// конфиг эквивалентен 2.0, нативу хвост не нужен
	return awgFlagOn(p.RandomTrailers) || awgFlagOn(p.DisableCookies)
}

// HasRandomInit - в I-полях есть маркеры <r N> случайных байтов. Кинетик
// их отбрасывает (статика работает), а вот kmod amneziawg 1.x/2.x на
// OpenWrt синтаксис не знает вовсе - там такие конфиги нужны движком.
func (p AWGParams) HasRandomInit() bool {
	for _, f := range []*string{p.I1, p.I2, p.I3, p.I4, p.I5} {
		if f != nil && strings.Contains(*f, "<r ") {
			return true
		}
	}
	return false
}

func (p AWGParams) HasExtended() bool {
	return p.S3 != nil || p.S4 != nil || p.anyInitPacket()
}

func awgVal(p *string) string {
	if p == nil || strings.TrimSpace(*p) == "" {
		return "0"
	}
	return strings.TrimSpace(*p)
}

// keeneticH - Keenetic не принимает H=0 («invalid ASC parameters»), а
// конфиги warp-генераторов H1-H4 просто не пишут, ожидая 1,2,3,4:
// их собственные рабочие конфиги для Кинетиков несут ровно эти значения.
func keeneticH(p *string, def string) string {
	if p == nil || strings.TrimSpace(*p) == "" || strings.TrimSpace(*p) == "0" {
		return def
	}
	return strings.TrimSpace(*p)
}

var initBlobRe = regexp.MustCompile(`<b\s+0x([0-9A-Fa-f]+)\s*>`)
var bareHexRe = regexp.MustCompile(`^([0-9A-Fa-f]+)$`)

// initTokenRe - I-поле из токенов, которые понимает прошивка Keenetic
// (strings /lib/libndmWireguard.so): <b 0x...> статичный блоб, <r N> N
// случайных байтов, <rc N>, <t>. <rd N> (N случайных ЦИФР - подписки
// Amnezia 3.x, живой конфиг с SIP-шаблоном) - попытка как есть: не все
// сборки прошивки его знают, фолбэк - AscArgsNoRandom. Последовательность
// передаётся как есть.
var initTokenRe = regexp.MustCompile(`^(?:<b 0x(?:[0-9A-Fa-f]{2})+>|<t>|<r [0-9]+>|<rc [0-9]+>|<rd [0-9]+>)+$`)

var randomTokenRe = regexp.MustCompile(`<r(?:c|d)?\s+[0-9]+>`)

// initStatic - статичная форма I-поля: все <b>-куски склеиваются в один
// блоб (Keenetic не склеивает сам), голый hex оборачивается, <r>-маркеры
// случайных байтов выпадают. Фолбэк для значений мимо токен-формата.
func initStatic(v string) string {
	var hex strings.Builder
	for _, m := range initBlobRe.FindAllStringSubmatch(v, -1) {
		hex.WriteString(strings.ToLower(m[1]))
	}
	s := hex.String()
	if s == "" {
		if m := bareHexRe.FindStringSubmatch(strings.TrimSpace(v)); m != nil {
			s = strings.ToLower(m[1])
		}
	}
	if s == "" || len(s)%2 != 0 {
		return ""
	}
	return "<b 0x" + s + ">"
}

func initArg(v *string) string {
	if v == nil {
		return "\"\""
	}
	s := strings.TrimSpace(*v)
	// <b>/<r>/<rc>/<t>-последовательность прошивка понимает нативно -
	// передаём без искажений (проверено live на 5.01.C.3.0-1)
	if initTokenRe.MatchString(s) {
		return "\"" + s + "\""
	}
	if st := initStatic(s); st != "" {
		return "\"" + st + "\""
	}
	return "\"\""
}

// awgRangePair - значение 3.x-таймера «N» или «min-max» в два позиционных
// аргумента CLI; пустое поле - нули (прошивка трактует как дефолт).
func awgRangePair(v *string) (string, string) {
	if v == nil {
		return "0", "0"
	}
	s := strings.TrimSpace(*v)
	if s == "" {
		return "0", "0"
	}
	if i := strings.IndexByte(s, '-'); i > 0 {
		lo, hi := s[:i], s[i+1:]
		if hi == "" {
			return lo, lo
		}
		return lo, hi
	}
	return s, s
}

// awgFlagOn - RandomTrailers/DisableCookies во включённом виде
func awgFlagOn(v *string) bool {
	if v == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(*v)) {
	case "on", "1", "true":
		return true
	}
	return false
}

// AscArgs - позиционные аргументы команды «wireguard asc» Keenetic.
// 2.0-префикс: jc jmin jmax s1 s2 h1 h2 h3 h4 [+ s3 s4 i1-i5].
// 3.x-хвост (Keenetic 5.2+, подтверждён live на 5.2 Beta 1 - позиционная
// форма принимается и persists в running-config один в один):
// hp-ключ, шесть пар min-max (паддинг, rekey-after, rekey-timeout,
// reject-after, keepalive, max-handshake) и два флага 1/0.
func (p AWGParams) AscArgs() []string {
	out := make([]string, 0, 31)
	out = append(out, awgVal(p.Jc), awgVal(p.Jmin), awgVal(p.Jmax),
		awgVal(p.S1), awgVal(p.S2),
		keeneticH(p.H1, "1"), keeneticH(p.H2, "2"), keeneticH(p.H3, "3"), keeneticH(p.H4, "4"),
	)
	if p.HasExtended() || p.AWG3() {
		out = append(out, awgVal(p.S3), awgVal(p.S4))
		out = append(out, initArg(p.I1), initArg(p.I2), initArg(p.I3), initArg(p.I4), initArg(p.I5))
	}
	if p.AWG3() {
		key := `""`
		if p.HeaderProtectionKey != nil && strings.TrimSpace(*p.HeaderProtectionKey) != "" {
			key = strings.TrimSpace(*p.HeaderProtectionKey)
		}
		out = append(out, key)
		for _, f := range []*string{p.ContentPaddingAddition, p.RekeyAfterTime, p.RekeyTimeout,
			p.RejectAfterTime, p.KeepaliveTimeout, p.MaxHandshakeAttempts} {
			lo, hi := awgRangePair(f)
			out = append(out, lo, hi)
		}
		if awgFlagOn(p.RandomTrailers) {
			out = append(out, "1")
		} else {
			out = append(out, "0")
		}
		if awgFlagOn(p.DisableCookies) {
			out = append(out, "1")
		} else {
			out = append(out, "0")
		}
	}
	return out
}

// AscArgsNoRandom - asc-аргументы без случайных маркеров в I-полях
// (<r>/<rc>/<rd>): фолбэк-повтор, если прошивка токены не приняла.
// ok=false - случайных маркеров и не было, повтора не нужно.
func (p AWGParams) AscArgsNoRandom() (args []string, ok bool) {
	if !p.hasRandomInitTokens() {
		return nil, false
	}
	clone := p
	static := func(v *string) *string {
		if v == nil {
			return nil
		}
		s := initStatic(*v)
		if s == "" {
			return nil
		}
		return &s
	}
	clone.I1, clone.I2, clone.I3 = static(p.I1), static(p.I2), static(p.I3)
	clone.I4, clone.I5 = static(p.I4), static(p.I5)
	return clone.AscArgs(), true
}

func (p AWGParams) hasRandomInitTokens() bool {
	for _, f := range []*string{p.I1, p.I2, p.I3, p.I4, p.I5} {
		if f != nil && randomTokenRe.MatchString(*f) {
			return true
		}
	}
	return false
}

type Peer struct {
	PublicKey           string
	PresharedKey        string
	EndpointHost        string
	EndpointPort        int
	AllowedIPs          []string
	PersistentKeepalive int
	// KeepaliveRange - AWG 3.x форма «min-max»; при ней PersistentKeepalive
	// не заполняется (это не одно число).
	KeepaliveRange string
}

type Config struct {
	PrivateKey string
	Addresses  []string
	DNS        []string
	MTU        int
	AWG        AWGParams
	Peer       Peer
}

// NeedsEngine - конфиг требует движок sing-box-lx: AWG 3.x-поля или
// диапазонный keepalive не поднимаются нативными интерфейсами.
func (c Config) NeedsEngine() bool {
	return c.AWG.AWG3() || c.Peer.KeepaliveRange != ""
}

func (c Config) FirstIPv4() string {
	for _, a := range c.Addresses {
		if !strings.Contains(a, ":") {
			return a
		}
	}
	return ""
}

func (c Config) Endpoint() string {
	if c.Peer.EndpointHost == "" {
		return ""
	}
	if c.Peer.EndpointPort == 0 {
		return c.Peer.EndpointHost
	}
	return c.Peer.EndpointHost + ":" + strconv.Itoa(c.Peer.EndpointPort)
}
