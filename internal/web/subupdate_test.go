package web

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mawg/internal/magitrickle"
	"mawg/internal/platform/fake"
	"mawg/internal/rotator"
	"mawg/internal/store"
)

// subBody - подписка из одного wireguard-узла с заданным эндпоинтом.
func subBody(endpoint string) string {
	link := "wireguard://" + fakeWGKey('c') + "@" + endpoint +
		"?address=10.0.0.7%2F32&keepalive=25&mtu=1280&presharedkey=" + fakeWGKey('d') +
		"&publickey=" + fakeWGKey('e') + "#node"
	return base64.StdEncoding.EncodeToString([]byte(link))
}

// newSubServer - сервер с работающим ротатором и фейковой платформой.
func newSubServer(t *testing.T) (*Server, *fake.Fake, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fb := fake.New()
	e := rotator.New(st, fb, magitrickle.New("http://127.0.0.1:1"))
	srv := New(st, e, fb, nil, "test", nil)
	ctx, cancel := context.WithCancel(context.Background())
	e.Start(ctx)
	t.Cleanup(cancel)
	return srv, fb, st
}

func TestSubUpdateNativePoolLifecycle(t *testing.T) {
	var body atomic.Value
	body.Store(subBody("203.0.113.30:35091"))
	sub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, body.Load().(string))
	}))
	defer sub.Close()

	srv, fb, st := newSubServer(t)
	fb.SetProbe("sub", true)
	if _, err := st.CreatePool("sub", store.PoolSettings{
		Platform: store.PlatformOpenwrt, Source: sub.URL,
		ProbeHost: "203.0.113.1", CheckIntervalSec: 3, FailThreshold: 3,
	}); err != nil {
		t.Fatal(err)
	}

	// первый тик: базовая точка без скачивания
	srv.subUpdateTick()
	if st.State("sub").SubRefreshAt.IsZero() {
		t.Fatal("базовая точка не поставлена")
	}
	if p, _ := st.Pool("sub"); len(p.Configs) != 0 {
		t.Fatalf("первый тик не должен качать: %+v", p.Configs)
	}

	// плановое обновление: прошло больше интервала (дефолт сутки)
	st.MutateState("sub", func(x *store.PoolState) { x.SubRefreshAt = time.Now().Add(-25 * time.Hour) })
	srv.subUpdateTick()
	p, _ := st.Pool("sub")
	if len(p.Configs) != 1 || p.Configs[0].Endpoint != "203.0.113.30:35091" {
		t.Fatalf("после планового обновления: %+v", p.Configs)
	}
	if st.State("sub").LastSubHash == "" {
		t.Fatal("хеш тела не записан")
	}

	// хеш-гейт: то же тело - пул не трогаем
	st.MutateState("sub", func(x *store.PoolState) { x.SubRefreshAt = time.Now().Add(-25 * time.Hour) })
	srv.subUpdateTick()
	p, _ = st.Pool("sub")
	if len(p.Configs) != 1 || p.Configs[0].Endpoint != "203.0.113.30:35091" {
		t.Fatalf("хеш-гейт должен оставить пул в покое: %+v", p.Configs)
	}
	if !subUpdateHasEvent(st, "тело не изменилось") {
		t.Fatal("нет события хеш-гейта")
	}

	// тело изменилось: конфиги заменяются
	body.Store(subBody("203.0.113.31:35092"))
	st.MutateState("sub", func(x *store.PoolState) { x.SubRefreshAt = time.Now().Add(-25 * time.Hour) })
	srv.subUpdateTick()
	p, _ = st.Pool("sub")
	if len(p.Configs) != 1 || p.Configs[0].Endpoint != "203.0.113.31:35092" {
		t.Fatalf("новое тело не применилось: %+v", p.Configs)
	}

	// плохое обновление: проба не прошла - откат на прежний конфиг
	fb.SetProbe("sub", false)
	body.Store(subBody("203.0.113.32:35093"))
	st.MutateState("sub", func(x *store.PoolState) { x.SubRefreshAt = time.Now().Add(-25 * time.Hour) })
	srv.subUpdateTick()
	p, _ = st.Pool("sub")
	if len(p.Configs) != 1 || p.Configs[0].Endpoint != "203.0.113.31:35092" {
		t.Fatalf("откат не сработал: %+v", p.Configs)
	}
	if st.State("sub").RefreshFails != 1 {
		t.Fatalf("счётчик неудач: %d", st.State("sub").RefreshFails)
	}
	if !subUpdateHasEvent(st, "откачено") {
		t.Fatal("нет события отката")
	}

	// триггер деградации: проба валится, ротатор копит ConsecFails -
	// цикл перепроверяет источник (тело то же -> гейт, пул не тронут)
	body.Store(subBody("203.0.113.31:35092"))
	fb.SetProbe("sub", false)
	st.MutateState("sub", func(x *store.PoolState) { x.ConsecFails = 3; x.LastFailRefresh = time.Time{} })
	srv.subUpdateTick()
	if st.State("sub").LastFailRefresh.IsZero() {
		t.Fatal("деградация не привела к перепроверке")
	}
	if !subUpdateHasEvent(st, "пул деградировал") {
		t.Fatal("нет события деградации")
	}
	if !subUpdateHasEvent(st, "тело не изменилось") {
		t.Fatal("деградационное обновление должно пройти хеш-гейт")
	}

	// ручное обновление фиксирует хеш и снимает счётчик неудач
	srv.recordSourceRefresh("sub", "hash-manual", 12)
	if st.State("sub").RefreshFails != 0 {
		t.Fatalf("счётчик после ручного обновления: %d", st.State("sub").RefreshFails)
	}
	if st.State("sub").SubIntervalH != 12 {
		t.Fatalf("интервал из заголовка не записан: %v", st.State("sub").SubIntervalH)
	}
}

func subUpdateHasEvent(st *store.Store, sub string) bool {
	for _, e := range st.Events() {
		if strings.Contains(e.Message, sub) && e.Kind == "subupdate" {
			return true
		}
	}
	return false
}
