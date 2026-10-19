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
// не настоящая даже 3.0»): никаких 3.x-полей (HP-ключ, таймеры, L1),
// вариант - для ВНЕШНИХ клиентов AmneziaWG (Windows и пр.), в нативные
// интерфейсы Кинетиков его не импортируем.
var awgFields = [][2]string{
	{"Jc", "4"}, {"Jmin", "40"}, {"Jmax", "70"},
	{"S1", "0"}, {"S2", "0"}, {"S3", "0"}, {"S4", "0"},
	{"H1", "1"}, {"H2", "2"}, {"H3", "3"}, {"H4", "4"},
}

// warpInit1/warpInit2 - стандартные init-пакеты WARP (SIP INVITE / 100
// Trying, RFC 3261-пример в hex): «Собственный I1» warp-generator, поле
// ровно из спецификации AWG 2.0 (слово владельца: «посмотри те же конфиги
// варпа и протона что есть - они все с этими полями»). Рабочие конфиги
// warp в дикой природе несут ровно эти блобы. Значения - общедоступные
// константы, извлечены экстрактором из script.js warp-generator.
const warpInit1 = "I1 = <b 0x494e56495445207369703a626f624062696c6f78692e636f6d205349502f322e300d0a5669613a205349502f322e302f55445020706333332e61746c616e74612e636f6d3b6272616e63683d7a39684734624b3737366173646864730d0a4d61782d466f7277617264733a2037300d0a546f3a20426f62203c7369703a626f624062696c6f78692e636f6d3e0d0a46726f6d3a20416c696365203c7369703a616c6963654061746c616e74612e636f6d3e3b7461673d313932383330313737340d0a43616c6c2d49443a20613834623463373665363637313040706333332e61746c616e74612e636f6d0d0a435365713a2033313431353920494e564954450d0a436f6e746163743a203c7369703a616c69636540706333332e61746c616e74612e636f6d3e0d0a436f6e74656e742d547970653a206170706c69636174696f6e2f7364700d0a436f6e74656e742d4c656e6774683a20300d0a0d0a>"
const warpInit2 = "I2 = <b 0x5349502f322e302031303020547279696e670d0a5669613a205349502f322e302f55445020706333332e61746c616e74612e636f6d3b6272616e63683d7a39684734624b3737366173646864730d0a546f3a20426f62203c7369703a626f624062696c6f78692e636f6d3e0d0a46726f6d3a20416c696365203c7369703a616c6963654061746c616e74612e636f6d3e3b7461673d313932383330313737340d0a43616c6c2d49443a20613834623463373665363637313040706333332e61746c616e74612e636f6d0d0a435365713a2033313431353920494e564954450d0a436f6e74656e742d4c656e6774683a20300d0a0d0a>"

// WithAWGFields - тот же конфиг с AWG-полями в [Interface]: доп. опция
// скачивания для внешних клиентов (слово владельца: «бывает удобнее
// создать у нас и использовать в своём клиенте, например для Windows»).
// Вставка сразу после MTU (его конфиг всегда несёт); без MTU - после
// Address; конфиг не нашего формата - без изменений (не ломаем чужое).
func WithAWGFields(conf []byte) []byte {
	// Конфиг уже несёт обфускацию (ручной импорт warp-конфига) -
	// отдаём как есть: никаких дублей полей, ничего не выдумываем
	// (вопрос владельца: «ровно тот, что загружен, лишнего ничего?»).
	for _, ln := range strings.Split(string(conf), "\n") {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "Jc = ") || strings.HasPrefix(t, "I1 = ") {
			return conf
		}
	}
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
			out = append(out, warpInit1, warpInit2)
			inserted = true
		}
	}
	if !inserted {
		return conf
	}
	return []byte(strings.Join(out, "\n"))
}
