package warp

import (
	"strconv"
	"strings"
)

// Сборка простого WireGuard-конфига WARP. Подход и структура - как у
// warp-generator (script.js generateWireGuardConfig, ~573), реализация
// своя: без DNS и полей обфускации - «простые конфиги» (слово владельца).
// MTU 1280 - рекомендация Cloudflare против фрагментации, keepalive 25 -
// дефолт warp-generator/LxBox.

const (
	DefaultMTU       = 1280
	DefaultKeepalive = 25
)

// BuildConfig - текст конфига: v4-адрес из ответа регистрации, пир -
// Cloudflare из того же ответа. allowedIPs пустым не бывает (AllowedIPs
// ставит дефолт).
func BuildConfig(private, addressV4, peerPublic, endpoint string, allowedIPs []string) []byte {
	var b strings.Builder
	b.WriteString("[Interface]\n")
	b.WriteString("PrivateKey = " + private + "\n")
	b.WriteString("Address = " + addressV4 + "\n")
	b.WriteString("MTU = " + strconv.Itoa(DefaultMTU) + "\n")
	b.WriteString("\n[Peer]\n")
	b.WriteString("PublicKey = " + peerPublic + "\n")
	b.WriteString("AllowedIPs = " + strings.Join(allowedIPs, ", ") + "\n")
	b.WriteString("Endpoint = " + endpoint + "\n")
	b.WriteString("PersistentKeepalive = " + strconv.Itoa(DefaultKeepalive) + "\n")
	return []byte(b.String())
}

// awgFields - прозрачная обфускация warp-generator/LxBox (amneziaPreset):
// пакеты с этими полями бит-в-бит идентичны обычному WireGuard, Cloudflare
// принимает их как есть. УРОВЕНЬ AWG 2.0 и только (слово владельца:
// «обфускацию мы делаем под авг2.0, выше смысла нет - всё равно там будет
// не настоящая даже 3.0»): никаких 3.x-полей (HP-ключ, таймеры), вариант
// - для ВНЕШНИХ клиентов AmneziaWG (Windows и пр.), в нативные
// интерфейсы Кинетиков его не импортируем.
var awgFields = [][2]string{
	{"Jc", "4"}, {"Jmin", "40"}, {"Jmax", "70"},
	{"S1", "0"}, {"S2", "0"}, {"S3", "0"}, {"S4", "0"},
	{"H1", "1"}, {"H2", "2"}, {"H3", "3"}, {"H4", "4"},
}

// WithAWGFields - тот же конфиг с AWG-полями в [Interface]: доп. опция
// скачивания для внешних клиентов (слово владельца: «бывает удобнее
// создать у нас и использовать в своём клиенте, например для Windows»).
// Вставка сразу после MTU (его конфиг всегда несёт); без MTU - после
// Address; конфиг не нашего формата - без изменений (не ломаем чужое).
func WithAWGFields(conf []byte) []byte {
	lines := strings.Split(string(conf), "\n")
	out := make([]string, 0, len(lines)+len(awgFields))
	inserted := false
	for _, ln := range lines {
		out = append(out, ln)
		if inserted {
			continue
		}
		trim := strings.TrimSpace(ln)
		isMTU := strings.HasPrefix(trim, "MTU = ")
		isAddr := strings.HasPrefix(trim, "Address = ")
		if !isMTU && !isAddr {
			continue
		}
		// Смотрим, что дальше: если MTU стоит последним в [Interface],
		// поля всё равно встанут до пустой строки/[Peer] - не портят.
		next := ""
		if i := len(out); i < len(lines) {
			next = strings.TrimSpace(lines[i])
		}
		if isMTU || (isAddr && (next == "" || strings.HasPrefix(next, "["))) {
			for _, f := range awgFields {
				out = append(out, f[0]+" = "+f[1])
			}
			inserted = true
		}
	}
	if !inserted {
		return conf
	}
	return []byte(strings.Join(out, "\n"))
}
