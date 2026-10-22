package magitrickle

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// Окно мутации каскадов висит на клиенте (BeforeWrite/AfterWrite) и
// открывается только при ФАКТИЧЕСКОЙ записи: no-op-чтение с мутацией
// «без изменений» хуки не снимает, запись снимает и возвращает, при
// ошибке PUT окно закрывается всё равно.
func TestClientWriteWindow(t *testing.T) {
	SetShadowStorage(nil, nil)
	srv, st := shadowServer(t)
	var bw, aw int32
	c := New(srv.URL)
	c.BeforeWrite = func() { atomic.AddInt32(&bw, 1) }
	c.AfterWrite = func() { atomic.AddInt32(&aw, 1) }

	// no-op: мутация без изменений - ни снятия хуков, ни окна
	if err := c.MutateGroups(context.Background(), func(groups []Group) bool { return false }); err != nil {
		t.Fatal(err)
	}
	if n := atomic.LoadInt32(&bw); n != 0 {
		t.Fatalf("no-op запись: BeforeWrite вызван %d раз, хочу 0", n)
	}
	if n := atomic.LoadInt32(&aw); n != 0 {
		t.Fatalf("no-op запись: AfterWrite вызван %d раз, хочу 0", n)
	}

	// фактическая запись: окно открылось перед PUT и закрылось после
	if err := c.MutateGroups(context.Background(), func(groups []Group) bool {
		groups[0].Name = groups[0].Name + "!"
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if n := atomic.LoadInt32(&bw); n != 1 {
		t.Fatalf("запись: BeforeWrite вызван %d раз, хочу 1", n)
	}
	if n := atomic.LoadInt32(&aw); n != 1 {
		t.Fatalf("запись: AfterWrite вызван %d раз, хочу 1", n)
	}
	if got := st.snapshot()[0].Name; got != "a!" {
		t.Fatalf("запись не дошла до MT: имя %q", got)
	}
}

// ошибка PUT: окно закрывается всё равно (AfterWrite в defer), хуки
// вернутся SyncHooks'ом сразу после записи, а не на минутном тике
func TestClientWriteWindowOnPutError(t *testing.T) {
	SetShadowStorage(nil, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		jsonGroups(w)
	}))
	defer srv.Close()
	var bw, aw int32
	c := New(srv.URL)
	c.BeforeWrite = func() { atomic.AddInt32(&bw, 1) }
	c.AfterWrite = func() { atomic.AddInt32(&aw, 1) }

	if err := c.MutateGroups(context.Background(), func(groups []Group) bool {
		groups[0].Name = groups[0].Name + "!"
		return true
	}); err == nil {
		t.Fatal("PUT 500 должен возвращать ошибку")
	}
	if n := atomic.LoadInt32(&bw); n != 1 {
		t.Fatalf("BeforeWrite на ошибке: %d, хочу 1", n)
	}
	if n := atomic.LoadInt32(&aw); n != 1 {
		t.Fatalf("AfterWrite на ошибке: %d, хочу 1 (окно закрывается в defer)", n)
	}
}

func jsonGroups(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"groups":[{"id":"g1","name":"a","interface":"nwg1","enable":true}]}`))
}
