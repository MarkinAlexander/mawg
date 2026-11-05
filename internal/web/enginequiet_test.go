package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"mawg/internal/magitrickle"
	"mawg/internal/platform/fake"
	"mawg/internal/rotator"
	"mawg/internal/store"
)

func newQuietServer(t *testing.T, mtURL string) *Server {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fb := fake.New()
	e := rotator.New(st, fb, magitrickle.New(mtURL))
	srv := New(st, e, fb, magitrickle.New(mtURL), "test", nil)
	t.Cleanup(func() { magitrickle.SetShadowStorage(nil, nil) })
	return srv
}

func mustEnginePool(t *testing.T, s *Server, name, tun string) store.Pool {
	t.Helper()
	if _, err := s.store.CreatePool(name, store.PoolSettings{
		Platform: store.PlatformOpenwrt, EngineMode: engineMode, TunName: tun,
	}); err != nil {
		t.Fatal(err)
	}
	p, ok := s.store.Pool(name)
	if !ok {
		t.Fatal("пул не создан")
	}
	return p
}

// живой МТ со списком групп, среди которых нет tun5 (наш «спящий» пул)
func newMTWithoutTun5(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/groups" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"groups":[{"id":"a","name":"telegram","enable":true,"interface":"tun7"}]}`)
	}))
	t.Cleanup(ts.Close)
	return ts
}

// unrouted-пул (никаких групп/каскадов/бандлов на его tun) не должен
// требовать пересборки движка - иначе мёртвая подписка такого пула рвёт
// сессии соседей по общему ядру (живой кейс trojan-test/телеграм).
func TestEnginePoolRouted(t *testing.T) {
	// тень - пакетная глобаль: ранее прошедшие тесты могли её населить,
	// SetShadowStorage подменяет loader, но не перечитывает непустую
	// mtShadow - сбрасываем явно, кейс «пустая тень -> живой список»
	// должен идти от чистого листа
	magitrickle.SetShadowStorage(nil, nil)
	mt := newMTWithoutTun5(t)
	s := newQuietServer(t, mt.URL)

	p := mustEnginePool(t, s, "solo", "tun5")
	if s.enginePoolRouted(p) {
		t.Fatal("пул без привязок должен быть unrouted")
	}

	// группа МТ на tun пула (пусть и выключенная) - маршрутизируется
	magitrickle.SetShadowStorage(nil, nil) // непустая mtShadow не перечитывается сама
	magitrickle.SetShadowStorage(func() []magitrickle.Group {
		return []magitrickle.Group{
			{ID: "1", Name: "telegram", Interface: "tun7"},
			{ID: "2", Name: "резерв", Interface: "tun5", Enable: false},
		}
	}, nil)
	if !s.enginePoolRouted(p) {
		t.Fatal("выключенная группа на tun - уже привязка (включение не должно застать старые узлы)")
	}
	magitrickle.SetShadowStorage(nil, nil)

	// чужой умный каскад через пул (Via - имена пулов)
	if _, err := s.store.CreatePool("main", store.PoolSettings{
		Platform: store.PlatformOpenwrt, EngineMode: engineMode, TunName: "tun1",
		SmartCascade: &store.SmartCascadeSettings{Via: []string{"solo"}},
	}); err != nil {
		t.Fatal(err)
	}
	if !s.enginePoolRouted(p) {
		t.Fatal("пул в чужом Via - маршрутизируется")
	}

	// бандл устройств на интерфейс пула
	s2 := newQuietServer(t, mt.URL)
	p2 := mustEnginePool(t, s2, "solo", "tun5")
	if s2.enginePoolRouted(p2) {
		t.Fatal("чистый пул - unrouted")
	}
	if _, err := s2.store.CreateBundle(store.Bundle{Name: "tv", Members: []string{"tun5"}}); err != nil {
		t.Fatal(err)
	}
	if !s2.enginePoolRouted(p2) {
		t.Fatal("бандл на интерфейс - маршрутизируется")
	}

	// пустой tun и недоступный МТ - fail-safe «используется»
	if !s2.enginePoolRouted(mustEnginePool(t, s2, "notun", "")) {
		t.Fatal("пустой tun - fail-safe routed")
	}
	sDead := newQuietServer(t, "http://127.0.0.1:1")
	if !sDead.enginePoolRouted(mustEnginePool(t, sDead, "deadmt", "tun9")) {
		t.Fatal("МТ недоступен - fail-safe routed")
	}
}

// провал планового обновления не должен открывать ретраи каждую минуту
func TestSubRetryGated(t *testing.T) {
	now := time.Now()
	if subRetryGated(&store.PoolState{}, now) {
		t.Fatal("без провалов гейта быть не должно")
	}
	st := &store.PoolState{RefreshFails: 1, LastFailRefresh: now.Add(-time.Minute)}
	if !subRetryGated(st, now) {
		t.Fatal("недавний провал - под кулдауном")
	}
	st.LastFailRefresh = now.Add(-subFailCooldown - time.Second)
	if subRetryGated(st, now) {
		t.Fatal("кулдаун истёк - ретрай разрешён")
	}
	st.RefreshFails = 0
	st.LastFailRefresh = now
	if subRetryGated(st, now) {
		t.Fatal("после успеха счётчики сняты - гейта нет")
	}
}
