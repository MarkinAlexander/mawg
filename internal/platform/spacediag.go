package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Диагностика тесного места для сообщений об ошибках установки ядер
// (жалоба 11.2026: «распаковка не удалась: exit status 2» без единой
// цифры - пользователь не понимал, что у него кончилась tmpfs). /tmp на
// Keenetic/OpenWrt - tmpfs в оперативке: её съедают скачанные архивы и
// упавшие распаковки, перезагрузка роутера очищает.

func spacediagMiB(n int64) string { return fmt.Sprintf("%.0f МБ", float64(n)/float64(1<<20)) }

// BigFilesTop - крупнейшие файлы каталога (не рекурсивно) от limit байт,
// для подсказки «что занимает /tmp». Формат элементов: «30 МБ имя».
func BigFilesTop(dir string, limit int64, n int) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type sized struct {
		name string
		size int64
	}
	var files []sized
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || info.IsDir() {
			continue
		}
		if info.Size() >= limit {
			files = append(files, sized{e.Name(), info.Size()})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].size > files[j].size })
	var out []string
	for i, f := range files {
		if i >= n {
			break
		}
		out = append(out, spacediagMiB(f.size)+" "+f.name)
	}
	return out
}

// SpaceDiag - одной строкой: свободное место в каталоге, доступная RAM и
// крупнейшие файлы. Пустая строка - ничего не выяснили (не unix и т.п.).
func SpaceDiag(dir string) string {
	var parts []string
	if free := DiskFreeBytes(dir); free > 0 {
		parts = append(parts, fmt.Sprintf("в %s свободно %s", dir, spacediagMiB(free)))
	}
	if ram := MemAvailableBytes(); ram > 0 {
		parts = append(parts, "доступно RAM "+spacediagMiB(ram))
	}
	if top := BigFilesTop(dir, 1<<20, 3); len(top) > 0 {
		parts = append(parts, "крупное в "+filepath.Base(dir)+": "+strings.Join(top, ", "))
	}
	return strings.Join(parts, "; ")
}
