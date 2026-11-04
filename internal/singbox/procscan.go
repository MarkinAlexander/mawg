package singbox

// Сироты движков (112-док 3.6/4.4): после потери pid (рестарт mawg при
// живом движке, битый run.pid) старый процесс ядра продолжал жить со
// старым конфигом и держал tun-устройства/порты - новый процесс получал
// TUNSETIFF busy и «не запускался» (живой кейс 04.11 с двумя mihomo).
// Процессы ищем по /proc: comm + cmdline. Наш экземпляр движка отличается
// от чужих аргументами командной строки (путь конфига mawg / cache-каталог).
// Файл кроссплатформенный (работа с /proc через подмены в тестах), kill
// делают только linux-менеджеры.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// procDir - каталог процессов (подмена в тестах).
var procDir = "/proc"

// readlinkFn - чтение симлинка (подмена в тестах: symlink на Windows
// требует привилегий).
var readlinkFn = os.Readlink

// ProcInfo - живой процесс по данным /proc.
type ProcInfo struct {
	PID     int
	Comm    string // /proc/<pid>/comm: имя бинаря
	Exe     string // /proc/<pid>/exe (симлинк)
	Cmdline []string
}

func (p ProcInfo) String() string {
	if len(p.Cmdline) > 0 {
		return strings.Join(p.Cmdline, " ")
	}
	return p.Comm
}

// procsComm - процессы с comm == name (comm - дешёвый фильтр, имя бинаря
// без пути; Кинетик ps w показывает так же).
func procsComm(name string) []ProcInfo {
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return nil
	}
	var out []ProcInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join(procDir, e.Name(), "comm"))
		if err != nil || strings.TrimSpace(string(b)) != name {
			continue
		}
		info := ProcInfo{PID: pid, Comm: name}
		info.Exe, _ = readlinkFn(filepath.Join(procDir, e.Name(), "exe"))
		if cb, err := os.ReadFile(filepath.Join(procDir, e.Name(), "cmdline")); err == nil {
			info.Cmdline = strings.Split(strings.TrimRight(string(cb), "\x00"), "\x00")
		}
		out = append(out, info)
	}
	return out
}

// OurEngineProcs - живые процессы ядра name, чья командная строка несёт
// marker (наш конфиг / cache-каталог): наш экземпляр движка и его
// осиротевшие предшественники. excludePID не считается (обычно свой pid).
func OurEngineProcs(name, marker string, excludePID int) []ProcInfo {
	var out []ProcInfo
	for _, p := range procsComm(name) {
		if p.PID == excludePID || p.PID <= 1 {
			continue
		}
		for _, arg := range p.Cmdline {
			if arg == marker {
				out = append(out, p)
				break
			}
		}
	}
	return out
}

// BinProcs - процессы, исполняющие бинарь по пути exe (подмена exe не
// делается: ручные запуски из /tmp различимы по полному пути). Замена
// файла работающего ядра оставила бы сироту с удалённым образом - таких
// гасим перед конвейером замены (112-док 4.2).
func BinProcs(exe string) []ProcInfo {
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return nil
	}
	var out []ProcInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 1 {
			continue
		}
		target, err := readlinkFn(filepath.Join(procDir, e.Name(), "exe"))
		if err != nil || target != exe {
			continue
		}
		info := ProcInfo{PID: pid, Exe: target}
		if b, err := os.ReadFile(filepath.Join(procDir, e.Name(), "comm")); err == nil {
			info.Comm = strings.TrimSpace(string(b))
		}
		out = append(out, info)
	}
	return out
}

// TunHolderProcs - процессы, держащие /dev/net/tun (диагностика занятых
// tun-устройств: кто мешает TUNSETIFF). Наш pid исключаем.
func TunHolderProcs(excludePID int) []ProcInfo {
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return nil
	}
	var out []ProcInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == excludePID || pid <= 1 {
			continue
		}
		fds, err := os.ReadDir(filepath.Join(procDir, e.Name(), "fd"))
		if err != nil {
			continue
		}
		for _, fd := range fds {
			target, err := readlinkFn(filepath.Join(procDir, e.Name(), "fd", fd.Name()))
			if err != nil || target != "/dev/net/tun" {
				continue
			}
			info := ProcInfo{PID: pid}
			if b, err := os.ReadFile(filepath.Join(procDir, e.Name(), "comm")); err == nil {
				info.Comm = strings.TrimSpace(string(b))
			}
			out = append(out, info)
			break
		}
	}
	return out
}

// DescribeProcs - компактная сводка «pid 123 (comm)» для журнала/ошибок.
func DescribeProcs(ps []ProcInfo) string {
	parts := make([]string, 0, len(ps))
	for _, p := range ps {
		comm := p.Comm
		if comm == "" {
			comm = "?"
		}
		parts = append(parts, "pid "+strconv.Itoa(p.PID)+" ("+comm+")")
	}
	return strings.Join(parts, ", ")
}
