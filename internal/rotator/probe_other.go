//go:build !linux

package rotator

import (
	"fmt"
	"time"
)

func httpProbe(device, target string, timeout time.Duration) (bool, int, error) {
	return false, 0, fmt.Errorf("http-проба поддерживается только на linux")
}
