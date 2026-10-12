package rotator

// перевыпуск Amnezia Free: identity привязана к устройству и выдаётся
// один конфиг - reissue сбрасывает identity и идёт в gateway заново
// (свежий конфиг может быть от другого сервера); пул пересоздаётся с теми
// же настройками и остаётся выключенным до ручной активации.

import (
	"context"
	"encoding/base64"
	"net/http"
	"testing"

	"mawg/internal/platform/fake"
	"mawg/internal/store"
)

type countTransport struct {
	rt    http.RoundTripper
	onReq func(*http.Request)
}

func (c *countTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.onReq(r)
	return c.rt.RoundTrip(r)
}

func TestFreePoolReissue(t *testing.T) {
	st, _ := store.Open(t.TempDir())
	e := New(st, fake.New(), nil)
	_, client := freeGateway(t, nil)
	var configs int
	client.HTTP.Transport = &countTransport{rt: client.HTTP.Transport, onReq: func(r *http.Request) {
		if r.URL.Path == "/v1/config" {
			configs++
		}
	}}
	e.PremiumClient = client

	p, err := e.CreateFreePool(context.Background(), "free", store.PoolSettings{
		Platform: store.PlatformKeenetic, EngineMode: "singbox", TunName: "tun1", ProbeHost: "1.1.1.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = p
	oldUUID := st.Free().UUID
	if configs != 1 {
		t.Fatalf("после создания gateway вызван %d раз, want 1", configs)
	}

	p2, err := e.ReissueFreePool(context.Background(), "free", nil)
	if err != nil {
		t.Fatalf("reissue: %v", err)
	}
	if !p2.Free || !p2.Disabled || len(p2.Configs) != 1 {
		t.Fatalf("пул после reissue: %+v", p2)
	}
	if p2.Settings.EngineMode != "singbox" || p2.Settings.TunName != "tun1" {
		t.Fatalf("настройки пула потеряны: %+v", p2.Settings)
	}
	if st.Free().UUID == "" || st.Free().UUID == oldUUID {
		t.Fatalf("идентичность не сменилась: %q", st.Free().UUID)
	}
	if configs != 2 {
		t.Fatalf("reissue не сходил в gateway за новым конфигом (%d вызовов)", configs)
	}
	b, err := base64.StdEncoding.DecodeString(st.Free().Private)
	if err != nil || len(b) != 32 {
		t.Fatalf("ключ новой идентичности невалиден: %v", err)
	}

	// не-Free пул - отказ
	if _, err := st.CreatePool("plain", store.PoolSettings{Platform: store.PlatformOpenwrt}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ReissueFreePool(context.Background(), "plain", nil); err == nil {
		t.Fatal("reissue прошёл для не-Free пула")
	}
	// неизвестный пул - отказ
	if _, err := e.ReissueFreePool(context.Background(), "нет", nil); err == nil {
		t.Fatal("reissue прошёл для несуществующего пула")
	}
}
