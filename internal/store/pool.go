package store

import (
	"strings"
)

func (p Pool) EligibleConfigs() []ManagedConfig {
	var out []ManagedConfig
	for _, c := range p.Configs {
		if c.Enabled {
			out = append(out, c)
		}
	}
	return out
}

func (p Pool) ConfigByFile(file string) (ManagedConfig, bool) {
	for _, c := range p.Configs {
		if c.File == file {
			return c, true
		}
	}
	return ManagedConfig{}, false
}

func (p Pool) IndexByFile(file string) int {
	for i, c := range p.Configs {
		if c.File == file {
			return i
		}
	}
	return -1
}

func (p Pool) DeviceName() string {
	if p.Settings.Platform == PlatformKeenetic && p.Settings.KeeneticSlot != "" {
		return "nwg" + strings.TrimPrefix(strings.ToLower(p.Settings.KeeneticSlot), "wireguard")
	}
	return p.Name
}
