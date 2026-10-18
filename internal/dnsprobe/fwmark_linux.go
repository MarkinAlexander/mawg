//go:build linux

package dnsprobe

import "syscall"

// fwmarkMagi - метка «своих» пакетов MagiTrickle (0x4d616769 = "Magi"):
// цепочка перехвата MT_DNSOR делает по ней return, поэтому UDP-проба
// с такой меткой уходит наружу напрямую и меряет РЕАЛЬНЫЙ сервер, а не
// цепочку MT (живой кейс: «DNS провайдера 8.8.8.8» при живом перехвате
// отвечал цепочкой, и подмену на UDP было не увидеть - слово владельца
// 2026-10-18 про примитивный DNS-чекер). mawg работает root'ом -
// SO_MARK доступен; не вышел - метка молча не ставится, проба как раньше.
const fwmarkMagi = 0x4d616769

// dialControl - хук net.Dialer.Control: ставит SO_MARK до привязки сокета.
func dialControl(network, address string, c syscall.RawConn) error {
	_ = network
	_ = address
	c.Control(func(fd uintptr) {
		_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_MARK, fwmarkMagi)
	})
	return nil
}
