//go:build linux

package rotator

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
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

// httpProbe: дозвон через заданный интерфейс (SO_BINDTODEVICE) по очереди
// ко всем публичным адресам цели (v4 впереди - на интерфейсах без v6 дозвон
// по AAAA всегда падает, хотя curl выживает перебором семей). Ошибка
// возвращается вызывающему: "нет ответа" без причины не diagnosable.
func httpProbe(device, target string, timeout time.Duration) (ok bool, rttMs int, err error) {
	u, err := url.Parse(target)
	if err != nil {
		return false, 0, err
	}
	host, port := u.Hostname(), u.Port()
	if port == "" {
		if u.Scheme == "http" {
			port = "80"
		} else {
			port = "443"
		}
	}
	addrs, err := safeAddrs(host, port)
	if err != nil {
		return false, 0, err
	}
	dialer := &net.Dialer{Timeout: timeout, Control: bindToDevice(device)}
	start := time.Now()
	var lastErr error
	for _, addr := range addrs {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
		if err != nil {
			return false, 0, err
		}
		client := &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, network, a string) (net.Conn, error) {
					return dialer.DialContext(ctx, "tcp", addr)
				},
				DisableKeepAlives: true,
			},
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return fmt.Errorf("проба не следует редиректам")
			},
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		resp.Body.Close()
		return true, int(time.Since(start).Milliseconds()), nil
	}
	return false, 0, lastErr
}
