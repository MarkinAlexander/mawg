//go:build unix

package platform

import (
	"os"
	"strconv"
	"strings"
)

// MemAvailableBytes - доступная оперативная память из /proc/meminfo
// (MemAvailable; на старых ядрах его нет - берём MemFree+Buffers+Cached,
// как делает free(1)). 0 - не выяснили.
func MemAvailableBytes() int64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	var free, cached int64
	hasAvail := false
	var avail int64
	for _, line := range strings.Split(string(data), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(strings.TrimSpace(v))
		if len(fields) == 0 {
			continue
		}
		n, _ := strconv.ParseInt(fields[0], 10, 64)
		switch k {
		case "MemAvailable":
			avail, hasAvail = n, true
		case "MemFree":
			free = n
		case "Buffers", "Cached", "SReclaimable":
			cached += n
		}
	}
	if hasAvail {
		return avail * 1024
	}
	if free > 0 || cached > 0 {
		return (free + cached) * 1024
	}
	return 0
}
