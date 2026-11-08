package store

import (
	"fmt"
	"net"
	"path/filepath"
	"strings"
)

func (s *Store) Settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.root.Settings
}

// SingboxMode - "own" (свой экземпляр движка, по умолчанию) | "shared"
// (общее ядро: фрагмент mawg-pools.json в чужой каталог + рестарт чужого сервиса)
func (s *Store) SingboxMode() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root.Settings.SingboxMode == "shared" {
		return "shared"
	}
	return "own"
}

func (s *Store) SetSingboxMode(mode string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if mode != "own" && mode != "shared" {
		return fmt.Errorf("неизвестный режим движка %q (own|shared)", mode)
	}
	s.root.Settings.SingboxMode = mode
	return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
}

// EngineDisabled - мастер-выключатель движка sing-box («Система → Ядра
// VPN»): выключен - mawg не поднимает ни свой процесс, ни фрагмент в
// чужом ядре, движковые пулы показывают «движок выключен».
func (s *Store) EngineDisabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.root.Settings.EngineDisabled
}

func (s *Store) SetEngineDisabled(v bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root.Settings.EngineDisabled == v {
		return nil
	}
	s.root.Settings.EngineDisabled = v
	return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
}

// CoreDisabled - выключено ли ядро по id ("singbox"|"mihomo"): у singbox
// исторический флаг EngineDisabled остаётся источником правды, прочие
// ядра живут в карте CoreDisabled (88-док: стоп/старт по ядру).
func (s *Store) CoreDisabled(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "singbox" {
		return s.root.Settings.EngineDisabled
	}
	return s.root.Settings.CoreDisabled[id]
}

func (s *Store) SetCoreDisabled(id string, v bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "singbox" {
		if s.root.Settings.EngineDisabled == v {
			return nil
		}
		s.root.Settings.EngineDisabled = v
		return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
	}
	if s.root.Settings.CoreDisabled[id] == v {
		return nil
	}
	if s.root.Settings.CoreDisabled == nil {
		s.root.Settings.CoreDisabled = map[string]bool{}
	}
	s.root.Settings.CoreDisabled[id] = v
	return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
}

// SetMTGlitchy пишет флаг «глючная сборка MT» только при смене значения -
// проба совместимости ходит при каждом старте, config.json не надо
// перезаписывать впустую.
func (s *Store) SetMTGlitchy(v bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root.Settings.MTGlitchy == v {
		return nil
	}
	s.root.Settings.MTGlitchy = v
	return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
}

func (s *Store) MTGlitchy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.root.Settings.MTGlitchy
}

// AutoSaveMT - автосохранение правок групп MagiTrickle (114-док):
// выключено - правки копятся в черновике base/mt-draft.json и пишутся
// одним PUT по команде панели. nil = включено.
func (s *Store) AutoSaveMT() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.root.Settings.AutoSaveMT == nil || *s.root.Settings.AutoSaveMT
}

// SetAutoSaveMT пишет только при смене эффективного значения (nil = вкл):
// config.json не надо перезаписывать впустую.
func (s *Store) SetAutoSaveMT(v bool) error {
	s.mu.Lock()
	cur := s.root.Settings.AutoSaveMT == nil || *s.root.Settings.AutoSaveMT
	if cur == v {
		s.mu.Unlock()
		return nil
	}
	s.root.Settings.AutoSaveMT = &v
	err := s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
	s.mu.Unlock()
	if err == nil {
		s.LogEvent("settings", "mt", "автосохранение MT: "+map[bool]string{true: "вкл", false: "выкл"}[v])
	}
	return err
}

func (s *Store) SetSettings(v Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.root.Settings = v.WithDefaults()
	return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
}

func (s *Store) IfaceModes() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	for _, e := range s.root.Settings.Ifaces {
		if e.Device != "" && (e.Mode == IfaceExternal || e.Mode == IfaceHidden) {
			out[e.Device] = e.Mode
		}
	}
	return out
}

func (s *Store) SetIfaceMode(device, mode string) error {
	if mode != IfaceExternal && mode != IfaceHidden {
		return fmt.Errorf("режим должен быть external (внешний) или hidden (скрытый)")
	}
	for _, p := range s.root.Pools {
		if p.DeviceName() == device {
			return fmt.Errorf("устройством %s управляет пул %q", device, p.Name)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.root.Settings.Ifaces {
		if s.root.Settings.Ifaces[i].Device == device {
			s.root.Settings.Ifaces[i].Mode = mode
			return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
		}
	}
	s.root.Settings.Ifaces = append(s.root.Settings.Ifaces, IfaceEntry{Device: device, Mode: mode})
	return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
}

// ServerSettings - сетевые настройки панели, применяются на старте демона.
func (s *Store) ServerSettings() (addr string, port int, allowed []IPAllow, authDisabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.root.Settings.WithDefaults()
	return st.ListenAddr, st.WebPort, append([]IPAllow(nil), st.AllowedIPs...), st.AuthDisabled
}

// AllowedIPList - включённые записи allowlist (для IPGate).
func (s *Store) AllowedIPList() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, e := range s.root.Settings.AllowedIPs {
		if e.On {
			out = append(out, e.Value)
		}
	}
	return out
}

func (s *Store) SetServerSettings(addr string, port int, allowed []IPAllow, authDisabled bool) error {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		addr = "0.0.0.0"
	}
	if net.ParseIP(addr) == nil {
		return fmt.Errorf("%q не похож на IP-адрес", addr)
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("порт должен быть 1-65535")
	}
	seen := map[string]bool{}
	clean := make([]IPAllow, 0, len(allowed))
	for _, e := range allowed {
		c := strings.TrimSpace(e.Value)
		if c == "" || seen[c] {
			continue
		}
		if ip := net.ParseIP(c); ip == nil {
			if _, _, err := net.ParseCIDR(c); err != nil {
				return fmt.Errorf("%q не IP и не подсеть", c)
			}
		}
		seen[c] = true
		clean = append(clean, IPAllow{Value: c, On: e.On})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.root.Settings.ListenAddr = addr
	s.root.Settings.WebPort = port
	s.root.Settings.AllowedIPs = clean
	s.root.Settings.AuthDisabled = authDisabled
	return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
}

func (s *Store) WANProbe() ProbeConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.root.Settings.WANProbe.Normalized()
}

func (s *Store) WANProbeEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.root.Settings.WANProbe != nil && s.root.Settings.WANProbe.Target != ""
}

func (s *Store) SetWANProbe(p *ProbeConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p != nil {
		norm := p.Normalized()
		p = &norm
	}
	s.root.Settings.WANProbe = p
	return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
}

// DNSCascade - политика каскада DNS (nil = не задана).
func (s *Store) DNSCascade() *DnsCascadePolicy {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.root.Settings.DNSCascade
}

func (s *Store) SetDNSCascade(p *DnsCascadePolicy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.root.Settings.DNSCascade = p
	return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
}

// DNSProbeServers - свои серверы карточки «Проверка DNS и сети».
func (s *Store) DNSProbeServers() []DnsProbeServer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]DnsProbeServer(nil), s.root.Settings.DNSProbe...)
}

func (s *Store) SetDNSProbeServers(list []DnsProbeServer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if list == nil {
		list = []DnsProbeServer{}
	}
	s.root.Settings.DNSProbe = list
	return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
}

func (s *Store) DnsZones() []DnsZone {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]DnsZone(nil), s.root.Settings.DnsZones...)
}

func (s *Store) SetDnsZones(list []DnsZone) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if list == nil {
		list = []DnsZone{}
	}
	s.root.Settings.DnsZones = list
	return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
}

// DeviceBindings - реестр «пул ↔ устройство через пул» (97-док): записи
// о связках устройство->MT-группа->пул для карточек пулов и коллизий.
func (s *Store) DeviceBindings() []DeviceBinding {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]DeviceBinding(nil), s.root.Settings.DeviceBindings...)
}

func (s *Store) SetDeviceBindings(list []DeviceBinding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if list == nil {
		list = []DeviceBinding{}
	}
	s.root.Settings.DeviceBindings = list
	return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
}

// Excludes - глобальный список исключений из обработки (94-док):
// каноничные IPv4/CIDR, трафик на них и с них не маркируется группами.
func (s *Store) Excludes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.root.Settings.Excludes...)
}

func (s *Store) SetExcludes(list []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if list == nil {
		list = []string{}
	}
	s.root.Settings.Excludes = list
	return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
}

// ExcludesDisabled - функционал исключений выключен целиком (95-док,
// слово владельца 30.10): REST-гейты 503/400, синк в каскаде спит.
func (s *Store) ExcludesDisabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.root.Settings.ExcludesDisabled
}

func (s *Store) SetExcludesDisabled(v bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root.Settings.ExcludesDisabled == v {
		return nil
	}
	s.root.Settings.ExcludesDisabled = v
	return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
}

func (s *Store) GroupPolicy(id string) GroupPolicy {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.root.Settings.GroupPolicies[id]
}

func (s *Store) GroupPoliciesAll() map[string]GroupPolicy {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]GroupPolicy{}
	for id, p := range s.root.Settings.GroupPolicies {
		out[id] = p
	}
	return out
}

func (s *Store) SetGroupPolicy(id string, p *GroupPolicy) error {
	if p != nil && p.OnDead != "" && p.OnDead != "direct" && p.OnDead != "iface" {
		return fmt.Errorf("невалидный режим отвала %q", p.OnDead)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root.Settings.GroupPolicies == nil {
		s.root.Settings.GroupPolicies = map[string]GroupPolicy{}
	}
	// запись живёт, пока задан хоть один из разделов: отвал или источники
	if p == nil || (p.OnDead == "" && p.SourcesEmpty()) {
		delete(s.root.Settings.GroupPolicies, id)
	} else {
		s.root.Settings.GroupPolicies[id] = *p
	}
	if len(s.root.Settings.GroupPolicies) == 0 {
		s.root.Settings.GroupPolicies = nil
	}
	return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
}

func (s *Store) Cascades() []CascadeEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]CascadeEntry, len(s.root.Settings.Cascades))
	copy(out, s.root.Settings.Cascades)
	return out
}

func (s *Store) CascadeByGroup(group string) (CascadeEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.root.Settings.Cascades {
		if c.Group == group {
			return c, true
		}
	}
	return CascadeEntry{}, false
}

func (s *Store) SetCascades(c []CascadeEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.root.Settings.Cascades = c
	return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
}

// RCIToken - токен локального API Keenetic 5.2+ (X-NDMA-TKN), на 5.1 и
// старее не нужен.
func (s *Store) RCIToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.root.Settings.RCIToken
}

func (s *Store) SetRCIToken(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.root.Settings.RCIToken = strings.TrimSpace(token)
	return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
}

func (s *Store) IfaceProbe(device string) *ProbeConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.root.Settings.Ifaces {
		if e.Device == device {
			return e.Probe
		}
	}
	return nil
}

// ProbedExternals - внешние интерфейсы с настроенной пробой: их статусы
// движок держит в кэше для мгновенной отдачи в /ifaces.
func (s *Store) ProbedExternals() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, e := range s.root.Settings.Ifaces {
		if e.Device != "" && e.Mode == IfaceExternal && e.Probe != nil {
			out = append(out, e.Device)
		}
	}
	return out
}

func (s *Store) SetIfaceProbe(device string, p *ProbeConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p != nil {
		norm := p.Normalized()
		p = &norm
	}
	for i := range s.root.Settings.Ifaces {
		if s.root.Settings.Ifaces[i].Device == device {
			s.root.Settings.Ifaces[i].Probe = p
			return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
		}
	}
	return fmt.Errorf("интерфейс %s не найден в реестре", device)
}

// KeeneticDNS - режим «DNS самого роутера» на Keenetic (Settings.
// KeeneticDNS, 98-док): по явному согласию пользователя mawg пишет в ndm
// одну запись ip name-server и снимает её при выключении.
func (s *Store) KeeneticDNS() KeeneticDNS {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.root.Settings.KeeneticDNS
}

func (s *Store) SetKeeneticDNS(v KeeneticDNS) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.root.Settings.KeeneticDNS = v
	return s.saveLocked(filepath.Join(s.base, "config.json"), s.root)
}
