package keenetic

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"mawg/internal/platform"
	"mawg/internal/store"
	"mawg/internal/wgconf"
)

const rciBase = "http://127.0.0.1:79"

var (
	slotRe    = regexp.MustCompile(`(?i)^wireguard(\d+)$`)
	releaseRe = regexp.MustCompile(`release:\s+(\d+)\.(\d+)`)
	compRe    = regexp.MustCompile(`components:\s*(.+)`)
)

type Backend struct {
	token func() string
}

func New() *Backend { return &Backend{} }

// SetTokenProvider подключает источник токена локального API: на Keenetic
// 5.2+ RCI без него отдаёт 401 (NDM-4515), заголовок X-NDMA-TKN.
func (b *Backend) SetTokenProvider(fn func() string) { b.token = fn }

func (b *Backend) rciDo(path string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, rciBase+path, nil)
	if err != nil {
		return nil, err
	}
	if b.token != nil {
		if tk := strings.TrimSpace(b.token()); tk != "" {
			req.Header.Set("X-NDMA-TKN", tk)
		}
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func (b *Backend) Name() string { return store.PlatformKeenetic }

type versionInfo struct {
	title      string
	major      int
	minor      int
	components map[string]bool
}

func (b *Backend) parseVersion(out string) versionInfo {
	info := versionInfo{components: map[string]bool{}}
	if m := releaseRe.FindStringSubmatch(out); m != nil {
		info.major, _ = strconv.Atoi(m[1])
		info.minor, _ = strconv.Atoi(m[2])
	}
	lines := strings.Split(out, "\n")
	inComponents := false
	for _, line := range lines {
		if m := compRe.FindStringSubmatch(line); m != nil {
			inComponents = true
			collectComponents(info.components, m[1])
			continue
		}
		if inComponents {
			if line != "" && line[0] == ' ' {
				collectComponents(info.components, strings.TrimSpace(line))
			} else {
				inComponents = false
			}
		}
	}
	return info
}

func collectComponents(dst map[string]bool, list string) {
	for _, c := range strings.FieldsFunc(list, func(r rune) bool { return r == ',' || r == ' ' }) {
		c = strings.TrimSpace(c)
		if c != "" {
			dst[c] = true
		}
	}
}

func (b *Backend) Detect() error {
	if _, err := os.Stat("/bin/ndmc"); err != nil {
		return fmt.Errorf("ndmc not found, not a keenetic router")
	}
	out, err := b.ndmc("show version")
	if err != nil {
		return fmt.Errorf("ndmc show version: %v", err)
	}
	info := b.parseVersion(out)
	if info.major == 0 {
		return fmt.Errorf("cannot parse firmware version from: %s", out)
	}
	if info.major < 5 || (info.major == 5 && info.minor < 1) {
		return fmt.Errorf("firmware %d.%d is below 5.1, wireguard asc extended params unsupported", info.major, info.minor)
	}
	if !info.components["wireguard"] {
		return fmt.Errorf("wireguard component is not installed: install it in web ui (System - Component Options) and retry")
	}
	return nil
}

var ndmcConflictRe = regexp.MustCompile(`network (\S+) conflicts with interface "([^"]+)"`)
var ndmcErrorRe = regexp.MustCompile(`error\[\d+\]: ([^
]+)`)
var ndmcJunkRe = regexp.MustCompile(`\[K|\[K`)

// cleanNdmcError превращает сырой вывод ndmc в понятную ошибку: известные
// случаи получают готовый совет, управляющие символы вырезаются.
func cleanNdmcError(command string, err error, text string) error {
	text = ndmcJunkRe.ReplaceAllString(text, "")
	if m := ndmcConflictRe.FindStringSubmatch(text); m != nil {
		return fmt.Errorf("адрес %s из конфига уже занят интерфейсом %s: отключите его или выберите другой конфиг", m[1], m[2])
	}
	if m := ndmcErrorRe.FindStringSubmatch(text); m != nil {
		return fmt.Errorf("ndmc %q: %s", command, strings.TrimSpace(m[1]))
	}
	if err != nil {
		return fmt.Errorf("ndmc %q: %v: %s", command, err, strings.TrimSpace(text))
	}
	return fmt.Errorf("ndmc %q: %s", command, strings.TrimSpace(text))
}

func (b *Backend) ndmc(command string) (string, error) {
	cmd := exec.Command("/bin/ndmc", "-c", command)
	out, err := cmd.CombinedOutput()
	text := ndmcJunkRe.ReplaceAllString(string(out), "")
	if err != nil || strings.Contains(text, "Error") || strings.Contains(text, "error:") {
		return text, cleanNdmcError(command, err, text)
	}
	return text, nil
}

func (b *Backend) Ndmc(command string) (string, error) {
	return b.ndmc(command)
}

type rciPeer struct {
	PublicKey     string `json:"public-key"`
	Online        bool   `json:"online"`
	LastHandshake int64  `json:"last-handshake"`
	Enabled       bool   `json:"enabled"`
}

type rciInterface struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Link        string `json:"link"`
	Connected   string `json:"connected"`
	State       string `json:"state"`
	Address     string `json:"address"`
	Mask        string `json:"mask"`
	Wireguard   struct {
		Peers []rciPeer `json:"peer"`
	} `json:"wireguard"`
}

func (b *Backend) rciGet(path string) (rciInterface, error) {
	var out rciInterface
	body, err := b.rciDo(path)
	if err == nil {
		err = json.Unmarshal(body, &out)
	}
	if err == nil {
		return out, nil
	}
	// RCI недоступен (5.2 alpha: 401) - тот же интерфейс через CLI
	slot := path[strings.LastIndex(path, "/")+1:]
	iface, cerr := b.ndmcInterface(slot)
	if cerr != nil {
		return out, err
	}
	return iface, nil
}

func (b *Backend) ndmcInterface(slot string) (rciInterface, error) {
	out, err := b.ndmc("show interface " + slot)
	if err != nil {
		return rciInterface{}, err
	}
	ifaces := parseNDMCInterfaces(out)
	iface, ok := ifaces[slot]
	if !ok {
		return rciInterface{}, fmt.Errorf("ndmc: interface %s not found", slot)
	}
	return iface, nil
}

func (b *Backend) slotOf(pool store.Pool) (string, int, error) {
	m := slotRe.FindStringSubmatch(pool.Settings.KeeneticSlot)
	if m == nil {
		return "", -1, fmt.Errorf("pool %q has invalid keenetic slot %q", pool.Name, pool.Settings.KeeneticSlot)
	}
	idx, _ := strconv.Atoi(m[1])
	return m[0], idx, nil
}

func (b *Backend) Slots() ([]platform.SlotInfo, error) {
	body, err := b.rciDo("/rci/show/interface")
	if err == nil {
		if out, perr := parseSlots(body); perr == nil {
			return out, nil
		}
	}
	// 5.2 alpha закрыла локальный RCI паролем (NDM-4515): CLI работает всегда
	out, cerr := b.ndmc("show interface")
	if cerr != nil {
		return nil, err
	}
	return ndmcSlots(parseNDMCInterfaces(out)), nil
}

func ndmcSlots(ifaces map[string]rciInterface) []platform.SlotInfo {
	var out []platform.SlotInfo
	for id, ifc := range ifaces {
		if slotRe.FindStringSubmatch(id) == nil {
			continue
		}
		out = append(out, platform.SlotInfo{
			ID:          id,
			Device:      DeviceName(id),
			Description: ifc.Description,
			LinkUp:      ifc.Link == "up",
			Connected:   ifc.Connected == "yes",
			Address:     cidrOf(ifc.Address, ifc.Mask),
		})
	}
	sortSlots(out)
	return out
}

func cidrOf(addr, mask string) string {
	if addr == "" {
		return ""
	}
	ip := net.ParseIP(addr)
	if ip == nil {
		return ""
	}
	if mask == "" {
		return addr
	}
	m := net.ParseIP(mask)
	if m == nil {
		return addr
	}
	ones, _ := net.IPMask(m.To4()).Size()
	return fmt.Sprintf("%s/%d", addr, ones)
}

// RCI разных прошивок отдаёт список интерфейсов то объектом, то массивом;
// 5.2 alpha известна изменениями формата.
func parseSlots(body []byte) ([]platform.SlotInfo, error) {
	var raw map[string]rciInterface
	if err := json.Unmarshal(body, &raw); err != nil {
		var list []rciInterface
		if err2 := json.Unmarshal(body, &list); err2 != nil {
			return nil, err
		}
		return slotInfosList(list), nil
	}
	return slotInfos(raw), nil
}

func slotInfos(ifaces map[string]rciInterface) []platform.SlotInfo {
	var out []platform.SlotInfo
	for _, ifc := range ifaces {
		if !strings.HasPrefix(strings.ToLower(ifc.ID), "wireguard") {
			continue
		}
		out = append(out, platform.SlotInfo{
			ID:          ifc.ID,
			Device:      DeviceName(ifc.ID),
			Description: ifc.Description,
			LinkUp:      ifc.Link == "up",
			Connected:   ifc.Connected == "yes",
			Address:     cidrOf(ifc.Address, ifc.Mask),
		})
	}
	sortSlots(out)
	return out
}

func sortSlots(out []platform.SlotInfo) {
	sort.Slice(out, func(i, j int) bool {
		return slotNum(out[i].ID) < slotNum(out[j].ID)
	})
}

func slotNum(id string) int {
	if m := slotRe.FindStringSubmatch(id); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 1 << 30
}

func slotInfosList(list []rciInterface) []platform.SlotInfo {
	var out []platform.SlotInfo
	for _, ifc := range list {
		if !strings.HasPrefix(strings.ToLower(ifc.ID), "wireguard") {
			continue
		}
		out = append(out, platform.SlotInfo{
			ID:          ifc.ID,
			Device:      DeviceName(ifc.ID),
			Description: ifc.Description,
			LinkUp:      ifc.Link == "up",
			Connected:   ifc.Connected == "yes",
		})
	}
	return out
}

// SaveConfig сбрасывает running-config в сохраненный: веб-интерфейс
// Keenetic показывает состояние подключений из сохраненного конфига, без
// сохранения его тумблеры врут после CLI-изменений.
func (b *Backend) SaveConfig() error {
	_, err := b.ndmc("system configuration save")
	return err
}

// CreateSlot создает следующий слот WireguardN голой CLI-командой (грабли
// №3: сначала создание, потом подккоманды) и возвращает его id.
func (b *Backend) CreateSlot() (string, error) {
	slots, err := b.Slots()
	if err != nil {
		return "", err
	}
	max := -1
	for _, sl := range slots {
		if m := slotRe.FindStringSubmatch(sl.ID); m != nil {
			if n, e := strconv.Atoi(m[1]); e == nil && n > max {
				max = n
			}
		}
	}
	id := fmt.Sprintf("Wireguard%d", max+1)
	if _, err := b.ndmc("interface " + id); err != nil {
		return "", err
	}
	return id, nil
}

func DeviceName(slot string) string {
	m := slotRe.FindStringSubmatch(slot)
	if m == nil {
		return slot
	}
	return "nwg" + m[1]
}

func (b *Backend) Apply(pool store.Pool, cfg wgconf.Config) error {
	slot, _, err := b.slotOf(pool)
	if err != nil {
		return err
	}
	current, err := b.rciGet("/rci/show/interface/" + slot)
	if err == nil {
		for _, p := range current.Wireguard.Peers {
			if p.PublicKey == "" || p.PublicKey == cfg.Peer.PublicKey {
				continue
			}
			if _, err := b.ndmc("interface " + slot + " no wireguard peer " + p.PublicKey); err != nil {
				return err
			}
		}
	}
	steps := []string{
		"interface " + slot,
		"interface " + slot + " description " + pool.Name,
		"interface " + slot + " security-level public",
		"interface " + slot + " wireguard private-key " + cfg.PrivateKey,
	}
	if cfg.AWG.Present() {
		args := cfg.AWG.AscArgs()
		steps = append(steps, "interface "+slot+" wireguard asc "+strings.Join(args, " "))
	} else {
		steps = append(steps, "interface "+slot+" no wireguard asc")
	}
	if cfg.MTU > 0 {
		steps = append(steps,
			"interface "+slot+" ip mtu "+strconv.Itoa(cfg.MTU),
			"interface "+slot+" ip tcp adjust-mss pmtu",
		)
	}
	if v4 := cfg.FirstIPv4(); v4 != "" {
		if !strings.Contains(v4, "/") {
			v4 += "/32"
		}
		steps = append(steps,
			"interface "+slot+" no ip address",
			"interface "+slot+" ip address "+v4,
		)
	}
	keepalive := pool.Settings.Keepalive
	if cfg.Peer.PersistentKeepalive > 0 {
		keepalive = cfg.Peer.PersistentKeepalive
	}
	peer := "interface " + slot + " wireguard peer " + cfg.Peer.PublicKey
	steps = append(steps, peer)
	steps = append(steps,
		peer+" allow-ips 0.0.0.0/0",
		peer+" endpoint "+cfg.Endpoint(),
	)
	for _, ip := range cfg.Peer.AllowedIPs {
		if strings.HasPrefix(ip, "::") {
			steps = append(steps, peer+" allow-ips "+ip)
			break
		}
	}
	if cfg.Peer.PresharedKey != "" {
		steps = append(steps, peer+" preshared-key "+cfg.Peer.PresharedKey)
	}
	if keepalive > 0 {
		steps = append(steps, peer+" keepalive-interval "+strconv.Itoa(keepalive))
	}
	steps = append(steps, "interface "+slot+" up")
	if store.ValidProbeHost(pool.Settings.ProbeHost) {
		steps = append(steps, "ip route "+pool.Settings.ProbeHost+" 255.255.255.255 "+slot+" auto")
	}
	steps = append(steps, "system configuration save")
	for _, s := range steps {
		if _, err := b.ndmc(s); err != nil {
			return err
		}
	}
	return nil
}

func (b *Backend) Up(pool store.Pool) error {
	slot, _, err := b.slotOf(pool)
	if err != nil {
		return err
	}
	_, err = b.ndmc("interface " + slot + " up")
	return err
}

func (b *Backend) Down(pool store.Pool) error {
	slot, _, err := b.slotOf(pool)
	if err != nil {
		return err
	}
	_, err = b.ndmc("interface " + slot + " down")
	return err
}

func (b *Backend) Status(pool store.Pool) (platform.TunnelStatus, error) {
	var st platform.TunnelStatus
	slot, _, err := b.slotOf(pool)
	if err != nil {
		return st, err
	}
	iface, err := b.rciGet("/rci/show/interface/" + slot)
	if err != nil {
		return st, err
	}
	st.LinkUp = iface.Link == "up" && iface.Connected == "yes"
	st.HandshakeAgo = -1
	for _, p := range iface.Wireguard.Peers {
		if !p.Enabled || p.LastHandshake <= 0 || p.LastHandshake > 86400*365 {
			continue
		}
		if st.HandshakeAgo < 0 || int(p.LastHandshake) < st.HandshakeAgo {
			st.HandshakeAgo = int(p.LastHandshake)
			st.Connected = p.Online
		}
	}
	if !st.LinkUp {
		st.Connected = false
	}
	return st, nil
}

var rttRe = regexp.MustCompile(`(?:rtt|round-trip) min/avg/max(?:/(?:mdev|stddev))? = ([\d.]+)/([\d.]+)/`)

func pingRTT(out string) int {
	if m := rttRe.FindStringSubmatch(out); m != nil {
		if ms, err := strconv.ParseFloat(m[2], 64); err == nil {
			return int(ms)
		}
	}
	return 0
}

func (b *Backend) Probe(pool store.Pool, host string) (bool, int, error) {
	slot, _, err := b.slotOf(pool)
	if err != nil {
		return false, 0, err
	}
	cmd := exec.Command("ping", "-I", DeviceName(slot), "-c", "3", "-W", "3", host)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, 0, nil
	}
	return true, pingRTT(string(out)), nil
}

func (b *Backend) ProbeDevice(device, target string) (bool, int) {
	cmd := exec.Command("ping", "-I", device, "-c", "1", "-W", "2", target)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, 0
	}
	return true, pingRTT(string(out))
}

var devRe = regexp.MustCompile(`^nwg(\d+)$`)

func slotFromDevice(device string) string {
	m := devRe.FindStringSubmatch(device)
	if m == nil {
		return ""
	}
	return "Wireguard" + m[1]
}

var sysTunnelRe = regexp.MustCompile(`^(tun|tap|wg|awg|nwg)[0-9]+$`)

func (b *Backend) SysTunnels() ([]platform.SlotInfo, error) {
	return sysTunnelsImpl()
}

func sysTunnelsImpl() ([]platform.SlotInfo, error) {

	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return nil, err
	}
	var out []platform.SlotInfo
	for _, e := range entries {
		name := e.Name()
		if !sysTunnelRe.MatchString(name) && !slotRe.MatchString(strings.ToLower(name)) {
			continue
		}
		linkUp := linkUpFlag(name)
		out = append(out, platform.SlotInfo{Device: name, LinkUp: linkUp, Address: ifaceAddrCIDR(name)})
	}
	return out, nil
}

// linkUpFlag: IFF_UP из flags (значение в hex: 0x1...).
func linkUpFlag(device string) bool {
	flags, err := os.ReadFile("/sys/class/net/" + device + "/flags")
	if err != nil {
		return false
	}
	t := strings.TrimSpace(string(flags))
	return strings.HasPrefix(t, "0x1") || strings.HasPrefix(t, "1")
}

// ifaceAddrCIDR - первый IPv4 с маской ("10.2.0.2/32").
func ifaceAddrCIDR(device string) string {
	out, err := exec.Command("ip", "-4", "-o", "addr", "show", "dev", device).CombinedOutput()
	if err != nil {
		return ""
	}
	for _, field := range strings.Fields(string(out)) {
		if strings.Contains(field, "/") && net.ParseIP(strings.SplitN(field, "/", 2)[0]) != nil {
			return field
		}
	}
	return ""
}

func (b *Backend) IfaceHandshake(device string) int {
	slot := slotFromDevice(device)
	if slot == "" {
		return -1
	}
	iface, err := b.rciGet("/rci/show/interface/" + slot)
	if err != nil {
		return -1
	}
	best := -1
	for _, p := range iface.Wireguard.Peers {
		if !p.Enabled || p.LastHandshake <= 0 || p.LastHandshake > 86400*365 {
			continue
		}
		if best < 0 || int(p.LastHandshake) < best {
			best = int(p.LastHandshake)
		}
	}
	return best
}

func (b *Backend) RestartMagitrickle() error {
	cmd := exec.Command("/bin/sh", "-c", "/opt/etc/init.d/S99magitrickle restart")
	return cmd.Run()
}
