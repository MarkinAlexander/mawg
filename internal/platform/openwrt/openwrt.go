package openwrt

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"mawg/internal/platform"
	"mawg/internal/store"
	"mawg/internal/wgconf"
)

var (
	ifaceRe    = regexp.MustCompile(`^network\.([A-Za-z0-9_]+)=interface$`)
	protoRe    = regexp.MustCompile(`^network\.([A-Za-z0-9_]+)\.proto='(.+)'$`)
	zoneNetRe  = regexp.MustCompile(`^firewall\.@zone\[(\d+)\]\.network='(.*)'$`)
	zoneTypeRe = regexp.MustCompile(`^firewall\.@zone\[(\d+)\]=zone$`)
	zoneNameRe = regexp.MustCompile(`^firewall\.@zone\[(\d+)\]\.name='(.+)'$`)
)

const routerPath = "/sbin:/usr/sbin:/bin:/usr/bin"

type Backend struct{}

func New() *Backend { return &Backend{} }

func (b *Backend) Name() string { return store.PlatformOpenwrt }

// SupportsNativeAWG3 - kmod amneziawg 3.x поднимает AWG 3.x-конфиги
// (HP-ключ, таймеры) нативно: free-пулам на OpenWrt движок не нужен.
// Определяем по версии awg-tools (awg --version), кэш на час.
var (
	nawg3Mu   sync.Mutex
	nawg3At   time.Time
	nawg3Prev bool
)

func (b *Backend) SupportsNativeAWG3() bool {
	nawg3Mu.Lock()
	defer nawg3Mu.Unlock()
	if !nawg3At.IsZero() && time.Since(nawg3At) < time.Hour {
		return nawg3Prev
	}
	nawg3At = time.Now()
	out, err := run("awg", "--version")
	// вывод: «amneziawg-tools v3.1.20260812 - https://amnezia.org» -
	// версия идёт с префиксом v (1.x/2.x так же)
	v := strings.Fields(out)
	nawg3Prev = err == nil && len(v) >= 2 && strings.HasPrefix(strings.TrimPrefix(v[1], "v"), "3.")
	return nawg3Prev
}

func (b *Backend) Detect() error {
	if _, err := os.Stat("/etc/openwrt_release"); err != nil {
		return fmt.Errorf("это не OpenWrt")
	}
	if _, err := run("uci", "show", "network"); err != nil {
		return fmt.Errorf("uci недоступен: %v", err)
	}
	return nil
}

func prep(cmd *exec.Cmd) *exec.Cmd {
	if os.Getenv("PATH") == "" {
		cmd.Env = []string{"PATH=" + routerPath}
	}
	return cmd
}

func run(name string, args ...string) (string, error) {
	// uci/ip/wg/ifup в норме укладываются в секунды; потолок держит
	// повисший на битом интерфейсе netlink-запрос от вечного накопления
	return platform.RunBoundedOutput(prep(exec.Command(name, args...)), 30*time.Second)
}

func runShell(script string) (string, error) {
	return platform.RunBoundedCombined(prep(exec.Command("/bin/sh", "-c", script)), 60*time.Second)
}

func (b *Backend) Slots() ([]platform.SlotInfo, error) {
	show, err := run("uci", "-q", "show", "network")
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	protoByName := map[string]string{}
	for _, line := range strings.Split(show, "\n") {
		if m := ifaceRe.FindStringSubmatch(line); m != nil {
			known[m[1]] = true
			continue
		}
		if m := protoRe.FindStringSubmatch(line); m != nil && known[m[1]] {
			protoByName[m[1]] = m[2]
		}
	}
	var out []platform.SlotInfo
	for name, proto := range protoByName {
		if proto != "wireguard" && proto != "amneziawg" {
			continue
		}
		info := platform.SlotInfo{ID: name, Device: name, Description: proto}
		linkOut, _ := run("ip", "-o", "link", "show", "dev", name)
		info.LinkUp = strings.Contains(linkOut, ",UP,") && strings.Contains(linkOut, "LOWER_UP")
		info.Address = ifaceAddr(name)
		out = append(out, info)
	}
	return out, nil
}

func (b *Backend) Apply(pool store.Pool, cfg wgconf.Config) error {
	name := pool.DeviceName()
	proto := pool.Settings.OpenwrtProto
	if proto == "" {
		proto = "wireguard"
	}
	if out, err := runShell(dropSectionsScript(name)); err != nil {
		return fmt.Errorf("uci drop: %v: %s", err, out)
	}
	ifRef := "network." + name + "."
	peerType := proto + "_" + name
	var batch []string
	batch = append(batch,
		"set network."+name+"=interface",
		"set "+ifRef+"proto='"+proto+"'",
		// авто-поднятие: Down ставит auto='0', иначе netifd сам поднимет
		// туннель на загрузке роутера - пул выключен/пуст, а адрес занят
		"set "+ifRef+"auto='1'",
		"set "+ifRef+"private_key='"+cfg.PrivateKey+"'",
	)
	for _, a := range cfg.Addresses {
		batch = append(batch, "add_list "+ifRef+"addresses='"+a+"'")
	}
	for _, d := range cfg.DNS {
		batch = append(batch, "add_list "+ifRef+"dns='"+d+"'")
	}
	if cfg.MTU > 0 {
		batch = append(batch, "set "+ifRef+"mtu='"+strconv.Itoa(cfg.MTU)+"'")
	}
	if proto == "amneziawg" {
		batch = append(batch, awgUci(ifRef, cfg.AWG)...)
	}
	batch = append(batch, "add network "+peerType)
	peerRef := "network.@" + peerType + "[-1]."
	batch = append(batch,
		"set "+peerRef+"public_key='"+cfg.Peer.PublicKey+"'",
		"set "+peerRef+"endpoint_host='"+cfg.Peer.EndpointHost+"'",
		"set "+peerRef+"endpoint_port='"+strconv.Itoa(cfg.Peer.EndpointPort)+"'",
		"set "+peerRef+"route_allowed_ip='0'",
	)
	if cfg.Peer.PresharedKey != "" {
		batch = append(batch, "set "+peerRef+"preshared_key='"+cfg.Peer.PresharedKey+"'")
	}
	for _, ip := range cfg.Peer.AllowedIPs {
		batch = append(batch, "add_list "+peerRef+"allowed_ips='"+ip+"'")
	}
	keepalive := pool.Settings.Keepalive
	if cfg.Peer.PersistentKeepalive > 0 {
		keepalive = cfg.Peer.PersistentKeepalive
	}
	if cfg.Peer.KeepaliveRange != "" {
		// AWG 3.x: диапазон «min-max» у uci не поддерживается прото-
		// скриптом - берём минимум диапазона (сервер допускает)
		if lo := strings.SplitN(cfg.Peer.KeepaliveRange, "-", 2)[0]; lo != "" {
			if n, err := strconv.Atoi(lo); err == nil {
				keepalive = n
			}
		}
	}
	if keepalive > 0 {
		batch = append(batch, "set "+peerRef+"persistent_keepalive='"+strconv.Itoa(keepalive)+"'")
	}
	batch = append(batch, probeRoute(pool.Settings.ProbeHost, name)...)
	script := "uci -q batch <<'EOF'\n" + strings.Join(batch, "\n") + "\ncommit network\nEOF"
	if out, err := runShell(script); err != nil {
		return fmt.Errorf("uci: %v: %s", err, out)
	}
	if err := b.ensureZone(name); err != nil {
		return err
	}
	if _, err := run("ifup", name); err != nil {
		return fmt.Errorf("ifup: %v", err)
	}
	return nil
}

func dropSectionsScript(name string) string {
	return `
n=$(uci show network 2>/dev/null | grep -c '=route$'); i=0
while [ "$i" -lt "$n" ]; do
  if [ "$(uci -q get network.@route[$i].interface 2>/dev/null)" = "` + name + `" ]; then
    uci -q delete network.@route[$i]; n=$((n-1))
  else
    i=$((i+1))
  fi
done
while uci -q delete network.@wireguard_` + name + `[0] 2>/dev/null; do :; done
while uci -q delete network.@amneziawg_` + name + `[0] 2>/dev/null; do :; done
uci -q delete network.` + name + `
uci -q commit network
`
}

func probeRoute(probeHost, name string) []string {
	if !store.ValidProbeHost(probeHost) {
		return nil
	}
	return []string{
		"add network route",
		"set network.@route[-1].interface='" + name + "'",
		"set network.@route[-1].target='" + probeHost + "'",
		"set network.@route[-1].netmask='255.255.255.255'",
	}
}

func awgUci(ifRef string, p wgconf.AWGParams) []string {
	var out []string
	setStr := func(key string, v *string) {
		if v != nil {
			out = append(out, "set "+ifRef+"awg_"+key+"='"+*v+"'")
		}
	}
	setStr("jc", p.Jc)
	setStr("jmin", p.Jmin)
	setStr("jmax", p.Jmax)
	setStr("s1", p.S1)
	setStr("s2", p.S2)
	setStr("s3", p.S3)
	setStr("s4", p.S4)
	setStr("h1", p.H1)
	setStr("h2", p.H2)
	setStr("h3", p.H3)
	setStr("h4", p.H4)
	setStr("i1", p.I1)
	setStr("i2", p.I2)
	setStr("i3", p.I3)
	setStr("i4", p.I4)
	setStr("i5", p.I5)
	// AWG 3.x: proto-скрипт luci-proto-amneziawg 3.1 умеет эти ключи;
	// без них (в частности awg_header_protection_key) kmod шлёт пакеты
	// без защиты заголовка и сервер не отвечает - интерфейс «жив»,
	// а хендшейка нет
	setStr("header_protection_key", p.HeaderProtectionKey)
	setStr("content_padding_addition", p.ContentPaddingAddition)
	setStr("rekey_after_time", p.RekeyAfterTime)
	setStr("rekey_timeout", p.RekeyTimeout)
	setStr("reject_after_time", p.RejectAfterTime)
	setStr("keepalive_timeout", p.KeepaliveTimeout)
	setStr("max_handshake_attempts", p.MaxHandshakeAttempts)
	return out
}

func (b *Backend) ensureZone(iface string) error {
	if _, err := run("uci", "-q", "show", "firewall"); err != nil {
		return nil
	}
	if err := ensureMawgZone(iface); err != nil {
		return err
	}
	out, err := run("fw4", "reload")
	if err != nil {
		if _, err2 := run("/etc/init.d/firewall", "restart"); err2 != nil {
			return fmt.Errorf("firewall reload: %v: %s", err, out)
		}
	}
	return nil
}

func ensureMawgZone(iface string) error {
	fw, err := run("uci", "-q", "show", "firewall")
	if err != nil {
		return nil
	}
	zoneIdx := -1
	inMawgZone := false
	for _, line := range strings.Split(fw, "\n") {
		if m := zoneNameRe.FindStringSubmatch(line); m != nil && m[2] == "mawg" {
			idx, _ := strconv.Atoi(m[1])
			zoneIdx = idx
		}
		if zoneIdx >= 0 {
			if m := zoneNetRe.FindStringSubmatch(line); m != nil {
				idx, _ := strconv.Atoi(m[1])
				if idx == zoneIdx {
					for _, net := range strings.Split(strings.Trim(m[2], "'"), " ") {
						if net == iface {
							inMawgZone = true
						}
					}
				}
			}
		}
	}
	var batch []string
	if zoneIdx < 0 {
		batch = append(batch,
			"add firewall zone",
			"set firewall.@zone[-1].name='mawg'",
			"set firewall.@zone[-1].input='ACCEPT'",
			"set firewall.@zone[-1].output='ACCEPT'",
			"set firewall.@zone[-1].forward='ACCEPT'",
			"set firewall.@zone[-1].masq='1'",
			"set firewall.@zone[-1].mtu_fix='1'",
		)
	}
	if !strings.Contains(fw, "src='lan'") || !strings.Contains(fw, "dest='mawg'") {
		batch = append(batch,
			"add firewall forwarding",
			"set firewall.@forwarding[-1].src='lan'",
			"set firewall.@forwarding[-1].dest='mawg'",
		)
	}
	if !inMawgZone {
		ref := "firewall.@zone[-1].network"
		if zoneIdx >= 0 {
			ref = "firewall.@zone[" + strconv.Itoa(zoneIdx) + "].network"
		}
		batch = append(batch, "add_list "+ref+"='"+iface+"'")
	}
	if len(batch) == 0 {
		return nil
	}
	script := "uci -q batch <<'EOF'\n" + strings.Join(batch, "\n") + "\ncommit firewall\nEOF"
	if out, err := runShell(script); err != nil {
		return fmt.Errorf("uci firewall: %v: %s", err, out)
	}
	return nil
}

func (b *Backend) Up(pool store.Pool) error {
	_, err := run("ifup", pool.DeviceName())
	return err
}

func (b *Backend) Down(pool store.Pool) error {
	name := pool.DeviceName()
	// auto='0' - иначе netifd поднимет туннель сам на загрузке роутера:
	// пул выключен/без конфигов, а интерфейс живёт и держит адрес,
	// блокируя другие пулы с тем же адресом (клоны warp-конфигов).
	// Best effort: секции может не быть (пул ни разу не применялся).
	_, _ = runShell("uci -q batch <<'EOF'\nset network." + name + ".auto='0'\ncommit network\nEOF")
	_, err := run("ifdown", name)
	return err
}

func (b *Backend) toolFor(pool store.Pool) string {
	if pool.Settings.OpenwrtProto == "amneziawg" {
		for _, c := range []string{"/usr/bin/awg", "/usr/bin/amneziawg"} {
			if _, err := os.Stat(c); err == nil {
				return c
			}
		}
	}
	return "wg"
}

func (b *Backend) Status(pool store.Pool) (platform.TunnelStatus, error) {
	var st platform.TunnelStatus
	linkOut, err := run("ip", "-o", "link", "show", "dev", pool.DeviceName())
	if err != nil {
		return st, fmt.Errorf("интерфейс %s не найден", pool.DeviceName())
	}
	st.LinkUp = strings.Contains(linkOut, ",UP,") && strings.Contains(linkOut, "LOWER_UP")
	out, err := run(b.toolFor(pool), "show", pool.DeviceName(), "latest-handshakes")
	st.HandshakeAgo = -1
	if err == nil {
		best := int64(-1)
		for _, field := range strings.Fields(out) {
			if ts, e := strconv.ParseInt(field, 10, 64); e == nil && ts > 0 {
				ago := time.Now().Unix() - ts
				if best < 0 || ago < best {
					best = ago
				}
			}
		}
		if best >= 0 {
			st.HandshakeAgo = int(best)
			st.Connected = st.LinkUp && best < 180
		}
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
	cmd := prep(exec.Command("ping", "-I", pool.DeviceName(), "-c", "3", "-W", "3", host))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, 0, nil
	}
	return true, pingRTT(string(out)), nil
}

func (b *Backend) ProbeDevice(device, target string) (bool, int) {
	cmd := prep(exec.Command("ping", "-I", device, "-c", "1", "-W", "2", target))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, 0
	}
	return true, pingRTT(string(out))
}

var sysTunnelRe = regexp.MustCompile(`^(tun|tap|wg|awg)[0-9]+$`)

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
		if !sysTunnelRe.MatchString(name) {
			continue
		}
		linkUp := false
		if flags, err := os.ReadFile("/sys/class/net/" + name + "/flags"); err == nil {
			t := strings.TrimSpace(string(flags))
			linkUp = strings.HasPrefix(t, "0x1") || strings.HasPrefix(t, "1")
		}
		out = append(out, platform.SlotInfo{Device: name, LinkUp: linkUp, Address: ifaceAddr(name)})
	}
	return out, nil
}

func parseHandshakes(out string) int {
	best := int64(-1)
	for _, field := range strings.Fields(out) {
		if ts, e := strconv.ParseInt(field, 10, 64); e == nil && ts > 0 {
			ago := time.Now().Unix() - ts
			if best < 0 || ago < best {
				best = ago
			}
		}
	}
	return int(best)
}

func (b *Backend) IfaceHandshake(device string) int {
	for _, tool := range []string{"wg", "/usr/bin/awg", "/usr/bin/amneziawg"} {
		out, err := run(tool, "show", device, "latest-handshakes")
		if err != nil {
			continue
		}
		if best := parseHandshakes(out); best >= 0 {
			return best
		}
	}
	return -1
}

func (b *Backend) RestartMagitrickle() error {
	_, err := runShell("/etc/init.d/magitrickle restart")
	return err
}

// SaveConfig: mawg коммитит uci сразу в Apply/Up/Down, LuCI видит живое
// состояние; отдельное сохранение не нужно.
func (b *Backend) SaveConfig() error { return nil }

// ifaceAddr - первый IPv4 с маской интерфейса (10.2.0.2/32).
func ifaceAddr(device string) string {
	out, err := run("ip", "-4", "-o", "addr", "show", "dev", device)
	if err != nil {
		return ""
	}
	for _, field := range strings.Fields(out) {
		if net.ParseIP(strings.SplitN(field, "/", 2)[0]) != nil && strings.Contains(field, "/") {
			return field
		}
	}
	return ""
}
