//go:build !unix

package platform

// MemAvailableBytes - заглушка для локальной отладки на Windows.
func MemAvailableBytes() int64 { return 0 }
