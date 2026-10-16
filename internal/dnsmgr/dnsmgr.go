// Package dnsmgr - DNS-менеджер mawg (46-док §3, срез OpenWrt 64-док):
// состояние DNS-стека роутера и минимальные действия. Живучий профиль
// пресета (63-док): dnsmasq → MT:3553 БЕЗ noresolv/cachesize/resolvfile -
// интерфейсный upstream остаётся параллельным, смерть MT = деградация,
// не смерть DNS. Форкоп-стиль (noresolv=1+cachesize=0) распознаётся и
// показывается как строгий режим (SPOF ядра).
package dnsmgr

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Runner - ограниченный шелл платформы (platform.ShellRunner).
type Runner func(script string, timeout time.Duration) (string, error)

// MTListen - куда указывает dnsmasq в режиме «в MT» (dns-proxy :3553).
const MTListen = "127.0.0.1#3553"

// uciBakKey - куда прячется прядевой список серверов на время режима MT.
const uciBakKey = "dhcp.@dnsmasq[0].mawg_server_bak"

type State struct {
	// Platform - "openwrt" | "keenetic": панель ветвит разрез карточки
	// (71-док: на Keenetic свои серверы роутера не трогаются).
	Platform string  `json:"platform"`
	Dnsmasq  Dnsmasq `json:"dnsmasq"`
	MT       MTProxy `json:"mt"`
}

// Платформы DNS-стека (значения совпадают с store.Platform*).
const (
	PlatformOpenWrt  = "openwrt"
	PlatformKeenetic = "keenetic"
)

type Dnsmasq struct {
	Servers   []string `json:"servers"` // dhcp.@dnsmasq[0].server; пусто = из resolvfile
	InMT      bool     `json:"inMT"`    // среди серверов есть MTListen
	NoResolv  bool     `json:"noresolv"`
	CacheSize *int     `json:"cacheSize,omitempty"`
	// Strict - форкоп-стиль (noresolv=1 или cachesize=0): смерть апстрима
	// роняет DNS целиком (SPOF), 63-док.
	Strict bool `json:"strict"`
}

type MTProxy struct {
	Upstream  string   `json:"upstream"`            // legacy одиночный (host:port)
	Upstreams []string `json:"upstreams,omitempty"` // список приоритета (rev6 multi-upstream, 64-док)
	Remap53   bool     `json:"remap53"`             // цепочка MT_DNSOR живёт (:53 → MT)
}

// Collect собирает состояние DNS-стека (openwrt: uci + iptables + файл
// конфига MT; mtConfigPath пуст или не читается - апстримы неизвестны).
func Collect(r Runner, mtConfigPath string) (State, error) {
	st := State{Platform: PlatformOpenWrt}

	srvOut, _ := r(`uci -q show dhcp.@dnsmasq[0].server`, 10*time.Second)
	st.Dnsmasq.Servers = parseUCIValues(srvOut)
	for _, s := range st.Dnsmasq.Servers {
		if s == MTListen {
			st.Dnsmasq.InMT = true
		}
	}
	if v, err := r(`uci -q get dhcp.@dnsmasq[0].noresolv`, 10*time.Second); err == nil {
		st.Dnsmasq.NoResolv = strings.TrimSpace(v) == "1"
	}
	if v, err := r(`uci -q get dhcp.@dnsmasq[0].cachesize`, 10*time.Second); err == nil {
		if n, perr := strconv.Atoi(strings.TrimSpace(v)); perr == nil {
			st.Dnsmasq.CacheSize = &n
		}
	}
	st.Dnsmasq.Strict = st.Dnsmasq.NoResolv || (st.Dnsmasq.CacheSize != nil && *st.Dnsmasq.CacheSize == 0)

	n, _ := r(`iptables -t nat -S MT_DNSOR 2>/dev/null | wc -l`, 10*time.Second)
	st.MT.Remap53 = strings.TrimSpace(n) != "0"

	if mtConfigPath != "" {
		if data, err := os.ReadFile(mtConfigPath); err == nil {
			st.MT.Upstream, st.MT.Upstreams = ParseMTUpstreams(string(data))
		}
	}
	return st, nil
}

// CollectKeenetic - состояние DNS-стека Keenetic (71-док, дизайн-слово
// владельца: собственные серверы Keenetic панель не читает и не меняет -
// управляемый слой один: MagiTrickle). Показываем факт перехвата
// MT_DNSOR (DNS роутера и устройств завёрнут в MT) и апстримы MT из
// дискового конфига; Dnsmasq остаётся нулевым.
func CollectKeenetic(r Runner, mtConfigPath string) (State, error) {
	st := State{Platform: PlatformKeenetic}
	n, _ := r(`iptables -t nat -S MT_DNSOR 2>/dev/null | wc -l`, 10*time.Second)
	st.MT.Remap53 = strings.TrimSpace(n) != "0"
	if mtConfigPath != "" {
		if data, err := os.ReadFile(mtConfigPath); err == nil {
			st.MT.Upstream, st.MT.Upstreams = ParseMTUpstreams(string(data))
		}
	}
	return st, nil
}

// parseUCIValues - значения uci-листа из вывода `uci show`: строки вида
// `dhcp.@dnsmasq[0].server='a' 'b'` (и старые `key+='b'` приращения).
func parseUCIValues(out string) []string {
	var vals []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		eq := strings.Index(line, "=")
		if eq < 0 {
			continue
		}
		for _, part := range strings.Split(line[eq+1:], "' '") {
			part = strings.Trim(strings.TrimSpace(part), "'")
			if part != "" {
				vals = append(vals, part)
			}
		}
	}
	return vals
}

// ParseMTUpstreams - мини-парсер config.yaml MT (yaml-депы mawg не тянет,
// формат блока стабилен): legacy upstream и список upstreams (host:port,
// приоритет = порядок).
func ParseMTUpstreams(data string) (legacy string, list []string) {
	inProxy := false
	sub := "" // "" | "upstream" | "upstreams"
	cur := ""
	for _, raw := range strings.Split(data, "\n") {
		line := strings.TrimRight(raw, " \t\r")
		if line == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		body := strings.TrimSpace(line)
		if indent <= 2 && body != "dnsProxy:" {
			if inProxy {
				break // блок dnsProxy кончился
			}
			continue
		}
		switch {
		case indent == 2 && body == "dnsProxy:":
			inProxy = true
		// элементы списка yaml.v2 висят на отступе ключа (`upstreams:\n
		//    - address: ...`), рукописный стиль - глубже; покрываем оба
		case inProxy && sub == "upstreams" && strings.HasPrefix(body, "- "):
			if h, ok := cutKey(strings.TrimPrefix(body, "- "), "address:"); ok {
				list = append(list, h)
			}
		case inProxy && sub == "upstreams" && indent > 4:
			if p, ok := cutKey(body, "port:"); ok && len(list) > 0 {
				list[len(list)-1] += ":" + p
			}
		case inProxy && indent == 4 && body == "upstream:":
			sub, cur = "upstream", ""
		case inProxy && indent == 4 && body == "upstreams:":
			sub = "upstreams"
		case inProxy && indent == 4 && sub != "":
			sub = "" // другой ключ dnsProxy
		case inProxy && sub == "upstream" && indent > 4:
			if h, ok := cutKey(body, "address:"); ok {
				cur = h
			}
			if p, ok := cutKey(body, "port:"); ok && cur != "" {
				legacy = cur + ":" + p
			}
		}
	}
	return legacy, list
}

// cutKey достаёт скаляр `key: value` (кавычки yaml снимаются).
func cutKey(body, key string) (string, bool) {
	if !strings.HasPrefix(body, key) {
		return "", false
	}
	v := strings.TrimSpace(strings.TrimPrefix(body, key))
	v = strings.Trim(v, `"'`)
	if v == "" {
		return "", false
	}
	return v, true
}

// SetDnsmasqTarget переключает dnsmasq: "mt" - сервер = MT:3553 (прядевой
// список прячется в uciBakKey), "direct" - рестор из uciBakKey (или чистый
// resolvfile-режим, если бэкапа нет). После смены - рестарт dnsmasq.
func SetDnsmasqTarget(r Runner, mode string) error {
	curOut, _ := r(`uci -q show dhcp.@dnsmasq[0].server`, 10*time.Second)
	cur := parseUCIValues(curOut)
	bakOut, _ := r(`uci -q show `+uciBakKey, 10*time.Second)
	bak := parseUCIValues(bakOut)

	var batch []string
	switch mode {
	case "mt":
		for _, s := range cur {
			if s == MTListen || contains(bak, s) {
				continue
			}
			batch = append(batch, fmt.Sprintf("add_list %s='%s'", uciBakKey, s))
		}
		batch = append(batch, "delete dhcp.@dnsmasq[0].server")
		batch = append(batch, fmt.Sprintf("add_list dhcp.@dnsmasq[0].server='%s'", MTListen))
	case "direct":
		batch = append(batch, "delete dhcp.@dnsmasq[0].server")
		for _, s := range bak {
			if s == MTListen {
				continue
			}
			batch = append(batch, fmt.Sprintf("add_list dhcp.@dnsmasq[0].server='%s'", s))
		}
		batch = append(batch, fmt.Sprintf("delete %s", uciBakKey))
	default:
		return fmt.Errorf("неизвестный режим %q", mode)
	}
	script := "uci -q batch <<'EOF'\n" + strings.Join(batch, "\n") + "\ncommit dhcp\nEOF\n/etc/init.d/dnsmasq restart"
	out, err := r(script, 60*time.Second)
	if err != nil {
		return fmt.Errorf("uci/рестарт dnsmasq: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
