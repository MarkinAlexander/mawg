package singbox

import (
	"fmt"
	"os/exec"
	"strings"
)

type Engine struct {
	Bin     string
	Version string
	LX      bool
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
		return &Engine{Bin: bin, Version: ver, LX: strings.Contains(ver, "-lx.")}, nil
	}
	return nil, fmt.Errorf("бинар sing-box не найден (ищу в /opt/bin, /usr/bin, /usr/local/bin)")
}

func (e *Engine) Check(cfgPath string) error {
	out, err := exec.Command(e.Bin, "check", "-c", cfgPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("check не прошёл: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
