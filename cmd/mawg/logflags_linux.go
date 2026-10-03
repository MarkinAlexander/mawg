//go:build linux

package main

import "syscall"

// logExtraFlags: O_NOFOLLOW против symlink-атаки на файл лога в /tmp.
func logExtraFlags() int {
	return syscall.O_NOFOLLOW
}
