//go:build linux

package rotator

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func bindToDevice(device string) func(string, string, syscall.RawConn) error {
	return func(network, address string, c syscall.RawConn) error {
		var opErr error
		err := c.Control(func(fd uintptr) {
			if err := unix.BindToDevice(int(fd), device); err != nil {
				opErr = fmt.Errorf("bind to %s: %w", device, err)
			}
		})
		if err != nil {
			return err
		}
		return opErr
	}
}

func httpProbe(device, target string, timeout time.Duration) (ok bool, rttMs int, err error) {
	dialer := &net.Dialer{Timeout: timeout, Control: bindToDevice(device)}
	base := dialer.DialContext
	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				safe, err := safeAddr(host, port)
				if err != nil {
					return nil, err
				}
				return base(ctx, network, safe)
			},
			DisableKeepAlives: true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return fmt.Errorf("redirects disabled for probe")
		},
	}
	start := time.Now()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
	if err != nil {
		return false, 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, 0, nil
	}
	resp.Body.Close()
	return true, int(time.Since(start).Milliseconds()), nil
}
