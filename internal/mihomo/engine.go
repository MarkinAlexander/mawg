package mihomo

import (
	"fmt"
	"os/exec"
	"strings"
)

// Engine - найденный бинарь mihomo (Clash.Meta). Своего «lx»-профиля нет:
// ядро ставится из релизов MetaCubeX/mihomo как есть.
type Engine struct {
	Bin     string
	Version string
}

var binCandidates = []string{"/opt/bin/mihomo", "/usr/bin/mihomo", "/usr/local/bin/mihomo"}

func Detect() (*Engine, error) {
	for _, bin := range binCandidates {
		ver, err := MihomoVersion(bin, nil)
		if err != nil {
			continue
		}
		return &Engine{Bin: bin, Version: ver}, nil
	}
	return nil, fmt.Errorf("бинар mihomo не найден (ищу в /opt/bin, /usr/bin, /usr/local/bin; установить можно в «Система → Ядра VPN»)")
}

// MihomoVersion запускает `bin -v` и достаёт версию: первая строка имеет вид
// «Mihomo Meta v1.19.33 linux 386 with go1.2x …».
func MihomoVersion(bin string, run func(argv0 string, args ...string) ([]byte, error)) (string, error) {
	if run == nil {
		run = func(argv0 string, args ...string) ([]byte, error) {
			return exec.Command(argv0, args...).Output()
		}
	}
	out, err := run(bin, "-v")
	if err != nil {
		return "", err
	}
	line := strings.SplitN(string(out), "\n", 2)[0]
	fields := strings.Fields(line)
	for _, f := range fields {
		if len(f) > 1 && f[0] == 'v' && strings.Contains(f, ".") {
			return f, nil
		}
	}
	return "", fmt.Errorf("не удалось разобрать вывод -v: %s", strings.TrimSpace(line))
}

// Check проверяет конфиг тестовым запуском `mihomo -t` (homeDir - каталог
// состояния: там же ядро ищет свои файлы при нормальном старте).
func (e *Engine) Check(cfgPath, homeDir string) error {
	out, err := exec.Command(e.Bin, "-t", "-d", homeDir, "-f", cfgPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("проверка конфига (mihomo -t) не прошла: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
