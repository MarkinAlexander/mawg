//go:build unix

package platform

import "syscall"

// DiskFreeBytes - свободное место (байты), доступное обычному пользователю.
func DiskFreeBytes(path string) int64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0
	}
	return int64(st.Bavail) * int64(st.Bsize)
}
