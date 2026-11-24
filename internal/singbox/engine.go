package singbox

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type Engine struct {
	Bin     string
	Version string
	// LX - сборка нашего форка sing-box-lx (версия "...-lx.N").
	LX bool
	// Extended - сборка форка sing-box-extended (версия "...-extended-N.N.N").
	Extended bool
	// SourceID - источник ядра по версии: lx | extended | upstream.
	SourceID string
}

var binCandidates = []string{"/opt/bin/sing-box", "/usr/bin/sing-box", "/usr/local/bin/sing-box"}

// BinPresent - первый из кандидатов, кто есть на диске, даже если не
// запускается: нечитаемая версия - не «не установлен».
func BinPresent() (string, bool) {
	for _, bin := range binCandidates {
		if _, err := os.Stat(bin); err == nil {
			return bin, true
		}
	}
	return "", false
}

func Detect() (*Engine, error) {
	for _, bin := range binCandidates {
		out, err := exec.Command(bin, "version").Output()
		if err == nil {
			line := strings.SplitN(string(out), "\n", 2)[0]
			ver := strings.TrimSpace(strings.TrimPrefix(line, "sing-box version"))
			if ver != "" {
				return &Engine{Bin: bin, Version: ver, LX: HasLXSuffix(ver), Extended: HasExtSuffix(ver), SourceID: DetectSourceID(ver)}, nil
			}
			continue
		}
		// бинар есть, но version-запрос не прошёл: на роутере без памяти
		// зонд запускает бинарь и его убивает OOM (жалоба 24.11) - это не
		// «не найден»
		if _, serr := os.Stat(bin); serr == nil {
			return nil, fmt.Errorf("бинар %s на месте, но не ответил на version: %w", bin, err)
		}
	}
	return nil, fmt.Errorf("бинар sing-box не найден (ищу в /opt/bin, /usr/bin, /usr/local/bin)")
}

// FullCaps - легаси-гейт «расширенное ядро» (lx/extended); новые гейты -
// по матрице возможностей Engine.Caps() (112-док 2.2). Оставлен для
// системной проверки зависимостей (provision).
func (e Engine) FullCaps() bool { return e.LX || e.Extended }

func DetectSourceID(ver string) string {
	switch {
	case HasLXSuffix(ver):
		return SourceLXID
	case HasExtSuffix(ver):
		return SourceExtID
	default:
		return SourceUpID
	}
}

func (e *Engine) Check(cfgPath string) error {
	out, err := exec.Command(e.Bin, "check", "-c", cfgPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("check не прошёл: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
