package singbox

import (
	"fmt"
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

func Detect() (*Engine, error) {
	for _, bin := range binCandidates {
		out, err := exec.Command(bin, "version").Output()
		if err != nil {
			continue
		}
		line := strings.SplitN(string(out), "\n", 2)[0]
		ver := strings.TrimSpace(strings.TrimPrefix(line, "sing-box version"))
		if ver == "" {
			continue
		}
		return &Engine{Bin: bin, Version: ver, LX: HasLXSuffix(ver), Extended: HasExtSuffix(ver), SourceID: DetectSourceID(ver)}, nil
	}
	return nil, fmt.Errorf("бинар sing-box не найден (ищу в /opt/bin, /usr/bin, /usr/local/bin)")
}

// FullCaps - ядро понимает расширения lx (xhttp, vless-шифрование,
// AWG-поля wireguard): сборки lx и extended. У апстрима такие узлы
// скрываются с подсказкой поставить расширенное ядро.
func (e Engine) FullCaps() bool { return e.LX || e.Extended }

// DetectSourceID - источник по форме версии.
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
