package links

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

const fetchLimit = 8 << 20

type Fetched struct {
	URL         string
	ContentType string
	BodySHA     string // sha256 тела: гейт «ничего нового» автообновления
	Sub         SubInfo
	Result      Result
	// HwidLimit - панель (Remnawave x-hwid) сообщила, что лимит устройств
	// подписки достигнут: узлы будут заглушками, причина - в warnings
	HwidLimit bool
}

// DeviceHeaders - представление установки для подписок с лимитом
// устройств (Remnawave hwid): x-hwid обязателен, ось/модель помогают
// провайдеру опознать устройство в карточке пользователя.
type DeviceHeaders struct {
	Hwid  string
	OS    string
	Model string
}

func Fetch(ctx context.Context, rawURL string, dev DeviceHeaders) (*Fetched, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("подписка должна быть http/https, получено %q", u.Scheme)
	}
	client := clientFor(u.Hostname())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "mawg/1.0")
	if dev.Hwid != "" {
		req.Header.Set("x-hwid", dev.Hwid)
		if dev.OS != "" {
			req.Header.Set("x-device-os", dev.OS)
		}
		if dev.Model != "" {
			req.Header.Set("x-device-model", dev.Model)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, fetchLimit))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("подписка ответила %d", resp.StatusCode)
	}
	f := &Fetched{
		URL:         rawURL,
		ContentType: resp.Header.Get("Content-Type"),
		BodySHA:     fmt.Sprintf("%x", sha256.Sum256(body)),
		HwidLimit:   resp.Header.Get("x-hwid-max-devices-reached") == "true" || resp.Header.Get("x-hwid-limit") == "true",
	}
	if ui := resp.Header.Get(subscriptionUserinfo); ui != "" {
		f.Sub = ParseUserinfo(ui)
	}
	if iv, ok := ParseUpdateInterval(resp.Header.Get(profileUpdateInterval)); ok {
		f.Sub.UpdateIntervalHours = iv
	}
	f.Result = ParseSubscription(u.Hostname(), string(body))
	return f, nil
}

func clientFor(host string) *http.Client {
	if net.ParseIP(host) == nil {
		return &http.Client{Timeout: 30 * time.Second}
	}
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify:    true,
				VerifyPeerCertificate: parseLeafOnly(host),
			},
		},
	}
}

// панели на IP не имеют доверяемой цепочки (self-signed, приватный CA или
// серт на IP-SAN) - серт только разбирается; для доменов верификация полная
func parseLeafOnly(host string) func([][]byte, [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return fmt.Errorf("нет сертификата")
		}
		_, err := x509.ParseCertificate(rawCerts[0])
		return err
	}
}
