//go:build !linux

package singbox

// KillProcs - заглушка не-Linux сборки (kill живёт на роутере).
func KillProcs(ps []ProcInfo) int { return 0 }
