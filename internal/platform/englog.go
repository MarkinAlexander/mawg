package platform

// Логи движков (sing-box/mihomo): общий каталог, ротация по размеру,
// голова/хвост/первая ошибка. Диагностика «движок не запущен» без причины
// (кейс 04.11: FATAL уходил в stdout init-скрипта и терялся) - теперь
// вывод ядра живёт в файле, ограниченном по размеру (112-док 3.6/3.7).

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// EngineLogMaxBytes - порог ротации лога движка: одна живая копия + одна
// .old, историю не копим - место на роутерах дорого.
const EngineLogMaxBytes = 512 * 1024

// EngineLogPath - путь лога движка: /opt/var/log/mawg на Entware (Keenetic,
// USB-диск), /var/log/mawg на OpenWrt (tmpfs - не изнашивает флешку, но
// живёт до перезагрузки); если каталог не создаётся - рядом с конфигом
// движка (dir). name - "singbox" | "mihomo".
func EngineLogPath(dir, name string) string {
	for _, base := range []string{"/opt/var/log/mawg", "/var/log/mawg"} {
		if err := os.MkdirAll(base, 0o755); err == nil {
			return filepath.Join(base, name+".log")
		}
	}
	return filepath.Join(dir, name+".log")
}

// RotateIfLarge - перед открытием с O_TRUNC: большой лог уезжает в .old,
// предыдущая .old уходит (размер ограничен двумя файлами).
func RotateIfLarge(path string, maxBytes int64) {
	st, err := os.Stat(path)
	if err != nil || st.Size() <= maxBytes {
		return
	}
	_ = os.Remove(path + ".old")
	_ = os.Rename(path, path+".old")
}

// LogHead - первые непустые строки лога (ошибка старта - в начале файла).
func LogHead(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024)
	var out []string
	for sc.Scan() && len(out) < n {
		if s := strings.TrimSpace(sc.Text()); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// LogTail - последние n строк лога (живой процесс пишет в конец).
func LogTail(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024)
	ring := make([]string, 0, n)
	for sc.Scan() {
		s := strings.TrimSpace(sc.Text())
		if s == "" {
			continue
		}
		if len(ring) == n {
			ring = ring[1:]
		}
		ring = append(ring, s)
	}
	return ring
}

// LogFirstError - первая строка с маркером ошибки/фатала (sing-box и
// mihomo пишут ERROR/FATAL/PANIC); "" если в голове лога ошибок нет.
func LogFirstError(path string) string {
	for _, line := range LogHead(path, 40) {
		up := strings.ToUpper(line)
		if strings.Contains(up, "ERROR") || strings.Contains(up, "FATAL") || strings.Contains(up, "PANIC") {
			return line
		}
	}
	return ""
}
