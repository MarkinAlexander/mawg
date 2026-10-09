//go:build !unix

package platform

// DiskFreeBytes - заглушка для Windows-сборки локальной отладки.
func DiskFreeBytes(path string) int64 { return 0 }
