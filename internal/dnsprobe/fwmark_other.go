//go:build !linux

package dnsprobe

import "syscall"

// dialControl - заглушка не-Linux: SO_MARK нет, проба идёт без метки
// (на роутерах mawg всегда Linux - метка там ставится, см. fwmark_linux.go).
func dialControl(network, address string, c syscall.RawConn) error {
	_ = network
	_ = address
	_ = c
	return nil
}
