package wgconf

import (
	"strconv"
	"strings"
)

// Уровни обфускации конфига и антиобфускация (слово владельца, 2026-10-19):
// «механизм антиобфускации чтобы голый получить, комментарии оставить, а
// всё что от АВГ - удалить». Кнопки скачивания в панели - по уровню:
// голый как WG -> можно отдать как AWG (добавить поля); AWG 2.0 -> можно
// отдать голым (снять поля); чистый AWG 3.0/3.1 -> до WG смысла нет
// (скорее всего не заведётся) - преобразований не предлагаем.

const (
	AwgPlain = "plain" // чистый WireGuard
	Awg2     = "awg2"  // обфускация 2.0: Jc/Jmin/Jmax, S1-S4, H1-H4, I1-I5
	Awg3     = "awg3"  // 3.x: HP-ключ, таймеры, L/J-параметры - в WG не переводим
)

// AwgLevelOf - уровень обфускации текста конфига ("", если не разобрать).
func AwgLevelOf(data []byte) string {
	cfg, err := Parse(data)
	if err != nil {
		return ""
	}
	return cfg.AwgLevel()
}

// AwgLevel - уровень обфускации разобранного конфига.
func (c Config) AwgLevel() string {
	if c.AWG.AWG3() {
		return Awg3
	}
	if c.AWG.Present() {
		return Awg2
	}
	return AwgPlain
}

// awgStripKeys - имена полей, составляющие обфускацию AmneziaWG: весь
// набор 2.0 плюс 3.x (HP-ключ, паддинг, таймеры, J1-J3, L1-L25).
var awgStripKeys = map[string]bool{
	"Jc": true, "Jmin": true, "Jmax": true,
	"S1": true, "S2": true, "S3": true, "S4": true,
	"H1": true, "H2": true, "H3": true, "H4": true,
	"I1": true, "I2": true, "I3": true, "I4": true, "I5": true,
	"J1": true, "J2": true, "J3": true,
	"HeaderProtectionKey":      true,
	"ContentPaddingAddition":   true,
	"RekeyAfterTime":           true,
	"RekeyTimeout":             true,
	"RejectAfterTime":          true,
	"KeepaliveTimeout":         true,
	"MaxHandshakeAttempts":     true,
	"RandomTrailers":           true,
	"DisableCookies":           true,
}

func isAwgStripKey(key string) bool {
	if awgStripKeys[key] {
		return true
	}
	// L1-L25 (контент-паддинг 3.x)
	if len(key) >= 2 && key[0] == 'L' {
		if n, err := strconv.Atoi(key[1:]); err == nil && n >= 1 && n <= 25 {
			return true
		}
	}
	return false
}

// StripAWGParams - снуть поля обфускации, оставив чистый WireGuard:
// комментарии (#/;) и все прочие строки сохраняются как есть.
func StripAWGParams(data []byte) []byte {
	lines := strings.Split(string(data), "\n")
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		trim := strings.TrimSpace(ln)
		if trim == "" || strings.HasPrefix(trim, "#") || strings.HasPrefix(trim, ";") {
			out = append(out, ln)
			continue
		}
		key := trim
		if i := strings.IndexByte(trim, '='); i >= 0 {
			key = strings.TrimSpace(trim[:i])
		}
		if isAwgStripKey(key) {
			continue
		}
		out = append(out, ln)
	}
	return []byte(strings.Join(out, "\n"))
}
